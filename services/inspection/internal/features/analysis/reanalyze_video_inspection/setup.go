// Package reanalyze_video_inspection queues an audited video analysis generation.
package reanalyze_video_inspection

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

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Command identifies the active plan the caller expects to replace.
type Command struct {
	TenantID         identity.ID
	InspectionID     identity.ID
	ExpectedPlanID   identity.ID
	ClientMutationID string
}

// Result identifies the newly queued generation.
type Result struct {
	PlanID     identity.ID
	Generation int
}

// Dependencies are the external ports required by this slice.
type Dependencies struct {
	DB         *gorm.DB
	Bus        *mediator.Bus
	Authorizer auth.Authorizer
	Now        func() time.Time
	Within     func(context.Context, identity.ID, func(*gorm.DB) error) error
}

// Setup registers the reanalysis command with the mediator.
func Setup(d Dependencies) error {
	if d.DB == nil || d.Bus == nil {
		return fmt.Errorf("slice analysis/reanalyze_video_inspection: missing dependency")
	}
	if d.Now == nil {
		d.Now = func() time.Time { return time.Now().UTC() }
	}
	within := d.Within
	if within == nil {
		within = (tenanttx.Runner{DB: d.DB}).Within
	}
	return d.Bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		command := raw.(Command)
		if command.TenantID == (identity.ID{}) || command.InspectionID == (identity.ID{}) || command.ExpectedPlanID == (identity.ID{}) || command.ClientMutationID == "" || len(command.ClientMutationID) > 128 {
			return nil, apperror.New(apperror.InvalidInput, "input", "inspection, expected plan and client mutation id are required")
		}
		var inspection database.Inspection
		var asset database.Asset
		if err := within(ctx, command.TenantID, func(tx *gorm.DB) error {
			if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", command.TenantID, command.InspectionID).Take(&inspection).Error; err != nil {
				return err
			}
			return tx.WithContext(ctx).Where("tenant_id=? AND id=?", command.TenantID, inspection.AssetID).Take(&asset).Error
		}); err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, apperror.New(apperror.NotFound, "inspectionId", "inspection not found")
			}
			return nil, err
		}
		if _, err := d.Authorizer.Authorize(ctx, command.TenantID, []string{auth.TenantAdmin, auth.Manager}, &requestctx.Scope{Kind: "BUSINESS_UNIT", ID: asset.BusinessUnitID}, true); err != nil {
			return nil, err
		}
		return queue(ctx, within, d.Now, command)
	})
}

func queue(ctx context.Context, within func(context.Context, identity.ID, func(*gorm.DB) error) error, now func() time.Time, command Command) (Result, error) {
	meta, _ := requestctx.FromContext(ctx)
	var result Result
	err := within(ctx, command.TenantID, func(tx *gorm.DB) error {
		var inspection database.Inspection
		if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=?", command.TenantID, command.InspectionID).Take(&inspection).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return apperror.New(apperror.NotFound, "inspectionId", "inspection not found")
			}
			return err
		}
		var replay database.VideoPlan
		if err := tx.WithContext(ctx).Where("tenant_id=? AND client_mutation_id=?", command.TenantID, command.ClientMutationID).Take(&replay).Error; err == nil {
			if replay.InspectionID != command.InspectionID || replay.ExpectedPlanID == nil || *replay.ExpectedPlanID != command.ExpectedPlanID {
				return apperror.New(apperror.Conflict, "clientMutationId", "mutation id was already used for another reanalysis")
			}
			result = Result{PlanID: replay.ID, Generation: replay.Generation}
			return nil
		} else if err != gorm.ErrRecordNotFound {
			return err
		}
		if inspection.MediaKind != "VIDEO" {
			return apperror.New(apperror.InvalidState, "inspectionId", "video inspection is required")
		}
		if inspection.ActiveVideoPlanID == nil {
			return apperror.New(apperror.InvalidState, "inspectionId", "inspection has no sealed video plan")
		}
		var active database.VideoPlan
		if err := tx.WithContext(ctx).Where("tenant_id=? AND id=? AND inspection_id=? AND status='SEALED'", command.TenantID, *inspection.ActiveVideoPlanID, inspection.ID).Take(&active).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return apperror.New(apperror.InvalidState, "inspectionId", "inspection has no sealed video plan")
			}
			return err
		}
		if active.ID != command.ExpectedPlanID {
			return apperror.New(apperror.Conflict, "expectedPlanId", "active video plan has changed")
		}
		generation := active.Generation + 1
		var inProgress int64
		if err := tx.WithContext(ctx).Model(&database.VideoPlan{}).Where("tenant_id=? AND inspection_id=? AND status IN ?", command.TenantID, inspection.ID, []string{"BUILDING", "QUEUED"}).Count(&inProgress).Error; err != nil {
			return err
		}
		if inProgress > 0 {
			return apperror.New(apperror.InvalidState, "inspectionId", "video analysis is already in progress")
		}
		var laterGeneration int64
		if err := tx.WithContext(ctx).Model(&database.VideoPlan{}).Where("tenant_id=? AND inspection_id=? AND generation>=?", command.TenantID, inspection.ID, generation).Count(&laterGeneration).Error; err != nil {
			return err
		}
		if laterGeneration > 0 {
			return apperror.New(apperror.InvalidState, "inspectionId", "a later video plan generation already exists")
		}
		createdAt := now().UTC()
		newPlan := database.VideoPlan{
			ID: identity.NewID(), TenantID: command.TenantID, InspectionID: inspection.ID,
			SubmissionID: active.SubmissionID, ReferenceVersionID: active.ReferenceVersionID,
			OriginPreparationID: active.OriginPreparationID, CurrentPreparationID: active.CurrentPreparationID,
			Generation: generation, PlanDigest: "", Status: "BUILDING", NextAttemptAt: createdAt,
			ClientMutationID: command.ClientMutationID, ExpectedPlanID: &active.ID, CreatedAt: createdAt,
		}
		if err := tx.WithContext(ctx).Create(&newPlan).Error; err != nil {
			return err
		}
		if meta.Principal.IdentityID != (identity.ID{}) {
			audit := database.AuditEvent{ID: identity.NewID(), TenantID: command.TenantID, ActorID: meta.Principal.IdentityID, Action: "video.inspection_reanalyzed", TargetType: "inspection", TargetID: inspection.ID.String(), Outcome: "ACCEPTED", CorrelationID: meta.CorrelationID, OccurredAt: createdAt}
			if err := tx.WithContext(ctx).Create(&audit).Error; err != nil {
				return err
			}
		}
		result = Result{PlanID: newPlan.ID, Generation: generation}
		return nil
	})
	return result, err
}
