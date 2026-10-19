//go:build integration

package resolvers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

// newPhotoCaptureFixture reuses the external capture fixture with a PHOTO draft.
func newPhotoCaptureFixture(t *testing.T) api03FailureFixture {
	t.Helper()
	fixture := newAPI05CaptureFixture(t)
	if err := fixture.harness.AdminDB.Model(&database.CaptureDraft{}).
		Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).
		Updates(map[string]any{"media_kind": "PHOTO", "requirements": []byte(`[{"key":"room-1","section":"photo","label":"Sala","evidenceKind":"PHOTO","required":false,"minimumCount":0,"maximumCount":1}]`)}).Error; err != nil {
		t.Fatal(err)
	}
	return fixture
}

func TestVV_IT_360RevokedPhotoInvitationKeepsExternalCaptureDenied(t *testing.T) {
	fixture := newPhotoCaptureFixture(t)
	query := `query { externalCapture { responsibilityId mediaKind status } }`
	if allowed := executeAPI03WithCredentials(t, fixture, query); !strings.Contains(allowed, `"mediaKind":"PHOTO"`) || strings.Contains(allowed, `"errors"`) {
		t.Fatalf("active photo invitation could not read its capture: %s", allowed)
	}
	if err := fixture.harness.AdminDB.Model(&database.Invitation{}).Where("tenant_id=? AND id=?", fixture.tenantID, fixture.invitationID).Update("status", "REVOKED").Error; err != nil {
		t.Fatal(err)
	}
	response := executeAPI03WithCredentials(t, fixture, query)
	assertAPI03Rejected(t, response, "SESSION_EXPIRED", `"externalCapture":{`)
	if strings.Contains(response, fixture.responsibilityID.String()) {
		t.Fatalf("revoked photo invitation leaked capture metadata: %s", response)
	}
}

func TestVV_IT_363ReplayedPhotoSubmitCaptureReusesLogicalSubmission(t *testing.T) {
	fixture := newPhotoCaptureFixture(t)
	submit := `mutation { submitCapture(input:{confirmIncomplete:true,clientMutationId:"k25"}) { submission { id } clientMutationId } }`
	submissionID := func(response string) string {
		t.Helper()
		var payload struct {
			Data struct {
				Submit struct {
					Submission struct {
						ID string `json:"id"`
					} `json:"submission"`
					MutationID string `json:"clientMutationId"`
				} `json:"submitCapture"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(response), &payload); err != nil || payload.Data.Submit.Submission.ID == "" || payload.Data.Submit.MutationID != "k25" || strings.Contains(response, `"errors"`) {
			t.Fatalf("photo submitCapture k25: %s (%v)", response, err)
		}
		return payload.Data.Submit.Submission.ID
	}
	first := submissionID(executeAPI03WithCredentials(t, fixture, submit))
	replay := submissionID(executeAPI03WithCredentials(t, fixture, submit))
	if replay != first {
		t.Fatalf("replayed k25 created submission %s, want %s", replay, first)
	}
	if err := fixture.harness.WithinTenant(context.Background(), fixture.tenantID, func(tx *gorm.DB) error {
		var submissions, events, preparations int64
		if err := tx.Model(&database.SubmissionVersion{}).Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).Count(&submissions).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.OutboxIntent{}).Where("tenant_id=? AND type='capture.submitted.v1'", fixture.tenantID).Count(&events).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.VideoPreparationJob{}).Where("tenant_id=?", fixture.tenantID).Count(&preparations).Error; err != nil {
			return err
		}
		if submissions != 1 || events != 1 || preparations != 0 {
			t.Errorf("photo replay submissions=%d submittedEvents=%d videoPreparations=%d; want 1, 1, 0", submissions, events, preparations)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
