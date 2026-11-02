// Package core contains the report publication state machine.
package core

import (
	"strings"
	"time"

	"inspection/services/inspection/internal/platform/apperror"
)

const (
	Manual      = "MANUAL"
	Automatic   = "AUTOMATIC"
	Published   = "PUBLISHED"
	Invalidated = "INVALIDATED"
	Superseded  = "SUPERSEDED"
)

type Policy struct {
	Mode    string
	Version int64
}

func EffectivePolicy(policy *Policy) Policy {
	if policy == nil || policy.Mode == "" {
		return Policy{Mode: Manual, Version: 0}
	}
	return *policy
}

func Configure(mode string, expectedVersion, currentVersion int64) (Policy, error) {
	mode = strings.ToUpper(strings.TrimSpace(mode))
	if mode != Manual && mode != Automatic {
		return Policy{}, apperror.New(apperror.InvalidInput, "mode", "Modo de publicação inválido.")
	}
	if expectedVersion != currentVersion {
		return Policy{}, apperror.New(apperror.Conflict, "expectedVersion", "A política de publicação foi alterada; recarregue e tente novamente.")
	}
	return Policy{Mode: mode, Version: currentVersion + 1}, nil
}

func CanPublish(snapshotFinal bool, currentStatus string) error {
	if !snapshotFinal {
		return apperror.New(apperror.InvalidState, "snapshotId", "Somente laudos finais podem ser publicados.")
	}
	if currentStatus != "" && currentStatus != Published {
		return apperror.New(apperror.InvalidState, "publication", "Esta publicação não pode ser publicada novamente.")
	}
	return nil
}

func Publish(snapshotFinal bool, currentStatus string, now time.Time) (string, *time.Time, error) {
	if err := CanPublish(snapshotFinal, currentStatus); err != nil {
		return "", nil, err
	}
	t := now.UTC()
	return Published, &t, nil
}

// Invalidate validates the INVALIDATED transition. It returns a nil time when
// the publication is already invalidated, meaning no state change.
func Invalidate(status, reason string, expectedVersion, version int64, now time.Time) (string, *time.Time, error) {
	if strings.TrimSpace(reason) == "" {
		return "", nil, apperror.New(apperror.InvalidInput, "reason", "Informe o motivo da invalidação.")
	}
	// Repeating an invalidation is idempotent: the current state is returned
	// even when the caller still holds the pre-invalidation version.
	if status == Invalidated {
		return Invalidated, nil, nil
	}
	if expectedVersion != version {
		return "", nil, apperror.New(apperror.Conflict, "expectedVersion", "A publicação foi alterada; recarregue e tente novamente.")
	}
	if status != Published {
		return "", nil, apperror.New(apperror.InvalidState, "publication", "Somente laudos publicados podem ser invalidados.")
	}
	t := now.UTC()
	return Invalidated, &t, nil
}
