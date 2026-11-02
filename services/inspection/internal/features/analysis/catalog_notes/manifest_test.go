package catalog_notes_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"inspection/libs/identity"
	catalognotes "inspection/services/inspection/internal/features/analysis/catalog_notes"
	"inspection/services/inspection/internal/features/analysis/inventory/inventorytest"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	"inspection/services/inspection/internal/platform/database"
)

type fixture struct {
	seed  *inventorytest.Seed
	key   string
	photo identity.ID
	room  catalognotes.Room
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	key := "env:new:" + identity.NewID().String()
	seed := inventorytest.New(t, inventorytest.InspectionOptions{Requirements: []capturecore.Requirement{inventorytest.EnvironmentRequirement(key, "LIVING_ROOM", "Sala", true)}})
	photo := seed.Photo(t, key)
	seed.Answer(t, key, []identity.ID{photo}, "")
	return fixture{seed: seed, key: key, photo: photo, room: catalognotes.Room{TenantID: seed.Tenant, InspectionID: seed.Inspection, ResponsibilityID: seed.Responsibility, RequirementKey: key, MediaIDs: []identity.ID{photo}, Generation: 1}}
}

func (f fixture) evaluate(t *testing.T, at time.Time, final bool) catalognotes.Outcome {
	t.Helper()
	outcome, err := catalognotes.Evaluate(context.Background(), f.seed.DB, f.room, at, final)
	if err != nil {
		t.Fatal(err)
	}
	return outcome
}

func (f fixture) note(t *testing.T, spec inventorytest.NoteSpec) (identity.ID, identity.ID) {
	t.Helper()
	spec.Key = f.key
	if spec.PhotoMedia == nil && spec.TargetKind == "" {
		spec.PhotoMedia, spec.Source = &f.photo, &f.photo
	}
	return f.seed.Note(t, spec)
}

func manifests(t *testing.T, f fixture) []database.CatalogNoteManifest {
	t.Helper()
	var rows []database.CatalogNoteManifest
	if err := f.seed.DB.Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	return rows
}

func entryOf(t *testing.T, manifest *catalognotes.Manifest, note identity.ID) catalognotes.Entry {
	t.Helper()
	for _, entry := range manifest.Entries {
		if entry.NoteID == note {
			return entry
		}
	}
	t.Fatalf("note %s is not in the manifest %+v", note, manifest.Entries)
	return catalognotes.Entry{}
}

func TestRoomWithoutNotesIsReadyAtOnceAndLeavesNoManifest(t *testing.T) {
	f := newFixture(t)
	outcome := f.evaluate(t, f.seed.Now, false)
	if !outcome.Ready || outcome.Manifest != nil || len(manifests(t, f)) != 0 {
		t.Fatalf("outcome=%+v manifests=%d, want ready with no manifest", outcome, len(manifests(t, f)))
	}
}

func TestUT015ManifestWaitsTwoMinutesForNotesStillProcessing(t *testing.T) {
	f := newFixture(t)
	t0 := f.seed.Now
	note, _ := f.note(t, inventorytest.NoteSpec{Status: "PROCESSING"})

	waiting := f.evaluate(t, t0, false)
	if waiting.Ready || waiting.Manifest == nil || waiting.Manifest.Sealed() || waiting.Manifest.MissingCount != 1 || !waiting.Manifest.DeadlineAt.Equal(t0.Add(2*time.Minute)) {
		t.Fatalf("first evaluation = %+v, want an open manifest due at T0+2m with one missing note", waiting)
	}

	// The transcript completes within the wait: the manifest seals on R1.
	revision := f.seed.Revise(t, note, "cinco pratos brancos")
	f.seed.SetNote(t, note, "status", "COMPLETED")
	sealed := f.evaluate(t, t0.Add(30*time.Second), false)
	if !sealed.Ready || !sealed.Manifest.Sealed() || sealed.Manifest.MissingCount != 0 {
		t.Fatalf("after completion = %+v, want a sealed manifest with nothing missing", sealed)
	}
	entry := entryOf(t, sealed.Manifest, note)
	if entry.State != catalognotes.StateIncluded || entry.RevisionID == nil || *entry.RevisionID != revision {
		t.Fatalf("entry = %+v, want N1 included with R1", entry)
	}
	if rows := manifests(t, f); len(rows) != 1 || !rows[0].DeadlineAt.Equal(t0.Add(2*time.Minute)) {
		t.Fatalf("manifests = %+v, want one row that keeps the original deadline", rows)
	}
}

func TestManifestSealsAtDeadlineWithMissingNoteAndNeverWaitsLonger(t *testing.T) {
	f := newFixture(t)
	t0 := f.seed.Now
	stalled, _ := f.note(t, inventorytest.NoteSpec{Status: "RETRY_WAIT"})
	ready, readyRevision := f.note(t, inventorytest.NoteSpec{Text: "quinze copos"})

	if outcome := f.evaluate(t, t0, false); outcome.Ready {
		t.Fatalf("manifest sealed before the deadline with a note still processing: %+v", outcome)
	}
	just := f.evaluate(t, t0.Add(2*time.Minute-time.Second), false)
	if just.Ready {
		t.Fatal("manifest sealed one second before the two-minute wait ended")
	}
	late := f.evaluate(t, t0.Add(2*time.Minute), false)
	if !late.Ready || !late.Manifest.Sealed() || late.Manifest.MissingCount != 1 {
		t.Fatalf("at the deadline = %+v, want sealed with one missing note", late)
	}
	if entry := entryOf(t, late.Manifest, stalled); entry.State != catalognotes.StateMissing || entry.Reason != catalognotes.ReasonTranscriptionPending {
		t.Fatalf("stalled entry = %+v", entry)
	}
	if entry := entryOf(t, late.Manifest, ready); entry.State != catalognotes.StateIncluded || *entry.RevisionID != readyRevision {
		t.Fatalf("ready entry = %+v", entry)
	}
}

func TestSealedManifestIgnoresLateNotesAndCorrectedText(t *testing.T) {
	f := newFixture(t)
	note, first := f.note(t, inventorytest.NoteSpec{Text: "cinco pratos"})
	sealed := f.evaluate(t, f.seed.Now, false)
	if !sealed.Ready || !sealed.Manifest.Sealed() {
		t.Fatalf("manifest = %+v", sealed)
	}
	digest := sealed.Manifest.Digest

	// A correction and a brand-new note arrive after the manifest was fixed.
	f.seed.Revise(t, note, "seis pratos")
	f.note(t, inventorytest.NoteSpec{Text: "mais dois no armário"})
	again := f.evaluate(t, f.seed.Now.Add(time.Hour), false)
	if !again.Ready || again.Manifest.Digest != digest || len(again.Manifest.Entries) != 1 {
		t.Fatalf("sealed manifest changed: %+v", again.Manifest)
	}
	if entry := again.Manifest.Entries[0]; entry.RevisionID == nil || *entry.RevisionID != first {
		t.Fatalf("sealed entry follows the corrected text: %+v", entry)
	}
	seal, err := catalognotes.Seal(context.Background(), f.seed.DB, f.room, f.seed.Now.Add(time.Hour))
	if err != nil || seal == nil || seal.Digest != digest {
		t.Fatalf("Seal() = %+v, %v", seal, err)
	}
}

func TestManifestExplainsEveryExcludedNote(t *testing.T) {
	f := newFixture(t)
	otherPhoto := f.seed.Photo(t, "other-room")
	cases := map[string]struct {
		spec   inventorytest.NoteSpec
		state  string
		reason string
	}{
		"discarded":        {inventorytest.NoteSpec{Status: "CANCELED", Text: "x"}, catalognotes.StateExcluded, catalognotes.ReasonNoteDiscarded},
		"expired":          {inventorytest.NoteSpec{Status: "EXPIRED"}, catalognotes.StateExcluded, catalognotes.ReasonNoteExpired},
		"failed":           {inventorytest.NoteSpec{Status: "FAILED"}, catalognotes.StateExcluded, catalognotes.ReasonTranscriptionFailed},
		"no speech":        {inventorytest.NoteSpec{NoSpeech: true}, catalognotes.StateExcluded, catalognotes.ReasonNoSpeech},
		"no revision":      {inventorytest.NoteSpec{}, catalognotes.StateExcluded, catalognotes.ReasonRevisionUnavailable},
		"invalid anchor":   {inventorytest.NoteSpec{Anchor: "INVALID", Text: "x"}, catalognotes.StateExcluded, catalognotes.ReasonAnchorInvalid},
		"unavailable":      {inventorytest.NoteSpec{Anchor: "UNAVAILABLE", Text: "x"}, catalognotes.StateExcluded, catalognotes.ReasonAnchorInvalid},
		"another room":     {inventorytest.NoteSpec{PhotoMedia: &otherPhoto, Source: &otherPhoto, Text: "x"}, catalognotes.StateExcluded, catalognotes.ReasonAnchorOutsideRoom},
		"anchor not ready": {inventorytest.NoteSpec{Anchor: "PENDING", Text: "x"}, catalognotes.StateMissing, catalognotes.ReasonAnchorPending},
	}
	ids := map[string]identity.ID{}
	for name, tc := range cases {
		spec := tc.spec
		if spec.Anchor == "PENDING" {
			// A pending photo anchor whose media is not ready stays pending.
			pending := f.seed.Photo(t, f.key)
			spec.PhotoMedia, spec.Source = &pending, &pending
			if err := f.seed.DB.Model(&database.MediaObject{}).Where("id=?", pending).Update("status", "UPLOADED").Error; err != nil {
				t.Fatal(err)
			}
			f.room.MediaIDs = append(f.room.MediaIDs, pending)
		}
		ids[name], _ = f.note(t, spec)
	}
	// Notes that are still uploading are not part of any manifest.
	uploading, _ := f.note(t, inventorytest.NoteSpec{Status: "UPLOADING", Text: "x"})
	// A note from another inspection never appears.
	foreign, _ := f.note(t, inventorytest.NoteSpec{Inspection: identity.NewID(), Text: "x"})

	outcome := f.evaluate(t, f.seed.Now, false)
	if outcome.Manifest == nil {
		t.Fatal("no manifest")
	}
	for name, tc := range cases {
		entry := entryOf(t, outcome.Manifest, ids[name])
		if entry.State != tc.state || entry.Reason != tc.reason {
			t.Errorf("%s: entry=%+v, want %s/%s", name, entry, tc.state, tc.reason)
		}
	}
	for _, entry := range outcome.Manifest.Entries {
		if entry.NoteID == uploading || entry.NoteID == foreign {
			t.Fatalf("manifest lists a note that must not be part of it: %+v", entry)
		}
	}
	if outcome.Manifest.MissingCount != 1 || outcome.Ready {
		t.Fatalf("missing=%d ready=%v, want the pending anchor to hold the wait", outcome.Manifest.MissingCount, outcome.Ready)
	}
}

func TestManifestOnlyListsNotesOfTheSubmittedCapture(t *testing.T) {
	f := newFixture(t)
	otherCapture := identity.NewID()
	f.note(t, inventorytest.NoteSpec{Responsibility: otherCapture, Text: "texto de outra captura"})
	own, _ := f.note(t, inventorytest.NoteSpec{Text: "cinco pratos"})
	outcome := f.evaluate(t, f.seed.Now, false)
	if len(outcome.Manifest.Entries) != 1 || outcome.Manifest.Entries[0].NoteID != own {
		t.Fatalf("entries = %+v, want only the submitted capture's note", outcome.Manifest.Entries)
	}
}

func TestManifestBoundsNotesPerRoom(t *testing.T) {
	f := newFixture(t)
	for i := 0; i < catalognotes.MaxNotes+3; i++ {
		spec := inventorytest.NoteSpec{Text: fmt.Sprintf("nota %d", i), CreatedAt: f.seed.Now.Add(time.Duration(i) * time.Second)}
		f.note(t, spec)
	}
	outcome := f.evaluate(t, f.seed.Now, false)
	included, limited := 0, 0
	for _, entry := range outcome.Manifest.Entries {
		switch {
		case entry.State == catalognotes.StateIncluded:
			included++
		case entry.Reason == catalognotes.ReasonNoteLimitExceeded:
			limited++
		}
	}
	if included != catalognotes.MaxNotes || limited != 3 {
		t.Fatalf("included=%d limited=%d, want %d/3", included, limited, catalognotes.MaxNotes)
	}
}

func TestAnchorResolutionFollowsExactAttemptAndPreparedTimeline(t *testing.T) {
	key := "env:new:" + identity.NewID().String()
	seed := inventorytest.New(t, inventorytest.InspectionOptions{MediaKind: "VIDEO", Requirements: []capturecore.Requirement{inventorytest.EnvironmentRequirement(key, "LIVING_ROOM", "Sala", true)}})
	clip, prep, _ := seed.Clip(t, key, "PROCESSING", 0)
	attempt := identity.NewID()
	if err := seed.DB.Model(&database.MediaObject{}).Where("id=?", clip).Update("video_attempt_id", attempt).Error; err != nil {
		t.Fatal(err)
	}
	seed.Answer(t, key, []identity.ID{clip}, "")
	room := catalognotes.Room{TenantID: seed.Tenant, InspectionID: seed.Inspection, ResponsibilityID: seed.Responsibility, RequirementKey: key, MediaIDs: []identity.ID{clip}, Generation: 1}
	start, end := int64(1000), int64(2500)
	valid, _ := seed.Note(t, inventorytest.NoteSpec{Key: key, TargetKind: "VIDEO_INTERVAL", Anchor: "PENDING", VideoAttempt: &attempt, StartMS: &start, EndMS: &end, Text: "cinco pratos"})
	farEnd := int64(60_000)
	beyond, _ := seed.Note(t, inventorytest.NoteSpec{Key: key, TargetKind: "VIDEO_INTERVAL", Anchor: "PENDING", VideoAttempt: &attempt, StartMS: &start, EndMS: &farEnd, Text: "fora do vídeo"})
	otherAttempt := identity.NewID()
	orphan, _ := seed.Note(t, inventorytest.NoteSpec{Key: key, TargetKind: "VIDEO_INTERVAL", Anchor: "PENDING", VideoAttempt: &otherAttempt, StartMS: &start, EndMS: &end, Text: "outra tentativa"})
	ctx := context.Background()

	// While the clip is still being prepared nothing resolves.
	if err := catalognotes.ResolveAnchors(ctx, seed.DB, room, seed.Now); err != nil {
		t.Fatal(err)
	}
	if status := anchorStatus(t, seed, valid); status != "PENDING" {
		t.Fatalf("anchor = %s while preparation is running, want PENDING", status)
	}

	if err := seed.DB.Model(&database.VideoPreparationJob{}).Where("id=?", prep).Update("status", "READY").Error; err != nil {
		t.Fatal(err)
	}
	if err := seed.DB.Create(&database.VideoSource{ID: identity.NewID(), TenantID: seed.Tenant, MediaID: clip, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 10_000_000, Width: 10, Height: 10, FrameRate: 30, Status: "RETAINED", CreatedAt: seed.Now, UpdatedAt: seed.Now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := catalognotes.ResolveAnchors(ctx, seed.DB, room, seed.Now); err != nil {
		t.Fatal(err)
	}
	if status := anchorStatus(t, seed, valid); status != "VALID" {
		t.Fatalf("anchor = %s after the clip is ready, want VALID", status)
	}
	var resolved struct {
		SourceMediaID    identity.ID
		SourceGeneration int
	}
	if err := seed.DB.Raw("SELECT source_media_id,source_generation FROM transcription.audio_notes WHERE id=?", valid).Scan(&resolved).Error; err != nil || resolved.SourceMediaID != clip || resolved.SourceGeneration != 1 {
		t.Fatalf("resolved lineage = %+v, %v; want the attempt's media at generation 1", resolved, err)
	}
	if status := anchorStatus(t, seed, beyond); status != "INVALID" {
		t.Fatalf("interval beyond the prepared timeline = %s, want INVALID", status)
	}
	if status := anchorStatus(t, seed, orphan); status != "PENDING" {
		t.Fatalf("note of another attempt = %s, want it to stay PENDING (never moved to other media)", status)
	}
}

func anchorStatus(t *testing.T, seed *inventorytest.Seed, note identity.ID) string {
	t.Helper()
	var status string
	if err := seed.DB.Raw("SELECT anchor_status FROM transcription.audio_notes WHERE id=?", note).Row().Scan(&status); err != nil {
		t.Fatal(err)
	}
	return status
}

func TestLateNoteNeverCreatesAManifestForACatalogThatAlreadyStarted(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   string
		attempts int
		manifest bool
	}{
		{"running job", "RUNNING", 1, false},
		{"succeeded job", "SUCCEEDED", 1, false},
		{"retried job", "PENDING", 1, false},
		{"job still waiting for its first run", "PENDING", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			job := f.seed.Job(t, "CATALOG", f.key, identity.NewID(), nil)
			if err := f.seed.DB.Model(&database.InventoryJob{}).Where("id=?", job.ID).Updates(map[string]any{"status": tc.status, "attempts": tc.attempts}).Error; err != nil {
				t.Fatal(err)
			}
			// The note was still uploading when the room was submitted and now has text.
			f.note(t, inventorytest.NoteSpec{Text: "cinco pratos"})
			outcome := f.evaluate(t, f.seed.Now, false)
			rows := manifests(t, f)
			if !outcome.Ready || (outcome.Manifest != nil) != tc.manifest || (len(rows) == 1) != tc.manifest {
				t.Fatalf("outcome=%+v manifests=%d, want manifest=%v and ready", outcome, len(rows), tc.manifest)
			}
			seal, err := catalognotes.Seal(context.Background(), f.seed.DB, f.room, f.seed.Now)
			if err != nil || (seal != nil) != tc.manifest {
				t.Fatalf("Seal() = %+v, %v, want manifest=%v", seal, err, tc.manifest)
			}
		})
	}
}
