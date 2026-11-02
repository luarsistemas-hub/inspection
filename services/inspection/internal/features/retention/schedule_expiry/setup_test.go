package schedule_expiry

import (
	"context"
	"testing"
	"time"

	"inspection/libs/identity"
)

func TestEventIDIsDeterministicPerTenantInspectionAndDay(t *testing.T) {
	tenant, inspection := identity.NewID(), identity.NewID()
	day := time.Date(2026, 11, 1, 3, 4, 5, 0, time.UTC)
	if EventID(tenant, inspection, day) != EventID(tenant, inspection, day.Add(10*time.Hour)) {
		t.Fatal("event identity changes within the same UTC day")
	}
	if EventID(tenant, inspection, day) == EventID(tenant, inspection, day.Add(24*time.Hour)) {
		t.Fatal("the next day reuses the identity, so the consumer inbox would swallow the re-emission")
	}
	if EventID(tenant, inspection, day) == EventID(tenant, identity.NewID(), day) || EventID(tenant, inspection, day) == EventID(identity.NewID(), inspection, day) {
		t.Fatal("different tenants or inspections share one event identity")
	}
	if EventID(tenant, inspection, day) == OperationalEventID(tenant, inspection, day) {
		t.Fatal("evidence and operational classes share one event identity")
	}
	if SecurityEventID(tenant, day) == SecurityEventID(identity.NewID(), day) || SecurityEventID(tenant, day) == SecurityEventID(tenant, day.Add(24*time.Hour)) {
		t.Fatal("security event identity ignores tenant or day")
	}
}

func TestEventIDBucketsByUTCDay(t *testing.T) {
	tenant, inspection := identity.NewID(), identity.NewID()
	late := time.Date(2026, 11, 1, 23, 30, 0, 0, time.FixedZone("west", -3*3600))
	if EventID(tenant, inspection, late) != EventID(tenant, inspection, late.UTC()) {
		t.Fatal("the bucket depends on the time zone of the caller")
	}
}

func TestEmitDueRejectsMissingDependencies(t *testing.T) {
	if _, err := EmitDue(context.Background(), nil, identity.NewID(), time.Now(), 1); err == nil {
		t.Fatal("missing database accepted")
	}
}
