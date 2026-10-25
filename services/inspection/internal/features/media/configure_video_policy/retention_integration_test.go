//go:build integration

package configure_video_policy

import (
	"context"
	"os"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/gorm"
)

func newPolicyIT(t *testing.T) (*harness.Harness, harness.Task06Fixture) {
	t.Helper()
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	fixture, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return h, fixture
}

// TestIntegration_UT318_NewPolicyRowUsesDatabaseDefaults inserts a policy row
// naming only its tenant (and the NOT NULL audit timestamp, which has no
// database default on schemas created by AutoMigrate) and reads the retention
// defaults owned by the schema (migration v84 and the model tags).
func TestIntegration_UT318_NewPolicyRowUsesDatabaseDefaults(t *testing.T) {
	h, fixture := newPolicyIT(t)
	if defaults := Defaults(fixture.TenantID); defaults.CurrentRetentionDays != 30 || defaults.SupersededBaselineRetentionDays != 365 {
		t.Fatalf("UT-318 Go defaults current=%d superseded=%d, want 30/365", defaults.CurrentRetentionDays, defaults.SupersededBaselineRetentionDays)
	}
	var policy database.VideoPolicy
	if err := h.WithinTenant(context.Background(), fixture.TenantID, func(tx *gorm.DB) error {
		if err := tx.Exec(`INSERT INTO media.video_policies(tenant_id,updated_at) VALUES (?,now())`, fixture.TenantID).Error; err != nil {
			return err
		}
		return tx.Where("tenant_id=?", fixture.TenantID).Take(&policy).Error
	}); err != nil {
		t.Fatal(err)
	}
	if policy.CurrentRetentionDays != 30 || policy.SupersededBaselineRetentionDays != 365 {
		t.Fatalf("UT-318 database defaults current=%d superseded=%d, want 30/365", policy.CurrentRetentionDays, policy.SupersededBaselineRetentionDays)
	}
}

// TestIntegration_UT316_LoweringRetentionKeepsAssignedSourceExpiry lowers both
// lifecycle periods through the command and checks an already assigned source
// expiry is untouched (US-036 EC-2).
func TestIntegration_UT316_LoweringRetentionKeepsAssignedSourceExpiry(t *testing.T) {
	h, fixture := newPolicyIT(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	expiry := now.AddDate(0, 0, 30)
	days := 30
	mediaID := identity.NewID()
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		if err := tx.Create(&database.MediaObject{ID: mediaID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, ObjectKey: "ut316/" + mediaID.String(), ContentType: "video/mp4", SHA256: "0000000000000000000000000000000000000000000000000000000000000000", SizeBytes: 1, Status: "READY", MediaKind: "VIDEO", IdempotencyKey: "ut316-" + mediaID.String(), AttentionItems: []byte(`[]`), Flags: []byte(`[]`), CreatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.VideoSource{ID: identity.NewID(), TenantID: fixture.TenantID, MediaID: mediaID, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 1_000_000, Width: 2, Height: 2, FrameRate: 30, ScreeningStatus: "APPROVED", FirstFrameReadyAt: &now, RetentionDays: &days, SourceExpiryAt: &expiry, RetentionAnchor: "FIXED", Status: "RETAINED", CreatedAt: now, UpdatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	bus := mediator.New()
	if err := Setup(Dependencies{DB: h.DB, Bus: bus, Authorizer: auth.Authorizer{}}); err != nil {
		t.Fatal(err)
	}
	requestCtx := requestctx.WithMetadata(ctx, requestctx.Metadata{TenantID: fixture.TenantID, Principal: requestctx.Principal{IdentityID: fixture.IdentityID, MembershipID: fixture.MembershipID, TenantID: fixture.TenantID, Roles: []string{auth.TenantAdmin}}})
	lowered := 1
	raw, err := bus.Send(requestCtx, Command{TenantID: fixture.TenantID, MaxDurationMinutes: 15, OriginRetentionDays: 1, CurrentRetentionDays: 1, SupersededBaselineRetentionDays: &lowered, ExpectedVersion: 1, MutationID: "ut316-lower"})
	if err != nil {
		t.Fatalf("UT-316 lower policy: %v", err)
	}
	if policy := raw.(database.VideoPolicy); policy.CurrentRetentionDays != 1 || policy.SupersededBaselineRetentionDays != 1 {
		t.Fatalf("UT-316 policy not lowered: %+v", policy)
	}
	var source database.VideoSource
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND media_id=?", fixture.TenantID, mediaID).Take(&source).Error
	}); err != nil {
		t.Fatal(err)
	}
	if source.SourceExpiryAt == nil || !source.SourceExpiryAt.Equal(expiry) || source.RetentionAnchor != "FIXED" {
		t.Fatalf("UT-316 lowering retention changed the assigned expiry: anchor=%s expiry=%v, want FIXED %v", source.RetentionAnchor, source.SourceExpiryAt, expiry)
	}
}
