package catalog_notes

import (
	"context"
	"time"

	"inspection/libs/identity"

	"gorm.io/gorm"
)

// pendingAnchor is a submitted note whose anchor still has to be resolved.
type pendingAnchor struct {
	ID               identity.ID
	ResponsibilityID identity.ID
	TargetKind       string
	PhotoMediaID     *identity.ID
	VideoAttemptID   *identity.ID
	EndMS            *int64
}

// ResolveAnchors settles the anchor of every submitted note of the room that is
// still pending. A photo anchor follows its media. A video interval resolves
// only to the final media of that exact attempt, and only when its prepared
// timeline contains the interval; it is never moved to other media. Anchors
// whose evidence is not final stay pending.
func ResolveAnchors(ctx context.Context, tx *gorm.DB, room Room, now time.Time) error {
	tx = tx.WithContext(ctx)
	if err := invalidateStaleAnchors(tx, room, now); err != nil {
		return err
	}
	var pending []pendingAnchor
	query := `SELECT id,responsibility_id,target_kind,photo_media_id,video_attempt_id,end_ms FROM transcription.audio_notes
WHERE tenant_id=? AND inspection_id=? AND requirement_key=? AND anchor_status='PENDING' AND status NOT IN ('UPLOADING','CANCELED','EXPIRED')`
	args := []any{room.TenantID, room.InspectionID, room.RequirementKey}
	if room.ResponsibilityID != (identity.ID{}) {
		query += " AND responsibility_id=?"
		args = append(args, room.ResponsibilityID)
	}
	if err := tx.Raw(query+" ORDER BY created_at ASC, id ASC", args...).Scan(&pending).Error; err != nil {
		return err
	}
	for _, note := range pending {
		status, media, generation, err := resolveOne(tx, room.TenantID, note)
		if err != nil {
			return err
		}
		if status == "PENDING" {
			continue
		}
		var generationValue, mediaValue any
		if media != nil {
			mediaValue = *media
		}
		if generation > 0 {
			generationValue = generation
		}
		if err := tx.Exec(`UPDATE transcription.audio_notes SET anchor_status=?,source_media_id=COALESCE(?,source_media_id),source_generation=COALESCE(?,source_generation),version=version+1,updated_at=?
WHERE tenant_id=? AND id=? AND anchor_status='PENDING'`, status, mediaValue, generationValue, now, room.TenantID, note.ID).Error; err != nil {
			return err
		}
	}
	return nil
}

// invalidateStaleAnchors prevents a previously valid note from remaining
// active after its source media is no longer available.
// The source IDs remain on the note so published provenance stays traceable.
func invalidateStaleAnchors(tx *gorm.DB, room Room, now time.Time) error {
	query := `UPDATE transcription.audio_notes SET anchor_status='INVALID',version=version+1,updated_at=?
WHERE tenant_id=? AND inspection_id=? AND requirement_key=? AND anchor_status='VALID' AND status NOT IN ('UPLOADING','CANCELED','EXPIRED')
AND (COALESCE(photo_media_id,source_media_id) IS NULL OR NOT EXISTS (
	SELECT 1 FROM media.media_objects m WHERE m.tenant_id=transcription.audio_notes.tenant_id AND m.id=COALESCE(transcription.audio_notes.photo_media_id,transcription.audio_notes.source_media_id) AND m.status='READY'))`
	args := []any{now, room.TenantID, room.InspectionID, room.RequirementKey}
	if room.ResponsibilityID != (identity.ID{}) {
		query += " AND responsibility_id=?"
		args = append(args, room.ResponsibilityID)
	}
	return tx.Exec(query, args...).Error
}

func resolveOne(tx *gorm.DB, tenantID identity.ID, note pendingAnchor) (status string, media *identity.ID, generation int, err error) {
	switch {
	case note.TargetKind == "PHOTO" && note.PhotoMediaID != nil:
		var mediaStatus string
		if err := tx.Raw("SELECT status FROM media.media_objects WHERE tenant_id=? AND id=?", tenantID, *note.PhotoMediaID).Row().Scan(&mediaStatus); err != nil {
			return "UNAVAILABLE", nil, 0, nil
		}
		return photoAnchorStatus(mediaStatus), note.PhotoMediaID, 0, nil
	case note.TargetKind == "VIDEO_INTERVAL" && note.VideoAttemptID != nil && note.EndMS != nil:
		var clip struct {
			ID     identity.ID
			Status string
		}
		if err := tx.Raw(`SELECT id,status FROM media.media_objects WHERE tenant_id=? AND responsibility_id=? AND video_attempt_id=? AND media_kind='VIDEO' ORDER BY created_at DESC, id DESC LIMIT 1`, tenantID, note.ResponsibilityID, *note.VideoAttemptID).Scan(&clip).Error; err != nil {
			return "", nil, 0, err
		}
		if clip.ID == (identity.ID{}) {
			return "PENDING", nil, 0, nil
		}
		switch clip.Status {
		case "REJECTED", "FAILED":
			return "INVALID", nil, 0, nil
		case "ABORTED", "PURGED":
			return "UNAVAILABLE", nil, 0, nil
		case "READY":
		default:
			return "PENDING", nil, 0, nil
		}
		var prepared struct {
			Status         string
			Generation     int
			DurationMicros *int64
		}
		if err := tx.Raw(`SELECT j.status,j.generation,s.duration_micros FROM media.video_preparation_jobs j
LEFT JOIN media.video_sources s ON s.tenant_id=j.tenant_id AND s.media_id=j.media_id
WHERE j.tenant_id=? AND j.media_id=? ORDER BY j.generation DESC LIMIT 1`, tenantID, clip.ID).Scan(&prepared).Error; err != nil {
			return "", nil, 0, err
		}
		switch prepared.Status {
		case "READY":
		case "FAILED":
			return "INVALID", nil, 0, nil
		default:
			return "PENDING", nil, 0, nil
		}
		if prepared.DurationMicros == nil {
			// The prepared timeline has not been measured yet.
			return "PENDING", nil, 0, nil
		}
		if *note.EndMS > *prepared.DurationMicros/1000 {
			// The interval does not exist on the final media timeline.
			return "INVALID", nil, 0, nil
		}
		return "VALID", &clip.ID, prepared.Generation, nil
	}
	return "UNAVAILABLE", nil, 0, nil
}

func photoAnchorStatus(mediaStatus string) string {
	switch mediaStatus {
	case "READY":
		return "VALID"
	case "REJECTED", "FAILED":
		return "INVALID"
	case "ABORTED", "PURGED":
		return "UNAVAILABLE"
	default:
		return "PENDING"
	}
}
