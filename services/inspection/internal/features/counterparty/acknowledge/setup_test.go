package acknowledge

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

func TestUnit_AcknowledgeSetupRejectsMissingDependencies(t *testing.T) {
	if err := Setup(Dependencies{Service: slicetest.Service(slicetest.DB(t))}); err == nil {
		t.Fatal("Setup accepted a missing bus")
	}
	if err := Setup(Dependencies{Bus: mediator.New()}); err == nil {
		t.Fatal("Setup accepted a missing database")
	}
}

func TestUnit_UT287_AcknowledgeRegisteredCommandRecordsResponse(t *testing.T) {
	db := slicetest.DB(t)
	bus := mediator.New()
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	service := slicetest.Service(db)
	service.Clock = func() time.Time { return now }
	if err := Setup(Dependencies{Bus: bus, Service: service}); err != nil {
		t.Fatal(err)
	}
	tenantID := identity.NewID()
	row := slicetest.Invitation(t, db, tenantID, now.Add(time.Hour), "PENDING")
	result, err := bus.Send(context.Background(), Command{Owner: core.SessionOwner{TenantID: tenantID, InvitationID: row.ID}, MutationID: "ack"})
	if err != nil {
		t.Fatal(err)
	}
	if got := result.(database.CounterpartyInvitation); got.Response != "ACKNOWLEDGED" || got.RespondedAt == nil || !got.RespondedAt.Equal(now) {
		t.Fatalf("UT-287 acknowledgment=%+v", got)
	}
	_, err = bus.Send(context.Background(), Command{Owner: core.SessionOwner{TenantID: tenantID, InvitationID: identity.NewID()}, MutationID: "ack"})
	if code, field, _ := apperror.Public(err); code != apperror.NotFound || field != "counterpartyInvitationId" {
		t.Fatalf("unknown invitation error=%v code=%s field=%q", err, code, field)
	}
}
