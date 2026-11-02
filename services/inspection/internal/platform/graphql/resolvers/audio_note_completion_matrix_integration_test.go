//go:build integration

package resolvers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	invitationcore "inspection/services/inspection/internal/features/invitations/core"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	graph "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/security"

	"github.com/99designs/gqlgen/graphql/handler"
	"gorm.io/gorm"
)

type audioCompletionITFixture struct {
	h        *harness.Harness
	seed     harness.Task06Fixture
	server   *handler.Server
	ctx      context.Context
	noteID   identity.ID
	dataHash string
}

func newAudioCompletionITFixture(t *testing.T) audioCompletionITFixture {
	t.Helper()
	fx := newAPI06Fixture(t)
	seed := fx.seed
	data := validAudioFixture()
	digest := sha256.Sum256(data)
	client := &audioObjectClient{data: data, typeName: "audio/webm"}
	pepper := []byte(strings.Repeat("r", 32))
	token, err := security.NewScopedToken(seed.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	csrf := "it-r3a-csrf"
	hash := security.HashToken(token)
	csrfHash, err := security.CSRFProof(hash, csrf, pepper)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	invitationID := identity.NewID()
	if err := fx.h.AdminDB.Create(&database.Invitation{ID: invitationID, TenantID: seed.TenantID, ResponsibilityID: seed.ResponsibilityID, TokenHash: hash[:], DeliveryIntents: []byte(`[]`), Status: "ACTIVE", ExpiresAt: now.Add(time.Hour), CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := fx.h.AdminDB.Create(&database.ExternalSession{ID: identity.NewID(), TenantID: seed.TenantID, InvitationID: identity.Ptr(invitationID), ResponsibilityID: identity.Ptr(seed.ResponsibilityID), SessionDigest: hash[:], CSRFDigest: csrfHash[:], ExpiresAt: now.Add(time.Hour), CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := fx.h.AdminDB.Model(&database.CaptureDraft{}).Where("tenant_id=? AND responsibility_id=?", seed.TenantID, seed.ResponsibilityID).Update("status", "OPEN").Error; err != nil {
		t.Fatal(err)
	}
	invites := invitationcore.Service{DB: fx.h.DB, Pepper: pepper, Within: fx.h.WithinTenant}
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{DB: fx.h.DB, Bus: mediator.New(), Invitations: invites, Store: objectstore.Store{Bucket: "audio-it", Client: client, AudioProbe: func(context.Context, []byte) (time.Duration, error) { return time.Second, nil }}}}))
	server.SetErrorPresenter(graph.PresentError)
	ctx := requestctx.WithExternalCredentials(context.Background(), requestctx.ExternalCredentials{SessionToken: token, CSRFToken: csrf})
	return audioCompletionITFixture{h: fx.h, seed: seed, server: server, ctx: ctx, noteID: identity.NewID(), dataHash: hex.EncodeToString(digest[:])}
}

func (f audioCompletionITFixture) seedUploadingNote(t *testing.T, tenant, inspection, responsibility, media, id identity.ID) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := f.h.WithinTenant(context.Background(), tenant, func(tx *gorm.DB) error {
		return tx.Exec(`INSERT INTO transcription.audio_notes (tenant_id,id,inspection_id,responsibility_id,client_note_id,requirement_key,consent_version,disclosure_version,target_kind,photo_media_id,duration_ms,content_sha256,content_type,size_bytes,status,anchor_status,version,audio_deletion_status,upload_key,upload_id,created_at,updated_at,idempotency_payload_digest)
VALUES (?,?,?,?,?,'room-1','voice-v1','pt-BR-1','PHOTO',?,1000,?,'audio/webm',1024,'UPLOADING','PENDING',1,'PENDING',?,?,?,?,'reserved-at-admission')`, tenant, id, inspection, responsibility, id, media, f.dataHash, "audio-key-"+id.String(), "audio-proof", now, now).Error
	}); err != nil {
		t.Fatal(err)
	}
}

func (f audioCompletionITFixture) complete(t *testing.T, id identity.ID) string {
	t.Helper()
	query := fmt.Sprintf(`mutation { completeAudioNoteUpload(input:{noteId:%q,uploadProof:"audio-proof",clientMutationId:"r3a-complete"}) { note { id status version } userErrors { code message } clientMutationId } }`, id.String())
	return executeTask06GraphQL(t, f.server, query, f.ctx)
}

func TestAudioNoteCompletionMatrixIT338IT340IT341(t *testing.T) {
	t.Run("IT-338 reserved admission completes after quota reaches ceiling", func(t *testing.T) {
		f := newAudioCompletionITFixture(t)
		f.seedUploadingNote(t, f.seed.TenantID, f.seed.InspectionID, f.seed.ResponsibilityID, f.seed.MediaID, f.noteID)
		if err := f.h.WithinTenant(context.Background(), f.seed.TenantID, func(tx *gorm.DB) error {
			return tx.Exec(`UPDATE transcription.tenant_policies SET tenant_note_limit=1,inspection_note_limit=1,tenant_audio_limit_ms=1000,inspection_audio_limit_ms=1000 WHERE tenant_id=?`, f.seed.TenantID).Error
		}); err != nil {
			t.Fatal(err)
		}
		response := f.complete(t, f.noteID)
		if strings.Contains(response, "LIMIT_REACHED") || strings.Contains(response, `"errors"`) || !strings.Contains(response, `"status":"QUEUED"`) || !strings.Contains(response, `"userErrors":[]`) {
			t.Fatalf("IT-338 admitted note did not complete at quota ceiling: %s", response)
		}
	})

	t.Run("IT-340 repeat completion after version advances replays one job and event", func(t *testing.T) {
		f := newAudioCompletionITFixture(t)
		if _, exists := reflect.TypeOf(graph.CompleteAudioNoteUploadInput{}).FieldByName("ExpectedVersion"); exists {
			t.Fatal("IT-340 CompleteAudioNoteUploadInput must not define expectedVersion")
		}
		f.seedUploadingNote(t, f.seed.TenantID, f.seed.InspectionID, f.seed.ResponsibilityID, f.seed.MediaID, f.noteID)
		first := f.complete(t, f.noteID)
		if strings.Contains(first, "errors") || !strings.Contains(first, `"status":"QUEUED"`) {
			t.Fatalf("IT-340 first completion: %s", first)
		}
		if err := f.h.WithinTenant(context.Background(), f.seed.TenantID, func(tx *gorm.DB) error {
			return tx.Exec(`UPDATE transcription.audio_notes SET version=version+1 WHERE tenant_id=? AND id=?`, f.seed.TenantID, f.noteID).Error
		}); err != nil {
			t.Fatal(err)
		}
		second := f.complete(t, f.noteID)
		if strings.Contains(second, "errors") || !strings.Contains(second, f.noteID.String()) || !strings.Contains(second, `"status":"QUEUED"`) {
			t.Fatalf("IT-340 replay did not resolve to same note: %s", second)
		}
		var jobs, events int64
		if err := f.h.WithinTenant(context.Background(), f.seed.TenantID, func(tx *gorm.DB) error {
			if err := tx.Table("transcription.jobs").Where("tenant_id=? AND note_id=?", f.seed.TenantID, f.noteID).Count(&jobs).Error; err != nil {
				return err
			}
			return tx.Table("messaging.outbox").Where("tenant_id=? AND correlation_id=?", f.seed.TenantID, f.noteID.String()).Count(&events).Error
		}); err != nil || jobs != 1 || events != 1 {
			t.Fatalf("IT-340 jobs=%d events=%d err=%v", jobs, events, err)
		}
	})

	t.Run("IT-341 tenant B completion is hidden from tenant A", func(t *testing.T) {
		f := newAudioCompletionITFixture(t)
		foreign, err := f.h.SeedTask06Fixture(context.Background(), harness.Task06FixtureOptions{})
		if err != nil {
			t.Fatal(err)
		}
		f.seedUploadingNote(t, foreign.TenantID, foreign.InspectionID, foreign.ResponsibilityID, foreign.MediaID, f.noteID)
		response := f.complete(t, f.noteID)
		if !strings.Contains(response, "NOT_FOUND") || !strings.Contains(response, "não foi encontrada") || strings.Contains(response, foreign.TenantID.String()) {
			t.Fatalf("IT-341 expected safe NOT_FOUND response: %s", response)
		}
		var state string
		if err := f.h.AdminDB.Raw(`SELECT status FROM transcription.audio_notes WHERE tenant_id=? AND id=?`, foreign.TenantID, f.noteID).Row().Scan(&state); err != nil || state != "UPLOADING" {
			t.Fatalf("IT-341 foreign note changed: status=%s err=%v", state, err)
		}
		var jobs, events int64
		if err := f.h.AdminDB.Table("transcription.jobs").Where("tenant_id=? AND note_id=?", foreign.TenantID, f.noteID).Count(&jobs).Error; err != nil {
			t.Fatal(err)
		}
		if err := f.h.AdminDB.Table("messaging.outbox").Where("tenant_id=? AND correlation_id=?", foreign.TenantID, f.noteID.String()).Count(&events).Error; err != nil {
			t.Fatal(err)
		}
		if jobs != 0 || events != 0 {
			t.Fatalf("IT-341 unauthorized completion caused side effects: jobs=%d events=%d", jobs, events)
		}
	})
}

func TestInventoryVoiceSurfaceStateAndAuthorizationIT374IT375(t *testing.T) {
	t.Run("IT-374 discarded draft is rejected safely", func(t *testing.T) {
		fx, d := seedVoiceDraft(t, auth.Employee)
		if err := fx.h.AdminDB.Model(&database.OriginVersion{}).Where("tenant_id=? AND id=?", fx.seed.TenantID, d.version).Updates(map[string]any{"status": "DISCARDED", "discarded_at": time.Now().UTC()}).Error; err != nil {
			t.Fatal(err)
		}
		query := fmt.Sprintf(`query { inventoryDraft(originVersionId:%q) { environments { items { id } } } }`, d.version.String())
		response := executeTask06GraphQL(t, fx.server, query, fx.ctx)
		if !strings.Contains(response, "INVALID_STATE") || !strings.Contains(response, "rascunho") {
			t.Fatalf("IT-374 discarded inventory draft should return safe INVALID_STATE: %s", response)
		}
		decide := fmt.Sprintf(`mutation { decideInventoryItems(input:{originVersionId:%q,decisions:[{itemId:%q,action:"ACCEPT",expectedVersion:1}],clientMutationId:"r3a-discarded-item"}) { items { id } userErrors { code message } } }`, d.version.String(), d.item.String())
		response = executeTask06GraphQL(t, fx.server, decide, fx.ctx)
		if !strings.Contains(response, "INVALID_STATE") || !strings.Contains(response, "rascunho") || strings.Contains(response, d.item.String()) {
			t.Fatalf("IT-374 discarded draft InventoryItem operation should return safe INVALID_STATE without an item: %s", response)
		}
	})

	t.Run("IT-375 tenant without capability cannot read inventory or usage", func(t *testing.T) {
		fx, d := seedVoiceDraft(t, auth.Employee)
		foreign, err := fx.h.SeedTask06Fixture(context.Background(), harness.Task06FixtureOptions{})
		if err != nil {
			t.Fatal(err)
		}
		foreignCtx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: foreign.TenantID, Principal: requestctx.Principal{IdentityID: foreign.IdentityID, TenantID: foreign.TenantID, Roles: []string{auth.CustomerViewer}, ProductEntitlements: auth.ProductsForRole(auth.CustomerViewer)}})
		for _, query := range []string{
			fmt.Sprintf(`query { inventoryDraft(originVersionId:%q) { environments { items { id } } } }`, d.version.String()),
			`query { llmUsage(filter:{tenantId:"` + fx.seed.TenantID.String() + `"}) { attemptedCalls } }`,
		} {
			response := executeTask06GraphQL(t, fx.server, query, foreignCtx)
			if !strings.Contains(response, "FORBIDDEN") || strings.Contains(response, fx.seed.TenantID.String()) {
				t.Fatalf("IT-375 expected safe FORBIDDEN response: %s", response)
			}
		}
	})
}
