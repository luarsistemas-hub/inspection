// Package upsert_handover_record registers typed capture declarations.
package upsert_handover_record

import (
	"context"
	"encoding/json"
	"fmt"
	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/capture/core"
	"inspection/services/inspection/internal/platform/mediator"
)

// Command identifies a typed, versioned handover declaration.
type Command struct {
	TenantID, ResponsibilityID identity.ID
	RecordKey, Kind, Declarant string
	Payload                    json.RawMessage
	MediaIDs                   []identity.ID
	ExpectedVersion            int64
}

// Dependencies supplies the mediator and capture service used by this operation.
type Dependencies struct {
	Bus     *mediator.Bus
	Service core.Service
}

// Setup validates dependencies and registers the handover declaration command.
func Setup(d Dependencies) error {
	if d.Bus == nil || d.Service.DB == nil {
		return fmt.Errorf("slice capture/upsert_handover_record: missing dependency")
	}
	return d.Bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		c := raw.(Command)
		return d.Service.UpsertHandoverRecord(ctx, core.HandoverInput{TenantID: c.TenantID, ResponsibilityID: c.ResponsibilityID, RecordKey: c.RecordKey, Kind: c.Kind, Declarant: c.Declarant, Payload: c.Payload, MediaIDs: c.MediaIDs, ExpectedVersion: c.ExpectedVersion})
	})
}
