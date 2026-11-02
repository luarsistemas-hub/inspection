package expire_audio

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/objectstore"
)

type stubRepository struct {
	claims         []Deletion
	completed      []Deletion
	failedClaims   []Deletion
	calls          []string
	recoverErr     error
	expireErr      error
	requeueErr     error
	completeLost   bool
	claimErr       error
	requeued       int
	expiredCount   int
	recoveredCount int
}

func (r *stubRepository) DueTenants(context.Context, time.Duration) ([]identity.ID, error) {
	return nil, nil
}

func (r *stubRepository) RecoverLeases(context.Context, identity.ID, int, time.Duration) (int, int, error) {
	r.calls = append(r.calls, "recover")
	if r.recoverErr != nil {
		// A rolled-back scan can still report the rows it touched before failing.
		return 3, 1, r.recoverErr
	}
	return r.recoveredCount, 0, nil
}

func (r *stubRepository) ExpireNotes(context.Context, identity.ID, int, time.Duration) (int, error) {
	r.calls = append(r.calls, "expire")
	if r.expireErr != nil {
		return 2, r.expireErr
	}
	return r.expiredCount, nil
}

func (r *stubRepository) RequeueDue(context.Context, identity.ID, int, time.Duration) (int, error) {
	r.calls = append(r.calls, "requeue")
	if r.requeueErr != nil {
		return 2, r.requeueErr
	}
	return r.requeued, nil
}

func (r *stubRepository) ClaimDeletions(context.Context, identity.ID, int, time.Duration) ([]Deletion, error) {
	r.calls = append(r.calls, "claim")
	return r.claims, r.claimErr
}

func (r *stubRepository) CompleteDeletion(_ context.Context, _ identity.ID, claim Deletion) (bool, error) {
	r.completed = append(r.completed, claim)
	return !r.completeLost, nil
}

func (r *stubRepository) FailDeletion(_ context.Context, _ identity.ID, claim Deletion) (bool, error) {
	r.failedClaims = append(r.failedClaims, claim)
	return true, nil
}

type stubStore struct {
	failKey string
	uploads []string
	deleted []string
}

func (s *stubStore) DeleteUpload(_ context.Context, key, uploadID string) error {
	s.uploads = append(s.uploads, key+":"+uploadID)
	if key == s.failKey {
		return errors.New("storage unavailable")
	}
	s.deleted = append(s.deleted, key)
	return nil
}

func (s *stubStore) Delete(_ context.Context, key string) error {
	if key == s.failKey {
		return errors.New("storage unavailable")
	}
	s.deleted = append(s.deleted, key)
	return nil
}

type stubRecorder struct{ samples map[string]int }

func (r *stubRecorder) TranscriptionSweep(scan, outcome string, count int) {
	r.samples[scan+"/"+outcome] += count
}

func TestUT013SetupDeletesOriginalAndDerivativesAfterCompletedTranscript(t *testing.T) {
	note := identity.NewID()
	repo := &stubRepository{claims: []Deletion{{ID: identity.NewID(), NoteID: note, ObjectKey: "tenant/a/original/n1", UploadID: "upload-1", DerivativeKeys: []string{"tenant/a/derived/n1.ogg", ""}, LeaseToken: identity.NewID()}}}
	store := &stubStore{}
	recorder := &stubRecorder{samples: map[string]int{}}
	executor, err := Setup(Dependencies{Repository: repo, Store: store, Metrics: recorder})
	if err != nil {
		t.Fatal(err)
	}
	counts, err := executor.RunDue(context.Background(), identity.NewID(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if counts.Deleted != 1 || counts.Failed != 0 || counts.Total() != 1 {
		t.Fatalf("counts=%+v", counts)
	}
	if len(store.uploads) != 1 || store.uploads[0] != "tenant/a/original/n1:upload-1" {
		t.Fatalf("original upload cleanup=%v", store.uploads)
	}
	if len(store.deleted) != 2 || store.deleted[1] != "tenant/a/derived/n1.ogg" {
		t.Fatalf("deleted objects=%v", store.deleted)
	}
	if len(repo.completed) != 1 || repo.completed[0].NoteID != note || len(repo.failedClaims) != 0 {
		t.Fatalf("completed=%v failed=%v", repo.completed, repo.failedClaims)
	}
	if recorder.samples["cleanup/deleted"] != 1 {
		t.Fatalf("metrics=%v", recorder.samples)
	}
	want := []string{"recover", "expire", "requeue", "claim"}
	for i, call := range want {
		if i >= len(repo.calls) || repo.calls[i] != call {
			t.Fatalf("scan order=%v, want %v", repo.calls, want)
		}
	}
}

func TestUT014SetupKeepsCleanupPendingWhenStorageDeleteFails(t *testing.T) {
	failing := Deletion{ID: identity.NewID(), NoteID: identity.NewID(), ObjectKey: "tenant/a/original/n1", LeaseToken: identity.NewID()}
	healthy := Deletion{ID: identity.NewID(), NoteID: identity.NewID(), ObjectKey: "tenant/a/original/n2", DerivativeKeys: []string{"tenant/a/derived/n2.ogg"}, LeaseToken: identity.NewID()}
	derivativeFailure := Deletion{ID: identity.NewID(), NoteID: identity.NewID(), ObjectKey: "tenant/a/original/n3", DerivativeKeys: []string{"tenant/a/derived/n3.ogg"}, LeaseToken: identity.NewID()}
	repo := &stubRepository{claims: []Deletion{failing, healthy, derivativeFailure}}
	store := &stubStore{failKey: "tenant/a/original/n1"}
	executor, err := Setup(Dependencies{Repository: repo, Store: store})
	if err != nil {
		t.Fatal(err)
	}
	counts, err := executor.RunDue(context.Background(), identity.NewID(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if counts.Failed != 1 || counts.Deleted != 2 || counts.Requeued != 0 || counts.Recovered != 0 {
		t.Fatalf("counts=%+v", counts)
	}
	if len(repo.failedClaims) != 1 || repo.failedClaims[0].ID != failing.ID {
		t.Fatalf("failed claims=%v", repo.failedClaims)
	}

	store.failKey = "tenant/a/derived/n3.ogg"
	repo.claims, repo.completed, repo.failedClaims = []Deletion{derivativeFailure}, nil, nil
	counts, err = executor.RunDue(context.Background(), identity.NewID(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if counts.Failed != 1 || len(repo.completed) != 0 {
		t.Fatalf("derivative failure must keep the obligation pending: counts=%+v completed=%v", counts, repo.completed)
	}
}

func TestRunDueSkipsLostFencesAndReportsDependencyFailure(t *testing.T) {
	repo := &stubRepository{claims: []Deletion{{ID: identity.NewID(), NoteID: identity.NewID(), ObjectKey: "k", LeaseToken: identity.NewID()}}, completeLost: true}
	executor, err := Setup(Dependencies{Repository: repo, Store: &stubStore{}})
	if err != nil {
		t.Fatal(err)
	}
	counts, err := executor.RunDue(context.Background(), identity.NewID(), 10)
	if err != nil || counts.Deleted != 0 {
		t.Fatalf("lost fence counts=%+v err=%v", counts, err)
	}

	failure := errors.New("database unavailable")
	repo = &stubRepository{claimErr: failure}
	executor, _ = Setup(Dependencies{Repository: repo, Store: &stubStore{}})
	if _, err := executor.RunDue(context.Background(), identity.NewID(), 10); !errors.Is(err, failure) {
		t.Fatalf("claim error = %v, want %v", err, failure)
	}
}

func TestRunDueRunsEveryScanAndJoinsTheErrors(t *testing.T) {
	recoverFailure := errors.New("recover unavailable")
	expireFailure := errors.New("expire unavailable")
	requeueFailure := errors.New("requeue unavailable")
	claim := Deletion{ID: identity.NewID(), NoteID: identity.NewID(), ObjectKey: "tenant/a/original/n1", LeaseToken: identity.NewID()}
	cases := []struct {
		name    string
		repo    *stubRepository
		want    []error
		deleted int
	}{
		{name: "lease recovery fails", repo: &stubRepository{recoverErr: recoverFailure, claims: []Deletion{claim}}, want: []error{recoverFailure}, deleted: 1},
		{name: "expiry fails", repo: &stubRepository{expireErr: expireFailure, claims: []Deletion{claim}}, want: []error{expireFailure}, deleted: 1},
		{name: "retry wakeup fails", repo: &stubRepository{requeueErr: requeueFailure, claims: []Deletion{claim}}, want: []error{requeueFailure}, deleted: 1},
		{name: "every earlier scan fails", repo: &stubRepository{recoverErr: recoverFailure, expireErr: expireFailure, requeueErr: requeueFailure, claims: []Deletion{claim}}, want: []error{recoverFailure, expireFailure, requeueFailure}, deleted: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &stubStore{}
			recorder := &stubRecorder{samples: map[string]int{}}
			executor, err := Setup(Dependencies{Repository: tc.repo, Store: store, Metrics: recorder})
			if err != nil {
				t.Fatal(err)
			}
			counts, err := executor.RunDue(context.Background(), identity.NewID(), 10)
			for _, want := range tc.want {
				if !errors.Is(err, want) {
					t.Fatalf("RunDue() error = %v, want it to wrap %v", err, want)
				}
			}
			if want := []string{"recover", "expire", "requeue", "claim"}; fmt.Sprint(tc.repo.calls) != fmt.Sprint(want) {
				t.Fatalf("scans=%v, want all of %v", tc.repo.calls, want)
			}
			if counts.Deleted != tc.deleted || len(store.deleted) != tc.deleted || len(tc.repo.completed) != tc.deleted {
				t.Fatalf("cleanup did not run after the failure: counts=%+v deleted=%v", counts, store.deleted)
			}
			// Work reported by a scan that returned an error was rolled back.
			if (tc.repo.recoverErr != nil && (counts.Recovered != 0 || counts.Exhausted != 0)) || (tc.repo.expireErr != nil && counts.Expired != 0) || (tc.repo.requeueErr != nil && counts.Requeued != 0) {
				t.Fatalf("counts include rolled-back work: %+v", counts)
			}
			for _, key := range []string{"lease_recovery/recovered", "lease_recovery/exhausted", "expiry/expired", "retry_wakeup/requeued"} {
				if recorder.samples[key] != 0 {
					t.Fatalf("metrics recorded rolled-back work: %v", recorder.samples)
				}
			}
			if recorder.samples["cleanup/deleted"] != tc.deleted {
				t.Fatalf("cleanup metrics=%v", recorder.samples)
			}
		})
	}
}

func TestRunDueCleanupFailureKeepsCommittedCounts(t *testing.T) {
	failure := errors.New("database unavailable")
	repo := &stubRepository{claimErr: failure, recoveredCount: 2, expiredCount: 1, requeued: 3}
	executor, err := Setup(Dependencies{Repository: repo, Store: &stubStore{}})
	if err != nil {
		t.Fatal(err)
	}
	counts, err := executor.RunDue(context.Background(), identity.NewID(), 10)
	if !errors.Is(err, failure) || counts.Recovered != 2 || counts.Expired != 1 || counts.Requeued != 3 {
		t.Fatalf("counts=%+v err=%v", counts, err)
	}
}

func TestCleanupLogsOnlyTheStorageFailureClass(t *testing.T) {
	const key = "tenant/a/original/secret-object-key"
	deletion := Deletion{ID: identity.NewID(), NoteID: identity.NewID(), ObjectKey: key, LeaseToken: identity.NewID()}
	repo := &stubRepository{claims: []Deletion{deletion}}
	var lines []string
	executor, err := Setup(Dependencies{Repository: repo, Store: &failingKeyStore{err: fmt.Errorf("dependency unavailable: delete %s: %w", key, context.DeadlineExceeded)}, Logf: func(format string, args ...any) {
		lines = append(lines, fmt.Sprintf(format, args...))
	}})
	if err != nil {
		t.Fatal(err)
	}
	counts, err := executor.RunDue(context.Background(), identity.NewID(), 10)
	if err != nil || counts.Failed != 1 {
		t.Fatalf("counts=%+v err=%v", counts, err)
	}
	if len(lines) != 1 || !strings.Contains(lines[0], "class=timeout") || strings.Contains(lines[0], key) || strings.Contains(lines[0], "secret") {
		t.Fatalf("storage failure log=%q", lines)
	}
}

func TestStorageErrorClassIsBounded(t *testing.T) {
	cases := map[string]error{
		"timeout":     context.DeadlineExceeded,
		"canceled":    context.Canceled,
		"denied":      fmt.Errorf("x: %w", objectstore.ErrDenied),
		"invalid":     objectstore.ErrInvalid,
		"unavailable": errors.New("provider says bucket/secret-key failed"),
	}
	for want, err := range cases {
		if got := storageErrorClass(err); got != want {
			t.Errorf("storageErrorClass(%v) = %q, want %q", err, got, want)
		}
	}
}

// failingKeyStore fails every deletion with a fixed error.
type failingKeyStore struct{ err error }

func (s *failingKeyStore) DeleteUpload(context.Context, string, string) error { return s.err }
func (s *failingKeyStore) Delete(context.Context, string) error               { return s.err }

func TestRunDueRejectsInvalidInput(t *testing.T) {
	executor, err := Setup(Dependencies{Repository: &stubRepository{}, Store: &stubStore{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executor.RunDue(context.Background(), identity.ID{}, 10); err == nil {
		t.Fatal("RunDue accepted an empty tenant")
	}
	if _, err := executor.RunDue(context.Background(), identity.NewID(), 0); err == nil {
		t.Fatal("RunDue accepted an empty batch")
	}
}

func TestSetupRequiresDependencies(t *testing.T) {
	cases := map[string]Dependencies{
		"missing store":         {Repository: &stubRepository{}},
		"unconfigured store":    {Repository: &stubRepository{}, Store: objectstore.Store{}},
		"missing database":      {Store: &stubStore{}},
		"missing store with db": {},
	}
	for name, deps := range cases {
		if _, err := Setup(deps); err == nil {
			t.Errorf("%s: Setup accepted invalid dependencies", name)
		}
	}
}

func TestDeletionBackoffIsBounded(t *testing.T) {
	want := []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour, time.Hour}
	for attempt, duration := range want {
		if got := deletionBackoff(attempt + 1); got != duration {
			t.Errorf("attempt %d backoff=%s, want %s", attempt+1, got, duration)
		}
	}
}
