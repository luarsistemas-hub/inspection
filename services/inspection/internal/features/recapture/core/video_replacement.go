package core

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	invitationcore "inspection/services/inspection/internal/features/invitations/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"
	"inspection/services/inspection/internal/platform/security"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// MaxVideoReplacementWindow bounds how far in the future a replacement deadline may be set.
const MaxVideoReplacementWindow = 30 * 24 * time.Hour

// VideoReplacementDeadlineEvent is the delayed event that expires an unanswered replacement.
const VideoReplacementDeadlineEvent = "video_replacement.deadline_reached.v1"

// VideoReplacementInput asks the participant for a new recording of a blocked video.
type VideoReplacementInput struct {
	TenantID, InspectionID, MediaID, ActorID identity.ID
	DeadlineAt                               time.Time
	Reason, IdempotencyKey                   string
	Delivery                                 []invitationcore.DeliveryIntent
}

// VideoReplacementResult describes the replacement request and its one-time link token.
type VideoReplacementResult struct {
	ID, ResponsibilityID, InvitationID identity.ID
	Status                             string
	DeadlineAt                         time.Time
	LinkToken                          string
}

// RequestVideoReplacement creates a replacement capture on the same inspection.
// The blocked plan stays BLOCKED until a replacement video is submitted, so an
// expired request returns the inspection to review with every action available.
func (s Service) RequestVideoReplacement(ctx context.Context, in VideoReplacementInput) (VideoReplacementResult, error) {
	in.Reason = strings.TrimSpace(in.Reason)
	in.IdempotencyKey = strings.TrimSpace(in.IdempotencyKey)
	now := s.now()
	switch {
	case in.TenantID == (identity.ID{}) || in.InspectionID == (identity.ID{}) || in.MediaID == (identity.ID{}):
		return VideoReplacementResult{}, apperror.New(apperror.InvalidInput, "input", "inspection and media are required")
	case in.Reason == "" || len([]rune(in.Reason)) > 2000:
		return VideoReplacementResult{}, apperror.New(apperror.InvalidInput, "reason", "reason is required and must have at most 2000 characters")
	case in.IdempotencyKey == "" || len(in.IdempotencyKey) > 128:
		return VideoReplacementResult{}, apperror.New(apperror.InvalidInput, "clientMutationId", "client mutation id is required")
	case !in.DeadlineAt.After(now) || in.DeadlineAt.After(now.Add(MaxVideoReplacementWindow)):
		return VideoReplacementResult{}, apperror.New(apperror.InvalidInput, "deadlineAt", "deadline must be in the next 30 days")
	case len(in.Delivery) == 0:
		return VideoReplacementResult{}, apperror.New(apperror.InvalidState, "participant", "a selected verified channel is required")
	}
	token, err := security.NewScopedToken(in.TenantID)
	if err != nil {
		return VideoReplacementResult{}, err
	}
	hash := security.HashToken(token)
	deliveries, _ := json.Marshal(in.Delivery)
	deadline := in.DeadlineAt.UTC()
	out := VideoReplacementResult{Status: "REQUESTED", DeadlineAt: deadline, LinkToken: token}
	err = s.within(ctx, in.TenantID, func(tx *gorm.DB) error {
		var existing database.VideoReplacement
		if err := tx.Where("tenant_id=? AND idempotency_key=?", in.TenantID, in.IdempotencyKey).Take(&existing).Error; err == nil {
			if existing.InspectionID != in.InspectionID || existing.MediaID != in.MediaID {
				return apperror.New(apperror.Conflict, "clientMutationId", "client mutation id was already used with different input")
			}
			out = VideoReplacementResult{ID: existing.ID, ResponsibilityID: existing.ResponsibilityID, InvitationID: existing.InvitationID, Status: existing.Status, DeadlineAt: existing.DeadlineAt}
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var inspection database.Inspection
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=?", in.TenantID, in.InspectionID).Take(&inspection).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return apperror.New(apperror.NotFound, "inspectionId", "inspection not found")
		} else if err != nil {
			return err
		}
		if inspection.Status != "REVIEW_REQUIRED" && inspection.Status != "PROCESSING_FAILED" {
			return apperror.New(apperror.InvalidState, "inspectionId", "a new recording can only be requested for an inspection that needs attention")
		}
		if _, err := blockedPlanForMedia(tx, in.TenantID, in.InspectionID, in.MediaID); err != nil {
			return err
		}
		var media database.MediaObject
		if err := tx.Where("tenant_id=? AND id=? AND media_kind='VIDEO'", in.TenantID, in.MediaID).Take(&media).Error; err != nil {
			return apperror.New(apperror.InvalidInput, "mediaId", "media is not the blocked inspection video")
		}
		var original database.CaptureDraft
		if err := tx.Where("tenant_id=? AND responsibility_id=? AND media_kind='VIDEO'", in.TenantID, media.ResponsibilityID).Take(&original).Error; err != nil {
			return apperror.New(apperror.InvalidState, "mediaId", "original video capture was not found")
		}
		requirements, err := mainVideoRequirement(original.Requirements)
		if err != nil {
			return err
		}
		responsibility := database.Responsibility{ID: identity.NewID(), TenantID: in.TenantID, InspectionID: in.InspectionID, ParticipantID: inspection.ParticipantID, Status: "PENDING", Version: 1, CreatedAt: now, UpdatedAt: now}
		draft := database.CaptureDraft{ID: identity.NewID(), TenantID: in.TenantID, ResponsibilityID: responsibility.ID, Kind: "VIDEO_REPLACEMENT", MediaKind: "VIDEO", TemplateVersionID: original.TemplateVersionID, ReferencePayload: original.ReferencePayload, PolicyPayload: original.PolicyPayload, Requirements: requirements, Status: "OPEN", Version: 1, CreatedAt: now, UpdatedAt: now}
		invite := database.Invitation{ID: identity.NewID(), TenantID: in.TenantID, ResponsibilityID: responsibility.ID, TokenHash: hash[:], DeliveryIntents: deliveries, Status: "ACTIVE", ExpiresAt: deadline, CreatedAt: now}
		replacement := database.VideoReplacement{ID: identity.NewID(), TenantID: in.TenantID, InspectionID: in.InspectionID, MediaID: in.MediaID, ResponsibilityID: responsibility.ID, InvitationID: invite.ID, DeadlineAt: deadline, Reason: in.Reason, Status: "REQUESTED", IdempotencyKey: in.IdempotencyKey, CreatedAt: now, UpdatedAt: now}
		for _, row := range []any{&responsibility, &draft, &invite, &replacement} {
			if err := tx.Create(row).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&inspection).Updates(map[string]any{"status": "REPLACEMENT_PENDING", "state_reason": in.Reason, "version": gorm.Expr("version + 1"), "updated_at": now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.AuditEvent{ID: identity.NewID(), TenantID: in.TenantID, ActorID: in.ActorID, Action: "video.replacement_requested", TargetType: "inspection", TargetID: in.InspectionID.String(), Outcome: "ACCEPTED", Reason: in.Reason, CorrelationID: "video-replacement-" + replacement.ID.String(), OccurredAt: now}).Error; err != nil {
			return err
		}
		if err := scheduleReplacementDeadline(tx, in.TenantID, replacement, deadline, now); err != nil {
			return err
		}
		out.ID, out.ResponsibilityID, out.InvitationID = replacement.ID, responsibility.ID, invite.ID
		return nil
	})
	if err != nil {
		return VideoReplacementResult{}, err
	}
	return out, nil
}

// FinalizeVideoReplacementTx accepts a timely replacement submission and resumes analysis.
func (s Service) FinalizeVideoReplacementTx(tx *gorm.DB, draft database.CaptureDraft, submission database.SubmissionVersion) error {
	now := submission.SubmittedAt.UTC()
	var replacement database.VideoReplacement
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND responsibility_id=?", draft.TenantID, draft.ResponsibilityID).Take(&replacement).Error; err != nil {
		return apperror.New(apperror.NotFound, "responsibility", "video replacement not found")
	}
	if replacement.Status != "REQUESTED" || !now.Before(replacement.DeadlineAt) {
		return apperror.New(apperror.InvalidState, "responsibility", "video replacement is no longer accepted")
	}
	var evidenceCount int64
	if err := tx.Model(&database.MediaObject{}).Where("tenant_id=? AND responsibility_id=? AND media_kind='VIDEO' AND status IN ?", draft.TenantID, draft.ResponsibilityID, []string{"RECEIVED", "READY"}).Count(&evidenceCount).Error; err != nil {
		return err
	}
	if err := tx.Model(&replacement).Updates(map[string]any{"status": "SUBMITTED", "updated_at": now}).Error; err != nil {
		return err
	}
	if err := tx.Model(&database.Responsibility{}).Where("tenant_id=? AND id=?", draft.TenantID, draft.ResponsibilityID).Updates(map[string]any{"status": "SUBMITTED", "version": gorm.Expr("version + 1"), "updated_at": now}).Error; err != nil {
		return err
	}
	if err := tx.Model(&database.Invitation{}).Where("tenant_id=? AND responsibility_id=? AND status='ACTIVE'", draft.TenantID, draft.ResponsibilityID).Updates(map[string]any{"status": "REVOKED", "revoked_at": now}).Error; err != nil {
		return err
	}
	if err := tx.Model(&database.VideoPlan{}).Where("tenant_id=? AND inspection_id=? AND status='BLOCKED'", draft.TenantID, replacement.InspectionID).Updates(map[string]any{"status": "FAILED", "error_code": "REPLACED", "lease_token": nil, "lease_until": nil}).Error; err != nil {
		return err
	}
	transition := tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=? AND status='REPLACEMENT_PENDING'", draft.TenantID, replacement.InspectionID).Updates(map[string]any{"status": "ANALYZING", "state_reason": "", "evidence_count": evidenceCount, "version": gorm.Expr("version + 1"), "updated_at": now})
	if transition.Error != nil {
		return transition.Error
	}
	if transition.RowsAffected != 1 {
		return apperror.New(apperror.InvalidState, "inspection", "inspection no longer accepts a replacement video")
	}
	return nil
}

// ExpireVideoReplacementTx closes an unanswered replacement and returns the inspection to attention.
func (s Service) ExpireVideoReplacementTx(tx *gorm.DB, tenantID, replacementID identity.ID) error {
	now := s.now()
	var replacement database.VideoReplacement
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=?", tenantID, replacementID).Take(&replacement).Error; err != nil {
		return err
	}
	if replacement.Status != "REQUESTED" {
		return nil
	}
	if now.Before(replacement.DeadlineAt) {
		// A premature delivery is rescheduled with a fresh event id so it cannot collide with the original.
		return scheduleReplacementDeadlineEvent(tx, tenantID, replacement, replacement.DeadlineAt, now, identity.NewID())
	}
	if err := tx.Model(&replacement).Updates(map[string]any{"status": "EXPIRED", "updated_at": now}).Error; err != nil {
		return err
	}
	if err := tx.Model(&database.CaptureDraft{}).Where("tenant_id=? AND responsibility_id=? AND status='OPEN'", tenantID, replacement.ResponsibilityID).Updates(map[string]any{"status": "EXPIRED", "version": gorm.Expr("version + 1"), "updated_at": now}).Error; err != nil {
		return err
	}
	if err := tx.Model(&database.ExternalSession{}).Where("tenant_id=? AND responsibility_id=? AND revoked_at IS NULL", tenantID, replacement.ResponsibilityID).Update("revoked_at", now).Error; err != nil {
		return err
	}
	if err := tx.Model(&database.Invitation{}).Where("tenant_id=? AND responsibility_id=? AND status='ACTIVE'", tenantID, replacement.ResponsibilityID).Updates(map[string]any{"status": "REVOKED", "revoked_at": now}).Error; err != nil {
		return err
	}
	if err := tx.Model(&database.Responsibility{}).Where("tenant_id=? AND id=?", tenantID, replacement.ResponsibilityID).Updates(map[string]any{"status": "EXPIRED", "version": gorm.Expr("version + 1"), "updated_at": now}).Error; err != nil {
		return err
	}
	target := "REVIEW_REQUIRED"
	var plan database.VideoPlan
	if err := tx.Where("tenant_id=? AND inspection_id=? AND status='BLOCKED'", tenantID, replacement.InspectionID).Order("created_at DESC").Take(&plan).Error; err == nil && plan.ErrorCode != "SENSITIVE_CONTENT" {
		target = "PROCESSING_FAILED"
	} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	return tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=? AND status='REPLACEMENT_PENDING'", tenantID, replacement.InspectionID).Updates(map[string]any{"status": target, "state_reason": "Nova gravação não enviada no prazo", "version": gorm.Expr("version + 1"), "updated_at": now}).Error
}

func blockedPlanForMedia(tx *gorm.DB, tenantID, inspectionID, mediaID identity.ID) (database.VideoPlan, error) {
	var plan database.VideoPlan
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Table("analysis.video_plans AS p").Select("p.*").
		Joins("JOIN media.video_preparation_jobs j ON j.tenant_id=p.tenant_id AND j.id=p.current_preparation_id").
		Where("p.tenant_id=? AND p.inspection_id=? AND p.status='BLOCKED' AND j.media_id=?", tenantID, inspectionID, mediaID).
		Order("p.created_at DESC").Take(&plan).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return plan, apperror.New(apperror.InvalidInput, "mediaId", "media is not the blocked inspection video")
	}
	return plan, err
}

func mainVideoRequirement(raw json.RawMessage) (json.RawMessage, error) {
	var requirements []capturecore.Requirement
	if err := json.Unmarshal(raw, &requirements); err != nil {
		return nil, apperror.Wrap(apperror.InvalidState, err)
	}
	for _, requirement := range requirements {
		if requirement.Key == "video:main" {
			requirement.Required = true
			requirement.ImpossibilityAllowed = false
			encoded, _ := json.Marshal([]capturecore.Requirement{requirement})
			return encoded, nil
		}
	}
	return nil, apperror.New(apperror.InvalidState, "mediaId", "original capture has no main video requirement")
}

func scheduleReplacementDeadline(tx *gorm.DB, tenantID identity.ID, replacement database.VideoReplacement, at, now time.Time) error {
	return scheduleReplacementDeadlineEvent(tx, tenantID, replacement, at, now, identity.NewDeterministicID("video-replacement-deadline", replacement.ID.String()))
}

func scheduleReplacementDeadlineEvent(tx *gorm.DB, tenantID identity.ID, replacement database.VideoReplacement, at, now time.Time, id identity.ID) error {
	if err := messaging.AddOutbox(tx, events.Envelope[map[string]any]{ID: id, Type: VideoReplacementDeadlineEvent, SchemaVersion: 1, OccurredAt: now, TenantID: tenantID, AggregateID: replacement.ID, CorrelationID: "video-replacement-deadline-" + replacement.ID.String(), Payload: map[string]any{"replacementId": replacement.ID, "deadlineAt": replacement.DeadlineAt}}); err != nil {
		return err
	}
	return tx.Model(&database.OutboxIntent{}).Where("tenant_id=? AND id=?", tenantID, id).Update("next_attempt_at", at).Error
}
