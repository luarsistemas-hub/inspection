package contest

import (
	"context"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/counterparty/core"
	"inspection/services/inspection/internal/features/counterparty/internal/slicetest"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
)

func TestUnit_ContestSetupRejectsMissingDependencies(t *testing.T) {
	if err := Setup(Dependencies{Service: slicetest.Service(slicetest.DB(t))}); err == nil {
		t.Fatal("Setup accepted a missing bus")
	}
	if err := Setup(Dependencies{Bus: mediator.New()}); err == nil {
		t.Fatal("Setup accepted a missing database")
	}
}

func TestUnit_UT289_ContestRegisteredCommandValidatesStatement(t *testing.T) {
	db := slicetest.DB(t)
	bus := mediator.New()
	if err := Setup(Dependencies{Bus: bus, Service: slicetest.Service(db)}); err != nil {
		t.Fatal(err)
	}
	tenantID := identity.NewID()
	row := slicetest.Invitation(t, db, tenantID, time.Now().UTC().Add(time.Hour), "PENDING")
	_, err := bus.Send(context.Background(), Command{Owner: core.SessionOwner{TenantID: tenantID, InvitationID: row.ID}, Statement: "   ", MutationID: "blank"})
	if code, field, _ := apperror.Public(err); code != apperror.InvalidInput || field != "statement" {
		t.Fatalf("UT-289 blank statement error=%v code=%s field=%q, want INVALID_INPUT/statement", err, code, field)
	}
	var stored database.CounterpartyInvitation
	if err := db.Where("id=?", row.ID).First(&stored).Error; err != nil || stored.Response != "PENDING" || stored.Statement != nil {
		t.Fatalf("UT-289 rejected statement changed invitation: %+v err=%v", stored, err)
	}
	_, err = bus.Send(context.Background(), Command{Owner: core.SessionOwner{TenantID: tenantID, InvitationID: identity.NewID()}, Statement: "x", MutationID: "unknown"})
	if code, _, _ := apperror.Public(err); code != apperror.NotFound {
		t.Fatalf("unknown invitation error=%v, want NOT_FOUND", err)
	}
}
