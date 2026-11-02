//go:build integration

package complete

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	getprompt "inspection/services/inspection/internal/features/analysis/get_prompt"
	assetget "inspection/services/inspection/internal/features/assets/get_asset"
	inspectioncore "inspection/services/inspection/internal/features/inspections/core"
	correctresponsibleemail "inspection/services/inspection/internal/features/invitations/correct_responsible_email"
	dispatchinvitation "inspection/services/inspection/internal/features/invitations/dispatch_capture_invitation"
	notificationcore "inspection/services/inspection/internal/features/notifications/core"
	executedelivery "inspection/services/inspection/internal/features/notifications/execute_delivery"
	notificationrequest "inspection/services/inspection/internal/features/notifications/request"
	deliverystatus "inspection/services/inspection/internal/features/onboarding/delivery_status"
	onboardingsession "inspection/services/inspection/internal/features/onboarding/session"
	participantget "inspection/services/inspection/internal/features/participants/get_participant"
	segmentresolve "inspection/services/inspection/internal/features/segments/resolve_definition"
	templateresolve "inspection/services/inspection/internal/features/templates/resolve_template"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	platformnotifications "inspection/services/inspection/internal/platform/notifications"
	"inspection/services/inspection/internal/platform/security"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

var reoMigration sync.Once
var reoMigrationErr error

type reoIntegrationSession struct {
	service onboardingsession.Service
	locator string
	csrf    string
	version int64
	id      identity.ID
	tenant  identity.ID
}

type reoNotificationRecorder struct {
	calls int
	err   error
	db    *gorm.DB
}

type reoFailingNotificationAdapter struct{}

func (reoFailingNotificationAdapter) Send(context.Context, platformnotifications.DeliveryIntent) (platformnotifications.Receipt, error) {
	return platformnotifications.Receipt{}, &platformnotifications.DeliveryError{Kind: platformnotifications.ErrorPermanent, Code: "provider_rejected", PreSend: true}
}

func (r *reoNotificationRecorder) Send(_ context.Context, request notificationcore.Notification) (notificationcore.NotificationResult, error) {
	r.calls++
	deliveryID := identity.NewID()
	status := string(notificationcore.StateQueued)
	if r.err != nil {
		err := r.err
		r.err = nil
		status = "FAILED"
		if r.db != nil {
			if persistErr := r.persist(request, deliveryID, status); persistErr != nil {
				return notificationcore.NotificationResult{}, persistErr
			}
		}
		return notificationcore.NotificationResult{}, err
	}
	if r.db != nil {
		if err := r.persist(request, deliveryID, status); err != nil {
			return notificationcore.NotificationResult{}, err
		}
	}
	return notificationcore.NotificationResult{ID: deliveryID, State: notificationcore.StateQueued}, nil
}

func (r *reoNotificationRecorder) persist(request notificationcore.Notification, deliveryID identity.ID, status string) error {
	now := time.Now().UTC()
	delivery := database.Delivery{ID: deliveryID, TenantID: request.TenantID, IntentID: identity.NewID(), InspectionID: request.InspectionID, InvitationID: request.InvitationID, Status: status, LogicalTemplate: request.Template.Name, TemplateVersion: request.Template.Version, CorrelationID: request.CorrelationID, IdempotencyKey: request.IdempotencyKey, RecipientID: "reo-test-recipient", CreatedAt: now, UpdatedAt: now}
	if err := r.db.Create(&delivery).Error; err != nil {
		return err
	}
	channel := database.ChannelAttempt{ID: identity.NewID(), TenantID: request.TenantID, DeliveryID: deliveryID, Channel: string(request.Channel), Destination: "reo-test-destination", Status: status, NextAttemptAt: now, CreatedAt: now, UpdatedAt: now}
	return r.db.Create(&channel).Error
}

func openREOIntegrationDB(t *testing.T) (*gorm.DB, *gorm.DB) {
	t.Helper()
	adminURL := os.Getenv("INSPECTION_TEST_DATABASE_URL")
	runtimeURL := os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL")
	if adminURL == "" || runtimeURL == "" {
		t.Skip("INSPECTION_TEST_DATABASE_URL and INSPECTION_TEST_RUNTIME_DATABASE_URL must target inspection_reo_tmp")
	}
	admin, err := gorm.Open(postgres.Open(adminURL), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := gorm.Open(postgres.Open(runtimeURL), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	reoMigration.Do(func() {
		reoMigrationErr = (database.Migrator{DB: admin}).Migrate(context.Background())
	})
	if reoMigrationErr != nil {
		t.Fatalf("migrate disposable REO database: %v", reoMigrationErr)
	}
	return admin, runtime
}

func reoSession(t *testing.T, admin, runtime *gorm.DB, currentStep string, version int64, checkpoints ...string) reoIntegrationSession {
	t.Helper()
	now := time.Now().UTC()
	locator, csrf := "reo-locator-"+identity.NewID().String(), "reo-csrf-"+identity.NewID().String()
	tenantID := identity.NewID()
	if err := admin.Create(&database.Tenant{ID: tenantID, TenantID: tenantID, Name: "REO onboarding test", Language: "pt-BR", DefaultTimezone: "America/Sao_Paulo", Status: "ACTIVE"}).Error; err != nil {
		t.Fatal(err)
	}
	pepper := []byte("reo-integration-pepper-with-at-least-32-bytes")
	locatorDigest := sha256.Sum256([]byte(locator))
	csrfDigest, err := security.CSRFProof(locatorDigest, csrf, pepper)
	if err != nil {
		t.Fatal(err)
	}
	row := database.OnboardingSession{ID: identity.NewID(), TenantID: &tenantID, Email: "reo-" + identity.NewID().String() + "@example.test", OwnerName: "REO Test", OwnerSubject: "reo-owner-subject", SessionLocatorDigest: locatorDigest[:], State: currentStep, CurrentStep: currentStep, DefinitionVersion: 4, Version: version, ExpiresAt: now.Add(time.Hour), CSRFDigest: csrfDigest[:], CreatedAt: now, UpdatedAt: now}
	if row.State == "AGENCY" || row.State == "PROPERTY" {
		row.State = onboardingsession.StateVerified
	}
	if err := admin.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	verifiedID := row.ID
	if err := admin.Create(&database.OnboardingEmailState{Email: row.Email, VerifiedSessionID: &verifiedID, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if len(checkpoints) > 0 {
		for index, step := range checkpoints {
			payload := []byte(`{"agencyName":"Agência"}`)
			if step == "PROPERTY" {
				payload = []byte(`{"address":"Rua Um","deadline":"2099-01-01","purpose":"SALE","propertyType":"APARTMENT"}`)
			}
			if step == "PARTICIPANT" {
				payload = []byte(`{"mode":"SELF"}`)
			}
			if err := admin.Create(&database.OnboardingStepRecord{ID: identity.NewID(), SessionID: row.ID, Step: step, Payload: payload, PayloadDigest: "seed", Version: int64(index + 1), CompletedAt: now}).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	service := onboardingsession.Service{DB: runtime, Pepper: pepper, Clock: func() time.Time { return now }}
	return reoIntegrationSession{service: service, locator: locator, csrf: csrf, version: version, id: row.ID, tenant: tenantID}
}

// reoCleanupTenant removes every row the delivery fixtures write for tenantID,
// ordered from dependents to parents. It is registered before seeding so a
// failed assertion still leaves the disposable database clean for later tests.
func reoCleanupTenant(t *testing.T, admin *gorm.DB, tenantID identity.ID) {
	t.Helper()
	t.Cleanup(func() {
		for _, model := range []any{
			&database.NotificationPayload{},
			&database.NotificationAttempt{},
			&database.ChannelAttempt{},
			&database.Delivery{},
			&database.OutboxIntent{},
			&database.AuditEvent{},
			&database.ExternalSession{},
			&database.OTPChallenge{},
			&database.Invitation{},
			&database.ChannelSelection{},
			&database.ParticipantContact{},
			&database.OnboardingRequest{},
			&database.Responsibility{},
			&database.Inspection{},
			&database.Participant{},
			&database.Asset{},
			&database.Tenant{},
		} {
			if err := admin.Where("tenant_id = ?", tenantID).Delete(model).Error; err != nil {
				t.Errorf("remove REO fixture rows %T: %v", model, err)
			}
		}
	})
}

func TestREO_IT_008InvalidPropertyHasNoCheckpointOrAsset(t *testing.T) {
	admin, runtime := openREOIntegrationDB(t)
	seed := reoSession(t, admin, runtime, onboardingsession.StepAgency, 1, "AGENCY")
	_, err := seed.service.Checkpoint(context.Background(), seed.locator, seed.csrf, "PROPERTY", seed.version, map[string]any{"address": "", "deadline": "2000-01-01", "purpose": "SALE"})
	if code, field, _ := apperror.Public(err); code != apperror.InvalidInput || field == "" {
		t.Fatalf("invalid property error=%v code=%s field=%q", err, code, field)
	}
	var checkpoints, assets int64
	if err := admin.Model(&database.OnboardingStepRecord{}).Where("session_id=? AND step='PROPERTY'", seed.id).Count(&checkpoints).Error; err != nil {
		t.Fatal(err)
	}
	if err := admin.Model(&database.Asset{}).Where("external_key=?", "onboarding-"+seed.id.String()).Count(&assets).Error; err != nil {
		t.Fatal(err)
	}
	if checkpoints != 0 || assets != 0 {
		t.Fatalf("invalid property persisted checkpoints=%d assets=%d", checkpoints, assets)
	}
}

func TestREO_IT_009ParticipantBeforePropertyIsRejected(t *testing.T) {
	admin, runtime := openREOIntegrationDB(t)
	seed := reoSession(t, admin, runtime, onboardingsession.StepAgency, 1, "AGENCY")
	_, err := seed.service.Checkpoint(context.Background(), seed.locator, seed.csrf, "PARTICIPANT", seed.version, map[string]any{"mode": "SELF"})
	if code, field, _ := apperror.Public(err); code != apperror.InvalidState || field == "" {
		t.Fatalf("out-of-order participant error=%v code=%s field=%q", err, code, field)
	}
	var row database.OnboardingSession
	if err := admin.First(&row, "id=?", seed.id).Error; err != nil {
		t.Fatal(err)
	}
	var participants int64
	if err := admin.Model(&database.OnboardingStepRecord{}).Where("session_id=? AND step='PARTICIPANT'", seed.id).Count(&participants).Error; err != nil {
		t.Fatal(err)
	}
	if row.CurrentStep != onboardingsession.StepAgency || row.Version != seed.version || participants != 0 {
		t.Fatalf("out-of-order checkpoint mutated session=%s version=%d participant rows=%d", row.CurrentStep, row.Version, participants)
	}
}

func TestREO_IT_010ConcurrentPropertySavesUseOptimisticVersion(t *testing.T) {
	admin, runtime := openREOIntegrationDB(t)
	seed := reoSession(t, admin, runtime, onboardingsession.StepAgency, 1, "AGENCY")
	payload := map[string]any{"address": "Rua A, 100", "deadline": "2099-01-01", "purpose": "SALE"}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := seed.service.Checkpoint(context.Background(), seed.locator, seed.csrf, "PROPERTY", seed.version, payload)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	var succeeded, conflicted int
	for err := range results {
		if err == nil {
			succeeded++
			continue
		}
		if code, _, _ := apperror.Public(err); code == apperror.Conflict {
			conflicted++
		} else {
			t.Fatalf("concurrent save unexpected error: %v", err)
		}
	}
	var count int64
	if err := admin.Model(&database.OnboardingStepRecord{}).Where("session_id=? AND step='PROPERTY'", seed.id).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if succeeded != 1 || conflicted != 1 || count != 1 {
		t.Fatalf("concurrent saves success=%d conflict=%d property rows=%d", succeeded, conflicted, count)
	}
}

func TestREO_UT014StaleCheckpointLeavesPersistedStepAndSessionUnchanged(t *testing.T) {
	admin, runtime := openREOIntegrationDB(t)
	seed := reoSession(t, admin, runtime, onboardingsession.StepProperty, 3, "AGENCY", "PROPERTY")
	var beforeSession database.OnboardingSession
	if err := admin.First(&beforeSession, "id=?", seed.id).Error; err != nil {
		t.Fatal(err)
	}
	var beforeStep database.OnboardingStepRecord
	if err := admin.Where("session_id=? AND step='PROPERTY'", seed.id).First(&beforeStep).Error; err != nil {
		t.Fatal(err)
	}
	_, err := seed.service.Checkpoint(context.Background(), seed.locator, seed.csrf, onboardingsession.StepProperty, 2, map[string]any{"address": "Rua alterada", "deadline": "2099-01-01", "purpose": "SALE"})
	if code, field, _ := apperror.Public(err); code != apperror.Conflict || field != "expectedVersion" {
		t.Fatalf("stale checkpoint error=%v code=%s field=%q", err, code, field)
	}
	var afterSession database.OnboardingSession
	if err := admin.First(&afterSession, "id=?", seed.id).Error; err != nil {
		t.Fatal(err)
	}
	var afterStep database.OnboardingStepRecord
	if err := admin.Where("session_id=? AND step='PROPERTY'", seed.id).First(&afterStep).Error; err != nil {
		t.Fatal(err)
	}
	if afterSession.Version != beforeSession.Version || afterSession.CurrentStep != beforeSession.CurrentStep || afterSession.State != beforeSession.State || string(afterStep.Payload) != string(beforeStep.Payload) || afterStep.PayloadDigest != beforeStep.PayloadDigest || afterStep.Version != beforeStep.Version {
		t.Fatalf("stale checkpoint changed persisted session or step: before=%+v/%+v after=%+v/%+v", beforeSession, beforeStep, afterSession, afterStep)
	}
}

func TestREO_IT_015MalformedDelegateDoesNotPersistParticipantCheckpoint(t *testing.T) {
	admin, runtime := openREOIntegrationDB(t)
	seed := reoSession(t, admin, runtime, onboardingsession.StepProperty, 2, "AGENCY", "PROPERTY")
	_, err := seed.service.Checkpoint(context.Background(), seed.locator, seed.csrf, "PARTICIPANT", seed.version, map[string]any{"mode": "DELEGATE", "name": "Pessoa", "email": "bad-email", "emailConfirmation": "bad-email"})
	if code, field, _ := apperror.Public(err); code != apperror.InvalidInput || field != "email" {
		t.Fatalf("malformed delegate error=%v code=%s field=%q", err, code, field)
	}
	var count int64
	if err := admin.Model(&database.OnboardingStepRecord{}).Where("session_id=? AND step='PARTICIPANT'", seed.id).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("malformed delegate persisted %d participant checkpoints", count)
	}
}

func TestREO_IT_016DelegateStatusIsTenantScopedAndOmitsTenantConfiguration(t *testing.T) {
	admin, runtime := openREOIntegrationDB(t)
	tenantID, foreignTenantID, sessionID, inspectionID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	for _, id := range []identity.ID{tenantID, foreignTenantID} {
		if err := admin.Create(&database.Tenant{ID: id, TenantID: id, Name: "REO status test", Language: "pt-BR", DefaultTimezone: "America/Sao_Paulo", Status: "ACTIVE"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := admin.Create(&database.OnboardingRequest{ID: identity.NewID(), TenantID: tenantID, SessionID: sessionID, InspectionID: &inspectionID, Status: "SUBMITTED", IdempotencyKey: "it-016", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range []database.Responsibility{
		{ID: identity.NewID(), TenantID: tenantID, InspectionID: inspectionID, ParticipantID: identity.NewID(), Status: "PENDING", Version: 1},
		{ID: identity.NewID(), TenantID: foreignTenantID, InspectionID: inspectionID, ParticipantID: identity.NewID(), Status: "SUBMITTED", Version: 2},
	} {
		if err := admin.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	var visibleResponsibilities int64
	if err := (tenanttx.Runner{DB: runtime}).Within(context.Background(), tenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.Responsibility{}).Where("inspection_id=?", inspectionID).Count(&visibleResponsibilities).Error
	}); err != nil {
		t.Fatal(err)
	}
	if visibleResponsibilities != 1 {
		t.Fatalf("RLS positive control expected one own responsibility, saw %d", visibleResponsibilities)
	}
	service := deliverystatus.Service{DB: runtime}
	status, err := service.Load(context.Background(), tenantID, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if status.RequestID == nil || status.State != "SUBMITTED" || status.OriginStatus != "NOT_REQUIRED" || status.DeliveryStatus != "NOT_STARTED" {
		t.Fatalf("unexpected scoped delegate status: %+v", status)
	}
	foreign, err := service.Load(context.Background(), foreignTenantID, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if foreign.RequestID != nil || foreign.InspectionID != nil {
		t.Fatalf("foreign tenant read onboarding request: %+v", foreign)
	}
}

func TestREO_IT_017AndIT_018ProviderFailureRetryStaysSingular(t *testing.T) {
	// IT-017/UT-023: dispatch creates and reuses invitation/delivery state; the test never seeds a delivery row.
	admin, runtime := openREOIntegrationDB(t)
	tenantID, inspectionID, assetID := identity.NewID(), identity.NewID(), identity.NewID()
	reoCleanupTenant(t, admin, tenantID)
	participantID, responsibilityID := identity.NewID(), identity.NewID()
	now := time.Now().UTC()
	for _, row := range []any{
		&database.Tenant{ID: tenantID, TenantID: tenantID, Name: "REO invitation test", Language: "pt-BR", DefaultTimezone: "America/Sao_Paulo", Status: "ACTIVE"},
		&database.Inspection{ID: inspectionID, TenantID: tenantID, BusinessUnitID: identity.NewID(), AssetID: assetID, ParticipantID: participantID, TemplateID: identity.NewID(), TemplateVersionID: identity.NewID(), Source: "MANUAL", SourceKey: "reo-it-017", Status: "PLANNED", MediaKind: "PHOTO", DueAt: now, DeadlineAt: now.Add(24 * time.Hour), ReminderInstants: json.RawMessage(`[]`), ContextSnapshot: json.RawMessage(`{}`), Version: 1, CreatedAt: now},
		&database.Asset{ID: assetID, TenantID: tenantID, BusinessUnitID: identity.NewID(), SegmentVersionID: identity.NewID(), Name: "Imóvel de teste", ExternalKey: "reo-it-017", Address: "Rua Um", PolicyOverrides: json.RawMessage(`{}`), Status: "ACTIVE", IdempotencyKey: "reo-it-017", CreatedAt: now, UpdatedAt: now},
		&database.Participant{ID: participantID, TenantID: tenantID, BusinessUnitID: identity.NewID(), Name: "Responsável", SegmentRole: "PROPERTY_OWNER", Status: "ACTIVE", Version: 1, IdempotencyKey: "reo-it-017", CreatedAt: now, UpdatedAt: now},
		&database.Responsibility{ID: responsibilityID, TenantID: tenantID, InspectionID: inspectionID, ParticipantID: participantID, Status: "PENDING", Version: 1, CreatedAt: now, UpdatedAt: now},
		&database.ParticipantContact{ID: identity.NewID(), TenantID: tenantID, ParticipantID: participantID, Channel: "EMAIL", Value: "delegate@example.test", Normalized: "delegate@example.test", Active: true, CreatedAt: now, UpdatedAt: now},
	} {
		if err := admin.Create(row).Error; err != nil {
			t.Fatalf("seed %T: %v", row, err)
		}
	}
	var contact database.ParticipantContact
	if err := admin.Where("tenant_id=? AND participant_id=?", tenantID, participantID).First(&contact).Error; err != nil {
		t.Fatal(err)
	}
	if err := admin.Create(&database.ChannelSelection{ID: identity.NewID(), TenantID: tenantID, ParticipantID: participantID, ContactID: contact.ID, CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	recorder := &reoNotificationRecorder{err: errors.New("mail provider secret"), db: admin}
	handler, err := dispatchinvitation.Setup(dispatchinvitation.Dependencies{Notifications: recorder, CaptureBaseURL: "https://capture.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]identity.ID{"inspectionId": inspectionID, "responsibilityId": responsibilityID, "participantId": participantID})
	if err != nil {
		t.Fatal(err)
	}
	envelope := events.RawEnvelope{ID: identity.NewID(), Type: "inspection.created.v1", SchemaVersion: 1, OccurredAt: now, TenantID: tenantID, AggregateID: inspectionID, CorrelationID: "reo-it-017", Payload: payload}
	firstErr := (tenanttx.Runner{DB: runtime}).Within(context.Background(), tenantID, func(tx *gorm.DB) error { return handler(context.Background(), tx, envelope) })
	if firstErr == nil {
		t.Fatal("provider failure unexpectedly succeeded")
	}
	if err := (tenanttx.Runner{DB: runtime}).Within(context.Background(), tenantID, func(tx *gorm.DB) error { return handler(context.Background(), tx, envelope) }); err != nil {
		t.Fatalf("provider retry failed: %v", err)
	}
	if err := (tenanttx.Runner{DB: runtime}).Within(context.Background(), tenantID, func(tx *gorm.DB) error { return handler(context.Background(), tx, envelope) }); err != nil {
		t.Fatalf("third duplicate dispatch failed: %v", err)
	}
	var invitations int64
	if err := admin.Model(&database.Invitation{}).Where("tenant_id=? AND responsibility_id=? AND status='ACTIVE'", tenantID, responsibilityID).Count(&invitations).Error; err != nil {
		t.Fatal(err)
	}
	if invitations != 1 || recorder.calls != 2 {
		t.Fatalf("duplicate event produced invitations=%d notification requests=%d", invitations, recorder.calls)
	}
	var responsibilities, emailAttempts int64
	if err := admin.Model(&database.Responsibility{}).Where("tenant_id=?", tenantID).Count(&responsibilities).Error; err != nil {
		t.Fatal(err)
	}
	if err := admin.Model(&database.ChannelAttempt{}).Where("tenant_id=? AND channel='EMAIL'", tenantID).Count(&emailAttempts).Error; err != nil {
		t.Fatal(err)
	}
	if responsibilities != 1 || emailAttempts != 2 {
		t.Fatalf("IT-017 tenant/channel counts: responsibilities=%d email deliveries=%d", responsibilities, emailAttempts)
	}
}

func TestREO_IT_018FailedDelegateDeliveryExposesCorrectionAndKeepsParticipant(t *testing.T) {
	admin, runtime := openREOIntegrationDB(t)
	tenantID, sessionID, inspectionID, assetID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	reoCleanupTenant(t, admin, tenantID)
	participantID, responsibilityID := identity.NewID(), identity.NewID()
	now := time.Now().UTC().Add(-time.Hour)
	for _, row := range []any{
		&database.Tenant{ID: tenantID, TenantID: tenantID, Name: "REO delivery test", Language: "pt-BR", DefaultTimezone: "America/Sao_Paulo", Status: "ACTIVE"},
		&database.Participant{ID: participantID, TenantID: tenantID, BusinessUnitID: identity.NewID(), Name: "Responsável delegado", SegmentRole: "PROPERTY_OWNER", Status: "ACTIVE", Version: 1, IdempotencyKey: "reo-it-018", CreatedAt: now, UpdatedAt: now},
		&database.Asset{ID: assetID, TenantID: tenantID, BusinessUnitID: identity.NewID(), SegmentVersionID: identity.NewID(), Name: "Imóvel de teste", ExternalKey: "reo-it-018", Address: "Rua Um", PolicyOverrides: json.RawMessage(`{}`), Status: "ACTIVE", IdempotencyKey: "reo-it-018", CreatedAt: now, UpdatedAt: now},
		&database.Inspection{ID: inspectionID, TenantID: tenantID, BusinessUnitID: identity.NewID(), AssetID: assetID, ParticipantID: participantID, TemplateID: identity.NewID(), TemplateVersionID: identity.NewID(), Source: "MANUAL", SourceKey: "reo-it-018", Status: "INVITED", MediaKind: "PHOTO", DueAt: now, DeadlineAt: now.Add(24 * time.Hour), ReminderInstants: json.RawMessage(`[]`), ContextSnapshot: json.RawMessage(`{}`), Version: 1, CreatedAt: now},
		&database.Responsibility{ID: responsibilityID, TenantID: tenantID, InspectionID: inspectionID, ParticipantID: participantID, Status: "PENDING", Version: 1, CreatedAt: now, UpdatedAt: now},
		&database.OnboardingRequest{ID: identity.NewID(), TenantID: tenantID, SessionID: sessionID, InspectionID: &inspectionID, ParticipantID: &participantID, Status: "SUBMITTED", IdempotencyKey: "reo-it-018", CreatedAt: now, UpdatedAt: now},
	} {
		if err := admin.Create(row).Error; err != nil {
			t.Fatalf("seed %T: %v", row, err)
		}
	}
	contact := database.ParticipantContact{ID: identity.NewID(), TenantID: tenantID, ParticipantID: participantID, Channel: "EMAIL", Value: "delegate@example.test", Normalized: "delegate@example.test", Active: true, CreatedAt: now, UpdatedAt: now}
	if err := admin.Create(&contact).Error; err != nil {
		t.Fatal(err)
	}
	if err := admin.Create(&database.ChannelSelection{ID: identity.NewID(), TenantID: tenantID, ParticipantID: participantID, ContactID: contact.ID, CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	payloadCipher, err := platformnotifications.NewPayloadCipher("reo-it-018", map[string]string{"reo-it-018": key})
	if err != nil {
		t.Fatal(err)
	}
	requestTime := now
	requestService, err := notificationrequest.Setup(notificationrequest.Dependencies{DB: runtime, Payloads: payloadCipher, Now: func() time.Time { return requestTime }})
	if err != nil {
		t.Fatal(err)
	}
	dispatch, err := dispatchinvitation.Setup(dispatchinvitation.Dependencies{Notifications: requestService, CaptureBaseURL: "https://capture.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]identity.ID{"inspectionId": inspectionID, "responsibilityId": responsibilityID, "participantId": participantID})
	envelope := events.RawEnvelope{ID: identity.NewID(), Type: "inspection.created.v1", SchemaVersion: 1, OccurredAt: now, TenantID: tenantID, AggregateID: inspectionID, CorrelationID: "reo-it-018", Payload: payload}
	service := deliverystatus.Service{DB: runtime}
	if err := (tenanttx.Runner{DB: runtime}).Within(context.Background(), tenantID, func(tx *gorm.DB) error { return dispatch(context.Background(), tx, envelope) }); err != nil {
		t.Fatalf("dispatch did not enqueue notification transactionally: %v", err)
	}
	var queuedRow database.Delivery
	if err := admin.Where("tenant_id=? AND inspection_id=? AND logical_template='capture-link'", tenantID, inspectionID).First(&queuedRow).Error; err != nil || queuedRow.Status != "QUEUED" {
		t.Fatalf("dispatch did not persist queued delivery: %+v err=%v", queuedRow, err)
	}
	// RunDue polls all tenants. Delay unrelated due attempts and restore their
	// scheduling timestamps after this fixture finishes.
	runAt := now.Add(time.Second)
	var unrelatedAttempts []database.ChannelAttempt
	if err := admin.Where("status=? AND next_attempt_at<=? AND delivery_id<>?", string(notificationcore.StateQueued), runAt, queuedRow.ID).Find(&unrelatedAttempts).Error; err != nil {
		t.Fatal(err)
	}
	for _, attempt := range unrelatedAttempts {
		if err := admin.Model(&database.ChannelAttempt{}).Where("tenant_id=? AND id=?", attempt.TenantID, attempt.ID).Update("next_attempt_at", runAt.Add(24*time.Hour)).Error; err != nil {
			t.Fatalf("delay unrelated channel attempt %s: %v", attempt.ID, err)
		}
	}
	t.Cleanup(func() {
		for _, attempt := range unrelatedAttempts {
			if err := admin.Model(&database.ChannelAttempt{}).Where("tenant_id=? AND id=? AND status=?", attempt.TenantID, attempt.ID, string(notificationcore.StateQueued)).Update("next_attempt_at", attempt.NextAttemptAt).Error; err != nil {
				t.Errorf("restore unrelated channel attempt %s: %v", attempt.ID, err)
			}
		}
	})
	gateway, err := platformnotifications.NewGateway(map[platformnotifications.Channel]map[platformnotifications.Provider]platformnotifications.Adapter{
		platformnotifications.Email: {platformnotifications.ProviderSMTP: reoFailingNotificationAdapter{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := executedelivery.Setup(executedelivery.Dependencies{DB: admin, Gateway: gateway, Payloads: payloadCipher, Now: func() time.Time { return now.Add(time.Second) }})
	if err != nil {
		t.Fatal(err)
	}
	if ran, err := worker.RunDue(context.Background()); err != nil || !ran {
		t.Fatalf("worker did not attempt queued notification: ran=%v err=%v", ran, err)
	}
	status, err := service.Load(context.Background(), tenantID, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if status.DeliveryStatus != "FAILED" || status.NextAction != "CORRECT_RESPONSIBLE_EMAIL" || !status.CanCorrect {
		t.Fatalf("failed delegate delivery lacks retry action: %+v", status)
	}
	requestTime = now.Add(2 * time.Second)
	retry := correctresponsibleemail.Service{DB: runtime, Notifications: requestService, CaptureBaseURL: "https://capture.example.test", Now: func() time.Time { return now.Add(2 * time.Second) }}
	corrected, err := retry.Correct(context.Background(), correctresponsibleemail.Input{TenantID: tenantID, InspectionID: inspectionID, ResponsibilityID: responsibilityID, Email: "delegate-retry@example.test", EmailConfirmation: "delegate-retry@example.test", IdempotencyKey: "reo-it-018-correct", Source: "onboarding", ExpectedResponsibilityVersion: 1})
	if err != nil {
		t.Fatalf("responsible email retry failed: %v", err)
	}
	status, err = service.Load(context.Background(), tenantID, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if status.DeliveryStatus != "QUEUED" || status.NextAction != "WAIT_FOR_DELIVERY" || !status.CanCorrect || corrected.DeliveryID == nil {
		t.Fatalf("retry status is not truthfully resendable: status=%+v correction=%+v", status, corrected)
	}
	var participants, emailAttempts int64
	if err := admin.Model(&database.Participant{}).Where("tenant_id=?", tenantID).Count(&participants).Error; err != nil {
		t.Fatal(err)
	}
	if err := admin.Model(&database.ChannelAttempt{}).Where("tenant_id=? AND channel='EMAIL'", tenantID).Count(&emailAttempts).Error; err != nil {
		t.Fatal(err)
	}
	if participants != 1 || emailAttempts != 2 {
		t.Fatalf("delivery status retry tenant/channel counts: participants=%d email deliveries=%d", participants, emailAttempts)
	}
	var failed, queued int64
	if err := admin.Model(&database.Delivery{}).Where("tenant_id=? AND inspection_id=? AND status='FAILED'", tenantID, inspectionID).Count(&failed).Error; err != nil {
		t.Fatal(err)
	}
	if err := admin.Model(&database.Delivery{}).Where("tenant_id=? AND inspection_id=? AND status='QUEUED'", tenantID, inspectionID).Count(&queued).Error; err != nil {
		t.Fatal(err)
	}
	if failed != 1 || queued != 1 {
		t.Fatalf("dispatch retry delivery states: failed=%d queued=%d", failed, queued)
	}
}

func TestREO_IT_019CompleteRejectsMissingParticipantCheckpoint(t *testing.T) {
	admin, runtime := openREOIntegrationDB(t)
	seed := reoSession(t, admin, runtime, onboardingsession.StepProperty, 2, "AGENCY", "PROPERTY")
	_, err := (Service{DB: runtime, Bus: mediator.New(), Sessions: seed.service, AdminOrigin: "https://admin.example.test", OwnerIssuer: "issuer", ActivationNotifier: &activationInvitationRecorder{}}).Complete(context.Background(), seed.locator, seed.csrf, "it-019")
	if code, field, _ := apperror.Public(err); code != apperror.InvalidState || field != "participant" {
		t.Fatalf("missing participant completion error=%v code=%s field=%q", err, code, field)
	}
	var requests, inspections int64
	if err := admin.Model(&database.OnboardingRequest{}).Where("session_id=?", seed.id).Count(&requests).Error; err != nil {
		t.Fatal(err)
	}
	if err := admin.Model(&database.Inspection{}).Where("source_key=?", "onboarding:"+seed.id.String()).Count(&inspections).Error; err != nil {
		t.Fatal(err)
	}
	if requests != 0 || inspections != 0 {
		t.Fatalf("incomplete completion wrote requests=%d inspections=%d", requests, inspections)
	}
}

func TestREO_UT026ServiceCompleteMapsInspectionFailureAndPreservesCheckpoints(t *testing.T) {
	admin, runtime := openREOIntegrationDB(t)
	seed := reoSession(t, admin, runtime, onboardingsession.StepReady, 4, "AGENCY", "PROPERTY", "PARTICIPANT")
	now := time.Now().UTC()
	unitID := identity.NewID()
	if err := admin.Create(&database.BusinessUnit{ID: unitID, TenantID: seed.tenant, Code: "HQ", Name: "Matriz", Status: "ACTIVE", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := admin.Create(&database.Membership{ID: identity.NewID(), TenantID: seed.tenant, IdentityID: identity.NewID(), Issuer: "issuer", Subject: "reo-owner-subject", Name: "REO Test", Email: "owner@example.test", Role: auth.TenantAdmin, Status: "ACTIVE", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	var beforeSession database.OnboardingSession
	if err := admin.First(&beforeSession, "id=?", seed.id).Error; err != nil {
		t.Fatal(err)
	}
	var beforeSteps []database.OnboardingStepRecord
	if err := admin.Where("session_id=?", seed.id).Order("step").Find(&beforeSteps).Error; err != nil {
		t.Fatal(err)
	}
	bus := mediator.New()
	setups := []func() error{
		func() error { return segmentresolve.Setup(segmentresolve.Dependencies{DB: runtime, Bus: bus}) },
		func() error { return templateresolve.Setup(templateresolve.Dependencies{DB: runtime, Bus: bus}) },
		func() error { return participantget.Setup(participantget.Dependencies{DB: runtime, Bus: bus}) },
		func() error { return assetget.Setup(assetget.Dependencies{DB: runtime, Bus: bus}) },
		func() error { return getprompt.Setup(getprompt.Dependencies{DB: runtime, Bus: bus}) },
	}
	for _, setup := range setups {
		if err := setup(); err != nil {
			t.Fatal(err)
		}
	}
	failCreate := true
	createError := error(apperror.New(apperror.InvalidState, "templateId", "active template required"))
	service := Service{DB: runtime, Bus: bus, Sessions: seed.service, AdminOrigin: "https://admin.example.test", OwnerIssuer: "issuer", ActivationNotifier: &activationInvitationRecorder{}, createInspection: inspectionCreateFunc(func(ctx context.Context, input inspectioncore.CreateInput) (inspectioncore.View, error) {
		if failCreate {
			return inspectioncore.View{}, createError
		}
		return (inspectioncore.Service{DB: runtime, Bus: bus, Now: func() time.Time { return now }}).Create(ctx, input)
	})}
	_, err := service.Complete(context.Background(), seed.locator, seed.csrf, "it-021")
	if code, field, message := apperror.Public(err); code != apperror.InvalidState || field != "templateId" || message != "Selecione um modelo ativo para a vistoria." {
		t.Fatalf("inspection business error was not mapped: code=%s field=%q message=%q err=%v", code, field, message, err)
	}
	createError = apperror.Wrap(apperror.Internal, errors.New("inspection creation failed"))
	_, err = service.Complete(context.Background(), seed.locator, seed.csrf, "it-021")
	if code, _, message := apperror.Public(err); code != apperror.Internal || message != "Não foi possível concluir a operação." {
		t.Fatalf("inspection internal error is not generic: code=%s message=%q err=%v", code, message, err)
	}
	var requests, inspections int64
	if err := admin.Model(&database.OnboardingRequest{}).Where("session_id=?", seed.id).Count(&requests).Error; err != nil {
		t.Fatal(err)
	}
	if err := admin.Model(&database.Inspection{}).Where("source_key=?", "onboarding:"+seed.id.String()).Count(&inspections).Error; err != nil {
		t.Fatal(err)
	}
	if requests != 0 || inspections != 0 {
		t.Fatalf("failed creation persisted requests=%d inspections=%d", requests, inspections)
	}
	var afterSession database.OnboardingSession
	if err := admin.First(&afterSession, "id=?", seed.id).Error; err != nil {
		t.Fatal(err)
	}
	var afterSteps []database.OnboardingStepRecord
	if err := admin.Where("session_id=?", seed.id).Order("step").Find(&afterSteps).Error; err != nil {
		t.Fatal(err)
	}
	if afterSession.State != onboardingsession.StepReady || afterSession.Version != beforeSession.Version || len(afterSteps) != len(beforeSteps) {
		t.Fatalf("failed completion changed session/checkpoint state: before=%+v/%+v after=%+v/%+v", beforeSession, beforeSteps, afterSession, afterSteps)
	}
	for index := range beforeSteps {
		if beforeSteps[index].Step != afterSteps[index].Step || beforeSteps[index].PayloadDigest != afterSteps[index].PayloadDigest || beforeSteps[index].Version != afterSteps[index].Version || string(beforeSteps[index].Payload) != string(afterSteps[index].Payload) {
			t.Fatalf("failed completion changed checkpoint %q: before=%+v after=%+v", beforeSteps[index].Step, beforeSteps[index], afterSteps[index])
		}
	}
	failCreate = false
	if _, err := service.Complete(context.Background(), seed.locator, seed.csrf, "it-021"); err != nil {
		var wrapped *apperror.Error
		if errors.As(err, &wrapped) && wrapped.Cause != nil {
			t.Fatalf("retry after restoring inspection dependency failed: %v (cause: %v)", err, wrapped.Cause)
		}
		t.Fatalf("retry after restoring inspection dependency failed: %v", err)
	}
	if _, err := service.Complete(context.Background(), seed.locator, seed.csrf, "it-021"); err != nil {
		t.Fatalf("idempotent retry failed: %v", err)
	}
	var createdInspection database.Inspection
	if err := admin.Where("tenant_id=? AND source_key=?", seed.tenant, "onboarding:"+seed.id.String()).First(&createdInspection).Error; err != nil {
		t.Fatal(err)
	}
	var responsibility database.Responsibility
	if err := admin.Where("tenant_id=? AND inspection_id=?", seed.tenant, createdInspection.ID).First(&responsibility).Error; err != nil {
		t.Fatal(err)
	}
	dispatch, err := dispatchinvitation.Setup(dispatchinvitation.Dependencies{Notifications: &reoNotificationRecorder{}, CaptureBaseURL: "https://capture.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	invitePayload, _ := json.Marshal(map[string]identity.ID{"inspectionId": createdInspection.ID, "responsibilityId": responsibility.ID, "participantId": responsibility.ParticipantID})
	inviteEvent := events.RawEnvelope{ID: identity.NewID(), Type: "inspection.created.v1", SchemaVersion: 1, OccurredAt: now, TenantID: seed.tenant, AggregateID: createdInspection.ID, CorrelationID: "reo-it-021", Payload: invitePayload}
	if err := (tenanttx.Runner{DB: runtime}).Within(context.Background(), seed.tenant, func(tx *gorm.DB) error { return dispatch(context.Background(), tx, inviteEvent) }); err != nil {
		t.Fatalf("dispatching the durable invitation intention failed: %v", err)
	}
	if err := admin.Model(&database.OnboardingRequest{}).Where("session_id=?", seed.id).Count(&requests).Error; err != nil {
		t.Fatal(err)
	}
	if err := admin.Model(&database.Inspection{}).Where("source_key=?", "onboarding:"+seed.id.String()).Count(&inspections).Error; err != nil {
		t.Fatal(err)
	}
	if requests != 1 || inspections != 1 {
		t.Fatalf("restored retry persisted requests=%d inspections=%d", requests, inspections)
	}
	var request database.OnboardingRequest
	if err := admin.Where("tenant_id=? AND session_id=?", seed.tenant, seed.id).First(&request).Error; err != nil || request.InspectionID == nil || request.ParticipantID == nil {
		t.Fatalf("onboarding request lacks created references: request=%+v err=%v", request, err)
	}
	var assets, participants, outbox, invitations int64
	for _, check := range []struct {
		model any
		where string
		args  []any
		count *int64
	}{{&database.Asset{}, "tenant_id=? AND external_key=?", []any{seed.tenant, "onboarding-" + seed.id.String()}, &assets}, {&database.Participant{}, "tenant_id=?", []any{seed.tenant}, &participants}, {&database.OutboxIntent{}, "tenant_id=? AND type='inspection.created.v1' AND payload::text LIKE ?", []any{seed.tenant, "%" + request.InspectionID.String() + "%"}, &outbox}, {&database.Invitation{}, "tenant_id=? AND responsibility_id IN (SELECT id FROM inspections.responsibilities WHERE tenant_id=? AND inspection_id=?)", []any{seed.tenant, seed.tenant, *request.InspectionID}, &invitations}} {
		if err := admin.Model(check.model).Where(check.where, check.args...).Count(check.count).Error; err != nil {
			t.Fatal(err)
		}
	}
	if assets != 1 || participants != 1 || outbox != 1 || invitations != 1 {
		t.Fatalf("retry duplicated materialized rows: assets=%d participants=%d outbox=%d invitations=%d", assets, participants, outbox, invitations)
	}
	submission, loadErr := seed.service.LoadSubmission(context.Background(), seed.locator, seed.csrf)
	if loadErr != nil {
		t.Fatalf("persisted draft could not be retried: %v", loadErr)
	}
	if len(submission.Steps) != 3 || submission.Session.CurrentStep != onboardingsession.StepReady {
		t.Fatalf("failed completion changed checkpoints: %+v", submission)
	}
}
