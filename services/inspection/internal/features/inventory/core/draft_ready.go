package core

import (
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	"inspection/services/inspection/internal/platform/messaging"

	"gorm.io/gorm"
)

// EmitDraftReady records inventory.draft_ready.v1 for a draft that has just
// become READY. The payload carries the version id only, as the event contract
// requires; the inspection id, when known, only correlates the event.
func EmitDraftReady(tx *gorm.DB, tenantID, versionID, inspectionID identity.ID, now time.Time) error {
	correlation := "inventory-draft-" + versionID.String()
	if inspectionID != (identity.ID{}) {
		correlation = inspectionID.String()
	}
	return messaging.AddOutbox(tx, events.Envelope[events.InventoryDraftReadyPayload]{ID: identity.NewID(), Type: "inventory.draft_ready.v1", SchemaVersion: 1, OccurredAt: now.UTC(), TenantID: tenantID, AggregateID: versionID, CorrelationID: correlation, Payload: events.InventoryDraftReadyPayload{OriginVersionID: versionID}})
}
