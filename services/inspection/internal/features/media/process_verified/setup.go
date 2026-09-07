package process_verified

import (
	"context"
	"encoding/json"
	"fmt"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	mediacore "inspection/services/inspection/internal/features/media/core"
	"inspection/services/inspection/internal/features/media/normalize"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/sensitivecontent"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Dependencies struct {
	Store    objectstore.Store
	Detector sensitivecontent.Detector
}

func Setup(d Dependencies) (func(context.Context, *gorm.DB, events.RawEnvelope) error, error) {
	if d.Store.Client == nil || d.Detector.Model == nil {
		return nil, fmt.Errorf("slice media/process_verified: missing dependency")
	}
	return func(ctx context.Context, tx *gorm.DB, envelope events.RawEnvelope) error {
		var payload struct {
			MediaID identity.ID `json:"mediaId"`
		}
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil || payload.MediaID == (identity.ID{}) {
			return messaging.ErrPermanent
		}
		var media database.MediaObject
		if err := tx.Where("tenant_id=? AND id=?", envelope.TenantID, payload.MediaID).First(&media).Error; err != nil {
			return apperror.New(apperror.NotFound, "mediaId", "media not found")
		}
		if media.Status == "READY" || media.Status == "SCREENED" {
			return nil
		}
		if media.Status != "VERIFIED" {
			return apperror.New(apperror.InvalidState, "mediaId", "verified media is required")
		}
		original, err := d.Store.Read(ctx, media.ObjectKey)
		if err != nil {
			return err
		}
		display, err := normalize.Image(ctx, original, 2048)
		if err != nil {
			return err
		}
		for _, kind := range []string{"DISPLAY", "ANALYSIS"} {
			key, digest, err := d.Store.PutDerivative(ctx, envelope.TenantID, media.ID, kind, display.ContentType, display.Bytes)
			if err != nil {
				return err
			}
			derivative := database.MediaDerivative{ID: identity.NewID(), TenantID: envelope.TenantID, MediaID: media.ID, ObjectKey: key, Kind: kind, SHA256: digest, CreatedAt: envelope.OccurredAt}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&derivative).Error; err != nil {
				return err
			}
		}
		service := mediacore.Service{DB: tx, Detector: d.Detector, Within: func(_ context.Context, _ identity.ID, fn func(*gorm.DB) error) error { return fn(tx) }}
		_, err = service.Screen(ctx, envelope.TenantID, media.ID, display.Bytes)
		return err
	}, nil
}
