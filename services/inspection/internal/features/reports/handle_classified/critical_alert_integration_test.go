//go:build integration

package handle_classified

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	activateinternal "inspection/services/inspection/internal/features/access/activate_internal_user"
	executedelivery "inspection/services/inspection/internal/features/notifications/execute_delivery"
	notificationrequest "inspection/services/inspection/internal/features/notifications/request"
	generatesnapshot "inspection/services/inspection/internal/features/reports/generate_snapshot"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"
	"inspection/services/inspection/internal/platform/notifications"
	"inspection/services/inspection/internal/platform/observability"
	"inspection/services/inspection/internal/platform/ratelimit"
	"inspection/services/inspection/internal/platform/security"

	"gorm.io/gorm"
)

var (
	criticalIntegrationOnce sync.Once
	criticalIntegration     *criticalIntegrationEnv
	criticalIntegrationErr  error
)

type criticalIntegrationEnv struct {
	harness *harness.Harness
}

func TestAIP_IT_291InvalidCriticalRecipientIsSkippedWithoutRollingBackReport(t *testing.T) {
	withCriticalFixture(t, func(ctx context.Context, h *harness.Harness, fixture harness.Task06Fixture, serviceDependencies notificationrequest.Dependencies) {
		if err := addCriticalChannel(ctx, h, fixture, fixture.MembershipID, "invalid destination", true); err != nil {
			t.Fatal(err)
		}
		if err := submitCritical(ctx, h, fixture, serviceDependencies, identity.NewID(), "ATTENTION", "CRITICAL"); err != nil {
			t.Fatalf("invalid recipient rolled back classified event: %v", err)
		}
		if count := countDeliveries(t, h, ctx, fixture.TenantID); count != 0 {
			t.Fatalf("invalid alert persisted %d deliveries", count)
		}
		var triageCount int64
		if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
			return tx.Model(&database.TriageCase{}).Where("tenant_id=? AND inspection_id=?", fixture.TenantID, fixture.InspectionID).Count(&triageCount).Error
		}); err != nil || triageCount != 1 {
			t.Fatalf("triage case count=%d err=%v; invalid recipient must not roll it back", triageCount, err)
		}
	})
}

func TestAIP_B7ActivatedInternalUserReceivesCriticalAlertThroughRealInbox(t *testing.T) {
	withCriticalFixture(t, func(ctx context.Context, h *harness.Harness, fixture harness.Task06Fixture, deps notificationrequest.Dependencies) {
		membershipID := identity.NewID()
		token, err := security.NewOpaqueToken()
		if err != nil {
			t.Fatal(err)
		}
		digest := security.HashToken(token)
		now := time.Now().UTC()
		if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
			if err := tx.Create(&database.Membership{ID: membershipID, TenantID: fixture.TenantID, IdentityID: identity.NewID(), Issuer: "integration", Subject: membershipID.String(), Email: "activated-manager@example.test", Role: "MANAGER", Status: "INVITED", InvitationStatus: "SENT", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
				return err
			}
			return tx.Create(&database.UserInvitation{ID: identity.NewID(), TenantID: fixture.TenantID, MembershipID: membershipID, Issuer: "integration", Subject: membershipID.String(), Email: "activated-manager@example.test", Name: "Activated manager", Status: "SENT", TokenDigest: digest[:], ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now}).Error
		}); err != nil {
			t.Fatal(err)
		}
		notifier, provider := &criticalActivationNotifier{}, criticalActivationProvider{}
		activation := activateinternal.Service{DB: h.DB, Pepper: []byte("01234567890123456789012345678901"), Limits: criticalActivationLimits{}, Notifier: notifier, Provider: provider, Clock: func() time.Time { return now }}
		claim, err := activation.ClaimInvitation(ctx, token)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := activation.RequestOTP(ctx, claim.Locator, claim.CSRF); err != nil {
			t.Fatal(err)
		}
		if _, err := activation.VerifyOTP(ctx, claim.Locator, claim.CSRF, notifier.code); err != nil {
			t.Fatal(err)
		}
		if _, err := activation.Complete(ctx, claim.Locator, claim.CSRF, "ignored"); err != nil {
			t.Fatal(err)
		}
		if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
			if err := tx.Create(&database.ProductEntitlement{ID: identity.NewID(), TenantID: fixture.TenantID, MembershipID: membershipID, Product: "DASHBOARD", CreatedAt: now}).Error; err != nil {
				return err
			}
			return tx.Create(&database.ResourceScope{ID: identity.NewID(), TenantID: fixture.TenantID, MembershipID: membershipID, Kind: "ASSET", ResourceID: fixture.AssetID, CreatedAt: now}).Error
		}); err != nil {
			t.Fatal(err)
		}
		if err := submitCritical(ctx, h, fixture, deps, identity.NewID(), "ATTENTION", "CRITICAL"); err != nil {
			t.Fatal(err)
		}
		var delivery database.Delivery
		var attempt database.ChannelAttempt
		if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
			if err := tx.Where("tenant_id=? AND inspection_id=? AND recipient_id=?", fixture.TenantID, fixture.InspectionID, membershipID).Take(&delivery).Error; err != nil {
				return err
			}
			return tx.Where("tenant_id=? AND delivery_id=?", fixture.TenantID, delivery.ID).Take(&attempt).Error
		}); err != nil {
			t.Fatalf("activated recipient has no critical delivery: %v", err)
		}
		if delivery.InspectionID == nil || *delivery.InspectionID != fixture.InspectionID || attempt.Destination != "activated-manager@example.test" {
			t.Fatalf("delivery=%+v attempt=%+v, want activated email and inspection identity", delivery, attempt)
		}
	})
}

func TestAIP_IT_292NoInternalRecipientKeepsClassificationWithoutDelivery(t *testing.T) {
	withCriticalFixture(t, func(ctx context.Context, h *harness.Harness, fixture harness.Task06Fixture, deps notificationrequest.Dependencies) {
		if err := submitCritical(ctx, h, fixture, deps, identity.NewID(), "ATTENTION", "CRITICAL"); err != nil {
			t.Fatal(err)
		}
		if count := countDeliveries(t, h, ctx, fixture.TenantID); count != 0 {
			t.Fatalf("no-recipient critical alert persisted %d deliveries", count)
		}
		var triage database.TriageCase
		if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
			return tx.Where("tenant_id=? AND inspection_id=?", fixture.TenantID, fixture.InspectionID).Take(&triage).Error
		}); err != nil {
			t.Fatalf("classification did not reach the dashboard triage projection: %v", err)
		}
		if triage.Classification != "CRITICAL" {
			t.Fatalf("dashboard triage classification=%s, want CRITICAL", triage.Classification)
		}
	})
}

func TestAIP_IT_293CriticalDeliveryRetainsInspectionIdentityForEachDestination(t *testing.T) {
	withCriticalFixture(t, func(ctx context.Context, h *harness.Harness, fixture harness.Task06Fixture, deps notificationrequest.Dependencies) {
		if err := addCriticalChannel(ctx, h, fixture, fixture.MembershipID, "burst-one@example.test", true); err != nil {
			t.Fatal(err)
		}
		if err := addCriticalChannel(ctx, h, fixture, fixture.MembershipID, "burst-two@example.test", true); err != nil {
			t.Fatal(err)
		}
		if err := submitCritical(ctx, h, fixture, deps, identity.NewID(), "ATTENTION", "CRITICAL"); err != nil {
			t.Fatal(err)
		}
		var deliveries []database.Delivery
		if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
			return tx.Where("tenant_id=?", fixture.TenantID).Find(&deliveries).Error
		}); err != nil {
			t.Fatal(err)
		}
		if len(deliveries) != 2 {
			t.Fatalf("deliveries=%d, want one per verified destination", len(deliveries))
		}
		for _, delivery := range deliveries {
			if delivery.InspectionID == nil || *delivery.InspectionID != fixture.InspectionID {
				t.Fatalf("delivery %s inspection=%v, want %s", delivery.ID, delivery.InspectionID, fixture.InspectionID)
			}
		}
	})
}

func TestAIP_IT_294LosingInspectionScopeStopsFutureAlertDelivery(t *testing.T) {
	withCriticalFixture(t, func(ctx context.Context, h *harness.Harness, fixture harness.Task06Fixture, deps notificationrequest.Dependencies) {
		managerID := addScopedManager(t, ctx, h, fixture)
		if err := addCriticalChannel(ctx, h, fixture, managerID, "manager@example.test", true); err != nil {
			t.Fatal(err)
		}
		if err := submitCritical(ctx, h, fixture, deps, identity.NewID(), "ATTENTION", "CRITICAL"); err != nil {
			t.Fatal(err)
		}
		if count := countDeliveries(t, h, ctx, fixture.TenantID); count != 1 {
			t.Fatalf("manager did not receive positive control before revocation: %d", count)
		}
		secondInspection := cloneInspection(t, ctx, h, fixture)
		if err := revokeManagerScope(ctx, h, fixture, managerID); err != nil {
			t.Fatal(err)
		}
		if err := submitCritical(ctx, h, secondInspection, deps, identity.NewID(), "ATTENTION", "CRITICAL"); err != nil {
			t.Fatal(err)
		}
		if count := countDeliveries(t, h, ctx, fixture.TenantID); count != 1 {
			t.Fatalf("revoked recipient received a post-revocation delivery; total=%d", count)
		}
	})
}

func TestAIP_IT_295MultipleFindingsProduceOnePerRecipientChannel(t *testing.T) {
	withCriticalFixture(t, func(ctx context.Context, h *harness.Harness, fixture harness.Task06Fixture, deps notificationrequest.Dependencies) {
		if err := addCriticalChannel(ctx, h, fixture, fixture.MembershipID, "review@example.test", true); err != nil {
			t.Fatal(err)
		}
		secondInspection := cloneInspection(t, ctx, h, fixture)
		for _, inspection := range []harness.Task06Fixture{fixture, secondInspection} {
			if err := submitCritical(ctx, h, inspection, deps, identity.NewID(), "ATTENTION", "CRITICAL"); err != nil {
				t.Fatal(err)
			}
		}
		var deliveries []database.Delivery
		if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
			return tx.Where("tenant_id=?", fixture.TenantID).Find(&deliveries).Error
		}); err != nil {
			t.Fatal(err)
		}
		if len(deliveries) != 2 {
			t.Fatalf("distinct inspections produced %d deliveries, want one each", len(deliveries))
		}
		seen := map[identity.ID]bool{}
		for _, delivery := range deliveries {
			if delivery.InspectionID == nil {
				t.Fatalf("delivery %s has no inspection identity", delivery.ID)
			}
			seen[*delivery.InspectionID] = true
		}
		if !seen[fixture.InspectionID] || !seen[secondInspection.InspectionID] {
			t.Fatalf("deliveries reference inspections %v, want %s and %s", seen, fixture.InspectionID, secondInspection.InspectionID)
		}
	})
}

func TestAIP_IT_296ProviderWorkStartsFromDurableRetryableOutbox(t *testing.T) {
	withCriticalFixture(t, func(ctx context.Context, h *harness.Harness, fixture harness.Task06Fixture, deps notificationrequest.Dependencies) {
		if err := addCriticalChannel(ctx, h, fixture, fixture.MembershipID, "retry@example.test", true); err != nil {
			t.Fatal(err)
		}
		if err := submitCritical(ctx, h, fixture, deps, identity.NewID(), "ATTENTION", "CRITICAL"); err != nil {
			t.Fatal(err)
		}
		var attempt database.ChannelAttempt
		var delivery database.Delivery
		if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
			if err := tx.Where("tenant_id=? AND inspection_id=?", fixture.TenantID, fixture.InspectionID).Take(&delivery).Error; err != nil {
				return err
			}
			return tx.Where("tenant_id=? AND delivery_id=?", fixture.TenantID, delivery.ID).Take(&attempt).Error
		}); err != nil {
			t.Fatal(err)
		}
		if err := criticalIntegration.harness.AdminDB.Model(&database.ChannelAttempt{}).Where("tenant_id=? AND status='QUEUED' AND id<>?", fixture.TenantID, attempt.ID).Update("status", "CANCELED").Error; err != nil {
			t.Fatal(err)
		}
		if attempt.Status != "QUEUED" || attempt.Attempts != 0 {
			t.Fatalf("provider attempt = status %s count %d, want durable queued work before provider call", attempt.Status, attempt.Attempts)
		}
		var outbox int64
		if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
			return tx.Model(&database.OutboxIntent{}).Where("tenant_id=? AND type='notification.delivery_requested.v2'", fixture.TenantID).Count(&outbox).Error
		}); err != nil {
			t.Fatal(err)
		}
		if outbox != 1 {
			t.Fatalf("notification outbox rows = %d, want 1", outbox)
		}
		now := time.Now().UTC().Add(time.Hour)
		gateway, err := notifications.NewGateway(map[notifications.Channel]map[notifications.Provider]notifications.Adapter{
			notifications.Email: {notifications.ProviderSMTP: failingCriticalProvider{}},
		})
		if err != nil {
			t.Fatal(err)
		}
		executor, err := executedelivery.Setup(executedelivery.Dependencies{DB: criticalIntegration.harness.AdminDB, Gateway: gateway, Metrics: observability.NewMetrics(), Now: func() time.Time { return now }, ProviderAccounts: map[notifications.Provider]string{notifications.ProviderSMTP: "integration"}})
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 4; i++ {
			now = now.Add(6 * time.Minute)
			processed, err := executor.RunDue(ctx)
			if err != nil || !processed {
				t.Fatalf("provider attempt %d processed=%t err=%v", i+1, processed, err)
			}
		}
		if err := criticalIntegration.harness.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
			if err := tx.Where("tenant_id=? AND delivery_id=?", fixture.TenantID, delivery.ID).Take(&attempt).Error; err != nil {
				return err
			}
			var histories int64
			if err := tx.Model(&database.NotificationAttempt{}).Where("tenant_id=? AND channel_attempt_id=?", fixture.TenantID, attempt.ID).Count(&histories).Error; err != nil {
				return err
			}
			if attempt.Status != "FAILED" || attempt.Attempts != 4 || histories != 4 {
				return fmt.Errorf("final provider state status=%s attempts=%d histories=%d", attempt.Status, attempt.Attempts, histories)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})
}

func TestAIP_B7ReclassificationKeepsOneCriticalAlertAcrossCorrelationAndDestinationChanges(t *testing.T) {
	withCriticalFixture(t, func(ctx context.Context, h *harness.Harness, fixture harness.Task06Fixture, deps notificationrequest.Dependencies) {
		channelID := identity.NewID()
		now := time.Now().UTC()
		if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
			return tx.Create(&database.RecipientChannel{ID: channelID, TenantID: fixture.TenantID, RecipientMembershipID: fixture.MembershipID, Channel: "EMAIL", Destination: "first-reclassification@example.test", VerifiedAt: &now, Selected: true, Version: 1, CreatedAt: now, UpdatedAt: now}).Error
		}); err != nil {
			t.Fatal(err)
		}
		transitions := []struct{ previous, current, correlation string }{
			{"ATTENTION", "CRITICAL", "correlation-first"},
			{"CRITICAL", "ATTENTION", "correlation-second"},
			{"ATTENTION", "CRITICAL", "correlation-third"},
		}
		for index, transition := range transitions {
			if index == 2 {
				if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
					return tx.Model(&database.RecipientChannel{}).Where("tenant_id=? AND id=?", fixture.TenantID, channelID).Updates(map[string]any{"destination": "changed-reclassification@example.test", "updated_at": time.Now().UTC()}).Error
				}); err != nil {
					t.Fatal(err)
				}
			}
			if err := submitCriticalWithCorrelation(ctx, h, fixture, deps, identity.NewID(), transition.previous, transition.current, transition.correlation); err != nil {
				t.Fatalf("transition %s to %s rolled back: %v", transition.previous, transition.current, err)
			}
		}
		if count := countDeliveries(t, h, ctx, fixture.TenantID); count != 1 {
			t.Fatalf("critical reclassifications persisted %d alerts, want one", count)
		}
		var triage database.TriageCase
		var inboxCount int64
		if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
			if err := tx.Where("tenant_id=? AND inspection_id=?", fixture.TenantID, fixture.InspectionID).Take(&triage).Error; err != nil {
				return err
			}
			return tx.Model(&database.InboxReceipt{}).Where("tenant_id=? AND consumer=?", fixture.TenantID, "critical-alert-integration").Count(&inboxCount).Error
		}); err != nil {
			t.Fatal(err)
		}
		if triage.Classification != "CRITICAL" || inboxCount != 3 {
			t.Fatalf("triage=%+v inbox events=%d; all three events should commit", triage, inboxCount)
		}
	})
}

type failingCriticalProvider struct{}

type criticalActivationNotifier struct{ code string }

func (n *criticalActivationNotifier) SendOTP(_ context.Context, _, code, _ string) error {
	n.code = code
	return nil
}

type criticalActivationProvider struct{}

func (criticalActivationProvider) VerifyEmail(context.Context, string) error { return nil }
func (criticalActivationProvider) SetInitialPassword(context.Context, string, string) error {
	return nil
}

type criticalActivationLimits struct{}

func (criticalActivationLimits) AllowSend(context.Context, string) (ratelimit.Result, error) {
	return ratelimit.Result{Allowed: true}, nil
}
func (criticalActivationLimits) AllowResend(context.Context, string) (ratelimit.Result, error) {
	return ratelimit.Result{Allowed: true}, nil
}
func (criticalActivationLimits) AllowAttempt(context.Context, string) (ratelimit.Result, error) {
	return ratelimit.Result{Allowed: true}, nil
}

func (failingCriticalProvider) Send(context.Context, notifications.DeliveryIntent) (notifications.Receipt, error) {
	return notifications.Receipt{}, &notifications.DeliveryError{Kind: notifications.ErrorTransient, Code: "temporary_failure", PreSend: true}
}

func TestAIP_IT_297ClassifiedEventReplayDoesNotDuplicateOutbox(t *testing.T) {
	testSameEventReplayDoesNotDuplicate(t)
}

func TestAIP_IT_298AlertLinkRemainsInternalWhileReportIsPending(t *testing.T) {
	withCriticalFixture(t, func(ctx context.Context, h *harness.Harness, fixture harness.Task06Fixture, deps notificationrequest.Dependencies) {
		if err := addCriticalChannel(ctx, h, fixture, fixture.MembershipID, "pending@example.test", true); err != nil {
			t.Fatal(err)
		}
		eventID := identity.NewID()
		if err := submitCritical(ctx, h, fixture, deps, eventID, "ATTENTION", "CRITICAL"); err != nil {
			t.Fatal(err)
		}
		var attempt database.ChannelAttempt
		if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
			return tx.Where("tenant_id=?", fixture.TenantID).Take(&attempt).Error
		}); err != nil {
			t.Fatal(err)
		}
		var variables map[string]string
		if err := json.Unmarshal(attempt.TemplateVariables, &variables); err != nil {
			t.Fatal(err)
		}
		if variables["dashboardUrl"] != "inspection/"+fixture.InspectionID.String() || variables["reportUrl"] != "" {
			t.Fatalf("pending alert variables = %#v", variables)
		}
	})
}

func TestAIP_IT_299LaterNoncriticalStageKeepsHistoricalAlert(t *testing.T) {
	withCriticalFixture(t, func(ctx context.Context, h *harness.Harness, fixture harness.Task06Fixture, deps notificationrequest.Dependencies) {
		if err := addCriticalChannel(ctx, h, fixture, fixture.MembershipID, "history@example.test", true); err != nil {
			t.Fatal(err)
		}
		if err := submitCritical(ctx, h, fixture, deps, identity.NewID(), "ATTENTION", "CRITICAL"); err != nil {
			t.Fatal(err)
		}
		if err := submitCritical(ctx, h, fixture, deps, identity.NewID(), "CRITICAL", "ATTENTION"); err != nil {
			t.Fatal(err)
		}
		if count := countDeliveries(t, h, ctx, fixture.TenantID); count != 1 {
			t.Fatalf("historical alert count = %d, want one unchanged historical delivery", count)
		}
	})
}

func TestAIP_IT_300CriticalRecipientsAreTenantIsolated(t *testing.T) {
	withCriticalFixture(t, func(ctx context.Context, h *harness.Harness, first harness.Task06Fixture, deps notificationrequest.Dependencies) {
		second, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{InspectionStatus: "COMPLETED"})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = criticalIntegration.harness.AdminDB.Model(&database.ChannelAttempt{}).Where("tenant_id=? AND status='QUEUED'", second.TenantID).Update("status", "CANCELED").Error
		})
		if err := h.WithinTenant(ctx, second.TenantID, func(tx *gorm.DB) error {
			return tx.Create(&database.ProductEntitlement{ID: identity.NewID(), TenantID: second.TenantID, MembershipID: second.MembershipID, Product: "DASHBOARD", CreatedAt: time.Now().UTC()}).Error
		}); err != nil {
			t.Fatal(err)
		}
		if err := addCriticalChannel(ctx, h, first, first.MembershipID, "first@example.test", true); err != nil {
			t.Fatal(err)
		}
		if err := addCriticalChannel(ctx, h, second, second.MembershipID, "second@example.test", true); err != nil {
			t.Fatal(err)
		}
		if err := h.WithinTenant(ctx, second.TenantID, func(tx *gorm.DB) error {
			var inspection database.Inspection
			if err := tx.Where("tenant_id=? AND id=?", second.TenantID, second.InspectionID).Take(&inspection).Error; err != nil {
				return err
			}
			rows, err := loadCriticalRecipients(ctx, tx, second.TenantID, inspection)
			if err == nil && len(rows) != 1 {
				return fmt.Errorf("second tenant eligible channels = %d", len(rows))
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if err := submitCritical(ctx, h, first, deps, identity.NewID(), "ATTENTION", "CRITICAL"); err != nil {
			t.Fatal(err)
		}
		if err := submitCritical(ctx, h, second, deps, identity.NewID(), "ATTENTION", "CRITICAL"); err != nil {
			t.Fatal(err)
		}
		firstCount, secondCount := countDeliveries(t, h, ctx, first.TenantID), countDeliveries(t, h, ctx, second.TenantID)
		if firstCount != 1 || secondCount != 1 {
			t.Fatalf("tenant delivery counts = %d, %d; want one per tenant", firstCount, secondCount)
		}
	})
}

func testSameEventReplayDoesNotDuplicate(t *testing.T) {
	t.Helper()
	withCriticalFixture(t, func(ctx context.Context, h *harness.Harness, fixture harness.Task06Fixture, deps notificationrequest.Dependencies) {
		if err := addCriticalChannel(ctx, h, fixture, fixture.MembershipID, "replay@example.test", true); err != nil {
			t.Fatal(err)
		}
		eventID := identity.NewID()
		if err := submitCritical(ctx, h, fixture, deps, eventID, "ATTENTION", "CRITICAL"); err != nil {
			t.Fatal(err)
		}
		if err := submitCritical(ctx, h, fixture, deps, eventID, "ATTENTION", "CRITICAL"); err != nil {
			t.Fatal(err)
		}
		if count := countDeliveries(t, h, ctx, fixture.TenantID); count != 1 {
			t.Fatalf("replayed classified event created %d deliveries", count)
		}
		var inboxCount int64
		if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
			return tx.Model(&database.InboxReceipt{}).Where("tenant_id=? AND consumer=? AND event_id=?", fixture.TenantID, "critical-alert-integration", eventID).Count(&inboxCount).Error
		}); err != nil || inboxCount != 1 {
			t.Fatalf("inbox receipt count=%d err=%v, want one receipt after replay", inboxCount, err)
		}
	})
}

func withCriticalFixture(t *testing.T, run func(context.Context, *harness.Harness, harness.Task06Fixture, notificationrequest.Dependencies)) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	criticalIntegrationOnce.Do(func() {
		cfg := harness.ConfigFromEnv()
		admin, err := database.Open(cfg.MigrationDatabaseURL)
		if err != nil {
			criticalIntegrationErr = err
			return
		}
		runtimeURL := cfg.RuntimeDatabaseURL
		if runtimeURL == "" {
			criticalIntegrationErr = fmt.Errorf("INSPECTION_TEST_RUNTIME_DATABASE_URL is required")
			return
		}
		runtimeDB, err := database.Open(runtimeURL)
		if err != nil {
			criticalIntegrationErr = err
			return
		}
		if cfg.Migrate {
			if err := (database.Migrator{DB: admin}).Migrate(ctx); err != nil {
				criticalIntegrationErr = err
				return
			}
		}
		criticalIntegration = &criticalIntegrationEnv{harness: &harness.Harness{Config: cfg, DB: runtimeDB, AdminDB: admin, MigrationDB: admin}}
	})
	if criticalIntegrationErr != nil {
		t.Fatal(criticalIntegrationErr)
	}
	fixture, err := criticalIntegration.harness.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{InspectionStatus: "COMPLETED"})
	if err != nil {
		t.Fatal(err)
	}
	providers, err := notifications.NewProviderResolver(notifications.ProviderTwilio)
	if err != nil {
		t.Fatal(err)
	}
	deps := notificationrequest.Dependencies{DB: criticalIntegration.harness.DB, Providers: providers, EnabledChannels: map[string]bool{"EMAIL": true}}
	if err := criticalIntegration.harness.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		return tx.Create(&database.ProductEntitlement{ID: identity.NewID(), TenantID: fixture.TenantID, MembershipID: fixture.MembershipID, Product: "DASHBOARD", CreatedAt: time.Now().UTC()}).Error
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = criticalIntegration.harness.AdminDB.Model(&database.ChannelAttempt{}).Where("tenant_id=? AND status='QUEUED'", fixture.TenantID).Update("status", "CANCELED").Error
	})
	run(ctx, criticalIntegration.harness, fixture, deps)
}

func addCriticalChannel(ctx context.Context, h *harness.Harness, f harness.Task06Fixture, membershipID identity.ID, destination string, selected bool) error {
	return h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
		now := time.Now().UTC()
		return tx.Create(&database.RecipientChannel{ID: identity.NewID(), TenantID: f.TenantID, RecipientMembershipID: membershipID, Channel: "EMAIL", Destination: destination, VerifiedAt: &now, Selected: selected, Version: 1, CreatedAt: now, UpdatedAt: now}).Error
	})
}

func cloneInspection(t *testing.T, ctx context.Context, h *harness.Harness, source harness.Task06Fixture) harness.Task06Fixture {
	t.Helper()
	clone := source
	clone.InspectionID = identity.NewID()
	err := h.WithinTenant(ctx, source.TenantID, func(tx *gorm.DB) error {
		var inspection database.Inspection
		if err := tx.Where("tenant_id=? AND id=?", source.TenantID, source.InspectionID).Take(&inspection).Error; err != nil {
			return err
		}
		inspection.ID = clone.InspectionID
		inspection.SourceKey = "critical-clone-" + clone.InspectionID.String()
		if err := tx.Create(&inspection).Error; err != nil {
			return err
		}
		var reference database.ReferenceSnapshot
		if err := tx.Where("tenant_id=? AND inspection_id=?", source.TenantID, source.InspectionID).Take(&reference).Error; err != nil {
			return err
		}
		reference.ID, reference.InspectionID = identity.NewID(), clone.InspectionID
		if err := tx.Create(&reference).Error; err != nil {
			return err
		}
		var policy database.PolicySnapshot
		if err := tx.Where("tenant_id=? AND inspection_id=?", source.TenantID, source.InspectionID).Take(&policy).Error; err != nil {
			return err
		}
		policy.ID, policy.InspectionID = identity.NewID(), clone.InspectionID
		return tx.Create(&policy).Error
	})
	if err != nil {
		t.Fatal(err)
	}
	return clone
}

func addScopedManager(t *testing.T, ctx context.Context, h *harness.Harness, f harness.Task06Fixture) identity.ID {
	t.Helper()
	id := identity.NewID()
	err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
		now := time.Now().UTC()
		if err := tx.Create(&database.Membership{ID: id, TenantID: f.TenantID, IdentityID: identity.NewID(), Issuer: "integration", Subject: id.String(), Role: "MANAGER", Status: "ACTIVE", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.ProductEntitlement{ID: identity.NewID(), TenantID: f.TenantID, MembershipID: id, Product: "DASHBOARD", CreatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.ResourceScope{ID: identity.NewID(), TenantID: f.TenantID, MembershipID: id, Kind: "ASSET", ResourceID: f.AssetID, CreatedAt: now}).Error
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func revokeManagerScope(ctx context.Context, h *harness.Harness, f harness.Task06Fixture, membershipID identity.ID) error {
	return h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND membership_id=?", f.TenantID, membershipID).Delete(&database.ResourceScope{}).Error
	})
}

func submitCritical(ctx context.Context, h *harness.Harness, f harness.Task06Fixture, deps notificationrequest.Dependencies, eventID identity.ID, previous, current string) error {
	return submitCriticalWithCorrelation(ctx, h, f, deps, eventID, previous, current, "critical-test")
}

func submitCriticalWithCorrelation(ctx context.Context, h *harness.Harness, f harness.Task06Fixture, deps notificationrequest.Dependencies, eventID identity.ID, previous, current, correlation string) error {
	now := time.Now().UTC()
	runID := identity.NewID()
	if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
		if previous != "" {
			if err := tx.Create(&database.ClassificationRun{ID: identity.NewID(), TenantID: f.TenantID, InspectionID: f.InspectionID, PromptSnapshotID: f.PromptSnapshotID, Classification: previous, ReasonCodes: json.RawMessage(`[]`), CreatedAt: now.Add(-time.Second)}).Error; err != nil {
				return err
			}
		}
		return tx.Create(&database.ClassificationRun{ID: runID, TenantID: f.TenantID, InspectionID: f.InspectionID, PromptSnapshotID: f.PromptSnapshotID, Classification: current, ReasonCodes: json.RawMessage(`[]`), CreatedAt: now}).Error
	}); err != nil {
		return err
	}
	service, err := notificationrequest.Setup(deps)
	if err != nil {
		return err
	}
	handler, err := Setup(Dependencies{Notifications: service, CreateSnapshot: func(context.Context, generatesnapshot.Input) (generatesnapshot.Result, error) {
		return generatesnapshot.Result{SnapshotID: identity.NewID(), Version: int(now.UnixNano())}, nil
	}})
	if err != nil {
		return err
	}
	envelope := events.Envelope[events.InspectionClassifiedPayload]{
		ID: eventID, Type: "inspection.classified.v1", SchemaVersion: 1, OccurredAt: now,
		TenantID: f.TenantID, AggregateID: f.InspectionID, CorrelationID: correlation,
		Payload: events.InspectionClassifiedPayload{InspectionID: f.InspectionID, ClassificationRunID: runID, Classification: current, ReasonCodes: []string{}},
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	consumer := messaging.Consumer{DB: h.DB, Registry: events.DefaultRegistry(), Name: "critical-alert-integration", Handle: handler}
	_, err = consumer.Process(ctx, body, 0)
	return err
}

func countDeliveries(t *testing.T, h *harness.Harness, ctx context.Context, tenantID identity.ID) int64 {
	t.Helper()
	var count int64
	if err := h.WithinTenant(ctx, tenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.Delivery{}).Where("tenant_id=?", tenantID).Count(&count).Error
	}); err != nil {
		t.Fatal(err)
	}
	return count
}
