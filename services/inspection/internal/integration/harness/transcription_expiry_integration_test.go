//go:build integration

package harness

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/transcription/create_note_upload"
	"inspection/services/inspection/internal/features/transcription/discard_note"
	"inspection/services/inspection/internal/features/transcription/expire_audio"
	"inspection/services/inspection/internal/features/transcription/get_note"
	"inspection/services/inspection/internal/features/transcription/process_note"
	"inspection/services/inspection/internal/features/transcription/retry_note"
	"inspection/services/inspection/internal/features/transcription/revise_note"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/llm"
	"inspection/services/inspection/internal/platform/objectstore"

	"gorm.io/gorm"
)

type expiryObjectClient struct {
	transcriptionObjectClient
	mu       sync.Mutex
	failKeys map[string]bool
	deleted  []string
	called   []string
	aborted  []string
}

func (c *expiryObjectClient) Delete(_ context.Context, _, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.called = append(c.called, key)
	if c.failKeys[key] {
		return errors.New("storage unavailable")
	}
	c.deleted = append(c.deleted, key)
	return nil
}

func (c *expiryObjectClient) AbortMultipart(_ context.Context, _, key, uploadID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.aborted = append(c.aborted, key+":"+uploadID)
	return nil
}

func (c *expiryObjectClient) setFailure(key string, fail bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failKeys == nil {
		c.failKeys = map[string]bool{}
	}
	c.failKeys[key] = fail
}

func (c *expiryObjectClient) deleteCalls(key string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	count := 0
	for _, called := range c.called {
		if called == key {
			count++
		}
	}
	return count
}

func (c *expiryObjectClient) wasDeleted(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Contains(c.deleted, key)
}

func (c *expiryObjectClient) wasAborted(entry string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Contains(c.aborted, entry)
}

type expiryNoteState struct {
	Status              string
	AudioDeletionStatus string
	SafeFailureCode     *string
	Version             int
	UploadKey           *string
	CurrentRevisionID   *identity.ID
}

// Cases assigned to task_03 part C. The remaining US-021 cases are surfaces
// owned elsewhere and are only covered here through their scheduler effects:
// IT-279 (consent disclosure copy) and IT-282/IT-288 (removing local IndexedDB
// audio after durable receipt) belong to the Capture UI in task_04; IT-283's
// "original unavailable" message and IT-286's participant authorization belong
// to the audioNote GraphQL/operational surface in task_03 part D/task_07. The
// backend half of IT-283 (no retry of deleted audio) and IT-284 (an abandoned
// upload expires without a transcription event) is asserted below.
func TestExpireAudioSchedulerIT268IT271IT276IT279To292(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	h, err := NewFromEnv(ctx)
	if err != nil {
		t.Skipf("integration dependencies unavailable: %v", err)
	}
	defer h.Close()
	var exists bool
	if err := h.MigrationDB.Raw("SELECT to_regprocedure('transcription.discover_due_tenants(interval)') IS NOT NULL").Row().Scan(&exists); err != nil || !exists {
		t.Skip("transcription scheduler migrations are not installed")
	}
	client := &expiryObjectClient{}
	store := objectstore.Store{Bucket: "private", Client: client}
	executor, err := expire_audio.Setup(expire_audio.Dependencies{DB: h.DB, Store: store, Within: h.WithinTenant})
	if err != nil {
		t.Fatal(err)
	}
	repo, err := process_note.NewRepository(process_note.RepositoryDependencies{DB: h.DB, Within: h.WithinTenant})
	if err != nil {
		t.Fatal(err)
	}
	exec := func(t *testing.T, tenant identity.ID, query string, args ...any) {
		t.Helper()
		if err := h.WithinTenant(ctx, tenant, func(tx *gorm.DB) error { return tx.Exec(query, args...).Error }); err != nil {
			t.Fatal(err)
		}
	}
	scalar := func(t *testing.T, tenant identity.ID, query string, args ...any) int64 {
		t.Helper()
		var value int64
		if err := h.WithinTenant(ctx, tenant, func(tx *gorm.DB) error { return tx.Raw(query, args...).Row().Scan(&value) }); err != nil {
			t.Fatal(err)
		}
		return value
	}
	note := func(t *testing.T, tenant, noteID identity.ID) expiryNoteState {
		t.Helper()
		var state expiryNoteState
		if err := h.WithinTenant(ctx, tenant, func(tx *gorm.DB) error {
			return tx.Raw("SELECT status,audio_deletion_status,safe_failure_code,version,upload_key,current_revision_id FROM transcription.audio_notes WHERE tenant_id=? AND id=?", tenant, noteID).Scan(&state).Error
		}); err != nil {
			t.Fatal(err)
		}
		return state
	}
	jobStatus := func(t *testing.T, tenant, jobID identity.ID) string {
		t.Helper()
		var status string
		if err := h.WithinTenant(ctx, tenant, func(tx *gorm.DB) error {
			return tx.Raw("SELECT status FROM transcription.jobs WHERE tenant_id=? AND id=?", tenant, jobID).Row().Scan(&status)
		}); err != nil {
			t.Fatal(err)
		}
		return status
	}
	outbox := func(t *testing.T, tenant, noteID identity.ID, typ string) int64 {
		t.Helper()
		return scalar(t, tenant, "SELECT count(*) FROM messaging.outbox WHERE tenant_id=? AND type=? AND payload->'payload'->>'noteId'=?", tenant, typ, noteID.String())
	}
	modelCalls := func(t *testing.T, tenant, jobID identity.ID) int64 {
		t.Helper()
		return scalar(t, tenant, "SELECT count(*) FROM usage.llm_calls WHERE tenant_id=? AND job_id=? AND comparison_mode='TRANSCRIPTION'", tenant, jobID)
	}
	runDue := func(t *testing.T, tenant identity.ID) expire_audio.Counts {
		t.Helper()
		counts, err := executor.RunDue(ctx, tenant, 50)
		if err != nil {
			t.Fatal(err)
		}
		return counts
	}
	runWorker := func(f Task06Fixture, jobID identity.ID, generation int, gateway *setupGateway) error {
		ledger, err := process_note.NewAttemptLedger(h.DB, "mock")
		if err != nil {
			return err
		}
		return process_note.Setup(ctx, process_note.Dependencies{Repository: repo, Audio: setupAudio{}, Gateway: gateway, Ledger: ledger}, jobID, f.TenantID, generation)
	}
	transcribed := func() *setupGateway {
		return &setupGateway{result: llm.TranscriptionResult{Transcript: "duas cadeiras de madeira", Usage: llm.StructuredResult{TransportDelivered: true}}}
	}
	ageUpload := func(t *testing.T, tenant, noteID identity.ID, age string) {
		t.Helper()
		exec(t, tenant, "UPDATE transcription.audio_notes SET created_at=now()-?::interval WHERE tenant_id=? AND id=?", age, tenant, noteID)
		exec(t, tenant, "UPDATE transcription.jobs SET created_at=now()-?::interval WHERE tenant_id=? AND note_id=? AND generation=1", age, tenant, noteID)
	}
	// failAttempt claims the job and fails it through the worker repository, so
	// the note and job reach their failure states by the production code path.
	failAttempt := func(t *testing.T, f Task06Fixture, jobID identity.ID, generation int, uncertain bool) {
		t.Helper()
		claimed, err := repo.Claim(ctx, jobID, f.TenantID, generation)
		if err != nil {
			t.Fatalf("claim before a failed attempt: %v", err)
		}
		if err := repo.Fail(ctx, claimed, "TRANSCRIPTION_FAILED", uncertain); err != nil {
			t.Fatalf("worker failure: %v", err)
		}
	}
	makeDue := func(t *testing.T, tenant, noteID identity.ID) {
		t.Helper()
		exec(t, tenant, "UPDATE transcription.audio_deletions SET retry_at=now()-interval '1 second' WHERE tenant_id=? AND note_id=?", tenant, noteID)
	}

	t.Run("UT-013 IT-276 IT-280 cleanup waits for the durable result and then deletes the audio", func(t *testing.T) {
		f := seedVoiceFixture(t, ctx, h, 10)
		intent, jobID := createProcessableNote(t, ctx, h, f)
		key := audioObjectKey(t, ctx, h, f.TenantID, intent.NoteID)
		// An obligation that reaches the scan before the transcript commit is
		// durable must keep the audio until a terminal decision exists.
		exec(t, f.TenantID, "INSERT INTO transcription.audio_deletions(tenant_id,id,note_id,object_generation,object_key,derivative_keys,status,retry_at,created_at,updated_at) VALUES(?,?,?,1,?,'[]'::jsonb,'PENDING',now(),now(),now())", f.TenantID, identity.NewID(), intent.NoteID, key)
		if counts := runDue(t, f.TenantID); counts.Deleted != 0 || client.deleteCalls(key) != 0 {
			t.Fatalf("audio deleted before a durable result: counts=%+v", counts)
		}
		assertAudioDeletion(t, ctx, h, f.TenantID, intent.NoteID, "PENDING", 0)
		gateway := transcribed()
		if err := runWorker(f, jobID, 1, gateway); err != nil {
			t.Fatal(err)
		}
		if state := note(t, f.TenantID, intent.NoteID); state.Status != "COMPLETED" || state.AudioDeletionStatus != "SCHEDULED" {
			t.Fatalf("completed note=%+v", state)
		}
		counts := runDue(t, f.TenantID)
		if counts.Deleted != 1 || !client.wasDeleted(key) {
			t.Fatalf("cleanup counts=%+v deleted=%t", counts, client.wasDeleted(key))
		}
		assertAudioDeletion(t, ctx, h, f.TenantID, intent.NoteID, "DELETED", 0)
		read, err := get_note.Setup(ctx, get_note.Dependencies{DB: h.DB, Within: h.WithinTenant}, get_note.Command{TenantID: f.TenantID, NoteID: intent.NoteID})
		if err != nil {
			t.Fatal(err)
		}
		if read.Status != "COMPLETED" || read.AudioDeletionStatus != "DELETED" || read.CurrentRevisionID == nil || len(read.Revisions) != 1 || read.Revisions[0].Text != "duas cadeiras de madeira" {
			t.Fatalf("read after deletion=%+v", read)
		}
		if state := note(t, f.TenantID, intent.NoteID); state.UploadKey != nil {
			t.Fatalf("deleted audio key is still referenced: %+v", state)
		}
		if gateway.Calls() != 1 || modelCalls(t, f.TenantID, jobID) != 1 {
			t.Fatalf("gateway calls=%d ledger calls=%d", gateway.Calls(), modelCalls(t, f.TenantID, jobID))
		}
	})

	t.Run("IT-268 UT-014 IT-289 cleanup failure is retried with backoff and never retranscribes", func(t *testing.T) {
		f := seedVoiceFixture(t, ctx, h, 10)
		intent, jobID := createProcessableNote(t, ctx, h, f)
		key := audioObjectKey(t, ctx, h, f.TenantID, intent.NoteID)
		gateway := transcribed()
		if err := runWorker(f, jobID, 1, gateway); err != nil {
			t.Fatal(err)
		}
		requestsBefore := outbox(t, f.TenantID, intent.NoteID, "transcription.requested.v1")
		versionBefore := note(t, f.TenantID, intent.NoteID).Version
		client.setFailure(key, true)
		if counts := runDue(t, f.TenantID); counts.Failed != 1 || counts.Deleted != 0 {
			t.Fatalf("failed cleanup counts=%+v", counts)
		}
		assertAudioDeletion(t, ctx, h, f.TenantID, intent.NoteID, "FAILED", 1)
		if scalar(t, f.TenantID, "SELECT count(*) FROM transcription.audio_deletions WHERE tenant_id=? AND note_id=? AND retry_at>now()+interval '30 seconds'", f.TenantID, intent.NoteID) != 1 {
			t.Fatal("failed cleanup was not rescheduled with backoff")
		}
		if state := note(t, f.TenantID, intent.NoteID); state.Status != "COMPLETED" || state.AudioDeletionStatus != "FAILED" || state.Version != versionBefore {
			t.Fatalf("note after cleanup failure=%+v", state)
		}
		if calls := client.deleteCalls(key); runDue(t, f.TenantID).Failed != 0 || client.deleteCalls(key) != calls {
			t.Fatal("cleanup retried before its backoff elapsed")
		}
		client.setFailure(key, false)
		makeDue(t, f.TenantID, intent.NoteID)
		if counts := runDue(t, f.TenantID); counts.Deleted != 1 {
			t.Fatalf("retried cleanup counts=%+v", counts)
		}
		assertAudioDeletion(t, ctx, h, f.TenantID, intent.NoteID, "DELETED", 1)
		state := note(t, f.TenantID, intent.NoteID)
		if state.Status != "COMPLETED" || state.AudioDeletionStatus != "DELETED" || state.CurrentRevisionID == nil {
			t.Fatalf("note after cleanup retry=%+v", state)
		}
		if jobStatus(t, f.TenantID, jobID) != "COMPLETED" || gateway.Calls() != 1 || modelCalls(t, f.TenantID, jobID) != 1 || outbox(t, f.TenantID, intent.NoteID, "transcription.requested.v1") != requestsBefore {
			t.Fatalf("cleanup retry touched transcription: job=%s gateway=%d", jobStatus(t, f.TenantID, jobID), gateway.Calls())
		}
		deleteCalls := client.deleteCalls(key)
		makeDue(t, f.TenantID, intent.NoteID)
		runDue(t, f.TenantID)
		if client.deleteCalls(key) != deleteCalls {
			t.Fatal("a deleted object was sent to storage again")
		}
	})

	t.Run("IT-271 IT-281 IT-285 accepted audio expires 24 hours after upload completion", func(t *testing.T) {
		f := seedVoiceFixture(t, ctx, h, 10)
		fresh, freshJob := createProcessableNote(t, ctx, h, f)
		stale, staleJob := createProcessableNote(t, ctx, h, f)
		ageUpload(t, f.TenantID, fresh.NoteID, "23 hours")
		ageUpload(t, f.TenantID, stale.NoteID, "25 hours")
		staleKey := audioObjectKey(t, ctx, h, f.TenantID, stale.NoteID)
		tenants, err := executor.DueTenants(ctx)
		if err != nil || !slices.Contains(tenants, f.TenantID) {
			t.Fatalf("scheduler discovery missed a transcription-only tenant: %v err=%v", tenants, err)
		}
		counts := runDue(t, f.TenantID)
		if counts.Expired != 1 || counts.Deleted != 1 {
			t.Fatalf("expiry counts=%+v", counts)
		}
		state := note(t, f.TenantID, stale.NoteID)
		if state.Status != "EXPIRED" || state.SafeFailureCode == nil || *state.SafeFailureCode != "AUDIO_EXPIRED" || state.AudioDeletionStatus != "DELETED" || !client.wasDeleted(staleKey) {
			t.Fatalf("expired note=%+v deleted=%t", state, client.wasDeleted(staleKey))
		}
		if jobStatus(t, f.TenantID, staleJob) != "EXPIRED" || outbox(t, f.TenantID, stale.NoteID, "transcription.failed.v1") != 1 {
			t.Fatal("expired job did not stop with a terminal failure event")
		}
		if fresh := note(t, f.TenantID, fresh.NoteID); fresh.Status != "QUEUED" || jobStatus(t, f.TenantID, freshJob) != "QUEUED" {
			t.Fatalf("note inside the 24-hour window expired: %+v", fresh)
		}
		err = retry_note.Setup(ctx, retry_note.Dependencies{DB: h.DB, Within: h.WithinTenant}, retry_note.Command{TenantID: f.TenantID, NoteID: stale.NoteID, ExpectedVersion: state.Version, ClientMutationID: identity.NewID().String()})
		if !apperror.Is(err, apperror.Expired) {
			t.Fatalf("retry of expired audio error=%v, want EXPIRED", err)
		}
		gateway := transcribed()
		if err := runWorker(f, staleJob, 1, gateway); err == nil || gateway.Calls() != 0 {
			t.Fatalf("expired job was processed: err=%v gateway=%d", err, gateway.Calls())
		}
	})

	t.Run("IT-281 a manual retry does not extend the 24-hour window", func(t *testing.T) {
		f := seedVoiceFixture(t, ctx, h, 10)
		intent, jobID := createProcessableNote(t, ctx, h, f)
		// A real worker failure that is not retried automatically leaves the note FAILED.
		failAttempt(t, f, jobID, 1, true)
		if state := note(t, f.TenantID, intent.NoteID); state.Status != "FAILED" || jobStatus(t, f.TenantID, jobID) != "FAILED" {
			t.Fatalf("worker failure left note=%+v job=%s", state, jobStatus(t, f.TenantID, jobID))
		}
		// One hour before the bound the user may still retry, through the API slice.
		ageUpload(t, f.TenantID, intent.NoteID, "23 hours")
		retryCommand := func() retry_note.Command {
			return retry_note.Command{TenantID: f.TenantID, NoteID: intent.NoteID, ExpectedVersion: note(t, f.TenantID, intent.NoteID).Version, ClientMutationID: identity.NewID().String()}
		}
		if err := retry_note.Setup(ctx, retry_note.Dependencies{DB: h.DB, Within: h.WithinTenant}, retryCommand()); err != nil {
			t.Fatalf("retry inside the window: %v", err)
		}
		if state := note(t, f.TenantID, intent.NoteID); state.Status != "QUEUED" || scalar(t, f.TenantID, "SELECT count(*) FROM transcription.jobs WHERE tenant_id=? AND note_id=? AND generation=2 AND status='QUEUED'", f.TenantID, intent.NoteID) != 1 {
			t.Fatalf("retry did not queue generation 2: %+v", state)
		}
		if counts := runDue(t, f.TenantID); counts.Expired != 0 {
			t.Fatalf("a retried note inside the window expired: %+v", counts)
		}
		// The retry created a fresh job, yet the window still counts from generation 1.
		ageUpload(t, f.TenantID, intent.NoteID, "25 hours")
		if counts := runDue(t, f.TenantID); counts.Expired != 1 {
			t.Fatalf("retried note counts=%+v", counts)
		}
		if state := note(t, f.TenantID, intent.NoteID); state.Status != "EXPIRED" {
			t.Fatalf("retried note=%+v", state)
		}
		if jobStatus(t, f.TenantID, jobID) != "FAILED" || scalar(t, f.TenantID, "SELECT count(*) FROM transcription.jobs WHERE tenant_id=? AND note_id=? AND generation=2 AND status='EXPIRED'", f.TenantID, intent.NoteID) != 1 {
			t.Fatal("expiry rewrote the failed attempt or left the retry generation runnable")
		}
	})

	t.Run("IT-281 a retry after the 24-hour window is refused", func(t *testing.T) {
		f := seedVoiceFixture(t, ctx, h, 10)
		intent, jobID := createProcessableNote(t, ctx, h, f)
		failAttempt(t, f, jobID, 1, true)
		// The API checks the bound with the database clock, before any sweep ran.
		ageUpload(t, f.TenantID, intent.NoteID, "25 hours")
		err := retry_note.Setup(ctx, retry_note.Dependencies{DB: h.DB, Within: h.WithinTenant}, retry_note.Command{TenantID: f.TenantID, NoteID: intent.NoteID, ExpectedVersion: note(t, f.TenantID, intent.NoteID).Version, ClientMutationID: identity.NewID().String()})
		if !apperror.Is(err, apperror.RetryUnavailable) {
			t.Fatalf("late retry error=%v, want RETRY_UNAVAILABLE", err)
		}
		if scalar(t, f.TenantID, "SELECT count(*) FROM transcription.jobs WHERE tenant_id=? AND note_id=? AND generation>1", f.TenantID, intent.NoteID) != 0 {
			t.Fatal("a refused retry created a generation")
		}
	})

	t.Run("IT-281 IT-284 an abandoned upload expires and aborts its multipart upload", func(t *testing.T) {
		f := seedVoiceFixture(t, ctx, h, 10)
		intent, err := create_note_upload.Setup(ctx, create_note_upload.Dependencies{DB: h.DB, Store: voiceStore(nil), Within: h.WithinTenant}, voiceCommand(f, identity.NewID()))
		if err != nil {
			t.Fatal(err)
		}
		key := audioObjectKey(t, ctx, h, f.TenantID, intent.NoteID)
		exec(t, f.TenantID, "UPDATE transcription.audio_notes SET created_at=now()-interval '23 hours' WHERE tenant_id=? AND id=?", f.TenantID, intent.NoteID)
		if runDue(t, f.TenantID).Expired != 0 {
			t.Fatal("upload intent expired before 24 hours")
		}
		exec(t, f.TenantID, "UPDATE transcription.audio_notes SET created_at=now()-interval '25 hours' WHERE tenant_id=? AND id=?", f.TenantID, intent.NoteID)
		if counts := runDue(t, f.TenantID); counts.Expired != 1 || counts.Deleted != 1 {
			t.Fatalf("abandoned upload counts=%+v", counts)
		}
		if !client.wasAborted(key + ":" + intent.UploadProof) {
			t.Fatal("pending multipart upload was not aborted")
		}
		if state := note(t, f.TenantID, intent.NoteID); state.Status != "EXPIRED" || state.AudioDeletionStatus != "DELETED" {
			t.Fatalf("abandoned upload=%+v", state)
		}
		if outbox(t, f.TenantID, intent.NoteID, "transcription.failed.v1") != 0 || scalar(t, f.TenantID, "SELECT count(*) FROM transcription.jobs WHERE tenant_id=? AND note_id=?", f.TenantID, intent.NoteID) != 0 {
			t.Fatal("an upload that never completed produced transcription work")
		}
	})

	t.Run("IT-271 lease recovery returns work to retry or stops at the attempt limit", func(t *testing.T) {
		f := seedVoiceFixture(t, ctx, h, 10)
		retryable, retryJob := createProcessableNote(t, ctx, h, f)
		exhausted, exhaustedJob := createProcessableNote(t, ctx, h, f)
		claimed, err := repo.Claim(ctx, retryJob, f.TenantID, 1)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := repo.Claim(ctx, exhaustedJob, f.TenantID, 1); err != nil {
			t.Fatal(err)
		}
		exec(t, f.TenantID, "UPDATE transcription.jobs SET attempts=3 WHERE tenant_id=? AND id=?", f.TenantID, exhaustedJob)
		exec(t, f.TenantID, "UPDATE transcription.jobs SET lease_deadline=now()-interval '1 second' WHERE tenant_id=? AND id IN (?,?)", f.TenantID, retryJob, exhaustedJob)
		counts := runDue(t, f.TenantID)
		if counts.Recovered != 1 || counts.Exhausted != 1 || counts.Requeued != 1 {
			t.Fatalf("lease recovery counts=%+v", counts)
		}
		if err := repo.Complete(ctx, claimed, "", "resposta atrasada", false); err == nil {
			t.Fatal("stale worker completed after scheduler recovery")
		}
		if state := note(t, f.TenantID, retryable.NoteID); state.Status != "RETRY_WAIT" || jobStatus(t, f.TenantID, retryJob) != "RETRY_WAIT" {
			t.Fatalf("recovered note=%+v", state)
		}
		state := note(t, f.TenantID, exhausted.NoteID)
		if state.Status != "FAILED" || state.SafeFailureCode == nil || *state.SafeFailureCode != "RETRY_LIMIT_EXCEEDED" || jobStatus(t, f.TenantID, exhaustedJob) != "FAILED" {
			t.Fatalf("exhausted note=%+v", state)
		}
		if outbox(t, f.TenantID, exhausted.NoteID, "transcription.failed.v1") != 1 {
			t.Fatal("exhausted retry did not emit one terminal failure")
		}
		gateway := transcribed()
		if err := runWorker(f, retryJob, 1, gateway); err != nil {
			t.Fatal(err)
		}
		if note(t, f.TenantID, retryable.NoteID).Status != "COMPLETED" || gateway.Calls() != 1 {
			t.Fatal("recovered job did not complete through the worker")
		}
	})

	t.Run("lease recovery with a staged result completes without calling the provider again", func(t *testing.T) {
		f := seedVoiceFixture(t, ctx, h, 10)
		intent, jobID := createProcessableNote(t, ctx, h, f)
		key := audioObjectKey(t, ctx, h, f.TenantID, intent.NoteID)
		active, err := repo.Claim(ctx, jobID, f.TenantID, 1)
		if err != nil {
			t.Fatal(err)
		}
		ledger, err := process_note.NewAttemptLedger(h.DB, "mock")
		if err != nil {
			t.Fatal(err)
		}
		callID, err := ledger.Start(ctx, active)
		if err != nil {
			t.Fatal(err)
		}
		if err := repo.StageResult(ctx, active, callID, "resultado já pago"); err != nil {
			t.Fatal(err)
		}
		if err := ledger.Finish(ctx, callID, llm.StructuredResult{TransportDelivered: true}, "SUCCEEDED", active.DurationMS); err != nil {
			t.Fatal(err)
		}
		exec(t, f.TenantID, "UPDATE transcription.jobs SET attempts=3,lease_deadline=now()-interval '1 second' WHERE tenant_id=? AND id=?", f.TenantID, jobID)
		ageUpload(t, f.TenantID, intent.NoteID, "25 hours")
		requests := outbox(t, f.TenantID, intent.NoteID, "transcription.requested.v1")
		if counts := runDue(t, f.TenantID); counts.Recovered != 1 || counts.Exhausted != 0 || counts.Expired != 0 || counts.Requeued != 1 {
			t.Fatalf("staged recovery counts=%+v", counts)
		}
		if outbox(t, f.TenantID, intent.NoteID, "transcription.requested.v1") != requests+1 {
			t.Fatal("staged result was not republished for the worker")
		}
		gateway := transcribed()
		if err := runWorker(f, jobID, 1, gateway); err != nil {
			t.Fatal(err)
		}
		state := note(t, f.TenantID, intent.NoteID)
		if state.Status != "COMPLETED" || gateway.Calls() != 0 || modelCalls(t, f.TenantID, jobID) != 1 {
			t.Fatalf("staged completion note=%+v gateway=%d", state, gateway.Calls())
		}
		if counts := runDue(t, f.TenantID); counts.Deleted != 1 || !client.wasDeleted(key) {
			t.Fatalf("staged completion cleanup counts=%+v", counts)
		}
	})

	t.Run("lease recovery past retention expires instead of waking the worker", func(t *testing.T) {
		f := seedVoiceFixture(t, ctx, h, 10)
		intent, jobID := createProcessableNote(t, ctx, h, f)
		if _, err := repo.Claim(ctx, jobID, f.TenantID, 1); err != nil {
			t.Fatal(err)
		}
		ageUpload(t, f.TenantID, intent.NoteID, "25 hours")
		exec(t, f.TenantID, "UPDATE transcription.jobs SET lease_deadline=now()-interval '1 second' WHERE tenant_id=? AND id=?", f.TenantID, jobID)
		requests := outbox(t, f.TenantID, intent.NoteID, "transcription.requested.v1")
		if counts := runDue(t, f.TenantID); counts.Recovered != 1 || counts.Expired != 1 || counts.Requeued != 0 {
			t.Fatalf("late recovery counts=%+v", counts)
		}
		if note(t, f.TenantID, intent.NoteID).Status != "EXPIRED" || jobStatus(t, f.TenantID, jobID) != "EXPIRED" || outbox(t, f.TenantID, intent.NoteID, "transcription.requested.v1") != requests {
			t.Fatal("expired audio was sent back to the worker")
		}
	})

	t.Run("due retry publishes one MSG01 per attempt and re-arms only a stale delivery", func(t *testing.T) {
		f := seedVoiceFixture(t, ctx, h, 10)
		intent, jobID := createProcessableNote(t, ctx, h, f)
		// A retryable worker failure schedules the wakeup; only the backoff is aged.
		dueNow := func() {
			exec(t, f.TenantID, "UPDATE transcription.jobs SET retry_at=now()-interval '1 second' WHERE tenant_id=? AND id=? AND status='RETRY_WAIT'", f.TenantID, jobID)
		}
		failAttempt(t, f, jobID, 1, false)
		if state := note(t, f.TenantID, intent.NoteID); state.Status != "RETRY_WAIT" || jobStatus(t, f.TenantID, jobID) != "RETRY_WAIT" {
			t.Fatalf("retryable failure left note=%+v job=%s", state, jobStatus(t, f.TenantID, jobID))
		}
		if counts := runDue(t, f.TenantID); counts.Requeued != 0 {
			t.Fatalf("a wakeup before its backoff elapsed: %+v", counts)
		}
		dueNow()
		before := outbox(t, f.TenantID, intent.NoteID, "transcription.requested.v1")
		if counts := runDue(t, f.TenantID); counts.Requeued != 1 {
			t.Fatalf("first wakeup counts=%+v", counts)
		}
		if counts := runDue(t, f.TenantID); counts.Requeued != 0 {
			t.Fatalf("pending wakeup was duplicated: %+v", counts)
		}
		exec(t, f.TenantID, "UPDATE messaging.outbox SET status='PUBLISHED',published_at=now()-interval '1 minute' WHERE tenant_id=? AND type='transcription.requested.v1' AND status='PENDING' AND payload->'payload'->>'noteId'=?", f.TenantID, intent.NoteID.String())
		if counts := runDue(t, f.TenantID); counts.Requeued != 0 {
			t.Fatalf("recent delivery was re-armed: %+v", counts)
		}
		exec(t, f.TenantID, "UPDATE messaging.outbox SET published_at=now()-interval '10 minutes' WHERE tenant_id=? AND type='transcription.requested.v1' AND status='PUBLISHED' AND payload->'payload'->>'noteId'=?", f.TenantID, intent.NoteID.String())
		if counts := runDue(t, f.TenantID); counts.Requeued != 1 {
			t.Fatalf("lost delivery was not re-armed: %+v", counts)
		}
		if after := outbox(t, f.TenantID, intent.NoteID, "transcription.requested.v1"); after != before+1 {
			t.Fatalf("MSG01 rows before=%d after=%d, want one idempotent wakeup", before, after)
		}
		// The next attempt is a real claim and failure, which advances the wakeup key.
		failAttempt(t, f, jobID, 1, false)
		dueNow()
		if counts := runDue(t, f.TenantID); counts.Requeued != 1 {
			t.Fatalf("next attempt wakeup counts=%+v", counts)
		}
	})

	t.Run("sweep racing a completing worker keeps one durable result", func(t *testing.T) {
		f := seedVoiceFixture(t, ctx, h, 10)
		intent, jobID := createProcessableNote(t, ctx, h, f)
		key := audioObjectKey(t, ctx, h, f.TenantID, intent.NoteID)
		gateway := transcribed()
		gateway.delay = 300 * time.Millisecond
		workerDone := make(chan error, 1)
		go func() { workerDone <- runWorker(f, jobID, 1, gateway) }()
		var workerErr error
		finished := false
		for !finished {
			if _, err := executor.RunDue(ctx, f.TenantID, 50); err != nil {
				t.Fatal(err)
			}
			if client.deleteCalls(key) != 0 && note(t, f.TenantID, intent.NoteID).Status != "COMPLETED" {
				t.Fatal("audio deleted while the worker was still processing")
			}
			select {
			case workerErr = <-workerDone:
				finished = true
			case <-time.After(20 * time.Millisecond):
			}
		}
		if workerErr != nil {
			t.Fatal(workerErr)
		}
		runDue(t, f.TenantID)
		state := note(t, f.TenantID, intent.NoteID)
		if state.Status != "COMPLETED" || state.AudioDeletionStatus != "DELETED" || jobStatus(t, f.TenantID, jobID) != "COMPLETED" {
			t.Fatalf("worker race note=%+v job=%s", state, jobStatus(t, f.TenantID, jobID))
		}
		if scalar(t, f.TenantID, "SELECT count(*) FROM transcription.transcript_revisions WHERE tenant_id=? AND note_id=?", f.TenantID, intent.NoteID) != 1 || gateway.Calls() != 1 {
			t.Fatal("race produced more than one transcript or provider call")
		}
	})

	t.Run("sweep racing manual retry preserves the winning generation", func(t *testing.T) {
		f := seedVoiceFixture(t, ctx, h, 10)
		intent, jobID := createProcessableNote(t, ctx, h, f)
		exec(t, f.TenantID, "UPDATE transcription.audio_notes SET status='FAILED',safe_failure_code='TRANSCRIPTION_FAILED' WHERE tenant_id=? AND id=?", f.TenantID, intent.NoteID)
		exec(t, f.TenantID, "UPDATE transcription.jobs SET status='FAILED',safe_failure_code='TRANSCRIPTION_FAILED' WHERE tenant_id=? AND id=?", f.TenantID, jobID)
		// Half a second before the bound, either the API retry or the scan may win.
		ageUpload(t, f.TenantID, intent.NoteID, "86399.5 seconds")
		version := note(t, f.TenantID, intent.NoteID).Version
		start := make(chan struct{})
		errs := make(chan error, 2)
		go func() { <-start; _, err := executor.RunDue(ctx, f.TenantID, 20); errs <- err }()
		var retryErr error
		retryDone := make(chan struct{})
		go func() {
			<-start
			retryErr = retry_note.Setup(ctx, retry_note.Dependencies{DB: h.DB, Within: h.WithinTenant}, retry_note.Command{TenantID: f.TenantID, NoteID: intent.NoteID, ExpectedVersion: version, ClientMutationID: identity.NewID().String()})
			close(retryDone)
		}()
		close(start)
		if err := <-errs; err != nil {
			t.Fatalf("sweep failed: %v", err)
		}
		<-retryDone
		// Once the bound has certainly passed, whichever side won must converge
		// to one expired note without a runnable generation.
		time.Sleep(time.Second)
		runDue(t, f.TenantID)
		state := note(t, f.TenantID, intent.NoteID)
		generations := scalar(t, f.TenantID, "SELECT count(*) FROM transcription.jobs WHERE tenant_id=? AND note_id=? AND generation>1", f.TenantID, intent.NoteID)
		runnable := scalar(t, f.TenantID, "SELECT count(*) FROM transcription.jobs WHERE tenant_id=? AND note_id=? AND status IN ('QUEUED','RETRY_WAIT','PROCESSING')", f.TenantID, intent.NoteID)
		if state.Status != "EXPIRED" || runnable != 0 {
			t.Fatalf("race left a runnable or unexpired note: note=%+v runnable=%d retryErr=%v", state, runnable, retryErr)
		}
		if (retryErr == nil && generations != 1) || (retryErr != nil && generations != 0) {
			t.Fatalf("retry outcome and generations disagree: generations=%d retryErr=%v", generations, retryErr)
		}
	})

	t.Run("IT-291 sweep racing discard keeps cancellation and removes audio", func(t *testing.T) {
		f := seedVoiceFixture(t, ctx, h, 10)
		intent, jobID := createProcessableNote(t, ctx, h, f)
		// Half a second before the bound, either the discard or the scan may win.
		ageUpload(t, f.TenantID, intent.NoteID, "86399.5 seconds")
		version := note(t, f.TenantID, intent.NoteID).Version
		start := make(chan struct{})
		type raceResult struct {
			source string
			err    error
		}
		results := make(chan raceResult, 2)
		go func() {
			<-start
			_, err := executor.RunDue(ctx, f.TenantID, 20)
			results <- raceResult{source: "sweep", err: err}
		}()
		go func() {
			<-start
			err := discard_note.Setup(ctx, discard_note.Dependencies{DB: h.DB, Store: store, Within: h.WithinTenant}, discard_note.Command{TenantID: f.TenantID, NoteID: intent.NoteID, ExpectedVersion: version, ClientMutationID: identity.NewID().String()})
			results <- raceResult{source: "discard", err: err}
		}()
		close(start)
		var discardErr error
		for range 2 {
			result := <-results
			if result.source == "sweep" && result.err != nil {
				t.Fatalf("sweep failed: %v", result.err)
			}
			if result.source == "discard" {
				discardErr = result.err
			}
		}
		runDue(t, f.TenantID)
		state := note(t, f.TenantID, intent.NoteID)
		if (state.Status != "CANCELED" && state.Status != "EXPIRED") || state.AudioDeletionStatus != "DELETED" {
			t.Fatalf("discard race note=%+v", state)
		}
		if (state.Status == "CANCELED" && discardErr != nil) || (state.Status == "EXPIRED" && discardErr == nil) {
			t.Fatalf("discard race winner mismatch: status=%s err=%v", state.Status, discardErr)
		}
		gateway := transcribed()
		if err := runWorker(f, jobID, 1, gateway); err == nil || gateway.Calls() != 0 {
			t.Fatalf("discarded or expired note was processed: err=%v", err)
		}
	})

	t.Run("IT-291 a note discarded before processing follows temporary-audio cleanup", func(t *testing.T) {
		f := seedVoiceFixture(t, ctx, h, 10)
		intent, jobID := createProcessableNote(t, ctx, h, f)
		key := audioObjectKey(t, ctx, h, f.TenantID, intent.NoteID)
		if err := discard_note.Setup(ctx, discard_note.Dependencies{DB: h.DB, Store: store, Within: h.WithinTenant}, discard_note.Command{TenantID: f.TenantID, NoteID: intent.NoteID, ExpectedVersion: note(t, f.TenantID, intent.NoteID).Version, ClientMutationID: identity.NewID().String()}); err != nil {
			t.Fatal(err)
		}
		if counts := runDue(t, f.TenantID); counts.Deleted != 1 || !client.wasDeleted(key) {
			t.Fatalf("discard cleanup counts=%+v", counts)
		}
		if state := note(t, f.TenantID, intent.NoteID); state.Status != "CANCELED" || state.AudioDeletionStatus != "DELETED" || jobStatus(t, f.TenantID, jobID) != "CANCELED" {
			t.Fatalf("discarded note=%+v", state)
		}
		gateway := transcribed()
		if err := runWorker(f, jobID, 1, gateway); err == nil || gateway.Calls() != 0 {
			t.Fatalf("discarded note reached the provider: err=%v", err)
		}
	})

	t.Run("IT-287 IT-290 IT-283 reads during deletion keep text and allow only text revision", func(t *testing.T) {
		f := seedVoiceFixture(t, ctx, h, 10)
		intent, jobID := createProcessableNote(t, ctx, h, f)
		if err := runWorker(f, jobID, 1, transcribed()); err != nil {
			t.Fatal(err)
		}
		stop := make(chan struct{})
		readErr := make(chan error, 1)
		go func() {
			defer close(readErr)
			for {
				select {
				case <-stop:
					return
				default:
				}
				read, err := get_note.Setup(ctx, get_note.Dependencies{DB: h.DB, Within: h.WithinTenant}, get_note.Command{TenantID: f.TenantID, NoteID: intent.NoteID})
				if err != nil {
					readErr <- err
					return
				}
				if read.Status != "COMPLETED" || len(read.Revisions) == 0 || (read.AudioDeletionStatus != "SCHEDULED" && read.AudioDeletionStatus != "DELETED") {
					readErr <- errors.New("status read observed an inconsistent note during deletion")
					return
				}
			}
		}()
		runDue(t, f.TenantID)
		close(stop)
		if err := <-readErr; err != nil {
			t.Fatal(err)
		}
		state := note(t, f.TenantID, intent.NoteID)
		if state.AudioDeletionStatus != "DELETED" {
			t.Fatalf("note after overlapping reads=%+v", state)
		}
		if _, err := revise_note.Setup(ctx, revise_note.Dependencies{DB: h.DB, Within: h.WithinTenant}, revise_note.Command{TenantID: f.TenantID, NoteID: intent.NoteID, ExpectedVersion: state.Version, ClientMutationID: identity.NewID().String(), Text: "duas cadeiras e uma mesa", Kind: "HUMAN_EDIT", AuthorID: &f.MembershipID}); err != nil {
			t.Fatalf("text revision after audio deletion: %v", err)
		}
		state = note(t, f.TenantID, intent.NoteID)
		err := retry_note.Setup(ctx, retry_note.Dependencies{DB: h.DB, Within: h.WithinTenant}, retry_note.Command{TenantID: f.TenantID, NoteID: intent.NoteID, ExpectedVersion: state.Version, ClientMutationID: identity.NewID().String()})
		if !apperror.Is(err, apperror.RetryUnavailable) {
			t.Fatalf("retranscription of deleted audio error=%v, want RETRY_UNAVAILABLE", err)
		}
	})

	// Lock order is note, then job, in every writer. The two subtests below
	// prove the claim follows it and that claims racing the scans never deadlock.
	isDeadlock := func(err error) bool {
		return err != nil && (strings.Contains(err.Error(), "40P01") || strings.Contains(err.Error(), "deadlock detected"))
	}
	t.Run("claim locks the note before the job", func(t *testing.T) {
		f := seedVoiceFixture(t, ctx, h, 10)
		intent, jobID := createProcessableNote(t, ctx, h, f)
		holding, release := make(chan struct{}), make(chan struct{})
		holderDone := make(chan error, 1)
		go func() {
			holderDone <- h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
				var id identity.ID
				if err := tx.Raw("SELECT id FROM transcription.audio_notes WHERE tenant_id=? AND id=? FOR UPDATE", f.TenantID, intent.NoteID).Row().Scan(&id); err != nil {
					return err
				}
				close(holding)
				<-release
				return nil
			})
		}()
		select {
		case <-holding:
		case err := <-holderDone:
			t.Fatalf("note lock holder ended early: %v", err)
		case <-time.After(10 * time.Second):
			t.Fatal("note lock was never taken")
		}
		releaseHolder := sync.OnceFunc(func() { close(release) })
		defer releaseHolder()
		claimDone := make(chan error, 1)
		go func() {
			_, err := repo.Claim(ctx, jobID, f.TenantID, 1)
			claimDone <- err
		}()
		// Wait until the claim is parked on the note lock held above.
		blocked := false
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline) && !blocked; time.Sleep(25 * time.Millisecond) {
			var waiting int64
			if err := h.MigrationDB.Raw("SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%j.generation=%FOR UPDATE OF%' AND query NOT LIKE '%pg_stat_activity%'").Row().Scan(&waiting); err != nil {
				t.Fatal(err)
			}
			blocked = waiting > 0
		}
		if !blocked {
			t.Fatal("the claim did not wait for the note lock")
		}
		// While parked on the note, the claim must not hold the job row.
		if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
			var id identity.ID
			return tx.Raw("SELECT id FROM transcription.jobs WHERE tenant_id=? AND id=? FOR UPDATE NOWAIT", f.TenantID, jobID).Row().Scan(&id)
		}); err != nil {
			t.Fatalf("the claim locked the job before the note: %v", err)
		}
		releaseHolder()
		if err := <-holderDone; err != nil {
			t.Fatal(err)
		}
		if err := <-claimDone; err != nil {
			t.Fatalf("claim after the note lock was released: %v", err)
		}
		if state := note(t, f.TenantID, intent.NoteID); state.Status != "PROCESSING" {
			t.Fatalf("claimed note=%+v", state)
		}
	})

	t.Run("claims racing expiry and discard never deadlock", func(t *testing.T) {
		const rounds, notesPerRound = 25, 4
		for round := range rounds {
			// A fresh tenant per round keeps every note inside the voice limits.
			f := seedVoiceFixture(t, ctx, h, 10)
			type target struct {
				noteID, jobID identity.ID
				version       int
			}
			var targets []target
			for range notesPerRound {
				intent, jobID := createProcessableNote(t, ctx, h, f)
				// Past the bound, so the scan wants the same rows the claim locks.
				ageUpload(t, f.TenantID, intent.NoteID, "25 hours")
				targets = append(targets, target{noteID: intent.NoteID, jobID: jobID, version: note(t, f.TenantID, intent.NoteID).Version})
			}
			start := make(chan struct{})
			errs := make(chan error, notesPerRound*2+1)
			var wg sync.WaitGroup
			spawn := func(fn func() error) {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					errs <- fn()
				}()
			}
			spawn(func() error { _, err := executor.RunDue(ctx, f.TenantID, 50); return err })
			for i, item := range targets {
				spawn(func() error { _, err := repo.Claim(ctx, item.jobID, f.TenantID, 1); return err })
				if i%2 == 0 {
					spawn(func() error {
						return discard_note.Setup(ctx, discard_note.Dependencies{DB: h.DB, Store: store, Within: h.WithinTenant}, discard_note.Command{TenantID: f.TenantID, NoteID: item.noteID, ExpectedVersion: item.version, ClientMutationID: identity.NewID().String()})
					})
				}
			}
			close(start)
			wg.Wait()
			close(errs)
			for err := range errs {
				if isDeadlock(err) {
					t.Fatalf("round %d: lock order deadlock: %v", round, err)
				}
			}
			// The scan itself has to succeed whichever side won each row.
			if _, err := executor.RunDue(ctx, f.TenantID, 50); err != nil {
				t.Fatalf("round %d: sweep after the race: %v", round, err)
			}
		}
	})

	t.Run("IT-292 each note keeps an independent cleanup state", func(t *testing.T) {
		f := seedVoiceFixture(t, ctx, h, 10)
		type pending struct {
			noteID identity.ID
			key    string
		}
		var notes []pending
		for range 3 {
			intent, jobID := createProcessableNote(t, ctx, h, f)
			if err := runWorker(f, jobID, 1, transcribed()); err != nil {
				t.Fatal(err)
			}
			notes = append(notes, pending{noteID: intent.NoteID, key: audioObjectKey(t, ctx, h, f.TenantID, intent.NoteID)})
		}
		client.setFailure(notes[1].key, true)
		defer client.setFailure(notes[1].key, false)
		counts := runDue(t, f.TenantID)
		if counts.Deleted != 2 || counts.Failed != 1 {
			t.Fatalf("batch counts=%+v", counts)
		}
		for i, item := range notes {
			want, attempts := "DELETED", 0
			if i == 1 {
				want, attempts = "FAILED", 1
			}
			assertAudioDeletion(t, ctx, h, f.TenantID, item.noteID, want, attempts)
			if state := note(t, f.TenantID, item.noteID); state.Status != "COMPLETED" || state.AudioDeletionStatus != want {
				t.Fatalf("note %d=%+v, want audio %s", i, state, want)
			}
		}
	})
}

func audioObjectKey(t *testing.T, ctx context.Context, h *Harness, tenant, note identity.ID) string {
	t.Helper()
	var key string
	if err := h.WithinTenant(ctx, tenant, func(tx *gorm.DB) error {
		return tx.Raw("SELECT upload_key FROM transcription.audio_notes WHERE tenant_id=? AND id=?", tenant, note).Row().Scan(&key)
	}); err != nil {
		t.Fatal(err)
	}
	return key
}

func assertAudioDeletion(t *testing.T, ctx context.Context, h *Harness, tenant, note identity.ID, wantStatus string, attempts int) {
	t.Helper()
	var state struct {
		Status   string
		Attempts int
	}
	if err := h.WithinTenant(ctx, tenant, func(tx *gorm.DB) error {
		return tx.Raw("SELECT status,attempts FROM transcription.audio_deletions WHERE tenant_id=? AND note_id=?", tenant, note).Scan(&state).Error
	}); err != nil {
		t.Fatal(err)
	}
	if state.Status != wantStatus || state.Attempts != attempts {
		t.Fatalf("deletion state=%+v, want status=%s attempts=%d", state, wantStatus, attempts)
	}
}
