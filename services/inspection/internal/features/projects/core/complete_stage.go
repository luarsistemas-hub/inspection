package core

import (
	"context"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// CompleteStageOfInspection moves the project stage of an inspection that just
// reached a terminal state to COMPLETED, so the project can be closed. It runs
// inside the transaction that changed the inspection (completion by the
// classifier, cancellation or invalidation), under a system actor (the tenant).
//
// Locks follow the order inspection, project, stage (the caller already holds
// the inspection row; StartStage/SkipStage lock project then stage), so
// concurrent completions of the same stage serialize instead of each one seeing
// the other still open, and nothing deadlocks.
//
// The stage completes only when:
//   - it is AVAILABLE or IN_PROGRESS (a SKIPPED, CANCELED or INVALIDATED stage
//     is never overwritten) and its project is ACTIVE;
//   - none of its inspections is still open;
//   - at least one of its inspections is COMPLETED, so canceling the only
//     inspection never completes a stage nobody performed;
//   - it has no ACTIVE schedule. A schedule is recurring work of the stage that
//     will still create inspections; the stage stays open until the schedule is
//     canceled (or the stage is skipped, which cancels it).
//
// The transition, the stage and project version increments and the
// project.stage_changed.v1 event (with a deterministic ID, so a replay never
// duplicates it) are written in tx. It reports whether the stage changed.
func CompleteStageOfInspection(ctx context.Context, tx *gorm.DB, inspection database.Inspection, now time.Time, correlationID string) (bool, error) {
	if inspection.ProjectID == nil || inspection.StageID == nil {
		return false, nil
	}
	tx = tx.WithContext(ctx)
	var project database.Project
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=?", inspection.TenantID, *inspection.ProjectID).First(&project).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return false, nil
		}
		return false, err
	}
	if project.Status != ProjectActive {
		return false, nil
	}
	var stage database.ProjectStage
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND project_id=? AND id=?", inspection.TenantID, project.ID, *inspection.StageID).First(&stage).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return false, nil
		}
		return false, err
	}
	if stage.Status != StageAvailable && stage.Status != StageInProgress {
		return false, nil
	}
	var open, completed, schedules int64
	if err := tx.Model(&database.Inspection{}).Where("tenant_id=? AND stage_id=? AND status NOT IN ('COMPLETED','CANCELED','INVALIDATED')", inspection.TenantID, stage.ID).Count(&open).Error; err != nil {
		return false, err
	}
	if open > 0 {
		return false, nil
	}
	if err := tx.Model(&database.Inspection{}).Where("tenant_id=? AND stage_id=? AND status='COMPLETED'", inspection.TenantID, stage.ID).Count(&completed).Error; err != nil {
		return false, err
	}
	if completed == 0 {
		return false, nil
	}
	if err := tx.Model(&database.Schedule{}).Where("tenant_id=? AND stage_id=? AND status='ACTIVE'", inspection.TenantID, stage.ID).Count(&schedules).Error; err != nil {
		return false, err
	}
	if schedules > 0 {
		return false, nil
	}
	if err := tx.Model(&database.ProjectStage{}).Where("tenant_id=? AND id=?", inspection.TenantID, stage.ID).
		Updates(map[string]any{"status": StageCompleted, "version": gorm.Expr("version + 1"), "updated_at": now}).Error; err != nil {
		return false, err
	}
	if err := tx.Model(&database.Project{}).Where("tenant_id=? AND id=?", inspection.TenantID, project.ID).
		Updates(map[string]any{"version": gorm.Expr("version + 1"), "updated_at": now}).Error; err != nil {
		return false, err
	}
	eventID := identity.ID(uuid.NewSHA1(uuid.Nil, []byte("project-stage-completed:"+inspection.TenantID.String()+":"+stage.ID.String())))
	if err := writeTransition(tx, project, &stage, stage.Status, StageCompleted, "", now, inspection.TenantID, correlationID, eventID); err != nil {
		return false, err
	}
	return true, nil
}
