// Package manage_video_preparation exposes safe preparation status and audited retries.
package manage_video_preparation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/tenanttx"
	"inspection/services/inspection/internal/platform/video"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Query struct {
	TenantID identity.ID
	MediaID  identity.ID
}

// CaptureQuery reads preparation after the public resolver has validated the
// external session and checked that the video belongs to its responsibility.
type CaptureQuery struct {
	TenantID identity.ID
	MediaID  identity.ID
}

type RetryCommand struct {
	TenantID                   identity.ID
	MediaID                    identity.ID
	ExpectedPreparationVersion int
	MutationID                 string
}

type ReprocessCommand struct {
	TenantID                   identity.ID
	MediaID                    identity.ID
	ExpectedPreparationVersion int
	ProfileDigest              string
	MutationID                 string
}

type Status struct {
	MediaID            identity.ID
	PreparationID      identity.ID
	Status             string
	Stage              string
	PreparationVersion int
	Attempts           int
	ProcessedFrames    int
	ErrorCode          string
	Retryable          bool
	SourceAvailable    bool
	ManifestDigest     string
	FirstFrameReadyAt  *time.Time
	SourceExpiryAt     *time.Time
}

type Dependencies struct {
	DB         *gorm.DB
	Bus        *mediator.Bus
	Authorizer auth.Authorizer
	Within     func(context.Context, identity.ID, func(*gorm.DB) error) error
}

func Setup(d Dependencies) error {
	if d.DB == nil || d.Bus == nil {
		return fmt.Errorf("slice media/manage_video_preparation: missing dependency")
	}
	if err := d.Bus.RegisterQuery(Query{}, func(ctx context.Context, raw any) (any, error) {
		q := raw.(Query)
		if q.TenantID == (identity.ID{}) || q.MediaID == (identity.ID{}) {
			return nil, apperror.New(apperror.InvalidInput, "mediaId", "media id is required")
		}
		if _, err := d.Authorizer.Authorize(ctx, q.TenantID, []string{auth.TenantAdmin, auth.InspectionConfigAdmin, auth.Manager, auth.Employee, auth.Viewer}, nil, false); err != nil {
			return nil, err
		}
		return load(ctx, d.DB, d.Within, q)
	}); err != nil {
		return err
	}
	if err := d.Bus.RegisterQuery(CaptureQuery{}, func(ctx context.Context, raw any) (any, error) {
		q := raw.(CaptureQuery)
		if q.TenantID == (identity.ID{}) || q.MediaID == (identity.ID{}) {
			return nil, apperror.New(apperror.InvalidInput, "mediaId", "media id is required")
		}
		if _, ok := requestctx.ExternalCredentialsFromContext(ctx); !ok {
			return nil, apperror.New(apperror.Unauthenticated, "", "capture session required")
		}
		return load(ctx, d.DB, d.Within, Query{TenantID: q.TenantID, MediaID: q.MediaID})
	}); err != nil {
		return err
	}
	if err := d.Bus.RegisterCommand(RetryCommand{}, func(ctx context.Context, raw any) (any, error) {
		c := raw.(RetryCommand)
		if _, err := d.Authorizer.Authorize(ctx, c.TenantID, []string{auth.TenantAdmin, auth.InspectionConfigAdmin, auth.Manager}, nil, true); err != nil {
			return nil, err
		}
		return retry(ctx, d.DB, d.Within, c)
	}); err != nil {
		return err
	}
	return d.Bus.RegisterCommand(ReprocessCommand{}, func(ctx context.Context, raw any) (any, error) {
		c := raw.(ReprocessCommand)
		if _, err := d.Authorizer.Authorize(ctx, c.TenantID, []string{auth.TenantAdmin, auth.InspectionConfigAdmin, auth.Manager}, nil, true); err != nil {
			return nil, err
		}
		return reprocess(ctx, d.DB, d.Within, c)
	})
}

func runWithin(ctx context.Context, db *gorm.DB, within func(context.Context, identity.ID, func(*gorm.DB) error) error, tenantID identity.ID, fn func(*gorm.DB) error) error {
	if within != nil {
		return within(ctx, tenantID, fn)
	}
	return (tenanttx.Runner{DB: db}).Within(ctx, tenantID, fn)
}

func load(ctx context.Context, db *gorm.DB, within func(context.Context, identity.ID, func(*gorm.DB) error) error, q Query) (Status, error) {
	var status Status
	err := runWithin(ctx, db, within, q.TenantID, func(tx *gorm.DB) error {
		var job database.VideoPreparationJob
		if err := tx.WithContext(ctx).Where("tenant_id=? AND media_id=?", q.TenantID, q.MediaID).Order("created_at DESC, id DESC").Take(&job).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return apperror.New(apperror.NotFound, "mediaId", "video preparation not found")
		} else if err != nil {
			return err
		}
		var source database.VideoSource
		sourceBlocked := false
		sourceAvailable := false
		retainedBank := false
		if err := tx.WithContext(ctx).Where("tenant_id=? AND media_id=?", q.TenantID, q.MediaID).Take(&source).Error; err == nil {
			sourceAvailable = source.Status != "EXPIRED" && source.Status != "DELETING"
			sourceBlocked = source.ScreeningStatus == "BLOCKED"
			if source.FirstFrameReadyAt != nil && (source.ScreeningStatus == "APPROVED" || source.ScreeningStatus == "DISABLED") {
				var frameCount int64
				if err := tx.WithContext(ctx).Model(&database.VideoFrame{}).Where("tenant_id=? AND preparation_id=?", q.TenantID, job.ID).Count(&frameCount).Error; err != nil {
					return err
				}
				retainedBank = frameCount > 0
			}
		} else if err != gorm.ErrRecordNotFound {
			return err
		} else {
			var media database.MediaObject
			if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", q.TenantID, q.MediaID).Take(&media).Error; err != nil {
				return err
			}
			sourceAvailable = media.ObjectKey != "" && media.Status != "EXPIRED" && media.Status != "DELETED" && media.Status != "REJECTED"
		}
		status = Status{
			MediaID: job.MediaID, PreparationID: job.ID, Status: job.Status, Stage: job.Phase,
			PreparationVersion: job.PreparationVersion, Attempts: job.Attempts,
			ProcessedFrames: job.ProcessedFrames, ErrorCode: job.ErrorCode,
			Retryable:       job.Status == "FAILED" && (sourceAvailable || retainedBank) && !sourceBlocked,
			SourceAvailable: sourceAvailable, ManifestDigest: job.ManifestDigest,
			FirstFrameReadyAt: job.FirstReadyAt,
		}
		if source.ID != (identity.ID{}) {
			status.SourceExpiryAt = source.SourceExpiryAt
		}
		if job.Status == "READY" {
			status.Stage = "READY"
		}
		if job.Status == "FAILED" && status.Stage == "" {
			status.Stage = "FAILED"
		}
		return nil
	})
	return status, err
}

func retry(ctx context.Context, db *gorm.DB, within func(context.Context, identity.ID, func(*gorm.DB) error) error, c RetryCommand) (Status, error) {
	if c.TenantID == (identity.ID{}) || c.MediaID == (identity.ID{}) || c.MutationID == "" || c.ExpectedPreparationVersion < 1 {
		return Status{}, apperror.New(apperror.InvalidInput, "input", "invalid preparation retry")
	}
	meta, _ := requestctx.FromContext(ctx)
	var status Status
	err := runWithin(ctx, db, within, c.TenantID, func(tx *gorm.DB) error {
		var job database.VideoPreparationJob
		if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND media_id=?", c.TenantID, c.MediaID).Order("created_at DESC, id DESC").Take(&job).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return apperror.New(apperror.NotFound, "mediaId", "video preparation not found")
		} else if err != nil {
			return err
		}
		if job.PreparationVersion != c.ExpectedPreparationVersion {
			return apperror.New(apperror.Conflict, "expectedPreparationVersion", "video preparation has changed")
		}
		if job.Status != "FAILED" {
			return apperror.New(apperror.InvalidState, "mediaId", "video preparation cannot be retried")
		}
		var source database.VideoSource
		if err := tx.WithContext(ctx).Where("tenant_id=? AND media_id=?", c.TenantID, c.MediaID).Take(&source).Error; err == nil {
			if source.Status == "DELETING" || (source.Status == "EXPIRED" && source.FirstFrameReadyAt == nil) {
				return apperror.New(apperror.InvalidState, "mediaId", "video source is no longer available; capture a replacement video")
			}
			if source.ScreeningStatus == "BLOCKED" {
				return apperror.New(apperror.InvalidState, "mediaId", "video source is blocked pending review")
			}
			if source.Status == "EXPIRED" {
				var frameCount int64
				if err := tx.WithContext(ctx).Model(&database.VideoFrame{}).Where("tenant_id=? AND preparation_id=?", c.TenantID, job.ID).Count(&frameCount).Error; err != nil {
					return err
				}
				if source.FirstFrameReadyAt == nil || frameCount == 0 || (source.ScreeningStatus != "APPROVED" && source.ScreeningStatus != "DISABLED") {
					return apperror.New(apperror.InvalidState, "mediaId", "video source is no longer available; capture a replacement video")
				}
			}
		} else if err != gorm.ErrRecordNotFound {
			return err
		} else {
			var media database.MediaObject
			if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", c.TenantID, c.MediaID).Take(&media).Error; err != nil {
				return err
			}
			if media.ObjectKey == "" || media.Status == "EXPIRED" || media.Status == "DELETED" || media.Status == "REJECTED" {
				return apperror.New(apperror.InvalidState, "mediaId", "video source is no longer available; capture a replacement video")
			}
		}
		now := time.Now().UTC()
		nextVersion := c.ExpectedPreparationVersion + 1
		result := tx.WithContext(ctx).Model(&job).Where("tenant_id=? AND id=? AND preparation_version=? AND status='FAILED'", c.TenantID, job.ID, c.ExpectedPreparationVersion).
			Updates(map[string]any{"status": "QUEUED", "preparation_version": nextVersion, "attempts": 0, "next_attempt_at": now, "lease_token": nil, "lease_expires_at": nil, "heartbeat_at": nil, "error_code": ""})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return apperror.New(apperror.Conflict, "expectedPreparationVersion", "video preparation has changed")
		}
		if meta.Principal.IdentityID != (identity.ID{}) {
			if err := tx.WithContext(ctx).Create(&database.AuditEvent{ID: identity.NewID(), TenantID: c.TenantID, ActorID: meta.Principal.IdentityID, Action: "video.preparation_retried", TargetType: "media", TargetID: c.MediaID.String(), Outcome: "ACCEPTED", CorrelationID: meta.CorrelationID, OccurredAt: now}).Error; err != nil {
				return err
			}
		}
		job.Status, job.PreparationVersion, job.Attempts = "QUEUED", nextVersion, 0
		job.ErrorCode, job.NextAttemptAt = "", now
		status = Status{MediaID: c.MediaID, PreparationID: job.ID, Status: job.Status, Stage: job.Phase, PreparationVersion: job.PreparationVersion, Attempts: job.Attempts, ProcessedFrames: job.ProcessedFrames, SourceAvailable: true}
		return nil
	})
	return status, err
}

func reprocess(ctx context.Context, db *gorm.DB, within func(context.Context, identity.ID, func(*gorm.DB) error) error, c ReprocessCommand) (Status, error) {
	if c.TenantID == (identity.ID{}) || c.MediaID == (identity.ID{}) || c.MutationID == "" || len(c.MutationID) > 128 || c.ExpectedPreparationVersion < 1 {
		return Status{}, apperror.New(apperror.InvalidInput, "input", "invalid video preparation reprocess request")
	}
	profile, err := video.ProfileByDigest(c.ProfileDigest)
	if err != nil {
		return Status{}, apperror.New(apperror.InvalidInput, "profileDigest", "unknown deployed video profile")
	}
	c.ProfileDigest = profile.Digest()
	meta, _ := requestctx.FromContext(ctx)
	var status Status
	err = runWithin(ctx, db, within, c.TenantID, func(tx *gorm.DB) error {
		var replay database.VideoPreparationJob
		if err := tx.WithContext(ctx).Where("tenant_id=? AND client_mutation_id=?", c.TenantID, c.MutationID).Take(&replay).Error; err == nil {
			if replay.MediaID != c.MediaID || replay.ProfileDigest != c.ProfileDigest {
				return apperror.New(apperror.Conflict, "clientMutationId", "mutation id was already used for another reprocess request")
			}
			status = statusForJob(replay)
			return nil
		} else if err != gorm.ErrRecordNotFound {
			return err
		}
		var sourceJob database.VideoPreparationJob
		if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND media_id=?", c.TenantID, c.MediaID).Order("created_at DESC, id DESC").Take(&sourceJob).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return apperror.New(apperror.NotFound, "mediaId", "video preparation not found")
			}
			return err
		}
		if err := tx.WithContext(ctx).Where("tenant_id=? AND client_mutation_id=?", c.TenantID, c.MutationID).Take(&replay).Error; err == nil {
			if replay.MediaID != c.MediaID || replay.ProfileDigest != c.ProfileDigest {
				return apperror.New(apperror.Conflict, "clientMutationId", "mutation id was already used for another reprocess request")
			}
			status = statusForJob(replay)
			return nil
		} else if err != gorm.ErrRecordNotFound {
			return err
		}
		if sourceJob.PreparationVersion != c.ExpectedPreparationVersion {
			return apperror.New(apperror.Conflict, "expectedPreparationVersion", "video preparation has changed")
		}
		if sourceJob.Status == "QUEUED" || sourceJob.Status == "PROCESSING" {
			return apperror.New(apperror.InvalidState, "mediaId", "video preparation is already active")
		}
		if sourceJob.Status != "READY" && sourceJob.Status != "FAILED" {
			return apperror.New(apperror.InvalidState, "mediaId", "video preparation cannot be reprocessed")
		}
		var active int64
		if err := tx.WithContext(ctx).Model(&database.VideoPreparationJob{}).Where("tenant_id=? AND media_id=? AND status IN ('QUEUED','PROCESSING')", c.TenantID, c.MediaID).Count(&active).Error; err != nil {
			return err
		}
		if active != 0 {
			return apperror.New(apperror.InvalidState, "mediaId", "video preparation is already active")
		}
		var media database.MediaObject
		if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", c.TenantID, c.MediaID).Take(&media).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return apperror.New(apperror.NotFound, "mediaId", "video preparation not found")
			}
			return err
		}
		if media.ObjectKey == "" || media.Status == "EXPIRED" || media.Status == "DELETED" || media.Status == "REJECTED" {
			return apperror.New(apperror.InvalidState, "mediaId", "video source is no longer available; capture a replacement video")
		}
		var source database.VideoSource
		if err := tx.WithContext(ctx).Where("tenant_id=? AND media_id=?", c.TenantID, c.MediaID).Take(&source).Error; err != nil && err != gorm.ErrRecordNotFound {
			return err
		}
		if source.Status == "DELETING" || source.Status == "EXPIRED" || source.ScreeningStatus == "BLOCKED" {
			return apperror.New(apperror.InvalidState, "mediaId", "video source is unavailable for reprocessing")
		}
		var lastGeneration int
		if err := tx.WithContext(ctx).Model(&database.VideoPreparationJob{}).Select("COALESCE(MAX(generation), 0)").Where("tenant_id=? AND media_id=? AND profile_digest=?", c.TenantID, c.MediaID, c.ProfileDigest).Scan(&lastGeneration).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		anchor := sourceJob.FirstReadyAt
		if anchor == nil {
			anchor = source.FirstFrameReadyAt
		}
		job := database.VideoPreparationJob{ID: identity.NewID(), TenantID: c.TenantID, MediaID: c.MediaID, SubmissionID: sourceJob.SubmissionID, Status: "QUEUED", Phase: "RECEIVED", PreparationVersion: 1, Generation: lastGeneration + 1, ProfileDigest: c.ProfileDigest, ClientMutationID: c.MutationID, NextAttemptAt: now, FirstReadyAt: anchor, PendingObjectKeys: []byte("[]"), CreatedAt: now}
		if err := tx.WithContext(ctx).Create(&job).Error; err != nil {
			return err
		}
		if meta.Principal.IdentityID != (identity.ID{}) {
			if err := tx.WithContext(ctx).Create(&database.AuditEvent{ID: identity.NewID(), TenantID: c.TenantID, ActorID: meta.Principal.IdentityID, Action: "video.preparation_reprocessed", TargetType: "media", TargetID: c.MediaID.String(), Outcome: "ACCEPTED", CorrelationID: meta.CorrelationID, OccurredAt: now}).Error; err != nil {
				return err
			}
		}
		status = statusForJob(job)
		return nil
	})
	return status, err
}

func statusForJob(job database.VideoPreparationJob) Status {
	stage := job.Phase
	if job.Status == "READY" || job.Status == "FAILED" {
		stage = job.Status
	}
	return Status{MediaID: job.MediaID, PreparationID: job.ID, Status: job.Status, Stage: stage, PreparationVersion: job.PreparationVersion, Attempts: job.Attempts, ProcessedFrames: job.ProcessedFrames, ErrorCode: job.ErrorCode, Retryable: job.Status == "FAILED", SourceAvailable: true, ManifestDigest: job.ManifestDigest, FirstFrameReadyAt: job.FirstReadyAt}
}
