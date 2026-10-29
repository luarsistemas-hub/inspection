// Package promote_inspection_to_origin prepares explicit inspection promotions.
package promote_inspection_to_origin

import (
	"context"
	"fmt"
	"time"

	"inspection/libs/identity"
	inventorycore "inspection/services/inspection/internal/features/inventory/core"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"

	"gorm.io/gorm"
)

// Dependencies are the external collaborators used by this slice.
type Dependencies struct {
	DB         *gorm.DB
	Bus        *mediator.Bus
	Authorizer auth.Authorizer
	Now        func() time.Time
	Inventory  inventorycore.Service
}

// Query asks for the current promotion eligibility of an inspection.
type Query struct{ TenantID, InspectionID identity.ID }

// Command requests an inspection promotion after explicit confirmation.
type Command struct {
	TenantID, InspectionID    identity.ID
	ClientMutationID, Purpose string
	ReplaceDraftID            *identity.ID
	ConfirmationDigest        string
}

// PrepareDraftCommand prepares an inspection inventory draft for human review.
type PrepareDraftCommand struct{ TenantID, InspectionID, ActorID identity.ID }

// Result contains the durable operation and the active version when available.
type Result struct {
	Promotion     database.InspectionOriginPromotion
	OriginVersion *database.OriginVersion
}

// StatusView is the user-facing eligibility projection.
type StatusView struct {
	Eligible                bool
	Impediments             []Impediment
	ActiveOrigin            *ActiveOrigin
	ConflictingDraft        *ConflictingDraft
	AutomaticCatalogSkipped bool
	ConfirmationDigest      string
	CurrentPromotion        *database.InspectionOriginPromotion
	GeneratedVersionActive  bool
}

// Impediment describes a stable blocking condition and its Portuguese message.
type Impediment struct{ Code, Message string }

// ActiveOrigin describes the active version currently installed for the asset.
type ActiveOrigin struct {
	VersionID    identity.ID
	Number       int
	MediaKind    string
	HasInventory bool
}

// ConflictingDraft describes the open inventory draft on the asset.
type ConflictingDraft struct {
	ID                 identity.ID
	Source             string
	SourceInspectionID *identity.ID
	DraftState         *string
	OwnInspection      bool
}

// Setup validates dependencies and registers the public mediator handlers.
func Setup(d Dependencies) error {
	if d.DB == nil || d.Bus == nil || d.Inventory.DB == nil {
		return fmt.Errorf("slice origins/promote_inspection_to_origin: missing dependency")
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if err := d.Bus.RegisterQuery(Query{}, func(ctx context.Context, raw any) (any, error) { return status(ctx, d, raw.(Query)) }); err != nil {
		return err
	}
	if err := d.Bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) { return request(ctx, d, raw.(Command)) }); err != nil {
		return err
	}
	return d.Bus.RegisterCommand(PrepareDraftCommand{}, func(ctx context.Context, raw any) (any, error) {
		return prepareDraft(ctx, d, raw.(PrepareDraftCommand))
	})
}
