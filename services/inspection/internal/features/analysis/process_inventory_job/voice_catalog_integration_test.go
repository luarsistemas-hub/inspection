//go:build integration

package process_inventory_job

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	catalognotes "inspection/services/inspection/internal/features/analysis/catalog_notes"
	"inspection/services/inspection/internal/features/analysis/inventory/inventorytest"
	"inspection/services/inspection/internal/features/analysis/request_inventory_jobs"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"
	"inspection/services/inspection/internal/platform/messaging"

	"gorm.io/gorm"
)

// These flows exercise the voice-note catalog against the disposable
// PostgreSQL (RLS, runtime role): the real request handler, the manifest, the
// executor and the processor, with a scripted model gateway.

type itNote struct {
	Key            string
	Photo          identity.ID
	Status, Anchor string
	Text           string
	NoSpeech       bool
	Responsibility identity.ID
}

// voiceRoom registers one captured room with a READY photo.
func (f *flow) voiceRoom(name string) (string, identity.ID) {
	f.t.Helper()
	key := "env:new:" + identity.NewID().String()
	f.requirements(inventorytest.EnvironmentRequirement(key, "LIVING_ROOM", name, true))
	photo := f.photo(key)
	f.answer(key, []identity.ID{photo}, "")
	return key, photo
}

// addRoom adds another captured room to the submitted capture.
func (f *flow) voiceRooms(names ...string) ([]string, []identity.ID) {
	f.t.Helper()
	requirements := make([]capturecore.Requirement, 0, len(names))
	keys := make([]string, 0, len(names))
	photos := make([]identity.ID, 0, len(names))
	for _, name := range names {
		key := "env:new:" + identity.NewID().String()
		keys = append(keys, key)
		requirements = append(requirements, inventorytest.EnvironmentRequirement(key, "LIVING_ROOM", name, true))
	}
	f.requirements(requirements...)
	for _, key := range keys {
		photo := f.photo(key)
		photos = append(photos, photo)
		f.answer(key, []identity.ID{photo}, "")
	}
	return keys, photos
}

// note stores an audio note with its first transcript revision.
func (f *flow) note(n itNote) (identity.ID, identity.ID) {
	f.t.Helper()
	if n.Status == "" {
		n.Status = "COMPLETED"
	}
	if n.Anchor == "" {
		n.Anchor = "VALID"
	}
	if n.Responsibility == (identity.ID{}) {
		n.Responsibility = f.seed.ResponsibilityID
	}
	note, revision, now := identity.NewID(), identity.ID{}, f.now()
	f.within(func(tx *gorm.DB) error {
		if err := tx.Exec(`INSERT INTO transcription.audio_notes (tenant_id,id,inspection_id,responsibility_id,client_note_id,requirement_key,consent_version,disclosure_version,target_kind,photo_media_id,source_media_id,duration_ms,content_sha256,content_type,size_bytes,status,anchor_status,no_speech,created_at,updated_at)
VALUES (?,?,?,?,?,?,'v1','v1','PHOTO',?,?,3000,?,'audio/webm',1000,?,?,?,?,?)`, f.seed.TenantID, note, f.seed.InspectionID, n.Responsibility, note, n.Key, n.Photo, n.Photo, strings.Repeat("a", 64), n.Status, n.Anchor, n.NoSpeech, now, now).Error; err != nil {
			return err
		}
		if n.Text == "" {
			return nil
		}
		revision = identity.NewID()
		if err := tx.Create(&database.TranscriptRevision{ID: revision, TenantID: f.seed.TenantID, NoteID: note, RevisionNumber: 1, Text: n.Text, Kind: "PARTICIPANT_DECLARATION", CreatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Exec("UPDATE transcription.audio_notes SET current_revision_id=? WHERE tenant_id=? AND id=?", revision, f.seed.TenantID, note).Error
	})
	return note, revision
}

func (f *flow) revise(note identity.ID, number int, text string) identity.ID {
	f.t.Helper()
	revision := identity.NewID()
	f.within(func(tx *gorm.DB) error {
		if err := tx.Create(&database.TranscriptRevision{ID: revision, TenantID: f.seed.TenantID, NoteID: note, RevisionNumber: number, Text: text, Kind: "HUMAN_EDIT", CreatedAt: f.now()}).Error; err != nil {
			return err
		}
		return tx.Exec("UPDATE transcription.audio_notes SET current_revision_id=? WHERE tenant_id=? AND id=?", revision, f.seed.TenantID, note).Error
	})
	return revision
}

func (f *flow) setNote(note identity.ID, column string, value any) {
	f.t.Helper()
	f.within(func(tx *gorm.DB) error {
		return tx.Exec(fmt.Sprintf("UPDATE transcription.audio_notes SET %s=? WHERE tenant_id=? AND id=?", column), value, f.seed.TenantID, note).Error
	})
}

func (f *flow) manifests() []catalognotes.Manifest {
	f.t.Helper()
	var rows []database.CatalogNoteManifest
	f.within(func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND inspection_id=?", f.seed.TenantID, f.seed.InspectionID).Order("requirement_key, catalog_generation").Find(&rows).Error
	})
	out := make([]catalognotes.Manifest, 0, len(rows))
	for _, row := range rows {
		manifest := catalognotes.Manifest{CatalogNoteManifest: row}
		if err := json.Unmarshal(row.NoteRevisions, &manifest.Entries); err != nil {
			f.t.Fatal(err)
		}
		out = append(out, manifest)
	}
	return out
}

func (f *flow) catalogItems() []database.InventoryItem {
	f.t.Helper()
	var items []database.InventoryItem
	f.within(func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND origin='AI'", f.seed.TenantID).Order("name").Find(&items).Error
	})
	return items
}

func (f *flow) catalogJob(key string) database.InventoryJob {
	f.t.Helper()
	for _, job := range f.jobs() {
		if job.RequirementKey == key && job.Kind == "CATALOG" {
			return job
		}
	}
	f.t.Fatalf("no CATALOG job for %s", key)
	return database.InventoryJob{}
}

func (f *flow) catalogRequests() []llm.StructuredRequest { return f.gateway.Calls("INVENTORY_CATALOG") }

// answerWith scripts the catalog model with one item per spec.
func (f *flow) answerWith(items ...voiceItem) {
	f.gateway.Respond = func(llm.StructuredRequest) (string, error) { return voiceCatalogJSONT(f.t, items...), nil }
}

// voiceCatalogJSONT mirrors voiceCatalogJSON for *testing.T helpers without a unit harness.
func voiceCatalogJSONT(t *testing.T, items ...voiceItem) string { return voiceCatalogJSON(t, items...) }

func mustVoiceData(t *testing.T, request llm.StructuredRequest) []any {
	t.Helper()
	notes, _ := userData(t, request)["voiceNotes"].([]any)
	return notes
}

func TestVoiceCatalogIT153ToIT166(t *testing.T) {
	t.Run("IT-153 spoken quantity is suggested with voice provenance", func(t *testing.T) {
		f := newFlow(t, "PHOTO")
		key, photo := f.voiceRoom("Cozinha")
		note, revision := f.note(itNote{Key: key, Photo: photo, Text: "cinco pratos brancos"})
		f.answerWith(voiceItem{name: "Pratos", quantity: intp(5), evidence: []string{photo.String()}, citations: []map[string]any{cite(note, revision, "name", "quantity")}})
		f.submitPhotos()
		f.drain()
		items := f.catalogItems()
		if len(items) != 1 || items[0].Quantity == nil || *items[0].Quantity != 5 || items[0].Decision != "PENDING" {
			t.Fatalf("items = %+v", items)
		}
		sources := ExtractVoiceSources(items[0])
		if len(sources) != 2 || sources[0].NoteID != note || sources[0].RevisionID != revision {
			t.Fatalf("voice sources = %+v, want N1/R1", sources)
		}
		job := f.catalogJob(key)
		manifests := f.manifests()
		if job.Status != "SUCCEEDED" || job.InputDigest == nil || len(manifests) != 1 || manifests[0].SealedAt == nil || manifests[0].MissingCount != 0 {
			t.Fatalf("job=%+v manifests=%+v", job, manifests)
		}
	})

	t.Run("IT-154 absence declaration is data and never proof", func(t *testing.T) {
		f := newFlow(t, "PHOTO")
		key, photo := f.voiceRoom("Cozinha")
		f.note(itNote{Key: key, Photo: photo, Text: "não tem pratos"})
		f.answerWith()
		f.submitPhotos()
		f.drain()
		requests := f.catalogRequests()
		if len(requests) != 1 || len(mustVoiceData(t, requests[0])) != 1 || !strings.Contains(requests[0].SystemPrompt, "não prova ausência") {
			t.Fatalf("requests = %+v", requests)
		}
		if items := f.catalogItems(); len(items) != 0 {
			t.Fatalf("items = %+v, want no positive plate item", items)
		}
	})

	t.Run("IT-155 and IT-156 rooms without notes proceed at once with the stored prompt", func(t *testing.T) {
		f := newFlow(t, "PHOTO")
		key, photo := f.voiceRoom("Sala")
		f.gateway.Respond = func(llm.StructuredRequest) (string, error) {
			return catalogJSON(t, catalogItemSpec{name: "Sofá", confidence: 9000, evidence: []string{photo.String()}}), nil
		}
		f.submitPhotos()
		if job := f.catalogJob(key); job.NextAttemptAt.After(f.now()) {
			t.Fatalf("job due at %v, want it to start without waiting for voice", job.NextAttemptAt)
		}
		f.drain()
		var snapshot database.AnalysisPromptSnapshot
		if err := f.h.AdminDB.Where("id=?", f.catalog).Take(&snapshot).Error; err != nil {
			t.Fatal(err)
		}
		requests := f.catalogRequests()
		if len(requests) != 1 || requests[0].PromptDigest != snapshot.CanonicalDigest || len(mustVoiceData(t, requests[0])) != 0 || len(f.manifests()) != 0 {
			t.Fatalf("requests=%d manifests=%d, want the unchanged visual request", len(requests), len(f.manifests()))
		}
	})

	t.Run("IT-157 a note of another room or media is excluded with its reason", func(t *testing.T) {
		f := newFlow(t, "PHOTO")
		keys, photos := f.voiceRooms("Sala", "Cozinha")
		own, ownRevision := f.note(itNote{Key: keys[0], Photo: photos[0], Text: "cinco pratos"})
		// A note filed under the kitchen but anchored to the living-room photo.
		crossed, _ := f.note(itNote{Key: keys[1], Photo: photos[0], Text: "mesa de jantar"})
		f.answerWith()
		f.submitPhotos()
		f.drain()
		byKey := map[string]catalognotes.Manifest{}
		for _, manifest := range f.manifests() {
			byKey[manifest.RequirementKey] = manifest
		}
		living, kitchen := byKey[keys[0]], byKey[keys[1]]
		if len(living.Entries) != 1 || living.Entries[0].NoteID != own || living.Entries[0].State != catalognotes.StateIncluded || *living.Entries[0].RevisionID != ownRevision {
			t.Fatalf("living room entries = %+v", living.Entries)
		}
		if len(kitchen.Entries) != 1 || kitchen.Entries[0].NoteID != crossed || kitchen.Entries[0].State != catalognotes.StateExcluded || kitchen.Entries[0].Reason != catalognotes.ReasonAnchorOutsideRoom {
			t.Fatalf("kitchen entries = %+v, want the cross-room note excluded with its reason", kitchen.Entries)
		}
		for _, request := range f.catalogRequests() {
			for _, entry := range mustVoiceData(t, request) {
				if entry.(map[string]any)["text"] == "mesa de jantar" {
					t.Fatal("the cross-room note text reached a request")
				}
			}
		}
	})

	t.Run("IT-158 a note without usable speech leaves a visual-only catalog", func(t *testing.T) {
		f := newFlow(t, "PHOTO")
		key, photo := f.voiceRoom("Sala")
		note, _ := f.note(itNote{Key: key, Photo: photo, NoSpeech: true})
		f.gateway.Respond = func(llm.StructuredRequest) (string, error) {
			return catalogJSON(t, catalogItemSpec{name: "Sofá", confidence: 9000, evidence: []string{photo.String()}}), nil
		}
		f.submitPhotos()
		f.drain()
		manifests := f.manifests()
		if len(manifests) != 1 || manifests[0].Entries[0].NoteID != note || manifests[0].Entries[0].Reason != catalognotes.ReasonNoSpeech || len(f.catalogItems()) != 1 || len(mustVoiceData(t, f.catalogRequests()[0])) != 0 {
			t.Fatalf("manifests=%+v items=%d", manifests, len(f.catalogItems()))
		}
	})

	t.Run("IT-159 a limited result stays marked as limited", func(t *testing.T) {
		f := newFlow(t, "PHOTO")
		key, photo := f.voiceRoom("Sala")
		note, revision := f.note(itNote{Key: key, Photo: photo, Text: "muitos itens"})
		items := make([]voiceItem, 0, 90)
		for i := 0; i < 90; i++ {
			items = append(items, voiceItem{name: fmt.Sprintf("Item %02d", i), evidence: []string{photo.String()}, confidence: 5000 + i*10, citations: []map[string]any{cite(note, revision, "name")}})
		}
		f.answerWith(items...)
		f.submitPhotos()
		f.drain()
		if got := len(f.catalogItems()); got != 80 {
			t.Fatalf("items = %d, want the 80 most confident", got)
		}
		if job := f.catalogJob(key); job.ErrorCode == nil || *job.ErrorCode != "TRUNCATED_ITEMS" {
			t.Fatalf("job = %+v, want the truncation notice", job)
		}
	})

	t.Run("IT-160 a note of another capture is excluded and its text hidden", func(t *testing.T) {
		f := newFlow(t, "PHOTO")
		key, photo := f.voiceRoom("Sala")
		other := identity.NewID()
		f.within(func(tx *gorm.DB) error {
			return tx.Create(&database.Responsibility{ID: other, TenantID: f.seed.TenantID, InspectionID: f.seed.InspectionID, ParticipantID: f.seed.ParticipantID, Status: "SUBMITTED", Version: 1, CreatedAt: f.now(), UpdatedAt: f.now()}).Error
		})
		f.note(itNote{Key: key, Photo: photo, Text: "texto de outra captura", Responsibility: other})
		f.gateway.Respond = func(llm.StructuredRequest) (string, error) { return catalogJSON(t), nil }
		f.submitPhotos()
		f.drain()
		if len(f.manifests()) != 0 {
			t.Fatalf("manifests = %+v, want none: the foreign capture's note is not part of this room", f.manifests())
		}
		for _, request := range f.catalogRequests() {
			if strings.Contains(request.UserPrompt, "outra captura") {
				t.Fatal("a note of another capture reached the model")
			}
		}
	})

	t.Run("IT-161 the result cites the revision fixed before the new one appeared", func(t *testing.T) {
		f := newFlow(t, "PHOTO")
		key, photo := f.voiceRoom("Sala")
		note, first := f.note(itNote{Key: key, Photo: photo, Text: "cinco pratos"})
		f.answerWith(voiceItem{name: "Pratos", evidence: []string{photo.String()}, citations: []map[string]any{cite(note, first, "name")}})
		f.submitPhotos()
		// A correction lands after the manifest was fixed and before the job runs.
		f.revise(note, 2, "seis pratos")
		f.drain()
		items := f.catalogItems()
		if len(items) != 1 || ExtractVoiceSources(items[0])[0].RevisionID != first {
			t.Fatalf("items = %+v, want a citation of the fixed revision", items)
		}
		notes := mustVoiceData(t, f.catalogRequests()[0])
		if len(notes) != 1 || notes[0].(map[string]any)["text"] != "cinco pratos" {
			t.Fatalf("voice notes = %v, want R1", notes)
		}
	})

	t.Run("IT-162 a stalled transcription releases visual suggestions at the deadline", func(t *testing.T) {
		f := newFlow(t, "PHOTO")
		key, photo := f.voiceRoom("Sala")
		start := f.now()
		f.note(itNote{Key: key, Photo: photo, Status: "PROCESSING"})
		f.gateway.Respond = func(llm.StructuredRequest) (string, error) {
			return catalogJSON(t, catalogItemSpec{name: "Sofá", confidence: 9000, evidence: []string{photo.String()}}), nil
		}
		f.submitPhotos()
		if job := f.catalogJob(key); !job.NextAttemptAt.Equal(start.Add(catalognotes.VoiceWait)) {
			t.Fatalf("job due at %v, want visual-ready + 2m", job.NextAttemptAt)
		}
		f.drain()
		if f.now().Before(start.Add(catalognotes.VoiceWait)) {
			t.Fatalf("job ran at %v, before the wait ended", f.now())
		}
		manifests := f.manifests()
		if len(f.catalogItems()) != 1 || len(manifests) != 1 || manifests[0].SealedAt == nil || manifests[0].MissingCount != 1 || len(mustVoiceData(t, f.catalogRequests()[0])) != 0 {
			t.Fatalf("items=%d manifests=%+v, want visual suggestions with a missing-voice notice", len(f.catalogItems()), manifests)
		}
	})

	t.Run("IT-163 a retry with identical inputs does not duplicate suggestions", func(t *testing.T) {
		f := newFlow(t, "PHOTO")
		key, photo := f.voiceRoom("Sala")
		note, revision := f.note(itNote{Key: key, Photo: photo, Text: "cinco pratos"})
		f.answerWith(voiceItem{name: "Pratos", evidence: []string{photo.String()}, citations: []map[string]any{cite(note, revision, "name")}})
		f.submitPhotos()
		f.drain()
		f.within(func(tx *gorm.DB) error {
			return tx.Model(&database.InventoryJob{}).Where("tenant_id=? AND requirement_key=?", f.seed.TenantID, key).Updates(map[string]any{"status": "PENDING", "attempts": 0, "next_attempt_at": f.now(), "lease_token": nil, "lease_until": nil}).Error
		})
		f.drain()
		if len(f.catalogRequests()) != 2 || len(f.catalogItems()) != 1 || len(f.manifests()) != 1 {
			t.Fatalf("requests=%d items=%d manifests=%d, want 2/1/1", len(f.catalogRequests()), len(f.catalogItems()), len(f.manifests()))
		}
	})

	t.Run("IT-164 a video note waits for its anchor, then follows its interval to the exact clip", func(t *testing.T) {
		f := newFlow(t, "VIDEO")
		key := "env:new:" + identity.NewID().String()
		f.requirements(inventorytest.EnvironmentRequirement(key, "LIVING_ROOM", "Sala", true))
		attempt, clip, prep, now := identity.NewID(), identity.NewID(), identity.NewID(), f.now()
		frames := make([]identity.ID, 0, 4)
		f.within(func(tx *gorm.DB) error {
			if err := tx.Create(&database.VideoAttempt{ID: attempt, TenantID: f.seed.TenantID, ResponsibilityID: f.seed.ResponsibilityID, Kind: "CURRENT", RequirementKey: &key, PolicyVersion: 1, MaxDurationSeconds: 900, MaxBytes: 1 << 30, AcquisitionSource: "CAMERA", Status: "RECEIVED", IdempotencyKey: "it164-" + attempt.String(), PayloadDigest: "it164", AdmittedAt: now, ExpiresAt: now.Add(time.Hour), CreatedAt: now}).Error; err != nil {
				return err
			}
			if err := tx.Create(&database.MediaObject{ID: clip, TenantID: f.seed.TenantID, ResponsibilityID: f.seed.ResponsibilityID, ObjectKey: "tenant/" + f.seed.TenantID.String() + "/media/" + clip.String(), ContentType: "video/mp4", SHA256: strings.Repeat("c", 64), SizeBytes: 1, Status: "READY", MediaKind: "VIDEO", VideoAttemptID: &attempt, IdempotencyKey: "it164-clip-" + clip.String(), RequirementKey: key, AttentionItems: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), CreatedAt: now}).Error; err != nil {
				return err
			}
			requirementKey := key
			if err := tx.Create(&database.VideoPreparationJob{ID: prep, TenantID: f.seed.TenantID, MediaID: clip, RequirementKey: &requirementKey, Status: "READY", Phase: "READY", PreparationVersion: 2, Generation: 1, NextAttemptAt: now, SourceSHA256: strings.Repeat("c", 64), ManifestDigest: strings.Repeat("c", 64), PendingObjectKeys: []byte(`[]`), FirstReadyAt: &now, CreatedAt: now}).Error; err != nil {
				return err
			}
			for i := 0; i < 4; i++ {
				frame := identity.NewID()
				object := "tenant/" + f.seed.TenantID.String() + "/video-frames/" + frame.String() + ".jpg"
				f.put(object)
				if err := tx.Create(&database.VideoFrame{ID: frame, TenantID: f.seed.TenantID, SourceMediaID: clip, PreparationID: prep, PTSMicros: int64(i+1) * 1_000_000, SHA256: strings.Repeat("d", 64), ObjectKey: object, Width: 1, Height: 1, Role: "BASELINE", Representative: true, Quality: "GOOD", SizeBytes: 1, CreatedAt: now}).Error; err != nil {
					return err
				}
				frames = append(frames, frame)
			}
			return nil
		})
		f.answer(key, []identity.ID{clip}, "")
		note, revision := identity.NewID(), identity.NewID()
		f.within(func(tx *gorm.DB) error {
			if err := tx.Exec(`INSERT INTO transcription.audio_notes (tenant_id,id,inspection_id,responsibility_id,client_note_id,requirement_key,consent_version,disclosure_version,target_kind,video_attempt_id,start_ms,end_ms,time_basis,duration_ms,content_sha256,content_type,size_bytes,status,anchor_status,created_at,updated_at)
VALUES (?,?,?,?,?,?,'v1','v1','VIDEO_INTERVAL',?,500,2000,'CAPTURE_CLOCK',3000,?,'audio/webm',1000,'COMPLETED','PENDING',?,?)`, f.seed.TenantID, note, f.seed.InspectionID, f.seed.ResponsibilityID, note, key, attempt, strings.Repeat("a", 64), now, now).Error; err != nil {
				return err
			}
			if err := tx.Create(&database.TranscriptRevision{ID: revision, TenantID: f.seed.TenantID, NoteID: note, RevisionNumber: 1, Text: "cinco pratos", Kind: "PARTICIPANT_DECLARATION", CreatedAt: now}).Error; err != nil {
				return err
			}
			return tx.Exec("UPDATE transcription.audio_notes SET current_revision_id=? WHERE tenant_id=? AND id=?", revision, f.seed.TenantID, note).Error
		})
		f.deliver("capture.video_submitted.v2", events.CaptureVideoSubmittedV2Payload{InspectionID: f.seed.InspectionID, ResponsibilityID: f.seed.ResponsibilityID, SubmissionVersionID: identity.NewID()})
		start := f.now()
		if job := f.catalogJob(key); !job.NextAttemptAt.After(start) {
			t.Fatalf("job due at %v, want it to wait for the unresolved anchor", job.NextAttemptAt)
		}
		// The prepared clip is measured: the exact attempt's interval resolves.
		f.within(func(tx *gorm.DB) error {
			return tx.Create(&database.VideoSource{ID: identity.NewID(), TenantID: f.seed.TenantID, MediaID: clip, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 8_000_000, Width: 1, Height: 1, FrameRate: 30, Status: "RETAINED", CreatedAt: now, UpdatedAt: now}).Error
		})
		f.clock.Add(int64(10 * time.Second))
		f.deliver("media.video_prepared.v1", events.VideoPreparedPayload{MediaID: clip, PreparationID: prep, ManifestDigest: strings.Repeat("c", 64)})
		if job := f.catalogJob(key); job.NextAttemptAt.After(f.now()) {
			t.Fatalf("job due at %v, want it released once the anchor resolved", job.NextAttemptAt)
		}
		f.answerWith(voiceItem{name: "Pratos", citations: []map[string]any{cite(note, revision, "name")}})
		f.drain()
		requests := f.catalogRequests()
		if len(requests) != 1 {
			t.Fatalf("catalog requests = %d", len(requests))
		}
		entry := mustVoiceData(t, requests[0])[0].(map[string]any)
		if entry["targetKind"] != "VIDEO_INTERVAL" || entry["sourceMediaId"] != clip.String() || entry["startMs"] != float64(500) || entry["endMs"] != float64(2000) {
			t.Fatalf("voice note = %v, want the interval of the attempt's clip", entry)
		}
		var near []string
		for _, evidence := range userData(t, requests[0])["evidence"].([]any) {
			if ids, ok := evidence.(map[string]any)["nearVoiceNoteIds"]; ok {
				near = append(near, evidence.(map[string]any)["frameId"].(string))
				if ids.([]any)[0] != note.String() {
					t.Fatalf("near hint = %v", ids)
				}
			}
		}
		if len(near) != 2 || near[0] != frames[0].String() && near[1] != frames[0].String() {
			t.Fatalf("frames near the interval = %v, want the frames at 1s and 2s", near)
		}
	})

	t.Run("IT-165 a fresh note cannot rewrite an active version", func(t *testing.T) {
		f := newFlow(t, "PHOTO")
		key, photo := f.voiceRoom("Sala")
		note, revision := f.note(itNote{Key: key, Photo: photo, Text: "cinco pratos"})
		f.submitPhotos()
		f.within(func(tx *gorm.DB) error {
			return tx.Model(&database.OriginVersion{}).Where("tenant_id=? AND source_inspection_id=?", f.seed.TenantID, f.seed.InspectionID).Updates(map[string]any{"status": "DISCARDED"}).Error
		})
		f.answerWith(voiceItem{name: "Pratos", evidence: []string{photo.String()}, citations: []map[string]any{cite(note, revision, "name")}})
		f.drain()
		if items := f.catalogItems(); len(items) != 0 {
			t.Fatalf("items = %+v, want a closed draft left untouched", items)
		}
	})

	t.Run("IT-166 each room catalog uses only its own notes and photo", func(t *testing.T) {
		f := newFlow(t, "PHOTO")
		keys, photos := f.voiceRooms("Sala", "Cozinha")
		f.note(itNote{Key: keys[0], Photo: photos[0], Text: "nota da sala"})
		f.note(itNote{Key: keys[1], Photo: photos[1], Text: "nota da cozinha"})
		f.gateway.Respond = func(llm.StructuredRequest) (string, error) { return catalogJSON(t), nil }
		f.submitPhotos()
		f.drain()
		requests := f.catalogRequests()
		if len(requests) != 2 {
			t.Fatalf("catalog requests = %d, want one per room", len(requests))
		}
		for _, request := range requests {
			notes := mustVoiceData(t, request)
			if len(notes) != 1 || len(request.Images) != 1 {
				t.Fatalf("notes=%d images=%d, want one own note and photo", len(notes), len(request.Images))
			}
			text := notes[0].(map[string]any)["text"]
			ownPhoto := photos[0]
			if text == "nota da cozinha" {
				ownPhoto = photos[1]
			}
			if request.Images[0].EvidenceID != ownPhoto.String() {
				t.Fatalf("room with note %q got image %s", text, request.Images[0].EvidenceID)
			}
		}
	})
}

func TestVisualOnlyParityIT307ToIT320(t *testing.T) {
	t.Run("IT-307 IT-312 IT-313 IT-317 visual inspections catalog without waiting whatever the voice policy says", func(t *testing.T) {
		f := newFlow(t, "PHOTO")
		key, photo := f.voiceRoom("Sala")
		// The tenant's voice allowance is exhausted and voice is disabled.
		f.within(func(tx *gorm.DB) error {
			return tx.Exec(`INSERT INTO transcription.tenant_policies (tenant_id,enabled,tenant_note_limit,tenant_audio_limit_ms,inspection_note_limit,inspection_audio_limit_ms) VALUES (?,false,1,1000,1,1000)`, f.seed.TenantID).Error
		})
		f.gateway.Respond = func(llm.StructuredRequest) (string, error) {
			// A model that invents a voice citation must not create a voice-derived item.
			return voiceCatalogJSON(t, voiceItem{name: "Fantasma", citations: []map[string]any{cite(identity.NewID(), identity.NewID(), "name")}}, voiceItem{name: "Sofá", evidence: []string{photo.String()}}), nil
		}
		f.submitPhotos()
		if job := f.catalogJob(key); job.NextAttemptAt.After(f.now()) {
			t.Fatalf("job due at %v, want no wait without notes", job.NextAttemptAt)
		}
		f.drain()
		items := f.catalogItems()
		if len(items) != 1 || items[0].Name != "Sofá" || items[0].Declared || len(f.manifests()) != 0 {
			t.Fatalf("items=%+v manifests=%d, want the visual-only result", items, len(f.manifests()))
		}
	})

	t.Run("IT-308 IT-318 legacy and historical inspections create neither jobs nor manifests", func(t *testing.T) {
		if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
			t.Skip("isolated PostgreSQL and runtime-role URLs are required")
		}
		ctx := context.Background()
		h, err := harness.NewFromEnv(ctx)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = h.Close() })
		// A legacy (schema 1) inspection keeps its current rules even if a note row exists.
		seed, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{InspectionStatus: "SUBMITTED", SchemaVersion: 1, RequirementKey: "room-1", ComparisonMode: "CURRENT_ONLY"})
		if err != nil {
			t.Fatal(err)
		}
		f := &flow{t: t, ctx: ctx, h: h, seed: seed, gateway: &inventorytest.Gateway{}, clock: &atomic.Int64{}}
		f.clock.Store(time.Now().UTC().Truncate(time.Microsecond).UnixNano())
		f.note(itNote{Key: "room-1", Photo: seed.MediaID, Text: "nota forjada"})
		f.submitPhotos()
		if len(f.jobs()) != 0 || len(f.manifests()) != 0 {
			t.Fatalf("jobs=%d manifests=%d, want the legacy rules untouched", len(f.jobs()), len(f.manifests()))
		}
	})

	t.Run("IT-309 incidental video sound is never transcribed by the catalog flow", func(t *testing.T) {
		f := newFlow(t, "PHOTO")
		key, photo := f.voiceRoom("Sala")
		f.gateway.Respond = func(llm.StructuredRequest) (string, error) {
			return catalogJSON(t, catalogItemSpec{name: "Sofá", confidence: 9000, evidence: []string{photo.String()}}), nil
		}
		f.submitPhotos()
		f.drain()
		var notes, jobs int64
		f.within(func(tx *gorm.DB) error {
			if err := tx.Raw("SELECT count(*) FROM transcription.audio_notes WHERE tenant_id=?", f.seed.TenantID).Row().Scan(&notes); err != nil {
				return err
			}
			return tx.Raw("SELECT count(*) FROM transcription.jobs WHERE tenant_id=?", f.seed.TenantID).Row().Scan(&jobs)
		})
		if notes != 0 || jobs != 0 || f.catalogJob(key).Status != "SUCCEEDED" {
			t.Fatalf("notes=%d transcription jobs=%d, want none created by cataloging", notes, jobs)
		}
	})

	t.Run("IT-310 IT-319 an active baseline is not recataloged and its decisions stay", func(t *testing.T) {
		f := newFlow(t, "PHOTO")
		baseline := f.pin(inventorytest.BaselineEnvironment{Name: "Sala", TypeCode: "LIVING_ROOM", Items: []inventorytest.BaselineItem{{Name: "Sofá", TypeCode: "SOFA", Condition: "GOOD", Evidence: 1}}})
		key := inventorytest.EnvironmentKey(baseline.environments[0])
		f.requirements(inventorytest.EnvironmentRequirement(key, "LIVING_ROOM", "Sala", false))
		photo := f.photo(key)
		f.answer(key, []identity.ID{photo}, "")
		f.note(itNote{Key: key, Photo: photo, Text: "cinco pratos"})
		before := baseline.items["Sofá"]
		f.submitPhotos()
		f.submitPhotos()
		for _, job := range f.jobs() {
			if job.Kind == "CATALOG" {
				t.Fatalf("job %+v: an active baseline must not be recataloged", job)
			}
		}
		var item database.InventoryItem
		f.within(func(tx *gorm.DB) error {
			return tx.Where("tenant_id=? AND id=?", f.seed.TenantID, before.ID).Take(&item).Error
		})
		if len(f.manifests()) != 0 || item.Decision != before.Decision || item.Version != before.Version || item.Name != before.Name {
			t.Fatalf("manifests=%d item=%+v, want the baseline untouched", len(f.manifests()), item)
		}
	})

	t.Run("IT-315 changing voice settings during cataloging keeps the fixed scope", func(t *testing.T) {
		f := newFlow(t, "PHOTO")
		key, photo := f.voiceRoom("Sala")
		note, revision := f.note(itNote{Key: key, Photo: photo, Text: "cinco pratos"})
		f.submitPhotos()
		f.within(func(tx *gorm.DB) error {
			return tx.Exec(`INSERT INTO transcription.tenant_policies (tenant_id,enabled) VALUES (?,false)`, f.seed.TenantID).Error
		})
		f.answerWith(voiceItem{name: "Pratos", evidence: []string{photo.String()}, citations: []map[string]any{cite(note, revision, "name")}})
		f.drain()
		if items := f.catalogItems(); len(items) != 1 || len(ExtractVoiceSources(items[0])) != 1 {
			t.Fatalf("items = %+v, want the accepted note to stay in the fixed scope", items)
		}
	})

	t.Run("IT-316 an unavailable transcription service keeps visual work and an honest note status", func(t *testing.T) {
		f := newFlow(t, "PHOTO")
		key, photo := f.voiceRoom("Sala")
		note, _ := f.note(itNote{Key: key, Photo: photo, Status: "RETRY_WAIT"})
		f.gateway.Respond = func(llm.StructuredRequest) (string, error) {
			return catalogJSON(t, catalogItemSpec{name: "Sofá", confidence: 9000, evidence: []string{photo.String()}}), nil
		}
		f.submitPhotos()
		f.setNote(note, "status", "FAILED")
		f.deliver("transcription.failed.v1", events.TranscriptionFailedPayload{NoteID: note, Generation: 1, SafeCode: "PROVIDER_UNAVAILABLE"})
		f.drain()
		var status string
		f.within(func(tx *gorm.DB) error {
			return tx.Raw("SELECT status FROM transcription.audio_notes WHERE tenant_id=? AND id=?", f.seed.TenantID, note).Row().Scan(&status)
		})
		if len(f.catalogItems()) != 1 || status != "FAILED" {
			t.Fatalf("items=%d note status=%s, want visual suggestions and the real FAILED status", len(f.catalogItems()), status)
		}
	})

	t.Run("IT-320 parallel inspections never leak notes or manifests across tenants", func(t *testing.T) {
		f := newFlow(t, "PHOTO")
		key, photo := f.voiceRoom("Sala")
		note, _ := f.note(itNote{Key: key, Photo: photo, Text: "nota do cliente A"})
		other, err := f.h.SeedTask06Fixture(f.ctx, harness.Task06FixtureOptions{InspectionStatus: "SUBMITTED", SchemaVersion: 2, RequirementKey: "photo:overview", ComparisonMode: "CURRENT_ONLY"})
		if err != nil {
			t.Fatal(err)
		}
		f.gateway.Respond = func(llm.StructuredRequest) (string, error) { return catalogJSON(t), nil }
		f.submitPhotos()
		f.drain()
		var leaked int64
		if err := f.h.WithinTenant(f.ctx, other.TenantID, func(tx *gorm.DB) error {
			return tx.Raw("SELECT count(*) FROM transcription.audio_notes WHERE id=?", note).Row().Scan(&leaked)
		}); err != nil || leaked != 0 {
			t.Fatalf("tenant B sees %d notes of tenant A (err=%v)", leaked, err)
		}
		if err := f.h.WithinTenant(f.ctx, other.TenantID, func(tx *gorm.DB) error {
			return tx.Raw("SELECT count(*) FROM analysis.catalog_note_manifests").Row().Scan(&leaked)
		}); err != nil || leaked != 0 {
			t.Fatalf("tenant B sees %d manifests of tenant A (err=%v)", leaked, err)
		}
		notes := mustVoiceData(t, f.catalogRequests()[0])
		if len(notes) != 1 || notes[0].(map[string]any)["text"] != "nota do cliente A" {
			t.Fatalf("voice notes = %v", notes)
		}
	})
}

// TestCatalogMessageConsumersIT378ToIT385 delivers MSG02, MSG03, MSG04 and
// MSG05 envelopes through the real consumer (registry validation, inbox
// receipt, tenant transaction) to the request handler.
func TestCatalogMessageConsumersIT378ToIT385(t *testing.T) {
	handlerFor := func(f *flow) func(context.Context, *gorm.DB, events.RawEnvelope) error {
		handler, err := request_inventory_jobs.Setup(request_inventory_jobs.Dependencies{Now: f.now})
		if err != nil {
			t.Fatal(err)
		}
		return handler
	}
	deliver := func(f *flow, name string, tenant identity.ID, eventType string, payload any) (bool, error) {
		body, err := json.Marshal(events.Envelope[any]{ID: identity.NewID(), Type: eventType, SchemaVersion: 1, OccurredAt: f.now(), TenantID: tenant, AggregateID: f.seed.InspectionID, CorrelationID: f.seed.InspectionID.String(), Payload: payload})
		if err != nil {
			t.Fatal(err)
		}
		consumer := messaging.Consumer{DB: f.h.DB, Registry: events.DefaultRegistry(), Name: name, Handle: handlerFor(f), Clock: f.now}
		return consumer.Process(f.ctx, body, 0)
	}
	waiting := func(t *testing.T) (*flow, string, identity.ID, identity.ID) {
		t.Helper()
		f := newFlow(t, "PHOTO")
		key, photo := f.voiceRoom("Sala")
		note, _ := f.note(itNote{Key: key, Photo: photo, Status: "PROCESSING"})
		f.submitPhotos()
		if job := f.catalogJob(key); !job.NextAttemptAt.After(f.now()) {
			t.Fatal("the job should be waiting for the note")
		}
		return f, key, note, photo
	}
	released := func(f *flow, key string) bool { return !f.catalogJob(key).NextAttemptAt.After(f.now()) }

	t.Run("IT-378 IT-379 MSG02 completed: valid envelope twice is one transition, invalid is rejected", func(t *testing.T) {
		f, key, note, _ := waiting(t)
		revision := f.revise(note, 1, "cinco pratos")
		f.setNote(note, "status", "COMPLETED")
		payload := events.TranscriptionCompletedPayload{NoteID: note, RevisionID: &revision, Generation: 1}
		if processed, err := deliver(f, "it-msg02", f.seed.TenantID, "transcription.completed.v1", payload); err != nil || !processed {
			t.Fatalf("first delivery processed=%v err=%v", processed, err)
		}
		if !released(f, key) || len(f.manifests()) != 1 || f.manifests()[0].SealedAt == nil {
			t.Fatal("MSG02 did not seal the manifest and release the job")
		}
		body, _ := json.Marshal(events.Envelope[any]{ID: identity.NewID(), Type: "transcription.completed.v1", SchemaVersion: 1, OccurredAt: f.now(), TenantID: f.seed.TenantID, AggregateID: note, CorrelationID: note.String(), Payload: payload})
		consumer := messaging.Consumer{DB: f.h.DB, Registry: events.DefaultRegistry(), Name: "it-msg02-dup", Handle: handlerFor(f), Clock: f.now}
		for i := 0; i < 2; i++ {
			if _, err := consumer.Process(f.ctx, body, 0); err != nil {
				t.Fatal(err)
			}
		}
		if len(f.manifests()) != 1 || len(f.jobs()) != 1 {
			t.Fatalf("manifests=%d jobs=%d, want one durable transition after redelivery", len(f.manifests()), len(f.jobs()))
		}
		for _, bad := range []any{map[string]any{"generation": 1}, events.TranscriptionCompletedPayload{NoteID: note, RevisionID: &revision, Generation: 0}} {
			if _, err := deliver(f, "it-msg02-bad", f.seed.TenantID, "transcription.completed.v1", bad); err == nil {
				t.Fatal("an invalid MSG02 envelope was accepted")
			}
		}
	})

	t.Run("IT-380 IT-381 MSG03 failed: valid envelope releases the wait, invalid is rejected", func(t *testing.T) {
		f, key, note, _ := waiting(t)
		f.setNote(note, "status", "FAILED")
		payload := events.TranscriptionFailedPayload{NoteID: note, Generation: 1, SafeCode: "PROVIDER_UNAVAILABLE"}
		for i := 0; i < 2; i++ {
			if _, err := deliver(f, "it-msg03", f.seed.TenantID, "transcription.failed.v1", payload); err != nil {
				t.Fatal(err)
			}
		}
		if !released(f, key) || len(f.manifests()) != 1 {
			t.Fatal("MSG03 did not release the waiting job")
		}
		if _, err := deliver(f, "it-msg03-bad", f.seed.TenantID, "transcription.failed.v1", events.TranscriptionFailedPayload{NoteID: note, Generation: 1, SafeCode: "detalhe do provedor"}); err == nil {
			t.Fatal("an MSG03 envelope with an unsafe code was accepted")
		}
		if _, err := deliver(f, "it-msg03-bad", f.seed.TenantID, "transcription.failed.v1", map[string]any{"generation": 1, "safeCode": "PROVIDER_UNAVAILABLE"}); err == nil {
			t.Fatal("an MSG03 envelope without a note was accepted")
		}
	})

	t.Run("IT-382 IT-383 MSG04 submission: twice is one manifest and one job, invalid is rejected", func(t *testing.T) {
		f := newFlow(t, "PHOTO")
		key, photo := f.voiceRoom("Sala")
		f.note(itNote{Key: key, Photo: photo, Status: "QUEUED"})
		for i := 0; i < 2; i++ {
			if _, err := deliver(f, fmt.Sprintf("it-msg04-%d", i), f.seed.TenantID, "capture.submitted.v1", map[string]any{"inspectionId": f.seed.InspectionID}); err != nil {
				t.Fatal(err)
			}
		}
		if len(f.manifests()) != 1 || len(f.jobs()) != 1 || len(f.catalogRequests()) != 0 {
			t.Fatalf("manifests=%d jobs=%d model calls=%d, want one transition and no model transport", len(f.manifests()), len(f.jobs()), len(f.catalogRequests()))
		}
		if _, err := deliver(f, "it-msg04-bad", f.seed.TenantID, "capture.submitted.v1", map[string]any{"inspectionId": identity.ID{}}); err == nil {
			t.Fatal("a submission without an inspection was accepted")
		}
		// An envelope of another tenant cannot mutate this tenant's rows.
		if _, err := deliver(f, "it-msg04-foreign", identity.NewID(), "capture.submitted.v1", map[string]any{"inspectionId": f.seed.InspectionID}); err == nil {
			// The foreign tenant has no such inspection: it must be rejected, not applied.
			t.Fatal("a foreign-tenant submission was accepted")
		}
		if len(f.manifests()) != 1 || len(f.jobs()) != 1 {
			t.Fatal("a foreign envelope changed this tenant")
		}
	})

	t.Run("IT-384 IT-385 MSG05 job requested: valid envelope re-evaluates readiness, invalid is rejected", func(t *testing.T) {
		f, key, note, _ := waiting(t)
		revision := f.revise(note, 1, "cinco pratos")
		f.setNote(note, "status", "COMPLETED")
		payload := events.InventoryJobRequestedPayload{JobID: identity.NewID(), InspectionID: f.seed.InspectionID}
		for i := 0; i < 2; i++ {
			if _, err := deliver(f, fmt.Sprintf("it-msg05-%d", i), f.seed.TenantID, "inventory.job_requested.v1", payload); err != nil {
				t.Fatal(err)
			}
		}
		manifests := f.manifests()
		if !released(f, key) || len(manifests) != 1 || manifests[0].SealedAt == nil || *manifests[0].Entries[0].RevisionID != revision || len(f.jobs()) != 1 {
			t.Fatalf("manifests=%+v jobs=%d, want a single release with R1 fixed", manifests, len(f.jobs()))
		}
		if _, err := deliver(f, "it-msg05-bad", f.seed.TenantID, "inventory.job_requested.v1", map[string]any{"jobId": identity.ID{}, "inspectionId": f.seed.InspectionID}); err == nil {
			t.Fatal("an MSG05 envelope without a job was accepted")
		}
	})
}

// TestSealedManifestIsImmutableInPostgres proves the database refuses to change
// a sealed manifest, even through a privileged connection, while an open one
// can still be updated by the runtime role.
func TestSealedManifestIsImmutableInPostgres(t *testing.T) {
	f := newFlow(t, "PHOTO")
	key, photo := f.voiceRoom("Sala")
	f.note(itNote{Key: key, Photo: photo, Status: "PROCESSING"})
	f.submitPhotos()
	manifests := f.manifests()
	if len(manifests) != 1 || manifests[0].SealedAt != nil {
		t.Fatalf("manifests = %+v, want one open manifest", manifests)
	}
	f.within(func(tx *gorm.DB) error {
		return tx.Exec("UPDATE analysis.catalog_note_manifests SET missing_count=2 WHERE tenant_id=? AND id=?", f.seed.TenantID, manifests[0].ID).Error
	})
	f.within(func(tx *gorm.DB) error {
		return tx.Exec("UPDATE analysis.catalog_note_manifests SET sealed_at=? WHERE tenant_id=? AND id=?", f.now(), f.seed.TenantID, manifests[0].ID).Error
	})
	err := f.h.WithinTenant(f.ctx, f.seed.TenantID, func(tx *gorm.DB) error {
		return tx.Exec("UPDATE analysis.catalog_note_manifests SET missing_count=0, note_revisions='[]' WHERE tenant_id=? AND id=?", f.seed.TenantID, manifests[0].ID).Error
	})
	if err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("update of a sealed manifest error = %v, want it rejected as immutable", err)
	}
}
