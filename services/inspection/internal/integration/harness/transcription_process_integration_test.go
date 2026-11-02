//go:build integration

package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	"inspection/services/inspection/internal/features/transcription/complete_note_upload"
	"inspection/services/inspection/internal/features/transcription/create_note_upload"
	"inspection/services/inspection/internal/features/transcription/process_note"
	"inspection/services/inspection/internal/features/transcription/retry_note"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"

	"gorm.io/gorm"
)

func TestProcessNoteFencingAndDurableResultsIT099IT100IT273IT274IT275IT277(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	h, err := NewFromEnv(ctx)
	if err != nil {
		t.Skipf("integration dependencies unavailable: %v", err)
	}
	defer h.Close()
	var present bool
	if err := h.MigrationDB.Raw("SELECT to_regclass('transcription.audio_notes') IS NOT NULL").Row().Scan(&present); err != nil || !present {
		t.Skip("transcription migrations are not installed")
	}
	fixture := seedVoiceFixture(t, ctx, h, 20)
	intent, jobID := createProcessableNote(t, ctx, h, fixture)
	t.Run("IT-104 uploaded note remains queued until claimed", func(t *testing.T) {
		var status string
		if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
			return tx.Raw("SELECT status FROM transcription.audio_notes WHERE tenant_id=? AND id=?", fixture.TenantID, intent.NoteID).Row().Scan(&status)
		}); err != nil {
			t.Fatal(err)
		}
		if status != "QUEUED" {
			t.Fatalf("pending note status=%s, want QUEUED", status)
		}
	})
	repo, err := process_note.NewRepository(process_note.RepositoryDependencies{DB: h.DB, Within: h.WithinTenant})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("IT-273 two workers have one fenced claim", func(t *testing.T) {
		start := make(chan struct{})
		errs := make(chan error, 2)
		var mu sync.Mutex
		wins := 0
		for range 2 {
			go func() {
				<-start
				_, claimErr := repo.Claim(ctx, jobID, fixture.TenantID, 1)
				mu.Lock()
				if claimErr == nil {
					wins++
				}
				mu.Unlock()
				errs <- claimErr
			}()
		}
		close(start)
		for range 2 {
			// The loser must observe the winner's valid lease, not a terminal state.
			if claimErr := <-errs; claimErr != nil && !errors.Is(claimErr, process_note.ErrLeaseActive) {
				t.Fatalf("losing claim error=%v, want ErrLeaseActive", claimErr)
			}
		}
		if wins != 1 {
			t.Fatalf("successful claims=%d, want 1", wins)
		}
	})

	var claimed process_note.Job
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		return tx.Exec("UPDATE transcription.jobs SET lease_deadline=? WHERE tenant_id=? AND id=?", time.Now().Add(-time.Second), fixture.TenantID, jobID).Error
	}); err != nil {
		t.Fatal(err)
	}
	claimed, err = repo.Claim(ctx, jobID, fixture.TenantID, 1)
	if err != nil {
		t.Fatalf("claim after expired lease: %v", err)
	}
	old := claimed
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		return tx.Exec("UPDATE transcription.jobs SET lease_deadline=? WHERE tenant_id=? AND id=?", time.Now().Add(-time.Second), fixture.TenantID, jobID).Error
	}); err != nil {
		t.Fatal(err)
	}
	claimed, err = repo.Claim(ctx, jobID, fixture.TenantID, 1)
	if err != nil {
		t.Fatalf("reclaim expired lease: %v", err)
	}
	t.Run("IT-276 audio cleanup waits for durable transcript commit", func(t *testing.T) {
		var pendingDeletions int64
		if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
			return tx.Table("transcription.audio_deletions").Where("tenant_id=? AND note_id=?", fixture.TenantID, intent.NoteID).Count(&pendingDeletions).Error
		}); err != nil {
			t.Fatal(err)
		}
		if pendingDeletions != 0 {
			t.Fatalf("audio deletion was scheduled before a terminal result: %d", pendingDeletions)
		}
	})
	if err := repo.Complete(ctx, old, "", "texto atrasado", false); err == nil {
		t.Fatal("stale lease completed after fencing token changed")
	}
	if err := repo.Complete(ctx, claimed, "", "", true); err != nil {
		t.Fatalf("persist no-speech completion: %v", err)
	}

	var note struct {
		Status   string
		NoSpeech bool
	}
	var deletions, msg02 int64
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		if err := tx.Raw("SELECT status,no_speech FROM transcription.audio_notes WHERE tenant_id=? AND id=?", fixture.TenantID, intent.NoteID).Scan(&note).Error; err != nil {
			return err
		}
		if err := tx.Table("transcription.audio_deletions").Where("tenant_id=? AND note_id=? AND status='PENDING'", fixture.TenantID, intent.NoteID).Count(&deletions).Error; err != nil {
			return err
		}
		return tx.Model(&database.OutboxIntent{}).Where("tenant_id=? AND type='transcription.completed.v1'", fixture.TenantID).Count(&msg02).Error
	}); err != nil {
		t.Fatal(err)
	}
	t.Run("IT-099 no-speech completion emits MSG02 and deletion obligation", func(t *testing.T) {
		if note.Status != "COMPLETED" || !note.NoSpeech || deletions != 1 || msg02 != 1 {
			t.Fatalf("completion projection status=%s noSpeech=%v deletions=%d MSG02=%d", note.Status, note.NoSpeech, deletions, msg02)
		}
	})

	t.Run("IT-275 replay cannot complete twice", func(t *testing.T) {
		if err := repo.Complete(ctx, claimed, "", "", true); err == nil {
			t.Fatal("completed lease was accepted again")
		}
	})
	t.Run("IT-277 canceled note cannot accept late output", func(t *testing.T) {
		other, otherJob := createProcessableNote(t, ctx, h, fixture)
		active, claimErr := repo.Claim(ctx, otherJob, fixture.TenantID, 1)
		if claimErr != nil {
			t.Fatal(claimErr)
		}
		if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
			return tx.Exec("UPDATE transcription.audio_notes SET status='CANCELED' WHERE tenant_id=? AND id=?", fixture.TenantID, other.NoteID).Error
		}); err != nil {
			t.Fatal(err)
		}
		if err := repo.Complete(ctx, active, "", "late", false); err == nil {
			t.Fatal("late output changed canceled note")
		}
	})
	t.Run("IT-100 retry records bounded backoff and MSG03", func(t *testing.T) {
		_, otherJob := createProcessableNote(t, ctx, h, fixture)
		active, claimErr := repo.Claim(ctx, otherJob, fixture.TenantID, 1)
		if claimErr != nil {
			t.Fatal(claimErr)
		}
		if err := repo.Fail(ctx, active, "TRANSCRIPTION_FAILED", false); err != nil {
			t.Fatal(err)
		}
		var state struct {
			Status  string
			RetryAt *time.Time
		}
		var msg03 int64
		if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
			if err := tx.Raw("SELECT status,retry_at FROM transcription.jobs WHERE tenant_id=? AND id=?", fixture.TenantID, otherJob).Scan(&state).Error; err != nil {
				return err
			}
			return tx.Model(&database.OutboxIntent{}).Where("tenant_id=? AND type='transcription.failed.v1'", fixture.TenantID).Count(&msg03).Error
		}); err != nil {
			t.Fatal(err)
		}
		if state.Status != "RETRY_WAIT" || state.RetryAt == nil || !state.RetryAt.After(time.Now()) || msg03 != 1 {
			t.Fatalf("retry state=%s retryAt=%v MSG03=%d", state.Status, state.RetryAt, msg03)
		}
		if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
			return tx.Exec("UPDATE transcription.jobs SET retry_at=? WHERE tenant_id=? AND id=?", time.Now().Add(-time.Second), fixture.TenantID, otherJob).Error
		}); err != nil {
			t.Fatal(err)
		}
		retried, retryErr := repo.Claim(ctx, otherJob, fixture.TenantID, 1)
		if retryErr != nil || retried.Attempt != 2 {
			t.Fatalf("due retry claim=%+v err=%v", retried, retryErr)
		}
	})
	t.Run("late model result preserves participant declaration", func(t *testing.T) {
		other, otherJob := createProcessableNote(t, ctx, h, fixture)
		active, claimErr := repo.Claim(ctx, otherJob, fixture.TenantID, 1)
		if claimErr != nil {
			t.Fatal(claimErr)
		}
		declarationID := identity.NewID()
		declaration := database.TranscriptRevision{ID: declarationID, TenantID: fixture.TenantID, NoteID: other.NoteID, RevisionNumber: 1, Text: "declaração", Kind: "PARTICIPANT_DECLARATION", CreatedAt: time.Now().UTC()}
		if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
			if err := tx.Create(&declaration).Error; err != nil {
				return err
			}
			return tx.Exec("UPDATE transcription.audio_notes SET current_revision_id=? WHERE tenant_id=? AND id=?", declarationID, fixture.TenantID, other.NoteID).Error
		}); err != nil {
			t.Fatal(err)
		}
		ledger, ledgerErr := process_note.NewAttemptLedger(h.DB, "live")
		if ledgerErr != nil {
			t.Fatal(ledgerErr)
		}
		active.Attempt = 1
		callID, ledgerErr := ledger.Start(ctx, active)
		if ledgerErr != nil {
			t.Fatal(ledgerErr)
		}
		if err := repo.Complete(ctx, active, callID, "texto tardio do modelo", false); err != nil {
			t.Fatal(err)
		}
		var current identity.ID
		var revisions int64
		if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
			if err := tx.Raw("SELECT current_revision_id FROM transcription.audio_notes WHERE tenant_id=? AND id=?", fixture.TenantID, other.NoteID).Row().Scan(&current); err != nil {
				return err
			}
			return tx.Table("transcription.transcript_revisions").Where("tenant_id=? AND note_id=?", fixture.TenantID, other.NoteID).Count(&revisions).Error
		}); err != nil {
			t.Fatal(err)
		}
		if current != declarationID || revisions != 2 {
			t.Fatalf("current revision=%s declaration=%s revisions=%d", current, declarationID, revisions)
		}
	})
	t.Run("IT-274 uncertain transport does not schedule another call", func(t *testing.T) {
		_, otherJob := createProcessableNote(t, ctx, h, fixture)
		active, claimErr := repo.Claim(ctx, otherJob, fixture.TenantID, 1)
		if claimErr != nil {
			t.Fatal(claimErr)
		}
		if err := repo.Fail(ctx, active, "USAGE_UNCERTAIN", true); err != nil {
			t.Fatal(err)
		}
		var state struct {
			Status  string
			RetryAt *time.Time
		}
		if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
			return tx.Raw("SELECT status,retry_at FROM transcription.jobs WHERE tenant_id=? AND id=?", fixture.TenantID, otherJob).Scan(&state).Error
		}); err != nil {
			t.Fatal(err)
		}
		if state.Status != "FAILED" || state.RetryAt != nil {
			t.Fatalf("uncertain state=%s retryAt=%v", state.Status, state.RetryAt)
		}
	})
	t.Run("IT-105 retries stop at the configured attempt limit", func(t *testing.T) {
		_, otherJob := createProcessableNote(t, ctx, h, fixture)
		for attempt := 1; attempt <= 3; attempt++ {
			active, claimErr := repo.Claim(ctx, otherJob, fixture.TenantID, 1)
			if claimErr != nil {
				t.Fatalf("claim attempt %d: %v", attempt, claimErr)
			}
			if active.Attempt != attempt {
				t.Fatalf("attempt=%d, want %d", active.Attempt, attempt)
			}
			if err := repo.Fail(ctx, active, "TRANSCRIPTION_FAILED", false); err != nil {
				t.Fatal(err)
			}
			if attempt < 3 {
				if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
					return tx.Exec("UPDATE transcription.jobs SET retry_at=? WHERE tenant_id=? AND id=?", time.Now().Add(-time.Second), fixture.TenantID, otherJob).Error
				}); err != nil {
					t.Fatal(err)
				}
			}
		}
		var status string
		if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
			return tx.Raw("SELECT status FROM transcription.jobs WHERE tenant_id=? AND id=?", fixture.TenantID, otherJob).Row().Scan(&status)
		}); err != nil {
			t.Fatal(err)
		}
		if status != "FAILED" {
			t.Fatalf("exhausted retry status=%s, want FAILED", status)
		}
	})
	t.Run("IT-112 claims for separate notes do not block each other", func(t *testing.T) {
		_, oneJob := createProcessableNote(t, ctx, h, fixture)
		_, twoJob := createProcessableNote(t, ctx, h, fixture)
		start := make(chan struct{})
		errs := make(chan error, 2)
		for _, id := range []identity.ID{oneJob, twoJob} {
			go func(id identity.ID) {
				<-start
				_, claimErr := repo.Claim(ctx, id, fixture.TenantID, 1)
				errs <- claimErr
			}(id)
		}
		close(start)
		for range 2 {
			if err := <-errs; err != nil {
				t.Fatalf("independent note claim failed: %v", err)
			}
		}
	})
}

func TestProcessNoteSetupRecoveryAndRetryIT103IT107IT108IT109IT274IT275(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	h, err := NewFromEnv(ctx)
	if err != nil {
		t.Skipf("integration dependencies unavailable: %v", err)
	}
	defer h.Close()
	repo, err := process_note.NewRepository(process_note.RepositoryDependencies{DB: h.DB, Within: h.WithinTenant})
	if err != nil {
		t.Fatal(err)
	}
	process := func(fixture Task06Fixture, jobID identity.ID, generation int, gateway *setupGateway, silence process_note.SilenceDetector) error {
		ledger, err := process_note.NewAttemptLedger(h.DB, "mock")
		if err != nil {
			return err
		}
		return process_note.Setup(ctx, process_note.Dependencies{Repository: repo, Audio: setupAudio{}, Gateway: gateway, Ledger: ledger, Silence: silence}, jobID, fixture.TenantID, generation)
	}
	count := func(t *testing.T, fixture Task06Fixture, noteID, jobID identity.ID) (int64, int64) {
		t.Helper()
		var revisions, calls int64
		if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
			if err := tx.Table("transcription.transcript_revisions").Where("tenant_id=? AND note_id=?", fixture.TenantID, noteID).Count(&revisions).Error; err != nil {
				return err
			}
			return tx.Table("usage.llm_calls").Where("tenant_id=? AND job_id=? AND comparison_mode='TRANSCRIPTION'", fixture.TenantID, jobID).Count(&calls).Error
		}); err != nil {
			t.Fatal(err)
		}
		return revisions, calls
	}

	t.Run("IT-103 corrupted audio fails safely without revision", func(t *testing.T) {
		detector, err := process_note.NewFFmpegSilenceDetector(10*time.Second, "-50dB")
		if err != nil {
			t.Skip("ffmpeg is unavailable")
		}
		fixture := seedVoiceFixture(t, ctx, h, 20)
		intent, jobID := createProcessableNote(t, ctx, h, fixture)
		gateway := &setupGateway{result: llm.TranscriptionResult{Transcript: "must not be called"}}
		if err := process(fixture, jobID, 1, gateway, detector); err != nil {
			t.Fatal(err)
		}
		var status, code string
		if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
			return tx.Raw("SELECT status,safe_failure_code FROM transcription.audio_notes WHERE tenant_id=? AND id=?", fixture.TenantID, intent.NoteID).Row().Scan(&status, &code)
		}); err != nil {
			t.Fatal(err)
		}
		revisions, _ := count(t, fixture, intent.NoteID, jobID)
		if status != "FAILED" || code != "AUDIO_INVALID" || revisions != 0 || gateway.Calls() != 0 {
			t.Fatalf("status=%s code=%s revisions=%d gatewayCalls=%d", status, code, revisions, gateway.Calls())
		}
	})

	t.Run("IT-107 concurrent retry and result produce one transcript", func(t *testing.T) {
		fixture := seedVoiceFixture(t, ctx, h, 20)
		intent, jobID := createProcessableNote(t, ctx, h, fixture)
		oldJob, err := repo.Claim(ctx, jobID, fixture.TenantID, 1)
		if err != nil {
			t.Fatal(err)
		}
		if err := repo.Fail(ctx, oldJob, "TRANSCRIPTION_REJECTED", false); err != nil {
			t.Fatal(err)
		}
		var version int
		if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
			return tx.Raw("SELECT version FROM transcription.audio_notes WHERE tenant_id=? AND id=?", fixture.TenantID, intent.NoteID).Row().Scan(&version)
		}); err != nil {
			t.Fatal(err)
		}
		if err := retry_note.Setup(ctx, retry_note.Dependencies{DB: h.DB, Within: h.WithinTenant}, retry_note.Command{TenantID: fixture.TenantID, NoteID: intent.NoteID, ExpectedVersion: version, ClientMutationID: identity.NewID().String(), IsParticipant: false}); err != nil {
			t.Fatal(err)
		}
		var retryJob identity.ID
		if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
			return tx.Raw("SELECT id FROM transcription.jobs WHERE tenant_id=? AND note_id=? AND generation=2", fixture.TenantID, intent.NoteID).Row().Scan(&retryJob)
		}); err != nil {
			t.Fatal(err)
		}
		gateway := &setupGateway{delay: 100 * time.Millisecond, result: llm.TranscriptionResult{Transcript: "um transcript"}}
		start := make(chan struct{})
		errs := make(chan error, 3)
		for range 2 {
			go func() { <-start; errs <- process(fixture, retryJob, 2, gateway, nil) }()
		}
		go func() {
			<-start
			errs <- repo.Complete(ctx, oldJob, identity.NewID().String(), "resultado atrasado", false)
		}()
		close(start)
		for range 3 {
			// The losing concurrent claim sees the winner's live lease.
			if err := <-errs; err != nil && !errors.Is(err, process_note.ErrStaleLease) && !errors.Is(err, process_note.ErrNotClaimable) && !errors.Is(err, process_note.ErrLeaseActive) {
				t.Fatal(err)
			}
		}
		revisions, _ := count(t, fixture, intent.NoteID, jobID)
		if revisions != 1 || gateway.Calls() != 1 {
			t.Fatalf("revisions=%d gatewayCalls=%d", revisions, gateway.Calls())
		}
	})

	t.Run("IT-108 slow call reaches a durable status", func(t *testing.T) {
		fixture := seedVoiceFixture(t, ctx, h, 20)
		intent, jobID := createProcessableNote(t, ctx, h, fixture)
		gateway := &setupGateway{delay: 150 * time.Millisecond, result: llm.TranscriptionResult{Transcript: "processamento concluído"}}
		if err := process(fixture, jobID, 1, gateway, nil); err != nil {
			t.Fatal(err)
		}
		var status string
		if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
			return tx.Raw("SELECT status FROM transcription.audio_notes WHERE tenant_id=? AND id=?", fixture.TenantID, intent.NoteID).Row().Scan(&status)
		}); err != nil {
			t.Fatal(err)
		}
		if status != "COMPLETED" {
			t.Fatalf("status=%s", status)
		}
	})

	t.Run("IT-109 and IT-275 completed redelivery through Setup does not call gateway", func(t *testing.T) {
		fixture := seedVoiceFixture(t, ctx, h, 20)
		_, jobID := createProcessableNote(t, ctx, h, fixture)
		gateway := &setupGateway{result: llm.TranscriptionResult{Transcript: "uma chamada"}}
		if err := process(fixture, jobID, 1, gateway, nil); err != nil {
			t.Fatal(err)
		}
		if err := process(fixture, jobID, 1, gateway, nil); !errors.Is(err, process_note.ErrNotClaimable) {
			t.Fatalf("redelivery error=%v", err)
		}
		if gateway.Calls() != 1 {
			t.Fatalf("gateway calls=%d", gateway.Calls())
		}
	})

	t.Run("IT-274 timeout is visible and never retried automatically", func(t *testing.T) {
		fixture := seedVoiceFixture(t, ctx, h, 20)
		_, jobID := createProcessableNote(t, ctx, h, fixture)
		gateway := &setupGateway{err: llm.NewError(llm.CodeTimeout, 0, context.DeadlineExceeded), result: llm.TranscriptionResult{Usage: llm.StructuredResult{TransportDelivered: true}}}
		if err := process(fixture, jobID, 1, gateway, nil); err != nil {
			t.Fatal(err)
		}
		var row struct {
			State           string
			Outcome         string
			AudioDurationMS *int64
		}
		if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
			return tx.Raw("SELECT state,technical_outcome,audio_duration_ms FROM usage.llm_calls WHERE tenant_id=? AND job_id=?", fixture.TenantID, jobID).Row().Scan(&row.State, &row.Outcome, &row.AudioDurationMS)
		}); err != nil {
			t.Fatal(err)
		}
		if row.State != "FINISHED" || row.Outcome != "UNCERTAIN" || row.AudioDurationMS == nil || *row.AudioDurationMS != 1000 {
			t.Fatalf("ledger=%+v", row)
		}
		if err := process(fixture, jobID, 1, gateway, nil); !errors.Is(err, process_note.ErrNotClaimable) {
			t.Fatalf("timeout redelivery=%v", err)
		}
		if gateway.Calls() != 1 {
			t.Fatalf("gateway calls=%d", gateway.Calls())
		}
	})

	t.Run("MSG02 references the effective HUMAN_EDIT revision", func(t *testing.T) {
		fixture := seedVoiceFixture(t, ctx, h, 20)
		intent, jobID := createProcessableNote(t, ctx, h, fixture)
		humanRevision := identity.NewID()
		if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
			row := database.TranscriptRevision{ID: humanRevision, TenantID: fixture.TenantID, NoteID: intent.NoteID, RevisionNumber: 1, Text: "declaração revisada", Kind: "HUMAN_EDIT", CreatedAt: time.Now().UTC()}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
			return tx.Exec("UPDATE transcription.audio_notes SET current_revision_id=? WHERE tenant_id=? AND id=?", humanRevision, fixture.TenantID, intent.NoteID).Error
		}); err != nil {
			t.Fatal(err)
		}
		if err := process(fixture, jobID, 1, &setupGateway{result: llm.TranscriptionResult{Transcript: "texto do modelo"}}, nil); err != nil {
			t.Fatal(err)
		}
		var payloadRaw []byte
		if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
			return tx.Raw("SELECT payload FROM messaging.outbox WHERE tenant_id=? AND type='transcription.completed.v1' AND correlation_id=? ORDER BY created_at DESC LIMIT 1", fixture.TenantID, intent.NoteID.String()).Row().Scan(&payloadRaw)
		}); err != nil {
			t.Fatal(err)
		}
		var envelope events.Envelope[events.TranscriptionCompletedPayload]
		if err := json.Unmarshal(payloadRaw, &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.Payload.RevisionID == nil || *envelope.Payload.RevisionID != humanRevision {
			t.Fatalf("MSG02 revision=%v want current HUMAN_EDIT %s", envelope.Payload.RevisionID, humanRevision)
		}
	})

	t.Run("claim at retry limit records MSG03 atomically", func(t *testing.T) {
		fixture := seedVoiceFixture(t, ctx, h, 20)
		intent, jobID := createProcessableNote(t, ctx, h, fixture)
		if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
			if err := tx.Exec("UPDATE transcription.jobs SET status='RETRY_WAIT',attempts=3,retry_at=now()-interval '1 second' WHERE tenant_id=? AND id=?", fixture.TenantID, jobID).Error; err != nil {
				return err
			}
			return tx.Exec("UPDATE transcription.audio_notes SET status='RETRY_WAIT' WHERE tenant_id=? AND id=?", fixture.TenantID, intent.NoteID).Error
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.Claim(ctx, jobID, fixture.TenantID, 1); !errors.Is(err, process_note.ErrNotClaimable) {
			t.Fatalf("claim error=%v", err)
		}
		var jobStatus, noteStatus, code string
		var msg03 int64
		if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
			if err := tx.Raw("SELECT status,safe_failure_code FROM transcription.jobs WHERE tenant_id=? AND id=?", fixture.TenantID, jobID).Row().Scan(&jobStatus, &code); err != nil {
				return err
			}
			if err := tx.Raw("SELECT status FROM transcription.audio_notes WHERE tenant_id=? AND id=?", fixture.TenantID, intent.NoteID).Row().Scan(&noteStatus); err != nil {
				return err
			}
			return tx.Model(&database.OutboxIntent{}).Where("tenant_id=? AND type='transcription.failed.v1' AND correlation_id=?", fixture.TenantID, intent.NoteID.String()).Count(&msg03).Error
		}); err != nil {
			t.Fatal(err)
		}
		if jobStatus != "FAILED" || noteStatus != "FAILED" || code != "RETRY_LIMIT_EXCEEDED" || msg03 != 1 {
			t.Fatalf("job=%s note=%s code=%s MSG03=%d", jobStatus, noteStatus, code, msg03)
		}
	})

	t.Run("claim at retry limit recovers an already-paid staged transcript without opening audio", func(t *testing.T) {
		fixture := seedVoiceFixture(t, ctx, h, 20)
		intent, jobID := createProcessableNote(t, ctx, h, fixture)
		for attempt := 1; attempt < 3; attempt++ {
			active, claimErr := repo.Claim(ctx, jobID, fixture.TenantID, 1)
			if claimErr != nil {
				t.Fatal(claimErr)
			}
			if err := repo.Fail(ctx, active, "TRANSCRIPTION_FAILED", false); err != nil {
				t.Fatal(err)
			}
			if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
				return tx.Exec("UPDATE transcription.jobs SET retry_at=now()-interval '1 second' WHERE tenant_id=? AND id=?", fixture.TenantID, jobID).Error
			}); err != nil {
				t.Fatal(err)
			}
		}
		active, claimErr := repo.Claim(ctx, jobID, fixture.TenantID, 1)
		if claimErr != nil || active.Attempt != 3 {
			t.Fatalf("third claim=%+v err=%v", active, claimErr)
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
		if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
			return tx.Exec("UPDATE transcription.jobs SET lease_deadline=now()-interval '1 second' WHERE tenant_id=? AND id=?", fixture.TenantID, jobID).Error
		}); err != nil {
			t.Fatal(err)
		}
		gateway := &setupGateway{}
		// Storage and detector are both down: the staged result must still complete.
		audio := &failingSetupAudio{}
		if err := process_note.Setup(ctx, process_note.Dependencies{Repository: repo, Audio: audio, Gateway: gateway, Ledger: ledger, Silence: failingSetupSilence{}}, jobID, fixture.TenantID, 1); err != nil {
			t.Fatal(err)
		}
		var text, status string
		if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
			if err := tx.Raw("SELECT status FROM transcription.audio_notes WHERE tenant_id=? AND id=?", fixture.TenantID, intent.NoteID).Row().Scan(&status); err != nil {
				return err
			}
			return tx.Raw("SELECT text FROM transcription.transcript_revisions WHERE tenant_id=? AND note_id=? AND kind='MODEL'", fixture.TenantID, intent.NoteID).Row().Scan(&text)
		}); err != nil {
			t.Fatal(err)
		}
		if gateway.Calls() != 0 || audio.opens.Load() != 0 || status != "COMPLETED" || text != "resultado já pago" {
			t.Fatalf("gateway calls=%d audio opens=%d status=%s recovered text=%q", gateway.Calls(), audio.opens.Load(), status, text)
		}
	})
}

type setupAudio struct{}

func (setupAudio) Open(context.Context, string) (io.ReadSeekCloser, error) {
	return setupReadSeekCloser{bytes.NewReader([]byte{0x1a, 0x45, 0xdf, 0xa3, 1, 2, 3, 4})}, nil
}

type failingSetupAudio struct{ opens atomic.Int64 }

func (a *failingSetupAudio) Open(context.Context, string) (io.ReadSeekCloser, error) {
	a.opens.Add(1)
	return nil, errors.New("object store unavailable")
}

type failingSetupSilence struct{}

func (failingSetupSilence) IsSilent(context.Context, io.Reader, string, int64) (bool, error) {
	return false, process_note.ErrSilenceDetectorUnavailable
}

type setupReadSeekCloser struct{ *bytes.Reader }

func (setupReadSeekCloser) Close() error { return nil }

type setupGateway struct {
	calls  atomic.Int64
	delay  time.Duration
	result llm.TranscriptionResult
	err    error
}

func (g *setupGateway) Calls() int64 { return g.calls.Load() }
func (g *setupGateway) Transcribe(ctx context.Context, _ llm.TranscriptionRequest) (llm.TranscriptionResult, error) {
	g.calls.Add(1)
	if g.delay > 0 {
		select {
		case <-time.After(g.delay):
		case <-ctx.Done():
			return llm.TranscriptionResult{}, ctx.Err()
		}
	}
	return g.result, g.err
}

func createProcessableNote(t *testing.T, ctx context.Context, h *Harness, fixture Task06Fixture) (create_note_upload.UploadIntent, identity.ID) {
	t.Helper()
	store := voiceStore(nil)
	intent, err := create_note_upload.Setup(ctx, create_note_upload.Dependencies{DB: h.DB, Store: store, Within: h.WithinTenant}, voiceCommand(fixture, identity.NewID()))
	if err != nil {
		t.Fatal(err)
	}
	if err := complete_note_upload.Setup(ctx, complete_note_upload.Dependencies{DB: h.DB, Store: store, Within: h.WithinTenant}, complete_note_upload.Command{TenantID: fixture.TenantID, NoteID: intent.NoteID, UploadProof: intent.UploadProof}); err != nil {
		t.Fatal(err)
	}
	var jobID identity.ID
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		return tx.Raw("SELECT id FROM transcription.jobs WHERE tenant_id=? AND note_id=?", fixture.TenantID, intent.NoteID).Row().Scan(&jobID)
	}); err != nil {
		t.Fatal(err)
	}
	return intent, jobID
}
