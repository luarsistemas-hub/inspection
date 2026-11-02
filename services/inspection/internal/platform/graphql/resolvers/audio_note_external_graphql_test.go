package resolvers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"inspection/libs/identity"
	invitationcore "inspection/services/inspection/internal/features/invitations/core"
	"inspection/services/inspection/internal/features/transcription/revise_note"
	"inspection/services/inspection/internal/platform/auth"
	graph "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/security"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type audioObjectClient struct {
	mu          sync.Mutex
	data        []byte
	typeName    string
	createErr   error
	completeErr error
}

func (c *audioObjectClient) CreateMultipart(_ context.Context, _, _, contentType string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.createErr != nil {
		return "", c.createErr
	}
	c.typeName = contentType
	return "audio-upload", nil
}
func (c *audioObjectClient) PresignPart(context.Context, string, string, string, int, time.Duration) (string, error) {
	return "https://upload.invalid/part", nil
}
func (c *audioObjectClient) CompleteMultipart(_ context.Context, _, _, _ string, parts []objectstore.Part) error {
	if c.completeErr != nil {
		return c.completeErr
	}
	if len(parts) != 1 || parts[0].ETag != "provider-md5" {
		return objectstore.ErrInvalid
	}
	return nil
}
func (*audioObjectClient) ListMultipartParts(context.Context, string, string, string) ([]objectstore.UploadedPart, error) {
	return []objectstore.UploadedPart{{Number: 1, ETag: "provider-md5", SizeBytes: 8}}, nil
}
func (c *audioObjectClient) AbortMultipart(context.Context, string, string, string) error { return nil }
func (c *audioObjectClient) Head(context.Context, string, string) (objectstore.Head, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return objectstore.Head{Size: int64(len(c.data)), ContentType: c.typeName}, nil
}
func (c *audioObjectClient) Get(context.Context, string, string) (io.ReadCloser, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return io.NopCloser(bytes.NewReader(c.data)), nil
}
func (c *audioObjectClient) Put(context.Context, string, string, io.Reader, int64, string) error {
	return nil
}

type audioCaptureActor struct {
	token, csrf                                 string
	tenant, responsibility, inspection, attempt identity.ID
	photo                                       identity.ID
}
type audioExternalFixture struct {
	db     *gorm.DB
	server http.Handler
	client *audioObjectClient
	actor  audioCaptureActor
	noteID identity.ID
	hash   string
}

func newAudioExternalFixture(t *testing.T) audioExternalFixture {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	for _, schema := range []string{"transcription", "inspections", "templates", "capture", "invitations", "messaging", "media", "usage"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	ddl := []string{
		`CREATE TABLE inspections.inspections (id text,tenant_id text,business_unit_id text,asset_id text,project_id text,template_version_id text)`,
		`CREATE TABLE templates.template_versions (id text,tenant_id text,schema_version integer,definition_json blob)`,
		`CREATE TABLE transcription.tenant_policies (tenant_id text PRIMARY KEY,enabled boolean,tenant_note_limit integer,tenant_audio_limit_ms integer,inspection_note_limit integer,inspection_audio_limit_ms integer,version integer,created_at datetime,updated_at datetime)`,
		`CREATE TABLE transcription.policy_mutation_receipts (tenant_id text,client_mutation_id text,payload_digest text,enabled boolean,tenant_note_limit integer,tenant_audio_limit_ms integer,inspection_note_limit integer,inspection_audio_limit_ms integer,result_version integer,created_at datetime,PRIMARY KEY(tenant_id,client_mutation_id))`,
		`CREATE TABLE transcription.audio_notes (tenant_id text,id text,inspection_id text,responsibility_id text,client_note_id text,requirement_key text,consent_version text,disclosure_version text,target_kind text,photo_media_id text,video_attempt_id text,source_media_id text,start_ms integer,end_ms integer,time_basis text,duration_ms integer,content_sha256 text,content_type text,size_bytes integer,status text,anchor_status text,version integer,audio_deletion_status text,upload_key text,upload_id text,created_at datetime,updated_at datetime,idempotency_payload_digest text,highlighted boolean,highlight_source text,text_confirmed boolean,no_speech boolean,safe_failure_code text,current_revision_id text,UNIQUE(tenant_id,inspection_id,responsibility_id,client_note_id))`,
		`CREATE TABLE transcription.transcript_revisions (id text,tenant_id text,note_id text,revision_number integer,text text,kind text,author_id text,call_id text,created_at datetime,PRIMARY KEY(id,tenant_id))`,
		`CREATE TABLE usage.llm_calls (tenant_id text,call_id text,job_id text,replay_generation integer)`,
		`CREATE TABLE transcription.mutation_receipts (tenant_id text,id text,note_id text,client_mutation_id text,operation text,payload_digest text,result_version integer,created_at datetime,UNIQUE(tenant_id,note_id,client_mutation_id))`,
		`CREATE TABLE transcription.jobs (tenant_id text,id text,note_id text,generation integer,profile_key text,profile_version integer,profile_digest text,status text,attempts integer,created_at datetime,updated_at datetime,UNIQUE(tenant_id,note_id,generation))`,
		`CREATE TABLE transcription.audio_deletions (tenant_id text,id text,note_id text,object_generation integer,object_key text,derivative_keys blob,status text,created_at datetime,updated_at datetime)`,
		`CREATE TABLE messaging.outbox (id text,tenant_id text,type text,schema_version integer,payload blob,correlation_id text,causation_id text,status text,attempts integer,next_attempt_at datetime,claimed_at datetime,last_error text,published_at datetime,created_at datetime)`,
		`CREATE TABLE inspections.responsibilities (id text,tenant_id text,inspection_id text,participant_id text,status text,version integer,created_at datetime,updated_at datetime)`,
		`CREATE TABLE capture.capture_drafts (id text,tenant_id text,responsibility_id text,status text,media_kind text,kind text)`,
		`CREATE TABLE capture.video_attempts (id text,tenant_id text,responsibility_id text,max_duration_seconds integer,status text)`,
		`CREATE TABLE media.media_objects (id text,tenant_id text,responsibility_id text,requirement_key text,media_kind text,status text)`,
		`CREATE TABLE invitations.processing_acceptances (id text,tenant_id text,responsibility_id text,disclosure_version text,photo_processing boolean,video_processing boolean,voice_processing boolean,voice_consent_version text,ai_analysis boolean,gps_use boolean,accepted_at datetime)`,
		`CREATE TABLE invitations.invitations (id text,tenant_id text,responsibility_id text,status text,expires_at datetime,revoked_at datetime)`,
		`CREATE TABLE invitations.external_sessions (id text,tenant_id text,invitation_id text,responsibility_id text,session_digest blob,csrf_digest blob,kind text,expires_at datetime,revoked_at datetime)`,
	}
	for _, sql := range ddl {
		if err := db.Exec(sql).Error; err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	actor := audioCaptureActor{tenant: identity.NewID(), responsibility: identity.NewID(), inspection: identity.NewID(), attempt: identity.NewID(), photo: identity.NewID(), csrf: "csrf-proof"}
	templateVersionID := identity.NewID()
	actor.token, err = security.NewScopedToken(actor.tenant)
	if err != nil {
		t.Fatal(err)
	}
	tokenHash := security.HashToken(actor.token)
	csrfDigest, err := security.CSRFProof(tokenHash, actor.csrf, []byte(strings.Repeat("p", 32)))
	if err != nil {
		t.Fatal(err)
	}
	invitationID := identity.NewID()
	seed := []struct {
		q string
		a []any
	}{
		{`INSERT INTO transcription.tenant_policies VALUES (?,?,?,?,?,?,?,?,?)`, []any{actor.tenant, true, 100, 300000, 20, 30000, 1, now, now}},
		{`INSERT INTO inspections.inspections VALUES (?,?,?,?,NULL,?)`, []any{actor.inspection, actor.tenant, identity.NewID(), identity.NewID(), templateVersionID}},
		{`INSERT INTO templates.template_versions VALUES (?,?,?,?)`, []any{templateVersionID, actor.tenant, 2, []byte(`{"captureLayout":"ENVIRONMENTS"}`)}},
		{`INSERT INTO inspections.responsibilities VALUES (?,?,?,?,?,?,?,?)`, []any{actor.responsibility, actor.tenant, actor.inspection, identity.NewID(), "IN_PROGRESS", 1, now, now}},
		{`INSERT INTO capture.capture_drafts VALUES (?,?,?,?,?,?)`, []any{identity.NewID(), actor.tenant, actor.responsibility, "OPEN", "VIDEO", "ENVIRONMENT"}},
		{`INSERT INTO capture.video_attempts VALUES (?,?,?,?,?)`, []any{actor.attempt, actor.tenant, actor.responsibility, 30, "RECEIVED"}},
		{`INSERT INTO media.media_objects VALUES (?,?,?,?,?,?)`, []any{actor.photo, actor.tenant, actor.responsibility, "room-1", "PHOTO", "READY"}},
		{`INSERT INTO invitations.processing_acceptances VALUES (?,?,?,?,?,?,?,?,?,?,?)`, []any{identity.NewID(), actor.tenant, actor.responsibility, "pt-BR-1", true, true, true, "voice-v1", true, true, now}},
		{`INSERT INTO invitations.invitations VALUES (?,?,?,?,?,NULL)`, []any{invitationID, actor.tenant, actor.responsibility, "ACTIVE", now.Add(time.Hour)}},
		{`INSERT INTO invitations.external_sessions VALUES (?,?,?,?,?,?,?,?,NULL)`, []any{identity.NewID(), actor.tenant, invitationID, actor.responsibility, tokenHash[:], csrfDigest[:], "CAPTURE", now.Add(time.Hour)}},
	}
	for _, v := range seed {
		if err := db.Exec(v.q, v.a...).Error; err != nil {
			t.Fatal(err)
		}
	}
	client := &audioObjectClient{data: validAudioFixture(), typeName: "audio/webm"}
	digest := sha256.Sum256(client.data)
	within := func(ctx context.Context, _ identity.ID, fn func(*gorm.DB) error) error {
		return db.WithContext(ctx).Transaction(fn)
	}
	invites := invitationcore.Service{DB: db, Pepper: []byte(strings.Repeat("p", 32)), Within: within}
	store := objectstore.Store{Bucket: "private", Client: client, AudioProbe: func(context.Context, []byte) (time.Duration, error) { return time.Second, nil }}
	resolver := &Resolver{DB: db, Bus: mediator.New(), Invitations: invites, Store: store, WithinTenant: within}
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: resolver}))
	server.AddTransport(transport.POST{})
	server.SetErrorPresenter(graph.PresentError)
	return audioExternalFixture{db: db, server: server, client: client, actor: actor, noteID: identity.NewID(), hash: hex.EncodeToString(digest[:])}
}

func validAudioFixture() []byte {
	data := make([]byte, 1024)
	copy(data, []byte{0x1a, 0x45, 0xdf, 0xa3})
	return data
}

func (f audioExternalFixture) execute(t *testing.T, query string, actor audioCaptureActor) map[string]any {
	t.Helper()
	result, err := f.executeRaw(query, actor)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func (f audioExternalFixture) executeRaw(query string, actor audioCaptureActor) (map[string]any, error) {
	body, _ := json.Marshal(map[string]string{"query": query})
	req := httptest.NewRequest(http.MethodPost, "/graphql", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(requestctx.WithExternalCredentials(req.Context(), requestctx.ExternalCredentials{SessionToken: actor.token, CSRFToken: actor.csrf}))
	w := httptest.NewRecorder()
	f.server.ServeHTTP(w, req)
	var result map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (f audioExternalFixture) executeInternal(t *testing.T, query string, role string) map[string]any {
	t.Helper()
	body, err := json.Marshal(map[string]string{"query": query})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/graphql", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	meta := requestctx.Metadata{TenantID: f.actor.tenant, Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: f.actor.tenant, Internal: true, Roles: []string{role}}}
	req = req.WithContext(requestctx.WithMetadata(req.Context(), meta))
	w := httptest.NewRecorder()
	f.server.ServeHTTP(w, req)
	var result map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode GraphQL response %q: %v", w.Body.String(), err)
	}
	return result
}

func (f audioExternalFixture) createQuery(noteID identity.ID, hash string, duration int) string {
	return `mutation { createAudioNoteUpload(input:{clientNoteId:"` + noteID.String() + `",requirementKey:"room-1",videoAttemptId:"` + f.actor.attempt.String() + `",startMs:500,endMs:1500,timeBasis:"CAPTURE_CLOCK",consentVersion:"voice-v1",disclosureVersion:"pt-BR-1",contentType:"audio/webm",contentSha256:"` + hash + `",sizeBytes:1024,durationMs:` + strconv.Itoa(duration) + `,clientMutationId:"admit-1"}) { noteId upload { url method headers } userErrors { code message } clientMutationId } }`
}

func (f audioExternalFixture) createPhotoQuery(noteID identity.ID, hash string, duration int) string {
	return `mutation { createAudioNoteUpload(input:{clientNoteId:"` + noteID.String() + `",requirementKey:"room-1",photoMediaId:"` + f.actor.photo.String() + `",consentVersion:"voice-v1",disclosureVersion:"pt-BR-1",contentType:"audio/webm",contentSha256:"` + hash + `",sizeBytes:1024,durationMs:` + strconv.Itoa(duration) + `,clientMutationId:"admit-photo"}) { noteId upload { url method headers } userErrors { code message } clientMutationId } }`
}

func (f audioExternalFixture) completeQuery(noteID identity.ID) string {
	return `mutation { completeAudioNoteUpload(input:{noteId:"` + noteID.String() + `",uploadProof:"audio-upload",clientMutationId:"complete-1"}) { note { id status version } userErrors { code message } clientMutationId } }`
}

func assertNoExternalAudioSideEffects(t *testing.T, f audioExternalFixture) {
	t.Helper()
	var notes, jobs, outbox int
	checks := []struct {
		query  string
		target *int
	}{
		{`SELECT count(*) FROM transcription.audio_notes WHERE tenant_id=?`, &notes},
		{`SELECT count(*) FROM transcription.jobs WHERE tenant_id=?`, &jobs},
		{`SELECT count(*) FROM messaging.outbox WHERE tenant_id=?`, &outbox},
	}
	for _, check := range checks {
		if err := f.db.Raw(check.query, f.actor.tenant).Scan(check.target).Error; err != nil {
			t.Fatal(err)
		}
	}
	if notes != 0 || jobs != 0 || outbox != 0 {
		t.Fatalf("rejected request left side effects: notes=%d jobs=%d outbox=%d", notes, jobs, outbox)
	}
}

func TestParticipantAudioMutationsRequireOpenCapture(t *testing.T) {
	f := newAudioExternalFixture(t)
	assertAudioGraphQLResult(t, f.execute(t, f.createPhotoQuery(f.noteID, f.hash, 1000), f.actor), "")
	assertAudioGraphQLResult(t, f.execute(t, f.completeQuery(f.noteID), f.actor), "")
	if err := f.db.Exec(`UPDATE transcription.audio_notes SET status='FAILED',anchor_status='VALID' WHERE tenant_id=? AND id=?`, f.actor.tenant, f.noteID).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Exec(`UPDATE transcription.jobs SET status='FAILED' WHERE tenant_id=? AND note_id=?`, f.actor.tenant, f.noteID).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Exec(`UPDATE capture.capture_drafts SET status='SUBMITTED' WHERE tenant_id=? AND responsibility_id=?`, f.actor.tenant, f.actor.responsibility).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Exec(`UPDATE invitations.invitations SET status='COMPLETED' WHERE tenant_id=?`, f.actor.tenant).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Exec(`UPDATE invitations.external_sessions SET revoked_at=? WHERE tenant_id=?`, time.Now().UTC(), f.actor.tenant).Error; err != nil {
		t.Fatal(err)
	}
	mutations := []struct{ name, query, code string }{
		{"highlight", `mutation { setAudioNoteHighlight(input:{noteId:"` + f.noteID.String() + `",highlighted:true,expectedVersion:2,clientMutationId:"closed-highlight"}) { userErrors { code message } } }`, "INVALID_STATE"},
		{"declare", `mutation { reviseAudioNote(input:{noteId:"` + f.noteID.String() + `",text:"texto declarado",revisionKind:PARTICIPANT_DECLARATION,expectedVersion:2,clientMutationId:"closed-declare"}) { userErrors { code message } } }`, "INVALID_STATE"},
		{"discard", `mutation { discardAudioNote(input:{noteId:"` + f.noteID.String() + `",expectedVersion:2,clientMutationId:"closed-discard"}) { userErrors { code message } } }`, "INVALID_STATE"},
		{"retry", `mutation { retryAudioNote(input:{noteId:"` + f.noteID.String() + `",expectedVersion:2,clientMutationId:"closed-retry"}) { userErrors { code message } } }`, "RETRY_UNAVAILABLE"},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			result := f.execute(t, mutation.query, f.actor)
			assertAudioGraphQLResult(t, result, mutation.code)
			if mutation.name == "declare" {
				encoded, err := json.Marshal(result)
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(encoded), "A captura não está mais aberta para receber uma declaração.") {
					t.Fatalf("declare did not report the closed capture message: %s", encoded)
				}
			}
		})
	}
}

func TestCreateAudioNoteUploadMapsProviderFailureToSafeUserErrorUT022(t *testing.T) {
	f := newAudioExternalFixture(t)
	f.client.createErr = errors.New("provider secret: signed request failed with HTTP 502")
	result := f.execute(t, f.createQuery(f.noteID, f.hash, 1000), f.actor)
	assertAudioGraphQLResult(t, result, "DEPENDENCY_UNAVAILABLE")
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "provider secret") || strings.Contains(string(encoded), "HTTP 502") {
		t.Fatalf("provider detail leaked in GraphQL response: %s", encoded)
	}
	var notes, jobs, outbox int
	queries := []struct {
		query  string
		target *int
	}{
		{`SELECT count(*) FROM transcription.audio_notes WHERE tenant_id=?`, &notes},
		{`SELECT count(*) FROM transcription.jobs WHERE tenant_id=?`, &jobs},
		{`SELECT count(*) FROM messaging.outbox WHERE tenant_id=?`, &outbox},
	}
	for _, check := range queries {
		if err := f.db.Raw(check.query, f.actor.tenant).Scan(check.target).Error; err != nil {
			t.Fatal(err)
		}
	}
	if notes != 0 || jobs != 0 || outbox != 0 {
		t.Fatalf("provider failure left side effects: notes=%d jobs=%d outbox=%d", notes, jobs, outbox)
	}
}

func TestAuthenticatedAudioNoteAdmissionCompletionAndIntegratorJourneyIT293To306E2E022(t *testing.T) {
	t.Run("IT-293/294/328/329/333 authorized create, identical retry and conflicting content", func(t *testing.T) {
		f := newAudioExternalFixture(t)
		first := f.execute(t, f.createQuery(f.noteID, f.hash, 1000), f.actor)
		assertAudioGraphQLResult(t, first, "")
		payload := first["data"].(map[string]any)["createAudioNoteUpload"].(map[string]any)
		if payload["clientMutationId"] != "admit-1" || payload["noteId"] != f.noteID.String() || payload["upload"] == nil {
			t.Fatalf("UT-021 upload resolver response=%#v", payload)
		}
		retry := f.execute(t, f.createQuery(f.noteID, f.hash, 1000), f.actor)
		assertAudioGraphQLResult(t, retry, "")
		retryPayload := retry["data"].(map[string]any)["createAudioNoteUpload"].(map[string]any)
		if retryPayload["noteId"] != payload["noteId"] {
			t.Fatalf("identical create retry returned a different note: first=%#v retry=%#v", payload["noteId"], retryPayload["noteId"])
		}
		// CreateAudioNoteUploadInput has no expectedVersion. IT-333 therefore
		// verifies the defined identity-plus-content conflict instead.
		changed := f.execute(t, f.createQuery(f.noteID, f.hash, 1500), f.actor)
		assertAudioGraphQLResult(t, changed, "CONFLICT")
		var count int
		if err := f.db.Raw(`SELECT count(*) FROM transcription.audio_notes WHERE tenant_id=?`, f.actor.tenant).Scan(&count).Error; err != nil || count != 1 {
			t.Fatalf("admission retry count=%d err=%v", count, err)
		}
		var duration int64
		if err := f.db.Raw(`SELECT duration_ms FROM transcription.audio_notes WHERE tenant_id=? AND id=?`, f.actor.tenant, f.noteID).Scan(&duration).Error; err != nil || duration != 1000 {
			t.Fatalf("original note duration_ms=%d err=%v, want 1000", duration, err)
		}
	})
	t.Run("IT-301 sequential identical admission retry resolves to one note", func(t *testing.T) {
		f := newAudioExternalFixture(t)
		first := f.execute(t, f.createQuery(f.noteID, f.hash, 1000), f.actor)
		assertAudioGraphQLResult(t, first, "")
		second := f.execute(t, f.createQuery(f.noteID, f.hash, 1000), f.actor)
		assertAudioGraphQLResult(t, second, "")
		firstPayload := first["data"].(map[string]any)["createAudioNoteUpload"].(map[string]any)
		secondPayload := second["data"].(map[string]any)["createAudioNoteUpload"].(map[string]any)
		if firstPayload["noteId"] != secondPayload["noteId"] || firstPayload["noteId"] != f.noteID.String() {
			t.Fatalf("idempotent admissions returned different note IDs: first=%#v second=%#v", firstPayload["noteId"], secondPayload["noteId"])
		}
		var count int
		if err := f.db.Raw(`SELECT count(*) FROM transcription.audio_notes WHERE tenant_id=? AND client_note_id=?`, f.actor.tenant, f.noteID).Scan(&count).Error; err != nil || count != 1 {
			t.Fatalf("sequential admission count=%d err=%v", count, err)
		}
	})
	t.Run("IT-297 malformed hash and note identity", func(t *testing.T) {
		for _, tc := range []struct {
			name  string
			query func(audioExternalFixture) string
		}{
			{"hash", func(f audioExternalFixture) string {
				return strings.Replace(f.createQuery(f.noteID, f.hash, 1000), f.hash, "xyz", 1)
			}},
			{"note identity", func(f audioExternalFixture) string {
				return strings.Replace(f.createQuery(f.noteID, f.hash, 1000), f.noteID.String(), "not-a-uuid", 1)
			}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				f := newAudioExternalFixture(t)
				assertAudioGraphQLResult(t, f.execute(t, tc.query(f), f.actor), "INVALID_INPUT")
				assertNoExternalAudioSideEffects(t, f)
			})
		}
	})
	t.Run("IT-297/298/299 invalid anchor, consent, and file limits", func(t *testing.T) {
		f := newAudioExternalFixture(t)
		invalid := strings.Replace(f.createQuery(f.noteID, f.hash, 1000), "endMs:1500", "endMs:40000", 1)
		assertAudioGraphQLResult(t, f.execute(t, invalid, f.actor), "INVALID_INPUT")
		missingConsent := strings.Replace(f.createQuery(f.noteID, f.hash, 1000), "consentVersion:\"voice-v1\"", "consentVersion:\"wrong\"", 1)
		assertAudioGraphQLResult(t, f.execute(t, missingConsent, f.actor), "CONSENT_REQUIRED")
		large := strings.Replace(f.createQuery(f.noteID, f.hash, 1000), "sizeBytes:1024", "sizeBytes:6000000", 1)
		assertAudioGraphQLResult(t, f.execute(t, large, f.actor), "INVALID_INPUT")
		tooLong := strings.Replace(f.createQuery(f.noteID, f.hash, 1000), "durationMs:1000", "durationMs:30001", 1)
		assertAudioGraphQLResult(t, f.execute(t, tooLong, f.actor), "LIMIT_REACHED")
		foreignAnchor := strings.Replace(f.createQuery(f.noteID, f.hash, 1000), f.actor.attempt.String(), identity.NewID().String(), 1)
		assertAudioGraphQLResult(t, f.execute(t, foreignAnchor, f.actor), "NOT_FOUND")
		noAnchor := strings.Replace(f.createQuery(f.noteID, f.hash, 1000), `,videoAttemptId:"`+f.actor.attempt.String()+`"`, "", 1)
		assertAudioGraphQLResult(t, f.execute(t, noAnchor, f.actor), "INVALID_INPUT")
		assertNoExternalAudioSideEffects(t, f)
	})
	t.Run("IT-302/303/336 duplicate completion creates one job and outbox", func(t *testing.T) {
		f := newAudioExternalFixture(t)
		assertAudioGraphQLResult(t, f.execute(t, f.createPhotoQuery(f.noteID, f.hash, 1000), f.actor), "")
		first := f.execute(t, f.completeQuery(f.noteID), f.actor)
		assertAudioGraphQLResult(t, first, "")
		firstPayload := first["data"].(map[string]any)["completeAudioNoteUpload"].(map[string]any)
		if firstPayload["clientMutationId"] != "complete-1" {
			t.Fatalf("completion did not echo clientMutationId: %#v", firstPayload)
		}
		firstNote := firstPayload["note"].(map[string]any)
		if firstNote["id"] != f.noteID.String() || firstNote["status"] != "QUEUED" {
			t.Fatalf("completion did not return tenant A note: %#v", firstNote)
		}
		second := f.execute(t, f.completeQuery(f.noteID), f.actor)
		assertAudioGraphQLResult(t, second, "")
		secondPayload := second["data"].(map[string]any)["completeAudioNoteUpload"].(map[string]any)
		if secondPayload["clientMutationId"] != "complete-1" || secondPayload["note"].(map[string]any)["id"] != firstNote["id"] {
			t.Fatalf("completion retry did not resolve to same note: first=%#v second=%#v", firstPayload, secondPayload)
		}
		var jobs, events int
		_ = f.db.Raw(`SELECT count(*) FROM transcription.jobs WHERE tenant_id=? AND note_id=?`, f.actor.tenant, f.noteID).Scan(&jobs).Error
		_ = f.db.Raw(`SELECT count(*) FROM messaging.outbox WHERE tenant_id=? AND correlation_id=?`, f.actor.tenant, f.noteID.String()).Scan(&events).Error
		if jobs != 1 || events != 1 {
			t.Fatalf("completion duplicates jobs=%d events=%d", jobs, events)
		}
	})
	t.Run("IT-338 admitted quota reservation remains valid when policy changes", func(t *testing.T) {
		f := newAudioExternalFixture(t)
		assertAudioGraphQLResult(t, f.execute(t, f.createQuery(f.noteID, f.hash, 1000), f.actor), "")
		if err := f.db.Exec(`UPDATE transcription.tenant_policies SET tenant_note_limit=1, inspection_note_limit=1 WHERE tenant_id=?`, f.actor.tenant).Error; err != nil {
			t.Fatal(err)
		}
		assertAudioGraphQLResult(t, f.execute(t, f.completeQuery(f.noteID), f.actor), "")
		var jobs int
		if err := f.db.Raw(`SELECT count(*) FROM transcription.jobs WHERE tenant_id=? AND note_id=?`, f.actor.tenant, f.noteID).Scan(&jobs).Error; err != nil || jobs != 1 {
			t.Fatalf("reserved note completion jobs=%d err=%v", jobs, err)
		}
	})
	t.Run("IT-295/296 scoped reads, immutable correction and discard", func(t *testing.T) {
		f := newAudioExternalFixture(t)
		assertAudioGraphQLResult(t, f.execute(t, f.createPhotoQuery(f.noteID, f.hash, 1000), f.actor), "")
		assertAudioGraphQLResult(t, f.execute(t, f.completeQuery(f.noteID), f.actor), "")
		revisionID := identity.NewID()
		if err := f.db.Exec(`UPDATE transcription.audio_notes SET status='COMPLETED',anchor_status='VALID',audio_deletion_status='DELETED',current_revision_id=? WHERE tenant_id=? AND id=?`, revisionID, f.actor.tenant, f.noteID).Error; err != nil {
			t.Fatal(err)
		}
		if err := f.db.Exec(`UPDATE transcription.jobs SET status='COMPLETED' WHERE tenant_id=? AND note_id=?`, f.actor.tenant, f.noteID).Error; err != nil {
			t.Fatal(err)
		}
		if err := f.db.Exec(`INSERT INTO transcription.transcript_revisions(id,tenant_id,note_id,revision_number,text,kind,created_at) VALUES(?,?,?,?,?,?,?)`, revisionID, f.actor.tenant, f.noteID, 1, "cinco pratos", "MODEL", time.Now().UTC()).Error; err != nil {
			t.Fatal(err)
		}
		readQuery := `query { audioNote(id:"` + f.noteID.String() + `") { id inspectionId environmentName requirementKey targetKind photoMediaId anchorStatus anchor { kind status mediaId mediaStatus approximate previewUrl playbackUrl frames { id timestampMs url status } framesTruncated visualCoverage } durationMs status audioDeletionStatus audioAvailability revisions { revisionNumber text kind authorName } } }`
		read := f.execute(t, readQuery, f.actor)
		assertAudioGraphQLResult(t, read, "")
		listQuery := `query { audioNotes(inspectionId:"` + f.actor.inspection.String() + `",environmentId:"room-1",first:10) { nodes { id inspectionId environmentName requirementKey targetKind photoMediaId anchorStatus anchor { kind status mediaId mediaStatus approximate previewUrl playbackUrl frames { id timestampMs url status } framesTruncated visualCoverage } durationMs status audioDeletionStatus audioAvailability revisions { revisionNumber text kind authorName } } pageInfo { hasNextPage } } }`
		list := f.execute(t, listQuery, f.actor)
		assertAudioGraphQLResult(t, list, "")
		internalRead := f.executeInternal(t, readQuery, auth.TenantAdmin)
		assertAudioGraphQLResult(t, internalRead, "")
		internalList := f.executeInternal(t, listQuery, auth.TenantAdmin)
		assertAudioGraphQLResult(t, internalList, "")
		for name, result := range map[string]map[string]any{"capture read": read, "capture list": list, "tenant admin read": internalRead, "tenant admin list": internalList} {
			encoded, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			body := string(encoded)
			for _, expected := range []string{f.noteID.String(), f.actor.inspection.String(), f.actor.photo.String(), "COMPLETED", "cinco pratos", "room-1", "PHOTO", "VALID", "DELETED", "UNAVAILABLE"} {
				if !strings.Contains(body, expected) {
					t.Errorf("%s omitted scoped note detail %q: %s", name, expected, body)
				}
			}
			for _, forbidden := range []string{"upload.invalid", "audio-upload", "uploadProof", "signedUrl"} {
				if strings.Contains(body, forbidden) {
					t.Errorf("%s exposed temporary audio access %q: %s", name, forbidden, body)
				}
			}
		}
		unscopedRoleRead := f.executeInternal(t, readQuery, auth.Viewer)
		assertAudioGraphQLResult(t, unscopedRoleRead, "FORBIDDEN")
		unscopedRoleList := f.executeInternal(t, listQuery, auth.Viewer)
		assertAudioGraphQLResult(t, unscopedRoleList, "FORBIDDEN")
		revise := f.execute(t, `mutation { reviseAudioNote(input:{noteId:"`+f.noteID.String()+`",text:"seis pratos",expectedVersion:2,clientMutationId:"integrator-edit"}) { note { version revisions { revisionNumber text } } userErrors { code message } } }`, f.actor)
		assertAudioGraphQLResult(t, revise, "")
		discard := f.execute(t, `mutation { discardAudioNote(input:{noteId:"`+f.noteID.String()+`",expectedVersion:3,clientMutationId:"integrator-discard"}) { note { status } userErrors { code message } } }`, f.actor)
		assertAudioGraphQLResult(t, discard, "")
		var revisions int
		if err := f.db.Raw(`SELECT count(*) FROM transcription.transcript_revisions WHERE tenant_id=? AND note_id=?`, f.actor.tenant, f.noteID).Scan(&revisions).Error; err != nil || revisions != 2 {
			t.Fatalf("revision count=%d err=%v", revisions, err)
		}
	})
	t.Run("IT- audio-note mutation receipt replays after capture closes", func(t *testing.T) {
		f := newAudioExternalFixture(t)
		assertAudioGraphQLResult(t, f.execute(t, f.createPhotoQuery(f.noteID, f.hash, 1000), f.actor), "")
		assertAudioGraphQLResult(t, f.execute(t, f.completeQuery(f.noteID), f.actor), "")
		revisionID := identity.NewID()
		if err := f.db.Exec(`UPDATE transcription.audio_notes SET status='COMPLETED',current_revision_id=? WHERE tenant_id=? AND id=?`, revisionID, f.actor.tenant, f.noteID).Error; err != nil {
			t.Fatal(err)
		}
		if err := f.db.Exec(`INSERT INTO transcription.transcript_revisions(id,tenant_id,note_id,revision_number,text,kind,created_at) VALUES(?,?,?,?,?,?,?)`, revisionID, f.actor.tenant, f.noteID, 1, "texto original", "MODEL", time.Now().UTC()).Error; err != nil {
			t.Fatal(err)
		}
		replay := `mutation { reviseAudioNote(input:{noteId:"` + f.noteID.String() + `",text:"texto revisado",expectedVersion:2,clientMutationId:"close-replay"}) { note { version } userErrors { code message } } }`
		within := func(ctx context.Context, _ identity.ID, fn func(*gorm.DB) error) error {
			return f.db.WithContext(ctx).Transaction(fn)
		}
		if _, err := revise_note.Setup(context.Background(), revise_note.Dependencies{DB: f.db, Within: within}, revise_note.Command{TenantID: f.actor.tenant, NoteID: f.noteID, ExpectedVersion: 2, ClientMutationID: "close-replay", Text: "texto revisado", Kind: "HUMAN_EDIT", IsParticipant: true}); err != nil {
			t.Fatalf("apply the mutation before closing capture: %v", err)
		}
		if err := f.db.Exec(`UPDATE capture.capture_drafts SET status='SUBMITTED' WHERE tenant_id=? AND responsibility_id=?`, f.actor.tenant, f.actor.responsibility).Error; err != nil {
			t.Fatal(err)
		}
		if err := f.db.Exec(`UPDATE invitations.invitations SET status='COMPLETED' WHERE tenant_id=?`, f.actor.tenant).Error; err != nil {
			t.Fatal(err)
		}
		if err := f.db.Exec(`UPDATE invitations.external_sessions SET revoked_at=? WHERE tenant_id=?`, time.Now().UTC(), f.actor.tenant).Error; err != nil {
			t.Fatal(err)
		}
		assertAudioGraphQLResult(t, f.execute(t, replay, f.actor), "")
		newMutation := `mutation { reviseAudioNote(input:{noteId:"` + f.noteID.String() + `",text:"nova alteração",expectedVersion:3,clientMutationId:"closed-new-mutation"}) { userErrors { code message } } }`
		assertAudioGraphQLResult(t, f.execute(t, newMutation, f.actor), "INVALID_STATE")
	})
	t.Run("IT-102 participant fallback is a typed declaration; IT-110 requires a valid anchor", func(t *testing.T) {
		f := newAudioExternalFixture(t)
		assertAudioGraphQLResult(t, f.execute(t, f.createPhotoQuery(f.noteID, f.hash, 1000), f.actor), "")
		assertAudioGraphQLResult(t, f.execute(t, f.completeQuery(f.noteID), f.actor), "")
		if err := f.db.Exec(`UPDATE transcription.audio_notes SET status='FAILED' WHERE tenant_id=? AND id=?`, f.actor.tenant, f.noteID).Error; err != nil {
			t.Fatal(err)
		}
		if err := f.db.Exec(`UPDATE transcription.jobs SET status='FAILED' WHERE tenant_id=? AND note_id=?`, f.actor.tenant, f.noteID).Error; err != nil {
			t.Fatal(err)
		}
		declare := `mutation { reviseAudioNote(input:{noteId:"` + f.noteID.String() + `",text:"cinco cadeiras",revisionKind:PARTICIPANT_DECLARATION,expectedVersion:2,clientMutationId:"participant-fallback"}) { note { currentRevisionId revisions { text kind } } userErrors { code message } } }`
		assertAudioGraphQLResult(t, f.execute(t, declare, f.actor), "")
		var kind string
		if err := f.db.Raw(`SELECT kind FROM transcription.transcript_revisions WHERE tenant_id=? AND note_id=?`, f.actor.tenant, f.noteID).Scan(&kind).Error; err != nil || kind != "PARTICIPANT_DECLARATION" {
			t.Fatalf("fallback kind=%q err=%v", kind, err)
		}
		other := newAudioExternalFixture(t)
		assertAudioGraphQLResult(t, other.execute(t, other.createQuery(other.noteID, other.hash, 1000), other.actor), "")
		assertAudioGraphQLResult(t, other.execute(t, other.completeQuery(other.noteID), other.actor), "")
		if err := other.db.Exec(`UPDATE transcription.audio_notes SET status='FAILED' WHERE tenant_id=? AND id=?`, other.actor.tenant, other.noteID).Error; err != nil {
			t.Fatal(err)
		}
		if err := other.db.Exec(`UPDATE transcription.jobs SET status='FAILED' WHERE tenant_id=? AND note_id=?`, other.actor.tenant, other.noteID).Error; err != nil {
			t.Fatal(err)
		}
		var anchor string
		if err := other.db.Raw(`SELECT anchor_status FROM transcription.audio_notes WHERE tenant_id=? AND id=?`, other.actor.tenant, other.noteID).Row().Scan(&anchor); err != nil || anchor != "PENDING" {
			t.Fatalf("video anchor should remain pending until media validation: anchor=%s err=%v", anchor, err)
		}
		pending := `mutation { reviseAudioNote(input:{noteId:"` + other.noteID.String() + `",text:"cinco cadeiras",revisionKind:PARTICIPANT_DECLARATION,expectedVersion:2,clientMutationId:"pending-anchor-fallback"}) { userErrors { code message } } }`
		result := other.execute(t, pending, other.actor)
		assertAudioGraphQLResult(t, result, "INVALID_STATE")
		encoded, err := json.Marshal(result)
		if err != nil || !strings.Contains(string(encoded), "vínculo da gravação ainda não foi validado") {
			t.Fatalf("IT-110 should fail because the anchor is pending, result=%s err=%v", encoded, err)
		}
	})
	t.Run("IT-330/331/332/334 admission rejects quota, disabled, consent, and expired session", func(t *testing.T) {
		tests := []struct {
			name, code string
			prepare    func(audioExternalFixture)
		}{
			{"limit", "LIMIT_REACHED", func(f audioExternalFixture) {
				_ = f.db.Exec(`UPDATE transcription.tenant_policies SET tenant_note_limit=1 WHERE tenant_id=?`, f.actor.tenant)
				reservedID := identity.NewID()
				_ = f.db.Exec(`INSERT INTO transcription.audio_notes (tenant_id,id,status,duration_ms) VALUES (?,?,?,?)`, f.actor.tenant, reservedID, "COMPLETED", 1000)
				_ = f.db.Exec(`INSERT INTO transcription.jobs (tenant_id,id,note_id,generation,status) VALUES (?,?,?,?,?)`, f.actor.tenant, identity.NewID(), reservedID, 1, "COMPLETED")
			}},
			{"disabled", "VOICE_NOT_ENABLED", func(f audioExternalFixture) {
				_ = f.db.Exec(`UPDATE transcription.tenant_policies SET enabled=false WHERE tenant_id=?`, f.actor.tenant)
			}},
			{"consent", "CONSENT_REQUIRED", func(f audioExternalFixture) {
				_ = f.db.Exec(`UPDATE invitations.processing_acceptances SET voice_processing=false WHERE tenant_id=?`, f.actor.tenant)
			}},
			{"expired", "SESSION_EXPIRED", func(f audioExternalFixture) {
				_ = f.db.Exec(`UPDATE invitations.invitations SET expires_at=? WHERE tenant_id=?`, time.Now().UTC().Add(-time.Second), f.actor.tenant)
			}},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				f := newAudioExternalFixture(t)
				tc.prepare(f)
				result := f.execute(t, f.createQuery(f.noteID, f.hash, 1000), f.actor)
				assertAudioGraphQLResult(t, result, tc.code)
				var count int
				_ = f.db.Raw(`SELECT count(*) FROM transcription.audio_notes WHERE tenant_id=?`, f.actor.tenant).Scan(&count)
				wantCount := 0
				if tc.name == "limit" {
					wantCount = 1
				}
				if count != wantCount {
					t.Fatalf("rejected admission changed the existing note count: got=%d want=%d", count, wantCount)
				}
			})
		}
	})
	t.Run("IT-337/339/341/304 malformed, discarded, missing and incomplete uploads create no job", func(t *testing.T) {
		t.Run("invalid bytes", func(t *testing.T) {
			f := newAudioExternalFixture(t)
			assertAudioGraphQLResult(t, f.execute(t, f.createQuery(f.noteID, f.hash, 1000), f.actor), "")
			f.client.data = []byte("not webm")
			assertAudioGraphQLResult(t, f.execute(t, f.completeQuery(f.noteID), f.actor), "INVALID_AUDIO")
			var jobs int
			_ = f.db.Raw(`SELECT count(*) FROM transcription.jobs WHERE tenant_id=?`, f.actor.tenant).Scan(&jobs)
			if jobs != 0 {
				t.Fatalf("invalid audio created %d jobs", jobs)
			}
		})
		t.Run("discarded note", func(t *testing.T) {
			f := newAudioExternalFixture(t)
			assertAudioGraphQLResult(t, f.execute(t, f.createQuery(f.noteID, f.hash, 1000), f.actor), "")
			_ = f.db.Exec(`UPDATE transcription.audio_notes SET status='CANCELED' WHERE tenant_id=? AND id=?`, f.actor.tenant, f.noteID)
			assertAudioGraphQLResult(t, f.execute(t, f.completeQuery(f.noteID), f.actor), "INVALID_STATE")
			assertAudioGraphQLResult(t, f.execute(t, f.createQuery(f.noteID, f.hash, 1000), f.actor), "INVALID_STATE")
		})
		t.Run("IT-305 expired identity is not reactivated", func(t *testing.T) {
			f := newAudioExternalFixture(t)
			assertAudioGraphQLResult(t, f.execute(t, f.createQuery(f.noteID, f.hash, 1000), f.actor), "")
			if err := f.db.Exec(`UPDATE transcription.audio_notes SET status='EXPIRED' WHERE tenant_id=? AND id=?`, f.actor.tenant, f.noteID).Error; err != nil {
				t.Fatal(err)
			}
			assertAudioGraphQLResult(t, f.execute(t, f.createQuery(f.noteID, f.hash, 1000), f.actor), "INVALID_STATE")
			var status string
			_ = f.db.Raw(`SELECT status FROM transcription.audio_notes WHERE tenant_id=? AND id=?`, f.actor.tenant, f.noteID).Scan(&status)
			if status != "EXPIRED" {
				t.Fatalf("expired identity reactivated to %s", status)
			}
		})
		t.Run("unknown note", func(t *testing.T) {
			f := newAudioExternalFixture(t)
			assertAudioGraphQLResult(t, f.execute(t, f.completeQuery(identity.NewID()), f.actor), "NOT_FOUND")
		})
		t.Run("unreceived object", func(t *testing.T) {
			f := newAudioExternalFixture(t)
			assertAudioGraphQLResult(t, f.execute(t, f.createQuery(f.noteID, f.hash, 1000), f.actor), "")
			f.client.data = nil
			assertAudioGraphQLResult(t, f.execute(t, f.completeQuery(f.noteID), f.actor), "INVALID_AUDIO")
			var jobs int
			_ = f.db.Raw(`SELECT count(*) FROM transcription.jobs WHERE tenant_id=?`, f.actor.tenant).Scan(&jobs)
			if jobs != 0 {
				t.Fatalf("unreceived object created %d jobs", jobs)
			}
		})
	})
	t.Run("IT-300/335/341/E2E-022 tenant B receives no note details", func(t *testing.T) {
		f := newAudioExternalFixture(t)
		assertAudioGraphQLResult(t, f.execute(t, f.createQuery(f.noteID, f.hash, 1000), f.actor), "")
		other := seedOtherExternalTenant(t, f)
		otherNoteID := identity.NewID()
		otherFixture := f
		otherFixture.actor = other
		otherFixture.noteID = otherNoteID
		createB := otherFixture.createQuery(otherNoteID, f.hash, 1000)
		createdB := f.execute(t, createB, other)
		assertAudioGraphQLResult(t, createdB, "")
		bPayload := createdB["data"].(map[string]any)["createAudioNoteUpload"].(map[string]any)
		if bPayload["noteId"] != otherNoteID.String() {
			t.Fatalf("tenant B create returned wrong note: %#v", bPayload)
		}
		assertAudioGraphQLResult(t, f.execute(t, otherFixture.completeQuery(otherNoteID), other), "")
		var bJobsBefore, bOutboxBefore, bJobsAfter, bOutboxAfter int
		if err := f.db.Raw(`SELECT count(*) FROM transcription.jobs WHERE tenant_id=?`, other.tenant).Scan(&bJobsBefore).Error; err != nil {
			t.Fatal(err)
		}
		if err := f.db.Raw(`SELECT count(*) FROM messaging.outbox WHERE tenant_id=?`, other.tenant).Scan(&bOutboxBefore).Error; err != nil {
			t.Fatal(err)
		}

		foreignAnchor := strings.Replace(f.createQuery(identity.NewID(), f.hash, 1000), f.actor.attempt.String(), other.attempt.String(), 1)
		assertAudioGraphQLResult(t, f.execute(t, foreignAnchor, f.actor), "NOT_FOUND")
		var aNotes, aJobs, aOutbox int
		_ = f.db.Raw(`SELECT count(*) FROM transcription.audio_notes WHERE tenant_id=?`, f.actor.tenant).Scan(&aNotes)
		_ = f.db.Raw(`SELECT count(*) FROM transcription.jobs WHERE tenant_id=?`, f.actor.tenant).Scan(&aJobs)
		_ = f.db.Raw(`SELECT count(*) FROM messaging.outbox WHERE tenant_id=?`, f.actor.tenant).Scan(&aOutbox)
		if aNotes != 1 || aJobs != 0 || aOutbox != 0 {
			t.Fatalf("foreign anchor create changed tenant A state: notes=%d jobs=%d outbox=%d", aNotes, aJobs, aOutbox)
		}
		assertAudioGraphQLResult(t, f.execute(t, f.completeQuery(otherNoteID), f.actor), "NOT_FOUND")
		result := f.execute(t, `query { audioNote(id:"`+f.noteID.String()+`") { id revisions { text } } }`, other)
		assertAudioGraphQLResult(t, result, "NOT_FOUND")
		if err := f.db.Raw(`SELECT count(*) FROM transcription.jobs WHERE tenant_id=?`, other.tenant).Scan(&bJobsAfter).Error; err != nil {
			t.Fatal(err)
		}
		if err := f.db.Raw(`SELECT count(*) FROM messaging.outbox WHERE tenant_id=?`, other.tenant).Scan(&bOutboxAfter).Error; err != nil {
			t.Fatal(err)
		}
		if bJobsBefore != bJobsAfter || bOutboxBefore != bOutboxAfter {
			t.Fatalf("tenant B job/outbox state changed: jobs %d->%d outbox %d->%d", bJobsBefore, bJobsAfter, bOutboxBefore, bOutboxAfter)
		}
		if data, ok := result["data"].(map[string]any); ok && data["audioNote"] != nil {
			t.Fatalf("cross-tenant read exposed note data: %#v", data["audioNote"])
		}
	})
}

func seedOtherExternalTenant(t *testing.T, f audioExternalFixture) audioCaptureActor {
	t.Helper()
	actor := audioCaptureActor{tenant: identity.NewID(), responsibility: identity.NewID(), inspection: identity.NewID(), attempt: identity.NewID(), photo: identity.NewID(), csrf: "tenant-b-csrf"}
	token, err := security.NewScopedToken(actor.tenant)
	if err != nil {
		t.Fatal(err)
	}
	actor.token = token
	tokenHash := security.HashToken(token)
	csrfDigest, err := security.CSRFProof(tokenHash, actor.csrf, []byte(strings.Repeat("p", 32)))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	invitationID := identity.NewID()
	templateVersionID := identity.NewID()
	rows := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO transcription.tenant_policies VALUES (?,?,?,?,?,?,?,?,?)`, []any{actor.tenant, true, 100, 300000, 20, 30000, 1, now, now}},
		{`INSERT INTO inspections.inspections VALUES (?,?,?,?,NULL,?)`, []any{actor.inspection, actor.tenant, identity.NewID(), identity.NewID(), templateVersionID}},
		{`INSERT INTO templates.template_versions VALUES (?,?,?,?)`, []any{templateVersionID, actor.tenant, 2, []byte(`{"captureLayout":"ENVIRONMENTS"}`)}},
		{`INSERT INTO inspections.responsibilities VALUES (?,?,?,?,?,?,?,?)`, []any{actor.responsibility, actor.tenant, actor.inspection, identity.NewID(), "IN_PROGRESS", 1, now, now}},
		{`INSERT INTO capture.capture_drafts VALUES (?,?,?,?,?,?)`, []any{identity.NewID(), actor.tenant, actor.responsibility, "OPEN", "VIDEO", "ENVIRONMENT"}},
		{`INSERT INTO capture.video_attempts VALUES (?,?,?,?,?)`, []any{actor.attempt, actor.tenant, actor.responsibility, 30, "RECEIVED"}},
		{`INSERT INTO media.media_objects VALUES (?,?,?,?,?,?)`, []any{actor.photo, actor.tenant, actor.responsibility, "room-1", "PHOTO", "READY"}},
		{`INSERT INTO invitations.processing_acceptances VALUES (?,?,?,?,?,?,?,?,?,?,?)`, []any{identity.NewID(), actor.tenant, actor.responsibility, "pt-BR-1", true, true, true, "voice-v1", true, true, now}},
		{`INSERT INTO invitations.invitations VALUES (?,?,?,?,?,NULL)`, []any{invitationID, actor.tenant, actor.responsibility, "ACTIVE", now.Add(time.Hour)}},
		{`INSERT INTO invitations.external_sessions VALUES (?,?,?,?,?,?,?,?,NULL)`, []any{identity.NewID(), actor.tenant, invitationID, actor.responsibility, tokenHash[:], csrfDigest[:], "CAPTURE", now.Add(time.Hour)}},
	}
	for _, row := range rows {
		if err := f.db.Exec(row.query, row.args...).Error; err != nil {
			t.Fatal(err)
		}
	}
	return actor
}
