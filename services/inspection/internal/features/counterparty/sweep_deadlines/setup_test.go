package sweep_deadlines

import (
	"context"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/counterparty/internal/slicetest"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
)

func TestUnit_SweepDeadlinesSetupRejectsMissingDependencies(t *testing.T) {
	if err := Setup(Dependencies{Service: slicetest.Service(slicetest.DB(t))}); err == nil {
		t.Fatal("Setup accepted a missing bus")
	}
	if err := Setup(Dependencies{Bus: mediator.New()}); err == nil {
		t.Fatal("Setup accepted a missing database")
	}
}

func TestUnit_UT301_SweepDeadlinesRegisteredCommandMarksOnlyExpiredPending(t *testing.T) {
	db := slicetest.DB(t)
	bus := mediator.New()
	if err := Setup(Dependencies{Bus: bus, Service: slicetest.Service(db)}); err != nil {
		t.Fatal(err)
	}
	tenantID := identity.NewID()
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	expired := slicetest.Invitation(t, db, tenantID, now, "PENDING").ID
	open := slicetest.Invitation(t, db, tenantID, now.Add(time.Millisecond), "PENDING").ID
	acknowledged := slicetest.Invitation(t, db, tenantID, now.Add(-time.Hour), "ACKNOWLEDGED").ID
	result, err := bus.Send(context.Background(), Command{TenantID: tenantID, Now: now, Limit: 10})
	if err != nil || result.(int) != 1 {
		t.Fatalf("UT-301 sweep result=%v err=%v, want 1", result, err)
	}
	for id, want := range map[identity.ID]string{expired: "NO_RESPONSE", open: "PENDING", acknowledged: "ACKNOWLEDGED"} {
		var row database.CounterpartyInvitation
		if err := db.Where("id=?", id).First(&row).Error; err != nil || row.Response != want {
			t.Fatalf("UT-301 invitation response=%q err=%v, want %s", row.Response, err, want)
		}
	}
}
