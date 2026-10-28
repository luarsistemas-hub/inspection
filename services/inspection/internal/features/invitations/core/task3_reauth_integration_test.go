//go:build integration

package core

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"time"

	"inspection/libs/identity"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/security"

	"gorm.io/gorm"
)

func TestIT042ExpiredExternalSessionOTPKeepsEnvironmentAndAnswersAndDeclareReplay(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	fixture, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{SchemaVersion: 2})
	if err != nil {
		t.Fatal(err)
	}
	pepper := []byte("task3-integration-pepper-32-bytes!!")
	linkToken, err := security.NewScopedToken(fixture.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	oldToken, err := security.NewScopedToken(fixture.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	oldCSRF := "expired-session-csrf"
	now := time.Now().UTC().Truncate(time.Microsecond)
	invitationID := identity.NewID()
	linkHash := security.HashToken(linkToken)
	oldHash := security.HashToken(oldToken)
	intents := json.RawMessage(`[ {"channel":"EMAIL","destination":"task06@example.test","verified":true} ]`)
	invitation := database.Invitation{ID: invitationID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, TokenHash: linkHash[:], DeliveryIntents: intents, Status: "ACTIVE", ExpiresAt: now.Add(time.Hour), CreatedAt: now}
	oldSession := database.ExternalSession{ID: identity.NewID(), TenantID: fixture.TenantID, InvitationID: &invitationID, ResponsibilityID: &fixture.ResponsibilityID, Kind: "CAPTURE", SessionDigest: oldHash[:], CSRFDigest: make([]byte, 32), ExpiresAt: now.Add(-time.Minute), CreatedAt: now.Add(-time.Hour)}
	answerBefore := database.RequirementAnswer{}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		if err := tx.Create(&invitation).Error; err != nil {
			return err
		}
		if err := tx.Create(&oldSession).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.CaptureDraft{}).Where("tenant_id=? AND id=?", fixture.TenantID, fixture.DraftID).Updates(map[string]any{"status": "OPEN", "media_kind": "PHOTO"}).Error; err != nil {
			return err
		}
		requirements, _ := json.Marshal([]capturecore.Requirement{{Key: "room-1", EvidenceKind: "PHOTO", Required: true, Expected: true, MinimumMedia: 1, MaximumMedia: 20}})
		if err := tx.Model(&database.CaptureDraft{}).Where("tenant_id=? AND id=?", fixture.TenantID, fixture.DraftID).Update("requirements", requirements).Error; err != nil {
			return err
		}
		return tx.Where("tenant_id=? AND draft_id=?", fixture.TenantID, fixture.DraftID).Take(&answerBefore).Error
	}); err != nil {
		t.Fatal(err)
	}
	notifier := &notifierStub{}
	service := Service{DB: h.DB, Pepper: pepper, Limits: &limitsStub{allowed: true}, Notifier: notifier, Stage: "test", Within: h.WithinTenant}
	if _, err := service.ValidateSession(ctx, oldToken, oldCSRF); err == nil {
		t.Fatal("expired external session remained valid")
	}
	if err := service.RequestOTP(ctx, linkToken); err != nil {
		t.Fatal(err)
	}
	newSession, err := service.VerifyOTP(ctx, linkToken, notifier.code)
	if err != nil {
		t.Fatal(err)
	}
	if responsibilityID, err := service.ValidateSession(ctx, newSession.Token, newSession.CSRF); err != nil || responsibilityID != fixture.ResponsibilityID {
		t.Fatalf("OTP session validation responsibility=%s err=%v", responsibilityID, err)
	}
	capture := capturecore.Service{DB: h.DB, Within: h.WithinTenant}
	input := capturecore.DeclareEnvironmentInput{TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, TypeCode: "BEDROOM", Name: "Quarto novo", ClientMutationID: "it042-declare-room"}
	first, err := capture.DeclareEnvironment(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := capture.DeclareEnvironment(ctx, input)
	if err != nil || second.Key != first.Key || second.EnvironmentIdentityID != first.EnvironmentIdentityID {
		t.Fatalf("same declaration mutation returned first=%+v second=%+v err=%v", first, second, err)
	}
	var answerAfter database.RequirementAnswer
	var draft database.CaptureDraft
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND draft_id=?", fixture.TenantID, fixture.DraftID).Take(&answerAfter).Error; err != nil {
			return err
		}
		return tx.Where("tenant_id=? AND id=?", fixture.TenantID, fixture.DraftID).Take(&draft).Error
	}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(answerBefore.MediaIDs, answerAfter.MediaIDs) || !reflect.DeepEqual(answerBefore.Flags, answerAfter.Flags) {
		t.Fatalf("OTP and declaration changed existing answer: before=%+v after=%+v", answerBefore, answerAfter)
	}
	var requirements []capturecore.Requirement
	if err := json.Unmarshal(draft.Requirements, &requirements); err != nil {
		t.Fatal(err)
	}
	if len(requirements) != 2 || requirements[1].Key != first.Key {
		t.Fatalf("post-OTP environments=%+v; want existing requirement plus stable env:new key %s", requirements, first.Key)
	}
}
