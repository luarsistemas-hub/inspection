package core

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"inspection/libs/identity"
	catalognotes "inspection/services/inspection/internal/features/analysis/catalog_notes"
	"inspection/services/inspection/internal/platform/database"
)

func TestInventoryDraftWarnsAboutNotesAndRegenerations(t *testing.T) {
	db := inventoryTestDB(t)
	tenant, version, _, _ := seedReadyDraft(t, db)
	inspection := identity.NewID()
	if err := db.Model(&database.OriginVersion{}).Where("id=?", version).Update("source_inspection_id", inspection).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO inspections.inspections (id, tenant_id, status, asset_id) VALUES (?,?,'SUBMITTED',?)", inspection, tenant, identity.NewID()).Error; err != nil {
		t.Fatal(err)
	}
	warnings := func() []string {
		t.Helper()
		view, err := testService(db).InventoryDraft(context.Background(), tenant, version)
		if err != nil {
			t.Fatal(err)
		}
		return view.Warnings
	}
	if got := warnings(); len(got) != 0 {
		t.Fatalf("a draft without notes warns %v", got)
	}

	note, revision := identity.NewID(), identity.NewID()
	if err := db.Exec(`INSERT INTO transcription.audio_notes (id,tenant_id,inspection_id,requirement_key,status,anchor_status,no_speech,current_revision_id) VALUES (?,?,?,?,?,?,?,?)`, note, tenant, inspection, "env:a", "COMPLETED", "VALID", false, revision).Error; err != nil {
		t.Fatal(err)
	}
	if got := warnings(); !reflect.DeepEqual(got, []string{WarningVoiceNotIncorporated}) {
		t.Fatalf("IT-223 a note after the catalog warns %v, want VOICE_NOT_INCORPORATED", got)
	}

	manifest := func(generation int, revisionID identity.ID) {
		t.Helper()
		entries, _ := json.Marshal([]catalognotes.Entry{{NoteID: note, RevisionID: &revisionID, State: catalognotes.StateIncluded}})
		sealed := time.Now().UTC()
		if err := db.Create(&database.CatalogNoteManifest{ID: identity.NewID(), TenantID: tenant, InspectionID: inspection, RequirementKey: "env:a", CatalogGeneration: generation, NoteRevisions: entries, DeadlineAt: sealed, Digest: "d", SealedAt: &sealed, CreatedAt: sealed}).Error; err != nil {
			t.Fatal(err)
		}
	}
	manifest(1, revision)
	if got := warnings(); len(got) != 0 {
		t.Fatalf("a note the latest manifest includes warns %v", got)
	}
	corrected := identity.NewID()
	if err := db.Exec("UPDATE transcription.audio_notes SET current_revision_id=? WHERE id=?", corrected, note).Error; err != nil {
		t.Fatal(err)
	}
	if got := warnings(); !reflect.DeepEqual(got, []string{WarningVoiceNotIncorporated}) {
		t.Fatalf("IT-225 a corrected text warns %v", got)
	}
	manifest(2, corrected)
	if got := warnings(); len(got) != 0 {
		t.Fatalf("after the regeneration's manifest warns %v", got)
	}

	job := func(generation int, status string) {
		t.Helper()
		if err := db.Create(&database.InventoryJob{ID: identity.NewID(), TenantID: tenant, InspectionID: inspection, OriginVersionID: version, Kind: "CATALOG", RequirementKey: "env:a", Generation: generation, Status: status, NextAttemptAt: time.Now().UTC()}).Error; err != nil {
			t.Fatal(err)
		}
	}
	job(1, "SUCCEEDED")
	if got := warnings(); len(got) != 0 {
		t.Fatalf("the first catalog is not a regeneration: %v", got)
	}
	job(2, "PENDING")
	if got := warnings(); !reflect.DeepEqual(got, []string{WarningRegenerationRunning}) {
		t.Fatalf("a scheduled regeneration warns %v", got)
	}
	if err := db.Model(&database.InventoryJob{}).Where("generation=2").Update("status", "FAILED").Error; err != nil {
		t.Fatal(err)
	}
	if got := warnings(); !reflect.DeepEqual(got, []string{WarningRegenerationFailed}) {
		t.Fatalf("IT-232 a failed regeneration warns %v", got)
	}
	job(3, "SUCCEEDED")
	if got := warnings(); len(got) != 0 {
		t.Fatalf("a later successful run clears the failure: %v", got)
	}
}
