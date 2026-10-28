//go:build integration

package resolvers

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	invitationcore "inspection/services/inspection/internal/features/invitations/core"
	mediacore "inspection/services/inspection/internal/features/media/core"
	mediacreate "inspection/services/inspection/internal/features/media/create_upload"
	"inspection/services/inspection/internal/features/templates/catalog"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/security"

	"gorm.io/gorm"
)

func TestCreateMediaUploadClosedCurrentOnlyV2ReturnsInvalidStateWithoutCreatingMediaIT100(t *testing.T) {
	assertCurrentOnlyMediaUploadRejected(t, "it100", "SUBMITTED", 12, "INVALID_STATE")
}

// TestVV_IT_130ClosedResponsibilityWithSavedDraftRejectsUpload closes R1 by
// cancellation while the A1 attempt (the saved local draft) is still active.
func TestVV_IT_130ClosedResponsibilityWithSavedDraftRejectsUpload(t *testing.T) {
	assertCurrentOnlyMediaUploadRejected(t, "it130", "CANCELED", 12, "INVALID_STATE")
}

// TestVV_IT_139UploadAboveOneGibibyteIsInvalidInput sends one byte above the
// 1 GiB limit on an open responsibility.
func TestVV_IT_139UploadAboveOneGibibyteIsInvalidInput(t *testing.T) {
	assertCurrentOnlyMediaUploadRejected(t, "it139", "", 1<<30+1, "INVALID_INPUT")
}

// TestVV_IT_345DirectVideoUploadAboveOneGibibyteIsInvalidInput verifies the
// same byte ceiling through the authenticated GraphQL upload mutation used by
// direct integrations.
func TestVV_IT_345DirectVideoUploadAboveOneGibibyteIsInvalidInput(t *testing.T) {
	assertCurrentOnlyMediaUploadRejected(t, "vv-it-345", "", 1<<30+1, "INVALID_INPUT")
}

func TestVV_IT_373HundredUnfinishedReceiptsRateLimitWithoutCompletion(t *testing.T) {
	fx := newInspectionModeGraphQLFixture(t, false, catalog.ChecklistOnly)
	if err := fx.h.AdminDB.Exec("INSERT INTO platform.video_admission (id, enabled, version, changed_at, reason, mutation_id, payload_hash) VALUES (1, FALSE, 1, ?, 'IT-373 fixture', 'vv-it-373-admission', '') ON CONFLICT (id) DO UPDATE SET enabled=FALSE", time.Now().UTC()).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	create := `mutation { createInspection(input:{assetId:"` + fx.seed.AssetID.String() + `",participantId:"` + fx.seed.ParticipantID.String() + `",comparisonMode:CHECKLIST_ONLY,dueAt:"` + now.Add(time.Hour).Format(time.RFC3339) + `",deadlineAt:"` + now.Add(24*time.Hour).Format(time.RFC3339) + `",reminderInstants:[],reason:"IT-373",clientMutationId:"vv-it-373-inspection"}) { inspection { id } } }`
	created := executeTask06GraphQL(t, fx.server, create, fx.ctx)
	var payload struct {
		Data struct {
			Create struct {
				Inspection struct {
					ID string `json:"id"`
				} `json:"inspection"`
			} `json:"createInspection"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(created), &payload); err != nil || payload.Data.Create.Inspection.ID == "" || strings.Contains(created, `"errors"`) {
		t.Fatalf("create VIDEO responsibility: %s (%v)", created, err)
	}
	inspectionID, err := identity.ParseID(payload.Data.Create.Inspection.ID)
	if err != nil {
		t.Fatal(err)
	}
	attemptID := identity.NewID()
	var responsibilityID identity.ID
	unfinished := make([]database.MediaObject, 100)
	if err := fx.h.WithinTenant(context.Background(), fx.seed.TenantID, func(tx *gorm.DB) error {
		var responsibility database.Responsibility
		if err := tx.Where("tenant_id=? AND inspection_id=?", fx.seed.TenantID, inspectionID).Take(&responsibility).Error; err != nil {
			return err
		}
		responsibilityID = responsibility.ID
		if err := tx.Create(&database.VideoAttempt{ID: attemptID, TenantID: fx.seed.TenantID, ResponsibilityID: responsibilityID, Kind: "CURRENT", PolicyVersion: 1, MaxDurationSeconds: 900, MaxBytes: 1 << 30, AcquisitionSource: "CAMERA", Status: "ACTIVE", IdempotencyKey: "vv-it-373-attempt", ExpiresAt: now.Add(time.Hour), CreatedAt: now}).Error; err != nil {
			return err
		}
		for i := range unfinished {
			unfinished[i] = database.MediaObject{ID: identity.NewID(), TenantID: fx.seed.TenantID, ResponsibilityID: responsibilityID, ObjectKey: "vv-it-373/unfinished/" + identity.NewID().String(), ContentType: "video/mp4", SHA256: strings.Repeat("b", 64), SizeBytes: 1024, Status: "RECEIVED", MediaKind: "VIDEO", IdempotencyKey: "vv-it-373-receipt-" + identity.NewID().String(), RequirementKey: "video:main", AttentionItems: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), CreatedAt: now}
		}
		return tx.Create(&unfinished).Error
	}); err != nil {
		t.Fatal(err)
	}
	pepper := []byte(strings.Repeat("p", 32))
	token, err := security.NewScopedToken(fx.seed.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	csrf := "vv-it-373-csrf"
	hash := security.HashToken(token)
	proof, err := security.CSRFProof(hash, csrf, pepper)
	if err != nil {
		t.Fatal(err)
	}
	invitation := database.Invitation{ID: identity.NewID(), TenantID: fx.seed.TenantID, ResponsibilityID: responsibilityID, TokenHash: hash[:], DeliveryIntents: json.RawMessage(`[]`), Status: "ACTIVE", ExpiresAt: now.Add(time.Hour), CreatedAt: now}
	if err := fx.h.AdminDB.Create(&invitation).Error; err != nil {
		t.Fatal(err)
	}
	if err := fx.h.AdminDB.Create(&database.ExternalSession{ID: identity.NewID(), TenantID: fx.seed.TenantID, InvitationID: identity.Ptr(invitation.ID), ResponsibilityID: &responsibilityID, SessionDigest: hash[:], CSRFDigest: proof[:], ExpiresAt: now.Add(time.Hour), CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	bus := mediator.New()
	mediaService := mediacore.Service{DB: fx.h.DB, Store: objectstore.Store{Bucket: fx.h.Store.Bucket, Client: fx.h.Store.Client}, Within: fx.h.WithinTenant}
	if err := mediacreate.Setup(mediacreate.Dependencies{Bus: bus, Service: mediaService}); err != nil {
		t.Fatal(err)
	}
	server := captureGraphQLServer(bus, invitationcore.Service{DB: fx.h.DB, Pepper: pepper})
	mutation := `mutation { createMediaUpload(input:{contentType:"video/mp4",sizeBytes:1024,sha256:"` + strings.Repeat("a", 64) + `",attemptId:"` + attemptID.String() + `",clientMutationId:"vv-it-373-upload"}) { upload { mediaId } } }`
	response := executeCaptureGraphQL(t, server, mutation, fx.seed.TenantID, requestctx.ExternalCredentials{SessionToken: token, CSRFToken: csrf}, true)
	if !strings.Contains(response, "RATE_LIMITED") || strings.Contains(response, `"upload":{"mediaId"`) {
		t.Fatalf("upload admission did not return RATE_LIMITED without result: %s", response)
	}
	if err := fx.h.WithinTenant(context.Background(), fx.seed.TenantID, func(tx *gorm.DB) error {
		var receipts, completed int64
		if err := tx.Model(&database.MediaObject{}).Where("tenant_id=? AND responsibility_id=?", fx.seed.TenantID, responsibilityID).Count(&receipts).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.MediaObject{}).Where("tenant_id=? AND responsibility_id=? AND status='COMPLETED'", fx.seed.TenantID, responsibilityID).Count(&completed).Error; err != nil {
			return err
		}
		if receipts != 100 || completed != 0 {
			t.Errorf("after rate limit receipts=%d completed=%d; want 100 unfinished and no completion", receipts, completed)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// assertCurrentOnlyMediaUploadRejected creates a current-only V2 with an active
// A1 attempt, optionally closes R1, and requires createMediaUpload to fail with
// code without persisting media.
func assertCurrentOnlyMediaUploadRejected(t *testing.T, label, closedStatus string, sizeBytes int64, code string) {
	t.Helper()
	fx := newInspectionModeGraphQLFixture(t, false, catalog.ChecklistOnly)
	if err := fx.h.AdminDB.Exec("INSERT INTO platform.video_admission (id, enabled, version, changed_at, reason, mutation_id, payload_hash) VALUES (1, FALSE, 1, ?, ?, ?, '') ON CONFLICT (id) DO UPDATE SET enabled=FALSE", time.Now().UTC(), "isolated IT-100 fixture", "IT-100").Error; err != nil {
		t.Fatal(err)
	}
	nowForCreate := time.Now().UTC()
	create := `mutation { createInspection(input:{assetId:"` + fx.seed.AssetID.String() + `",participantId:"` + fx.seed.ParticipantID.String() + `",comparisonMode:CHECKLIST_ONLY,dueAt:"` + nowForCreate.Add(time.Hour).Format(time.RFC3339) + `",deadlineAt:"` + nowForCreate.Add(24*time.Hour).Format(time.RFC3339) + `",reminderInstants:[],reason:"` + label + `",clientMutationId:"` + label + `"}) { inspection { id } } }`
	createResponse := executeTask06GraphQL(t, fx.server, create, fx.ctx)
	var created struct {
		Data struct {
			Payload struct {
				Inspection struct {
					ID string `json:"id"`
				} `json:"inspection"`
			} `json:"createInspection"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(createResponse), &created); err != nil || created.Data.Payload.Inspection.ID == "" || strings.Contains(createResponse, `"errors"`) {
		t.Fatalf("create V2 failed: %s (%v)", createResponse, err)
	}
	inspectionID, err := identity.ParseID(created.Data.Payload.Inspection.ID)
	if err != nil {
		t.Fatal(err)
	}
	var responsibilityID identity.ID
	attemptID := identity.NewID()
	if err := fx.h.WithinTenant(context.Background(), fx.seed.TenantID, func(tx *gorm.DB) error {
		var responsibility database.Responsibility
		if err := tx.Where("tenant_id=? AND inspection_id=?", fx.seed.TenantID, inspectionID).Take(&responsibility).Error; err != nil {
			return err
		}
		responsibilityID = responsibility.ID
		if err := tx.Model(&database.CaptureDraft{}).Where("tenant_id=? AND responsibility_id=?", fx.seed.TenantID, responsibilityID).Update("media_kind", "VIDEO").Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoAttempt{ID: attemptID, TenantID: fx.seed.TenantID, ResponsibilityID: responsibilityID, Kind: "CURRENT", PolicyVersion: 1, MaxDurationSeconds: 900, MaxBytes: 1 << 30, AcquisitionSource: "CAMERA", Status: "ACTIVE", IdempotencyKey: label + "-attempt", ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now()}).Error; err != nil {
			return err
		}
		if closedStatus == "" {
			return nil
		}
		if closedStatus == "SUBMITTED" {
			if err := tx.Model(&database.CaptureDraft{}).Where("tenant_id=? AND responsibility_id=?", fx.seed.TenantID, responsibilityID).Update("status", "SUBMITTED").Error; err != nil {
				return err
			}
		}
		return tx.Model(&database.Responsibility{}).Where("tenant_id=? AND id=?", fx.seed.TenantID, responsibilityID).Update("status", closedStatus).Error
	}); err != nil {
		t.Fatal(err)
	}
	pepper := []byte(strings.Repeat("p", 32))
	token, err := security.NewScopedToken(fx.seed.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	csrf := label + "-csrf"
	hash := security.HashToken(token)
	proof, err := security.CSRFProof(hash, csrf, pepper)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	invitation := database.Invitation{ID: identity.NewID(), TenantID: fx.seed.TenantID, ResponsibilityID: responsibilityID, TokenHash: hash[:], DeliveryIntents: json.RawMessage(`[]`), Status: "ACTIVE", ExpiresAt: now.Add(time.Hour), CreatedAt: now}
	if err := fx.h.AdminDB.Create(&invitation).Error; err != nil {
		t.Fatal(err)
	}
	if err := fx.h.AdminDB.Create(&database.ExternalSession{ID: identity.NewID(), TenantID: fx.seed.TenantID, InvitationID: identity.Ptr(invitation.ID), ResponsibilityID: &responsibilityID, SessionDigest: hash[:], CSRFDigest: proof[:], ExpiresAt: now.Add(time.Hour), CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	bus := mediator.New()
	mediaService := mediacore.Service{DB: fx.h.DB, Store: objectstore.Store{Bucket: fx.h.Store.Bucket, Client: fx.h.Store.Client}, Within: fx.h.WithinTenant}
	if err := mediacreate.Setup(mediacreate.Dependencies{Bus: bus, Service: mediaService}); err != nil {
		t.Fatal(err)
	}
	server := captureGraphQLServer(bus, invitationcore.Service{DB: fx.h.DB, Pepper: pepper})
	query := `mutation { createMediaUpload(input:{contentType:"video/mp4",sizeBytes:` + strconv.FormatInt(sizeBytes, 10) + `,sha256:"` + strings.Repeat("a", 64) + `",attemptId:"` + attemptID.String() + `",clientMutationId:"` + label + `-upload"}) { upload { mediaId } } }`
	response := executeCaptureGraphQL(t, server, query, fx.seed.TenantID, requestctx.ExternalCredentials{SessionToken: token, CSRFToken: csrf}, true)
	if !strings.Contains(response, code) || strings.Contains(response, `"upload":{"mediaId"`) {
		t.Fatalf("%s createMediaUpload response = %s; want %s and no upload", label, response, code)
	}
	if err := fx.h.WithinTenant(context.Background(), fx.seed.TenantID, func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&database.MediaObject{}).Where("tenant_id=? AND responsibility_id=?", fx.seed.TenantID, responsibilityID).Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			t.Errorf("%s persisted %d media objects", label, count)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
