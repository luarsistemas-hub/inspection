// Package core owns media admission, multipart state and screening decisions.
package core

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/sensitivecontent"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const UploadTTL = 24 * time.Hour
const MaxScreeningAttempts = 3

type Service struct {
	DB       *gorm.DB
	Store    objectstore.Store
	Detector sensitivecontent.Detector
	Now      func() time.Time
	Within   func(context.Context, identity.ID, func(*gorm.DB) error) error
}

type Upload struct {
	MediaID             identity.ID
	UploadID, ObjectKey string
	ExpiresAt           time.Time
}

func ValidateAdmission(contentType string, size int64, active int) error {
	if !objectstore.SupportedType(contentType) {
		return apperror.New(apperror.InvalidInput, "contentType", "supported media is required")
	}
	if size <= 0 || size > objectstore.MaxOriginalBytes {
		return apperror.New(apperror.InvalidInput, "sizeBytes", "original must be at most 20 MiB")
	}
	if active >= capturecore.MaxActivePhotos {
		return apperror.New(apperror.InvalidState, "media", "active photo limit reached")
	}
	return nil
}

func (s Service) Create(ctx context.Context, tenantID, responsibilityID identity.ID, contentType, hash string, size int64) (Upload, error) {
	return s.CreateWithKey(ctx, tenantID, responsibilityID, contentType, hash, size, "media-"+identity.NewID().String())
}

func (s Service) CreateWithKey(ctx context.Context, tenantID, responsibilityID identity.ID, contentType, hash string, size int64, idempotencyKey string) (Upload, error) {
	if tenantID == (identity.ID{}) || responsibilityID == (identity.ID{}) || len(hash) != 64 || strings.TrimSpace(idempotencyKey) == "" {
		return Upload{}, apperror.New(apperror.InvalidInput, "input", "invalid media request")
	}
	if _, err := hex.DecodeString(hash); err != nil {
		return Upload{}, apperror.New(apperror.InvalidInput, "sha256", "sha256 must be hexadecimal")
	}
	if err := ValidateAdmission(contentType, size, 0); err != nil {
		return Upload{}, err
	}
	var existing database.MediaObject
	var existingUpload database.MultipartUpload
	if err := s.within(ctx, tenantID, func(tx *gorm.DB) error {
		err := tx.Where("tenant_id=? AND responsibility_id=? AND idempotency_key=?", tenantID, responsibilityID, idempotencyKey).First(&existing).Error
		if err != nil {
			return err
		}
		return tx.Where("tenant_id=? AND media_id=?", tenantID, existing.ID).First(&existingUpload).Error
	}); err == nil {
		if existing.ContentType != contentType || existing.SizeBytes != size || !strings.EqualFold(existing.SHA256, hash) {
			return Upload{}, apperror.New(apperror.Conflict, "clientMutationId", "media request payload changed")
		}
		return Upload{MediaID: existing.ID, UploadID: existingUpload.UploadID, ObjectKey: existing.ObjectKey, ExpiresAt: existingUpload.ExpiresAt}, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return Upload{}, err
	}
	mediaID := identity.NewID()
	var key, remoteID string
	var replay *Upload
	now := s.now()
	expires := now.Add(UploadTTL)
	err := s.within(ctx, tenantID, func(tx *gorm.DB) error {
		var draft database.CaptureDraft
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND responsibility_id=? AND status='OPEN'", tenantID, responsibilityID).First(&draft).Error; err != nil {
			return apperror.New(apperror.InvalidState, "responsibility", "capture is unavailable")
		}
		if err := tx.Where("tenant_id=? AND responsibility_id=? AND idempotency_key=?", tenantID, responsibilityID, idempotencyKey).First(&existing).Error; err == nil {
			if existing.ContentType != contentType || existing.SizeBytes != size || !strings.EqualFold(existing.SHA256, hash) {
				return apperror.New(apperror.Conflict, "clientMutationId", "media request payload changed")
			}
			if err := tx.Where("tenant_id=? AND media_id=?", tenantID, existing.ID).First(&existingUpload).Error; err != nil {
				return err
			}
			replay = &Upload{MediaID: existing.ID, UploadID: existingUpload.UploadID, ObjectKey: existing.ObjectKey, ExpiresAt: existingUpload.ExpiresAt}
			return nil
		} else if err != gorm.ErrRecordNotFound {
			return err
		}
		var active int64
		if err := tx.Model(&database.MediaObject{}).Where("tenant_id=? AND responsibility_id=? AND status NOT IN ?", tenantID, responsibilityID, []string{"ABORTED", "PURGED"}).Count(&active).Error; err != nil {
			return err
		}
		if err := ValidateAdmission(contentType, size, int(active)); err != nil {
			return err
		}
		var err error
		key, remoteID, err = s.Store.CreateMultipart(ctx, tenantID, mediaID, contentType)
		if err != nil {
			return dependency(err)
		}
		media := database.MediaObject{ID: mediaID, TenantID: tenantID, ResponsibilityID: responsibilityID, ObjectKey: key, ContentType: contentType, SHA256: strings.ToLower(hash), SizeBytes: size, Status: "UPLOADING", IdempotencyKey: idempotencyKey, Flags: json.RawMessage(`[]`), CreatedAt: now}
		upload := database.MultipartUpload{ID: identity.NewID(), TenantID: tenantID, MediaID: mediaID, UploadID: remoteID, ObjectKey: key, Status: "UPLOADING", ExpiresAt: expires, CreatedAt: now}
		if err := tx.Create(&media).Error; err != nil {
			return err
		}
		return tx.Create(&upload).Error
	})
	if err != nil {
		// A concurrent request may have won the unique idempotency index after
		// this transaction checked for an existing row. Re-read its durable
		// result so retries return the original upload instead of a false conflict.
		var winner database.MediaObject
		var winnerUpload database.MultipartUpload
		if readErr := s.within(ctx, tenantID, func(tx *gorm.DB) error {
			if err := tx.Where("tenant_id=? AND responsibility_id=? AND idempotency_key=?", tenantID, responsibilityID, idempotencyKey).First(&winner).Error; err != nil {
				return err
			}
			return tx.Where("tenant_id=? AND media_id=?", tenantID, winner.ID).First(&winnerUpload).Error
		}); readErr == nil && winner.ContentType == contentType && winner.SizeBytes == size && strings.EqualFold(winner.SHA256, hash) {
			if remoteID != "" {
				_ = s.Store.Client.AbortMultipart(ctx, s.Store.Bucket, key, remoteID)
			}
			return Upload{MediaID: winner.ID, UploadID: winnerUpload.UploadID, ObjectKey: winnerUpload.ObjectKey, ExpiresAt: winnerUpload.ExpiresAt}, nil
		}
		if remoteID != "" {
			_ = s.Store.Client.AbortMultipart(ctx, s.Store.Bucket, key, remoteID)
		}
		return Upload{}, err
	}
	if replay != nil {
		return *replay, nil
	}
	return Upload{MediaID: mediaID, UploadID: remoteID, ObjectKey: key, ExpiresAt: expires}, nil
}

func (s Service) Presign(ctx context.Context, tenantID, responsibilityID, mediaID identity.ID, parts []int) ([]objectstore.PresignedPart, error) {
	var upload database.MultipartUpload
	var media database.MediaObject
	err := s.within(ctx, tenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND id=? AND responsibility_id=?", tenantID, mediaID, responsibilityID).First(&media).Error; err != nil {
			return apperror.New(apperror.NotFound, "mediaId", "media not found")
		}
		return tx.Where("tenant_id=? AND media_id=?", tenantID, mediaID).First(&upload).Error
	})
	if err != nil || upload.Status != "UPLOADING" || !s.now().Before(upload.ExpiresAt) {
		return nil, apperror.New(apperror.SessionExpired, "mediaId", "upload unavailable")
	}
	allowedParts := objectstore.ExpectedParts(media.SizeBytes)
	seen := make(map[int]struct{}, len(parts))
	if len(parts) == 0 {
		return nil, apperror.New(apperror.InvalidInput, "partNumbers", "at least one upload part is required")
	}
	for _, part := range parts {
		if part < 1 || part > allowedParts {
			return nil, apperror.New(apperror.InvalidInput, "partNumbers", "upload part is outside the expected set")
		}
		if _, exists := seen[part]; exists {
			return nil, apperror.New(apperror.InvalidInput, "partNumbers", "upload parts must be unique")
		}
		seen[part] = struct{}{}
	}
	result := make([]objectstore.PresignedPart, 0, len(parts))
	for _, part := range parts {
		signed, err := s.Store.PresignPart(ctx, upload.ObjectKey, upload.UploadID, part, 15*time.Minute)
		if err != nil {
			return nil, dependency(err)
		}
		result = append(result, signed)
	}
	return result, nil
}

func (s Service) Complete(ctx context.Context, tenantID, responsibilityID, mediaID identity.ID, parts []objectstore.Part) (database.MediaObject, error) {
	var media database.MediaObject
	var upload database.MultipartUpload
	err := s.within(ctx, tenantID, func(tx *gorm.DB) error {
		var draft database.CaptureDraft
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND responsibility_id=?", tenantID, responsibilityID).First(&draft).Error; err != nil {
			return apperror.New(apperror.NotFound, "responsibility", "capture not found")
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=? AND responsibility_id=?", tenantID, mediaID, responsibilityID).First(&media).Error; err != nil {
			return apperror.New(apperror.NotFound, "mediaId", "media not found")
		}
		if media.Status == "VERIFIED" || media.Status == "SCREENED" || media.Status == "READY" {
			return nil
		}
		if draft.Status != "OPEN" || media.Status != "UPLOADING" {
			return apperror.New(apperror.InvalidState, "mediaId", "upload is not active")
		}
		if err := tx.Where("tenant_id=? AND media_id=?", tenantID, mediaID).First(&upload).Error; err != nil {
			return err
		}
		if !s.now().Before(upload.ExpiresAt) {
			return apperror.New(apperror.SessionExpired, "mediaId", "upload expired")
		}
		if err := s.Store.Complete(ctx, upload.ObjectKey, upload.UploadID, parts, media.SizeBytes, media.ContentType, media.SHA256); err != nil {
			return dependency(err)
		}
		now := s.now()
		if err := tx.Model(&database.MediaObject{}).Where("tenant_id=? AND id=? AND status='UPLOADING'", tenantID, mediaID).Update("status", "VERIFIED").Error; err != nil {
			return err
		}
		if err := tx.Model(&database.MultipartUpload{}).Where("tenant_id=? AND media_id=?", tenantID, mediaID).Updates(map[string]any{"status": "COMPLETED", "completed_at": now}).Error; err != nil {
			return err
		}
		for _, eventType := range []string{"media.upload_completed.v1", "media.verified.v1"} {
			eventID := identity.NewID()
			if err := messaging.AddOutbox(tx, events.Envelope[map[string]any]{ID: eventID, Type: eventType, SchemaVersion: 1, OccurredAt: now, TenantID: tenantID, AggregateID: mediaID, CorrelationID: "media-complete-" + mediaID.String(), Payload: map[string]any{"mediaId": mediaID, "responsibilityId": responsibilityID, "status": "VERIFIED"}}); err != nil {
				return err
			}
		}
		media.Status = "VERIFIED"
		return nil
	})
	return media, err
}

func (s Service) Screen(ctx context.Context, tenantID, mediaID identity.ID, data []byte) (database.ScreeningRun, error) {
	var prior database.ScreeningRun
	err := s.within(ctx, tenantID, func(tx *gorm.DB) error {
		var media database.MediaObject
		if err := tx.Where("tenant_id=? AND id=?", tenantID, mediaID).First(&media).Error; err != nil {
			return apperror.New(apperror.NotFound, "mediaId", "media not found")
		}
		if err := tx.Where("tenant_id=? AND media_id=?", tenantID, mediaID).Order("created_at desc").First(&prior).Error; err == nil && (prior.Status == "CLEARED" || prior.Status == "BLOCKED" || prior.Status == "OVERRIDDEN") {
			return nil
		} else if err != nil && err != gorm.ErrRecordNotFound {
			return err
		}
		var attempts int64
		if err := tx.Model(&database.ScreeningRun{}).Where("tenant_id=? AND media_id=?", tenantID, mediaID).Count(&attempts).Error; err != nil {
			return err
		}
		if attempts >= MaxScreeningAttempts {
			return apperror.New(apperror.RateLimited, "mediaId", "screening retry limit reached")
		}
		if media.Status != "VERIFIED" {
			return apperror.New(apperror.InvalidState, "mediaId", "media verification is required")
		}
		return nil
	})
	if err != nil {
		return database.ScreeningRun{}, err
	}
	if prior.ID != (identity.ID{}) && (prior.Status == "CLEARED" || prior.Status == "BLOCKED" || prior.Status == "OVERRIDDEN") {
		return prior, nil
	}
	result, err := s.Detector.Detect(ctx, data)
	if err != nil {
		failureErr := s.within(ctx, tenantID, func(tx *gorm.DB) error {
			var media database.MediaObject
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=?", tenantID, mediaID).First(&media).Error; err != nil {
				return apperror.New(apperror.NotFound, "mediaId", "media not found")
			}
			var terminal database.ScreeningRun
			if queryErr := tx.Where("tenant_id=? AND media_id=? AND status IN ?", tenantID, mediaID, []string{"CLEARED", "BLOCKED", "OVERRIDDEN"}).Order("created_at desc").First(&terminal).Error; queryErr == nil {
				return nil
			} else if queryErr != gorm.ErrRecordNotFound {
				return queryErr
			}
			attempts := database.ScreeningRun{ID: identity.NewID(), TenantID: tenantID, MediaID: mediaID, Status: "FAILED", ModelDigest: "detector-error", Regions: json.RawMessage(`[]`), CreatedAt: s.now()}
			return tx.Create(&attempts).Error
		})
		if failureErr != nil {
			return database.ScreeningRun{}, failureErr
		}
		return database.ScreeningRun{}, dependency(err)
	}
	regions, _ := json.Marshal(result.Regions)
	status := "CLEARED"
	mediaStatus := "READY"
	if len(result.Regions) > 0 {
		status, mediaStatus = "BLOCKED", "SCREENED"
	}
	run := database.ScreeningRun{ID: identity.NewID(), TenantID: tenantID, MediaID: mediaID, Status: status, ModelDigest: result.ModelDigest, Regions: regions, CreatedAt: s.now()}
	err = s.within(ctx, tenantID, func(tx *gorm.DB) error {
		var media database.MediaObject
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=?", tenantID, mediaID).First(&media).Error; err != nil {
			return apperror.New(apperror.NotFound, "mediaId", "media not found")
		}
		var prior database.ScreeningRun
		if err := tx.Where("tenant_id=? AND media_id=?", tenantID, mediaID).Order("created_at desc").First(&prior).Error; err == nil && (prior.Status == "CLEARED" || prior.Status == "BLOCKED" || prior.Status == "OVERRIDDEN") {
			run = prior
			return nil
		}
		if media.Status != "VERIFIED" {
			return apperror.New(apperror.InvalidState, "mediaId", "media verification is required")
		}
		if err := tx.Create(&run).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.MediaObject{}).Where("tenant_id=? AND id=?", tenantID, mediaID).Update("status", mediaStatus).Error; err != nil {
			return err
		}
		eventID := identity.NewID()
		return messaging.AddOutbox(tx, events.Envelope[map[string]any]{ID: eventID, Type: "media.screened.v1", SchemaVersion: 1, OccurredAt: s.now(), TenantID: tenantID, AggregateID: mediaID, CorrelationID: "media-screen-" + mediaID.String(), Payload: map[string]any{"mediaId": mediaID, "status": status, "modelDigest": result.ModelDigest}})
	})
	return run, err
}

func (s Service) DeclareFalsePositive(ctx context.Context, tenantID, responsibilityID, mediaID identity.ID, reason string) error {
	reason = strings.TrimSpace(reason)
	if err := capturecore.ValidateDescription(reason, true); err != nil {
		return apperror.New(apperror.InvalidInput, "reason", "false-positive reason is required")
	}
	now := s.now()
	return s.within(ctx, tenantID, func(tx *gorm.DB) error {
		var draft database.CaptureDraft
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND responsibility_id=?", tenantID, responsibilityID).First(&draft).Error; err != nil || draft.Status != "OPEN" {
			return apperror.New(apperror.InvalidState, "responsibility", "capture is unavailable")
		}
		var media database.MediaObject
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=? AND responsibility_id=?", tenantID, mediaID, responsibilityID).First(&media).Error; err != nil {
			return apperror.New(apperror.NotFound, "mediaId", "media not found")
		}
		var run database.ScreeningRun
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND media_id=?", tenantID, mediaID).Order("created_at desc").First(&run).Error; err != nil {
			return apperror.New(apperror.InvalidState, "mediaId", "media is not blocked by sensitive-content detection")
		}
		if run.Status == "OVERRIDDEN" && run.OverriddenAt != nil {
			if run.FalsePositiveReason != reason {
				return apperror.New(apperror.Conflict, "reason", "false-positive declaration already exists")
			}
			return nil
		}
		if run.Status != "BLOCKED" {
			return apperror.New(apperror.InvalidState, "mediaId", "media is not blocked by sensitive-content detection")
		}
		if media.Status != "SCREENED" {
			return apperror.New(apperror.InvalidState, "mediaId", "media no longer accepts a screening decision")
		}
		run.Status, run.FalsePositiveReason, run.OverriddenAt = "OVERRIDDEN", reason, &now
		if err := tx.Save(&run).Error; err != nil {
			return err
		}
		var currentFlags []string
		_ = json.Unmarshal(media.Flags, &currentFlags)
		currentFlags = append(currentFlags, "SENSITIVE_CONTENT_FALSE_POSITIVE")
		flags, _ := json.Marshal(unique(currentFlags))
		if err := tx.Model(&media).Updates(map[string]any{"status": "READY", "flags": flags}).Error; err != nil {
			return err
		}
		return tx.Create(&database.AuditEvent{ID: identity.NewID(), TenantID: tenantID, ActorID: responsibilityID, Action: "media.sensitive_false_positive_declared", TargetType: "media", TargetID: mediaID.String(), Outcome: "ACCEPTED", Reason: reason, CorrelationID: "media-false-positive-" + mediaID.String(), OccurredAt: now}).Error
	})
}

func unique(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func (s Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
func (s Service) within(ctx context.Context, tenant identity.ID, fn func(*gorm.DB) error) error {
	if s.Within != nil {
		return s.Within(ctx, tenant, fn)
	}
	return (tenanttx.Runner{DB: s.DB}).Within(ctx, tenant, fn)
}
func dependency(err error) error {
	var app *apperror.Error
	if errors.As(err, &app) {
		return err
	}
	return apperror.Wrap(apperror.DependencyUnavailable, err)
}
