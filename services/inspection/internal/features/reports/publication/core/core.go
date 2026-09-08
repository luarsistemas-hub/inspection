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
		return Policy{}, apperror.New(apperror.InvalidInput, "mode", "publication mode is invalid")
	}
	if expectedVersion != currentVersion {
		return Policy{}, apperror.New(apperror.Conflict, "expectedVersion", "stale publication policy version")
	}
	return Policy{Mode: mode, Version: currentVersion + 1}, nil
}

func CanPublish(snapshotFinal bool, currentStatus string) error {
	if !snapshotFinal {
		return apperror.New(apperror.InvalidState, "snapshotId", "only final snapshots can be published")
	}
	if currentStatus != "" && currentStatus != Published {
		return apperror.New(apperror.InvalidState, "publication", "publication is not publishable")
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

func Invalidate(status, reason string, expectedVersion, version int64, now time.Time) (string, *time.Time, error) {
	if strings.TrimSpace(reason) == "" {
		return "", nil, apperror.New(apperror.InvalidInput, "reason", "reason is required")
	}
	if expectedVersion != version {
		return "", nil, apperror.New(apperror.Conflict, "expectedVersion", "stale publication version")
	}
	if status == Invalidated {
		return Invalidated, nil, nil
	}
	if status != Published {
		return "", nil, apperror.New(apperror.InvalidState, "publication", "only published reports can be invalidated")
	}
	t := now.UTC()
	return Invalidated, &t, nil
}
