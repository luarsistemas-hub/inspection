package core

import (
	"encoding/json"
	"time"

	"inspection/libs/identity"
	retentioncore "inspection/services/inspection/internal/features/retention/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

// LoadEvidenceAvailability resolves evidence states with set-based database reads.
func LoadEvidenceAvailability(tx *gorm.DB, tenant identity.ID, evidence []database.InventoryItemEvidence) (map[identity.ID]string, error) {
	availability := make(map[identity.ID]string, len(evidence))
	mediaIDs, frameIDs := make([]identity.ID, 0, len(evidence)), make([]identity.ID, 0, len(evidence))
	for _, row := range evidence {
		if row.Kind == "PHOTO" {
			mediaIDs = append(mediaIDs, row.MediaID)
		}
		if row.Kind == "VIDEO_FRAME" && row.FrameID != nil {
			frameIDs = append(frameIDs, *row.FrameID)
		}
	}
	mediaIDs = uniqueIDs(mediaIDs)
	frameIDs = uniqueIDs(frameIDs)
	mediaStatus := map[identity.ID]string{}
	if len(mediaIDs) > 0 {
		var media []database.MediaObject
		if err := tx.Where("tenant_id=? AND id IN ?", tenant, mediaIDs).Find(&media).Error; err != nil {
			return nil, err
		}
		for _, row := range media {
			mediaStatus[row.ID] = row.Status
		}
	}
	frames := map[identity.ID]identity.ID{}
	var sourceIDs []identity.ID
	if len(frameIDs) > 0 {
		var rows []database.VideoFrame
		if err := tx.Where("tenant_id=? AND id IN ?", tenant, frameIDs).Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, row := range rows {
			frames[row.ID] = row.SourceMediaID
			sourceIDs = append(sourceIDs, row.SourceMediaID)
		}
	}
	sourceIDs = uniqueIDs(sourceIDs)
	sources := map[identity.ID]database.VideoSource{}
	if len(sourceIDs) > 0 {
		var rows []database.VideoSource
		if err := tx.Where("tenant_id=? AND media_id IN ?", tenant, sourceIDs).Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, row := range rows {
			sources[row.MediaID] = row
		}
	}
	for _, row := range evidence {
		state := "UNAVAILABLE"
		switch row.Kind {
		case "PHOTO":
			if mediaStatus[row.MediaID] == "READY" {
				state = "AVAILABLE"
			}
		case "VIDEO_FRAME":
			if row.FrameID != nil {
				if sourceID, ok := frames[*row.FrameID]; ok {
					source, exists := sources[sourceID]
					if !exists || source.Status == "EXPIRED" || source.OriginalDeletedAt != nil {
						state = "FRAME_ONLY"
					} else {
						state = "AVAILABLE"
					}
				}
			}
		}
		availability[row.ID] = state
	}
	return availability, nil
}

// LoadRetentionAvailability returns the lifecycle label exposed beside video evidence.
func LoadRetentionAvailability(tx *gorm.DB, tenant identity.ID, evidence []database.InventoryItemEvidence) (map[identity.ID]string, error) {
	result := make(map[identity.ID]string, len(evidence))
	frameIDs := make([]identity.ID, 0, len(evidence))
	for _, row := range evidence {
		if row.Kind == "VIDEO_FRAME" && row.FrameID != nil {
			frameIDs = append(frameIDs, *row.FrameID)
		}
	}
	frameIDs = uniqueIDs(frameIDs)
	if len(frameIDs) == 0 {
		return result, nil
	}
	var frames []database.VideoFrame
	if err := tx.Where("tenant_id=? AND id IN ?", tenant, frameIDs).Find(&frames).Error; err != nil {
		return nil, err
	}
	mediaIDs := make([]identity.ID, 0, len(frames))
	frameSource := make(map[identity.ID]identity.ID, len(frames))
	for _, row := range frames {
		mediaIDs = append(mediaIDs, row.SourceMediaID)
		frameSource[row.ID] = row.SourceMediaID
	}
	mediaIDs = uniqueIDs(mediaIDs)
	var sources []database.VideoSource
	if err := tx.Where("tenant_id=? AND media_id IN ?", tenant, mediaIDs).Find(&sources).Error; err != nil {
		return nil, err
	}
	type holdState struct {
		MediaID          identity.ID `gorm:"column:media_id"`
		LegalHold        bool        `gorm:"column:legal_hold"`
		ContestationHold bool        `gorm:"column:contestation_hold"`
	}
	// One set-based read per call; ownership uses the same reference query as
	// retention transitions and expiry claims, so the label never disagrees
	// with what the executor would protect.
	owners := retentioncore.ReferencingVersionsFor("s.tenant_id", "s.media_id")
	var holds []holdState
	if err := tx.Raw(`SELECT s.media_id,
		EXISTS (SELECT 1 FROM retention.legal_holds h WHERE h.tenant_id=s.tenant_id AND h.active=true AND (
			h.source_media_id=s.media_id OR
			h.inspection_id IN (SELECT r.inspection_id FROM inspections.responsibilities r JOIN media.media_objects m ON m.tenant_id=r.tenant_id AND m.responsibility_id=r.id WHERE r.tenant_id=s.tenant_id AND m.id=s.media_id) OR
			h.origin_version_id IN (`+owners+`)
		)) AS legal_hold,
		EXISTS (SELECT 1 FROM origins.counterparty_invitations c WHERE c.tenant_id=s.tenant_id AND c.response='CONTESTED' AND c.resolution='OPEN' AND c.origin_version_id IN (`+owners+`)) AS contestation_hold
		FROM media.video_sources s WHERE s.tenant_id=? AND s.media_id IN ?`, tenant, mediaIDs).Scan(&holds).Error; err != nil {
		return nil, err
	}
	holdByMedia := make(map[identity.ID]holdState, len(holds))
	for _, hold := range holds {
		holdByMedia[hold.MediaID] = hold
	}
	sourceLabels := make(map[identity.ID]string, len(sources))
	for _, source := range sources {
		hold := holdByMedia[source.MediaID]
		if source.Status == "EXPIRED" || source.OriginalDeletedAt != nil {
			hold = holdState{}
		}
		sourceLabels[source.MediaID] = RetentionAvailabilityLabel(source, hold.LegalHold, hold.ContestationHold)
	}
	for _, row := range evidence {
		if row.Kind == "VIDEO_FRAME" && row.FrameID != nil {
			result[row.ID] = sourceLabels[frameSource[*row.FrameID]]
		}
	}
	return result, nil
}

// RetentionAvailabilityLabel maps a source's lifecycle to the customer-facing label.
func RetentionAvailabilityLabel(source database.VideoSource, legalHold, contestationHold bool) string {
	switch {
	case source.Status == "EXPIRED" || source.OriginalDeletedAt != nil:
		return "EXPIRED_FRAMES_PRESERVED"
	case legalHold:
		return "LEGAL_HOLD"
	case contestationHold:
		return "CONTESTATION_HOLD"
	case source.RetentionAnchor == "ACTIVE_BASELINE":
		return "WHILE_BASELINE_ACTIVE"
	case source.SourceExpiryAt != nil:
		return source.SourceExpiryAt.UTC().Format(time.DateOnly)
	default:
		return ""
	}
}

func uniqueIDs(ids []identity.ID) []identity.ID {
	seen := make(map[identity.ID]bool, len(ids))
	unique := make([]identity.ID, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}
	return unique
}

func (s Service) validateEvidence(tx *gorm.DB, tenant, version identity.ID, evs []EvidenceInput) error {
	var v database.OriginVersion
	if err := tx.Where("tenant_id=? AND id=?", tenant, version).First(&v).Error; err != nil {
		return err
	}
	for _, e := range evs {
		if len(e.Region) > 0 {
			if err := validateRegion(e.Region); err != nil {
				return err
			}
		}
		if e.Kind == "PHOTO" {
			var m database.MediaObject
			if err := tx.Where("tenant_id=? AND id=? AND status='READY'", tenant, e.MediaID).First(&m).Error; err != nil {
				return apperror.New(apperror.InvalidInput, "evidence", "evidence must be ready and belong to the source inspection")
			}
			if v.SourceInspectionID != nil {
				var r database.Responsibility
				if err := tx.Where("tenant_id=? AND inspection_id=? AND id=?", tenant, *v.SourceInspectionID, m.ResponsibilityID).First(&r).Error; err != nil {
					return apperror.New(apperror.InvalidInput, "evidence", "evidence belongs to another inspection")
				}
			} else if v.SourceVersionID != nil {
				var count int64
				if err := tx.Model(&database.OriginEvidence{}).Where("tenant_id=? AND origin_version_id=? AND media_id=?", tenant, *v.SourceVersionID, e.MediaID).Count(&count).Error; err != nil {
					return err
				}
				if count == 0 {
					if err := tx.Model(&database.InventoryItemEvidence{}).Joins("JOIN origins.inventory_items i ON i.tenant_id=inventory_item_evidence.tenant_id AND i.id=inventory_item_evidence.item_id").Where("inventory_item_evidence.tenant_id=? AND i.origin_version_id=? AND inventory_item_evidence.kind='PHOTO' AND inventory_item_evidence.media_id=?", tenant, *v.SourceVersionID, e.MediaID).Count(&count).Error; err != nil {
						return err
					}
				}
				if count == 0 {
					return apperror.New(apperror.InvalidInput, "evidence", "evidence does not belong to source version")
				}
			}
		} else if e.Kind != "VIDEO_FRAME" {
			return apperror.New(apperror.InvalidInput, "evidence", "unknown evidence kind")
		} else {
			if e.FrameID == nil {
				return apperror.New(apperror.InvalidInput, "evidence", "video frame id is required")
			}
			var frame database.VideoFrame
			if err := tx.Where("tenant_id=? AND id=? AND source_media_id=?", tenant, *e.FrameID, e.MediaID).First(&frame).Error; err != nil {
				return apperror.New(apperror.InvalidInput, "evidence", "video frame must belong to source inspection")
			}
			if v.SourceInspectionID != nil {
				var media database.MediaObject
				if err := tx.Where("tenant_id=? AND id=?", tenant, e.MediaID).First(&media).Error; err != nil {
					return apperror.New(apperror.InvalidInput, "evidence", "video frame must belong to source inspection")
				}
				var responsibility database.Responsibility
				if err := tx.Where("tenant_id=? AND inspection_id=? AND id=?", tenant, *v.SourceInspectionID, media.ResponsibilityID).First(&responsibility).Error; err != nil {
					return apperror.New(apperror.InvalidInput, "evidence", "video frame must belong to source inspection")
				}
			} else if v.SourceVersionID != nil {
				var source database.OriginVersion
				if err := tx.Where("tenant_id=? AND id=?", tenant, *v.SourceVersionID).First(&source).Error; err != nil || source.VideoPreparationID == nil {
					return apperror.New(apperror.InvalidInput, "evidence", "video frame does not belong to source version")
				}
				var count int64
				if err := tx.Model(&database.VideoPreparationJob{}).Where("tenant_id=? AND id=? AND media_id=?", tenant, *source.VideoPreparationID, e.MediaID).Count(&count).Error; err != nil {
					return err
				}
				if count == 0 {
					if err := tx.Model(&database.InventoryItemEvidence{}).Joins("JOIN origins.inventory_items i ON i.tenant_id=inventory_item_evidence.tenant_id AND i.id=inventory_item_evidence.item_id").Where("inventory_item_evidence.tenant_id=? AND i.origin_version_id=? AND inventory_item_evidence.kind='VIDEO_FRAME' AND inventory_item_evidence.media_id=? AND inventory_item_evidence.frame_id=?", tenant, *v.SourceVersionID, e.MediaID, *e.FrameID).Count(&count).Error; err != nil {
						return err
					}
				}
				if count == 0 {
					return apperror.New(apperror.InvalidInput, "evidence", "video frame does not belong to source version")
				}
			}
		}
	}
	return nil
}

func validateRegion(raw json.RawMessage) error {
	var region map[string]float64
	if err := json.Unmarshal(raw, &region); err != nil {
		return apperror.New(apperror.InvalidInput, "evidence", "evidence region is invalid")
	}
	for _, key := range []string{"x", "y", "w", "h"} {
		value, ok := region[key]
		if !ok || value < 0 || value > 1 {
			return apperror.New(apperror.InvalidInput, "evidence", "evidence region must be normalized")
		}
	}
	if len(region) != 4 || region["x"]+region["w"] > 1 || region["y"]+region["h"] > 1 {
		return apperror.New(apperror.InvalidInput, "evidence", "evidence region must fit within the image")
	}
	return nil
}
