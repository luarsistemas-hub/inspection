package catalog_notes_test

import (
	"context"
	"errors"
	"testing"

	"inspection/libs/identity"
	catalognotes "inspection/services/inspection/internal/features/analysis/catalog_notes"
	"inspection/services/inspection/internal/features/analysis/inventory/inventorytest"
)

func (f fixture) sealSelected(t *testing.T, generation int, selected ...identity.ID) (*catalognotes.Manifest, error) {
	t.Helper()
	room := f.room
	room.Generation = generation
	return catalognotes.SealSelected(context.Background(), f.seed.DB, room, selected, f.seed.Now)
}

func TestSealSelectedKeepsOnlyTheSelectedCurrentRevisions(t *testing.T) {
	f := newFixture(t)
	chosen, chosenRevision := f.note(t, inventorytest.NoteSpec{Text: "cinco pratos"})
	left, _ := f.note(t, inventorytest.NoteSpec{Text: "quatro copos"})
	manifest, err := f.sealSelected(t, 2, chosenRevision)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.CatalogGeneration != 2 || !manifest.Sealed() || len(manifest.Included()) != 1 || *manifest.Included()[0].RevisionID != chosenRevision {
		t.Fatalf("manifest = %+v", manifest)
	}
	if entry := entryOf(t, manifest, left); entry.State != catalognotes.StateExcluded || entry.Reason != catalognotes.ReasonNotSelected {
		t.Fatalf("unselected note = %+v, want excluded as not selected", entry)
	}
	if entry := entryOf(t, manifest, chosen); entry.State != catalognotes.StateIncluded {
		t.Fatalf("selected note = %+v", entry)
	}
	stored, err := catalognotes.Load(context.Background(), f.seed.DB, catalognotes.Room{TenantID: f.room.TenantID, InspectionID: f.room.InspectionID, RequirementKey: f.key, Generation: 2})
	if err != nil || stored == nil || !stored.Sealed() || stored.Digest != manifest.Digest {
		t.Fatalf("stored = %+v err=%v", stored, err)
	}
	if _, err := f.sealSelected(t, 2, chosenRevision); err == nil {
		t.Fatal("a generation's manifest is created once")
	}
	if first, _ := catalognotes.Load(context.Background(), f.seed.DB, f.room); first != nil {
		t.Fatal("an earlier generation must not be created or changed")
	}
}

func TestSealSelectedRejectsWhatCannotFeedTheCatalog(t *testing.T) {
	f := newFixture(t)
	valid, validRevision := f.note(t, inventorytest.NoteSpec{Text: "cinco pratos"})
	corrected, first := f.note(t, inventorytest.NoteSpec{Text: "cinco pratos"})
	f.seed.Revise(t, corrected, "seis pratos")
	_, discarded := f.note(t, inventorytest.NoteSpec{Status: "CANCELED", Text: "descartada"})
	_, silent := f.note(t, inventorytest.NoteSpec{NoSpeech: true, Text: "x"})
	attempt, start, end := identity.NewID(), int64(1000), int64(4000)
	_, waiting := f.seed.Note(t, inventorytest.NoteSpec{Key: f.key, TargetKind: "VIDEO_INTERVAL", Anchor: "PENDING", VideoAttempt: &attempt, StartMS: &start, EndMS: &end, Text: "ainda sem vídeo"})
	otherKey := "env:new:" + identity.NewID().String()
	_, foreign := f.seed.Note(t, inventorytest.NoteSpec{Key: otherKey, Text: "outra sala"})
	_, processing := f.note(t, inventorytest.NoteSpec{Status: "PROCESSING", Text: "x"})

	tests := []struct {
		name     string
		selected []identity.ID
		want     error
	}{
		{"IT-241 IT-227 a note of another room", []identity.ID{validRevision, foreign}, catalognotes.ErrSelectionOutOfScope},
		{"an unknown revision", []identity.ID{identity.NewID()}, catalognotes.ErrSelectionOutOfScope},
		{"IT-225 a revision that is no longer current", []identity.ID{first}, catalognotes.ErrSelectionStale},
		{"IT-179 a discarded note", []identity.ID{discarded}, catalognotes.ErrSelectionIneligible},
		{"a note without speech", []identity.ID{silent}, catalognotes.ErrSelectionIneligible},
		{"IT-234 IT-248 an anchor that is not valid yet", []identity.ID{waiting}, catalognotes.ErrSelectionNotReady},
		{"a transcription still processing", []identity.ID{processing}, catalognotes.ErrSelectionNotReady},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := f.sealSelected(t, 2, tt.selected...); !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
	if stored, err := catalognotes.Load(context.Background(), f.seed.DB, catalognotes.Room{TenantID: f.room.TenantID, InspectionID: f.room.InspectionID, RequirementKey: f.key, Generation: 2}); err != nil || stored != nil {
		t.Fatalf("a rejected selection must leave no manifest: %+v err=%v", stored, err)
	}
	_ = valid
}

func TestSealSelectedNeverReassignsANoteToReplacedMedia(t *testing.T) {
	f := newFixture(t)
	_, revision := f.note(t, inventorytest.NoteSpec{Text: "cinco pratos"})
	// The room's media was replaced: the note stays anchored to the old photo.
	replacement := f.seed.Photo(t, f.key)
	room := f.room
	room.Generation, room.MediaIDs = 2, []identity.ID{replacement}
	_, err := catalognotes.SealSelected(context.Background(), f.seed.DB, room, []identity.ID{revision}, f.seed.Now)
	if !errors.Is(err, catalognotes.ErrSelectionIneligible) {
		t.Fatalf("IT-238 IT-245 err = %v, want the note excluded because its media is no longer in the room", err)
	}
	// The same note keeps feeding the generation whose media still contains its anchor.
	room.MediaIDs = []identity.ID{f.photo, replacement}
	manifest, err := catalognotes.SealSelected(context.Background(), f.seed.DB, room, []identity.ID{revision}, f.seed.Now)
	if err != nil || len(manifest.Included()) != 1 {
		t.Fatalf("manifest = %+v err=%v", manifest, err)
	}
}
