// Package core owns origin invitation, immutable versioning and activation.
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
	"inspection/services/inspection/internal/features/templates/catalog"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"
	"inspection/services/inspection/internal/platform/security"
	"inspection/services/inspection/internal/platform/tenanttx"
	"inspection/services/inspection/internal/platform/video_admission"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Service struct {
	DB     *gorm.DB
	Now    func() time.Time
	Within func(context.Context, identity.ID, func(*gorm.DB) error) error
}
type InviteInput struct {
	TenantID, AssetID, TemplateVersionID identity.ID
	Delivery                             []invitationcore.DeliveryIntent
	Requirements                         []capturecore.Requirement
	Policy, Reference                    json.RawMessage
	ExpiresAt                            time.Time
	IdempotencyKey                       string
}
type Invitation struct {
	OriginID, VersionID, ResponsibilityID, InvitationID identity.ID
	LinkToken                                           string
}

func (s Service) Invite(ctx context.Context, in InviteInput) (Invitation, error) {
	if in.TenantID == (identity.ID{}) || in.AssetID == (identity.ID{}) || in.TemplateVersionID == (identity.ID{}) || len(in.Delivery) == 0 || len(in.Requirements) == 0 || len(in.Requirements) > capturecore.MaxActivePhotos || !in.ExpiresAt.After(s.now()) || strings.TrimSpace(in.IdempotencyKey) == "" {
		return Invitation{}, apperror.New(apperror.InvalidInput, "input", "valid origin context, requirements, channels, and expiry are required")
	}
	seen := make(map[string]struct{}, len(in.Requirements))
	for _, requirement := range in.Requirements {
		key := strings.TrimSpace(requirement.Key)
		if key == "" || requirement.MinimumMedia < 0 || requirement.MaximumMedia < 1 || requirement.MinimumMedia > requirement.MaximumMedia || requirement.MaximumMedia > capturecore.MaxActivePhotos {
			return Invitation{}, apperror.New(apperror.InvalidInput, "requirements", "origin requirements are invalid")
		}
		if _, exists := seen[key]; exists {
			return Invitation{}, apperror.New(apperror.InvalidInput, "requirements", "origin requirement keys must be unique")
		}
		seen[key] = struct{}{}
	}
	token, err := security.NewScopedToken(in.TenantID)
	if err != nil {
		return Invitation{}, err
	}
	hash := security.HashToken(token)
	deliveries, _ := json.Marshal(in.Delivery)
	requirements, _ := json.Marshal(in.Requirements)
	now := s.now()
	out := Invitation{LinkToken: token}
	err = s.within(ctx, in.TenantID, func(tx *gorm.DB) error {
		var existing database.OriginVersion
		if err := tx.Where("tenant_id=? AND idempotency_key=?", in.TenantID, in.IdempotencyKey).First(&existing).Error; err == nil {
			if existing.ResponsibilityID == nil {
				return apperror.New(apperror.InvalidState, "responsibility", "origin version has no capture responsibility")
			}
			var invite database.Invitation
			if err := tx.Where("tenant_id=? AND responsibility_id=?", in.TenantID, *existing.ResponsibilityID).First(&invite).Error; err != nil {
				return err
			}
			out.OriginID, out.VersionID, out.ResponsibilityID, out.InvitationID = existing.OriginID, existing.ID, *existing.ResponsibilityID, invite.ID
			out.LinkToken = ""
			return nil
		} else if err != gorm.ErrRecordNotFound {
			return err
		}
		var templateVersion database.TemplateVersion
		if err := tx.Where("tenant_id=? AND id=?", in.TenantID, in.TemplateVersionID).First(&templateVersion).Error; err != nil {
			return apperror.New(apperror.NotFound, "templateVersionId", "template version not found")
		}
		var definition catalog.TemplateDocument
		if err := json.Unmarshal(templateVersion.DefinitionJSON, &definition); err != nil {
			return apperror.New(apperror.InvalidInput, "templateVersionId", "template definition is invalid")
		}
		if definition.MediaKind() == "VIDEO" {
			if err := video_admission.RequireEnabled(tx); err != nil {
				return err
			}
		}
		var origin database.Origin
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND asset_id=?", in.TenantID, in.AssetID).First(&origin).Error
		if err == gorm.ErrRecordNotFound {
			origin = database.Origin{ID: identity.NewID(), TenantID: in.TenantID, AssetID: in.AssetID, Version: 1, CreatedAt: now, UpdatedAt: now}
			if err = tx.Create(&origin).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		var count int64
		if err := tx.Model(&database.OriginVersion{}).Where("tenant_id=? AND origin_id=?", in.TenantID, origin.ID).Count(&count).Error; err != nil {
			return err
		}
		responsibilityID := identity.NewID()
		version := database.OriginVersion{ID: identity.NewID(), TenantID: in.TenantID, OriginID: origin.ID, VersionNumber: int(count) + 1, ResponsibilityID: &responsibilityID, SupersedesID: origin.ActiveVersionID, Status: "DRAFT", IdempotencyKey: in.IdempotencyKey, CreatedAt: now}
		version.MediaKind = definition.MediaKind()
		draft := database.CaptureDraft{ID: identity.NewID(), TenantID: in.TenantID, ResponsibilityID: responsibilityID, Kind: "ORIGIN", MediaKind: definition.MediaKind(), TemplateVersionID: in.TemplateVersionID, ReferencePayload: defaultJSON(in.Reference), PolicyPayload: defaultJSON(in.Policy), Requirements: requirements, Status: "OPEN", Version: 1, CreatedAt: now, UpdatedAt: now}
		invite := database.Invitation{ID: identity.NewID(), TenantID: in.TenantID, ResponsibilityID: responsibilityID, TokenHash: hash[:], DeliveryIntents: deliveries, Status: "ACTIVE", ExpiresAt: in.ExpiresAt, CreatedAt: now}
		for _, row := range []any{&version, &draft, &invite} {
			if err := tx.Create(row).Error; err != nil {
				return err
			}
		}
		eventID := identity.NewID()
		if err := messaging.AddOutbox(tx, events.Envelope[map[string]any]{ID: eventID, Type: "origin.invitation_requested.v1", SchemaVersion: 1, OccurredAt: now, TenantID: in.TenantID, AggregateID: origin.ID, CorrelationID: "origin-invite-" + version.ID.String(), Payload: map[string]any{"originId": origin.ID, "originVersionId": version.ID, "responsibilityId": responsibilityID}}); err != nil {
			return err
		}
		out.OriginID, out.VersionID, out.ResponsibilityID, out.InvitationID = origin.ID, version.ID, responsibilityID, invite.ID
		return nil
	})
	return out, err
}

func (s Service) ActivateCompleted(ctx context.Context, tenantID, responsibilityID identity.ID) (database.OriginVersion, error) {
	var result database.OriginVersion
	err := s.within(ctx, tenantID, func(tx *gorm.DB) error {
		var err error
		result, err = s.ActivateCompletedTx(tx, tenantID, responsibilityID)
		return err
	})
	return result, err
}

func (s Service) ActivateCompletedTx(tx *gorm.DB, tenantID, responsibilityID identity.ID) (database.OriginVersion, error) {
	var result database.OriginVersion
	now := s.now()
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND responsibility_id=?", tenantID, responsibilityID).First(&result).Error; err != nil {
		return database.OriginVersion{}, apperror.New(apperror.NotFound, "responsibility", "origin capture not found")
	}
	if result.Status == "ACTIVE" || result.Status == "SUPERSEDED" {
		return result, nil
	}
	if result.Status == "INVALIDATED" {
		return database.OriginVersion{}, apperror.New(apperror.InvalidState, "responsibility", "invalidated origin versions cannot be activated")
	}
	var draft database.CaptureDraft
	if err := tx.Where("tenant_id=? AND responsibility_id=?", tenantID, responsibilityID).First(&draft).Error; err != nil || draft.Status != "SUBMITTED" {
		return database.OriginVersion{}, apperror.New(apperror.InvalidState, "responsibility", "origin capture must be submitted before activation")
	}
	if draft.MediaKind == "VIDEO" {
		var media database.MediaObject
		if err := tx.Where("tenant_id=? AND responsibility_id=? AND media_kind='VIDEO'", tenantID, responsibilityID).Order("created_at DESC, id DESC").Take(&media).Error; err != nil {
			return database.OriginVersion{}, apperror.New(apperror.InvalidState, "media", "a received and prepared video origin is required")
		}
		var preparation database.VideoPreparationJob
		if err := tx.Where("tenant_id=? AND media_id=?", tenantID, media.ID).Order("created_at DESC, id DESC").Take(&preparation).Error; err != nil {
			return database.OriginVersion{}, apperror.New(apperror.InvalidState, "media", "video origin preparation is not ready")
		}
		if preparation.ErrorCode == "NO_USABLE_SCOPE" {
			return database.OriginVersion{}, apperror.New(apperror.InvalidState, "media", "video origin has no usable reference scope; capture a replacement video")
		}
		if media.Status != "READY" || preparation.Status != "READY" || preparation.ManifestDigest == "" {
			return database.OriginVersion{}, apperror.New(apperror.InvalidState, "media", "video origin preparation is not ready")
		}
		var source database.VideoSource
		if err := tx.Where("tenant_id=? AND media_id=? AND screening_status IN ('APPROVED','DISABLED')", tenantID, media.ID).Take(&source).Error; err != nil {
			return database.OriginVersion{}, apperror.New(apperror.InvalidState, "media", "video origin screening is not complete")
		}
		var scopeItems []database.VideoScopeItem
		if err := tx.Where("tenant_id=? AND preparation_id=?", tenantID, preparation.ID).Find(&scopeItems).Error; err != nil {
			return database.OriginVersion{}, err
		}
		if len(scopeItems) == 0 {
			return database.OriginVersion{}, apperror.New(apperror.InvalidState, "media", "video origin has no usable retained frame scope; capture a replacement video")
		}
		frameIDs := make([]identity.ID, 0, len(scopeItems))
		for _, item := range scopeItems {
			frameIDs = append(frameIDs, item.FrameID)
		}
		var frames []database.VideoFrame
		if err := tx.Where("tenant_id=? AND preparation_id=? AND source_media_id=? AND id IN ? AND sha256<>'' AND object_key<>''", tenantID, preparation.ID, media.ID, frameIDs).Find(&frames).Error; err != nil {
			return database.OriginVersion{}, err
		}
		validFrames := make(map[identity.ID]struct{}, len(frames))
		for _, frame := range frames {
			validFrames[frame.ID] = struct{}{}
		}
		for _, item := range scopeItems {
			if _, ok := validFrames[item.FrameID]; !ok {
				return database.OriginVersion{}, apperror.New(apperror.InvalidState, "media", "video origin frame lineage is incomplete")
			}
		}
		result.MediaKind = "VIDEO"
		result.VideoPreparationID = &preparation.ID
		result.ScopeManifestDigest = preparation.ManifestDigest
		if err := tx.Save(&result).Error; err != nil {
			return database.OriginVersion{}, err
		}
		reference := database.VideoEvidenceReference{ID: identity.NewID(), TenantID: tenantID, PreparationID: preparation.ID, OwnerType: "ORIGIN_VERSION", OwnerID: result.ID, CreatedAt: now}
		if err := tx.Where("tenant_id=? AND preparation_id=? AND owner_type=? AND owner_id=?", tenantID, preparation.ID, reference.OwnerType, reference.OwnerID).FirstOrCreate(&reference).Error; err != nil {
			return database.OriginVersion{}, err
		}
	} else {
		var media []database.MediaObject
		if err := tx.Where("tenant_id=? AND responsibility_id=? AND status='READY' AND media_kind='PHOTO'", tenantID, responsibilityID).Find(&media).Error; err != nil {
			return database.OriginVersion{}, err
		}
		if len(media) == 0 {
			return database.OriginVersion{}, apperror.New(apperror.InvalidState, "media", "at least one ready described origin photo is required")
		}
		for _, item := range media {
			if strings.TrimSpace(item.Description) == "" || strings.TrimSpace(item.RequirementKey) == "" {
				return database.OriginVersion{}, apperror.New(apperror.InvalidState, "media", "origin photos require category and description")
			}
			evidence := database.OriginEvidence{ID: identity.NewID(), TenantID: tenantID, OriginVersionID: result.ID, MediaID: item.ID, Category: item.RequirementKey, Description: item.Description, AttentionItems: item.AttentionItems, CreatedAt: now}
			if err := tx.Where(database.OriginEvidence{TenantID: tenantID, MediaID: item.ID}).Attrs(evidence).FirstOrCreate(&evidence).Error; err != nil {
				return database.OriginVersion{}, err
			}
		}
		result.MediaKind = "PHOTO"
	}
	var origin database.Origin
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=?", tenantID, result.OriginID).First(&origin).Error; err != nil {
		return database.OriginVersion{}, err
	}
	if origin.ActiveVersionID != nil && *origin.ActiveVersionID != result.ID {
		if err := tx.Model(&database.OriginVersion{}).Where("tenant_id=? AND id=? AND status='ACTIVE'", tenantID, *origin.ActiveVersionID).Update("status", "SUPERSEDED").Error; err != nil {
			return database.OriginVersion{}, err
		}
		if err := releaseOriginReference(tx, tenantID, *origin.ActiveVersionID); err != nil {
			return database.OriginVersion{}, err
		}
	}
	result.Status, result.SubmittedAt, result.ActivatedAt = "ACTIVE", &now, &now
	if err := tx.Save(&result).Error; err != nil {
		return database.OriginVersion{}, err
	}
	if err := tx.Model(&origin).Updates(map[string]any{"active_version_id": result.ID, "version": origin.Version + 1, "updated_at": now}).Error; err != nil {
		return database.OriginVersion{}, err
	}
	return result, nil
}

func (s Service) Invalidate(ctx context.Context, tenantID, versionID identity.ID) (database.OriginVersion, error) {
	var row database.OriginVersion
	now := s.now()
	err := s.within(ctx, tenantID, func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=?", tenantID, versionID).First(&row).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return apperror.New(apperror.NotFound, "versionId", "origin version not found")
		} else if err != nil {
			return err
		}
		if row.Status == "INVALIDATED" {
			return nil
		}
		row.Status, row.InvalidatedAt = "INVALIDATED", &now
		if err := tx.Save(&row).Error; err != nil {
			return err
		}
		if err := releaseOriginReference(tx, tenantID, row.ID); err != nil {
			return err
		}
		return tx.Model(&database.Origin{}).Where("tenant_id=? AND id=? AND active_version_id=?", tenantID, row.OriginID, row.ID).Updates(map[string]any{"active_version_id": nil, "updated_at": now, "version": gorm.Expr("version + 1")}).Error
	})
	return row, err
}

func releaseOriginReference(tx *gorm.DB, tenantID, versionID identity.ID) error {
	return tx.Where("tenant_id=? AND owner_type='ORIGIN_VERSION' AND owner_id=?", tenantID, versionID).Delete(&database.VideoEvidenceReference{}).Error
}

// ReleaseOriginReference removes retained-video ownership references for a superseded version.
func ReleaseOriginReference(tx *gorm.DB, tenantID, versionID identity.ID) error {
	return releaseOriginReference(tx, tenantID, versionID)
}

func defaultJSON(v json.RawMessage) json.RawMessage {
	if len(v) == 0 {
		return json.RawMessage(`{}`)
	}
	return v
}
func (s Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
func (s Service) within(ctx context.Context, t identity.ID, fn func(*gorm.DB) error) error {
	if s.Within != nil {
		return s.Within(ctx, t, fn)
	}
	return (tenanttx.Runner{DB: s.DB}).Within(ctx, t, fn)
}
