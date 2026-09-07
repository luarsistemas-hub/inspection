package record_event

import (
	"context"
	"fmt"
	"strings"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
)

type Command struct {
	TenantID, ActorID                                            identity.ID
	Action, TargetType, TargetID, Outcome, Reason, CorrelationID string
	OccurredAt                                                   time.Time
}
type Dependencies struct {
	DB  *gorm.DB
	Bus *mediator.Bus
}

func Setup(deps Dependencies) error {
	if deps.DB == nil || deps.Bus == nil {
		return fmt.Errorf("slice audit/record_event: missing dependency")
	}
	return deps.Bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) { return handle(ctx, deps, raw.(Command)) })
}
func handle(ctx context.Context, deps Dependencies, c Command) (database.AuditEvent, error) {
	if c.TenantID == (identity.ID{}) || c.ActorID == (identity.ID{}) || strings.TrimSpace(c.Action) == "" || c.TargetType == "" || c.TargetID == "" || c.Outcome == "" || c.CorrelationID == "" {
		return database.AuditEvent{}, apperror.New(apperror.InvalidInput, "audit", "missing audit context")
	}
	if requiresReason(c.Action) && strings.TrimSpace(c.Reason) == "" {
		return database.AuditEvent{}, apperror.New(apperror.InvalidInput, "reason", "reason is required")
	}
	if len([]rune(c.Reason)) > 2000 {
		return database.AuditEvent{}, apperror.New(apperror.InvalidInput, "reason", "reason is too long")
	}
	if c.OccurredAt.IsZero() {
		c.OccurredAt = time.Now().UTC()
	}
	e := database.AuditEvent{ID: identity.NewID(), TenantID: c.TenantID, ActorID: c.ActorID, Action: c.Action, TargetType: c.TargetType, TargetID: c.TargetID, Outcome: c.Outcome, Reason: c.Reason, CorrelationID: c.CorrelationID, OccurredAt: c.OccurredAt}
	if err := (tenanttx.Runner{DB: deps.DB}).Within(ctx, c.TenantID, func(tx *gorm.DB) error { return tx.Create(&e).Error }); err != nil {
		return database.AuditEvent{}, apperror.Wrap(apperror.Internal, err)
	}
	return e, nil
}
func requiresReason(action string) bool {
	switch action {
	case "recapture.requested", "sensitive.false_positive", "inspection.invalidated", "project.reopened", "project.stage_inserted", "retention.deletion_requested":
		return true
	}
	return false
}
