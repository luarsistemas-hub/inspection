package resolvers

import (
	"context"
	"fmt"
	"strings"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	graphql1 "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (r *mutationResolver) markAllNotificationsRead(ctx context.Context, meta requestctx.Metadata, input graphql1.MarkNotificationReadInput) (*graphql1.RecipientNotificationPayload, error) {
	if input.Through == nil {
		return nil, apperror.New(apperror.InvalidInput, "through", "a valid timestamp is required")
	}
	through, err := time.Parse(time.RFC3339Nano, *input.Through)
	if err != nil {
		return nil, apperror.New(apperror.InvalidInput, "through", "a valid timestamp is required")
	}
	projectID, err := parseOptionalNotificationProjectID(input.ProjectID)
	if err != nil {
		return nil, err
	}
	var marked int64
	now := time.Now().UTC()
	err = withTask06Tenant(ctx, r.DB, meta.TenantID, func(tx *gorm.DB) error {
		query := filterNotificationScope(tx.Model(&database.RecipientNotification{}).Where("tenant_id=? AND recipient_membership_id=? AND read_at IS NULL AND created_at<=?", meta.TenantID, meta.Principal.MembershipID, through.UTC()), tx, meta)
		if input.Kind != nil && strings.TrimSpace(*input.Kind) != "" {
			query = query.Where("kind=?", strings.TrimSpace(*input.Kind))
		}
		if projectID != nil {
			inspectionIDs := tx.Model(&database.Inspection{}).Select("id").Where("tenant_id=? AND project_id=?", meta.TenantID, *projectID)
			query = query.Where("resource_kind IN ('INSPECTION','REPORT') AND resource_id IN (?)", inspectionIDs)
		}
		result := query.Updates(map[string]any{"read_at": now})
		marked = result.RowsAffected
		return result.Error
	})
	if err != nil {
		return nil, err
	}
	var unread int64
	if err := withTask06Tenant(ctx, r.DB, meta.TenantID, func(tx *gorm.DB) error {
		query := filterNotificationScope(tx.Model(&database.RecipientNotification{}).Where("tenant_id=? AND recipient_membership_id=? AND read_at IS NULL", meta.TenantID, meta.Principal.MembershipID), tx, meta)
		return query.Count(&unread).Error
	}); err != nil {
		return nil, err
	}
	return &graphql1.RecipientNotificationPayload{MarkedCount: int(marked), UnreadCount: int(unread), UserErrors: []*graphql1.UserError{}, ClientMutationID: input.ClientMutationID}, nil
}

func parseOptionalNotificationProjectID(value *string) (*identity.ID, error) {
	if value == nil || strings.TrimSpace(*value) == "" {
		return nil, nil
	}
	id, err := identity.ParseID(strings.TrimSpace(*value))
	if err != nil {
		return nil, invalidID("projectId")
	}
	return &id, nil
}

func filterNotificationScope(query, tx *gorm.DB, meta requestctx.Metadata) *gorm.DB {
	if hasRole(meta, auth.TenantAdmin) {
		return query
	}
	parts := make([]string, 0, len(meta.Principal.Scopes))
	args := make([]any, 0, len(meta.Principal.Scopes))
	scheduleParts := make([]string, 0, len(meta.Principal.Scopes))
	scheduleArgs := make([]any, 0, len(meta.Principal.Scopes))
	for _, scope := range meta.Principal.Scopes {
		switch scope.Kind {
		case "BUSINESS_UNIT":
			parts, args = append(parts, "business_unit_id = ?"), append(args, scope.ID)
			scheduleParts, scheduleArgs = append(scheduleParts, "business_unit_id = ?"), append(scheduleArgs, scope.ID)
		case "ASSET":
			parts, args = append(parts, "asset_id = ?"), append(args, scope.ID)
			scheduleParts, scheduleArgs = append(scheduleParts, "asset_id = ?"), append(scheduleArgs, scope.ID)
		case "PROJECT":
			parts, args = append(parts, "project_id = ?"), append(args, scope.ID)
		case "INSPECTION":
			parts, args = append(parts, "id = ?"), append(args, scope.ID)
		}
	}
	if len(parts) == 0 && len(scheduleParts) == 0 {
		return query.Where("1=0")
	}
	branches, branchArgs := []string{}, []any{}
	if len(parts) > 0 {
		inspectionIDs := tx.Model(&database.Inspection{}).Select("id").Where("tenant_id=?", meta.TenantID).Where("("+strings.Join(parts, " OR ")+")", args...)
		branches = append(branches, "(resource_kind IN ('INSPECTION','REPORT') AND resource_id IN (?))")
		branchArgs = append(branchArgs, inspectionIDs)
	}
	if len(scheduleParts) > 0 {
		scheduleIDs := tx.Model(&database.Schedule{}).Select("id").Where("tenant_id=?", meta.TenantID).Where("("+strings.Join(scheduleParts, " OR ")+")", scheduleArgs...)
		branches = append(branches, "(resource_kind='SCHEDULE' AND resource_id IN (?))")
		branchArgs = append(branchArgs, scheduleIDs)
	}
	return query.Where("("+strings.Join(branches, " OR ")+")", branchArgs...)
}

func recordScheduleNotification(ctx context.Context, db *gorm.DB, tenantID identity.ID, schedule database.Schedule, kind, title, body string) error {
	return withTask06Tenant(ctx, db, tenantID, func(tx *gorm.DB) error {
		var asset database.Asset
		if err := tx.Where("tenant_id=? AND id=?", tenantID, schedule.AssetID).Take(&asset).Error; err != nil {
			return err
		}
		var admins, managers []database.Membership
		if err := tx.Where("tenant_id=? AND status='ACTIVE' AND role=?", tenantID, auth.TenantAdmin).Find(&admins).Error; err != nil {
			return err
		}
		query := tx.Table("access.memberships AS m").Select("m.*").Joins("JOIN access.resource_scopes AS s ON s.tenant_id=m.tenant_id AND s.membership_id=m.id").Where("m.tenant_id=? AND m.status='ACTIVE' AND m.role=?", tenantID, auth.Manager).Where("(s.kind='BUSINESS_UNIT' AND s.resource_id=?) OR (s.kind='ASSET' AND s.resource_id=?)", schedule.BusinessUnitID, schedule.AssetID)
		if err := query.Distinct("m.id").Find(&managers).Error; err != nil {
			return err
		}
		eventID := identity.NewDeterministicID("inspection/schedule-notification-event", schedule.ID.String()+":"+kind+":"+fmt.Sprint(schedule.Version))
		now := time.Now().UTC()
		seen := make(map[identity.ID]struct{}, len(admins)+len(managers))
		for _, recipient := range append(admins, managers...) {
			if _, exists := seen[recipient.ID]; exists {
				continue
			}
			seen[recipient.ID] = struct{}{}
			notification := database.RecipientNotification{ID: identity.NewDeterministicID("inspection/schedule-notification", recipient.ID.String()+":"+eventID.String()), TenantID: tenantID, RecipientMembershipID: recipient.ID, EventID: eventID, Kind: kind, Title: title, Body: body + " " + asset.Name + " · " + asset.Address + ".", ResourceKind: "SCHEDULE", ResourceID: &schedule.ID, CreatedAt: now}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&notification).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
