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
		if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", envelope.TenantID, payload.InspectionID).First(&inspection).Error; err != nil {
			return err
		}
		var class database.ClassificationRun
		if err := tx.WithContext(ctx).Where("tenant_id=? AND inspection_id=?", envelope.TenantID, payload.InspectionID).Order("created_at DESC").First(&class).Error; err != nil {
			return err
		}
		now := deps.Now().UTC()
		row := database.DashboardInspection{ID: identity.NewID(), TenantID: envelope.TenantID, InspectionID: payload.InspectionID, ProjectID: inspection.ProjectID, AssetID: inspection.AssetID, Classification: class.Classification, Status: inspection.Status, Invalidated: inspection.Status == "INVALIDATED", Sequence: now.UnixNano(), UpdatedAt: now}
		if err := UpsertDashboardInspection(tx.WithContext(ctx), row); err != nil {
			return err
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
		DoUpdates: clause.AssignmentColumns([]string{
			"project_id", "asset_id", "classification", "status", "invalidated", "sequence", "updated_at",
		}),
	}).Create(&row).Error
}
