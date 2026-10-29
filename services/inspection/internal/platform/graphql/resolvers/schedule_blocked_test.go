package resolvers

import (
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"
)

func TestMapScheduleExposesBlockedOriginReasonAndTime(t *testing.T) {
	blockedAt := time.Date(2026, 10, 20, 12, 30, 45, 123, time.FixedZone("local", -3*60*60))
	reason := "ORIGIN_UNAVAILABLE"
	got := mapSchedule(database.Schedule{ID: identity.NewID(), BlockedReason: &reason, BlockedAt: &blockedAt})
	if got.BlockedReason == nil || *got.BlockedReason != reason {
		t.Fatalf("blockedReason = %v; want %q", got.BlockedReason, reason)
	}
	wantTime := blockedAt.UTC().Format(time.RFC3339Nano)
	if got.BlockedAt == nil || *got.BlockedAt != wantTime {
		t.Fatalf("blockedAt = %v; want %q", got.BlockedAt, wantTime)
	}
}

func TestMapScheduleLeavesBlockFieldsNullWhenUnblocked(t *testing.T) {
	got := mapSchedule(database.Schedule{ID: identity.NewID()})
	if got.BlockedReason != nil || got.BlockedAt != nil {
		t.Fatalf("unblocked schedule has reason %v and time %v; want null", got.BlockedReason, got.BlockedAt)
	}
}
