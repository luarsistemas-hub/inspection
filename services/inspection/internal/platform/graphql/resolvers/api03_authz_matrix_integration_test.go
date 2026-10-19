//go:build integration

package resolvers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
	"inspection/libs/identity"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/security"
)

func TestAPI03UnauthenticatedOperationsIT399(t *testing.T) {
	for _, operation := range api03FailureOperations() {
		t.Run(operation.name, func(t *testing.T) {
			fixture := newAPI03FailureFixture(t, operation.name)
			response := executeAPI03WithoutCredentials(t, fixture, operation.query)
			assertAPI03Rejected(t, response, "UNAUTHENTICATED", operation.successPayload)
		})
	}
}

func TestAPI03ExternalOperationsRejectMissingOrIncorrectCSRFIT409(t *testing.T) {
	for _, operation := range api03FailureOperations() {
		for _, csrf := range []string{"", "incorrect-csrf"} {
			t.Run(operation.name+"/csrf-set="+strconv.FormatBool(csrf != ""), func(t *testing.T) {
				fixture := newAPI03FailureFixture(t, operation.name)
				var before database.ProcessingAcceptance
				if operation.name == "acceptProcessing" {
					if err := fixture.harness.WithinTenant(context.Background(), fixture.tenantID, func(tx *gorm.DB) error {
						return tx.Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).Take(&before).Error
					}); err != nil {
						t.Fatal(err)
					}
				}
				credentials := fixture.credentials
				credentials.CSRFToken = csrf
				response := executeAPI03WithCredentialsValue(t, fixture, operation.query, credentials)
				if strings.Contains(response, operation.successPayload) || !strings.Contains(response, "errors") {
					t.Fatalf("%s proceeded with CSRF token %q: %s", operation.name, csrf, response)
				}
				if operation.name == "beginVideoCapture" {
					var attempts int64
					if err := fixture.harness.WithinTenant(context.Background(), fixture.tenantID, func(tx *gorm.DB) error {
						return tx.Model(&database.VideoAttempt{}).Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).Count(&attempts).Error
					}); err != nil {
						t.Fatal(err)
					}
					if attempts != 0 {
						t.Fatalf("invalid CSRF created %d video attempts", attempts)
					}
				}
				if operation.name == "acceptProcessing" {
					if err := fixture.harness.WithinTenant(context.Background(), fixture.tenantID, func(tx *gorm.DB) error {
						var after database.ProcessingAcceptance
						if err := tx.Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).Take(&after).Error; err != nil {
							return err
						}
						if after.ID != before.ID || !after.AcceptedAt.Equal(before.AcceptedAt) {
							t.Fatalf("invalid CSRF changed processing acceptance: before=%+v after=%+v", before, after)
						}
						return nil
					}); err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	}
}

func TestAPI03InternalPrincipalWithoutParticipantSessionIsForbiddenIT400(t *testing.T) {
	for _, operation := range api03FailureOperations() {
		t.Run(operation.name, func(t *testing.T) {
			fixture := newAPI03FailureFixture(t, operation.name)
			var before database.ProcessingAcceptance
			if err := fixture.harness.WithinTenant(context.Background(), fixture.tenantID, func(tx *gorm.DB) error {
				return tx.Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).First(&before).Error
			}); err != nil {
				t.Fatal(err)
			}
			response := executeAPI03AsInternalPrincipal(t, fixture, operation.query)
			assertAPI03Rejected(t, response, "FORBIDDEN", operation.successPayload)
			if err := fixture.harness.WithinTenant(context.Background(), fixture.tenantID, func(tx *gorm.DB) error {
				var after database.ProcessingAcceptance
				if err := tx.Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).First(&after).Error; err != nil {
					return err
				}
				if after.ID != before.ID || !after.AcceptedAt.Equal(before.AcceptedAt) {
					t.Fatalf("internal principal changed processing acceptance: before=%+v after=%+v", before, after)
				}
				var attempts int64
				if err := tx.Model(&database.VideoAttempt{}).Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).Count(&attempts).Error; err != nil {
					return err
				}
				if attempts != 0 {
					t.Fatalf("internal principal created attempts: %d", attempts)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAPI03ForeignReplacementMediaIsNotFoundIT401(t *testing.T) {
	fixture := newAPI03FailureFixture(t, "beginVideoCapture")
	other, err := fixture.harness.SeedTask06Fixture(context.Background(), harness.Task06FixtureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	foreignMedia := database.MediaObject{ID: identity.NewID(), TenantID: other.TenantID, ResponsibilityID: other.ResponsibilityID, ObjectKey: "it401/foreign-video", ContentType: "video/mp4", SHA256: strings.Repeat("b", 64), SizeBytes: 1, Status: "FAILED", MediaKind: "VIDEO", RequirementKey: "video:main", AttentionItems: json.RawMessage(`[]`)}
	if err := fixture.harness.WithinTenant(context.Background(), other.TenantID, func(tx *gorm.DB) error { return tx.Create(&foreignMedia).Error }); err != nil {
		t.Fatal(err)
	}
	if err := fixture.harness.WithinTenant(context.Background(), fixture.tenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.ExternalSession{}).Where("tenant_id=? AND id=?", fixture.tenantID, fixture.sessionID).Update("responsibility_id", other.ResponsibilityID).Error
	}); err != nil {
		t.Fatal(err)
	}
	queryRead := `query { externalCapture { responsibilityId mediaKind } }`
	responseRead := executeAPI03WithCredentials(t, fixture, queryRead)
	assertAPI03Rejected(t, responseRead, "NOT_FOUND", `"externalCapture":{`)
	if strings.Contains(responseRead, other.ResponsibilityID.String()) {
		t.Fatalf("foreign responsibility metadata leaked: %s", responseRead)
	}
	queryConsent := `mutation { acceptProcessing(input:{disclosureVersion:"pt-BR-1",photoProcessing:true,videoProcessing:true,aiAnalysis:true,gpsUse:true,clientMutationId:"it401-foreign-consent"}) { status } }`
	responseConsent := executeAPI03WithCredentials(t, fixture, queryConsent)
	assertAPI03Rejected(t, responseConsent, "NOT_FOUND", `"acceptProcessing":{`)
	if strings.Contains(responseConsent, other.ResponsibilityID.String()) {
		t.Fatalf("foreign responsibility metadata leaked: %s", responseConsent)
	}
	if response := executeAPI03WithCredentials(t, fixture, `query { externalCapture(tenantId:"`+other.TenantID.String()+`") { responsibilityId } }`); !strings.Contains(response, "Cannot query field") && !strings.Contains(response, "Unknown argument") {
		t.Fatalf("tenant selector was not rejected by schema validation: %s", response)
	}
	// Restore the valid session binding before testing the explicit foreign media association.
	if err := fixture.harness.WithinTenant(context.Background(), fixture.tenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.ExternalSession{}).Where("tenant_id=? AND id=?", fixture.tenantID, fixture.sessionID).Update("responsibility_id", fixture.responsibilityID).Error
	}); err != nil {
		t.Fatal(err)
	}
	query := `mutation { beginVideoCapture(input:{acquisitionSource:"CAMERA",replacesMediaId:"` + foreignMedia.ID.String() + `",clientMutationId:"it401-foreign"}) { attempt { id } } }`
	response := executeAPI03WithCredentials(t, fixture, query)
	assertAPI03Rejected(t, response, "NOT_FOUND", `"beginVideoCapture":{`)
}

func TestAPI03PhotoResponsibilityCannotBeginVideoIT404(t *testing.T) {
	fixture := newAPI03FailureFixture(t, "beginVideoCapture")
	if err := fixture.harness.WithinTenant(context.Background(), fixture.tenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.CaptureDraft{}).Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).Update("media_kind", "PHOTO").Error
	}); err != nil {
		t.Fatal(err)
	}
	query := `mutation { beginVideoCapture(input:{acquisitionSource:"CAMERA",clientMutationId:"it404-photo"}) { attempt { id } } }`
	response := executeAPI03WithCredentials(t, fixture, query)
	assertAPI03Rejected(t, response, "INVALID_STATE", `"beginVideoCapture":{`)
}

func TestAPI03MissingVideoConsentRejectedIT405(t *testing.T) {
	for _, operation := range []string{"acceptProcessing", "beginVideoCapture"} {
		t.Run(operation, func(t *testing.T) {
			fixture := newAPI03FailureFixture(t, operation)
			if operation == "beginVideoCapture" {
				if err := fixture.harness.WithinTenant(context.Background(), fixture.tenantID, func(tx *gorm.DB) error {
					return tx.Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).Delete(&database.ProcessingAcceptance{}).Error
				}); err != nil {
					t.Fatal(err)
				}
			}
			var query string
			payload := `"acceptProcessing":{`
			if operation == "acceptProcessing" {
				query = `mutation { acceptProcessing(input:{disclosureVersion:"pt-BR-1",photoProcessing:true,videoProcessing:false,aiAnalysis:true,gpsUse:true,clientMutationId:"it405-consent"}) { status } }`
			} else {
				query = `mutation { beginVideoCapture(input:{acquisitionSource:"CAMERA",clientMutationId:"it405-attempt"}) { attempt { id } } }`
				payload = `"beginVideoCapture":{`
			}
			response := executeAPI03WithCredentials(t, fixture, query)
			assertAPI03Rejected(t, response, "INVALID_INPUT", payload)
		})
	}
}

func TestVVIT115ExpiredInvitationDuringRecordingRejectsVideoAdmission(t *testing.T) {
	fixture := newAPI03FailureFixture(t, "beginVideoCapture")
	// Keep the external session alive and expire only the invitation, matching
	// the state reached when the invitation expires during a local recording.
	if err := fixture.harness.AdminDB.Model(&database.Invitation{}).
		Where("tenant_id=? AND id=?", fixture.tenantID, fixture.invitationID).
		Update("expires_at", time.Now().UTC().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	query := `mutation { beginVideoCapture(input:{acquisitionSource:"CAMERA",clientMutationId:"vv-it115-expired-invite"}) { attempt { id } } }`
	response := executeAPI03WithCredentials(t, fixture, query)
	assertAPI03Rejected(t, response, "SESSION_EXPIRED", `"beginVideoCapture":{`)
	var attempts int64
	if err := fixture.harness.WithinTenant(context.Background(), fixture.tenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.VideoAttempt{}).Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).Count(&attempts).Error
	}); err != nil {
		t.Fatal(err)
	}
	if attempts != 0 {
		t.Fatalf("expired invitation admitted %d video attempts", attempts)
	}
}

func TestVVIT129OldPhotoConsentRequiresCurrentVideoDisclosure(t *testing.T) {
	fixture := newAPI03FailureFixture(t, "beginVideoCapture")
	if err := fixture.harness.WithinTenant(context.Background(), fixture.tenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.ProcessingAcceptance{}).
			Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).
			Updates(map[string]any{"disclosure_version": "photo-v0", "photo_processing": true, "video_processing": false}).Error
	}); err != nil {
		t.Fatal(err)
	}
	// Reauthenticate after the old photo-only disclosure: expire the first
	// session and issue a fresh participant session for the same invitation.
	if err := fixture.harness.AdminDB.Model(&database.ExternalSession{}).
		Where("tenant_id=? AND id=?", fixture.tenantID, fixture.sessionID).
		Update("expires_at", time.Now().UTC().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	pepper := []byte(strings.Repeat("p", 32))
	token, err := security.NewScopedToken(fixture.tenantID)
	if err != nil {
		t.Fatal(err)
	}
	csrf := "vv-it129-reauth-csrf"
	hash := security.HashToken(token)
	proof, err := security.CSRFProof(hash, csrf, pepper)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := fixture.harness.AdminDB.Create(&database.ExternalSession{ID: identity.NewID(), TenantID: fixture.tenantID, InvitationID: fixture.invitationID, ResponsibilityID: fixture.responsibilityID, SessionDigest: hash[:], CSRFDigest: proof[:], ExpiresAt: now.Add(time.Hour), CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	credentials := requestctx.ExternalCredentials{SessionToken: token, CSRFToken: csrf}
	begin := `mutation { beginVideoCapture(input:{acquisitionSource:"CAMERA",clientMutationId:"vv-it129-before-video-consent"}) { attempt { id } } }`
	assertAPI03Rejected(t, executeAPI03WithCredentialsValue(t, fixture, begin, credentials), "INVALID_INPUT", `"beginVideoCapture":{`)
	accept := `mutation { acceptProcessing(input:{disclosureVersion:"pt-BR-1",photoProcessing:true,videoProcessing:true,aiAnalysis:true,gpsUse:true,clientMutationId:"vv-it129-current-video-consent"}) { status userErrors { code } } }`
	accepted := executeAPI03WithCredentialsValue(t, fixture, accept, credentials)
	if strings.Contains(accepted, `"errors"`) || !strings.Contains(accepted, `"status":"ACCEPTED"`) {
		t.Fatalf("current video disclosure was not accepted after reauthentication: %s", accepted)
	}
	started := executeAPI03WithCredentialsValue(t, fixture, begin, credentials)
	if strings.Contains(started, `"errors"`) || !strings.Contains(started, `"attempt":{`) {
		t.Fatalf("beginVideoCapture remained blocked after current video disclosure: %s", started)
	}
}

func TestVV_IT_140CameraOnlyRejectsFileAdmissionAfterCameraNotReadable(t *testing.T) {
	fixture := newAPI03FailureFixture(t, "beginVideoCapture")
	if err := fixture.harness.WithinTenant(context.Background(), fixture.tenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.CaptureDraft{}).
			Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).
			Updates(map[string]any{
				"media_kind":   "VIDEO",
				"requirements": json.RawMessage(`[{"key":"video:main","section":"main","label":"Vídeo principal","required":true,"minimumMedia":1,"maximumMedia":1,"captureSourcePolicy":"CAMERA_ONLY","comparisonTarget":"CHECKLIST_ONLY"}]`),
			}).Error
	}); err != nil {
		t.Fatal(err)
	}
	fileAdmission := `mutation { beginVideoCapture(input:{acquisitionSource:"FILE",clientMutationId:"vv-it140-camera-fallback"}) { attempt { id acquisitionSource } userErrors { code } } }`
	response := executeAPI03WithCredentials(t, fixture, fileAdmission)
	assertAPI03Rejected(t, response, "INVALID_INPUT", `"beginVideoCapture":{`)
	var attempts int64
	if err := fixture.harness.WithinTenant(context.Background(), fixture.tenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.VideoAttempt{}).Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).Count(&attempts).Error
	}); err != nil {
		t.Fatal(err)
	}
	if attempts != 0 {
		t.Fatalf("camera-only FILE fallback created %d attempts", attempts)
	}
}

func TestVVIT125RevokedInvitationRejectsReauthenticatedResume(t *testing.T) {
	fixture := newAPI03FailureFixture(t, "externalCapture")
	ctx := context.Background()
	var mediaCountBefore int64
	if err := fixture.harness.WithinTenant(ctx, fixture.tenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.MediaObject{}).Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).Count(&mediaCountBefore).Error
	}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.harness.AdminDB.Model(&database.ExternalSession{}).
		Where("tenant_id=? AND id=?", fixture.tenantID, fixture.sessionID).
		Update("expires_at", time.Now().UTC().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.harness.AdminDB.Model(&database.Invitation{}).
		Where("tenant_id=? AND id=?", fixture.tenantID, fixture.invitationID).
		Update("status", "REVOKED").Error; err != nil {
		t.Fatal(err)
	}
	// Reauthentication can mint a new session token, but the revoked invitation
	// must still prevent Capture from reopening its saved upload.
	pepper := []byte(strings.Repeat("p", 32))
	token, err := security.NewScopedToken(fixture.tenantID)
	if err != nil {
		t.Fatal(err)
	}
	csrf := "vv-it125-reauth-csrf"
	hash := security.HashToken(token)
	proof, err := security.CSRFProof(hash, csrf, pepper)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := fixture.harness.AdminDB.Create(&database.ExternalSession{ID: identity.NewID(), TenantID: fixture.tenantID, InvitationID: fixture.invitationID, ResponsibilityID: fixture.responsibilityID, SessionDigest: hash[:], CSRFDigest: proof[:], ExpiresAt: now.Add(time.Hour), CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	credentials := requestctx.ExternalCredentials{SessionToken: token, CSRFToken: csrf}
	response := executeAPI03WithCredentialsValue(t, fixture, `query { externalCapture { responsibilityId status } }`, credentials)
	assertAPI03Rejected(t, response, "SESSION_EXPIRED", `"externalCapture":{`)
	var mediaCount int64
	if err := fixture.harness.WithinTenant(ctx, fixture.tenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.MediaObject{}).Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).Count(&mediaCount).Error
	}); err != nil {
		t.Fatal(err)
	}
	if mediaCount != mediaCountBefore {
		t.Fatalf("revoked invitation changed media count from %d to %d", mediaCountBefore, mediaCount)
	}
}

func TestAPI03CompetingAttemptRejectedIT406(t *testing.T) {
	fixture := newAPI03FailureFixture(t, "beginVideoCapture")
	now := time.Now().UTC()
	attempt := database.VideoAttempt{ID: identity.NewID(), TenantID: fixture.tenantID, ResponsibilityID: fixture.responsibilityID, Kind: "CURRENT", PolicyVersion: 1, MaxDurationSeconds: 900, MaxBytes: 1 << 30, AcquisitionSource: "CAMERA", Status: "ACTIVE", IdempotencyKey: "it406-live", PayloadDigest: strings.Repeat("a", 64), AdmittedAt: now, ExpiresAt: now.Add(time.Hour), CreatedAt: now}
	if err := fixture.harness.AdminDB.Create(&attempt).Error; err != nil {
		t.Fatal(err)
	}
	query := `mutation { beginVideoCapture(input:{acquisitionSource:"CAMERA",clientMutationId:"it406-new"}) { attempt { id } } }`
	response := executeAPI03WithCredentials(t, fixture, query)
	assertAPI03Rejected(t, response, "CONFLICT", `"beginVideoCapture":{`)
}

func TestAPI03SuspendedAdmissionRejectedIT407(t *testing.T) {
	fixture := newAPI03FailureFixture(t, "acceptProcessing")
	if err := fixture.harness.WithinTenant(context.Background(), fixture.tenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).Delete(&database.ProcessingAcceptance{}).Error
	}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.harness.AdminDB.Model(&database.VideoAdmission{}).Where("id=1").Updates(map[string]any{"enabled": false, "version": 2}).Error; err != nil {
		t.Fatal(err)
	}
	query := `mutation { acceptProcessing(input:{disclosureVersion:"pt-BR-1",photoProcessing:true,videoProcessing:true,aiAnalysis:true,gpsUse:true,clientMutationId:"it407-consent"}) { status } }`
	response := executeAPI03WithCredentials(t, fixture, query)
	assertAPI03Rejected(t, response, "INTEGRATION_DISABLED", `"acceptProcessing":{`)
}

func TestAPI03SuspendedAdmissionAllowsVideoConsentReacceptIT407(t *testing.T) {
	fixture := newAPI03FailureFixture(t, "acceptProcessing")
	if err := suspendAPI03VideoAdmission(fixture); err != nil {
		t.Fatal(err)
	}
	query := `mutation { acceptProcessing(input:{disclosureVersion:"pt-BR-1",photoProcessing:true,videoProcessing:true,aiAnalysis:true,gpsUse:true,clientMutationId:"it407-reaccept"}) { status } }`
	response := executeAPI03WithCredentials(t, fixture, query)
	if !strings.Contains(response, `"status":"ACCEPTED"`) || strings.Contains(response, `"code":"INTEGRATION_DISABLED"`) {
		t.Fatalf("reacceptance of previously accepted VIDEO responsibility was blocked: %s", response)
	}
}

func TestAPI03SuspendedAdmissionAllowsVideoRecaptureConsentIT368(t *testing.T) {
	fixture := newAPI03FailureFixture(t, "acceptProcessing")
	if err := fixture.harness.WithinTenant(context.Background(), fixture.tenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.CaptureDraft{}).Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).Update("kind", "RECAPTURE").Error
	}); err != nil {
		t.Fatal(err)
	}
	if err := suspendAPI03VideoAdmission(fixture); err != nil {
		t.Fatal(err)
	}
	query := `mutation { acceptProcessing(input:{disclosureVersion:"pt-BR-1",photoProcessing:true,videoProcessing:true,aiAnalysis:true,gpsUse:true,clientMutationId:"it368-recapture-consent"}) { status } }`
	response := executeAPI03WithCredentials(t, fixture, query)
	if !strings.Contains(response, `"status":"ACCEPTED"`) || strings.Contains(response, `"code":"INTEGRATION_DISABLED"`) {
		t.Fatalf("VIDEO recapture consent was blocked while admission was suspended: %s", response)
	}
}

func suspendAPI03VideoAdmission(fixture api03FailureFixture) error {
	return fixture.harness.AdminDB.Model(&database.VideoAdmission{}).Where("id=1").Updates(map[string]any{"enabled": false, "version": 2}).Error
}

func TestAPI03ExpiredSessionRejectsAllOperationsIT408(t *testing.T) {
	for _, operation := range api03FailureOperations() {
		t.Run(operation.name, func(t *testing.T) {
			fixture := newAPI03FailureFixture(t, operation.name)
			var acceptedAt time.Time
			if operation.name == "acceptProcessing" {
				if err := fixture.harness.WithinTenant(context.Background(), fixture.tenantID, func(tx *gorm.DB) error {
					var acceptance database.ProcessingAcceptance
					if err := tx.Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).First(&acceptance).Error; err != nil {
						return err
					}
					acceptedAt = acceptance.AcceptedAt
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			past := time.Now().UTC().Add(-time.Minute)
			if err := fixture.harness.AdminDB.Model(&database.ExternalSession{}).Where("id=?", fixture.sessionID).Update("expires_at", past).Error; err != nil {
				t.Fatal(err)
			}
			if err := fixture.harness.AdminDB.Model(&database.Invitation{}).Where("id=?", fixture.invitationID).Update("expires_at", past).Error; err != nil {
				t.Fatal(err)
			}
			response := executeAPI03WithCredentials(t, fixture, operation.query)
			assertAPI03Rejected(t, response, "SESSION_EXPIRED", operation.successPayload)
			var attempts int64
			if err := fixture.harness.WithinTenant(context.Background(), fixture.tenantID, func(tx *gorm.DB) error {
				return tx.Model(&database.VideoAttempt{}).Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).Count(&attempts).Error
			}); err != nil {
				t.Fatal(err)
			}
			if operation.name == "beginVideoCapture" && attempts != 0 {
				t.Fatalf("expired session advanced protected work: attempts=%d", attempts)
			}
			if operation.name == "acceptProcessing" {
				if err := fixture.harness.WithinTenant(context.Background(), fixture.tenantID, func(tx *gorm.DB) error {
					var acceptance database.ProcessingAcceptance
					if err := tx.Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).First(&acceptance).Error; err != nil {
						return err
					}
					if !acceptance.AcceptedAt.Equal(acceptedAt) {
						t.Fatalf("expired session changed processing acceptance timestamp: before=%s after=%s", acceptedAt, acceptance.AcceptedAt)
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func executeAPI03WithoutCredentials(t *testing.T, fixture api03FailureFixture, query string) string {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(`{"query":`+quote(query)+`}`))
	req.Header.Set("Content-Type", "application/json")
	fixture.server.ServeHTTP(w, req)
	return w.Body.String()
}

func executeAPI03AsInternalPrincipal(t *testing.T, fixture api03FailureFixture, query string) string {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(`{"query":`+quote(query)+`}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Inspection-Test-Tenant", fixture.tenantID.String())
	req.Header.Set("X-Inspection-Test-Identity", fixture.identityID.String())
	req.Header.Set("X-Inspection-Test-Membership", fixture.membershipID.String())
	req.Header.Set("X-Inspection-Test-Roles", "INTERNAL")
	meta, err := auth.TestMetadataFromHeaders(req)
	if err != nil {
		t.Fatal(err)
	}
	req = req.WithContext(requestctx.WithMetadata(req.Context(), meta))
	fixture.server.ServeHTTP(w, req)
	return w.Body.String()
}

func executeAPI03WithCredentials(t *testing.T, fixture api03FailureFixture, query string) string {
	return executeAPI03WithCredentialsValue(t, fixture, query, fixture.credentials)
}

func executeAPI03WithCredentialsValue(t *testing.T, fixture api03FailureFixture, query string, credentials requestctx.ExternalCredentials) string {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/graphql", strings.NewReader(`{"query":`+quote(query)+`}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(requestctx.WithExternalCredentials(req.Context(), credentials))
	fixture.server.ServeHTTP(w, req)
	return w.Body.String()
}

func assertAPI03Rejected(t *testing.T, response, code, successPayload string) {
	t.Helper()
	var payload struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Extensions struct {
				Code string `json:"code"`
			} `json:"extensions"`
		} `json:"errors"`
	}
	if err := json.Unmarshal([]byte(response), &payload); err != nil {
		t.Fatalf("invalid GraphQL response: %v: %s", err, response)
	}
	found := false
	for _, item := range payload.Errors {
		if item.Extensions.Code == code {
			found = true
		}
	}
	if !found || strings.Contains(response, successPayload) {
		t.Fatalf("expected %s without success payload, got %s", code, response)
	}
	if string(payload.Data) != "null" {
		var result map[string]json.RawMessage
		if err := json.Unmarshal(payload.Data, &result); err != nil {
			t.Fatalf("expected null GraphQL result data, got %s: %v", payload.Data, err)
		}
		field := strings.TrimPrefix(strings.SplitN(successPayload, ":", 2)[0], `"`)
		field = strings.TrimSuffix(field, `"`)
		if raw, ok := result[field]; !ok || string(raw) != "null" {
			t.Fatalf("expected null result for %s, got data=%s", field, payload.Data)
		}
	}
}
