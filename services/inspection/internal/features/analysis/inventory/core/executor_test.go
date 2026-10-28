package core

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/analysis/inventory/inventorytest"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"

	"gorm.io/gorm"
)

// fakeProcessor records what the executor hands it and returns a scripted error.
type fakeProcessor struct {
	mu        sync.Mutex
	db        *gorm.DB
	seen      []database.InventoryJob
	running   []database.InventoryJob
	err       func(database.InventoryJob) error
	write     func(*gorm.DB) error
	failed    []string
	succeeded int
}

func (f *fakeProcessor) Process(_ context.Context, job database.InventoryJob, fenced func(func(*gorm.DB) error) error) error {
	var stored database.InventoryJob
	if err := f.db.Where("id=?", job.ID).Take(&stored).Error; err != nil {
		return err
	}
	f.mu.Lock()
	f.seen = append(f.seen, job)
	f.running = append(f.running, stored)
	errFn, write := f.err, f.write
	f.mu.Unlock()
	if errFn != nil {
		if err := errFn(job); err != nil {
			return err
		}
	}
	if write != nil {
		return fenced(write)
	}
	return nil
}

func (f *fakeProcessor) Failed(_ context.Context, _ *gorm.DB, _ database.InventoryJob, code string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failed = append(f.failed, code)
	return nil
}

func (f *fakeProcessor) Succeeded(context.Context, *gorm.DB, database.InventoryJob) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.succeeded++
	return nil
}

func executorFixture(t *testing.T) (*inventorytest.Seed, *fakeProcessor, *Executor, *time.Time) {
	t.Helper()
	seed := inventorytest.New(t, inventorytest.InspectionOptions{})
	processor := &fakeProcessor{db: seed.DB}
	now := seed.Now
	executor := &Executor{DB: seed.DB, ClaimDB: seed.DB, Processor: processor, Now: func() time.Time { return now }, HeartbeatInterval: time.Hour}
	return seed, processor, executor, &now
}

func reload(t *testing.T, db *gorm.DB, id identity.ID) database.InventoryJob {
	t.Helper()
	var job database.InventoryJob
	if err := db.Where("id=?", id).Take(&job).Error; err != nil {
		t.Fatal(err)
	}
	return job
}

func TestUT190RunDueClaimsOldestDueJobWithLease(t *testing.T) {
	seed, processor, executor, now := executorFixture(t)
	version := identity.NewID()
	newer := seed.Job(t, "CATALOG", "env:b", version, nil)
	older := seed.Job(t, "CATALOG", "env:a", version, nil)
	future := seed.Job(t, "CATALOG", "env:c", version, nil)
	seed.DB.Model(&database.InventoryJob{}).Where("id=?", older.ID).Update("next_attempt_at", now.Add(-time.Minute))
	seed.DB.Model(&database.InventoryJob{}).Where("id=?", future.ID).Update("next_attempt_at", now.Add(time.Hour))
	worked, err := executor.RunDue(context.Background())
	if err != nil || !worked {
		t.Fatalf("RunDue = %v, %v", worked, err)
	}
	if len(processor.running) != 1 || processor.running[0].ID != older.ID {
		t.Fatalf("claimed %+v, want the oldest due job", processor.running)
	}
	claimed := processor.running[0]
	if claimed.Status != "RUNNING" || claimed.LeaseToken == nil || claimed.LeaseUntil == nil || !claimed.LeaseUntil.Equal(now.Add(120*time.Second)) || claimed.Attempts != 1 {
		t.Fatalf("claimed job = %+v, want RUNNING with a 120s lease", claimed)
	}
	if reload(t, seed.DB, older.ID).Status != "SUCCEEDED" || reload(t, seed.DB, newer.ID).Status != "PENDING" || reload(t, seed.DB, future.ID).Status != "PENDING" {
		t.Fatal("only the claimed job may change")
	}
}

func TestUT191ExpiredLeaseIsReclaimedAndStaleWriterRejected(t *testing.T) {
	seed, processor, executor, now := executorFixture(t)
	job := seed.Job(t, "CATALOG", "env:a", identity.NewID(), nil)
	staleToken, expired := identity.NewID(), now.Add(-time.Second)
	seed.DB.Model(&database.InventoryJob{}).Where("id=?", job.ID).Updates(map[string]any{"status": "RUNNING", "attempts": 1, "lease_token": staleToken, "lease_until": expired})
	stale := lease{job: reload(t, seed.DB, job.ID), token: staleToken}
	processor.err = func(database.InventoryJob) error {
		// While the new owner holds the lease, the previous owner's write is fenced out.
		err := executor.fencedWrite(context.Background(), stale, func(*gorm.DB) error { return nil })
		if !errors.Is(err, ErrLeaseLost) {
			return fmt.Errorf("stale write error = %v, want lease lost", err)
		}
		return nil
	}
	if _, err := executor.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(processor.running) != 1 || *processor.running[0].LeaseToken == staleToken || processor.running[0].Attempts != 2 {
		t.Fatalf("reclaimed job = %+v", processor.running)
	}
	if got := reload(t, seed.DB, job.ID); got.Status != "SUCCEEDED" {
		t.Fatalf("status = %s, want SUCCEEDED", got.Status)
	}
}

func TestUT192GatewayErrorsBackOffThenFailAfterFourAttempts(t *testing.T) {
	seed, processor, executor, now := executorFixture(t)
	job := seed.Job(t, "COMPARE", "env:a", identity.NewID(), nil)
	processor.err = func(database.InventoryJob) error { return errors.New("gateway unavailable") }
	for attempt := 1; attempt <= 4; attempt++ {
		if _, err := executor.RunDue(context.Background()); err != nil {
			t.Fatal(err)
		}
		got := reload(t, seed.DB, job.ID)
		if got.Attempts != attempt {
			t.Fatalf("attempt %d: attempts = %d", attempt, got.Attempts)
		}
		if attempt < 4 {
			want := now.Add(30 * time.Second * time.Duration(1<<attempt))
			if got.Status != "PENDING" || !got.NextAttemptAt.Equal(want) || got.LeaseToken != nil {
				t.Fatalf("attempt %d: job = %+v, want PENDING at %s", attempt, got, want)
			}
			*now = got.NextAttemptAt
			continue
		}
		if got.Status != "FAILED" || got.ErrorCode == nil || *got.ErrorCode != "INVENTORY_ANALYSIS_FAILED" {
			t.Fatalf("after 4 failures job = %+v, want FAILED", got)
		}
	}
	if len(processor.failed) != 1 {
		t.Fatalf("Failed hook calls = %d, want 1", len(processor.failed))
	}
}

func TestPermanentErrorFailsWithoutRetry(t *testing.T) {
	seed, processor, executor, _ := executorFixture(t)
	job := seed.Job(t, "CATALOG", "env:a", identity.NewID(), nil)
	processor.err = func(database.InventoryJob) error { return fmt.Errorf("%w: missing evidence", messaging.ErrPermanent) }
	if _, err := executor.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := reload(t, seed.DB, job.ID); got.Status != "FAILED" || got.Attempts != 1 || len(processor.failed) != 1 {
		t.Fatalf("job = %+v failed hooks = %d, want FAILED on the first attempt", got, len(processor.failed))
	}
}

func TestExhaustedExpiredLeaseIsFailedOnReclaim(t *testing.T) {
	seed, processor, executor, now := executorFixture(t)
	job := seed.Job(t, "COMPARE", "env:a", identity.NewID(), nil)
	expired := now.Add(-time.Second)
	seed.DB.Model(&database.InventoryJob{}).Where("id=?", job.ID).Updates(map[string]any{"status": "RUNNING", "attempts": 4, "lease_token": identity.NewID(), "lease_until": expired})
	if _, err := executor.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := reload(t, seed.DB, job.ID); got.Status != "FAILED" || len(processor.seen) != 0 || len(processor.failed) != 1 {
		t.Fatalf("job = %+v processed=%d failed=%d, want FAILED without a fifth attempt", got, len(processor.seen), len(processor.failed))
	}
}

func TestSuccessKeepsTruncationDiagnosticAndClearsRetryErrors(t *testing.T) {
	seed, processor, executor, _ := executorFixture(t)
	truncated := seed.Job(t, "CATALOG", "env:a", identity.NewID(), nil)
	retried := seed.Job(t, "CATALOG", "env:b", identity.NewID(), nil)
	seed.DB.Model(&database.InventoryJob{}).Where("id=?", retried.ID).Update("error_code", "INVENTORY_ANALYSIS_FAILED")
	processor.write = func(tx *gorm.DB) error {
		return tx.Model(&database.InventoryJob{}).Where("id=?", truncated.ID).Update("error_code", "TRUNCATED_ITEMS").Error
	}
	for i := 0; i < 2; i++ {
		if _, err := executor.RunDue(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if got := reload(t, seed.DB, truncated.ID); got.Status != "SUCCEEDED" || got.ErrorCode == nil || *got.ErrorCode != "TRUNCATED_ITEMS" {
		t.Fatalf("truncated job = %+v", got)
	}
	if got := reload(t, seed.DB, retried.ID); got.Status != "SUCCEEDED" || got.ErrorCode != nil {
		t.Fatalf("retried job = %+v, want cleared error code", got)
	}
	if processor.succeeded != 2 {
		t.Fatalf("Succeeded hooks = %d", processor.succeeded)
	}
}

func TestLeaseLossDuringWorkIsNotAFailedAttempt(t *testing.T) {
	seed, processor, executor, _ := executorFixture(t)
	executor.HeartbeatInterval = 5 * time.Millisecond
	job := seed.Job(t, "COMPARE", "env:a", identity.NewID(), nil)
	processor.err = func(running database.InventoryJob) error {
		// Another executor takes the job over; the heartbeat notices and cancels.
		seed.DB.Model(&database.InventoryJob{}).Where("id=?", running.ID).Update("lease_token", identity.NewID())
		time.Sleep(50 * time.Millisecond)
		return context.Canceled
	}
	if _, err := executor.RunDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := reload(t, seed.DB, job.ID); got.Status != "RUNNING" || got.ErrorCode != nil || len(processor.failed) != 0 {
		t.Fatalf("job = %+v, the new owner must keep it", got)
	}
}

func TestParallelExecutorsProcessEachJobOnce(t *testing.T) {
	seed, processor, executor, _ := executorFixture(t)
	for i := 0; i < 10; i++ {
		seed.Job(t, "COMPARE", fmt.Sprintf("env:%02d", i), identity.NewID(), nil)
	}
	second := *executor
	var wg sync.WaitGroup
	for _, e := range []*Executor{executor, &second} {
		wg.Add(1)
		go func(e *Executor) {
			defer wg.Done()
			for {
				worked, err := e.RunDue(context.Background())
				if err != nil {
					t.Error(err)
					return
				}
				if !worked {
					return
				}
			}
		}(e)
	}
	wg.Wait()
	counts := map[identity.ID]int{}
	for _, job := range processor.seen {
		counts[job.ID]++
	}
	if len(counts) != 10 {
		t.Fatalf("processed %d distinct jobs, want 10", len(counts))
	}
	for id, count := range counts {
		if count != 1 {
			t.Fatalf("job %s processed %d times", id, count)
		}
	}
}

func TestRetryDelaySchedule(t *testing.T) {
	for attempts, want := range map[int]time.Duration{1: 60 * time.Second, 2: 120 * time.Second, 3: 240 * time.Second} {
		if got := retryDelay(attempts); got != want {
			t.Errorf("retryDelay(%d) = %s, want %s", attempts, got, want)
		}
	}
}
