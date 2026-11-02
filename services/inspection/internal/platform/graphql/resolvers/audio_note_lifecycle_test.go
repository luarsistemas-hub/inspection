package resolvers

import (
	"context"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestAudioNoteRevisionIsImmutableOptimisticAndIdempotent(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"transcription", "inspections", "usage"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	ddl := []string{
		`CREATE TABLE inspections.inspections (tenant_id text,id text,business_unit_id text,asset_id text,project_id text)`,
		`CREATE TABLE transcription.audio_notes (tenant_id text,id text,inspection_id text,responsibility_id text,requirement_key text,target_kind text,anchor_status text,audio_deletion_status text,photo_media_id text,video_attempt_id text,source_media_id text,start_ms integer,end_ms integer,duration_ms integer,highlighted boolean,highlight_source text,text_confirmed boolean,no_speech boolean,safe_failure_code text,current_revision_id text,created_at datetime,updated_at datetime,version integer,status text)`,
		`CREATE TABLE transcription.transcript_revisions (id text,tenant_id text,note_id text,revision_number integer,text text,kind text,author_id text,call_id text,created_at datetime,PRIMARY KEY(id,tenant_id))`,
		`CREATE TABLE transcription.jobs (tenant_id text,id text,note_id text,generation integer,status text)`,
		`CREATE TABLE usage.llm_calls (tenant_id text,call_id text,job_id text,replay_generation integer)`,
		`CREATE TABLE transcription.mutation_receipts (tenant_id text,id text,note_id text,client_mutation_id text,operation text,payload_digest text,result_version integer,created_at datetime,UNIQUE(tenant_id,note_id,client_mutation_id))`,
	}
	for _, sql := range ddl {
		if err := db.Exec(sql).Error; err != nil {
			t.Fatal(err)
		}
	}
	tenantID, inspectionID, assetID, noteID, revisionID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	if err := db.Exec(`INSERT INTO inspections.inspections VALUES (?,?,?,?,NULL)`, tenantID, inspectionID, identity.NewID(), assetID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO transcription.audio_notes (tenant_id,id,inspection_id,requirement_key,target_kind,status,anchor_status,audio_deletion_status,highlighted,current_revision_id,created_at,updated_at,version) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`, tenantID, noteID, inspectionID, "room-1", "PHOTO", "COMPLETED", "VALID", "DELETED", false, revisionID, "2026-01-01", "2026-01-01", 1).Error; err != nil {
		t.Fatal(err)
	}
	// A real MODEL revision is written by the worker, linked to the llm call of
	// a COMPLETED job; the history keeps it visible after a human edit.
	jobID, callID := identity.NewID(), identity.NewID()
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO transcription.jobs (tenant_id,id,note_id,generation,status) VALUES (?,?,?,?,?)`, []any{tenantID, jobID, noteID, 1, "COMPLETED"}},
		{`INSERT INTO usage.llm_calls (tenant_id,call_id,job_id,replay_generation) VALUES (?,?,?,?)`, []any{tenantID, callID, jobID, 1}},
		{`INSERT INTO transcription.transcript_revisions VALUES (?,?,?,?,?,?,?,?,?)`, []any{revisionID, tenantID, noteID, 1, "cinco pratos", "MODEL", nil, callID.String(), "2026-01-01"}},
	} {
		if err := db.Exec(stmt.sql, stmt.args...).Error; err != nil {
			t.Fatal(err)
		}
	}
	ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{
		TenantID:  tenantID,
		Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: tenantID, Internal: true, Roles: []string{auth.Manager}, Scopes: []requestctx.Scope{{Kind: "ASSET", ID: assetID}}},
	})
	r := &Resolver{DB: db}
	first, err := mutateAudioNote(ctx, r, noteID.String(), "edit-1", "REVISE", "cinco pratos", false, 1)
	if err != nil || len(first.UserErrors) != 0 || first.Note == nil || first.Note.Version != 2 || len(first.Note.Revisions) != 2 {
		t.Fatalf("revision mutation failed: payload=%+v err=%v", first, err)
	}
	retry, err := mutateAudioNote(ctx, r, noteID.String(), "edit-1", "REVISE", "cinco pratos", false, 1)
	if err != nil || len(retry.UserErrors) != 0 || retry.Note == nil || retry.Note.Version != 2 || len(retry.Note.Revisions) != 2 {
		t.Fatalf("identical retry was not idempotent: payload=%+v err=%v", retry, err)
	}
	conflict, err := mutateAudioNote(ctx, r, noteID.String(), "edit-1", "REVISE", "seis pratos", false, 1)
	if err != nil || len(conflict.UserErrors) != 1 || conflict.UserErrors[0].Code != "CONFLICT" {
		t.Fatalf("mutation ID payload conflict not reported: payload=%+v err=%v", conflict, err)
	}
	stale, err := mutateAudioNote(ctx, r, noteID.String(), "edit-2", "REVISE", "seis pratos", false, 1)
	if err != nil || len(stale.UserErrors) != 1 || stale.UserErrors[0].Code != "CONFLICT" {
		t.Fatalf("stale version not reported: payload=%+v err=%v", stale, err)
	}
	var revisions, receipts int
	if err := db.Raw(`SELECT count(*) FROM transcription.transcript_revisions WHERE tenant_id=? AND note_id=?`, tenantID, noteID).Scan(&revisions).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Raw(`SELECT count(*) FROM transcription.mutation_receipts WHERE tenant_id=? AND note_id=?`, tenantID, noteID).Scan(&receipts).Error; err != nil {
		t.Fatal(err)
	}
	if revisions != 2 || receipts != 1 {
		t.Fatalf("unexpected durable history: revisions=%d receipts=%d", revisions, receipts)
	}
}
