// Package record_component_answer registers participant answers for pinned scope components.
package record_component_answer

import (
	"context"
	"fmt"
	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/capture/core"
	"inspection/services/inspection/internal/platform/mediator"
)

// Command identifies a versioned participant answer for one pinned scope component.
type Command struct {
	TenantID, ResponsibilityID                           identity.ID
	RequirementKey, ComponentKey, ViewKey, State, Reason string
	EvidenceIDs                                          []identity.ID
	ExpectedVersion                                      int64
}

// Dependencies supplies the mediator and capture service used by this operation.
type Dependencies struct {
	Bus     *mediator.Bus
	Service core.Service
}

// Setup validates dependencies and registers the answer command.
func Setup(d Dependencies) error {
	if d.Bus == nil || d.Service.DB == nil {
		return fmt.Errorf("slice capture/record_component_answer: missing dependency")
	}
	return d.Bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		c := raw.(Command)
		return d.Service.RecordComponentAnswer(ctx, core.ComponentAnswerInput{TenantID: c.TenantID, ResponsibilityID: c.ResponsibilityID, RequirementKey: c.RequirementKey, ComponentKey: c.ComponentKey, ViewKey: c.ViewKey, State: c.State, Reason: c.Reason, EvidenceIDs: c.EvidenceIDs, ExpectedVersion: c.ExpectedVersion})
	})
}
