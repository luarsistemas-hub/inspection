package resolvers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/auth"
	graph "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/requestctx"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type audioGraphQLFixture struct {
	server     http.Handler
	db         *gorm.DB
	tenantID   identity.ID
	inspection identity.ID
	noteID     identity.ID
	revisionID identity.ID
	assetID    identity.ID
}

func newAudioGraphQLFixture(t *testing.T, status, deletion string) audioGraphQLFixture {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"transcription", "inspections", "messaging", "usage"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	ddl := []string{
		`CREATE TABLE transcription.tenant_policies (tenant_id text PRIMARY KEY,enabled boolean,tenant_note_limit integer,tenant_audio_limit_ms integer,inspection_note_limit integer,inspection_audio_limit_ms integer,version integer,created_at datetime,updated_at datetime)`,
		`CREATE TABLE transcription.policy_mutation_receipts (tenant_id text,client_mutation_id text,payload_digest text,enabled boolean,tenant_note_limit integer,tenant_audio_limit_ms integer,inspection_note_limit integer,inspection_audio_limit_ms integer,result_version integer,created_at datetime,PRIMARY KEY(tenant_id,client_mutation_id))`,
		`CREATE TABLE inspections.inspections (id text,tenant_id text,business_unit_id text,asset_id text,project_id text)`,
		`CREATE TABLE transcription.audio_notes (tenant_id text,id text,inspection_id text,responsibility_id text,requirement_key text,target_kind text,anchor_status text,audio_deletion_status text,photo_media_id text,video_attempt_id text,source_media_id text,start_ms integer,end_ms integer,duration_ms integer,highlighted boolean,highlight_source text,text_confirmed boolean,no_speech boolean,safe_failure_code text,current_revision_id text,created_at datetime,updated_at datetime,version integer,status text,upload_key text,upload_id text)`,
		`CREATE TABLE transcription.transcript_revisions (id text,tenant_id text,note_id text,revision_number integer,text text,kind text,author_id text,call_id text,created_at datetime,PRIMARY KEY(id,tenant_id))`,
		`CREATE TABLE usage.llm_calls (tenant_id text,call_id text,job_id text,replay_generation integer)`,
		`CREATE TABLE transcription.mutation_receipts (tenant_id text,id text,note_id text,client_mutation_id text,operation text,payload_digest text,result_version integer,created_at datetime,UNIQUE(tenant_id,note_id,client_mutation_id))`,
		`CREATE TABLE transcription.jobs (tenant_id text,id text,note_id text,generation integer,profile_key text,profile_version integer,profile_digest text,status text,attempts integer,created_at datetime,updated_at datetime,UNIQUE(tenant_id,note_id,generation))`,
		`CREATE TABLE transcription.audio_deletions (tenant_id text,id text,note_id text,object_generation integer,object_key text,derivative_keys blob,status text,created_at datetime,updated_at datetime)`,
		`CREATE TABLE messaging.outbox (id text,tenant_id text,type text,schema_version integer,payload blob,correlation_id text,causation_id text,status text,attempts integer,next_attempt_at datetime,claimed_at datetime,last_error text,published_at datetime,created_at datetime)`,
	}
	for _, sql := range ddl {
		if err := db.Exec(sql).Error; err != nil {
			t.Fatal(err)
		}
	}
	tenantID, inspectionID, noteID, revisionID, assetID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	if err := db.Exec(`INSERT INTO inspections.inspections VALUES (?,?,?,?,NULL)`, inspectionID, tenantID, identity.NewID(), assetID).Error; err != nil {
		t.Fatal(err)
	}
	responsibilityID := identity.NewID()
	now := time.Now().UTC()
	if status == "FAILED" {
		if err := db.Exec(`INSERT INTO transcription.tenant_policies VALUES(?,?,?,?,?,?,?,?,?)`, tenantID, true, 100, 300000, 20, 60000, 1, now, now).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec(`INSERT INTO transcription.jobs(tenant_id,id,note_id,generation,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, tenantID, identity.NewID(), noteID, 1, "FAILED", now.Add(-time.Hour), now.Add(-time.Hour)).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Exec(`INSERT INTO transcription.audio_notes (tenant_id,id,inspection_id,responsibility_id,requirement_key,target_kind,anchor_status,audio_deletion_status,duration_ms,highlighted,text_confirmed,current_revision_id,created_at,updated_at,version,status) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, tenantID, noteID, inspectionID, responsibilityID, "room-1", "PHOTO", "VALID", deletion, 2000, false, false, revisionID, time.Now(), time.Now(), 1, status).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO transcription.transcript_revisions VALUES (?,?,?,?,?,?,?,?,?)`, revisionID, tenantID, noteID, 1, "cinco pratos", "MODEL", nil, nil, "2026-01-01").Error; err != nil {
		t.Fatal(err)
	}
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{DB: db}}))
	server.AddTransport(transport.POST{})
	server.SetErrorPresenter(graph.PresentError)
	return audioGraphQLFixture{server: server, db: db, tenantID: tenantID, inspection: inspectionID, noteID: noteID, revisionID: revisionID, assetID: assetID}
}

func (f audioGraphQLFixture) execute(t *testing.T, query string, roles ...string) map[string]any {
	t.Helper()
	body, err := json.Marshal(map[string]string{"query": query})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/graphql", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if len(roles) > 0 && roles[0] != "" {
		ctx := requestctx.WithMetadata(req.Context(), requestctx.Metadata{TenantID: f.tenantID, Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: f.tenantID, Internal: true, Roles: roles}})
		req = req.WithContext(ctx)
	}
	response := httptest.NewRecorder()
	f.server.ServeHTTP(response, req)
	var result map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode GraphQL response %q: %v", response.Body.String(), err)
	}
	return result
}

func TestVoicePolicyGraphQLTransportMatrixIT321To327(t *testing.T) {
	tests := []struct{ id, query, role, want string }{
		{"IT-321 voicePolicy success", `query { voicePolicy { enabled tenantNoteLimit tenantAudioLimitMs inspectionNoteLimit inspectionAudioLimitMs version } }`, auth.TenantAdmin, ""},
		{"IT-322 voicePolicy unauthenticated", `query { voicePolicy { enabled version } }`, "", "UNAUTHENTICATED"},
		{"IT-323 voicePolicy forbidden", `query { voicePolicy { enabled version } }`, auth.CustomerViewer, "FORBIDDEN"},
		{"IT-324 configure success", `mutation { configureVoicePolicy(input:{enabled:true,tenantNoteLimit:100,tenantAudioLimitMs:300000,inspectionNoteLimit:20,inspectionAudioLimitMs:30000,expectedVersion:0,clientMutationId:"policy-ok"}) { policy { enabled tenantNoteLimit tenantAudioLimitMs inspectionNoteLimit inspectionAudioLimitMs version } userErrors { code message } clientMutationId } }`, auth.TenantAdmin, ""},
		{"IT-325 configure invalid", `mutation { configureVoicePolicy(input:{enabled:true,tenantNoteLimit:0,tenantAudioLimitMs:300000,inspectionNoteLimit:20,inspectionAudioLimitMs:30000,expectedVersion:0,clientMutationId:"policy-invalid"}) { policy { enabled } userErrors { code message } } }`, auth.TenantAdmin, "INVALID_INPUT"},
		{"IT-326 configure conflict", `mutation { configureVoicePolicy(input:{enabled:true,tenantNoteLimit:100,tenantAudioLimitMs:300000,inspectionNoteLimit:20,inspectionAudioLimitMs:30000,expectedVersion:1,clientMutationId:"policy-conflict"}) { userErrors { code message } } }`, auth.TenantAdmin, "CONFLICT"},
		{"IT-327 configure forbidden", `mutation { configureVoicePolicy(input:{enabled:true,tenantNoteLimit:100,tenantAudioLimitMs:300000,inspectionNoteLimit:20,inspectionAudioLimitMs:30000,expectedVersion:0,clientMutationId:"policy-forbidden"}) { userErrors { code message } } }`, auth.CustomerViewer, "FORBIDDEN"},
	}
	for _, tc := range tests {
		t.Run(tc.id, func(t *testing.T) {
			fx := newAudioGraphQLFixture(t, "COMPLETED", "DELETED")
			if tc.id == "IT-321 voicePolicy success" {
				now := time.Now().UTC()
				if err := fx.db.Exec(`INSERT INTO transcription.tenant_policies VALUES(?,?,?,?,?,?,?,?,?)`, fx.tenantID, true, 100, 300000, 20, 30000, 2, now, now).Error; err != nil {
					t.Fatal(err)
				}
			}
			before := audioGraphQLSideEffectSnapshot(t, fx.db, fx.tenantID)
			got := fx.execute(t, tc.query, tc.role)
			assertAudioGraphQLResult(t, got, tc.want)
			if tc.want == "" {
				assertAudioGraphQLSuccessPayload(t, tc.id, got, fx)
			} else if after := audioGraphQLSideEffectSnapshot(t, fx.db, fx.tenantID); !reflect.DeepEqual(before, after) {
				t.Fatalf("%s left side effects: before=%v after=%v", tc.id, before, after)
			}
		})
	}
}

func TestAudioNoteGraphQLReadAndLifecycleMatrixIT342To367(t *testing.T) {
	tests := []struct{ id, status, deletion, role, query, want string }{
		{"IT-342 read success", "COMPLETED", "DELETED", auth.TenantAdmin, `query { audioNote(id:"%s") { id inspectionId requirementKey status revisions { revisionNumber text kind } } }`, ""},
		{"IT-343 read forbidden", "COMPLETED", "DELETED", auth.CustomerViewer, `query { audioNote(id:"%s") { id } }`, "FORBIDDEN"},
		{"IT-344 cross-tenant note absent", "COMPLETED", "DELETED", auth.TenantAdmin, `query { audioNote(id:"%s") { id } }`, "NOT_FOUND"},
		{"IT-345 pagination invalid", "COMPLETED", "DELETED", auth.TenantAdmin, `query { audioNotes(inspectionId:"%s",environmentId:"room-1",first:-1) { nodes { id } } }`, "PAGINATION_INVALID"},
		{"IT-346 revise success", "COMPLETED", "DELETED", auth.TenantAdmin, `mutation { reviseAudioNote(input:{noteId:"%s",text:"seis pratos",expectedVersion:1,clientMutationId:"revise-ok"}) { note { id version revisions { text } } userErrors { code message } clientMutationId } }`, ""},
		{"IT-347 revise invalid", "COMPLETED", "DELETED", auth.TenantAdmin, `mutation { reviseAudioNote(input:{noteId:"%s",text:"",expectedVersion:1,clientMutationId:"revise-invalid"}) { userErrors { code message } } }`, "INVALID_INPUT"},
		{"IT-348 revise conflict", "COMPLETED", "DELETED", auth.TenantAdmin, `mutation { reviseAudioNote(input:{noteId:"%s",text:"seis",expectedVersion:1,clientMutationId:"revise-conflict"}) { userErrors { code message } } }`, "CONFLICT"},
		{"IT-349 revise invalid state", "CANCELED", "DELETED", auth.TenantAdmin, `mutation { reviseAudioNote(input:{noteId:"%s",text:"seis",expectedVersion:1,clientMutationId:"revise-state"}) { userErrors { code message } } }`, "INVALID_STATE"},
		{"IT-350 revise forbidden", "COMPLETED", "DELETED", auth.CustomerViewer, `mutation { reviseAudioNote(input:{noteId:"%s",text:"seis",expectedVersion:1,clientMutationId:"revise-forbidden"}) { userErrors { code message } } }`, "FORBIDDEN"},
		{"IT-351 discard success", "COMPLETED", "DELETED", auth.TenantAdmin, `mutation { discardAudioNote(input:{noteId:"%s",expectedVersion:1,clientMutationId:"discard-ok"}) { note { status version } userErrors { code message } clientMutationId } }`, ""},
		{"IT-352 discard conflict", "COMPLETED", "DELETED", auth.TenantAdmin, `mutation { discardAudioNote(input:{noteId:"%s",expectedVersion:1,clientMutationId:"discard-conflict"}) { userErrors { code message } } }`, "CONFLICT"},
		{"IT-353 discard invalid state", "CANCELED", "DELETED", auth.TenantAdmin, `mutation { discardAudioNote(input:{noteId:"%s",expectedVersion:1,clientMutationId:"discard-state"}) { userErrors { code message } } }`, "INVALID_STATE"},
		{"IT-354 discard forbidden", "COMPLETED", "DELETED", auth.CustomerViewer, `mutation { discardAudioNote(input:{noteId:"%s",expectedVersion:1,clientMutationId:"discard-forbidden"}) { userErrors { code message } } }`, "FORBIDDEN"},
		{"IT-355 retry success", "FAILED", "PENDING", auth.TenantAdmin, `mutation { retryAudioNote(input:{noteId:"%s",expectedVersion:1,clientMutationId:"retry-ok"}) { note { status version } userErrors { code message } clientMutationId } }`, ""},
		{"IT-356 retry unavailable", "COMPLETED", "DELETED", auth.TenantAdmin, `mutation { retryAudioNote(input:{noteId:"%s",expectedVersion:1,clientMutationId:"retry-unavailable"}) { userErrors { code message } } }`, "RETRY_UNAVAILABLE"},
		{"IT-357 retry expired", "FAILED", "EXPIRED", auth.TenantAdmin, `mutation { retryAudioNote(input:{noteId:"%s",expectedVersion:1,clientMutationId:"retry-expired"}) { userErrors { code message } } }`, "EXPIRED"},
		{"IT-368 retry window expired", "FAILED", "PENDING", auth.TenantAdmin, `mutation { retryAudioNote(input:{noteId:"%s",expectedVersion:1,clientMutationId:"retry-window-expired"}) { userErrors { code message } } }`, "RETRY_UNAVAILABLE"},
		{"IT-358 retry conflict", "FAILED", "PENDING", auth.TenantAdmin, `mutation { retryAudioNote(input:{noteId:"%s",expectedVersion:1,clientMutationId:"retry-conflict"}) { userErrors { code message } } }`, "CONFLICT"},
		{"IT-359 retry forbidden", "FAILED", "PENDING", auth.CustomerViewer, `mutation { retryAudioNote(input:{noteId:"%s",expectedVersion:1,clientMutationId:"retry-forbidden"}) { userErrors { code message } } }`, "FORBIDDEN"},
		{"IT-360 highlight success", "COMPLETED", "DELETED", auth.TenantAdmin, `mutation { setAudioNoteHighlight(input:{noteId:"%s",highlighted:true,expectedVersion:1,clientMutationId:"highlight-ok"}) { note { highlighted version } userErrors { code message } clientMutationId } }`, ""},
		{"IT-361 highlight conflict", "COMPLETED", "DELETED", auth.TenantAdmin, `mutation { setAudioNoteHighlight(input:{noteId:"%s",highlighted:true,expectedVersion:1,clientMutationId:"highlight-conflict"}) { userErrors { code message } } }`, "CONFLICT"},
		{"IT-362 highlight invalid state", "CANCELED", "DELETED", auth.TenantAdmin, `mutation { setAudioNoteHighlight(input:{noteId:"%s",highlighted:true,expectedVersion:1,clientMutationId:"highlight-state"}) { userErrors { code message } } }`, "INVALID_STATE"},
		{"IT-363 highlight forbidden", "COMPLETED", "DELETED", auth.CustomerViewer, `mutation { setAudioNoteHighlight(input:{noteId:"%s",highlighted:true,expectedVersion:1,clientMutationId:"highlight-forbidden"}) { userErrors { code message } } }`, "FORBIDDEN"},
		{"IT-364 confirm success", "COMPLETED", "DELETED", auth.TenantAdmin, `mutation { confirmAudioNoteText(input:{noteId:"%s",expectedVersion:1,clientMutationId:"confirm-ok"}) { note { version textConfirmed } userErrors { code message } clientMutationId } }`, ""},
		{"IT-365 confirm conflict", "COMPLETED", "DELETED", auth.TenantAdmin, `mutation { confirmAudioNoteText(input:{noteId:"%s",expectedVersion:1,clientMutationId:"confirm-conflict"}) { userErrors { code message } } }`, "CONFLICT"},
		{"IT-366 confirm invalid state", "QUEUED", "PENDING", auth.TenantAdmin, `mutation { confirmAudioNoteText(input:{noteId:"%s",expectedVersion:1,clientMutationId:"confirm-state"}) { userErrors { code message } } }`, "INVALID_STATE"},
		{"IT-367 confirm forbidden", "COMPLETED", "DELETED", auth.CustomerViewer, `mutation { confirmAudioNoteText(input:{noteId:"%s",expectedVersion:1,clientMutationId:"confirm-forbidden"}) { userErrors { code message } } }`, "FORBIDDEN"},
	}
	for _, tc := range tests {
		t.Run(tc.id, func(t *testing.T) {
			fx := newAudioGraphQLFixture(t, tc.status, tc.deletion)
			if tc.id == "IT-368 retry window expired" {
				if err := fx.db.Exec(`UPDATE transcription.jobs SET created_at=? WHERE tenant_id=? AND note_id=? AND generation=1`, time.Now().UTC().Add(-24*time.Hour-time.Second), fx.tenantID, fx.noteID).Error; err != nil {
					t.Fatal(err)
				}
			}
			query := tc.query
			if tc.id == "IT-345 pagination invalid" {
				query = `query { audioNotes(inspectionId:"` + fx.inspection.String() + `",environmentId:"room-1",first:-1) { nodes { id } } }`
			} else if tc.id == "IT-344 cross-tenant note absent" {
				query = fmt.Sprintf(tc.query, identity.NewID())
			} else {
				query = fmt.Sprintf(tc.query, fx.noteID.String())
			}
			if tc.id == "IT-326 configure conflict" {
				now := time.Now().UTC()
				if err := fx.db.Exec(`INSERT INTO transcription.tenant_policies VALUES(?,?,?,?,?,?,?,?,?)`, fx.tenantID, true, 100, 300000, 20, 30000, 2, now, now).Error; err != nil {
					t.Fatal(err)
				}
			}
			if tc.want == "CONFLICT" {
				if err := fx.db.Exec(`UPDATE transcription.audio_notes SET version=2 WHERE tenant_id=? AND id=?`, fx.tenantID, fx.noteID).Error; err != nil {
					t.Fatal(err)
				}
			}
			before := audioGraphQLSideEffectSnapshot(t, fx.db, fx.tenantID)
			got := fx.execute(t, query, tc.role)
			assertAudioGraphQLResult(t, got, tc.want)
			if tc.want == "" {
				assertAudioGraphQLSuccessPayload(t, tc.id, got, fx)
			} else if after := audioGraphQLSideEffectSnapshot(t, fx.db, fx.tenantID); !reflect.DeepEqual(before, after) {
				t.Fatalf("%s left side effects: before=%v after=%v", tc.id, before, after)
			}
		})
	}
}

func audioGraphQLSideEffectSnapshot(t *testing.T, db *gorm.DB, tenant identity.ID) []string {
	t.Helper()
	checks := []string{
		`SELECT count(*) FROM transcription.tenant_policies WHERE tenant_id=?`,
		`SELECT count(*) FROM transcription.policy_mutation_receipts WHERE tenant_id=?`,
		`SELECT count(*) FROM transcription.audio_notes WHERE tenant_id=?`,
		`SELECT count(*) FROM transcription.transcript_revisions WHERE tenant_id=?`,
		`SELECT count(*) FROM transcription.mutation_receipts WHERE tenant_id=?`,
		`SELECT count(*) FROM transcription.jobs WHERE tenant_id=?`,
		`SELECT count(*) FROM transcription.audio_deletions WHERE tenant_id=?`,
		`SELECT count(*) FROM messaging.outbox WHERE tenant_id=?`,
	}
	snapshot := make([]string, 0, len(checks)+1)
	for _, query := range checks {
		var count int
		if err := db.Raw(query, tenant).Scan(&count).Error; err != nil {
			t.Fatal(err)
		}
		snapshot = append(snapshot, fmt.Sprintf("%d", count))
	}
	var notes []struct {
		ID            string
		Status        string
		Version       int
		Highlighted   bool
		TextConfirmed bool
	}
	if err := db.Raw(`SELECT id,status,version,highlighted,text_confirmed FROM transcription.audio_notes WHERE tenant_id=? ORDER BY id`, tenant).Scan(&notes).Error; err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(notes)
	if err != nil {
		t.Fatal(err)
	}
	snapshot = append(snapshot, string(encoded))
	return snapshot
}

func assertAudioGraphQLSuccessPayload(t *testing.T, caseID string, result map[string]any, fx audioGraphQLFixture) {
	t.Helper()
	data := result["data"].(map[string]any)
	var payload map[string]any
	var mutationID string
	switch caseID {
	case "IT-321 voicePolicy success":
		payload = data["voicePolicy"].(map[string]any)
		if payload["enabled"] != true || payload["tenantNoteLimit"] != float64(100) || payload["tenantAudioLimitMs"] != float64(300000) || payload["inspectionNoteLimit"] != float64(20) || payload["inspectionAudioLimitMs"] != float64(30000) {
			t.Fatalf("voice policy response did not contain tenant A policy: %#v", payload)
		}
		return
	case "IT-324 configure success":
		payload = data["configureVoicePolicy"].(map[string]any)
		mutationID = "policy-ok"
		policy := payload["policy"].(map[string]any)
		if policy["enabled"] != true || policy["version"] != float64(1) || policy["tenantNoteLimit"] != float64(100) {
			t.Fatalf("configured policy response=%#v", policy)
		}
		if payload["clientMutationId"] != mutationID {
			t.Fatalf("IT-324 clientMutationId=%#v, want %q", payload["clientMutationId"], mutationID)
		}
		return
	case "IT-342 read success":
		payload = data["audioNote"].(map[string]any)
		if payload["id"] != fx.noteID.String() || payload["inspectionId"] != fx.inspection.String() || payload["requirementKey"] != "room-1" || payload["status"] != "COMPLETED" {
			t.Fatalf("audioNote response was not scoped to tenant A: %#v", payload)
		}
		return
	case "IT-346 revise success":
		payload = data["reviseAudioNote"].(map[string]any)
		mutationID = "revise-ok"
	case "IT-351 discard success":
		payload = data["discardAudioNote"].(map[string]any)
		mutationID = "discard-ok"
	case "IT-355 retry success":
		payload = data["retryAudioNote"].(map[string]any)
		mutationID = "retry-ok"
	case "IT-360 highlight success":
		payload = data["setAudioNoteHighlight"].(map[string]any)
		mutationID = "highlight-ok"
	case "IT-364 confirm success":
		payload = data["confirmAudioNoteText"].(map[string]any)
		mutationID = "confirm-ok"
	default:
		return
	}
	if payload["clientMutationId"] != mutationID {
		t.Fatalf("%s clientMutationId=%#v, want %q", caseID, payload["clientMutationId"], mutationID)
	}
	note, ok := payload["note"].(map[string]any)
	if !ok {
		t.Fatalf("%s omitted note payload: %#v", caseID, payload)
	}
	if id, exists := note["id"]; exists && id != fx.noteID.String() {
		t.Fatalf("%s returned foreign note ID: %#v", caseID, id)
	}
	switch caseID {
	case "IT-346 revise success":
		if note["version"] != float64(2) || !strings.Contains(fmt.Sprint(note["revisions"]), "seis pratos") {
			t.Fatalf("revision response content=%#v", note)
		}
	case "IT-351 discard success":
		if note["status"] != "CANCELED" {
			t.Fatalf("discard response status=%#v", note["status"])
		}
	case "IT-355 retry success":
		if note["status"] != "QUEUED" {
			t.Fatalf("retry response status=%#v", note["status"])
		}
	case "IT-360 highlight success":
		if note["highlighted"] != true {
			t.Fatalf("highlight response=%#v", note)
		}
	case "IT-364 confirm success":
		if note["textConfirmed"] != true || note["version"] != float64(2) {
			t.Fatalf("confirmation response=%#v", note)
		}
	}
}

func assertAudioGraphQLResult(t *testing.T, result map[string]any, code string) {
	t.Helper()
	if code == "" {
		if result["errors"] != nil {
			t.Fatalf("unexpected GraphQL errors: %#v", result["errors"])
		}
		data, ok := result["data"].(map[string]any)
		if !ok {
			t.Fatalf("missing GraphQL data: %#v", result)
		}
		for operation, payload := range data {
			if object, ok := payload.(map[string]any); ok {
				if errs, ok := object["userErrors"].([]any); ok && len(errs) > 0 {
					t.Fatalf("unexpected %s user errors: %#v", operation, errs)
				}
			}
		}
		return
	}
	data, _ := result["data"].(map[string]any)
	if errorsRaw, ok := dataError(result); ok {
		for _, entry := range errorsRaw {
			if ext, ok := entry["extensions"].(map[string]any); ok && ext["code"] == code {
				message, ok := entry["message"].(string)
				if !ok {
					t.Fatalf("GraphQL error %s omitted message: %#v", code, entry)
				}
				assertAudioPortugueseMessage(t, message)
				return
			}
		}
	}
	for _, payload := range data {
		if object, ok := payload.(map[string]any); ok {
			if errs, ok := object["userErrors"].([]any); ok {
				for _, value := range errs {
					if item, ok := value.(map[string]any); ok && item["code"] == code {
						message, ok := item["message"].(string)
						if !ok {
							t.Fatalf("user error %s omitted message: %#v", code, item)
						}
						assertAudioPortugueseMessage(t, message)
						return
					}
				}
			}
		}
	}
	t.Fatalf("expected error code %s, got %#v", code, result)
}

func assertAudioPortugueseMessage(t *testing.T, message string) {
	t.Helper()
	if message == "" {
		t.Fatal("safe user error message is empty")
	}
	if !strings.ContainsAny(message, "áàâãéêíóôõúçÁÀÂÃÉÊÍÓÔÕÚÇ") && !strings.Contains(strings.ToLower(message), "acesso negado") && !strings.Contains(strings.ToLower(message), "limite de uso") && !strings.Contains(strings.ToLower(message), "limites de uso") && !strings.Contains(strings.ToLower(message), "atualize") {
		t.Fatalf("user error does not look like Portuguese text: %q", message)
	}
	for _, technical := range []string{"provider", "database", "request", "unauthorized", "forbidden", "not found", "conflict", "expired", "limit reached", "exception", "internal server", "token", "session"} {
		if strings.Contains(strings.ToLower(message), technical) {
			t.Fatalf("user error contains technical or English text %q: %q", technical, message)
		}
	}
}

func dataError(result map[string]any) ([]map[string]any, bool) {
	raw, ok := result["errors"].([]any)
	if !ok {
		return nil, false
	}
	out := make([]map[string]any, 0, len(raw))
	for _, entry := range raw {
		if object, ok := entry.(map[string]any); ok {
			out = append(out, object)
		}
	}
	return out, len(out) > 0
}
