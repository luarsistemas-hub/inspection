package list_notes

import (
	"context"
	"errors"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestSetupListsScopedPage(t *testing.T) {
	db := listDB(t)
	tenant, inspection := identity.NewID(), identity.NewID()
	for i := 0; i < 3; i++ {
		if err := db.Exec(`INSERT INTO transcription.audio_notes(tenant_id,id,inspection_id,requirement_key,created_at) VALUES(?,?,?,?,?)`, tenant, identity.NewID(), inspection, "room-1", time.Now().Add(time.Duration(i)*time.Second)).Error; err != nil {
			t.Fatal(err)
		}
	}
	result, err := Setup(context.Background(), Dependencies{DB: db, Within: listWithin(db)}, Command{TenantID: tenant, InspectionID: inspection, RequirementKey: "room-1", Limit: 2})
	if err != nil || len(result.IDs) != 2 || !result.HasNextPage {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}
func TestSetupValidatesAndPropagatesDependencyFailure(t *testing.T) {
	db := listDB(t)
	if _, err := Setup(context.Background(), Dependencies{DB: db}, Command{}); !apperror.Is(err, apperror.PaginationInvalid) {
		t.Fatalf("expected invalid pagination, got %v", err)
	}
	failure := errors.New("database unavailable")
	if _, err := Setup(context.Background(), Dependencies{DB: db, Within: func(context.Context, identity.ID, func(*gorm.DB) error) error { return failure }}, Command{TenantID: identity.NewID(), InspectionID: identity.NewID(), Limit: 10}); !errors.Is(err, failure) {
		t.Fatalf("expected dependency failure, got %v", err)
	}
}

func TestSetupOmitsStagedModelRevisionsFromNoteLists(t *testing.T) {
	db := listDB(t)
	tenant, inspection := identity.NewID(), identity.NewID()
	for _, status := range []string{"PROCESSING", "FAILED", "CANCELED"} {
		note := identity.NewID()
		if err := db.Exec(`INSERT INTO transcription.audio_notes(tenant_id,id,inspection_id,status,created_at) VALUES(?,?,?,?,?)`, tenant, note, inspection, status, time.Now()).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec(`INSERT INTO transcription.transcript_revisions(id,tenant_id,note_id,revision_number,text,kind,created_at) VALUES(?,?,?,?,?,?,?)`, identity.NewID(), tenant, note, 1, "staging", "MODEL", time.Now()).Error; err != nil {
			t.Fatal(err)
		}
	}
	result, err := Setup(context.Background(), Dependencies{DB: db, Within: listWithin(db)}, Command{TenantID: tenant, InspectionID: inspection, Limit: 10})
	if err != nil || len(result.Notes) != 3 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	for _, note := range result.Notes {
		if len(note.Revisions) != 0 {
			t.Fatalf("staging became visible for %s: %+v", note.Status, note.Revisions)
		}
	}
}
func TestSetupKeepsCurrentRevisionOfDiscardedCompletedNote(t *testing.T) {
	db := listDB(t)
	tenant, inspection, note := identity.NewID(), identity.NewID(), identity.NewID()
	current, staged, declaration := identity.NewID(), identity.NewID(), identity.NewID()
	if err := db.Exec(`INSERT INTO transcription.audio_notes(tenant_id,id,inspection_id,status,current_revision_id,created_at) VALUES(?,?,?,?,?,?)`, tenant, note, inspection, "CANCELED", current, time.Now()).Error; err != nil {
		t.Fatal(err)
	}
	for _, rev := range []struct {
		id     identity.ID
		number int
		kind   string
	}{{current, 1, "MODEL"}, {staged, 2, "MODEL"}, {declaration, 3, "PARTICIPANT_DECLARATION"}} {
		if err := db.Exec(`INSERT INTO transcription.transcript_revisions(id,tenant_id,note_id,revision_number,text,kind,created_at) VALUES(?,?,?,?,?,?,?)`, rev.id, tenant, note, rev.number, "texto", rev.kind, time.Now()).Error; err != nil {
			t.Fatal(err)
		}
	}
	result, err := Setup(context.Background(), Dependencies{DB: db, Within: listWithin(db)}, Command{TenantID: tenant, InspectionID: inspection, Limit: 10})
	if err != nil || len(result.Notes) != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	revisions := result.Notes[0].Revisions
	if len(revisions) != 2 || revisions[0].ID != current || revisions[1].ID != declaration {
		t.Fatalf("discarded note revisions=%+v", revisions)
	}
}
func listDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"transcription", "usage"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Exec(`CREATE TABLE transcription.audio_notes(tenant_id text,id text,inspection_id text,responsibility_id text,requirement_key text,target_kind text,status text,version integer,anchor_status text,audio_deletion_status text,photo_media_id text,video_attempt_id text,source_media_id text,start_ms integer,end_ms integer,duration_ms integer,highlighted boolean,highlight_source text,current_revision_id text,text_confirmed boolean,no_speech boolean,safe_failure_code text,created_at datetime,updated_at datetime)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE transcription.transcript_revisions(id text,tenant_id text,note_id text,revision_number integer,text text,kind text,author_id text,call_id text,created_at datetime)`).Error; err != nil {
		t.Fatal(err)
	}
	for _, ddl := range []string{
		`CREATE TABLE transcription.jobs(tenant_id text,id text,note_id text,generation integer,status text)`,
		`CREATE TABLE usage.llm_calls(tenant_id text,call_id text,job_id text,replay_generation integer)`,
	} {
		if err := db.Exec(ddl).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db
}
func listWithin(db *gorm.DB) func(context.Context, identity.ID, func(*gorm.DB) error) error {
	return func(ctx context.Context, _ identity.ID, fn func(*gorm.DB) error) error {
		return db.WithContext(ctx).Transaction(fn)
	}
}
