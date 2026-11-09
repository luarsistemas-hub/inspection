//go:build integration

package handle_classified

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
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
	"inspection/services/inspection/internal/platform/messaging"

	"gorm.io/gorm"
)

// r9Counts returns inbox receipts for the event, snapshots of the inspection
// and critical deliveries of the tenant.
func r9Counts(t *testing.T, ctx context.Context, h *harness.Harness, f harness.Task06Fixture, eventID identity.ID) (receipts, snapshots, deliveries int64) {
	t.Helper()
	if err := h.AdminDB.Model(&database.InboxReceipt{}).Where("tenant_id=? AND event_id=?", f.TenantID, eventID).Count(&receipts).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.AdminDB.Model(&database.ReportSnapshot{}).Where("tenant_id=? AND inspection_id=?", f.TenantID, f.InspectionID).Count(&snapshots).Error; err != nil {
		t.Fatal(err)
	}
	return receipts, snapshots, countDeliveries(t, h, ctx, f.TenantID)
}

// Item 1 (round 9): the FIRST classification of a v3 photo inspection whose
// inputs fail permanently has no reissue request to close. It is dead-lettered
// (messaging.ErrPermanent, nothing committed, no inbox receipt) exactly as v1/v2
// did, so the same event can be replayed once the inputs are corrected.
func TestR9FirstV3PermanentInputFailureIsDeadLetteredAndReplayable(t *testing.T) {
	withCriticalFixture(t, func(ctx context.Context, h *harness.Harness, f harness.Task06Fixture, deps request.Dependencies) {
		seedTask04Scope(t, ctx, h, f, "OBSERVED", false)
		if err := addCriticalChannel(ctx, h, f, f.MembershipID, "critical-r9@example.test", true); err != nil {
			t.Fatal(err)
		}
		runID := seedManualClassification(ctx, t, h, f, "CRITICAL")
		var scope database.InspectionScopeSnapshot
		if err := h.AdminDB.Where("tenant_id=? AND inspection_id=?", f.TenantID, f.InspectionID).Take(&scope).Error; err != nil {
			t.Fatal(err)
		}
		if err := h.AdminDB.Model(&database.InspectionScopeSnapshot{}).Where("tenant_id=? AND id=?", f.TenantID, scope.ID).Update("document", json.RawMessage(`{"rooms":"broken"}`)).Error; err != nil {
			t.Fatal(err)
		}
		eventID := identity.NewID()
		err := deliverClassified(ctx, h, f, deps, eventID, runID, "CRITICAL")
		if !errors.Is(err, messaging.ErrPermanent) {
			t.Fatalf("first classification with invalid inputs err=%v; want messaging.ErrPermanent (DLQ)", err)
		}
		if receipts, snapshots, deliveries := r9Counts(t, ctx, h, f, eventID); receipts != 0 || snapshots != 0 || deliveries != 0 {
			t.Fatalf("dead-lettered event committed receipts=%d snapshots=%d deliveries=%d", receipts, snapshots, deliveries)
		}
		// The inputs are corrected and the dead-lettered event is replayed.
		if err := h.AdminDB.Model(&database.InspectionScopeSnapshot{}).Where("tenant_id=? AND id=?", f.TenantID, scope.ID).Update("document", scope.Document).Error; err != nil {
			t.Fatal(err)
		}
		assertNoHang(t, deliverClassified(ctx, h, f, deps, eventID, runID, "CRITICAL"))
		if receipts, snapshots, deliveries := r9Counts(t, ctx, h, f, eventID); receipts != 1 || snapshots != 1 || deliveries == 0 {
			t.Fatalf("replay after correction receipts=%d snapshots=%d deliveries=%d; want the report and its alert", receipts, snapshots, deliveries)
		}
	})
}

// Item 1 (round 9): a permanent snapshot failure without a QUEUED reissue is
// dead-lettered too, and the adapter's partial write is rolled back with it.
func TestR9FirstV3PermanentSnapshotFailureIsDeadLetteredWithoutPartialWrite(t *testing.T) {
	withCriticalFixture(t, func(ctx context.Context, h *harness.Harness, f harness.Task06Fixture, deps request.Dependencies) {
		seedTask04Scope(t, ctx, h, f, "OBSERVED", false)
		if err := addCriticalChannel(ctx, h, f, f.MembershipID, "critical-r9b@example.test", true); err != nil {
			t.Fatal(err)
		}
		runID := seedManualClassification(ctx, t, h, f, "CRITICAL")
		marker := "task04-r9-partial-" + identity.NewID().String()
		eventID := identity.NewID()
		err := deliverClassifiedWith(ctx, h, f, deps, eventID, runID, "CRITICAL", func(handler *Dependencies) {
			handler.CreateSnapshotInTx = func(ctx context.Context, tx *gorm.DB, _ generatesnapshot.Input) (generatesnapshot.Result, error) {
				if err := tx.Create(&database.AuditEvent{ID: identity.NewID(), TenantID: f.TenantID, ActorID: f.MembershipID, Action: "report.partial_write", TargetType: "inspection", TargetID: f.InspectionID.String(), Outcome: "QUEUED", CorrelationID: marker, OccurredAt: time.Now().UTC()}).Error; err != nil {
					return generatesnapshot.Result{}, err
				}
				return generatesnapshot.Result{}, core.ErrInvalidSnapshot
			}
		})
		if !errors.Is(err, messaging.ErrPermanent) {
			t.Fatalf("first classification with an invalid snapshot err=%v; want messaging.ErrPermanent (DLQ)", err)
		}
		var partial int64
		if err := h.AdminDB.Model(&database.AuditEvent{}).Where("tenant_id=? AND correlation_id=?", f.TenantID, marker).Count(&partial).Error; err != nil || partial != 0 {
			t.Fatalf("partial snapshot write committed (%d rows, err=%v)", partial, err)
		}
		if receipts, snapshots, deliveries := r9Counts(t, ctx, h, f, eventID); receipts != 0 || snapshots != 0 || deliveries != 0 {
			t.Fatalf("dead-lettered event committed receipts=%d snapshots=%d deliveries=%d", receipts, snapshots, deliveries)
		}
	})
}

// Item 2 (round 9): through the real handler and PostgreSQL, the answer of an
// OPEN recapture (new photos for a requirement) does not enter the immutable
// report: neither its evidence nor its focused requirement list. Once the
// recapture is SUBMITTED, a new classification publishes a version with it.
func TestR9OpenRecaptureAnswerDoesNotEnterReportUntilSubmitted(t *testing.T) {
	withCriticalFixture(t, func(ctx context.Context, h *harness.Harness, f harness.Task06Fixture, deps request.Dependencies) {
		seedTask04Scope(t, ctx, h, f, "OBSERVED", false)
		requirementKey := "env:new:" + f.EnvironmentIdentityID.String()
		now := time.Now().UTC()
		if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
			return tx.Create(&database.ComparisonJob{ID: identity.NewID(), TenantID: f.TenantID, InspectionID: f.InspectionID, PromptSnapshotID: f.PromptSnapshotID, RequirementKey: requirementKey, ModelAlias: "test", InputDigest: "r9-open-recapture", Status: "COMPLETED", CreatedAt: now, UpdatedAt: now}).Error
		}); err != nil {
			t.Fatal(err)
		}
		recapture, err := (recapturecore.Service{DB: h.DB, Within: h.WithinTenant}).Request(ctx, recapturecore.RequestInput{TenantID: f.TenantID, InspectionID: f.InspectionID, Items: []recapturecore.Item{{RequirementKey: requirementKey, Reason: "Foto escura"}}, Delivery: []invitationcore.DeliveryIntent{{Channel: "EMAIL", Destination: "qa-r9@example.test", Verified: true}}, DeadlineAt: now.Add(24 * time.Hour), IdempotencyKey: "task04-r9-recapture-" + f.InspectionID.String()})
		if err != nil {
			t.Fatalf("create recapture through the production service: %v", err)
		}
		var draft database.CaptureDraft
		if err := h.AdminDB.Where("tenant_id=? AND responsibility_id=? AND kind='RECAPTURE'", f.TenantID, recapture.ResponsibilityID).Take(&draft).Error; err != nil || draft.Status != "OPEN" {
			t.Fatalf("recapture draft=%+v err=%v; want OPEN", draft, err)
		}
		var media database.MediaObject
		if err := h.AdminDB.Where("tenant_id=? AND id=?", f.TenantID, f.MediaID).Take(&media).Error; err != nil {
			t.Fatal(err)
		}
		newMedia := media
		newMedia.ID, newMedia.ResponsibilityID = identity.NewID(), recapture.ResponsibilityID
		newMedia.ObjectKey = media.ObjectKey + "-r9-" + newMedia.ID.String()
		newMedia.IdempotencyKey = "r9-recapture-media-" + newMedia.ID.String()
		newMedia.Description = "Foto da recaptura"
		if err := h.AdminDB.Create(&newMedia).Error; err != nil {
			t.Fatal(err)
		}
		later := time.Now().UTC().Add(time.Second)
		if err := h.AdminDB.Create(&database.RequirementAnswer{ID: identity.NewID(), TenantID: f.TenantID, DraftID: draft.ID, RequirementKey: requirementKey, MediaIDs: mustJSONIDs(t, newMedia.ID), Flags: json.RawMessage(`["GPS_MISSING"]`), Version: 1, CreatedAt: later, UpdatedAt: later}).Error; err != nil {
			t.Fatal(err)
		}
		latestReport := func() (core.Snapshot, int) {
			t.Helper()
			var row database.ReportSnapshot
			if err := h.AdminDB.Where("tenant_id=? AND inspection_id=?", f.TenantID, f.InspectionID).Order("version_number DESC").Take(&row).Error; err != nil {
				t.Fatal(err)
			}
			var snapshot core.Snapshot
			if err := json.Unmarshal(row.CanonicalJSON, &snapshot); err != nil {
				t.Fatal(err)
			}
			return snapshot, row.VersionNumber
		}
		hasEvidence := func(snapshot core.Snapshot, id identity.ID) bool {
			return slices.ContainsFunc(snapshot.Evidence, func(e core.Evidence) bool { return e.ID == id.String() })
		}
		runID := seedManualClassification(ctx, t, h, f, "ATTENTION")
		assertNoHang(t, deliverClassified(ctx, h, f, deps, identity.NewID(), runID, "ATTENTION"))
		first, firstVersion := latestReport()
		if hasEvidence(first, newMedia.ID) || !hasEvidence(first, f.MediaID) {
			t.Fatalf("OPEN recapture changed the report evidence: %+v", first.Evidence)
		}
		if len(first.Requirements) == 0 {
			t.Fatal("report lost the submitted requirements")
		}
		if err := h.AdminDB.Model(&database.CaptureDraft{}).Where("tenant_id=? AND id=?", f.TenantID, draft.ID).Update("status", "SUBMITTED").Error; err != nil {
			t.Fatal(err)
		}
		secondRun := identity.NewID()
		if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
			return tx.Create(&database.ClassificationRun{ID: secondRun, TenantID: f.TenantID, InspectionID: f.InspectionID, PromptSnapshotID: f.PromptSnapshotID, Classification: "ATTENTION", ReasonCodes: json.RawMessage(`[]`), CreatedAt: time.Now().UTC()}).Error
		}); err != nil {
			t.Fatal(err)
		}
		assertNoHang(t, deliverClassified(ctx, h, f, deps, identity.NewID(), secondRun, "ATTENTION"))
		second, secondVersion := latestReport()
		if secondVersion <= firstVersion || !hasEvidence(second, newMedia.ID) {
			t.Fatalf("SUBMITTED recapture did not reach a new version: first=v%d second=v%d evidence=%+v", firstVersion, secondVersion, second.Evidence)
		}
	})
}
