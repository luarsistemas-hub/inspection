package handle_classified

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/analysis/inventory/inventorytest"
	requestinventoryjobs "inspection/services/inspection/internal/features/analysis/request_inventory_jobs"
	"inspection/services/inspection/internal/features/reports/core"
	"inspection/services/inspection/internal/platform/database"
)

// openRecaptureAnswer records an answer in a new RECAPTURE draft that is still
// OPEN (not submitted), as recapture/core creates it, and returns the draft ID.
func openRecaptureAnswer(t *testing.T, seed *inventorytest.Seed, key string, media []identity.ID, impossibility string) identity.ID {
	t.Helper()
	later := seed.Now.Add(time.Hour)
	responsibility, draft := identity.NewID(), identity.NewID()
	if err := seed.DB.Create(&database.Responsibility{ID: responsibility, TenantID: seed.Tenant, InspectionID: seed.Inspection, ParticipantID: identity.NewID(), Status: "PENDING", Version: 1, CreatedAt: later, UpdatedAt: later}).Error; err != nil {
		t.Fatal(err)
	}
	if err := seed.DB.Create(&database.CaptureDraft{ID: draft, TenantID: seed.Tenant, ResponsibilityID: responsibility, Kind: "RECAPTURE", MediaKind: "PHOTO", TemplateVersionID: seed.Template, ReferencePayload: json.RawMessage(`{}`), PolicyPayload: json.RawMessage(`{}`), Requirements: json.RawMessage(`[]`), Status: "OPEN", Version: 1, CreatedAt: later, UpdatedAt: later}).Error; err != nil {
		t.Fatal(err)
	}
	if media == nil {
		media = []identity.ID{}
	}
	ids, _ := json.Marshal(media)
	if err := seed.DB.Create(&database.RequirementAnswer{ID: identity.NewID(), TenantID: seed.Tenant, DraftID: draft, RequirementKey: key, MediaIDs: ids, Flags: json.RawMessage(`["IMPOSSIBILITY:LOCKED"]`), ImpossibilityReason: impossibility, Version: 1, CreatedAt: later, UpdatedAt: later}).Error; err != nil {
		t.Fatal(err)
	}
	return draft
}

// Item 2 (round 9): an OPEN recapture draft declaring an environment
// impossible to capture does not change the inventory section of an immutable
// report; once the recapture is SUBMITTED, it does.
func TestR9OpenRecaptureImpossibilityDoesNotChangeInventoryUntilSubmitted(t *testing.T) {
	f := newReportFixture(t)
	sala := f.baseline.Environments[0]
	if slices.Contains(f.section.Coverage.EnvironmentsNotCaptured, "Sala") {
		t.Fatalf("fixture already lists Sala as not captured: %+v", f.section.Coverage)
	}
	draft := openRecaptureAnswer(t, f.seed, inventorytest.EnvironmentKey(sala), nil, "LOCKED")
	section, _ := build(t, f.seed)
	if slices.Contains(section.Coverage.EnvironmentsNotCaptured, "Sala") || len(section.Problems) != len(f.section.Problems) {
		t.Fatalf("OPEN recapture changed the inventory: coverage=%+v problems=%d (was %d)", section.Coverage, len(section.Problems), len(f.section.Problems))
	}
	// The focused requirement list of the OPEN recapture does not replace the
	// submitted one (environment names and environments added during capture).
	if !slices.Equal(section.Coverage.EnvironmentsAddedDuringCapture, f.section.Coverage.EnvironmentsAddedDuringCapture) || len(f.section.Coverage.EnvironmentsAddedDuringCapture) == 0 {
		t.Fatalf("OPEN recapture replaced the submitted requirements: added=%v (was %v)", section.Coverage.EnvironmentsAddedDuringCapture, f.section.Coverage.EnvironmentsAddedDuringCapture)
	}
	if err := f.seed.DB.Model(&database.CaptureDraft{}).Where("id=?", draft).Update("status", "SUBMITTED").Error; err != nil {
		t.Fatal(err)
	}
	section, _ = build(t, f.seed)
	if !slices.Contains(section.Coverage.EnvironmentsNotCaptured, "Sala") {
		t.Fatalf("SUBMITTED recapture did not reach the inventory: %+v", section.Coverage)
	}
}

// Item 2 (round 9): the per-requirement answer (evidence and flags of the
// report) comes only from SUBMITTED drafts; an OPEN recapture is ignored until
// it is submitted, and then it is the latest answer.
func TestR9LatestSubmittedAnswerIgnoresOpenRecapture(t *testing.T) {
	f := newReportFixture(t)
	key := inventorytest.EnvironmentKey(f.baseline.Environments[0])
	var original database.RequirementAnswer
	if err := latestSubmittedAnswer(context.Background(), f.seed.DB, f.seed.Tenant, f.seed.Inspection, key, &original); err != nil {
		t.Fatal(err)
	}
	newMedia := identity.NewID()
	draft := openRecaptureAnswer(t, f.seed, key, []identity.ID{newMedia}, "")
	var answer database.RequirementAnswer
	if err := latestSubmittedAnswer(context.Background(), f.seed.DB, f.seed.Tenant, f.seed.Inspection, key, &answer); err != nil {
		t.Fatal(err)
	}
	if answer.ID != original.ID {
		t.Fatalf("OPEN recapture answer %s replaced the submitted answer %s", answer.ID, original.ID)
	}
	if err := f.seed.DB.Model(&database.CaptureDraft{}).Where("id=?", draft).Update("status", "SUBMITTED").Error; err != nil {
		t.Fatal(err)
	}
	var submitted database.RequirementAnswer
	if err := latestSubmittedAnswer(context.Background(), f.seed.DB, f.seed.Tenant, f.seed.Inspection, key, &submitted); err != nil {
		t.Fatal(err)
	}
	if submitted.DraftID != draft {
		t.Fatalf("SUBMITTED recapture answer was not used: %+v", submitted)
	}
}

// Item 3 (round 9): a classification superseded by a reissue (the newest
// COMPARE generation was opened after it) never publishes: neither while the
// new generation is pending (it used to wait and then mix generations) nor
// once it is terminal (it used to publish N's classification with N+1's
// inventory). The new classification produces the report.
func TestR9ClassificationSupersededByReanalysisGenerationIsAcknowledged(t *testing.T) {
	f := newReportFixture(t)
	classifiedAt := f.seed.Now.Add(time.Minute)
	var inspection database.Inspection
	var reference database.ReferenceSnapshot
	f.seed.DB.Where("id=?", f.seed.Inspection).Take(&inspection)
	f.seed.DB.Where("inspection_id=?", f.seed.Inspection).Take(&reference)
	var evidence []core.Evidence
	if _, err := buildInventoryReport(context.Background(), f.seed.DB, f.seed.Tenant, inspection, reference, classifiedAt, &evidence); err != nil {
		t.Fatalf("current classification must build: %v", err)
	}
	reissuedAt := classifiedAt.Add(time.Minute)
	if created, err := requestinventoryjobs.Reanalyze(context.Background(), f.seed.DB, f.seed.Tenant, f.seed.Inspection, reissuedAt); err != nil || created == 0 {
		t.Fatalf("Reanalyze created=%d err=%v", created, err)
	}
	if section, err := buildInventoryReport(context.Background(), f.seed.DB, f.seed.Tenant, inspection, reference, classifiedAt, &evidence); !errors.Is(err, errClassificationSuperseded) {
		t.Fatalf("pending newer generation: section=%v err=%v; want errClassificationSuperseded", section, err)
	}
	if err := f.seed.DB.Model(&database.InventoryJob{}).Where("inspection_id=? AND kind='COMPARE' AND generation=2", f.seed.Inspection).Update("status", "SUCCEEDED").Error; err != nil {
		t.Fatal(err)
	}
	if section, err := buildInventoryReport(context.Background(), f.seed.DB, f.seed.Tenant, inspection, reference, classifiedAt, &evidence); !errors.Is(err, errClassificationSuperseded) {
		t.Fatalf("terminal newer generation mixed with the old classification: section=%v err=%v", section, err)
	}
	// The classification created after the new generation builds from it.
	if section, err := buildInventoryReport(context.Background(), f.seed.DB, f.seed.Tenant, inspection, reference, reissuedAt.Add(time.Minute), &evidence); err != nil || section == nil || section.Coverage == nil {
		t.Fatalf("new classification: section=%+v err=%v", section, err)
	}
}
