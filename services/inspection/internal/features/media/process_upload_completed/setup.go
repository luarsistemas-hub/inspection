// Package process_upload_completed routes a committed video receipt to durable preparation.
package process_upload_completed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"
	"inspection/services/inspection/internal/platform/video"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Dependencies struct{ Now func() time.Time }

// Setup validates the receipt against the authoritative media row before enqueuing once.
func Setup(d Dependencies) (func(context.Context, *gorm.DB, events.RawEnvelope) error, error) {
	if d.Now == nil {
		d.Now = time.Now
	}
	return func(ctx context.Context, tx *gorm.DB, envelope events.RawEnvelope) error {
		var payload struct {
			MediaID          identity.ID `json:"mediaId"`
			ResponsibilityID identity.ID `json:"responsibilityId"`
			Status           string      `json:"status"`
		}
		decoder := json.NewDecoder(bytes.NewReader(envelope.Payload))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&payload); err != nil || payload.MediaID == (identity.ID{}) || payload.ResponsibilityID == (identity.ID{}) || envelope.AggregateID != payload.MediaID || envelope.TenantID == (identity.ID{}) {
			return messaging.ErrPermanent
		}
		var media database.MediaObject
		err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", envelope.TenantID, payload.MediaID).Take(&media).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			var otherTenant int64
			if err := tx.WithContext(ctx).Model(&database.MediaObject{}).Where("id=? AND tenant_id<>?", payload.MediaID, envelope.TenantID).Count(&otherTenant).Error; err != nil {
				return err
			}
			if otherTenant > 0 {
				return messaging.ErrPermanent
			}
			return fmt.Errorf("media receipt prerequisite is not committed")
		}
		if err != nil {
			return err
		}
		if media.ResponsibilityID != payload.ResponsibilityID {
			return messaging.ErrPermanent
		}
		if media.MediaKind == "PHOTO" {
			if payload.Status != "VERIFIED" {
				return messaging.ErrPermanent
			}
			return nil
		}
		if media.MediaKind != "VIDEO" || payload.Status != "RECEIVED_PENDING_VALIDATION" || media.VideoAttemptID == nil || media.Status == "UPLOADING" || media.Status == "COMPLETING" {
			return messaging.ErrPermanent
		}
		var attempt database.VideoAttempt
		if err := tx.WithContext(ctx).Where("tenant_id=? AND id=? AND responsibility_id=?", envelope.TenantID, *media.VideoAttemptID, payload.ResponsibilityID).Take(&attempt).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return messaging.ErrPermanent
			}
			return err
		}
		if attempt.Status != "RECEIVED" {
			return fmt.Errorf("video attempt receipt prerequisite is pending")
		}
		var existing database.VideoPreparationJob
		if err := tx.WithContext(ctx).Where("tenant_id=? AND media_id=?", envelope.TenantID, payload.MediaID).Order("created_at DESC, id DESC").Take(&existing).Error; err == nil {
			return nil
		} else if err != gorm.ErrRecordNotFound {
			return err
		}
		now := d.Now().UTC()
		requirementKey := media.RequirementKey
		if attempt.RequirementKey != nil {
			requirementKey = *attempt.RequirementKey
		}
		profile, profileErr := video.ProfileByNameVersion("standard", 1)
		if strings.HasPrefix(requirementKey, "env:") {
			profile = video.DefaultProfile()
		} else if profileErr != nil {
			return profileErr
		}
		var requirementKeyPointer *string
		if requirementKey != "" {
			requirementKeyPointer = &requirementKey
		}
		job := database.VideoPreparationJob{ID: identity.NewID(), TenantID: envelope.TenantID, MediaID: payload.MediaID, RequirementKey: requirementKeyPointer, Status: "QUEUED", Phase: "RECEIVED", PreparationVersion: 1, Generation: 1, ProfileDigest: profile.Digest(), NextAttemptAt: now, PendingObjectKeys: []byte("[]"), CreatedAt: now}
		return tx.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&job).Error
	}, nil
}
