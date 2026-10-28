//go:build integration

package harness

import (
	"context"
	"testing"

	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

// TestVV_IT_354PhotoAnalysisCompletesWithVideoWorkerStopped runs the PHOTO
// journey while the video queue has no consumer; its 100 receipts stay queued.
func TestVV_IT_354PhotoAnalysisCompletesWithVideoWorkerStopped(t *testing.T) {
	runPhotoFlow(t)
}

func TestVV_IT_364CurrentOnlyPhotoAnalysisProceedsWithoutVideoOrigin(t *testing.T) {
	h, fixture := runPhotoFlow(t)
	if err := h.WithinTenant(context.Background(), fixture.TenantID, func(tx *gorm.DB) error {
		var videoOrigins, references int64
		if err := tx.Model(&database.OriginVersion{}).Where("tenant_id=? AND media_kind='VIDEO' AND origin_id IN (SELECT id FROM origins.origins WHERE tenant_id=? AND asset_id=?)", fixture.TenantID, fixture.TenantID, fixture.AssetID).Count(&videoOrigins).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.VideoEvidenceReference{}).Where("tenant_id=?", fixture.TenantID).Count(&references).Error; err != nil {
			return err
		}
		var inspection database.Inspection
		if err := tx.Where("tenant_id=? AND id=?", fixture.TenantID, fixture.InspectionID).Take(&inspection).Error; err != nil {
			return err
		}
		if videoOrigins != 0 || references != 0 || inspection.Status != "COMPLETED" {
			t.Errorf("current-only PHOTO run: videoOrigins=%d videoReferences=%d inspection=%s; want 0, 0, COMPLETED", videoOrigins, references, inspection.Status)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// TestVV_IT_161PhotoReachesReportWhileHundredVideosAreQueued queues 100 video
// receipts on the isolated video queue and completes photo P1 through the
// ordinary worker path without consuming that queue.
func TestVV_IT_161PhotoReachesReportWhileHundredVideosAreQueued(t *testing.T) {
	runPhotoFlow(t)
}
