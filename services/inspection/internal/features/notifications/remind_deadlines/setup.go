package remind_deadlines

import (
	"context"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Dependencies struct {
	DB     *gorm.DB
	Within func(context.Context, identity.ID, func(*gorm.DB) error) error
}

// Setup creates the idempotent evaluator for upcoming and overdue inspections.
func Setup(deps Dependencies) (func(context.Context, identity.ID, time.Time) error, error) {
	if deps.DB == nil {
		return nil, gorm.ErrInvalidDB
	}
	within := deps.Within
	if within == nil {
		within = (tenanttx.Runner{DB: deps.DB}).Within
	}
	return func(ctx context.Context, tenantID identity.ID, now time.Time) error {
		return within(ctx, tenantID, func(tx *gorm.DB) error {
			var cursorDeadline time.Time
			var cursorID identity.ID
			for {
				var inspections []database.Inspection
				query := tx.Where("tenant_id=? AND status IN ? AND deadline_at<=?", tenantID, []string{"PLANNED", "INVITED", "IN_PROGRESS", "SUBMITTED", "ANALYZING"}, now.UTC().Add(24*time.Hour))
				if !cursorDeadline.IsZero() {
					query = query.Where("deadline_at>? OR (deadline_at=? AND id>?)", cursorDeadline, cursorDeadline, cursorID)
				}
				if err := query.Order("deadline_at ASC, id ASC").Limit(100).Find(&inspections).Error; err != nil {
					return err
				}
				if len(inspections) == 0 {
					break
				}
				for _, inspection := range inspections {
					var asset database.Asset
					if err := tx.Where("tenant_id=? AND id=?", tenantID, inspection.AssetID).Take(&asset).Error; err != nil {
						return err
					}
					kind, title, body := "INSPECTION_DEADLINE", "Prazo da vistoria se aproxima", "A vistoria precisa avançar antes do prazo."
					if !inspection.DeadlineAt.After(now) {
						kind, title, body = "INSPECTION_OVERDUE", "Prazo da vistoria vencido", "A vistoria ainda está aberta após o prazo previsto."
					}
					body += " " + asset.Name + " · " + asset.Address + ". Prazo: " + inspection.DeadlineAt.UTC().Format("02/01/2006 15:04 UTC") + "."
					var admins, managers []database.Membership
					if err := tx.Where("tenant_id=? AND status='ACTIVE' AND role='TENANT_ADMIN'", tenantID).Find(&admins).Error; err != nil {
						return err
					}
					query := tx.Table("access.memberships AS m").Select("m.*").Joins("JOIN access.resource_scopes AS s ON s.tenant_id=m.tenant_id AND s.membership_id=m.id").Where("m.tenant_id=? AND m.status='ACTIVE' AND m.role='MANAGER'", tenantID).Where("(s.kind='BUSINESS_UNIT' AND s.resource_id=?) OR (s.kind='ASSET' AND s.resource_id=?) OR (s.kind='INSPECTION' AND s.resource_id=?)", inspection.BusinessUnitID, inspection.AssetID, inspection.ID)
					if inspection.ProjectID != nil {
						query = query.Or("m.tenant_id=? AND m.status='ACTIVE' AND m.role='MANAGER' AND EXISTS (SELECT 1 FROM access.resource_scopes s WHERE s.tenant_id=m.tenant_id AND s.membership_id=m.id AND s.kind='PROJECT' AND s.resource_id=?)", tenantID, *inspection.ProjectID)
					}
					if err := query.Distinct("m.id").Find(&managers).Error; err != nil {
						return err
					}
					eventID := identity.NewDeterministicID("inspection/deadline-notification-event", inspection.ID.String()+":"+inspection.DeadlineAt.UTC().Format(time.RFC3339Nano)+":"+kind)
					seen := make(map[identity.ID]struct{}, len(admins)+len(managers))
					for _, recipient := range append(admins, managers...) {
						if _, exists := seen[recipient.ID]; exists {
							continue
						}
						seen[recipient.ID] = struct{}{}
						row := database.RecipientNotification{ID: identity.NewDeterministicID("inspection/deadline-notification", recipient.ID.String()+":"+eventID.String()), TenantID: tenantID, RecipientMembershipID: recipient.ID, EventID: eventID, Kind: kind, Title: title, Body: body, ResourceKind: "INSPECTION", ResourceID: &inspection.ID, CreatedAt: now.UTC()}
						if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
							return err
						}
					}
				}
				last := inspections[len(inspections)-1]
				cursorDeadline, cursorID = last.DeadlineAt, last.ID
				if len(inspections) < 100 {
					break
				}
			}
			return nil
		})
	}, nil
}
