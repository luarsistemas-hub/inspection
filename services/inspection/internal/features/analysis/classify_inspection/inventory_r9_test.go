package classify_inspection

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/analysis/inventory/inventorytest"
	"inspection/services/inspection/internal/platform/database"
)

// openRecaptureAnswer records an answer in a new RECAPTURE draft that is still
// OPEN (not submitted), as recapture/core creates it, and returns the draft ID.
func openRecaptureAnswer(t *testing.T, seed *inventorytest.Seed, key string, media []identity.ID, flags, impossibility string) identity.ID {
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
	if err := seed.DB.Create(&database.RequirementAnswer{ID: identity.NewID(), TenantID: seed.Tenant, DraftID: draft, RequirementKey: key, MediaIDs: ids, Flags: json.RawMessage(flags), ImpossibilityReason: impossibility, Version: 1, CreatedAt: later, UpdatedAt: later}).Error; err != nil {
		t.Fatal(err)
	}
	return draft
}

// Item 2 (round 9): an OPEN recapture declaring an environment impossible does
// not change the classification facts; once SUBMITTED, it does.
func TestR9OpenRecaptureImpossibilityDoesNotCountUntilSubmitted(t *testing.T) {
	seed := inventorytest.New(t, inventorytest.InspectionOptions{})
	baseline := seed.PinBaseline(t, room)
	env := baseline.Environments[0]
	seed.Answer(t, inventorytest.EnvironmentKey(env), nil, "")
	job := seed.Job(t, "COMPARE", inventorytest.EnvironmentKey(env), baseline.Version, &env.IdentityID)
	finish(t, seed, job, "SUCCEEDED", result(baseline.Items[0], "PRESENT_UNCHANGED", ""), result(baseline.Items[1], "PRESENT_UNCHANGED", ""))
	draft := openRecaptureAnswer(t, seed, inventorytest.EnvironmentKey(env), nil, `[]`, "LOCKED")
	if facts := inventoryFacts(t, seed).Facts; facts.EnvironmentsNotCaptured != 0 {
		t.Fatalf("OPEN recapture counted as an impossible environment: %+v", facts)
	}
	if err := seed.DB.Model(&database.CaptureDraft{}).Where("id=?", draft).Update("status", "SUBMITTED").Error; err != nil {
		t.Fatal(err)
	}
	if facts := inventoryFacts(t, seed).Facts; facts.EnvironmentsNotCaptured != 1 {
		t.Fatalf("SUBMITTED recapture impossibility not counted: %+v", facts)
	}
}

// Item 2 (round 9): the comparison facts (flags and impossibility of each
// requirement) read only SUBMITTED drafts.
func TestR9LatestSubmittedAnswerIgnoresOpenRecapture(t *testing.T) {
	seed := inventorytest.New(t, inventorytest.InspectionOptions{})
	key := "env:" + identity.NewID().String()
	seed.Answer(t, key, nil, "")
	draft := openRecaptureAnswer(t, seed, key, []identity.ID{identity.NewID()}, `["GPS_MISSING"]`, "LOCKED")
	var answer database.RequirementAnswer
	if err := latestSubmittedAnswer(context.Background(), seed.DB, seed.Tenant, seed.Inspection, key, &answer); err != nil {
		t.Fatal(err)
	}
	if answer.DraftID != seed.CaptureDraft || answer.ImpossibilityReason != "" {
		t.Fatalf("OPEN recapture answer used for classification: %+v", answer)
	}
	if err := seed.DB.Model(&database.CaptureDraft{}).Where("id=?", draft).Update("status", "SUBMITTED").Error; err != nil {
		t.Fatal(err)
	}
	var submitted database.RequirementAnswer
	if err := latestSubmittedAnswer(context.Background(), seed.DB, seed.Tenant, seed.Inspection, key, &submitted); err != nil {
		t.Fatal(err)
	}
	if submitted.DraftID != draft {
		t.Fatalf("SUBMITTED recapture answer not used: %+v", submitted)
	}
}
