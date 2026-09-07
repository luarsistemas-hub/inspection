// Package core owns origin invitation, immutable versioning and activation.
package core

import (
	"context"
	"encoding/json"
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
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Service struct {
	DB     *gorm.DB
	Now    func() time.Time
	Within func(context.Context, identity.ID, func(*gorm.DB) error) error
}
type InviteInput struct {
	TenantID, AssetID, TemplateID, TemplateVersionID identity.ID
	Delivery                                         []invitationcore.DeliveryIntent
	Requirements                                     []capturecore.Requirement
	Policy, Reference                                json.RawMessage
	ExpiresAt                                        time.Time
	IdempotencyKey                                   string
}
type Invitation struct {
	OriginID, VersionID, ResponsibilityID, InvitationID identity.ID
	LinkToken                                           string
}

func (s Service) Invite(ctx context.Context, in InviteInput) (Invitation, error) {
	if in.TenantID == (identity.ID{}) || in.AssetID == (identity.ID{}) || in.TemplateID == (identity.ID{}) || in.TemplateVersionID == (identity.ID{}) || len(in.Delivery) == 0 || len(in.Requirements) == 0 || len(in.Requirements) > capturecore.MaxActivePhotos || !in.ExpiresAt.After(s.now()) || strings.TrimSpace(in.IdempotencyKey) == "" {
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
			var invite database.Invitation
			if err := tx.Where("tenant_id=? AND responsibility_id=?", in.TenantID, existing.ResponsibilityID).First(&invite).Error; err != nil {
				return err
			}
			out.OriginID, out.VersionID, out.ResponsibilityID, out.InvitationID = existing.OriginID, existing.ID, existing.ResponsibilityID, invite.ID
			out.LinkToken = ""
			return nil
		} else if err != gorm.ErrRecordNotFound {
			return err
		}
		var origin database.Origin
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND asset_id=? AND template_id=?", in.TenantID, in.AssetID, in.TemplateID).First(&origin).Error
		if err == gorm.ErrRecordNotFound {
			origin = database.Origin{ID: identity.NewID(), TenantID: in.TenantID, AssetID: in.AssetID, TemplateID: in.TemplateID, Version: 1, CreatedAt: now, UpdatedAt: now}
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
		version := database.OriginVersion{ID: identity.NewID(), TenantID: in.TenantID, OriginID: origin.ID, VersionNumber: int(count) + 1, ResponsibilityID: identity.NewID(), SupersedesID: origin.ActiveVersionID, Status: "DRAFT", IdempotencyKey: in.IdempotencyKey, CreatedAt: now}
		draft := database.CaptureDraft{ID: identity.NewID(), TenantID: in.TenantID, ResponsibilityID: version.ResponsibilityID, Kind: "ORIGIN", TemplateVersionID: in.TemplateVersionID, ReferencePayload: defaultJSON(in.Reference), PolicyPayload: defaultJSON(in.Policy), Requirements: requirements, Status: "OPEN", Version: 1, CreatedAt: now, UpdatedAt: now}
		invite := database.Invitation{ID: identity.NewID(), TenantID: in.TenantID, ResponsibilityID: version.ResponsibilityID, TokenHash: hash[:], DeliveryIntents: deliveries, Status: "ACTIVE", ExpiresAt: in.ExpiresAt, CreatedAt: now}
		for _, row := range []any{&version, &draft, &invite} {
			if err := tx.Create(row).Error; err != nil {
				return err
			}
		}
		eventID := identity.NewID()
		if err := messaging.AddOutbox(tx, events.Envelope[map[string]any]{ID: eventID, Type: "origin.invitation_requested.v1", SchemaVersion: 1, OccurredAt: now, TenantID: in.TenantID, AggregateID: origin.ID, CorrelationID: "origin-invite-" + version.ID.String(), Payload: map[string]any{"originId": origin.ID, "originVersionId": version.ID, "responsibilityId": version.ResponsibilityID}}); err != nil {
			return err
		}
		out.OriginID, out.VersionID, out.ResponsibilityID, out.InvitationID = origin.ID, version.ID, version.ResponsibilityID, invite.ID
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
	var draft database.CaptureDraft
	if err := tx.Where("tenant_id=? AND responsibility_id=?", tenantID, responsibilityID).First(&draft).Error; err != nil || draft.Status != "SUBMITTED" {
		return database.OriginVersion{}, apperror.New(apperror.InvalidState, "responsibility", "origin capture must be submitted before activation")
	}
	var media []database.MediaObject
	if err := tx.Where("tenant_id=? AND responsibility_id=? AND status='READY'", tenantID, responsibilityID).Find(&media).Error; err != nil {
		return database.OriginVersion{}, err
	}
	if len(media) == 0 {
		return database.OriginVersion{}, apperror.New(apperror.InvalidState, "media", "at least one ready described origin photo is required")
	}
	for _, item := range media {
		if strings.TrimSpace(item.Description) == "" || strings.TrimSpace(item.RequirementKey) == "" {
			return database.OriginVersion{}, apperror.New(apperror.InvalidState, "media", "origin photos require category and description")
		}
		evidence := database.OriginEvidence{ID: identity.NewID(), TenantID: tenantID, OriginVersionID: result.ID, MediaID: item.ID, Category: item.RequirementKey, Description: item.Description, CreatedAt: now}
		if err := tx.Where(database.OriginEvidence{TenantID: tenantID, MediaID: item.ID}).Attrs(evidence).FirstOrCreate(&evidence).Error; err != nil {
			return database.OriginVersion{}, err
		}
	}
	var origin database.Origin
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=?", tenantID, result.OriginID).First(&origin).Error; err != nil {
		return database.OriginVersion{}, err
	}
	if origin.ActiveVersionID != nil && *origin.ActiveVersionID != result.ID {
		if err := tx.Model(&database.OriginVersion{}).Where("tenant_id=? AND id=? AND status='ACTIVE'", tenantID, *origin.ActiveVersionID).Update("status", "SUPERSEDED").Error; err != nil {
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
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=?", tenantID, versionID).First(&row).Error; err != nil {
			return apperror.New(apperror.NotFound, "versionId", "origin version not found")
		}
		if row.Status == "INVALIDATED" {
			return nil
		}
		row.Status, row.InvalidatedAt = "INVALIDATED", &now
		if err := tx.Save(&row).Error; err != nil {
			return err
		}
		return tx.Model(&database.Origin{}).Where("tenant_id=? AND id=? AND active_version_id=?", tenantID, row.OriginID, row.ID).Updates(map[string]any{"active_version_id": nil, "updated_at": now, "version": gorm.Expr("version + 1")}).Error
	})
	return row, err
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
