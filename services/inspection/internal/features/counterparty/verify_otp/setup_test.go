package verify_otp

import (
	"context"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/counterparty/internal/slicetest"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/security"
)

func TestUnit_VerifyOTPSetupRejectsMissingDependencies(t *testing.T) {
	db := slicetest.DB(t)
	for name, mutate := range map[string]func(*Dependencies){
		"bus":      func(d *Dependencies) { d.Bus = nil },
		"database": func(d *Dependencies) { d.Service.DB = nil },
		"pepper":   func(d *Dependencies) { d.Service.Pepper = nil },
		"limits":   func(d *Dependencies) { d.Service.Limits = nil },
	} {
		t.Run(name, func(t *testing.T) {
			deps := Dependencies{Bus: mediator.New(), Service: slicetest.Service(db)}
			mutate(&deps)
			if err := Setup(deps); err == nil {
				t.Fatalf("Setup accepted missing %s", name)
			}
		})
	}
}

func TestUnit_VerifyOTPRegisteredHandlerRejectsUnknownToken(t *testing.T) {
	bus := mediator.New()
	if err := Setup(Dependencies{Bus: bus, Service: slicetest.Service(slicetest.DB(t))}); err != nil {
		t.Fatal(err)
	}
	token, err := security.NewScopedToken(identity.NewID())
	if err != nil {
		t.Fatal(err)
	}
	_, err = bus.Send(context.Background(), Command{Token: token, Code: "123456"})
	if code, field, _ := apperror.Public(err); code != apperror.NotFound || field != "token" {
		t.Fatalf("error=%v code=%s field=%q, want NOT_FOUND/token", err, code, field)
	}
}
