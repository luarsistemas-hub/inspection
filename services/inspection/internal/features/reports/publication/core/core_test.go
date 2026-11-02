package core

import (
	"testing"
	"time"

	"inspection/services/inspection/internal/platform/apperror"
)

func TestPolicyDefaultsAndVersionConflict(t *testing.T) {
	if got := EffectivePolicy(nil); got.Mode != Manual || got.Version != 0 {
		t.Fatalf("default policy: %+v", got)
	}
	if _, err := Configure(Automatic, 0, 1); err == nil {
		t.Fatal("stale policy accepted")
	}
	if got, err := Configure(Automatic, 0, 0); err != nil || got.Mode != Automatic || got.Version != 1 {
		t.Fatalf("configure: %+v %v", got, err)
	}
}

func TestPublicationTransitions(t *testing.T) {
	if _, _, err := Publish(false, "", time.Unix(0, 0)); err == nil {
		t.Fatal("non-final snapshot published")
	}
	if status, at, err := Publish(true, "", time.Unix(0, 0)); err != nil || status != Published || at == nil {
		t.Fatalf("publish: %s %v %v", status, at, err)
	}
	if status, _, err := Invalidate(Published, "safety review", 1, 1, time.Unix(0, 0)); err != nil || status != Invalidated {
		t.Fatalf("invalidate: %s %v", status, err)
	}
}

func TestInvalidationRequiresReasonAndOnlyPublishedUT032(t *testing.T) {
	now := time.Unix(0, 0)
	if _, _, err := Invalidate(Published, "  ", 1, 1, now); !apperror.Is(err, apperror.InvalidInput) {
		t.Fatalf("blank reason error = %v", err)
	}
	if _, _, err := Invalidate(Published, "motivo", 1, 2, now); !apperror.Is(err, apperror.Conflict) {
		t.Fatalf("stale version error = %v", err)
	}
	if _, _, err := Invalidate(Superseded, "motivo", 1, 1, now); !apperror.Is(err, apperror.InvalidState) {
		t.Fatalf("superseded invalidation error = %v", err)
	}
	status, at, err := Invalidate(Published, "motivo", 1, 1, now)
	if err != nil || status != Invalidated || at == nil {
		t.Fatalf("invalidate = %s %v %v", status, at, err)
	}
}

func TestRepeatedInvalidationIsIdempotentIT216(t *testing.T) {
	// The caller still holds the pre-invalidation version, which is stale now.
	status, at, err := Invalidate(Invalidated, "motivo", 1, 2, time.Unix(0, 0))
	if err != nil || status != Invalidated || at != nil {
		t.Fatalf("repeat = %s %v %v, want INVALIDATED without a new transition", status, at, err)
	}
	if _, _, err := Invalidate(Invalidated, "", 1, 2, time.Unix(0, 0)); !apperror.Is(err, apperror.InvalidInput) {
		t.Fatalf("a repeat with a blank reason must still be rejected: %v", err)
	}
}

// The publication state machine errors are shown to users in Portuguese.
func TestPublicationErrorMessagesArePortuguese(t *testing.T) {
	now := time.Unix(0, 0)
	_, _, blank := Invalidate(Published, " ", 1, 1, now)
	_, _, stale := Invalidate(Published, "motivo", 1, 2, now)
	_, _, notPublished := Invalidate(Superseded, "motivo", 1, 1, now)
	_, badMode := Configure("X", 0, 0)
	_, stalePolicy := Configure(Automatic, 0, 1)
	for err, want := range map[error]string{
		blank:                         "Informe o motivo da invalidação.",
		stale:                         "A publicação foi alterada; recarregue e tente novamente.",
		notPublished:                  "Somente laudos publicados podem ser invalidados.",
		CanPublish(false, ""):         "Somente laudos finais podem ser publicados.",
		CanPublish(true, Invalidated): "Esta publicação não pode ser publicada novamente.",
		badMode:                       "Modo de publicação inválido.",
		stalePolicy:                   "A política de publicação foi alterada; recarregue e tente novamente.",
	} {
		if err == nil || err.Error() != want {
			t.Errorf("message = %v, want %q", err, want)
		}
	}
}
