package core

import (
	"context"
	"fmt"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/security"
)

func seedDeliveries(t *testing.T, f *counterpartyFixture, invitation database.CounterpartyInvitation, statuses ...string) {
	t.Helper()
	for _, status := range statuses {
		row := database.Delivery{ID: identity.NewID(), TenantID: f.tenantID, IntentID: identity.NewID(), Status: status, CorrelationID: fmt.Sprintf("counterparty-%s-g%d", invitation.ID, invitation.DeliveryGeneration)}
		if err := f.db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func TestUnit_UT304_ResendAfterFailedDeliveryRotatesLinkKeepingInvitationAndDeadline(t *testing.T) {
	f := newCounterpartyFixture(t, 1)
	participant := f.participants[0]
	f.invite(t, participant)
	before := f.invitation(t, participant)
	oldToken := f.tokenFor(t, participant)
	firstDelivery := f.notifications.requests[0]
	if err := f.service.RequestOTP(context.Background(), oldToken); err != nil {
		t.Fatal(err)
	}
	session, err := f.service.VerifyOTP(context.Background(), oldToken, f.otp.code)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.service.RequestOTP(context.Background(), oldToken); err != nil {
		t.Fatal(err)
	}
	if pending := f.invitation(t, participant); pending.OTPHash == nil {
		t.Fatal("precondition: an OTP should be outstanding before resend")
	}
	seedDeliveries(t, f, before, "FAILED", "UNKNOWN")
	*f.clock = f.clock.Add(time.Hour)

	resent, err := f.service.Resend(context.Background(), f.tenantID, before.ID)
	if err != nil {
		t.Fatalf("UT-304 resend: %v", err)
	}
	if resent.ID != before.ID || !resent.DeadlineAt.Equal(before.DeadlineAt) || resent.DeliveryGeneration != before.DeliveryGeneration+1 || resent.Response != "PENDING" {
		t.Fatalf("UT-304 resent invitation=%+v, want same id/deadline and generation %d", resent, before.DeliveryGeneration+1)
	}
	if string(resent.TokenHash) == string(before.TokenHash) {
		t.Fatal("UT-304 resend kept the previous token hash")
	}
	if len(f.notifications.requests) != 2 {
		t.Fatalf("UT-304 delivery intents=%d, want a new one", len(f.notifications.requests))
	}
	second := f.notifications.requests[1]
	if hash := security.HashToken(second.Execution.Token); string(hash[:]) != string(resent.TokenHash) {
		t.Fatal("UT-304 new delivery does not carry the rotated token")
	}
	if second.Template != firstDelivery.Template || second.Execution.InvitationID != before.ID || second.Execution.ExpiresAt != before.DeadlineAt.Unix() {
		t.Fatalf("UT-304 new delivery=%+v", second)
	}
	if second.CorrelationID != fmt.Sprintf("counterparty-%s-g2", before.ID) || second.IdempotencyKey == firstDelivery.IdempotencyKey {
		t.Fatalf("UT-304 new delivery correlation=%q idempotency=%q, want generation 2 with a distinct key from %q", second.CorrelationID, second.IdempotencyKey, firstDelivery.IdempotencyKey)
	}
	stored := f.invitation(t, participant)
	if stored.OTPHash != nil || stored.OTPExpiresAt != nil || stored.OTPAttempts != 0 || stored.DeliveryGeneration != 2 {
		t.Fatalf("UT-304 OTP state was not reset: %+v", stored)
	}
	var sessions []database.ExternalSession
	if err := f.db.Where("tenant_id=? AND kind='COUNTERPARTY' AND counterparty_invitation_id=?", f.tenantID, before.ID).Find(&sessions).Error; err != nil || len(sessions) != 1 || sessions[0].RevokedAt == nil {
		t.Fatalf("UT-304 previous sessions=%+v err=%v, want one revoked", sessions, err)
	}
	if _, err := f.service.ValidateSession(context.Background(), session.Token, session.CSRF); !apperror.Is(err, apperror.Unauthenticated) {
		t.Fatalf("UT-304 previous session still valid: %v", err)
	}
	if err := f.service.RequestOTP(context.Background(), oldToken); !apperror.Is(err, apperror.NotFound) {
		t.Fatalf("UT-304 previous link still works: %v", err)
	}
	if err := f.service.RequestOTP(context.Background(), second.Execution.Token); err != nil {
		t.Fatalf("UT-304 rotated link rejected: %v", err)
	}
}

func TestUnit_UT304_ResendRequiresEveryCurrentDeliveryToHaveFailed(t *testing.T) {
	for name, statuses := range map[string][]string{
		"UT-304 prior delivery succeeded":            {"DELIVERED"},
		"UT-304 one of two prior deliveries sent":    {"FAILED", "SENT"},
		"UT-304 prior delivery still queued":         {"QUEUED"},
		"UT-304 no delivery recorded for generation": nil,
	} {
		t.Run(name, func(t *testing.T) {
			f := newCounterpartyFixture(t, 1)
			f.invite(t, f.participants[0])
			before := f.invitation(t, f.participants[0])
			seedDeliveries(t, f, before, statuses...)
			_, err := f.service.Resend(context.Background(), f.tenantID, before.ID)
			assertAppError(t, err, apperror.InvalidState, "counterpartyInvitationId")
			if after := f.invitation(t, f.participants[0]); string(after.TokenHash) != string(before.TokenHash) || after.DeliveryGeneration != 1 || len(f.notifications.requests) != 1 {
				t.Fatalf("rejected resend changed the invitation: %+v deliveries=%d", after, len(f.notifications.requests))
			}
		})
	}
}

func TestUnit_UT304_ResendOnlyCountsTheCurrentGeneration(t *testing.T) {
	f := newCounterpartyFixture(t, 1)
	f.invite(t, f.participants[0])
	first := f.invitation(t, f.participants[0])
	seedDeliveries(t, f, first, "FAILED")
	if _, err := f.service.Resend(context.Background(), f.tenantID, first.ID); err != nil {
		t.Fatal(err)
	}
	second := f.invitation(t, f.participants[0])
	if _, err := f.service.Resend(context.Background(), f.tenantID, first.ID); !apperror.Is(err, apperror.InvalidState) {
		t.Fatalf("resend without generation-2 failures error=%v, want INVALID_STATE", err)
	}
	seedDeliveries(t, f, second, "FAILED")
	if third, err := f.service.Resend(context.Background(), f.tenantID, first.ID); err != nil || third.DeliveryGeneration != 3 {
		t.Fatalf("resend after generation-2 failure=%+v err=%v, want generation 3", third, err)
	}
}

func TestUnit_UT307_ResendRejectsRespondedInvitation(t *testing.T) {
	for _, response := range []string{"ACKNOWLEDGED", "CONTESTED"} {
		t.Run("UT-307 "+response, func(t *testing.T) {
			f := newCounterpartyFixture(t, 1)
			f.invite(t, f.participants[0])
			before := f.invitation(t, f.participants[0])
			seedDeliveries(t, f, before, "FAILED")
			owner := SessionOwner{TenantID: f.tenantID, InvitationID: before.ID}
			if _, err := f.service.Respond(context.Background(), owner, response, "Discordo do estado da cozinha.", "respond"); err != nil {
				t.Fatal(err)
			}
			_, err := f.service.Resend(context.Background(), f.tenantID, before.ID)
			assertAppError(t, err, apperror.InvalidState, "counterpartyInvitationId")
			if len(f.notifications.requests) != 1 {
				t.Fatalf("UT-307 rejected resend created a delivery intent")
			}
		})
	}
}

func TestUnit_ResendUnknownInvitationIsNotFound(t *testing.T) {
	f := newCounterpartyFixture(t, 1)
	_, err := f.service.Resend(context.Background(), f.tenantID, identity.NewID())
	assertAppError(t, err, apperror.NotFound, "counterpartyInvitationId")
}
