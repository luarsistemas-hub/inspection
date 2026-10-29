package promote_inspection_to_origin

import (
	"context"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/inventory/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"

	"gorm.io/gorm"
)

func TestSetupRejectsMissingDependencies(t *testing.T) {
	if err := Setup(Dependencies{}); err == nil {
		t.Fatal("expected missing dependency error")
	}
}

func TestConfirmationDigestIsStableAndTracksEveryInput(t *testing.T) {
	base := DigestInput{OriginVersion: 4, ActiveVersionID: "active", DraftID: "draft", DraftStatus: "DRAFT", DraftState: "READY", CaptureDraftID: "capture", VideoPlanID: "plan", VideoGeneration: 2}
	want := ConfirmationDigest(base)
	if len(want) != 64 || ConfirmationDigest(base) != want {
		t.Fatalf("digest not stable: %q", want)
	}
	variants := []DigestInput{base, base, base, base, base, base, base}
	variants[0].OriginVersion++
	variants[1].ActiveVersionID = "other"
	variants[2].DraftID = "other"
	variants[3].DraftState = "PREPARING"
	variants[4].CaptureDraftID = "other"
	variants[5].VideoPlanID = "other"
	variants[6].VideoGeneration++
	for i, in := range variants {
		if ConfirmationDigest(in) == want {
			t.Errorf("variant %d did not change digest", i)
		}
	}
}

func TestDigestInputIgnoresOwnDraftStateButTracksExternalDraftState(t *testing.T) {
	inspectionID, draftID := identity.NewID(), identity.NewID()
	state := inspected{inspection: database.Inspection{ID: inspectionID}}
	draft := database.OriginVersion{ID: draftID, Source: "CAPTURE", SourceInspectionID: &inspectionID, Status: "DRAFT"}
	preparing, ready := "PREPARING", "READY"
	draft.DraftState = &preparing
	state.draft = &draft
	ownPreparing := ConfirmationDigest(digestInput(state))
	draft.DraftState = &ready
	state.draft = &draft
	if got := ConfirmationDigest(digestInput(state)); got != ownPreparing {
		t.Fatalf("own draft preparation state changed digest: %q != %q", got, ownPreparing)
	}
	draft.SourceInspectionID = ptrID(identity.NewID())
	draft.DraftState = &preparing
	state.draft = &draft
	externalPreparing := ConfirmationDigest(digestInput(state))
	draft.DraftState = &ready
	state.draft = &draft
	if got := ConfirmationDigest(digestInput(state)); got == externalPreparing {
		t.Fatal("external draft state change did not change digest")
	}
}

func ptrID(id identity.ID) *identity.ID { return &id }

func TestRequestValidatesInputBeforeUsingDependencies(t *testing.T) {
	_, err := request(context.Background(), Dependencies{}, Command{TenantID: identity.NewID(), InspectionID: identity.NewID()})
	if code, _, _ := apperror.Public(err); code != apperror.InvalidInput {
		t.Fatalf("expected invalid input, got %v", err)
	}
}

func TestSetupRegistersHandlersWhenDependenciesArePresent(t *testing.T) {
	bus := mediator.New()
	db := &gorm.DB{}
	if err := Setup(Dependencies{DB: db, Bus: bus, Inventory: core.Service{DB: db}}); err != nil {
		t.Fatalf("Setup should register mediator handlers: %v", err)
	}
	if _, err := bus.Send(context.Background(), Command{}); err == nil {
		t.Fatal("registered command should reject invalid input")
	}
}
