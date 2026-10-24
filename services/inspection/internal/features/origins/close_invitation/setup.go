// Package close_invitation closes a draft origin capture and revokes its access.
package close_invitation

import (
	"context"
	"fmt"
	"strings"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Command identifies the origin version whose draft invitation should close.
type Command struct {
	TenantID, ActorID, VersionID identity.ID
	Reason                       string
	ClientMutationID             string
}

// Dependencies are the persistence and authorization boundaries used by this slice.
type Dependencies struct {
	DB         *gorm.DB
	Bus        *mediator.Bus
	Authorizer auth.Authorizer
	Within     func(context.Context, identity.ID, func(*gorm.DB) error) error
	Now        func() time.Time
}

// Setup registers the close-origin-invitation command.
func Setup(d Dependencies) error {
	if d.DB == nil || d.Bus == nil {
		return fmt.Errorf("slice origins/close_invitation: missing dependency")
	}
	return d.Bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		command := raw.(Command)
		reason := strings.TrimSpace(command.Reason)
		if command.TenantID == (identity.ID{}) || command.ActorID == (identity.ID{}) || command.VersionID == (identity.ID{}) || strings.TrimSpace(command.ClientMutationID) == "" || reason == "" || len(reason) > 2000 {
			return nil, apperror.New(apperror.InvalidInput, "reason", "a valid close reason is required")
		}
		now := time.Now().UTC()
		if d.Now != nil {
			now = d.Now().UTC()
		}
		within := d.Within
		if within == nil {
			within = func(ctx context.Context, tenantID identity.ID, fn func(*gorm.DB) error) error {
				return (tenanttx.Runner{DB: d.DB}).Within(ctx, tenantID, fn)
			}
		}
		var version database.OriginVersion
		err := within(ctx, command.TenantID, func(tx *gorm.DB) error {
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=?", command.TenantID, command.VersionID).First(&version).Error; err != nil {
				return apperror.New(apperror.NotFound, "versionId", "origin version not found")
			}
			if version.ResponsibilityID == nil {
				return apperror.New(apperror.InvalidState, "versionId", "origin version has no capture responsibility")
			}
			var origin database.Origin
			if err := tx.Where("tenant_id=? AND id=?", command.TenantID, version.OriginID).First(&origin).Error; err != nil {
				return apperror.New(apperror.NotFound, "versionId", "origin version not found")
			}
			var asset database.Asset
			if err := tx.Where("tenant_id=? AND id=?", command.TenantID, origin.AssetID).First(&asset).Error; err != nil {
				return apperror.New(apperror.NotFound, "versionId", "origin version not found")
			}
			if _, err := d.Authorizer.Authorize(ctx, command.TenantID, []string{auth.TenantAdmin, auth.OrganizationAdmin, auth.Manager, auth.Employee}, &requestctx.Scope{Kind: "BUSINESS_UNIT", ID: asset.BusinessUnitID}, true); err != nil {
				return err
			}
			alreadyInvalidated := version.Status == "INVALIDATED"
			if !alreadyInvalidated && version.Status != "DRAFT" {
				return apperror.New(apperror.InvalidState, "versionId", "only draft origin invitations can be closed")
			}
			if !alreadyInvalidated {
				version.Status, version.InvalidatedAt = "INVALIDATED", &now
				if err := tx.Model(&version).Updates(map[string]any{"status": version.Status, "invalidated_at": now}).Error; err != nil {
					return err
				}
			}
			if err := tx.Model(&database.CaptureDraft{}).Where("tenant_id=? AND responsibility_id=? AND status='OPEN'", command.TenantID, version.ResponsibilityID).Updates(map[string]any{"status": "EXPIRED", "updated_at": now, "version": gorm.Expr("version + 1")}).Error; err != nil {
				return err
			}
			if err := tx.Model(&database.Invitation{}).Where("tenant_id=? AND responsibility_id=? AND status IN ? AND revoked_at IS NULL", command.TenantID, version.ResponsibilityID, []string{"ACTIVE", "SENT"}).Updates(map[string]any{"status": "REVOKED", "revoked_at": now}).Error; err != nil {
				return err
			}
			if err := tx.Model(&database.ExternalSession{}).Where("tenant_id=? AND responsibility_id=? AND revoked_at IS NULL", command.TenantID, version.ResponsibilityID).Update("revoked_at", now).Error; err != nil {
				return err
			}
			if alreadyInvalidated {
				return nil
			}
			metadata, _ := requestctx.FromContext(ctx)
			return tx.Create(&database.AuditEvent{ID: identity.NewID(), TenantID: command.TenantID, ActorID: command.ActorID, Action: "origin.invitation_closed", TargetType: "ORIGIN_VERSION", TargetID: version.ID.String(), Outcome: "SUCCESS", Reason: reason, CorrelationID: metadata.CorrelationID, OccurredAt: now}).Error
		})
		if err != nil {
			return nil, err
		}
		return version, nil
	})
}
