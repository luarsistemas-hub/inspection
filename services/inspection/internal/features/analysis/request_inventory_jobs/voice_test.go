package request_inventory_jobs

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	"inspection/services/inspection/internal/features/analysis/inventory/inventorytest"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"

	"gorm.io/gorm"
)

// voiceRoom seeds a submitted photo inspection with one captured room.
func voiceRoom(t *testing.T) (*inventorytest.Seed, string, identity.ID) {
	t.Helper()
	key := "env:new:" + identity.NewID().String()
	seed := inventorytest.New(t, inventorytest.InspectionOptions{Requirements: []capturecore.Requirement{inventorytest.EnvironmentRequirement(key, "LIVING_ROOM", "Sala", true)}})
	photo := seed.Photo(t, key)
	seed.Answer(t, key, []identity.ID{photo}, "")
	return seed, key, photo
}

func deliverAt(t *testing.T, seed *inventorytest.Seed, at time.Time, eventType string, payload any) error {
	t.Helper()
	handler, err := Setup(Dependencies{Now: func() time.Time { return at }})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(payload)
	return seed.DB.Transaction(func(tx *gorm.DB) error {
		return handler(context.Background(), tx, events.RawEnvelope{ID: identity.NewID(), Type: eventType, TenantID: seed.Tenant, Payload: raw})
	})
}

func manifestOf(t *testing.T, seed *inventorytest.Seed) []database.CatalogNoteManifest {
	t.Helper()
	var rows []database.CatalogNoteManifest
	if err := seed.DB.Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestUT015RequestWaitsForNoteUntilVisualReadyPlusTwoMinutesThenCatalogsR1(t *testing.T) {
	seed, key, photo := voiceRoom(t)
	t0 := seed.Now
	note, _ := seed.Note(t, inventorytest.NoteSpec{Key: key, Status: "PROCESSING", PhotoMedia: &photo, Source: &photo})

	if err := deliverAt(t, seed, t0, "capture.submitted.v1", map[string]any{"inspectionId": seed.Inspection}); err != nil {
		t.Fatal(err)
	}
	jobs := jobsOf(t, seed)
	if len(jobs) != 1 || !jobs[0].NextAttemptAt.Equal(t0.Add(2*time.Minute)) {
		t.Fatalf("jobs = %+v, want one CATALOG job held until T0+2m", jobs)
	}
	if rows := manifestOf(t, seed); len(rows) != 1 || rows[0].SealedAt != nil || rows[0].MissingCount != 1 {
		t.Fatalf("manifests = %+v, want one open manifest with one missing note", rows)
	}

	// N1 completes with R1 at T0+30s: the completion event releases the job.
	revision := seed.Revise(t, note, "cinco pratos brancos")
	seed.SetNote(t, note, "status", "COMPLETED")
	at := t0.Add(30 * time.Second)
	if err := deliverAt(t, seed, at, "transcription.completed.v1", events.TranscriptionCompletedPayload{NoteID: note, RevisionID: &revision, Generation: 1}); err != nil {
		t.Fatal(err)
	}
	jobs = jobsOf(t, seed)
	if len(jobs) != 1 || !jobs[0].NextAttemptAt.Equal(at) {
		t.Fatalf("job due at %v, want it released at %v", jobs[0].NextAttemptAt, at)
	}
	rows := manifestOf(t, seed)
	if len(rows) != 1 || rows[0].SealedAt == nil || rows[0].MissingCount != 0 || !rows[0].DeadlineAt.Equal(t0.Add(2*time.Minute)) {
		t.Fatalf("manifests = %+v, want sealed, nothing missing and the original deadline", rows)
	}
	var entries []struct {
		NoteID     identity.ID  `json:"noteId"`
		RevisionID *identity.ID `json:"revisionId"`
		State      string       `json:"state"`
	}
	if err := json.Unmarshal(rows[0].NoteRevisions, &entries); err != nil || len(entries) != 1 || entries[0].NoteID != note || entries[0].RevisionID == nil || *entries[0].RevisionID != revision || entries[0].State != "INCLUDED" {
		t.Fatalf("manifest entries = %s (%v), want N1 fixed at R1", rows[0].NoteRevisions, err)
	}
}

func TestStalledNoteNeverHoldsTheCatalogPastTheDeadline(t *testing.T) {
	seed, key, photo := voiceRoom(t)
	t0 := seed.Now
	seed.Note(t, inventorytest.NoteSpec{Key: key, Status: "PROCESSING", PhotoMedia: &photo, Source: &photo})
	if err := deliverAt(t, seed, t0, "capture.submitted.v1", map[string]any{"inspectionId": seed.Inspection}); err != nil {
		t.Fatal(err)
	}
	// An unrelated redelivery before the deadline keeps the hold.
	if err := deliverAt(t, seed, t0.Add(time.Minute), "capture.submitted.v1", map[string]any{"inspectionId": seed.Inspection}); err != nil {
		t.Fatal(err)
	}
	if jobs := jobsOf(t, seed); len(jobs) != 1 || !jobs[0].NextAttemptAt.Equal(t0.Add(2*time.Minute)) {
		t.Fatalf("jobs = %+v, want the job to keep waiting for the stalled note", jobs)
	}
	// The wake-up at the deadline seals the manifest and releases the job.
	if err := deliverAt(t, seed, t0.Add(2*time.Minute), "capture.submitted.v1", map[string]any{"inspectionId": seed.Inspection}); err != nil {
		t.Fatal(err)
	}
	rows := manifestOf(t, seed)
	if len(rows) != 1 || rows[0].SealedAt == nil || rows[0].MissingCount != 1 {
		t.Fatalf("manifests = %+v, want sealed with the stalled note reported as missing", rows)
	}
}

func TestRoomsWithoutNotesDoNotWaitAndLeaveNoManifest(t *testing.T) {
	seed, _, _ := voiceRoom(t)
	deliver(t, seed, "capture.submitted.v1")
	jobs := jobsOf(t, seed)
	if len(jobs) != 1 || !jobs[0].NextAttemptAt.Equal(seed.Now) || len(manifestOf(t, seed)) != 0 {
		t.Fatalf("jobs=%+v manifests=%d, want an immediate visual job and no manifest", jobs, len(manifestOf(t, seed)))
	}
}

func TestTranscriptionFailureReleasesTheWaitingJob(t *testing.T) {
	seed, key, photo := voiceRoom(t)
	note, _ := seed.Note(t, inventorytest.NoteSpec{Key: key, Status: "RETRY_WAIT", PhotoMedia: &photo, Source: &photo})
	deliver(t, seed, "capture.submitted.v1")
	seed.SetNote(t, note, "status", "FAILED")
	at := seed.Now.Add(10 * time.Second)
	if err := deliverAt(t, seed, at, "transcription.failed.v1", events.TranscriptionFailedPayload{NoteID: note, Generation: 1, SafeCode: "PROVIDER_UNAVAILABLE"}); err != nil {
		t.Fatal(err)
	}
	jobs := jobsOf(t, seed)
	rows := manifestOf(t, seed)
	if len(jobs) != 1 || !jobs[0].NextAttemptAt.Equal(at) || len(rows) != 1 || rows[0].SealedAt == nil || rows[0].MissingCount != 0 {
		t.Fatalf("jobs=%+v manifests=%+v, want visual cataloging released with the failed note excluded", jobs, rows)
	}
}

func TestRetriedSubmissionIsIdempotentWithVoiceNotes(t *testing.T) {
	seed, key, photo := voiceRoom(t)
	seed.Note(t, inventorytest.NoteSpec{Key: key, Text: "cinco pratos", PhotoMedia: &photo, Source: &photo})
	deliver(t, seed, "capture.submitted.v1")
	deliver(t, seed, "capture.submitted.v1")
	if len(jobsOf(t, seed)) != 1 || len(manifestOf(t, seed)) != 1 {
		t.Fatalf("jobs=%d manifests=%d, want 1/1 after a redelivery", len(jobsOf(t, seed)), len(manifestOf(t, seed)))
	}
}

func TestTranscriptionEventsAreValidatedAndTenantScoped(t *testing.T) {
	seed, key, photo := voiceRoom(t)
	note, _ := seed.Note(t, inventorytest.NoteSpec{Key: key, Text: "x", PhotoMedia: &photo, Source: &photo})
	for name, payload := range map[string]string{
		"missing note":      `{"generation":1}`,
		"wrong generation":  `{"noteId":"` + note.String() + `","generation":0}`,
		"malformed payload": `{`,
	} {
		for _, eventType := range []string{"transcription.completed.v1", "transcription.failed.v1"} {
			err := deliverRaw(t, seed, seed.Tenant, eventType, payload)
			if !errors.Is(err, messaging.ErrPermanent) {
				t.Fatalf("%s %s: error=%v, want permanent rejection", eventType, name, err)
			}
		}
	}
	// Another tenant's envelope cannot reach this tenant's note.
	if err := deliverRaw(t, seed, identity.NewID(), "transcription.failed.v1", `{"noteId":"`+note.String()+`","generation":1,"safeCode":"PROVIDER_UNAVAILABLE"}`); err != nil {
		t.Fatalf("foreign tenant envelope error=%v, want a harmless no-op", err)
	}
	if len(jobsOf(t, seed)) != 0 || len(manifestOf(t, seed)) != 0 {
		t.Fatal("a foreign or invalid transcription event changed state")
	}
}

func deliverRaw(t *testing.T, seed *inventorytest.Seed, tenant identity.ID, eventType, payload string) error {
	t.Helper()
	handler, err := Setup(Dependencies{Now: func() time.Time { return seed.Now }})
	if err != nil {
		t.Fatal(err)
	}
	return seed.DB.Transaction(func(tx *gorm.DB) error {
		return handler(context.Background(), tx, events.RawEnvelope{ID: identity.NewID(), Type: eventType, TenantID: tenant, Payload: json.RawMessage(payload)})
	})
}

func TestVideoNoteWaitsForItsAnchorUntilTheClipIsPrepared(t *testing.T) {
	key := "env:new:" + identity.NewID().String()
	seed := inventorytest.New(t, inventorytest.InspectionOptions{MediaKind: "VIDEO", Requirements: []capturecore.Requirement{inventorytest.EnvironmentRequirement(key, "LIVING_ROOM", "Sala", true)}})
	clip, prep, _ := seed.Clip(t, key, "READY", 2)
	attempt := identity.NewID()
	if err := seed.DB.Model(&database.MediaObject{}).Where("id=?", clip).Update("video_attempt_id", attempt).Error; err != nil {
		t.Fatal(err)
	}
	seed.Answer(t, key, []identity.ID{clip}, "")
	start, end := int64(500), int64(2000)
	note, _ := seed.Note(t, inventorytest.NoteSpec{Key: key, TargetKind: "VIDEO_INTERVAL", Anchor: "PENDING", VideoAttempt: &attempt, StartMS: &start, EndMS: &end, Text: "cinco pratos"})
	// The clip has no measured source yet, so its timeline cannot validate the interval.
	deliver(t, seed, "capture.video_submitted.v2")
	jobs := jobsOf(t, seed)
	if len(jobs) != 1 || !jobs[0].NextAttemptAt.After(seed.Now) {
		t.Fatalf("jobs = %+v, want the job to wait for the anchor", jobs)
	}
	if err := seed.DB.Create(&database.VideoSource{ID: identity.NewID(), TenantID: seed.Tenant, MediaID: clip, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 8_000_000, Width: 10, Height: 10, FrameRate: 30, Status: "RETAINED", CreatedAt: seed.Now, UpdatedAt: seed.Now}).Error; err != nil {
		t.Fatal(err)
	}
	at := seed.Now.Add(20 * time.Second)
	if err := deliverAt(t, seed, at, "media.video_prepared.v1", events.VideoPreparedPayload{MediaID: clip, PreparationID: prep, ManifestDigest: "digest"}); err != nil {
		t.Fatal(err)
	}
	jobs = jobsOf(t, seed)
	rows := manifestOf(t, seed)
	if len(jobs) != 1 || !jobs[0].NextAttemptAt.Equal(at) || len(rows) != 1 || rows[0].SealedAt == nil {
		t.Fatalf("jobs=%+v manifests=%+v, want the prepared-clip event to resolve the anchor and release the job", jobs, rows)
	}
	var status, source string
	if err := seed.DB.Raw("SELECT anchor_status, source_media_id FROM transcription.audio_notes WHERE id=?", note).Row().Scan(&status, &source); err != nil || status != "VALID" || source != clip.String() {
		t.Fatalf("anchor=%s source=%s err=%v, want VALID on the attempt's clip", status, source, err)
	}
}
