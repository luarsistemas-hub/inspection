//go:build integration

package process_inventory_job

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"inspection/libs/identity"
	catalognotes "inspection/services/inspection/internal/features/analysis/catalog_notes"
	inventorycore "inspection/services/inspection/internal/features/inventory/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"

	"gorm.io/gorm"
)

// These flows run the explicit regeneration (IT-223..236) and the provenance
// writer (IT-167..180, IT-195..208) against the disposable PostgreSQL with the
// runtime role: real RLS, grants, constraints and the real catalog executor.

func (f *flow) inventory() inventorycore.Service {
	return inventorycore.Service{DB: f.h.DB, Within: f.h.WithinTenant, Now: f.now}
}

// draftOf returns the draft of the room's catalog and the origin revision.
func (f *flow) draftOf(key string) (draft identity.ID, revision int) {
	f.t.Helper()
	job := f.catalogJob(key)
	f.within(func(tx *gorm.DB) error {
		var version database.OriginVersion
		if err := tx.Where("tenant_id=? AND id=?", f.seed.TenantID, job.OriginVersionID).Take(&version).Error; err != nil {
			return err
		}
		var origin database.Origin
		if err := tx.Where("tenant_id=? AND id=?", f.seed.TenantID, version.OriginID).Take(&origin).Error; err != nil {
			return err
		}
		draft, revision = version.ID, int(origin.Version)
		return nil
	})
	return draft, revision
}

func (f *flow) environmentOf(key string) database.InventoryEnvironment {
	f.t.Helper()
	var env database.InventoryEnvironment
	f.within(func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND requirement_key=?", f.seed.TenantID, key).Take(&env).Error
	})
	return env
}

func (f *flow) regenerate(key, mutation string, revisions ...identity.ID) (inventorycore.RegenerateResult, error) {
	f.t.Helper()
	draft, revision := f.draftOf(key)
	return f.inventory().RegenerateSuggestions(f.ctx, inventorycore.RegenerateInput{TenantID: f.seed.TenantID, OriginVersionID: draft, EnvironmentID: f.environmentOf(key).ID, ActorMembershipID: f.seed.MembershipID, ExpectedVersion: revision, RevisionIDs: revisions, ClientMutationID: mutation})
}

func (f *flow) mustRegenerate(key, mutation string, revisions ...identity.ID) inventorycore.RegenerateResult {
	f.t.Helper()
	result, err := f.regenerate(key, mutation, revisions...)
	if err != nil {
		f.t.Fatalf("regeneration failed: %v", err)
	}
	return result
}

func (f *flow) itemsOfRoom(key string) map[string]database.InventoryItem {
	f.t.Helper()
	out := map[string]database.InventoryItem{}
	f.within(func(tx *gorm.DB) error {
		var rows []database.InventoryItem
		if err := tx.Where("tenant_id=? AND environment_id=?", f.seed.TenantID, f.environmentOf(key).ID).Order("name,created_at").Find(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			if _, dup := out[row.Name]; dup {
				f.t.Fatalf("%s appears twice in the room", row.Name)
			}
			out[row.Name] = row
		}
		return nil
	})
	return out
}

func (f *flow) sourcesOf(item identity.ID) []database.InventoryItemAttributeSource {
	f.t.Helper()
	var rows []database.InventoryItemAttributeSource
	f.within(func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND item_id=?", f.seed.TenantID, item).Order("attribute_name,source_kind,created_at").Find(&rows).Error
	})
	return rows
}

func (f *flow) voiceCites(item identity.ID) map[string]identity.ID {
	f.t.Helper()
	out := map[string]identity.ID{}
	for _, row := range f.sourcesOf(item) {
		if row.SourceKind == "VOICE" {
			out[row.NoteID.String()] = *row.RevisionID
		}
	}
	return out
}

func (f *flow) decide(draft identity.ID, item database.InventoryItem, action string) {
	f.t.Helper()
	if _, err := f.inventory().Decide(f.ctx, inventorycore.DecideInput{TenantID: f.seed.TenantID, OriginVersionID: draft, ActorMembershipID: f.seed.MembershipID, Decisions: []inventorycore.Decision{{ItemID: item.ID, Action: action, ExpectedVersion: item.Version, Reason: "NOT_EXISTS"}}}); err != nil {
		f.t.Fatalf("decide %s: %v", action, err)
	}
}

// latestNotes answers every request by citing, for each item name, the notes
// of the request: pratos cite all of them, like a model reading the manifest.
func (f *flow) citeNotesOf(names ...string) {
	f.t.Helper()
	photo := func(req llm.StructuredRequest) []string {
		var ids []string
		if raw, ok := userData(f.t, req)["evidence"].([]any); ok {
			for _, entry := range raw {
				ids = append(ids, entry.(map[string]any)["mediaId"].(string))
			}
		}
		return ids
	}
	f.gateway.Respond = func(req llm.StructuredRequest) (string, error) {
		if req.Mode != "INVENTORY_CATALOG" {
			return "", errors.New("not a catalog request")
		}
		notes := mustVoiceData(f.t, req)
		var items []voiceItem
		for i, name := range names {
			ev := photo(req)
			if len(ev) == 0 {
				// A leftover job of another test sharing the database reached this
				// executor: it is not this flow's request.
				return "", errors.New("request without room evidence")
			}
			item := voiceItem{name: name, quantity: intp(i + 1), evidence: ev[:1]}
			for _, raw := range notes {
				entry := raw.(map[string]any)
				note, _ := identity.ParseID(entry["noteId"].(string))
				revision, _ := identity.ParseID(entry["revisionId"].(string))
				item.citations = append(item.citations, cite(note, revision, "name", "quantity"))
			}
			if len(notes) == 0 {
				item.citations = nil
			}
			items = append(items, item)
		}
		return voiceCatalogJSON(f.t, items...), nil
	}
}

// cataloged is a one-room flow whose first catalog already ran with note N1.
func cataloged(t *testing.T, names ...string) (f *flow, key string, photo identity.ID, note, revision identity.ID) {
	t.Helper()
	f = newFlow(t, "PHOTO")
	key, photo = f.voiceRoom("Cozinha")
	note, revision = f.note(itNote{Key: key, Photo: photo, Text: "cinco pratos brancos"})
	f.citeNotesOf(names...)
	f.submitPhotos()
	f.drain()
	return f, key, photo, note, revision
}

func TestRegenerationIT223ToIT236(t *testing.T) {
	t.Run("IT-223 IT-224 a late note joins only through an explicit regeneration that keeps decided items", func(t *testing.T) {
		f, key, photo, noteA, revA := cataloged(t, "Pratos", "Copos")
		draft, _ := f.draftOf(key)
		before := f.itemsOfRoom(key)
		if len(before) != 2 || before["Pratos"].Decision != "PENDING" {
			t.Fatalf("catalog items = %+v", before)
		}
		// The late note completes after the room's catalog: existing results stay unchanged.
		noteB, revB := f.note(itNote{Key: key, Photo: photo, Text: "seis taças de cristal"})
		after := f.itemsOfRoom(key)
		if len(after) != 2 || after["Pratos"].ID != before["Pratos"].ID || after["Copos"].ID != before["Copos"].ID {
			t.Fatalf("a late note changed the existing catalog: %+v", after)
		}
		if manifests := f.manifests(); len(manifests) != 1 || len(manifests[0].Included()) != 1 || *manifests[0].Included()[0].RevisionID != revA {
			t.Fatalf("the first manifest must stay fixed on N1/R1: %+v", manifests)
		}
		// A person decides "Pratos" before regenerating.
		f.decide(draft, before["Pratos"], "ACCEPT")
		f.citeNotesOf("Pratos", "Copos", "Taças")
		result := f.mustRegenerate(key, "regen-1", revA, revB)
		if result.Status != "PENDING" || result.Replayed {
			t.Fatalf("result = %+v", result)
		}
		f.drain()

		items := f.itemsOfRoom(key)
		if len(items) != 3 {
			t.Fatalf("items = %+v, want Pratos (decided), Copos and Taças", items)
		}
		if items["Pratos"].ID != before["Pratos"].ID || items["Pratos"].Decision != "ACCEPTED" {
			t.Fatalf("the decided item was rewritten: %+v", items["Pratos"])
		}
		if items["Copos"].Decision != "PENDING" || items["Copos"].ID == before["Copos"].ID {
			t.Fatalf("the undecided suggestion must be refreshed: %+v", items["Copos"])
		}
		cites := f.voiceCites(items["Taças"].ID)
		if cites[noteB.String()] != revB || cites[noteA.String()] != revA {
			t.Fatalf("the refreshed item cites %v, want both selected revisions", cites)
		}
		// The decided item keeps its first-generation citation and its review.
		if decided := f.voiceCites(items["Pratos"].ID); decided[noteA.String()] != revA || len(decided) != 1 {
			t.Fatalf("decided item citations = %v", decided)
		}
		reviewed := map[string]bool{}
		for _, row := range f.sourcesOf(items["Pratos"].ID) {
			if row.SourceKind == "HUMAN_REVIEW" {
				reviewed[row.AttributeName] = true
			}
		}
		if !reviewed["name"] || !reviewed["quantity"] {
			t.Fatalf("human reviews of the decided item = %v, want at least name and quantity", reviewed)
		}
		job := f.latestCatalogJob(key)
		if job.Generation != 2 || job.Status != "SUCCEEDED" || job.InputDigest == nil {
			t.Fatalf("job = %+v", job)
		}
		manifests := f.manifests()
		if len(manifests) != 2 || manifests[1].CatalogGeneration != 2 || len(manifests[1].Included()) != 2 || manifests[1].SealedAt == nil {
			t.Fatalf("manifests = %+v, want a second sealed generation with both notes", manifests)
		}
		if state := f.draftState(draft); state != "READY" {
			t.Fatalf("draft state = %s, a regeneration keeps the draft reviewable", state)
		}
		var trail int64
		f.within(func(tx *gorm.DB) error {
			return tx.Model(&database.InventoryDecision{}).Where("tenant_id=? AND origin_version_id=? AND action='REGENERATE'", f.seed.TenantID, draft).Count(&trail).Error
		})
		if trail != 1 {
			t.Fatalf("regeneration audit rows = %d, want 1", trail)
		}
	})

	t.Run("IT-225 a corrected transcript changes only the explicitly regenerated suggestion", func(t *testing.T) {
		f, key, _, note, first := cataloged(t, "Pratos")
		second := f.revise(note, 2, "seis pratos brancos")
		item := f.itemsOfRoom(key)["Pratos"]
		if cites := f.voiceCites(item.ID); cites[note.String()] != first {
			t.Fatalf("the item cites %v: the corrected text must not rewrite the cited revision", cites)
		}
		f.mustRegenerate(key, "regen-correct", second)
		f.drain()
		refreshed := f.itemsOfRoom(key)["Pratos"]
		if refreshed.ID == item.ID || f.voiceCites(refreshed.ID)[note.String()] != second {
			t.Fatalf("the regenerated suggestion must cite the corrected revision: %+v %v", refreshed, f.voiceCites(refreshed.ID))
		}
		requests := f.catalogRequests()
		last := mustVoiceData(t, requests[len(requests)-1])
		if len(last) != 1 || last[0].(map[string]any)["text"] != "seis pratos brancos" {
			t.Fatalf("the regeneration request carried %v", last)
		}
	})

	t.Run("IT-226 a published or active version is never rewritten", func(t *testing.T) {
		f, key, _, _, rev := cataloged(t, "Pratos")
		draft, _ := f.draftOf(key)
		before := f.itemsOfRoom(key)["Pratos"]
		f.within(func(tx *gorm.DB) error {
			return tx.Model(&database.OriginVersion{}).Where("tenant_id=? AND id=?", f.seed.TenantID, draft).Updates(map[string]any{"status": "ACTIVE", "purpose": "MOVE_IN"}).Error
		})
		late, lateRevision := f.note(itNote{Key: key, Photo: f.photo(key), Text: "mais pratos"})
		_ = late
		_, err := f.regenerate(key, "regen-active", rev, lateRevision)
		if !apperror.Is(err, apperror.InvalidState) {
			t.Fatalf("err = %v, want INVALID_STATE", err)
		}
		if got := f.itemsOfRoom(key)["Pratos"]; got.ID != before.ID || len(f.jobs()) != 1 {
			t.Fatalf("an active version was touched: %+v jobs=%d", got, len(f.jobs()))
		}
	})

	t.Run("IT-227 a note of another room is rejected with a scope message", func(t *testing.T) {
		f, key, _, _, rev := cataloged(t, "Pratos")
		otherKey, otherPhoto := f.voiceRoom("Sala")
		_, foreign := f.note(itNote{Key: otherKey, Photo: otherPhoto, Text: "dez cadeiras"})
		_, err := f.regenerate(key, "regen-foreign", rev, foreign)
		if !apperror.Is(err, apperror.InvalidInput) {
			t.Fatalf("err = %v, want INVALID_INPUT", err)
		}
		if _, field, message := apperror.Public(err); field != "revisionIds" || !strings.Contains(message, "não pertence") {
			t.Fatalf("field=%q message=%q", field, message)
		}
		if _, err := f.regenerate(key, "regen-unknown", identity.NewID()); !apperror.Is(err, apperror.InvalidInput) {
			t.Fatalf("unknown revision err = %v", err)
		}
	})

	t.Run("IT-228 nothing late or corrected means nothing to regenerate", func(t *testing.T) {
		f, key, _, _, rev := cataloged(t, "Pratos")
		_, err := f.regenerate(key, "regen-nothing", rev)
		if !apperror.Is(err, apperror.InvalidState) {
			t.Fatalf("err = %v, want INVALID_STATE", err)
		}
		if _, _, message := apperror.Public(err); !strings.Contains(message, "Não há notas novas") {
			t.Fatalf("message = %q", message)
		}
		if len(f.jobs()) != 1 {
			t.Fatal("a regeneration without news must not schedule work")
		}
	})

	t.Run("IT-229 IT-236 each room regenerates only its own notes and suggestions", func(t *testing.T) {
		f := newFlow(t, "PHOTO")
		keys, photos := f.voiceRooms("Cozinha", "Sala")
		noteK, revK := f.note(itNote{Key: keys[0], Photo: photos[0], Text: "cinco pratos"})
		noteS, revS := f.note(itNote{Key: keys[1], Photo: photos[1], Text: "dois sofás"})
		f.citeNotesOf("Item")
		f.submitPhotos()
		f.drain()
		sala := f.itemsOfRoom(keys[1])["Item"]
		lateK, lateRevK := f.note(itNote{Key: keys[0], Photo: photos[0], Text: "mais copos"})
		lateS, lateRevS := f.note(itNote{Key: keys[1], Photo: photos[1], Text: "mais cadeiras"})
		_, _ = lateS, lateRevS
		f.mustRegenerate(keys[0], "regen-kitchen", revK, lateRevK)
		f.drain()
		if got := f.itemsOfRoom(keys[1])["Item"]; got.ID != sala.ID {
			t.Fatal("the other room's suggestions changed")
		}
		requests := f.catalogRequests()
		kitchen := mustVoiceData(t, requests[len(requests)-1])
		if len(kitchen) != 2 {
			t.Fatalf("the kitchen request carried %d notes, want its own two", len(kitchen))
		}
		for _, raw := range kitchen {
			id := raw.(map[string]any)["noteId"].(string)
			if id == noteS.String() || id == lateS.String() {
				t.Fatalf("the kitchen request leaked a note of another room: %s", id)
			}
		}
		if jobs := f.jobsOf(keys[1]); len(jobs) != 1 {
			t.Fatalf("the other room scheduled work: %+v", jobs)
		}
		_ = noteK
		_ = revS
		_ = lateK
	})

	t.Run("IT-231 two regenerations together leave one current run", func(t *testing.T) {
		f, key, photo, _, rev := cataloged(t, "Pratos")
		_, late := f.note(itNote{Key: key, Photo: photo, Text: "mais taças"})
		draft, revision := f.draftOf(key)
		env := f.environmentOf(key)
		var wg sync.WaitGroup
		results := make([]error, 2)
		for i := range results {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				_, results[i] = f.inventory().RegenerateSuggestions(context.Background(), inventorycore.RegenerateInput{TenantID: f.seed.TenantID, OriginVersionID: draft, EnvironmentID: env.ID, ActorMembershipID: f.seed.MembershipID, ExpectedVersion: revision, RevisionIDs: []identity.ID{rev, late}, ClientMutationID: []string{"race-a", "race-b"}[i]})
			}(i)
		}
		wg.Wait()
		successes, blocked := 0, 0
		for _, err := range results {
			switch {
			case err == nil:
				successes++
			case apperror.Is(err, apperror.InvalidState) || apperror.Is(err, apperror.Conflict):
				blocked++
			default:
				t.Fatalf("unexpected error: %v", err)
			}
		}
		if successes != 1 || blocked != 1 {
			t.Fatalf("successes=%d blocked=%d (%v)", successes, blocked, results)
		}
		if jobs := f.jobsOf(key); len(jobs) != 2 {
			t.Fatalf("jobs = %+v, want the first catalog and one regeneration", jobs)
		}
	})

	t.Run("IT-232 a failed regeneration keeps the previous suggestions", func(t *testing.T) {
		f, key, photo, _, rev := cataloged(t, "Pratos")
		_, late := f.note(itNote{Key: key, Photo: photo, Text: "mais taças"})
		draft, _ := f.draftOf(key)
		before := f.itemsOfRoom(key)["Pratos"]
		f.gateway.Respond = func(llm.StructuredRequest) (string, error) { return "", errors.New("provider unavailable") }
		f.mustRegenerate(key, "regen-fail", rev, late)
		f.drain()
		job := f.latestCatalogJob(key)
		if job.Generation != 2 || job.Status != "FAILED" {
			t.Fatalf("job = %+v, want the regeneration to fail", job)
		}
		if got := f.itemsOfRoom(key)["Pratos"]; got.ID != before.ID {
			t.Fatal("a failed regeneration must keep the previous suggestions")
		}
		if state := f.draftState(draft); state != "READY" {
			t.Fatalf("draft state = %s, the draft must stay reviewable after a failed regeneration", state)
		}
	})

	t.Run("IT-233 IT-368 the same mutation is one run and a different payload conflicts", func(t *testing.T) {
		f, key, photo, _, rev := cataloged(t, "Pratos")
		_, late := f.note(itNote{Key: key, Photo: photo, Text: "mais taças"})
		first := f.mustRegenerate(key, "regen-same", rev, late)
		again, err := f.regenerate(key, "regen-same", late, rev)
		if err != nil || !again.Replayed || again.JobID != first.JobID {
			t.Fatalf("replay = %+v err=%v, want the same run", again, err)
		}
		if _, err := f.regenerate(key, "regen-same", late); !apperror.Is(err, apperror.Conflict) {
			t.Fatalf("same identifier with other data err = %v, want CONFLICT", err)
		}
		f.drain()
		replayAfterSuccess, err := f.regenerate(key, "regen-same", rev, late)
		if err != nil || !replayAfterSuccess.Replayed || replayAfterSuccess.Status != "SUCCEEDED" {
			t.Fatalf("replay after success = %+v err=%v", replayAfterSuccess, err)
		}
		if len(f.jobsOf(key)) != 2 {
			t.Fatal("a replay must not schedule another run")
		}
		f.itemsOfRoom(key) // fails the test if any suggestion is duplicated
	})

	t.Run("IT-235 a suggestion decided while the regeneration waits is preserved", func(t *testing.T) {
		f, key, photo, _, rev := cataloged(t, "Pratos", "Copos")
		_, late := f.note(itNote{Key: key, Photo: photo, Text: "seis taças"})
		draft, _ := f.draftOf(key)
		f.citeNotesOf("Pratos", "Copos", "Taças")
		f.mustRegenerate(key, "regen-race", rev, late)
		// The draft stays reviewable: the person rejects "Copos" before the run executes.
		copos := f.itemsOfRoom(key)["Copos"]
		f.decide(draft, copos, "REJECT")
		f.drain()
		items := f.itemsOfRoom(key)
		if items["Copos"].ID != copos.ID || items["Copos"].Decision != "REJECTED" {
			t.Fatalf("the rejection was not preserved: %+v", items["Copos"])
		}
		if len(items) != 3 || items["Pratos"].Decision != "PENDING" || items["Taças"].Decision != "PENDING" {
			t.Fatalf("items = %+v", items)
		}
	})

	t.Run("IT-179 a discarded note cannot be selected and the refreshed suggestions lose its support", func(t *testing.T) {
		f, key, photo, noteA, revA := cataloged(t, "Pratos")
		draft, _ := f.draftOf(key)
		decidedBefore := f.itemsOfRoom(key)["Pratos"]
		f.decide(draft, decidedBefore, "ACCEPT")
		f.setNote(noteA, "status", "CANCELED")
		_, err := f.regenerate(key, "regen-discarded", revA)
		if !apperror.Is(err, apperror.InvalidInput) {
			t.Fatalf("selecting a discarded note: err = %v, want INVALID_INPUT", err)
		}
		_, late := f.note(itNote{Key: key, Photo: photo, Text: "quatro copos"})
		f.citeNotesOf("Copos")
		f.mustRegenerate(key, "regen-after-discard", late)
		f.drain()
		requests := f.catalogRequests()
		if notes := mustVoiceData(t, requests[len(requests)-1]); len(notes) != 1 || notes[0].(map[string]any)["noteId"] == noteA.String() {
			t.Fatalf("the discarded note reached the regeneration request: %v", notes)
		}
		manifests := f.manifests()
		var excluded string
		for _, entry := range manifests[len(manifests)-1].Entries {
			if entry.NoteID == noteA {
				excluded = entry.Reason
			}
		}
		if excluded != catalognotes.ReasonNoteDiscarded {
			t.Fatalf("manifest reason for the discarded note = %q", excluded)
		}
		// The decided item keeps its recorded citation.
		if cites := f.voiceCites(f.itemsOfRoom(key)["Pratos"].ID); cites[noteA.String()] != revA {
			t.Fatalf("decided citation = %v", cites)
		}
	})

	t.Run("IT-370 a stale origin revision conflicts", func(t *testing.T) {
		f, key, photo, _, rev := cataloged(t, "Pratos")
		_, late := f.note(itNote{Key: key, Photo: photo, Text: "mais taças"})
		draft, revision := f.draftOf(key)
		_, err := f.inventory().RegenerateSuggestions(f.ctx, inventorycore.RegenerateInput{TenantID: f.seed.TenantID, OriginVersionID: draft, EnvironmentID: f.environmentOf(key).ID, ActorMembershipID: f.seed.MembershipID, ExpectedVersion: revision + 1, RevisionIDs: []identity.ID{rev, late}, ClientMutationID: "stale"})
		if !apperror.Is(err, apperror.Conflict) {
			t.Fatalf("err = %v, want CONFLICT", err)
		}
		if len(f.jobsOf(key)) != 1 {
			t.Fatal("a conflict must not schedule work")
		}
	})

	t.Run("IT-371 a discarded draft cannot be regenerated", func(t *testing.T) {
		f, key, photo, _, rev := cataloged(t, "Pratos")
		_, late := f.note(itNote{Key: key, Photo: photo, Text: "mais taças"})
		draft, _ := f.draftOf(key)
		f.within(func(tx *gorm.DB) error {
			return tx.Model(&database.OriginVersion{}).Where("tenant_id=? AND id=?", f.seed.TenantID, draft).Update("status", "DISCARDED").Error
		})
		if _, err := f.regenerate(key, "regen-discarded-draft", rev, late); !apperror.Is(err, apperror.InvalidState) {
			t.Fatalf("err = %v, want INVALID_STATE", err)
		}
	})
}

func (f *flow) latestCatalogJob(key string) database.InventoryJob {
	f.t.Helper()
	jobs := f.jobsOf(key)
	return jobs[len(jobs)-1]
}

func (f *flow) jobsOf(key string) []database.InventoryJob {
	f.t.Helper()
	var jobs []database.InventoryJob
	f.within(func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND inspection_id=? AND kind='CATALOG' AND requirement_key=?", f.seed.TenantID, f.seed.InspectionID, key).Order("generation").Find(&jobs).Error
	})
	return jobs
}

func (f *flow) draftState(draft identity.ID) string {
	f.t.Helper()
	var state string
	f.within(func(tx *gorm.DB) error {
		var version database.OriginVersion
		if err := tx.Where("tenant_id=? AND id=?", f.seed.TenantID, draft).Take(&version).Error; err != nil {
			return err
		}
		if version.DraftState != nil {
			state = *version.DraftState
		}
		return nil
	})
	return state
}
