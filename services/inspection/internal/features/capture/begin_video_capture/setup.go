// Package begin_video_capture admits a single video recording attempt and freezes its limits.
package begin_video_capture

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const MaxBytes int64 = 1 << 30

type Command struct {
	TenantID, ResponsibilityID    identity.ID
	AcquisitionSource, MutationID string
	ReplacesMediaID               *identity.ID
}
type Dependencies struct {
	DB     *gorm.DB
	Bus    *mediator.Bus
	Now    func() time.Time
	Within func(context.Context, identity.ID, func(*gorm.DB) error) error
}

func Setup(d Dependencies) error {
	if d.DB == nil || d.Bus == nil {
		return fmt.Errorf("slice capture/begin_video_capture: missing dependency")
	}
	return d.Bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		return admit(ctx, d.DB, d.Within, raw.(Command), now(d.Now))
	})
}

func now(clock func() time.Time) time.Time {
	if clock != nil {
		return clock().UTC()
	}
	return time.Now().UTC()
}

func admit(ctx context.Context, db *gorm.DB, within func(context.Context, identity.ID, func(*gorm.DB) error) error, cmd Command, at time.Time) (database.VideoAttempt, error) {
	cmd.AcquisitionSource = strings.ToUpper(strings.TrimSpace(cmd.AcquisitionSource))
	if cmd.TenantID == (identity.ID{}) || cmd.ResponsibilityID == (identity.ID{}) || strings.TrimSpace(cmd.MutationID) == "" || (cmd.AcquisitionSource != "CAMERA" && cmd.AcquisitionSource != "FILE") {
		return database.VideoAttempt{}, apperror.New(apperror.InvalidInput, "input", "invalid video capture request")
	}
	payload := cmd.AcquisitionSource
	if cmd.ReplacesMediaID != nil {
		payload += ":" + cmd.ReplacesMediaID.String()
	}
	digestBytes := sha256.Sum256([]byte(payload))
	digest := hex.EncodeToString(digestBytes[:])
	var result database.VideoAttempt
	run := within
	if run == nil {
		run = (tenanttx.Runner{DB: db}).Within
	}
	err := run(ctx, cmd.TenantID, func(tx *gorm.DB) error {
		var existing database.VideoAttempt
		if err := tx.Where("tenant_id=? AND responsibility_id=? AND idempotency_key=?", cmd.TenantID, cmd.ResponsibilityID, cmd.MutationID).First(&existing).Error; err == nil {
			if existing.PayloadDigest != digest {
				return apperror.New(apperror.Conflict, "clientMutationId", "request payload changed")
			}
			result = existing
			return nil
		} else if err != gorm.ErrRecordNotFound {
			return err
		}
		var draft database.CaptureDraft
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND responsibility_id=?", cmd.TenantID, cmd.ResponsibilityID).First(&draft).Error; err != nil {
			return apperror.New(apperror.NotFound, "responsibility", "capture not found")
		}
		if (draft.Status != "OPEN" && !(draft.Status == "SUBMITTED" && cmd.ReplacesMediaID != nil)) || draft.MediaKind != "VIDEO" {
			return apperror.New(apperror.InvalidState, "responsibility", "video capture is unavailable")
		}
		if cmd.AcquisitionSource == "FILE" {
			var capturePolicy struct {
				AllowGallery bool `json:"allowGallery"`
			}
			if err := json.Unmarshal(draft.PolicyPayload, &capturePolicy); err != nil {
				return err
			}
			if !capturePolicy.AllowGallery {
				return apperror.New(apperror.InvalidInput, "acquisitionSource", "file acquisition is not allowed")
			}
		}
		var acceptance database.ProcessingAcceptance
		if err := tx.Where("tenant_id=? AND responsibility_id=? AND video_processing=true AND ai_analysis=true AND gps_use=true", cmd.TenantID, cmd.ResponsibilityID).First(&acceptance).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			return apperror.New(apperror.InvalidInput, "videoProcessing", "video processing consent is required")
		}
		var invitation database.Invitation
		if err := tx.Where("tenant_id=? AND responsibility_id=? AND status='ACTIVE' AND revoked_at IS NULL", cmd.TenantID, cmd.ResponsibilityID).Order("created_at desc").First(&invitation).Error; err != nil || !at.Before(invitation.ExpiresAt) {
			return apperror.New(apperror.SessionExpired, "responsibility", "invitation expired")
		}
		var policy database.VideoPolicy
		if err := tx.Where("tenant_id=?", cmd.TenantID).First(&policy).Error; err == gorm.ErrRecordNotFound {
			policy = database.VideoPolicy{TenantID: cmd.TenantID, MaxDurationMinutes: 15, OriginRetentionDays: 30, CurrentRetentionDays: 5, Version: 1}
		} else if err != nil {
			return err
		}
		requirementKey := "video:main"
		if draft.Kind == "RECAPTURE" {
			requirementKey = "video:supplement"
		}
		var priorMedia []database.MediaObject
		if err := tx.Where("tenant_id=? AND responsibility_id=? AND media_kind='VIDEO' AND requirement_key=? AND status NOT IN ?", cmd.TenantID, cmd.ResponsibilityID, requirementKey, []string{"REJECTED", "FAILED", "ABORTED", "PURGED"}).Find(&priorMedia).Error; err != nil {
			return err
		}
		if len(priorMedia) > 0 {
			return apperror.New(apperror.Conflict, "responsibility", "the main video has already been received")
		}
		if cmd.ReplacesMediaID != nil {
			var old database.MediaObject
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND responsibility_id=? AND id=? AND media_kind='VIDEO' AND requirement_key='video:main'", cmd.TenantID, cmd.ResponsibilityID, *cmd.ReplacesMediaID).First(&old).Error; err != nil {
				if err == gorm.ErrRecordNotFound {
					return apperror.New(apperror.NotFound, "replacesMediaId", "media not found")
				}
				return err
			} else if old.Status != "REJECTED" && old.Status != "FAILED" {
				return apperror.New(apperror.InvalidState, "replacesMediaId", "only rejected unanalysed video can be replaced")
			}
			if draft.Kind == "INSPECTION" {
				var responsibility database.Responsibility
				if err := tx.Where("tenant_id=? AND id=?", cmd.TenantID, cmd.ResponsibilityID).First(&responsibility).Error; err != nil {
					return err
				}
				var reports int64
				if err := tx.Model(&database.ReportSnapshot{}).Where("tenant_id=? AND inspection_id=?", cmd.TenantID, responsibility.InspectionID).Count(&reports).Error; err != nil {
					return err
				}
				if reports > 0 {
					return apperror.New(apperror.InvalidState, "replacesMediaId", "completed inspections require a recapture")
				}
			}
			if draft.Status == "SUBMITTED" {
				if err := tx.Model(&draft).Updates(map[string]any{"status": "OPEN", "version": draft.Version + 1, "updated_at": at}).Error; err != nil {
					return err
				}
			}
		}
		var active database.VideoAttempt
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND responsibility_id=? AND status='ACTIVE'", cmd.TenantID, cmd.ResponsibilityID).First(&active).Error; err == nil {
			if !at.Before(active.ExpiresAt) {
				active.Status = "EXPIRED"
				if err := tx.Save(&active).Error; err != nil {
					return err
				}
			} else {
				return apperror.New(apperror.Conflict, "responsibility", "a video capture attempt is already active")
			}
		} else if err != gorm.ErrRecordNotFound {
			return err
		}
		expires := at.Add(24 * time.Hour)
		if invitation.ExpiresAt.Before(expires) {
			expires = invitation.ExpiresAt
		}
		kind := "CURRENT"
		if draft.Kind == "ORIGIN" {
			kind = "ORIGIN"
		} else if draft.Kind == "RECAPTURE" {
			kind = "SUPPLEMENT"
		}
		result = database.VideoAttempt{ID: identity.NewID(), TenantID: cmd.TenantID, ResponsibilityID: cmd.ResponsibilityID, Kind: kind, PolicyVersion: policy.Version, MaxDurationSeconds: policy.MaxDurationMinutes * 60, MaxBytes: MaxBytes, AcquisitionSource: cmd.AcquisitionSource, Status: "ACTIVE", IdempotencyKey: cmd.MutationID, PayloadDigest: digest, AdmittedAt: at, ExpiresAt: expires, CreatedAt: at}
		if err := tx.Create(&result).Error; err != nil {
			return err
		}
		return tx.Create(&database.AuditEvent{ID: identity.NewID(), TenantID: cmd.TenantID, ActorID: cmd.ResponsibilityID, Action: "capture.video_attempt_admitted", TargetType: "video_attempt", TargetID: result.ID.String(), Outcome: "ACCEPTED", Reason: cmd.AcquisitionSource, CorrelationID: "video-attempt-" + result.ID.String(), OccurredAt: at}).Error
	})
	return result, err
}
