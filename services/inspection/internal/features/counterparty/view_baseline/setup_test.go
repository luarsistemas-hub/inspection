package view_baseline

import (
	"context"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/counterparty/core"
	"inspection/services/inspection/internal/features/counterparty/internal/slicetest"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/mediator"
)

func TestUnit_ViewBaselineSetupRejectsMissingDependencies(t *testing.T) {
	if err := Setup(Dependencies{Service: slicetest.Service(slicetest.DB(t))}); err == nil {
		t.Fatal("Setup accepted a missing bus")
	}
	if err := Setup(Dependencies{Bus: mediator.New()}); err == nil {
		t.Fatal("Setup accepted a missing database")
	}
}

func TestUnit_ViewBaselineRegistersQuery(t *testing.T) {
	bus := mediator.New()
	if err := Setup(Dependencies{Bus: bus, Service: slicetest.Service(slicetest.DB(t))}); err != nil {
		t.Fatal(err)
	}
	if _, err := bus.Send(context.Background(), Query{}); err == nil {
		t.Fatal("view baseline was registered as a command")
	}
	_, err := bus.Ask(context.Background(), Query{Owner: core.SessionOwner{TenantID: identity.NewID(), InvitationID: identity.NewID()}})
	if code, field, _ := apperror.Public(err); code != apperror.NotFound || field != "counterpartyInvitationId" {
		t.Fatalf("error=%v code=%s field=%q, want NOT_FOUND/counterpartyInvitationId", err, code, field)
	}
}
