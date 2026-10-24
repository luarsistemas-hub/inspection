package configure_video_policy

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestPolicyDefaultsAndBoundsUT001UT003(t *testing.T) {
	if p := Defaults([16]byte{1}); p.MaxDurationMinutes != 15 || p.OriginRetentionDays != 30 || p.CurrentRetentionDays != 30 || p.SupersededBaselineRetentionDays != 365 || p.Version != 1 {
		t.Fatalf("unexpected defaults: %#v", p)
	}
	for _, tc := range []struct {
		duration, origin, current int
		valid                     bool
	}{{60, 36500, 1, true}, {61, 36500, 1, false}, {15, 36501, 1, false}, {15, 36500, 36501, false}, {0, 30, 5, false}, {15, 0, 5, false}} {
		err := Validate(tc.duration, tc.origin, tc.current)
		if (err == nil) != tc.valid {
			t.Fatalf("Validate(%d,%d,%d) err=%v", tc.duration, tc.origin, tc.current, err)
		}
	}
}

func TestPolicyQueriesAreTenantScopedAcrossOneHundredTenantsIT029(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"tenancy", "media", "audit"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, model := range []any{&database.Tenant{}, &database.VideoPolicy{}, &database.AuditEvent{}} {
		if err := db.AutoMigrate(model); err != nil && !strings.Contains(err.Error(), "no such table: main.") {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	tenants := make([]database.Tenant, 100)
	policies := make([]database.VideoPolicy, 100)
	for i := range policies {
		tenantID := identity.NewID()
		tenants[i] = database.Tenant{ID: tenantID, TenantID: tenantID, Name: fmt.Sprintf("Tenant %d", i), Language: "pt-BR", DefaultTimezone: "UTC", Status: "ACTIVE", Version: 1, CreatedAt: now, UpdatedAt: now}
		minutes := 10
		if i%2 == 1 {
			minutes = 15
		}
		policies[i] = database.VideoPolicy{TenantID: tenantID, MaxDurationMinutes: minutes, OriginRetentionDays: 30 + i, CurrentRetentionDays: 5 + i, Version: 2, UpdatedAt: now}
	}
	if err := db.Create(&tenants).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&policies).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&database.VideoPolicy{}).Where("tenant_id=?", policies[0].TenantID).Update("superseded_baseline_retention_days", 730).Error; err != nil {
		t.Fatal(err)
	}
	bus := mediator.New()
	if err := Setup(Dependencies{DB: db, Bus: bus, Authorizer: auth.Authorizer{}, Within: func(ctx context.Context, _ identity.ID, fn func(*gorm.DB) error) error {
		return fn(db.WithContext(ctx))
	}}); err != nil {
		t.Fatal(err)
	}
	actorID := identity.NewID()
	for i, policy := range policies {
		ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: policy.TenantID, Principal: requestctx.Principal{IdentityID: actorID, TenantID: policy.TenantID, Roles: []string{auth.TenantAdmin}}, CorrelationID: "video-policy-tenant-scope"})
		raw, err := bus.Ask(ctx, Query{TenantID: policy.TenantID})
		if err != nil {
			t.Fatalf("tenant %d query failed: %v", i, err)
		}
		got := raw.(database.VideoPolicy)
		want := policies[i]
		if got.TenantID != policy.TenantID || got.MaxDurationMinutes != want.MaxDurationMinutes || got.OriginRetentionDays != want.OriginRetentionDays || got.CurrentRetentionDays != want.CurrentRetentionDays || got.Version != want.Version {
			t.Fatalf("tenant %d received another policy: got=%+v want=%+v", i, got, want)
		}
	}
	if _, err := bus.Ask(context.Background(), CaptureQuery{TenantID: policies[0].TenantID}); err == nil {
		t.Fatal("capture policy read accepted without external credentials")
	}
	external := requestctx.WithExternalCredentials(context.Background(), requestctx.ExternalCredentials{SessionToken: "validated-at-boundary", CSRFToken: "proof"})
	if raw, err := bus.Ask(external, CaptureQuery{TenantID: policies[0].TenantID}); err != nil || raw.(database.VideoPolicy).MaxDurationMinutes != 10 {
		t.Fatalf("external capture could not read its effective policy: %v (%+v)", err, raw)
	}
	command := Command{TenantID: policies[0].TenantID, MaxDurationMinutes: 11, OriginRetentionDays: 41, CurrentRetentionDays: 8, ExpectedVersion: 2, MutationID: "policy-idempotency"}
	ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: command.TenantID, Principal: requestctx.Principal{IdentityID: actorID, TenantID: command.TenantID, Roles: []string{auth.TenantAdmin}}, CorrelationID: "video-policy-idempotency"})
	first, err := bus.Send(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	if first.(database.VideoPolicy).SupersededBaselineRetentionDays != 730 {
		t.Fatalf("policy update lost superseded retention value: %+v", first)
	}
	replay, err := bus.Send(ctx, command)
	if err != nil || replay.(database.VideoPolicy).Version != first.(database.VideoPolicy).Version {
		t.Fatalf("same policy mutation was not idempotent: first=%+v replay=%+v err=%v", first, replay, err)
	}
	command.MaxDurationMinutes++
	if _, err := bus.Send(ctx, command); err == nil || func() bool { code, _, _ := apperror.Public(err); return code != apperror.Conflict }() {
		t.Fatalf("changed policy replay with the same mutation id did not return CONFLICT: %v", err)
	}
}

func TestSetupValidatesDependenciesBeforeRegistrationUT004(t *testing.T) {
	if err := Setup(Dependencies{Bus: mediator.New()}); err == nil {
		t.Fatal("missing database was accepted")
	}
	if err := Setup(Dependencies{DB: &gorm.DB{}}); err == nil {
		t.Fatal("missing bus was accepted")
	}
}
