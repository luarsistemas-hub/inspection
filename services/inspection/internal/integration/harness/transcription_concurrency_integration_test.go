//go:build integration

package harness

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"inspection/libs/identity"
	mediacore "inspection/services/inspection/internal/features/media/core"
	"inspection/services/inspection/internal/features/transcription/complete_note_upload"
	"inspection/services/inspection/internal/features/transcription/create_note_upload"
	"inspection/services/inspection/internal/features/transcription/discard_note"
	"inspection/services/inspection/internal/features/transcription/retry_note"
	"inspection/services/inspection/internal/features/transcription/revise_note"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/objectstore"

	"gorm.io/gorm"
)

type transcriptionObjectClient struct {
	data   []byte
	mu     sync.Mutex
	onHead func(string)
}

func (c *transcriptionObjectClient) CreateMultipart(_ context.Context, _, key, _ string) (string, error) {
	return "proof-" + key[len(key)-8:], nil
}
func (c *transcriptionObjectClient) PresignPart(_ context.Context, _, key, upload string, _ int, _ time.Duration) (string, error) {
	return "https://upload.invalid/" + upload, nil
}
func (c *transcriptionObjectClient) CompleteMultipart(context.Context, string, string, string, []objectstore.Part) error {
	return nil
}
func (*transcriptionObjectClient) ListMultipartParts(context.Context, string, string, string) ([]objectstore.UploadedPart, error) {
	return []objectstore.UploadedPart{{Number: 1, ETag: "verified-etag", SizeBytes: 8}}, nil
}
func (c *transcriptionObjectClient) AbortMultipart(context.Context, string, string, string) error {
	return nil
}
func (c *transcriptionObjectClient) Head(_ context.Context, _, key string) (objectstore.Head, error) {
	if c.onHead != nil {
		c.onHead(key)
	}
	return objectstore.Head{Size: int64(len(c.data)), ContentType: "audio/webm"}, nil
}
func (c *transcriptionObjectClient) Get(context.Context, string, string) (io.ReadCloser, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return io.NopCloser(bytes.NewReader(c.data)), nil
}
func (c *transcriptionObjectClient) Put(context.Context, string, string, io.Reader, int64, string) error {
	return nil
}

func TestTranscriptionPostgresConcurrencyIT301IT306(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	h, err := NewFromEnv(ctx)
	if err != nil {
		t.Skipf("integration dependencies unavailable: %v", err)
	}
	defer h.Close()
	var voiceSchema sql.NullString
	if err := h.MigrationDB.Raw("SELECT to_regclass('transcription.tenant_policies')::text").Row().Scan(&voiceSchema); err != nil {
		t.Fatal(err)
	}
	if !voiceSchema.Valid {
		t.Skip("current test database does not have the voice-note migration; set up the current schema before running PostgreSQL integration tests")
	}

	t.Run("IT voice note anchor and video upload share lock order", func(t *testing.T) {
		fixture := seedVoiceFixture(t, ctx, h, 10)
		if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
			return tx.Model(&database.VideoAttempt{}).Where("tenant_id=? AND id=?", fixture.TenantID, fixture.VideoAttemptID).Update("status", "CANCELED").Error
		}); err != nil {
			t.Fatal(err)
		}
		fixture.VideoAttemptID = identity.NewID()
		now := time.Now().UTC()
		if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
			return tx.Create(&database.VideoAttempt{ID: fixture.VideoAttemptID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, Kind: "CURRENT", PolicyVersion: 1, MaxDurationSeconds: 900, MaxBytes: objectstore.MaxVideoBytes, AcquisitionSource: "CAMERA", Status: "ACTIVE", IdempotencyKey: identity.NewID().String(), PayloadDigest: strings.Repeat("a", 64), AdmittedAt: now, ExpiresAt: now.Add(time.Hour), CreatedAt: now}).Error
		}); err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		errs := make(chan error, 2)
		go func() {
			<-start
			_, err := create_note_upload.Setup(ctx, create_note_upload.Dependencies{DB: h.DB, Store: voiceStore(nil), Within: h.WithinTenant}, voiceCommand(fixture, identity.NewID()))
			errs <- err
		}()
		go func() {
			<-start
			_, err := (mediacore.Service{DB: h.DB, Store: voiceStore(nil), Within: h.WithinTenant}).CreateVideoWithAttempt(ctx, fixture.TenantID, fixture.ResponsibilityID, fixture.VideoAttemptID, "video/mp4", strings.Repeat("a", 64), 1024, identity.NewID().String())
			errs <- err
		}()
		close(start)
		for i := 0; i < 2; i++ {
			if err := <-errs; err != nil {
				t.Fatalf("concurrent note/video admission failed (including possible deadlock): %v", err)
			}
		}
	})

	t.Run("IT-301 concurrent identical creates resolve to the same reservation", func(t *testing.T) {
		fixture := seedVoiceFixture(t, ctx, h, 10)
		service := create_note_upload.Dependencies{DB: h.DB, Store: voiceStore(nil), Within: h.WithinTenant}
		command := voiceCommand(fixture, identity.NewID())
		start := make(chan struct{})
		type response struct {
			intent create_note_upload.UploadIntent
			err    error
		}
		results := make(chan response, 2)
		for i := 0; i < 2; i++ {
			go func() {
				<-start
				intent, callErr := create_note_upload.Setup(ctx, service, command)
				results <- response{intent: intent, err: callErr}
			}()
		}
		close(start)
		var intents []create_note_upload.UploadIntent
		for i := 0; i < 2; i++ {
			result := <-results
			if result.err != nil {
				t.Fatalf("identical concurrent create failed: %v", result.err)
			}
			intents = append(intents, result.intent)
		}
		if intents[0].NoteID != intents[1].NoteID || intents[0].UploadProof != intents[1].UploadProof || intents[0].URL != intents[1].URL {
			t.Fatalf("identical creates returned different reservations: first=%+v second=%+v", intents[0], intents[1])
		}
		var count int64
		if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
			return tx.Table("transcription.audio_notes").Where("tenant_id=? AND responsibility_id=? AND client_note_id=?", fixture.TenantID, fixture.ResponsibilityID, command.ClientNoteID).Count(&count).Error
		}); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("identical create reserved %d notes, want one", count)
		}
	})

	t.Run("quota reservation serializes concurrent distinct creates", func(t *testing.T) {
		fixture := seedVoiceFixture(t, ctx, h, 1)
		service := create_note_upload.Dependencies{DB: h.DB, Store: voiceStore(nil), Within: h.WithinTenant}
		start := make(chan struct{})
		errs := make(chan error, 2)
		for i := 0; i < 2; i++ {
			id := identity.NewID()
			go func() {
				<-start
				_, callErr := create_note_upload.Setup(ctx, service, voiceCommand(fixture, id))
				errs <- callErr
			}()
		}
		close(start)
		var successes, limited int
		for i := 0; i < 2; i++ {
			callErr := <-errs
			if callErr == nil {
				successes++
			} else if apperror.Is(callErr, apperror.LimitReached) {
				limited++
			} else {
				t.Errorf("unexpected concurrent admission error: %v", callErr)
			}
		}
		if successes != 1 || limited != 1 {
			t.Fatalf("success=%d limit=%d; want one reserved quota slot", successes, limited)
		}
		var count int64
		if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
			return tx.Table("transcription.audio_notes").Where("tenant_id=? AND responsibility_id=?", fixture.TenantID, fixture.ResponsibilityID).Count(&count).Error
		}); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("reserved note count=%d, want 1", count)
		}
	})

	t.Run("concurrent create and participant retry share draft and policy without deadlock", func(t *testing.T) {
		fixture := seedVoiceFixture(t, ctx, h, 10)
		deps := create_note_upload.Dependencies{DB: h.DB, Store: voiceStore(nil), Within: h.WithinTenant}
		first, err := create_note_upload.Setup(ctx, deps, voiceCommand(fixture, identity.NewID()))
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
			if err := tx.Exec("UPDATE transcription.audio_notes SET status='FAILED',version=2 WHERE tenant_id=? AND id=?", fixture.TenantID, first.NoteID).Error; err != nil {
				return err
			}
			return tx.Exec("INSERT INTO transcription.jobs(tenant_id,id,note_id,generation,profile_key,profile_version,profile_digest,status,attempts,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)", fixture.TenantID, identity.NewID(), first.NoteID, 1, "GEMINI_2_5_FLASH_LITE", 1, "fixture", "FAILED", 1, now, now).Error
		}); err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		errs := make(chan error, 2)
		retryCommand := retry_note.Command{TenantID: fixture.TenantID, NoteID: first.NoteID, ExpectedVersion: 2, ClientMutationID: identity.NewID().String(), IsParticipant: true}
		go func() {
			<-start
			_, e := create_note_upload.Setup(ctx, deps, voiceCommand(fixture, identity.NewID()))
			errs <- e
		}()
		go func() {
			<-start
			errs <- retry_note.Setup(ctx, retry_note.Dependencies{DB: h.DB, Within: h.WithinTenant}, retryCommand)
		}()
		close(start)
		for i := 0; i < 2; i++ {
			if err := <-errs; err != nil {
				t.Fatalf("concurrent transcription operation: %v", err)
			}
		}
		if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
			return tx.Model(&database.CaptureDraft{}).Where("tenant_id=? AND id=?", fixture.TenantID, fixture.DraftID).Update("status", "SUBMITTED").Error
		}); err != nil {
			t.Fatal(err)
		}
		if err := retry_note.Setup(ctx, retry_note.Dependencies{DB: h.DB, Within: h.WithinTenant}, retryCommand); err != nil {
			t.Fatalf("idempotent retry after capture closure: %v", err)
		}
	})

	t.Run("completion race creates one job and one outbox record", func(t *testing.T) {
		fixture := seedVoiceFixture(t, ctx, h, 10)
		data := []byte{0x1a, 0x45, 0xdf, 0xa3, 1, 2, 3, 4}
		store := voiceStore(data)
		command := voiceCommand(fixture, identity.NewID())
		intent, err := create_note_upload.Setup(ctx, create_note_upload.Dependencies{DB: h.DB, Store: store, Within: h.WithinTenant}, command)
		if err != nil {
			t.Fatal(err)
		}
		noteID, _ := identity.ParseID(intent.NoteID.String())
		complete := complete_note_upload.Dependencies{DB: h.DB, Store: store, Within: h.WithinTenant}
		start := make(chan struct{})
		errs := make(chan error, 2)
		for i := 0; i < 2; i++ {
			go func() {
				<-start
				errs <- complete_note_upload.Setup(ctx, complete, complete_note_upload.Command{TenantID: fixture.TenantID, NoteID: noteID, UploadProof: intent.UploadProof})
			}()
		}
		close(start)
		for i := 0; i < 2; i++ {
			if err := <-errs; err != nil {
				t.Errorf("completion race: %v", err)
			}
		}
		var jobs, events int64
		if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
			if err := tx.Table("transcription.jobs").Where("tenant_id=? AND note_id=?", fixture.TenantID, noteID).Count(&jobs).Error; err != nil {
				return err
			}
			return tx.Table("messaging.outbox").Where("tenant_id=? AND correlation_id=? AND type='transcription.requested.v1'", fixture.TenantID, noteID.String()).Count(&events).Error
		}); err != nil {
			t.Fatal(err)
		}
		if jobs != 1 || events != 1 {
			t.Fatalf("jobs=%d outbox=%d, want exactly one each", jobs, events)
		}
	})

	t.Run("IT-306 three tenants and two inspections keep quota and status isolated", func(t *testing.T) {
		type tenantInspections struct{ first, second Task06Fixture }
		groups := make([]tenantInspections, 0, 3)
		for i := 0; i < 3; i++ {
			first := seedVoiceFixture(t, ctx, h, 1)
			second := seedSecondVoiceInspection(t, ctx, h, first)
			groups = append(groups, tenantInspections{first: first, second: second})
		}
		deps := create_note_upload.Dependencies{DB: h.DB, Store: voiceStore(nil), Within: h.WithinTenant}
		for index, group := range groups {
			first, err := create_note_upload.Setup(ctx, deps, voiceCommand(group.first, identity.NewID()))
			if err != nil {
				t.Fatalf("tenant %s first inspection admission: %v", group.first.TenantID, err)
			}
			var status string
			if err := h.WithinTenant(ctx, group.first.TenantID, func(tx *gorm.DB) error {
				return tx.Table("transcription.audio_notes").Select("status").Where("tenant_id=? AND id=?", group.first.TenantID, first.NoteID).Take(&status).Error
			}); err != nil || status != "UPLOADING" {
				t.Fatalf("tenant %d first inspection status=%q err=%v", index+1, status, err)
			}
			if _, err := create_note_upload.Setup(ctx, deps, voiceCommand(group.second, identity.NewID())); !apperror.Is(err, apperror.LimitReached) {
				t.Fatalf("tenant %s cross-inspection tenant quota error=%v; want LIMIT_REACHED", group.first.TenantID, err)
			}
			if err := h.WithinTenant(ctx, group.first.TenantID, func(tx *gorm.DB) error {
				return tx.Exec("UPDATE transcription.tenant_policies SET tenant_note_limit=10,inspection_note_limit=1 WHERE tenant_id=?", group.first.TenantID).Error
			}); err != nil {
				t.Fatal(err)
			}
			second, err := create_note_upload.Setup(ctx, deps, voiceCommand(group.second, identity.NewID()))
			if err != nil {
				t.Fatalf("tenant %s second inspection quota should be independent: %v", group.first.TenantID, err)
			}
			if err := h.WithinTenant(ctx, group.first.TenantID, func(tx *gorm.DB) error {
				return tx.Table("transcription.audio_notes").Select("status").Where("tenant_id=? AND id=?", group.first.TenantID, second.NoteID).Take(&status).Error
			}); err != nil || status != "UPLOADING" {
				t.Fatalf("tenant %d second inspection status=%q err=%v", index+1, status, err)
			}
			if _, err := create_note_upload.Setup(ctx, deps, voiceCommand(group.second, identity.NewID())); !apperror.Is(err, apperror.LimitReached) {
				t.Fatalf("tenant %s exhausted inspection quota error=%v; want LIMIT_REACHED", group.first.TenantID, err)
			}
		}
	})

	t.Run("IT-087 IT-351 IT-355 operators can discard and retry after capture closes", func(t *testing.T) {
		for _, operation := range []string{"discard", "retry"} {
			t.Run(operation, func(t *testing.T) {
				fixture := seedVoiceFixture(t, ctx, h, 5)
				noteID := seedFailedVoiceNote(t, ctx, h, fixture)
				cmd := discard_note.Command{TenantID: fixture.TenantID, NoteID: noteID, ExpectedVersion: 2, ClientMutationID: "internal-discard", IsParticipant: false}
				if operation == "discard" {
					if err := discard_note.Setup(ctx, discard_note.Dependencies{DB: h.DB, Within: h.WithinTenant}, cmd); err != nil {
						t.Fatalf("operator discard after close: %v", err)
					}
				} else {
					if err := retry_note.Setup(ctx, retry_note.Dependencies{DB: h.DB, Within: h.WithinTenant}, retry_note.Command{TenantID: fixture.TenantID, NoteID: noteID, ExpectedVersion: 2, ClientMutationID: "internal-retry", IsParticipant: false}); err != nil {
						t.Fatalf("operator retry after close: %v", err)
					}
				}
			})
		}
	})

	t.Run("IT-351 participant cannot discard after capture closes", func(t *testing.T) {
		fixture := seedVoiceFixture(t, ctx, h, 5)
		noteID := seedFailedVoiceNote(t, ctx, h, fixture)
		err := discard_note.Setup(ctx, discard_note.Dependencies{DB: h.DB, Within: h.WithinTenant}, discard_note.Command{TenantID: fixture.TenantID, NoteID: noteID, ExpectedVersion: 2, ClientMutationID: "participant-after-close", IsParticipant: true})
		if !apperror.Is(err, apperror.InvalidState) {
			t.Fatalf("participant discard after close error=%v; want INVALID_STATE", err)
		}
	})

	t.Run("IT-355 participant cannot retry after capture closes", func(t *testing.T) {
		fixture := seedVoiceFixture(t, ctx, h, 5)
		noteID := seedFailedVoiceNote(t, ctx, h, fixture)
		err := retry_note.Setup(ctx, retry_note.Dependencies{DB: h.DB, Within: h.WithinTenant}, retry_note.Command{TenantID: fixture.TenantID, NoteID: noteID, ExpectedVersion: 2, ClientMutationID: "participant-retry-after-close", IsParticipant: true})
		if !apperror.Is(err, apperror.RetryUnavailable) {
			t.Fatalf("participant retry after close error=%v; want RETRY_UNAVAILABLE", err)
		}
	})

	t.Run("participant edit and highlight recheck the closed draft inside the transaction", func(t *testing.T) {
		fixture := seedVoiceFixture(t, ctx, h, 5)
		noteID := seedFailedVoiceNote(t, ctx, h, fixture)
		revisionID := identity.NewID()
		if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
			if err := tx.Exec("INSERT INTO transcription.transcript_revisions(tenant_id,id,note_id,revision_number,text,kind,created_at) VALUES(?,?,?,?,?,?,?)", fixture.TenantID, revisionID, noteID, 1, "texto reconhecido", "HUMAN_EDIT", time.Now().UTC()).Error; err != nil {
				return err
			}
			return tx.Exec("UPDATE transcription.audio_notes SET current_revision_id=? WHERE tenant_id=? AND id=?", revisionID, fixture.TenantID, noteID).Error
		}); err != nil {
			t.Fatal(err)
		}
		_, editErr := revise_note.Setup(ctx, revise_note.Dependencies{DB: h.DB, Within: h.WithinTenant}, revise_note.Command{TenantID: fixture.TenantID, NoteID: noteID, ExpectedVersion: 2, ClientMutationID: "participant-edit-after-close", Kind: "HUMAN_EDIT", Text: "texto corrigido", IsParticipant: true})
		if !apperror.Is(editErr, apperror.InvalidState) {
			t.Fatalf("participant edit after close error=%v; want INVALID_STATE", editErr)
		}
		_, participantErr := revise_note.Setup(ctx, revise_note.Dependencies{DB: h.DB, Within: h.WithinTenant}, revise_note.Command{TenantID: fixture.TenantID, NoteID: noteID, ExpectedVersion: 2, ClientMutationID: "participant-highlight-after-close", Kind: "HIGHLIGHT", Highlighted: true, IsParticipant: true})
		if !apperror.Is(participantErr, apperror.InvalidState) {
			t.Fatalf("participant highlight after close error=%v; want INVALID_STATE", participantErr)
		}
		if _, err := revise_note.Setup(ctx, revise_note.Dependencies{DB: h.DB, Within: h.WithinTenant}, revise_note.Command{TenantID: fixture.TenantID, NoteID: noteID, ExpectedVersion: 2, ClientMutationID: "operator-highlight-after-close", Kind: "HIGHLIGHT", Highlighted: true, HighlightSource: "OPERATOR", IsParticipant: false}); err != nil {
			t.Fatalf("operator highlight after close: %v", err)
		}
	})

	t.Run("IT-ADR-007 discarded reservation and retry attempt consume quota", func(t *testing.T) {
		t.Run("discard then new admission remains limited", func(t *testing.T) {
			fixture := seedVoiceFixture(t, ctx, h, 1)
			noteID := seedFailedVoiceNote(t, ctx, h, fixture)
			if err := discard_note.Setup(ctx, discard_note.Dependencies{DB: h.DB, Within: h.WithinTenant}, discard_note.Command{TenantID: fixture.TenantID, NoteID: noteID, ExpectedVersion: 2, ClientMutationID: "consume-then-discard"}); err != nil {
				t.Fatal(err)
			}
			if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
				return tx.Exec("UPDATE capture.capture_drafts SET status='OPEN' WHERE tenant_id=? AND id=?", fixture.TenantID, fixture.DraftID).Error
			}); err != nil {
				t.Fatal(err)
			}
			_, err := create_note_upload.Setup(ctx, create_note_upload.Dependencies{DB: h.DB, Store: voiceStore(nil), Within: h.WithinTenant}, voiceCommand(fixture, identity.NewID()))
			if !apperror.Is(err, apperror.LimitReached) {
				t.Fatalf("new admission after consumed discard error=%v; want LIMIT_REACHED", err)
			}
		})
		t.Run("retry cannot reserve beyond aggregate ceiling", func(t *testing.T) {
			fixture := seedVoiceFixture(t, ctx, h, 1)
			noteID := seedFailedVoiceNote(t, ctx, h, fixture)
			err := retry_note.Setup(ctx, retry_note.Dependencies{DB: h.DB, Within: h.WithinTenant}, retry_note.Command{TenantID: fixture.TenantID, NoteID: noteID, ExpectedVersion: 2, ClientMutationID: "quota-retry"})
			if !apperror.Is(err, apperror.LimitReached) {
				t.Fatalf("retry beyond aggregate ceiling error=%v; want LIMIT_REACHED", err)
			}
		})
	})

	t.Run("IT-completion failure after object materialization schedules cleanup", func(t *testing.T) {
		t.Run("closed capture", func(t *testing.T) {
			fixture := seedVoiceFixture(t, ctx, h, 5)
			store := voiceStore(nil)
			intent, err := create_note_upload.Setup(ctx, create_note_upload.Dependencies{DB: h.DB, Store: store, Within: h.WithinTenant}, voiceCommand(fixture, identity.NewID()))
			if err != nil {
				t.Fatal(err)
			}
			noteID, _ := identity.ParseID(intent.NoteID.String())
			if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
				return tx.Exec("UPDATE capture.capture_drafts SET status='SUBMITTED' WHERE tenant_id=? AND id=?", fixture.TenantID, fixture.DraftID).Error
			}); err != nil {
				t.Fatal(err)
			}
			err = complete_note_upload.Setup(ctx, complete_note_upload.Dependencies{DB: h.DB, Store: store, Within: h.WithinTenant}, complete_note_upload.Command{TenantID: fixture.TenantID, NoteID: noteID, UploadProof: intent.UploadProof})
			if !apperror.Is(err, apperror.InvalidState) {
				t.Fatalf("completion after closure error=%v; want INVALID_STATE", err)
			}
			assertAudioDeletionScheduled(t, ctx, h, fixture.TenantID, noteID)
		})
		t.Run("note expired during object verification", func(t *testing.T) {
			fixture := seedVoiceFixture(t, ctx, h, 5)
			store := voiceStore(nil)
			intent, err := create_note_upload.Setup(ctx, create_note_upload.Dependencies{DB: h.DB, Store: store, Within: h.WithinTenant}, voiceCommand(fixture, identity.NewID()))
			if err != nil {
				t.Fatal(err)
			}
			noteID, _ := identity.ParseID(intent.NoteID.String())
			objectClient := store.Client.(*transcriptionObjectClient)
			objectClient.onHead = func(string) {
				if hookErr := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
					if err := tx.Exec("UPDATE transcription.audio_notes SET status='EXPIRED',audio_deletion_status='PROCESSING' WHERE tenant_id=? AND id=?", fixture.TenantID, noteID).Error; err != nil {
						return err
					}
					now := time.Now().UTC()
					return tx.Exec("INSERT INTO transcription.audio_deletions(tenant_id,id,note_id,object_generation,object_key,derivative_keys,status,lease_token,lease_deadline,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)", fixture.TenantID, identity.NewID(), noteID, 1, "late-object", []byte("[]"), "PROCESSING", identity.NewID(), now.Add(time.Minute), now, now).Error
				}); hookErr != nil {
					t.Errorf("expire note during object verification: %v", hookErr)
				}
			}
			err = complete_note_upload.Setup(ctx, complete_note_upload.Dependencies{DB: h.DB, Store: store, Within: h.WithinTenant}, complete_note_upload.Command{TenantID: fixture.TenantID, NoteID: noteID, UploadProof: intent.UploadProof})
			if !apperror.Is(err, apperror.InvalidState) {
				t.Fatalf("completion after expiry error=%v; want INVALID_STATE", err)
			}
			assertAudioDeletionScheduled(t, ctx, h, fixture.TenantID, noteID)
		})
		t.Run("deleted obligation is reopened when late object is materialized", func(t *testing.T) {
			fixture := seedVoiceFixture(t, ctx, h, 5)
			store := voiceStore(nil)
			intent, err := create_note_upload.Setup(ctx, create_note_upload.Dependencies{DB: h.DB, Store: store, Within: h.WithinTenant}, voiceCommand(fixture, identity.NewID()))
			if err != nil {
				t.Fatal(err)
			}
			noteID, _ := identity.ParseID(intent.NoteID.String())
			objectClient := store.Client.(*transcriptionObjectClient)
			objectClient.onHead = func(key string) {
				if hookErr := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
					if err := tx.Exec("UPDATE transcription.audio_notes SET status='CANCELED',audio_deletion_status='DELETED' WHERE tenant_id=? AND id=?", fixture.TenantID, noteID).Error; err != nil {
						return err
					}
					return tx.Exec("INSERT INTO transcription.audio_deletions(tenant_id,id,note_id,object_generation,object_key,derivative_keys,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)", fixture.TenantID, identity.NewID(), noteID, 1, key, []byte("[]"), "DELETED", time.Now().UTC(), time.Now().UTC()).Error
				}); hookErr != nil {
					t.Errorf("prepare late-object deletion race: %v", hookErr)
				}
			}
			if err := complete_note_upload.Setup(ctx, complete_note_upload.Dependencies{DB: h.DB, Store: store, Within: h.WithinTenant}, complete_note_upload.Command{TenantID: fixture.TenantID, NoteID: noteID, UploadProof: intent.UploadProof}); err != nil {
				t.Fatalf("complete late materialized object: %v", err)
			}
			assertAudioDeletionScheduled(t, ctx, h, fixture.TenantID, noteID)
		})
	})
}

func assertAudioDeletionScheduled(t *testing.T, ctx context.Context, h *Harness, tenantID, noteID identity.ID) {
	t.Helper()
	var deletionStatus, noteStatus string
	if err := h.WithinTenant(ctx, tenantID, func(tx *gorm.DB) error {
		if err := tx.Raw("SELECT status FROM transcription.audio_deletions WHERE tenant_id=? AND note_id=?", tenantID, noteID).Row().Scan(&deletionStatus); err != nil {
			return err
		}
		return tx.Raw("SELECT audio_deletion_status FROM transcription.audio_notes WHERE tenant_id=? AND id=?", tenantID, noteID).Row().Scan(&noteStatus)
	}); err != nil {
		t.Fatal(err)
	}
	if deletionStatus != "PENDING" || noteStatus != "SCHEDULED" {
		t.Fatalf("deletion=%s note cleanup=%s; want PENDING/SCHEDULED", deletionStatus, noteStatus)
	}
}

func seedFailedVoiceNote(t *testing.T, ctx context.Context, h *Harness, fixture Task06Fixture) identity.ID {
	t.Helper()
	store := voiceStore([]byte{0x1a, 0x45, 0xdf, 0xa3, 1, 2, 3, 4})
	intent, err := create_note_upload.Setup(ctx, create_note_upload.Dependencies{DB: h.DB, Store: store, Within: h.WithinTenant}, voiceCommand(fixture, identity.NewID()))
	if err != nil {
		t.Fatal(err)
	}
	noteID, err := identity.ParseID(intent.NoteID.String())
	if err != nil {
		t.Fatal(err)
	}
	if err := complete_note_upload.Setup(ctx, complete_note_upload.Dependencies{DB: h.DB, Store: store, Within: h.WithinTenant}, complete_note_upload.Command{TenantID: fixture.TenantID, NoteID: noteID, UploadProof: intent.UploadProof}); err != nil {
		t.Fatal(err)
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		if err := tx.Exec("UPDATE capture.capture_drafts SET status='SUBMITTED' WHERE tenant_id=? AND id=?", fixture.TenantID, fixture.DraftID).Error; err != nil {
			return err
		}
		if err := tx.Exec("UPDATE transcription.audio_notes SET status='FAILED',version=2 WHERE tenant_id=? AND id=?", fixture.TenantID, noteID).Error; err != nil {
			return err
		}
		return tx.Exec("UPDATE transcription.jobs SET status='FAILED' WHERE tenant_id=? AND note_id=?", fixture.TenantID, noteID).Error
	}); err != nil {
		t.Fatal(err)
	}
	return noteID
}

func seedVoiceFixture(t *testing.T, ctx context.Context, h *Harness, tenantLimit int) Task06Fixture {
	t.Helper()
	f, err := h.SeedTask06Fixture(ctx, Task06FixtureOptions{MediaKind: "VIDEO", SchemaVersion: 2})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
		if err := tx.Model(&database.Responsibility{}).Where("tenant_id=? AND id=?", f.TenantID, f.ResponsibilityID).Update("status", "PENDING").Error; err != nil {
			return err
		}
		if err := tx.Model(&database.CaptureDraft{}).Where("tenant_id=? AND id=?", f.TenantID, f.DraftID).Update("status", "OPEN").Error; err != nil {
			return err
		}
		if err := tx.Model(&database.VideoAttempt{}).Where("tenant_id=? AND id=?", f.TenantID, f.VideoAttemptID).Update("status", "ACTIVE").Error; err != nil {
			return err
		}
		if err := tx.Exec(`INSERT INTO transcription.tenant_policies(tenant_id,enabled,tenant_note_limit,tenant_audio_limit_ms,inspection_note_limit,inspection_audio_limit_ms,version,created_at,updated_at) VALUES(?,true,?,?,10,60000,1,?,?)`, f.TenantID, tenantLimit, 60000, now, now).Error; err != nil {
			return err
		}
		return tx.Create(&database.ProcessingAcceptance{ID: identity.NewID(), TenantID: f.TenantID, ResponsibilityID: f.ResponsibilityID, DisclosureVersion: "pt-BR-1", PhotoProcessing: true, VideoProcessing: true, VoiceProcessing: true, VoiceConsentVersion: "voice-v1", AIAnalysis: true, GPSUse: true, AcceptedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	return f
}

func seedSecondVoiceInspection(t *testing.T, ctx context.Context, h *Harness, original Task06Fixture) Task06Fixture {
	t.Helper()
	second := original
	second.InspectionID, second.ResponsibilityID, second.DraftID, second.VideoAttemptID = identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	if err := h.WithinTenant(ctx, original.TenantID, func(tx *gorm.DB) error {
		var inspection database.Inspection
		if err := tx.Where("tenant_id=? AND id=?", original.TenantID, original.InspectionID).Take(&inspection).Error; err != nil {
			return err
		}
		inspection.ID = second.InspectionID
		inspection.SourceKey = "transcription-it306-" + second.InspectionID.String()
		inspection.CreatedAt = time.Now().UTC()
		inspection.UpdatedAt = inspection.CreatedAt
		if err := tx.Create(&inspection).Error; err != nil {
			return err
		}
		var responsibility database.Responsibility
		if err := tx.Where("tenant_id=? AND id=?", original.TenantID, original.ResponsibilityID).Take(&responsibility).Error; err != nil {
			return err
		}
		responsibility.ID = second.ResponsibilityID
		responsibility.InspectionID = second.InspectionID
		responsibility.CreatedAt = inspection.CreatedAt
		responsibility.UpdatedAt = inspection.CreatedAt
		if err := tx.Create(&responsibility).Error; err != nil {
			return err
		}
		var draft database.CaptureDraft
		if err := tx.Where("tenant_id=? AND id=?", original.TenantID, original.DraftID).Take(&draft).Error; err != nil {
			return err
		}
		draft.ID = second.DraftID
		draft.ResponsibilityID = second.ResponsibilityID
		draft.CreatedAt = inspection.CreatedAt
		draft.UpdatedAt = inspection.CreatedAt
		if err := tx.Create(&draft).Error; err != nil {
			return err
		}
		var attempt database.VideoAttempt
		if err := tx.Where("tenant_id=? AND id=?", original.TenantID, original.VideoAttemptID).Take(&attempt).Error; err != nil {
			return err
		}
		attempt.ID = second.VideoAttemptID
		attempt.ResponsibilityID = second.ResponsibilityID
		attempt.IdempotencyKey = identity.NewID().String()
		attempt.AdmittedAt = inspection.CreatedAt
		attempt.CreatedAt = inspection.CreatedAt
		attempt.ExpiresAt = inspection.CreatedAt.Add(time.Hour)
		if err := tx.Create(&attempt).Error; err != nil {
			return err
		}
		var acceptance database.ProcessingAcceptance
		if err := tx.Where("tenant_id=? AND responsibility_id=?", original.TenantID, original.ResponsibilityID).Take(&acceptance).Error; err != nil {
			return err
		}
		acceptance.ID = identity.NewID()
		acceptance.ResponsibilityID = second.ResponsibilityID
		acceptance.AcceptedAt = inspection.CreatedAt
		return tx.Create(&acceptance).Error
	}); err != nil {
		t.Fatal(err)
	}
	return second
}

func voiceCommand(f Task06Fixture, id identity.ID) create_note_upload.Command {
	start, end := int64(500), int64(1500)
	data := []byte{0x1a, 0x45, 0xdf, 0xa3, 1, 2, 3, 4}
	digest := sha256.Sum256(data)
	return create_note_upload.Command{TenantID: f.TenantID, ResponsibilityID: f.ResponsibilityID, ClientNoteID: id.String(), RequirementKey: voiceRequirementKey(f), VideoAttemptID: &f.VideoAttemptID, StartMS: &start, EndMS: &end, TimeBasis: "CAPTURE_CLOCK", ConsentVersion: "voice-v1", DisclosureVersion: "pt-BR-1", ContentType: "audio/webm", ContentSHA256: hex.EncodeToString(digest[:]), SizeBytes: int64(len(data)), DurationMS: 1000}
}

func voiceRequirementKey(f Task06Fixture) string {
	return "env:new:" + f.EnvironmentIdentityID.String()
}

func voiceStore(data []byte) objectstore.Store {
	if data == nil {
		data = []byte{0x1a, 0x45, 0xdf, 0xa3, 1, 2, 3, 4}
	}
	client := &transcriptionObjectClient{data: data}
	return objectstore.Store{Bucket: "private", Client: client, AudioProbe: func(context.Context, []byte) (time.Duration, error) { return time.Second, nil }}
}
