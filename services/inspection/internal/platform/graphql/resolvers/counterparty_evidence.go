package resolvers

import (
	"context"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/objectstore"

	"gorm.io/gorm"
)

// counterpartyEvidenceTTL bounds how long a participant's signed display URL
// stays valid; reopening the baseline signs new ones.
const counterpartyEvidenceTTL = 5 * time.Minute

// counterpartyMedia is what an external participant may load for one evidence.
type counterpartyMedia struct {
	url         string
	timestampMs *int
}

// signCounterpartyEvidence signs display URLs for evidence of the version the
// invitation points to. Photos use their DISPLAY derivative; frames must
// belong to the cited source, come from a READY preparation and a screened
// source, mirroring the staff evidence route. Signing is best effort: evidence
// that cannot be signed is listed without a URL.
func signCounterpartyEvidence(ctx context.Context, db *gorm.DB, store objectstore.Store, tenant identity.ID, evidence []database.InventoryItemEvidence) map[identity.ID]counterpartyMedia {
	out := make(map[identity.ID]counterpartyMedia, len(evidence))
	if db == nil || store.Client == nil || len(evidence) == 0 {
		return out
	}
	photoIDs, frameIDs := make([]identity.ID, 0, len(evidence)), make([]identity.ID, 0, len(evidence))
	for _, row := range evidence {
		switch {
		case row.Kind == "PHOTO":
			photoIDs = append(photoIDs, row.MediaID)
		case row.Kind == "VIDEO_FRAME" && row.FrameID != nil:
			frameIDs = append(frameIDs, *row.FrameID)
		}
	}
	var derivatives []database.MediaDerivative
	var frames []database.VideoFrame
	var sources []database.VideoSource
	var preparations []database.VideoPreparationJob
	err := withTask06Tenant(ctx, db, tenant, func(tx *gorm.DB) error {
		// Each read starts from the tenant-scoped handle, not the previous query.
		tx = tx.Session(&gorm.Session{})
		if len(photoIDs) > 0 {
			if err := tx.Where("tenant_id=? AND media_id IN ? AND kind='DISPLAY'", tenant, uniqueIDs(photoIDs)).Find(&derivatives).Error; err != nil {
				return err
			}
		}
		if len(frameIDs) == 0 {
			return nil
		}
		if err := tx.Where("tenant_id=? AND id IN ?", tenant, uniqueIDs(frameIDs)).Find(&frames).Error; err != nil || len(frames) == 0 {
			return err
		}
		sourceIDs, preparationIDs := make([]identity.ID, 0, len(frames)), make([]identity.ID, 0, len(frames))
		for _, frame := range frames {
			sourceIDs = append(sourceIDs, frame.SourceMediaID)
			preparationIDs = append(preparationIDs, frame.PreparationID)
		}
		if err := tx.Where("tenant_id=? AND media_id IN ? AND screening_status IN ('APPROVED','DISABLED')", tenant, uniqueIDs(sourceIDs)).Find(&sources).Error; err != nil {
			return err
		}
		return tx.Where("tenant_id=? AND id IN ? AND status='READY'", tenant, uniqueIDs(preparationIDs)).Find(&preparations).Error
	})
	if err != nil {
		return out
	}
	displayByMedia := make(map[identity.ID]string, len(derivatives))
	for _, derivative := range derivatives {
		displayByMedia[derivative.MediaID] = derivative.ObjectKey
	}
	screened := make(map[identity.ID]bool, len(sources))
	for _, source := range sources {
		screened[source.MediaID] = true
	}
	ready := make(map[identity.ID]bool, len(preparations))
	for _, preparation := range preparations {
		ready[preparation.ID] = true
	}
	frameByID := make(map[identity.ID]database.VideoFrame, len(frames))
	for _, frame := range frames {
		frameByID[frame.ID] = frame
	}
	signed := map[string]string{}
	sign := func(key string) (string, bool) {
		if url, ok := signed[key]; ok {
			return url, true
		}
		url, err := store.PresignGet(ctx, key, counterpartyEvidenceTTL)
		if err != nil {
			return "", false
		}
		signed[key] = url
		return url, true
	}
	for _, row := range evidence {
		if row.Kind == "PHOTO" {
			if key, ok := displayByMedia[row.MediaID]; ok {
				if url, ok := sign(key); ok {
					out[row.ID] = counterpartyMedia{url: url}
				}
			}
			continue
		}
		if row.FrameID == nil {
			continue
		}
		frame, ok := frameByID[*row.FrameID]
		if !ok || frame.SourceMediaID != row.MediaID || !screened[frame.SourceMediaID] || !ready[frame.PreparationID] {
			continue
		}
		if url, ok := sign(frame.ObjectKey); ok {
			timestamp := int(frame.PTSMicros / 1000)
			out[row.ID] = counterpartyMedia{url: url, timestampMs: &timestamp}
		}
	}
	return out
}
