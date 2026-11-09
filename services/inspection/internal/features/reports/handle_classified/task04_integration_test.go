//go:build integration

package handle_classified

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	classifyinspection "inspection/services/inspection/internal/features/analysis/classify_inspection"
	invitationcore "inspection/services/inspection/internal/features/invitations/core"
	"inspection/services/inspection/internal/features/notifications/request"
	recapturecore "inspection/services/inspection/internal/features/recapture/core"
	"inspection/services/inspection/internal/features/reports/core"
	generatesnapshot "inspection/services/inspection/internal/features/reports/generate_snapshot"
	"inspection/services/inspection/internal/features/templates/catalog"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"
	"inspection/services/inspection/internal/platform/notifications"

	"gorm.io/gorm"
)

func seedTask04Scope(t *testing.T, ctx context.Context, h *harness.Harness, f harness.Task06Fixture, state string, handover bool) {
	t.Helper()
	roomKey := "new:" + f.EnvironmentIdentityID.String()
	requirementKey := "env:" + roomKey
	document := struct {
		Rooms           []catalog.ExpectedRoom   `json:"rooms"`
		HandoverRecords []catalog.HandoverRecord `json:"handoverRecords"`
	}{Rooms: []catalog.ExpectedRoom{{Key: roomKey, TypeCode: "KITCHEN", Name: "Cozinha", Components: []catalog.ExpectedComponent{{Key: "floor", TypeCode: "FLOOR", Label: "Piso", Views: []string{"wide"}}}}}}
	if handover {
		document.HandoverRecords = []catalog.HandoverRecord{{Key: "keys", Kind: "KEY", Required: true}}
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
		if err := tx.Create(&database.InspectionScopeSnapshot{ID: identity.NewID(), TenantID: f.TenantID, InspectionID: f.InspectionID, TemplateVersionID: f.TemplateVersionID, Profile: "PROPERTY_COMPONENTS", Document: encoded, Digest: "task04-scope", Version: 1, CreatedAt: time.Now().UTC()}).Error; err != nil {
			return err
		}
		var ids []byte
		if state == "OBSERVED" || state == "PARTIAL" {
			ids, _ = json.Marshal([]identity.ID{f.MediaID})
		} else {
			ids = json.RawMessage(`[]`)
		}
		return tx.Create(&database.ComponentAnswer{ID: identity.NewID(), TenantID: f.TenantID, DraftID: f.DraftID, RequirementKey: requirementKey, ComponentKey: "floor", ViewKey: "wide", State: state, Reason: "", EvidenceIDs: ids, Version: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}).Error
	}); err != nil {
		t.Fatal(err)
	}
}

// TestIT041SubmittedNoJobInspectionPublishesLimitedInconclusiveReport exercises
// the no-job classification fallback. Consent is still mandatory upstream, so
// no NOT_AUTHORIZED producer is wired into this flow.
func TestIT041SubmittedNoJobInspectionPublishesLimitedInconclusiveReport(t *testing.T) {
	withCriticalFixture(t, func(ctx context.Context, h *harness.Harness, f harness.Task06Fixture, _ request.Dependencies) {
		seedTask04Scope(t, ctx, h, f, "NOT_OBSERVED", false)
		classify, err := classifyinspection.Setup(classifyinspection.Dependencies{})
		if err != nil {
			t.Fatal(err)
		}
		envelope := events.RawEnvelope{ID: identity.NewID(), Type: "analysis.comparison_completed.v1", SchemaVersion: 1, OccurredAt: time.Now().UTC(), TenantID: f.TenantID, AggregateID: f.InspectionID, CorrelationID: "it041-no-job", Payload: json.RawMessage(`{"inspectionId":"` + f.InspectionID.String() + `"}`)}
		if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error { return classify(ctx, tx, envelope) }); err != nil {
			t.Fatalf("submitted inspection with no jobs was rejected: %v", err)
		}
		var class database.ClassificationRun
		if err := h.AdminDB.Where("tenant_id=? AND inspection_id=?", f.TenantID, f.InspectionID).Order("created_at DESC").Take(&class).Error; err != nil {
			t.Fatal(err)
		}
		if class.Classification == "NORMAL" {
			t.Fatalf("no-job fallback produced positive normal classification: %+v", class)
		}
		assertNoHang(t, deliverClassified(ctx, h, f, task04CriticalDependencies(t, h), identity.NewID(), class.ID, class.Classification))
		var row database.ReportSnapshot
		if err := h.AdminDB.Where("tenant_id=? AND inspection_id=?", f.TenantID, f.InspectionID).Take(&row).Error; err != nil {
			t.Fatal(err)
		}
		var snapshot core.Snapshot
		if err := json.Unmarshal(row.CanonicalJSON, &snapshot); err != nil {
			t.Fatal(err)
		}
		if snapshot.Processing.Status != "INCONCLUSIVE" || snapshot.Checklist.Rooms[0].Components[0].Condition != "" {
			t.Fatalf("no-job report status/condition=%+v/%q", snapshot.Processing, snapshot.Checklist.Rooms[0].Components[0].Condition)
		}
	})
}

func task04CriticalDependencies(t *testing.T, h *harness.Harness) request.Dependencies {
	t.Helper()
	providers, err := notifications.NewProviderResolver(notifications.ProviderTwilio)
	if err != nil {
		t.Fatal(err)
	}
	return request.Dependencies{DB: h.DB, Providers: providers, EnabledChannels: map[string]bool{"EMAIL": true}}
}

// TestIT011PublishedV3ContainsObservedComponentEvidenceAndReviewState exercises
// the classified handler, snapshot creation, and automatic publication.
func TestIT011PublishedV3ContainsObservedComponentEvidenceAndReviewState(t *testing.T) {
	withCriticalFixture(t, func(ctx context.Context, h *harness.Harness, f harness.Task06Fixture, _ request.Dependencies) {
		seedTask04Scope(t, ctx, h, f, "OBSERVED", false)
		if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
			return tx.Create(&database.ComparisonJob{ID: identity.NewID(), TenantID: f.TenantID, InspectionID: f.InspectionID, PromptSnapshotID: f.PromptSnapshotID, RequirementKey: "env:new:" + f.EnvironmentIdentityID.String(), ModelAlias: "test", InputDigest: "it011", Status: "COMPLETED", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}).Error
		}); err != nil {
			t.Fatal(err)
		}
		runID := seedManualClassification(ctx, t, h, f, "NORMAL")
		assertNoHang(t, deliverClassified(ctx, h, f, task04CriticalDependencies(t, h), identity.NewID(), runID, "NORMAL"))
		var row database.ReportSnapshot
		if err := h.AdminDB.Where("tenant_id=? AND inspection_id=?", f.TenantID, f.InspectionID).Take(&row).Error; err != nil {
			t.Fatal(err)
		}
		var snapshot core.Snapshot
		if err := json.Unmarshal(row.CanonicalJSON, &snapshot); err != nil {
			t.Fatal(err)
		}
		component := snapshot.Checklist.Rooms[0].Components[0]
		if snapshot.SchemaVersion != 3 || component.Status != "PARTIAL" || component.ReviewStatus != "NOT_REVIEWED" || len(component.EvidenceIDs) == 0 || snapshot.Checklist.Coverage.Expected != 1 || snapshot.Checklist.Coverage.FullyAssessed != 0 || snapshot.Checklist.Coverage.PartiallyAssessed != 1 || len(component.Limitations) == 0 || snapshot.Processing.Status != "COMPLETED" {
			t.Fatalf("v3 snapshot does not retain expected checklist/evidence: %+v", snapshot)
		}
		var status string
		if err := h.AdminDB.Model(&database.ReportPublication{}).Select("status").Where("tenant_id=? AND snapshot_id=?", f.TenantID, row.ID).Scan(&status).Error; err != nil || status != "PUBLISHED" {
			t.Fatalf("publication status=%s err=%v, want PUBLISHED", status, err)
		}
	})
}

// TestTask04RecaptureDraftKeepsDeclaredHandoverRecords proves that a real
// RECAPTURE draft, created by the recapture service without handover records,
// does not hide declarations made in earlier drafts of the inspection, and
// that the most recent declaration of a record key wins.
func TestTask04RecaptureDraftKeepsDeclaredHandoverRecords(t *testing.T) {
	withCriticalFixture(t, func(ctx context.Context, h *harness.Harness, f harness.Task06Fixture, _ request.Dependencies) {
		seedTask04Scope(t, ctx, h, f, "OBSERVED", true)
		// A second required record that is never declared must stay PENDING.
		roomKey := "new:" + f.EnvironmentIdentityID.String()
		document, err := json.Marshal(struct {
			Rooms           []catalog.ExpectedRoom   `json:"rooms"`
			HandoverRecords []catalog.HandoverRecord `json:"handoverRecords"`
		}{Rooms: []catalog.ExpectedRoom{{Key: roomKey, TypeCode: "KITCHEN", Name: "Cozinha", Components: []catalog.ExpectedComponent{{Key: "floor", TypeCode: "FLOOR", Label: "Piso", Views: []string{"wide"}}}}}, HandoverRecords: []catalog.HandoverRecord{{Key: "keys", Kind: "KEY", Required: true}, {Key: "energy-meter", Kind: "METER", Required: true}}})
		if err != nil {
			t.Fatal(err)
		}
		if err := h.AdminDB.Model(&database.InspectionScopeSnapshot{}).Where("tenant_id=? AND inspection_id=?", f.TenantID, f.InspectionID).Update("document", json.RawMessage(document)).Error; err != nil {
			t.Fatal(err)
		}
		declaredAt := time.Now().UTC().Add(-time.Hour)
		if err := h.AdminDB.Create(&database.HandoverRecord{ID: identity.NewID(), TenantID: f.TenantID, DraftID: f.DraftID, RecordKey: "keys", Kind: "KEY", Payload: json.RawMessage(`{"keyType":"FRONT_DOOR","quantity":2}`), MediaIDs: json.RawMessage(`[]`), Declarant: "Participante", Version: 1, CreatedAt: declaredAt, UpdatedAt: declaredAt}).Error; err != nil {
			t.Fatal(err)
		}
		recapture, err := (recapturecore.Service{DB: h.DB, Within: h.WithinTenant}).Request(ctx, recapturecore.RequestInput{TenantID: f.TenantID, InspectionID: f.InspectionID, Items: []recapturecore.Item{{RequirementKey: "env:new:" + f.EnvironmentIdentityID.String(), Reason: "Foto desfocada"}}, Delivery: []invitationcore.DeliveryIntent{{Channel: "EMAIL", Destination: "qa@example.test", Verified: true}}, DeadlineAt: time.Now().UTC().Add(24 * time.Hour), IdempotencyKey: "task04-recapture-" + f.InspectionID.String()})
		if err != nil {
			t.Fatalf("create recapture through the production service: %v", err)
		}
		var recaptureDraft database.CaptureDraft
		if err := h.AdminDB.Where("tenant_id=? AND responsibility_id=? AND kind='RECAPTURE'", f.TenantID, recapture.ResponsibilityID).Take(&recaptureDraft).Error; err != nil {
			t.Fatal(err)
		}
		var copied int64
		if err := h.AdminDB.Model(&database.HandoverRecord{}).Where("tenant_id=? AND draft_id=?", f.TenantID, recaptureDraft.ID).Count(&copied).Error; err != nil || copied != 0 {
			t.Fatalf("the production recapture draft is expected to start without declarations: count=%d err=%v", copied, err)
		}
		evidence := []core.Evidence{{ID: f.MediaID.String(), RequirementKey: "env:new:" + f.EnvironmentIdentityID.String(), Role: "CURRENT", Availability: "AVAILABLE"}}
		build := func() map[string]core.HandoverEntry {
			t.Helper()
			var handover *core.HandoverSection
			if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
				_, _, _, result, _, err := buildDescriptiveReport(ctx, tx, f.TenantID, f.InspectionID, evidence, nil, "prompt", "COMPLETED")
				handover = result
				return err
			}); err != nil {
				t.Fatal(err)
			}
			byKey := make(map[string]core.HandoverEntry, len(handover.Entries))
			for _, entry := range handover.Entries {
				if _, duplicate := byKey[entry.Key]; duplicate {
					t.Fatalf("record key %q projected twice: %+v", entry.Key, handover.Entries)
				}
				byKey[entry.Key] = entry
			}
			if len(byKey) != 2 {
				t.Fatalf("handover entries = %+v, want declared keys and pending meter", handover.Entries)
			}
			return byKey
		}
		quantity := func(entry core.HandoverEntry) any {
			var payload map[string]any
			if err := json.Unmarshal(entry.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			return payload["quantity"]
		}
		entries := build()
		if keys := entries["keys"]; keys.Status != "DECLARED" || quantity(keys) != float64(2) {
			t.Fatalf("declaration from the original draft was lost after recapture: %+v", keys)
		}
		if meter := entries["energy-meter"]; meter.Status != "PENDING" {
			t.Fatalf("never-declared required meter should remain pending: %+v", meter)
		}
		// A newer declaration of the same key in the recapture draft wins once
		// that draft is submitted; while it is OPEN it contributes nothing.
		now := time.Now().UTC()
		if err := h.AdminDB.Create(&database.HandoverRecord{ID: identity.NewID(), TenantID: f.TenantID, DraftID: recaptureDraft.ID, RecordKey: "keys", Kind: "KEY", Payload: json.RawMessage(`{"keyType":"FRONT_DOOR","quantity":3}`), MediaIDs: json.RawMessage(`[]`), Declarant: "Participante", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
		if recaptureDraft.Status != "OPEN" {
			t.Fatalf("the production recapture draft is expected to start OPEN, got %s", recaptureDraft.Status)
		}
		entries = build()
		if keys := entries["keys"]; quantity(keys) != float64(2) {
			t.Fatalf("a declaration of an OPEN recapture draft reached the report: %+v", keys)
		}
		if err := h.AdminDB.Model(&database.CaptureDraft{}).Where("tenant_id=? AND id=?", f.TenantID, recaptureDraft.ID).Update("status", "SUBMITTED").Error; err != nil {
			t.Fatal(err)
		}
		entries = build()
		if keys := entries["keys"]; keys.Status != "DECLARED" || quantity(keys) != float64(3) {
			t.Fatalf("most recent declaration of the record key did not win: %+v", keys)
		}
		if meter := entries["energy-meter"]; meter.Status != "PENDING" {
			t.Fatalf("never-declared required meter should remain pending: %+v", meter)
		}
	})
}

// TestTask04PermanentInputFailureClosesQueuedReissue delivers the classified
// event through the real inbox consumer and the real snapshot adapter with
// inputs that can never form a report (corrupt pinned scope, corrupt evidence
// references). The event is acknowledged once instead of retried forever, no
// snapshot is created and the pending reissue request is closed as FAILED.
func TestTask04PermanentInputFailureClosesQueuedReissue(t *testing.T) {
	cases := map[string]func(t *testing.T, h *harness.Harness, f harness.Task06Fixture){
		"corrupt pinned scope": func(t *testing.T, h *harness.Harness, f harness.Task06Fixture) {
			if err := h.AdminDB.Model(&database.InspectionScopeSnapshot{}).Where("tenant_id=? AND inspection_id=?", f.TenantID, f.InspectionID).Update("document", json.RawMessage(`{"rooms":"broken"}`)).Error; err != nil {
				t.Fatal(err)
			}
		},
		"corrupt component evidence ids": func(t *testing.T, h *harness.Harness, f harness.Task06Fixture) {
			if err := h.AdminDB.Model(&database.ComponentAnswer{}).Where("tenant_id=? AND draft_id=?", f.TenantID, f.DraftID).Update("evidence_ids", json.RawMessage(`{"id":1}`)).Error; err != nil {
				t.Fatal(err)
			}
		},
		"corrupt handover media ids": func(t *testing.T, h *harness.Harness, f harness.Task06Fixture) {
			now := time.Now().UTC()
			if err := h.AdminDB.Create(&database.HandoverRecord{ID: identity.NewID(), TenantID: f.TenantID, DraftID: f.DraftID, RecordKey: "keys", Kind: "KEY", Payload: json.RawMessage(`{"quantity":1}`), MediaIDs: json.RawMessage(`{"id":1}`), Declarant: "Participante", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, corrupt := range cases {
		t.Run(name, func(t *testing.T) {
			withCriticalFixture(t, func(ctx context.Context, h *harness.Harness, f harness.Task06Fixture, _ request.Dependencies) {
				seedTask04Scope(t, ctx, h, f, "OBSERVED", true)
				runID := seedManualClassification(ctx, t, h, f, "ATTENTION")
				corrupt(t, h, f)
				correlation := "task04-permanent-" + identity.NewID().String()
				if err := h.AdminDB.Create(&database.AuditEvent{ID: identity.NewID(), TenantID: f.TenantID, ActorID: f.MembershipID, Action: "report.reissue_requested", TargetType: "inspection", TargetID: f.InspectionID.String(), Outcome: "QUEUED", Reason: "expectedVersion=1", CorrelationID: correlation, OccurredAt: time.Now().UTC()}).Error; err != nil {
					t.Fatal(err)
				}
				var before int64
				if err := h.AdminDB.Model(&database.ReportSnapshot{}).Where("tenant_id=? AND inspection_id=?", f.TenantID, f.InspectionID).Count(&before).Error; err != nil {
					t.Fatal(err)
				}
				eventID := identity.NewID()
				err := deliverClassified(ctx, h, f, task04CriticalDependencies(t, h), eventID, runID, "ATTENTION")
				if errors.Is(err, messaging.ErrPermanent) || err != nil {
					t.Fatalf("permanent input failure err=%v; want the event acknowledged once, not retried or dead-lettered without closing the request", err)
				}
				var receipts, after int64
				if err := h.AdminDB.Model(&database.InboxReceipt{}).Where("tenant_id=? AND event_id=?", f.TenantID, eventID).Count(&receipts).Error; err != nil || receipts != 1 {
					t.Fatalf("inbox receipts=%d err=%v; want the permanent failure committed once", receipts, err)
				}
				if err := h.AdminDB.Model(&database.ReportSnapshot{}).Where("tenant_id=? AND inspection_id=?", f.TenantID, f.InspectionID).Count(&after).Error; err != nil || after != before {
					t.Fatalf("snapshots before=%d after=%d err=%v; invalid inputs must not publish", before, after, err)
				}
				var event database.AuditEvent
				if err := h.AdminDB.Where("tenant_id=? AND action='report.reissue_requested' AND correlation_id=?", f.TenantID, correlation).Take(&event).Error; err != nil {
					t.Fatal(err)
				}
				if event.Outcome != "FAILED" {
					t.Fatalf("permanent failure left the reissue request %s", event.Outcome)
				}
			})
		})
	}
}

// TestTask04InvalidSnapshotClosesQueuedReissue covers the snapshot-validation
// branch: ErrInvalidSnapshot from the snapshot adapter closes the request.
func TestTask04InvalidSnapshotClosesQueuedReissue(t *testing.T) {
	withCriticalFixture(t, func(ctx context.Context, h *harness.Harness, f harness.Task06Fixture, deps request.Dependencies) {
		seedTask04Scope(t, ctx, h, f, "OBSERVED", false)
		runID := seedManualClassification(ctx, t, h, f, "ATTENTION")
		now := time.Now().UTC()
		if err := h.AdminDB.Create(&database.AuditEvent{ID: identity.NewID(), TenantID: f.TenantID, ActorID: f.MembershipID, Action: "report.reissue_requested", TargetType: "inspection", TargetID: f.InspectionID.String(), Outcome: "QUEUED", Reason: "expectedVersion=1", CorrelationID: "task04-invalid-snapshot", OccurredAt: now}).Error; err != nil {
			t.Fatal(err)
		}
		err := deliverClassifiedWith(ctx, h, f, deps, identity.NewID(), runID, "ATTENTION", func(handler *Dependencies) {
			handler.CreateSnapshotInTx = func(context.Context, *gorm.DB, generatesnapshot.Input) (generatesnapshot.Result, error) {
				return generatesnapshot.Result{}, core.ErrInvalidSnapshot
			}
		})
		assertNoHang(t, err)
		var event database.AuditEvent
		if err := h.AdminDB.Where("tenant_id=? AND action='report.reissue_requested' AND correlation_id=?", f.TenantID, "task04-invalid-snapshot").Take(&event).Error; err != nil {
			t.Fatal(err)
		}
		if event.Outcome != "FAILED" {
			t.Fatalf("permanent invalid snapshot left reissue pending forever: outcome=%s", event.Outcome)
		}
	})
}

func TestTask04UnavailableObservedMediaDoesNotCountAsFullyAssessed(t *testing.T) {
	withCriticalFixture(t, func(ctx context.Context, h *harness.Harness, f harness.Task06Fixture, _ request.Dependencies) {
		seedTask04Scope(t, ctx, h, f, "OBSERVED", false)
		if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
			if err := tx.Where("tenant_id=? AND media_id=? AND kind='DISPLAY'", f.TenantID, f.MediaID).Delete(&database.MediaDerivative{}).Error; err != nil {
				return err
			}
			return tx.Create(&database.ComparisonJob{ID: identity.NewID(), TenantID: f.TenantID, InspectionID: f.InspectionID, PromptSnapshotID: f.PromptSnapshotID, RequirementKey: "env:new:" + f.EnvironmentIdentityID.String(), ModelAlias: "test", InputDigest: "task04-unavailable", Status: "COMPLETED", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}).Error
		}); err != nil {
			t.Fatal(err)
		}
		runID := seedManualClassification(ctx, t, h, f, "ATTENTION")
		assertNoHang(t, deliverClassified(ctx, h, f, task04CriticalDependencies(t, h), identity.NewID(), runID, "ATTENTION"))
		var row database.ReportSnapshot
		if err := h.AdminDB.Where("tenant_id=? AND inspection_id=?", f.TenantID, f.InspectionID).Take(&row).Error; err != nil {
			t.Fatal(err)
		}
		var snapshot core.Snapshot
		if err := json.Unmarshal(row.CanonicalJSON, &snapshot); err != nil {
			t.Fatal(err)
		}
		component := snapshot.Checklist.Rooms[0].Components[0]
		if component.Status != "PARTIAL" || snapshot.Checklist.Coverage.FullyAssessed != 0 || snapshot.Checklist.Coverage.PartiallyAssessed != 1 || len(component.Limitations) == 0 {
			t.Fatalf("unavailable observed evidence was counted complete: component=%+v coverage=%+v", component, snapshot.Checklist.Coverage)
		}
	})
}

func TestV3CanceledAndInvalidatedInspectionStaysUnpublished(t *testing.T) {
	for _, status := range []string{"CANCELED", "INVALIDATED"} {
		t.Run(status, func(t *testing.T) {
			withCriticalFixture(t, func(ctx context.Context, h *harness.Harness, f harness.Task06Fixture, _ request.Dependencies) {
				seedTask04Scope(t, ctx, h, f, "NOT_OBSERVED", false)
				if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
					if err := tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", f.TenantID, f.InspectionID).Update("status", status).Error; err != nil {
						return err
					}
					return tx.Create(&database.ComparisonJob{ID: identity.NewID(), TenantID: f.TenantID, InspectionID: f.InspectionID, PromptSnapshotID: f.PromptSnapshotID, RequirementKey: "env:new:" + f.EnvironmentIdentityID.String(), ModelAlias: "test", InputDigest: "task04-canceled", Status: "COMPLETED", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}).Error
				}); err != nil {
					t.Fatal(err)
				}
				runID := seedManualClassification(ctx, t, h, f, "ATTENTION")
				assertNoHang(t, deliverClassified(ctx, h, f, task04CriticalDependencies(t, h), identity.NewID(), runID, "ATTENTION"))
				var row database.ReportSnapshot
				if err := h.AdminDB.Where("tenant_id=? AND inspection_id=?", f.TenantID, f.InspectionID).Take(&row).Error; err != nil {
					t.Fatalf("v3 snapshot was not created: %v", err)
				}
				var publications int64
				if err := h.AdminDB.Model(&database.ReportPublication{}).Where("tenant_id=? AND snapshot_id=?", f.TenantID, row.ID).Count(&publications).Error; err != nil || publications != 0 {
					t.Fatalf("terminal inspection publications=%d err=%v, want zero", publications, err)
				}
			})
		})
	}
}

// TestIT012IT055IT056LimitedReportsPublishFailureCoverageAndHandoverGap checks
// failure, an unobserved expected component, and a missing required key entry.
func TestIT012IT055IT056LimitedReportsPublishFailureCoverageAndHandoverGap(t *testing.T) {
	withCriticalFixture(t, func(ctx context.Context, h *harness.Harness, f harness.Task06Fixture, _ request.Dependencies) {
		seedTask04Scope(t, ctx, h, f, "NOT_OBSERVED", true)
		if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
			if err := tx.Create(&database.ComparisonJob{ID: identity.NewID(), TenantID: f.TenantID, InspectionID: f.InspectionID, PromptSnapshotID: f.PromptSnapshotID, RequirementKey: "env:new:" + f.EnvironmentIdentityID.String(), ModelAlias: "test", InputDigest: "it012-comparison", Status: "FAILED", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}).Error; err != nil {
				return err
			}
			now := time.Now().UTC()
			originID, versionID := identity.NewID(), identity.NewID()
			if err := tx.Create(&database.Origin{ID: originID, TenantID: f.TenantID, AssetID: f.AssetID, Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
				return err
			}
			if err := tx.Create(&database.OriginVersion{ID: versionID, TenantID: f.TenantID, OriginID: originID, VersionNumber: 1, Source: "CAPTURE", SourceInspectionID: &f.InspectionID, HasInventory: true, Status: "DRAFT", IdempotencyKey: "it012-catalog-" + versionID.String(), CreatedAt: now}).Error; err != nil {
				return err
			}
			return tx.Create(&database.InventoryJob{ID: identity.NewID(), TenantID: f.TenantID, InspectionID: f.InspectionID, OriginVersionID: versionID, Kind: "CATALOG", RequirementKey: "catalog", Generation: 1, Status: "FAILED", NextAttemptAt: now, ModelRevision: "test", CreatedAt: now, UpdatedAt: now}).Error
		}); err != nil {
			t.Fatal(err)
		}
		runID := seedManualClassification(ctx, t, h, f, "NORMAL")
		assertNoHang(t, deliverClassified(ctx, h, f, task04CriticalDependencies(t, h), identity.NewID(), runID, "NORMAL"))
		var row database.ReportSnapshot
		if err := h.AdminDB.Where("tenant_id=? AND inspection_id=?", f.TenantID, f.InspectionID).Take(&row).Error; err != nil {
			t.Fatal(err)
		}
		var snapshot core.Snapshot
		if err := json.Unmarshal(row.CanonicalJSON, &snapshot); err != nil {
			t.Fatal(err)
		}
		if snapshot.Processing.Status != "FAILED" || !strings.Contains(snapshot.Processing.Message, "catalogação") || snapshot.Checklist.Coverage.Expected != 1 || snapshot.Checklist.Coverage.NotObserved != 1 || len(snapshot.Handover.Entries) != 1 || snapshot.Handover.Entries[0].Key != "keys" || snapshot.Handover.Entries[0].Status != "PENDING" {
			t.Fatalf("limited report facts are incomplete: %+v", snapshot)
		}
		if snapshot.Checklist.Rooms[0].Components[0].Condition != "" || !strings.Contains(strings.Join(snapshot.Checklist.Rooms[0].Components[0].Limitations, " "), "Falha na catalogação") {
			t.Fatalf("failed/unobserved analysis made a positive condition claim: %+v", snapshot.Checklist.Rooms[0].Components[0])
		}
	})
}

// TestIT056HandoverPhotoIsReportEvidence verifies that a declared handover
// photo is included in the immutable report and remains available to readers.
func TestIT056HandoverPhotoIsReportEvidence(t *testing.T) {
	withCriticalFixture(t, func(ctx context.Context, h *harness.Harness, f harness.Task06Fixture, deps request.Dependencies) {
		seedTask04Scope(t, ctx, h, f, "NOT_OBSERVED", true)
		now := time.Now().UTC()
		if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
			if err := tx.Create(&database.HandoverRecord{ID: identity.NewID(), TenantID: f.TenantID, DraftID: f.DraftID, RecordKey: "keys", Kind: "KEY", Payload: json.RawMessage(`{"quantity":2}`), MediaIDs: mustJSONIDs(t, f.MediaID), Declarant: "Participante", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
				return err
			}
			return tx.Create(&database.MediaDerivative{ID: identity.NewID(), TenantID: f.TenantID, MediaID: f.MediaID, ObjectKey: "task04-display/" + f.MediaID.String(), Kind: "DISPLAY", SHA256: strings.Repeat("e", 64), ContentType: "image/png", Width: 1, Height: 1, SizeBytes: 8, CreatedAt: now}).Error
		}); err != nil {
			t.Fatal(err)
		}
		runID := seedManualClassification(ctx, t, h, f, "ATTENTION")
		assertNoHang(t, deliverClassified(ctx, h, f, deps, identity.NewID(), runID, "ATTENTION"))
		var row database.ReportSnapshot
		if err := h.AdminDB.Where("tenant_id=? AND inspection_id=?", f.TenantID, f.InspectionID).Take(&row).Error; err != nil {
			t.Fatal(err)
		}
		var snapshot core.Snapshot
		if err := json.Unmarshal(row.CanonicalJSON, &snapshot); err != nil {
			t.Fatal(err)
		}
		if len(snapshot.Handover.Entries) != 1 || len(snapshot.Handover.Entries[0].EvidenceIDs) != 1 || snapshot.Handover.Entries[0].EvidenceIDs[0] != f.MediaID.String() {
			t.Fatalf("handover media missing from report: %+v", snapshot.Handover)
		}
		found := false
		for _, evidence := range snapshot.Evidence {
			if evidence.ID == f.MediaID.String() && evidence.Availability == "AVAILABLE" {
				found = true
			}
		}
		if !found {
			t.Fatalf("handover media not available in report evidence: %+v", snapshot.Evidence)
		}
		var publication database.ReportPublication
		if err := h.AdminDB.Where("tenant_id=? AND snapshot_id=? AND status='PUBLISHED'", f.TenantID, row.ID).Take(&publication).Error; err != nil {
			t.Fatalf("v3 report did not publish under manual tenant policy: %v", err)
		}
	})
}

func mustJSONIDs(t *testing.T, id identity.ID) json.RawMessage {
	t.Helper()
	value, err := json.Marshal([]identity.ID{id})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestLegacyInspectionWithoutPinnedScopeKeepsPreV3Snapshot(t *testing.T) {
	withCriticalFixture(t, func(ctx context.Context, h *harness.Harness, f harness.Task06Fixture, deps request.Dependencies) {
		runID := seedManualClassification(ctx, t, h, f, "ATTENTION")
		assertNoHang(t, deliverClassified(ctx, h, f, deps, identity.NewID(), runID, "ATTENTION"))
		var row database.ReportSnapshot
		if err := h.AdminDB.Where("tenant_id=? AND inspection_id=?", f.TenantID, f.InspectionID).Take(&row).Error; err != nil {
			t.Fatal(err)
		}
		var snapshot core.Snapshot
		if err := json.Unmarshal(row.CanonicalJSON, &snapshot); err != nil {
			t.Fatal(err)
		}
		if snapshot.SchemaVersion >= 3 || snapshot.Checklist != nil || snapshot.Processing != nil || snapshot.Scope != nil || snapshot.Handover != nil {
			t.Fatalf("legacy inspection was promoted to descriptive v3: %+v", snapshot)
		}
	})
}

func TestV3EmptyPinnedScopeIsMarkedUnknown(t *testing.T) {
	withCriticalFixture(t, func(ctx context.Context, h *harness.Harness, f harness.Task06Fixture, deps request.Dependencies) {
		if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
			return tx.Create(&database.InspectionScopeSnapshot{ID: identity.NewID(), TenantID: f.TenantID, InspectionID: f.InspectionID, TemplateVersionID: f.TemplateVersionID, Profile: "PROPERTY_COMPONENTS", Document: json.RawMessage(`{"rooms":[]}`), Digest: strings.Repeat("a", 64), Version: 1, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}).Error
		}); err != nil {
			t.Fatal(err)
		}
		runID := seedManualClassification(ctx, t, h, f, "NORMAL")
		assertNoHang(t, deliverClassified(ctx, h, f, deps, identity.NewID(), runID, "NORMAL"))
		var row database.ReportSnapshot
		if err := h.AdminDB.Where("tenant_id=? AND inspection_id=?", f.TenantID, f.InspectionID).Take(&row).Error; err != nil {
			t.Fatal(err)
		}
		var snapshot core.Snapshot
		if err := json.Unmarshal(row.CanonicalJSON, &snapshot); err != nil {
			t.Fatal(err)
		}
		if snapshot.SchemaVersion != 3 || !snapshot.Scope.Unknown || !snapshot.Checklist.Coverage.UnknownScope || !strings.Contains(string(row.HTML), "Escopo não definido") || strings.Contains(string(row.HTML), "0 componentes esperados") {
			t.Fatalf("empty scope was presented as a defined zero denominator: scope=%+v coverage=%+v html=%s", snapshot.Scope, snapshot.Checklist.Coverage, row.HTML)
		}
	})
}

// TestIT059DistinctClassifiedDeliveriesDeduplicateTimeAndVersionChangedFacts
// covers re-emission with stable facts and a later correction to those facts.
func TestIT059DistinctClassifiedDeliveriesDeduplicateTimeAndVersionChangedFacts(t *testing.T) {
	withCriticalFixture(t, func(ctx context.Context, h *harness.Harness, f harness.Task06Fixture, deps request.Dependencies) {
		seedTask04Scope(t, ctx, h, f, "OBSERVED", false)
		if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
			return tx.Create(&database.ComparisonJob{ID: identity.NewID(), TenantID: f.TenantID, InspectionID: f.InspectionID, PromptSnapshotID: f.PromptSnapshotID, RequirementKey: "env:new:" + f.EnvironmentIdentityID.String(), ModelAlias: "test", InputDigest: "it059", Status: "COMPLETED", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}).Error
		}); err != nil {
			t.Fatal(err)
		}
		runID := seedManualClassification(ctx, t, h, f, "NORMAL")
		firstEvent := identity.NewID()
		assertNoHang(t, deliverClassified(ctx, h, f, deps, firstEvent, runID, "NORMAL"))
		var original database.ReportSnapshot
		if err := h.AdminDB.Where("tenant_id=? AND inspection_id=?", f.TenantID, f.InspectionID).Take(&original).Error; err != nil {
			t.Fatal(err)
		}
		originalJSON := append([]byte(nil), original.CanonicalJSON...)
		originalHTML := append([]byte(nil), original.HTML...)

		// A distinct event with a different OccurredAt has the same canonical facts.
		assertNoHang(t, deliverClassified(ctx, h, f, deps, identity.NewID(), runID, "NORMAL"))
		var count int64
		if err := h.AdminDB.Model(&database.ReportSnapshot{}).Where("tenant_id=? AND inspection_id=?", f.TenantID, f.InspectionID).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("unchanged classified re-emission created %d snapshots, want one", count)
		}

		// A changed participant answer is a report fact and must create a new version.
		if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
			return tx.Model(&database.ComponentAnswer{}).Where("tenant_id=? AND draft_id=? AND component_key=?", f.TenantID, f.DraftID, "floor").Updates(map[string]any{"state": "PARTIAL", "updated_at": time.Now().UTC()}).Error
		}); err != nil {
			t.Fatal(err)
		}
		assertNoHang(t, deliverClassified(ctx, h, f, deps, identity.NewID(), runID, "NORMAL"))
		var versions []database.ReportSnapshot
		if err := h.AdminDB.Where("tenant_id=? AND inspection_id=?", f.TenantID, f.InspectionID).Order("version_number ASC").Find(&versions).Error; err != nil {
			t.Fatal(err)
		}
		if len(versions) != 2 || versions[0].VersionNumber != 1 || versions[1].VersionNumber != 2 || bytes.Equal(versions[0].CanonicalJSON, versions[1].CanonicalJSON) {
			t.Fatalf("changed facts did not create exactly version 2: %+v", versions)
		}
		if !bytes.Equal(versions[0].CanonicalJSON, originalJSON) || !bytes.Equal(versions[0].HTML, originalHTML) {
			t.Fatal("the first immutable report changed after re-emission")
		}
	})
}
