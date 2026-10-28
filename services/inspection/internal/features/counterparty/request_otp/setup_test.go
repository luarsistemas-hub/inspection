package request_otp

import (
	"context"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/counterparty/core"
	"inspection/services/inspection/internal/features/counterparty/internal/slicetest"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/security"
)

func TestUnit_RequestOTPSetupRejectsMissingDependencies(t *testing.T) {
	db := slicetest.DB(t)
	for name, mutate := range map[string]func(*Dependencies){
		"bus":      func(d *Dependencies) { d.Bus = nil },
		"database": func(d *Dependencies) { d.Service.DB = nil },
		"pepper":   func(d *Dependencies) { d.Service.Pepper = make([]byte, 31) },
		"limits":   func(d *Dependencies) { d.Service.Limits = nil },
		"notifier": func(d *Dependencies) { d.Service.Notifier = nil },
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

func TestUnit_UT308_RequestOTPRegisteredHandlerRejectsUnknownToken(t *testing.T) {
	bus := mediator.New()
	if err := Setup(Dependencies{Bus: bus, Service: slicetest.Service(slicetest.DB(t))}); err != nil {
		t.Fatal(err)
	}
	wellFormed, err := security.NewScopedToken(identity.NewID())
	if err != nil {
		t.Fatal(err)
	}
	for name, token := range map[string]string{"UT-308 malformed token": "not-a-token", "UT-308 well-formed unknown token": wellFormed} {
		t.Run(name, func(t *testing.T) {
			_, err := bus.Send(context.Background(), Command{Token: token})
			if code, field, _ := apperror.Public(err); code != apperror.NotFound || field != "token" {
				t.Fatalf("error=%v code=%s field=%q, want NOT_FOUND/token", err, code, field)
			}
		})
	}
	if err := Setup(Dependencies{Bus: bus, Service: core.Service{DB: slicetest.DB(t), Pepper: make([]byte, 32), Limits: slicetest.AllowLimits{}, Notifier: slicetest.NoopNotifier{}}}); err == nil {
		t.Fatal("duplicate registration was accepted")
	}
}
