package process_inventory_job

import (
	"testing"

	"inspection/libs/identity"
)

func TestUT031CatalogInputDigestTracksTranscriptRevision(t *testing.T) {
	environment, media, note := identity.NewID(), identity.NewID(), identity.NewID()
	start, end := int64(1000), int64(2500)
	input := CatalogInput{ProfileDigest: "furnished-v1", MediaGenerationID: identity.NewID(), EnvironmentID: environment, Notes: []CatalogNote{{NoteID: note, RevisionID: identity.NewID(), EnvironmentID: environment, TargetKind: "VIDEO_INTERVAL", SourceMediaID: media, SourceGeneration: 2, StartMS: &start, EndMS: &end, Text: "cinco pratos"}}}
	first, err := input.Digest()
	if err != nil {
		t.Fatal(err)
	}
	input.Notes[0].RevisionID = identity.NewID()
	second, err := input.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("digest did not change after transcript revision changed")
	}
}

func TestUT032CatalogInputRejectsAnotherEnvironment(t *testing.T) {
	environment := identity.NewID()
	input := CatalogInput{ProfileDigest: "furnished-v1", MediaGenerationID: identity.NewID(), EnvironmentID: environment, Notes: []CatalogNote{{NoteID: identity.NewID(), RevisionID: identity.NewID(), EnvironmentID: identity.NewID(), TargetKind: "PHOTO", SourceMediaID: identity.NewID(), SourceGeneration: 1, Text: "cinco pratos"}}}
	if err := ValidateCatalogInput(input); err == nil {
		t.Fatal("expected a cross-room note citation to be rejected")
	}
}
