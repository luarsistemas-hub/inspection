//go:build integration

package handle_classified

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"inspection/libs/identity"
	invitationcore "inspection/services/inspection/internal/features/invitations/core"
	"inspection/services/inspection/internal/features/notifications/request"
	recapturecore "inspection/services/inspection/internal/features/recapture/core"
	"inspection/services/inspection/internal/features/reports/core"
	generatesnapshot "inspection/services/inspection/internal/features/reports/generate_snapshot"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

// Item 4 (round 8): a v3 photo event rejected permanently is acknowledged, but
// nothing it did before the decision may commit: no CRITICAL alert without a
// report, and no partial write of the snapshot adapter. The same CRITICAL
// event with a valid snapshot does deliver the alert (control).
func TestR8V3PermanentSnapshotFailureCommitsNoCriticalAlertOrPartialWrite(t *testing.T) {
	withCriticalFixture(t, func(ctx context.Context, h *harness.Harness, f harness.Task06Fixture, deps request.Dependencies) {
		seedTask04Scope(t, ctx, h, f, "OBSERVED", false)
		if err := addCriticalChannel(ctx, h, f, f.MembershipID, "critical-r8@example.test", true); err != nil {
			t.Fatal(err)
		}
		runID := seedManualClassification(ctx, t, h, f, "CRITICAL")
		correlation := "task04-r8-critical-" + identity.NewID().String()
		if err := h.AdminDB.Create(&database.AuditEvent{ID: identity.NewID(), TenantID: f.TenantID, ActorID: f.MembershipID, Action: "report.reissue_requested", TargetType: "inspection", TargetID: f.InspectionID.String(), Outcome: "QUEUED", Reason: "expectedVersion=1", CorrelationID: correlation, OccurredAt: time.Now().UTC()}).Error; err != nil {
			t.Fatal(err)
		}
		marker := "task04-r8-partial-" + identity.NewID().String()
		eventID := identity.NewID()
		err := deliverClassifiedWith(ctx, h, f, deps, eventID, runID, "CRITICAL", func(handler *Dependencies) {
			handler.CreateSnapshotInTx = func(ctx context.Context, tx *gorm.DB, _ generatesnapshot.Input) (generatesnapshot.Result, error) {
				// A partial write of the adapter before it fails permanently.
				if err := tx.Create(&database.AuditEvent{ID: identity.NewID(), TenantID: f.TenantID, ActorID: f.MembershipID, Action: "report.partial_write", TargetType: "inspection", TargetID: f.InspectionID.String(), Outcome: "QUEUED", CorrelationID: marker, OccurredAt: time.Now().UTC()}).Error; err != nil {
					return generatesnapshot.Result{}, err
				}
				return generatesnapshot.Result{}, core.ErrInvalidSnapshot
			}
		})
		assertNoHang(t, err)
		var receipts, partial, snapshots int64
		if err := h.AdminDB.Model(&database.InboxReceipt{}).Where("tenant_id=? AND event_id=?", f.TenantID, eventID).Count(&receipts).Error; err != nil || receipts != 1 {
			t.Fatalf("receipts=%d err=%v; the permanent v3 failure must be acknowledged once", receipts, err)
		}
		if deliveries := countDeliveries(t, h, ctx, f.TenantID); deliveries != 0 {
			t.Fatalf("a CRITICAL alert (%d deliveries) was committed for an event that produced no report", deliveries)
		}
		if err := h.AdminDB.Model(&database.AuditEvent{}).Where("tenant_id=? AND correlation_id=?", f.TenantID, marker).Count(&partial).Error; err != nil || partial != 0 {
			t.Fatalf("partial snapshot write committed (%d rows, err=%v)", partial, err)
		}
		if err := h.AdminDB.Model(&database.ReportSnapshot{}).Where("tenant_id=? AND inspection_id=?", f.TenantID, f.InspectionID).Count(&snapshots).Error; err != nil || snapshots != 0 {
			t.Fatalf("snapshots=%d err=%v", snapshots, err)
		}
		var request database.AuditEvent
		if err := h.AdminDB.Where("tenant_id=? AND correlation_id=?", f.TenantID, correlation).Take(&request).Error; err != nil || request.Outcome != "FAILED" {
			t.Fatalf("reissue request outcome=%q err=%v, want FAILED", request.Outcome, err)
		}

		assertNoHang(t, deliverClassified(ctx, h, f, deps, identity.NewID(), runID, "CRITICAL"))
		if deliveries := countDeliveries(t, h, ctx, f.TenantID); deliveries == 0 {
			t.Fatal("control: the CRITICAL event with a valid report did not request the alert")
		}
	})
}

// Item 8 (round 8): an OPEN (in progress or abandoned) recapture draft does not
// contribute component answers or declarations to an immutable report; once it
// is SUBMITTED, it does.
func TestR8OpenRecaptureDraftDoesNotChangeReportUntilSubmitted(t *testing.T) {
	withCriticalFixture(t, func(ctx context.Context, h *harness.Harness, f harness.Task06Fixture, _ request.Dependencies) {
		seedTask04Scope(t, ctx, h, f, "NOT_OBSERVED", true)
		requirementKey := "env:new:" + f.EnvironmentIdentityID.String()
		recapture, err := (recapturecore.Service{DB: h.DB, Within: h.WithinTenant}).Request(ctx, recapturecore.RequestInput{TenantID: f.TenantID, InspectionID: f.InspectionID, Items: []recapturecore.Item{{RequirementKey: requirementKey, Reason: "Componente não registrado"}}, Delivery: []invitationcore.DeliveryIntent{{Channel: "EMAIL", Destination: "qa-r8@example.test", Verified: true}}, DeadlineAt: time.Now().UTC().Add(24 * time.Hour), IdempotencyKey: "task04-r8-recapture-" + f.InspectionID.String()})
		if err != nil {
			t.Fatalf("create recapture through the production service: %v", err)
		}
		var draft database.CaptureDraft
		if err := h.AdminDB.Where("tenant_id=? AND responsibility_id=? AND kind='RECAPTURE'", f.TenantID, recapture.ResponsibilityID).Take(&draft).Error; err != nil {
			t.Fatal(err)
		}
		if draft.Status != "OPEN" {
			t.Fatalf("recapture draft status=%s, want OPEN", draft.Status)
		}
		now := time.Now().UTC()
		if err := h.AdminDB.Create(&database.ComponentAnswer{ID: identity.NewID(), TenantID: f.TenantID, DraftID: draft.ID, RequirementKey: requirementKey, ComponentKey: "floor", ViewKey: "wide", State: "OBSERVED", EvidenceIDs: mustJSONIDs(t, f.MediaID), Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
		if err := h.AdminDB.Create(&database.HandoverRecord{ID: identity.NewID(), TenantID: f.TenantID, DraftID: draft.ID, RecordKey: "keys", Kind: "KEY", Payload: json.RawMessage(`{"keyType":"FRONT_DOOR","quantity":1}`), MediaIDs: json.RawMessage(`[]`), Declarant: "Participante", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
		evidence := []core.Evidence{{ID: f.MediaID.String(), RequirementKey: requirementKey, Role: "CURRENT", Availability: "AVAILABLE"}}
		build := func() (core.ChecklistComponent, core.HandoverEntry) {
			t.Helper()
			var checklist *core.ChecklistSection
			var handover *core.HandoverSection
			if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
				var err error
				checklist, _, _, handover, _, err = buildDescriptiveReport(ctx, tx, f.TenantID, f.InspectionID, evidence, nil, "prompt", "COMPLETED")
				return err
			}); err != nil {
				t.Fatal(err)
			}
			for _, entry := range handover.Entries {
				if entry.Key == "keys" {
					return checklist.Rooms[0].Components[0], entry
				}
			}
			t.Fatalf("required keys record missing: %+v", handover.Entries)
			return core.ChecklistComponent{}, core.HandoverEntry{}
		}
		component, keys := build()
		if component.Status != "NOT_OBSERVED" || len(component.EvidenceIDs) != 0 || keys.Status != "PENDING" {
			t.Fatalf("OPEN recapture draft changed the report: component=%+v keys=%+v", component, keys)
		}
		if err := h.AdminDB.Model(&database.CaptureDraft{}).Where("tenant_id=? AND id=?", f.TenantID, draft.ID).Update("status", "SUBMITTED").Error; err != nil {
			t.Fatal(err)
		}
		component, keys = build()
		if component.Status == "NOT_OBSERVED" || len(component.EvidenceIDs) != 1 || keys.Status != "DECLARED" {
			t.Fatalf("SUBMITTED recapture draft did not reach the report: component=%+v keys=%+v", component, keys)
		}
	})
}
