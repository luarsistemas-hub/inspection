//go:build integration

package process_inventory_job

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"inspection/libs/identity"
	inventorycore "inspection/services/inspection/internal/features/inventory/core"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

// heldService runs the service inside a tenant transaction that, after the
// operation succeeded, announces it and waits before committing: the locks and
// the uncommitted rows of the operation stay in place while the test races it.
type heldService struct {
	service  inventorycore.Service
	reached  chan struct{}
	release  chan struct{}
	finished chan error
}

func (f *flow) held() *heldService {
	h := &heldService{reached: make(chan struct{}), release: make(chan struct{}), finished: make(chan error, 1)}
	h.service = f.inventory()
	h.service.Within = func(ctx context.Context, tenant identity.ID, fn func(*gorm.DB) error) error {
		return f.h.WithinTenant(ctx, tenant, func(tx *gorm.DB) error {
			if err := fn(tx); err != nil {
				return err
			}
			close(h.reached)
			<-h.release
			return nil
		})
	}
	return h
}

// waitForLockWaiter blocks until some session of the test database waits for a lock.
func (f *flow) waitForLockWaiter() {
	f.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		var waiting int64
		if err := f.h.AdminDB.Raw("SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock'").Scan(&waiting).Error; err != nil {
			f.t.Fatal(err)
		}
		if waiting > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	f.t.Fatal("no session ever waited for a lock")
}

func TestHumanDecisionRacingTheCatalogWriteKeepsEvidenceAndSources(t *testing.T) {
	f, key, photo, _, rev := cataloged(t, "Pratos", "Copos")
	draft, _ := f.draftOf(key)
	_, late := f.note(itNote{Key: key, Photo: photo, Text: "seis taças"})
	f.citeNotesOf("Pratos", "Copos", "Taças")
	f.mustRegenerate(key, "regen-race-lock", rev, late)
	pratos := f.itemsOfRoom(key)["Pratos"]
	before := len(f.sourcesOf(pratos.ID))
	var evidenceBefore int64
	f.within(func(tx *gorm.DB) error {
		return tx.Model(&database.InventoryItemEvidence{}).Where("tenant_id=? AND item_id=?", f.seed.TenantID, pratos.ID).Count(&evidenceBefore).Error
	})
	if before == 0 || evidenceBefore == 0 {
		t.Fatalf("fixture: sources=%d evidence=%d", before, evidenceBefore)
	}

	// The human decision holds the draft locks, uncommitted, while the catalog job runs.
	decision := f.held()
	go func() {
		_, err := decision.service.Decide(context.Background(), inventorycore.DecideInput{TenantID: f.seed.TenantID, OriginVersionID: draft, ActorMembershipID: f.seed.MembershipID, Decisions: []inventorycore.Decision{{ItemID: pratos.ID, Action: "ACCEPT", ExpectedVersion: pratos.Version}}})
		decision.finished <- err
	}()
	<-decision.reached
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		// The database is shared with other tests' leftovers: run due jobs until
		// the regeneration of this room has been processed.
		for i := 0; i < 100; i++ {
			if _, err := f.executor.RunDue(context.Background()); err != nil {
				t.Errorf("executor: %v", err)
				return
			}
			var job database.InventoryJob
			if err := f.h.AdminDB.Where("tenant_id=? AND requirement_key=? AND generation=2", f.seed.TenantID, key).Take(&job).Error; err != nil {
				t.Errorf("regeneration job: %v", err)
				return
			}
			if job.Status != "PENDING" && job.Status != "RUNNING" {
				return
			}
		}
		t.Error("the regeneration job never finished")
	}()
	f.waitForLockWaiter() // the job's write is serialized behind the decision
	close(decision.release)
	if err := <-decision.finished; err != nil {
		t.Fatal(err)
	}
	<-drained

	items := f.itemsOfRoom(key)
	if items["Pratos"].ID != pratos.ID || items["Pratos"].Decision != "ACCEPTED" {
		t.Fatalf("the decided item was replaced: %+v", items["Pratos"])
	}
	var evidenceAfter int64
	f.within(func(tx *gorm.DB) error {
		return tx.Model(&database.InventoryItemEvidence{}).Where("tenant_id=? AND item_id=?", f.seed.TenantID, pratos.ID).Count(&evidenceAfter).Error
	})
	kinds := map[string]int{}
	for _, row := range f.sourcesOf(pratos.ID) {
		kinds[row.SourceKind]++
	}
	if evidenceAfter != evidenceBefore || kinds["VISUAL"] == 0 || kinds["VOICE"] == 0 || kinds["HUMAN_REVIEW"] == 0 {
		t.Fatalf("the decided item lost evidence or sources: evidence %d->%d sources=%v", evidenceBefore, evidenceAfter, kinds)
	}
	if job := f.latestCatalogJob(key); job.Generation != 2 || job.Status != "SUCCEEDED" {
		t.Fatalf("job = %+v", job)
	}
}

func TestEditedItemIsNotProposedAgainByARegeneration(t *testing.T) {
	f, key, photo, _, rev := cataloged(t, "Pratos")
	draft, _ := f.draftOf(key)
	pratos := f.itemsOfRoom(key)["Pratos"]
	renamed, kind := "Aparelho de jantar", "TABLE"
	if _, err := f.inventory().Decide(f.ctx, inventorycore.DecideInput{TenantID: f.seed.TenantID, OriginVersionID: draft, ActorMembershipID: f.seed.MembershipID, Decisions: []inventorycore.Decision{{ItemID: pratos.ID, Action: "EDIT", ExpectedVersion: pratos.Version, Patch: &inventorycore.ItemPatch{Name: &renamed, TypeCode: &kind}}}}); err != nil {
		t.Fatal(err)
	}
	_, late := f.note(itNote{Key: key, Photo: photo, Text: "seis taças"})
	f.citeNotesOf("Pratos", "Taças")
	f.mustRegenerate(key, "regen-edited", rev, late)
	f.drain()
	items := f.itemsOfRoom(key)
	if _, again := items["Pratos"]; again {
		t.Fatalf("the edited item was proposed again under its original name: %+v", items)
	}
	if items[renamed].ID != pratos.ID || items[renamed].Decision != "EDITED" || items["Taças"].Decision != "PENDING" || len(items) != 2 {
		t.Fatalf("items = %+v, want the edited item untouched and one fresh suggestion", items)
	}
}

func TestConcurrentRetryOfTheSameRegenerationIsAReplay(t *testing.T) {
	f, key, photo, _, rev := cataloged(t, "Pratos")
	_, late := f.note(itNote{Key: key, Photo: photo, Text: "mais taças"})
	draft, revision := f.draftOf(key)
	env := f.environmentOf(key)
	input := inventorycore.RegenerateInput{TenantID: f.seed.TenantID, OriginVersionID: draft, EnvironmentID: env.ID, ActorMembershipID: f.seed.MembershipID, ExpectedVersion: revision, RevisionIDs: []identity.ID{rev, late}, ClientMutationID: "retry-race"}

	first := f.held() // the first attempt holds its locks and its uncommitted run
	go func() {
		_, err := first.service.RegenerateSuggestions(context.Background(), input)
		first.finished <- err
	}()
	<-first.reached
	type outcome struct {
		result inventorycore.RegenerateResult
		err    error
	}
	retried := make(chan outcome, 1)
	go func() {
		result, err := f.inventory().RegenerateSuggestions(context.Background(), input)
		retried <- outcome{result, err}
	}()
	f.waitForLockWaiter()
	close(first.release)
	if err := <-first.finished; err != nil {
		t.Fatal(err)
	}
	got := <-retried
	if got.err != nil || !got.result.Replayed {
		t.Fatalf("the concurrent retry = %+v err=%v, want a replay of the first run", got.result, got.err)
	}
	if jobs := f.jobsOf(key); len(jobs) != 2 || jobs[1].ID != got.result.JobID {
		t.Fatalf("jobs = %+v", jobs)
	}
}

func TestRegenerationAuditKeepsWhatItReplaces(t *testing.T) {
	f, key, photo, _, rev := cataloged(t, "Pratos", "Copos")
	_, late := f.note(itNote{Key: key, Photo: photo, Text: "mais taças"})
	draft, _ := f.draftOf(key)
	f.mustRegenerate(key, "regen-audit", rev, late)
	var trail database.InventoryDecision
	f.within(func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND origin_version_id=? AND action='REGENERATE'", f.seed.TenantID, draft).Take(&trail).Error
	})
	var before struct {
		Replaced []struct {
			ItemID  identity.ID `json:"itemId"`
			Name    string      `json:"name"`
			Sources []struct {
				Kind       string       `json:"kind"`
				Attribute  string       `json:"attribute"`
				NoteID     *identity.ID `json:"noteId"`
				RevisionID *identity.ID `json:"revisionId"`
			} `json:"sources"`
		} `json:"replacedSuggestions"`
	}
	if err := jsonUnmarshal(trail.Before, &before); err != nil || len(before.Replaced) != 2 {
		t.Fatalf("audit before = %s err=%v", trail.Before, err)
	}
	for _, item := range before.Replaced {
		voice := 0
		for _, source := range item.Sources {
			if source.Kind == "VOICE" && source.NoteID != nil && source.RevisionID != nil {
				voice++
			}
		}
		if item.ItemID == (identity.ID{}) || item.Name == "" || voice == 0 {
			t.Fatalf("audit item = %+v, want id, name and the voice citations that go away", item)
		}
	}
}

func jsonUnmarshal(raw []byte, out any) error { return json.Unmarshal(raw, out) }
