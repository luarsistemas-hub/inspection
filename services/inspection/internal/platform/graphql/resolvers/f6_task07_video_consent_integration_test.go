//go:build integration

package resolvers

import (
	"context"
	"testing"

	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

func TestVV_IT_138BeginVideoCaptureWithoutVideoProcessingIsInvalidInput(t *testing.T) {
	fixture := newAPI03FailureFixture(t, "beginVideoCapture")
	// The participant accepted photo processing and AI analysis, not video.
	if err := fixture.harness.AdminDB.Model(&database.ProcessingAcceptance{}).Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).Update("video_processing", false).Error; err != nil {
		t.Fatal(err)
	}
	counts := func() (attempts, preparations, outbox int64) {
		t.Helper()
		if err := fixture.harness.WithinTenant(context.Background(), fixture.tenantID, func(tx *gorm.DB) error {
			if err := tx.Model(&database.VideoAttempt{}).Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).Count(&attempts).Error; err != nil {
				return err
			}
			if err := tx.Model(&database.VideoPreparationJob{}).Where("tenant_id=?", fixture.tenantID).Count(&preparations).Error; err != nil {
				return err
			}
			return tx.Model(&database.OutboxIntent{}).Where("tenant_id=?", fixture.tenantID).Count(&outbox).Error
		}); err != nil {
			t.Fatal(err)
		}
		return
	}
	attempts, preparations, outbox := counts()
	response := executeAPI03WithCredentials(t, fixture, `mutation { beginVideoCapture(input:{acquisitionSource:"CAMERA",clientMutationId:"vv-it-138"}) { attempt { id } clientMutationId } }`)
	assertAPI03Rejected(t, response, "INVALID_INPUT", `"beginVideoCapture":{`)
	if a, p, o := counts(); a != attempts || p != preparations || o != outbox {
		t.Fatalf("rejected capture scheduled work: attempts %d->%d preparations %d->%d outbox %d->%d", attempts, a, preparations, p, outbox, o)
	}
}
