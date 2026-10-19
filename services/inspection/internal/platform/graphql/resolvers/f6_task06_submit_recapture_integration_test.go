//go:build integration

package resolvers

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"
)

func TestVV_IT_239SupplementAdmissionUsesDefault900SecondLimit(t *testing.T) {
	fixture := newAPI05CaptureFixture(t)
	requestID := seedAPI05RecaptureRequest(t, fixture, time.Now().UTC().Add(time.Hour))
	response := executeAPI03Success(t, fixture, `mutation { beginVideoCapture(input:{acquisitionSource:"CAMERA",clientMutationId:"vv-it-239-supplement"}) { attempt { id kind maxDurationSeconds maxBytes status } } }`)
	var payload struct {
		Data struct {
			Begin struct {
				Attempt struct {
					ID          string `json:"id"`
					Kind        string `json:"kind"`
					MaxDuration int    `json:"maxDurationSeconds"`
					Status      string `json:"status"`
				} `json:"attempt"`
			} `json:"beginVideoCapture"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(response), &payload); err != nil || strings.Contains(response, `"errors"`) {
		t.Fatalf("beginVideoCapture for RC1 supplement: %s (%v)", response, err)
	}
	attempt := payload.Data.Begin.Attempt
	if attempt.ID == "" || attempt.Kind != "SUPPLEMENT" || attempt.MaxDuration != 900 || attempt.Status != "ACTIVE" {
		t.Fatalf("default supplement admission=%+v; want SUPPLEMENT, 900 seconds, ACTIVE", attempt)
	}
	var request database.RecaptureRequest
	if err := fixture.harness.AdminDB.Where("tenant_id=? AND id=?", fixture.tenantID, requestID).Take(&request).Error; err != nil {
		t.Fatal(err)
	}
	if request.Status != "REQUESTED" {
		t.Fatalf("beginVideoCapture changed RC1 status to %s", request.Status)
	}
}

// seedTask06RecaptureRequest stores a video recapture request for the
// responsibility, optionally turning the session draft into its recapture.
func seedTask06RecaptureRequest(t *testing.T, fixture api03FailureFixture, responsibilityID identity.ID, deadline time.Time) identity.ID {
	t.Helper()
	now := time.Now().UTC()
	var inspection database.Responsibility
	if err := fixture.harness.AdminDB.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.responsibilityID).Take(&inspection).Error; err != nil {
		t.Fatal(err)
	}
	id := identity.NewID()
	if err := fixture.harness.AdminDB.Create(&database.RecaptureRequest{ID: id, TenantID: fixture.tenantID, InspectionID: inspection.InspectionID, ResponsibilityID: responsibilityID, Status: "REQUESTED", DeadlineAt: deadline, IdempotencyKey: "vv-task06-submit-" + id.String(), CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	return id
}

func submitTask06Recapture(t *testing.T, fixture api03FailureFixture, requestID identity.ID, key string) string {
	t.Helper()
	return executeAPI03WithCredentials(t, fixture, `mutation { submitRecapture(input:{requestId:"`+requestID.String()+`",confirmIncomplete:true,clientMutationId:"`+key+`"}) { recapture { id status } clientMutationId } }`)
}

func task06RequestStatus(t *testing.T, fixture api03FailureFixture, requestID identity.ID) string {
	t.Helper()
	var request database.RecaptureRequest
	if err := fixture.harness.AdminDB.Where("tenant_id=? AND id=?", fixture.tenantID, requestID).Take(&request).Error; err != nil {
		t.Fatal(err)
	}
	return request.Status
}

func TestVV_IT_244OtherResponsibilitySessionCannotSubmitToRC1(t *testing.T) {
	fixture := newAPI05CaptureFixture(t)
	// RC1 belongs to responsibility R1; the session belongs to R2.
	now := time.Now().UTC()
	var r2 database.Responsibility
	if err := fixture.harness.AdminDB.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.responsibilityID).Take(&r2).Error; err != nil {
		t.Fatal(err)
	}
	r1 := identity.NewID()
	if err := fixture.harness.AdminDB.Create(&database.Responsibility{ID: r1, TenantID: fixture.tenantID, InspectionID: r2.InspectionID, ParticipantID: r2.ParticipantID, Status: "PENDING", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	rc1 := seedTask06RecaptureRequest(t, fixture, r1, now.Add(24*time.Hour))
	before := api05State(t, fixture)
	response := submitTask06Recapture(t, fixture, rc1, "vv-it-244")
	assertAPI03Rejected(t, response, "NOT_FOUND", `"submitRecapture":{`)
	if strings.Contains(response, r1.String()) {
		t.Fatalf("response leaked R1: %s", response)
	}
	assertAPI05StateUnchanged(t, fixture, before)
	if status := task06RequestStatus(t, fixture, rc1); status != "REQUESTED" {
		t.Fatalf("R2 session changed RC1 to %s", status)
	}
}

func TestVV_IT_248NonexistentRecaptureRequestIsNotFound(t *testing.T) {
	fixture := newAPI05CaptureFixture(t)
	before := api05State(t, fixture)
	missing := identity.NewID()
	response := submitTask06Recapture(t, fixture, missing, "vv-it-248")
	assertAPI03Rejected(t, response, "NOT_FOUND", `"submitRecapture":{`)
	assertAPI05StateUnchanged(t, fixture, before)
	var requirements int64
	if err := fixture.harness.AdminDB.Model(&database.RecaptureRequirement{}).Where("tenant_id=?", fixture.tenantID).Count(&requirements).Error; err != nil {
		t.Fatal(err)
	}
	if requirements != 0 {
		t.Fatalf("nonexistent request attached %d gaps", requirements)
	}
}

func TestVV_IT_249SubmitAfterRC1DeadlineIsInvalidStateAndKeepsRPT1(t *testing.T) {
	fixture := newAPI05CaptureFixture(t)
	now := time.Now().UTC()
	if err := fixture.harness.AdminDB.Model(&database.CaptureDraft{}).Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).Update("kind", "RECAPTURE").Error; err != nil {
		t.Fatal(err)
	}
	rc1 := seedTask06RecaptureRequest(t, fixture, fixture.responsibilityID, now.Add(-time.Minute))
	var responsibility database.Responsibility
	if err := fixture.harness.AdminDB.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.responsibilityID).Take(&responsibility).Error; err != nil {
		t.Fatal(err)
	}
	// RPT1 is already published for the inspection.
	snapshotID, digest := identity.NewID(), strings.Repeat("d", 64)
	if err := fixture.harness.AdminDB.Create(&database.ReportSnapshot{ID: snapshotID, TenantID: fixture.tenantID, InspectionID: responsibility.InspectionID, Mode: "CONSOLIDATED", Classification: "ATTENTION", JSONDigest: digest, HTMLDigest: digest, VersionNumber: 1, CanonicalJSON: []byte(`{}`), HTML: []byte(`"<p>RPT1</p>"`), CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.harness.AdminDB.Create(&database.ReportPublication{ID: identity.NewID(), TenantID: fixture.tenantID, SnapshotID: snapshotID, InspectionID: responsibility.InspectionID, PolicyVersion: 1, Status: "PUBLISHED", ActorID: fixture.identityID, Version: 1, PublishedAt: &now, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	before := api05State(t, fixture)
	response := submitTask06Recapture(t, fixture, rc1, "vv-it-249")
	assertAPI03Rejected(t, response, "INVALID_STATE", `"submitRecapture":{`)
	assertAPI05StateUnchanged(t, fixture, before)
	if status := task06RequestStatus(t, fixture, rc1); status != "REQUESTED" {
		t.Fatalf("late submission changed RC1 to %s", status)
	}
	var published int64
	if err := fixture.harness.AdminDB.Model(&database.ReportPublication{}).Where("tenant_id=? AND snapshot_id=? AND status='PUBLISHED'", fixture.tenantID, snapshotID).Count(&published).Error; err != nil {
		t.Fatal(err)
	}
	var snapshots int64
	if err := fixture.harness.AdminDB.Model(&database.ReportSnapshot{}).Where("tenant_id=? AND inspection_id=?", fixture.tenantID, responsibility.InspectionID).Count(&snapshots).Error; err != nil {
		t.Fatal(err)
	}
	if published != 1 || snapshots != 1 {
		t.Fatalf("late recapture affected RPT1: published=%d snapshots=%d", published, snapshots)
	}
}

func TestVV_IT_247ReplaySubmitRecaptureK17DoesNotFulfillTwiceOrDuplicateReport(t *testing.T) {
	fixture := newAPI05CaptureFixture(t)
	now := time.Now().UTC()
	requestID := seedAPI05RecaptureRequest(t, fixture, now.Add(time.Hour))
	mediaID := identity.NewID()
	seedAPI05VideoMedia(t, fixture, mediaID, "video:supplement")
	query := `mutation { submitRecapture(input:{requestId:"` + requestID.String() + `",confirmIncomplete:false,clientMutationId:"k17"}) { recapture { id status } clientMutationId } }`
	first := executeAPI03WithCredentials(t, fixture, query)
	if strings.Contains(first, `"errors"`) || !strings.Contains(first, `"clientMutationId":"k17"`) || !strings.Contains(first, `"status":"RECEIVED"`) {
		t.Fatalf("first submitRecapture k17 failed: %s", first)
	}
	second := executeAPI03WithCredentials(t, fixture, query)
	if strings.Contains(second, `"errors"`) || !strings.Contains(second, `"clientMutationId":"k17"`) || !strings.Contains(second, `"status":"RECEIVED"`) {
		t.Fatalf("replayed submitRecapture k17 failed: %s", second)
	}
	var responsibility database.Responsibility
	if err := fixture.harness.AdminDB.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.responsibilityID).Take(&responsibility).Error; err != nil {
		t.Fatal(err)
	}
	var submissions, snapshots int64
	if err := fixture.harness.AdminDB.Model(&database.SubmissionVersion{}).Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).Count(&submissions).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.harness.AdminDB.Model(&database.ReportSnapshot{}).Where("tenant_id=? AND inspection_id=?", fixture.tenantID, responsibility.InspectionID).Count(&snapshots).Error; err != nil {
		t.Fatal(err)
	}
	if submissions != 1 || snapshots != 0 || task06RequestStatus(t, fixture, requestID) != "RECEIVED" {
		t.Fatalf("replay duplicated fulfillment or report: submissions=%d snapshots=%d RC1=%s", submissions, snapshots, task06RequestStatus(t, fixture, requestID))
	}
}
