// Package handle_report_ready updates report driven dashboard projections.
package handle_report_ready

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Dependencies struct {
	InAppNotification func(context.Context, *gorm.DB, events.RawEnvelope) error
	UpsertTriage      func(context.Context, *gorm.DB, identity.ID, identity.ID, database.ClassificationRun, int, time.Time) error
	Now               func() time.Time
}

// Setup validates dependencies and returns the report.ready handler.
func Setup(deps Dependencies) (func(context.Context, *gorm.DB, events.RawEnvelope) error, error) {
	if deps.InAppNotification == nil || deps.UpsertTriage == nil {
		return nil, fmt.Errorf("reports/handle_report_ready: missing notification or triage adapter")
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return func(ctx context.Context, tx *gorm.DB, envelope events.RawEnvelope) error {
		var payload struct {
			InspectionID identity.ID `json:"inspectionId"`
			SnapshotID   identity.ID `json:"snapshotId"`
		}
		if err := json.Unmarshal(envelope.Payload, &payload); err != nil || payload.InspectionID == (identity.ID{}) || payload.SnapshotID == (identity.ID{}) {
			return messaging.ErrPermanent
		}
		var snapshot database.ReportSnapshot
		if err := tx.WithContext(ctx).Where("tenant_id=? AND inspection_id=? AND id=?", envelope.TenantID, payload.InspectionID, payload.SnapshotID).Take(&snapshot).Error; err != nil {
			return err
		}
		var inspection database.Inspection
		if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "SHARE"}).Where("tenant_id=? AND id=?", envelope.TenantID, payload.InspectionID).First(&inspection).Error; err != nil {
			return err
		}
		var class database.ClassificationRun
		if err := tx.WithContext(ctx).Where("tenant_id=? AND inspection_id=?", envelope.TenantID, payload.InspectionID).Order("created_at DESC").First(&class).Error; err != nil {
			return err
		}
		now := deps.Now().UTC()
		invalidated := inspection.Status == "INVALIDATED" || inspection.Status == "CANCELED"
		row := database.DashboardInspection{ID: identity.NewID(), TenantID: envelope.TenantID, InspectionID: payload.InspectionID, ProjectID: inspection.ProjectID, AssetID: inspection.AssetID, Classification: class.Classification, Status: inspection.Status, Invalidated: invalidated, Sequence: now.UnixNano(), UpdatedAt: now}
		if err := UpsertDashboardInspection(tx.WithContext(ctx), row); err != nil {
			return err
		}
		if invalidated {
			return deps.InAppNotification(ctx, tx, envelope)
		}
		if err := deps.UpsertTriage(ctx, tx, envelope.TenantID, payload.InspectionID, class, snapshot.VersionNumber, now); err != nil {
			return err
		}
		return deps.InAppNotification(ctx, tx, envelope)
	}, nil
}

// UpsertDashboardInspection refreshes the projection while preserving its identity.
func UpsertDashboardInspection(tx *gorm.DB, row database.DashboardInspection) error {
	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "tenant_id"}, {Name: "inspection_id"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"project_id":     clause.Expr{SQL: "EXCLUDED.project_id"},
			"asset_id":       clause.Expr{SQL: "EXCLUDED.asset_id"},
			"classification": clause.Expr{SQL: "EXCLUDED.classification"},
			"status":         clause.Expr{SQL: "EXCLUDED.status"},
			"invalidated":    gorm.Expr("dashboard.inspections.invalidated OR EXCLUDED.invalidated"),
			"sequence":       clause.Expr{SQL: "EXCLUDED.sequence"},
			"updated_at":     clause.Expr{SQL: "EXCLUDED.updated_at"},
		}),
	}).Create(&row).Error
}
