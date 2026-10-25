//go:build integration

package core

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"inspection/libs/identity"
	inventorycore "inspection/services/inspection/internal/features/inventory/core"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/security"

	"gorm.io/gorm"
)

var errFailingNotification = errors.New("notification outbox unavailable")

// counterpartyIT owns one tenant seeded through the repository harness with an
// ACTIVE inventory baseline. Every service call uses the runtime role and the
// production tenant transaction boundary.
type counterpartyIT struct {
	h             *harness.Harness
	fx            harness.Task06Fixture
	ctx           context.Context
	service       Service
	inventory     inventorycore.Service
	notifications *invitationNotificationFake
	otp           *invitationOTPNotifierFake
	now           time.Time
	versionID     identity.ID
	originID      identity.ID
	items         map[string]database.InventoryItem
	environments  map[string]database.InventoryEnvironment
	participants  []identity.ID
}

func newCounterpartyIT(t *testing.T, extraParticipants int) *counterpartyIT {
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
	fx, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	it := &counterpartyIT{h: h, fx: fx, ctx: ctx, notifications: &invitationNotificationFake{}, otp: &invitationOTPNotifierFake{}, now: time.Now().UTC().Truncate(time.Microsecond), versionID: identity.NewID(), originID: identity.NewID(), items: map[string]database.InventoryItem{}, environments: map[string]database.InventoryEnvironment{}, participants: []identity.ID{fx.ParticipantID}}
	it.inventory = inventorycore.Service{DB: h.DB, Within: h.WithinTenant, Now: func() time.Time { return it.now }}
	it.service = Service{DB: h.DB, Within: h.WithinTenant, Clock: func() time.Time { return it.now }, Pepper: []byte(strings.Repeat("p", 32)), Limits: allowOTPLimits{}, Notifier: it.otp, Notifications: it.notifications, CaptureBaseURL: "https://capture.example", Inventory: it.inventory}
	it.seedBaseline(t)
	for i := 0; i < extraParticipants; i++ {
		it.participants = append(it.participants, it.seedParticipant(t, i))
	}
	return it
}

func (it *counterpartyIT) seedBaseline(t *testing.T) {
	t.Helper()
	tenantID := it.fx.TenantID
	now := it.now
	err := it.h.WithinTenant(it.ctx, tenantID, func(tx *gorm.DB) error {
		if err := tx.Create(&database.Origin{ID: it.originID, TenantID: tenantID, AssetID: it.fx.AssetID, ActiveVersionID: &it.versionID, Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		purpose, ready := "MOVE_IN", "READY"
		if err := tx.Create(&database.OriginVersion{ID: it.versionID, TenantID: tenantID, OriginID: it.originID, VersionNumber: 1, Purpose: &purpose, Source: "CAPTURE", HasInventory: true, Status: "ACTIVE", DraftState: &ready, IdempotencyKey: "counterparty-it-" + it.versionID.String(), CreatedAt: now, ActivatedAt: &now}).Error; err != nil {
			return err
		}
		for i, env := range []struct{ key, decision, typeCode, name string }{
			{"kitchen", "ACCEPTED", "KITCHEN", "Cozinha"},
			{"rejected", "REJECTED", "BATHROOM", "Banheiro sugerido"},
			{"living", "EDITED", "LIVING_ROOM", "Sala"},
		} {
			row := database.InventoryEnvironment{ID: identity.NewID(), TenantID: tenantID, OriginVersionID: it.versionID, IdentityID: identity.NewID(), TypeCode: env.typeCode, Name: env.name, Expected: true, Decision: env.decision, Position: i, Version: 1, CreatedAt: now, UpdatedAt: now}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
			it.environments[env.key] = row
		}
		confidence, quantity, reason := 9100, 1, "NOT_RELEVANT"
		for _, item := range []struct{ key, env, decision, origin string }{
			{"accepted", "kitchen", "ACCEPTED", "AI"},
			{"edited", "kitchen", "EDITED", "AI"},
			{"added", "living", "ADDED", "MANUAL"},
			{"pending", "kitchen", "PENDING", "AI"},
			{"rejected", "living", "REJECTED", "AI"},
			{"inRejectedEnvironment", "rejected", "ACCEPTED", "AI"},
		} {
			row := database.InventoryItem{ID: identity.NewID(), TenantID: tenantID, OriginVersionID: it.versionID, EnvironmentID: it.environments[item.env].ID, IdentityID: identity.NewID(), TypeCode: "OTHER", Name: "Item " + item.key, Quantity: &quantity, Condition: "GOOD", Observation: "OBSERVED", ConfidenceBps: &confidence, Origin: item.origin, Decision: item.decision, Version: 1, CreatedAt: now, UpdatedAt: now}
			if item.decision == "REJECTED" {
				row.RejectionReason = &reason
			}
			if item.decision == "EDITED" {
				row.Suggested = json.RawMessage(`{"name":"Sugestão da IA"}`)
			}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
			it.items[item.key] = row
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func (it *counterpartyIT) seedParticipant(t *testing.T, index int) identity.ID {
	t.Helper()
	participantID, contactID := identity.NewID(), identity.NewID()
	now := it.now
	address := participantID.String() + "@example.test"
	err := it.h.WithinTenant(it.ctx, it.fx.TenantID, func(tx *gorm.DB) error {
		if err := tx.Create(&database.Participant{ID: participantID, TenantID: it.fx.TenantID, BusinessUnitID: it.fx.BusinessUnitID, Name: "Contraparte", SegmentRole: "TENANT", Status: "ACTIVE", Version: 1, IdempotencyKey: "counterparty-it-" + participantID.String(), CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.ParticipantContact{ID: contactID, TenantID: it.fx.TenantID, ParticipantID: participantID, Channel: "EMAIL", Value: address, Normalized: address, Active: true, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.ContactVerification{ID: identity.NewID(), TenantID: it.fx.TenantID, ContactID: contactID, IdempotencyKey: "counterparty-it-" + contactID.String(), Status: "VERIFIED", VerifiedAt: &now, CreatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.ChannelSelection{ID: identity.NewID(), TenantID: it.fx.TenantID, ParticipantID: participantID, ContactID: contactID, CreatedAt: now}).Error
	})
	if err != nil {
		t.Fatalf("seed participant %d: %v", index, err)
	}
	return participantID
}

func (it *counterpartyIT) invite(t *testing.T, versionID identity.ID, participants ...identity.ID) {
	t.Helper()
	if err := it.h.WithinTenant(it.ctx, it.fx.TenantID, func(tx *gorm.DB) error {
		return it.service.Invite(tx, it.fx.TenantID, versionID, participants, it.fx.MembershipID)
	}); err != nil {
		t.Fatal(err)
	}
}

func (it *counterpartyIT) invitation(t *testing.T, versionID, participantID identity.ID) database.CounterpartyInvitation {
	t.Helper()
	var row database.CounterpartyInvitation
	if err := it.h.WithinTenant(it.ctx, it.fx.TenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND origin_version_id=? AND participant_id=?", it.fx.TenantID, versionID, participantID).First(&row).Error
	}); err != nil {
		t.Fatal(err)
	}
	return row
}

func (it *counterpartyIT) version(t *testing.T, id identity.ID) database.OriginVersion {
	t.Helper()
	var row database.OriginVersion
	if err := it.h.WithinTenant(it.ctx, it.fx.TenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND id=?", it.fx.TenantID, id).First(&row).Error
	}); err != nil {
		t.Fatal(err)
	}
	return row
}

func (it *counterpartyIT) contest(t *testing.T, participantID identity.ID) SessionOwner {
	t.Helper()
	owner := SessionOwner{TenantID: it.fx.TenantID, InvitationID: it.invitation(t, it.versionID, participantID).ID}
	if _, err := it.service.Respond(it.ctx, owner, "CONTESTED", "O armário da cozinha está quebrado.", "contest-"+participantID.String()); err != nil {
		t.Fatal(err)
	}
	return owner
}

func (it *counterpartyIT) count(t *testing.T, model any, query string, args ...any) int64 {
	t.Helper()
	count, err := it.h.CountTenant(it.ctx, it.fx.TenantID, model, query, args...)
	if err != nil {
		t.Fatal(err)
	}
	return count
}

func TestIT_UT280_281_283_302_InviteAndPolicyOnPostgres(t *testing.T) {
	it := newCounterpartyIT(t, 2)
	a, b, c := it.participants[0], it.participants[1], it.participants[2]
	it.invite(t, it.versionID, a, b, a)
	if got := it.count(t, &database.CounterpartyInvitation{}, "origin_version_id=?", it.versionID); got != 2 || len(it.notifications.requests) != 2 {
		t.Fatalf("UT-280/281 invitations=%d deliveries=%d, want 2/2", got, len(it.notifications.requests))
	}
	first := it.invitation(t, it.versionID, a)
	if first.Response != "PENDING" || first.Resolution != "OPEN" || first.DeliveryGeneration != 1 || !first.DeadlineAt.Equal(it.now.AddDate(0, 0, DefaultDeadlineDays)) {
		t.Fatalf("UT-280 stored invitation=%+v", first)
	}
	for _, request := range it.notifications.requests {
		if request.Template.String() != "baseline-acknowledgment:v1" || !strings.HasSuffix(request.CorrelationID, "-g1") {
			t.Fatalf("UT-280 delivery=%+v", request)
		}
	}
	policy, err := it.service.UpdatePolicy(it.ctx, it.fx.TenantID, 10)
	if err != nil || policy.ContestationDeadlineDays != 10 || policy.Version != 2 {
		t.Fatalf("UT-283 UpdatePolicy 5->10 on PostgreSQL=%+v err=%v, want version 2", policy, err)
	}
	invitedAt := it.now
	it.now = it.now.Add(time.Hour)
	it.invite(t, it.versionID, a, c)
	if again := it.invitation(t, it.versionID, a); !again.DeadlineAt.Equal(invitedAt.AddDate(0, 0, DefaultDeadlineDays)) {
		t.Fatalf("UT-283 existing deadline changed to %s", again.DeadlineAt)
	}
	if created := it.invitation(t, it.versionID, c); !created.DeadlineAt.Equal(it.now.AddDate(0, 0, 10)) {
		t.Fatalf("UT-283 new invitation deadline=%s, want %s", created.DeadlineAt, it.now.AddDate(0, 0, 10))
	}
	for _, days := range []int{0, 91} {
		_, err := it.service.UpdatePolicy(it.ctx, it.fx.TenantID, days)
		assertAppError(t, err, apperror.InvalidInput, "contestationDeadlineDays")
	}
	for i, days := range []int{1, 90} {
		policy, err := it.service.UpdatePolicy(it.ctx, it.fx.TenantID, days)
		if err != nil || policy.ContestationDeadlineDays != days || policy.Version != int64(3+i) {
			t.Fatalf("UT-302 UpdatePolicy(%d)=%+v err=%v, want version %d", days, policy, err, 3+i)
		}
	}
}

func TestIT_UT284_304_OTPSessionAndResendOnPostgres(t *testing.T) {
	it := newCounterpartyIT(t, 0)
	a := it.participants[0]
	it.invite(t, it.versionID, a)
	before := it.invitation(t, it.versionID, a)
	token := it.notifications.requests[0].Execution.Token
	if err := it.service.RequestOTP(it.ctx, token); err != nil {
		t.Fatal(err)
	}
	session, err := it.service.VerifyOTP(it.ctx, token, it.otp.code)
	if err != nil {
		t.Fatalf("UT-284 verify on PostgreSQL: %v", err)
	}
	owner, err := it.service.ValidateSession(it.ctx, session.Token, session.CSRF)
	if err != nil || owner.InvitationID != before.ID {
		t.Fatalf("UT-284 session owner=%+v err=%v", owner, err)
	}
	if got := it.count(t, &database.ExternalSession{}, "kind='COUNTERPARTY' AND counterparty_invitation_id=? AND responsibility_id IS NULL", before.ID); got != 1 {
		t.Fatalf("UT-284 counterparty sessions=%d, want 1", got)
	}
	if err := it.h.WithinTenant(it.ctx, it.fx.TenantID, func(tx *gorm.DB) error {
		for _, status := range []string{"FAILED", "UNKNOWN"} {
			if err := tx.Create(&database.Delivery{ID: identity.NewID(), TenantID: it.fx.TenantID, IntentID: identity.NewID(), Status: status, LogicalTemplate: "baseline-acknowledgment", TemplateVersion: "v1", CorrelationID: it.notifications.requests[0].CorrelationID, IdempotencyKey: identity.NewID().String(), RequestDigest: strings.Repeat("a", 64), RecipientID: a.String(), CreatedAt: it.now, UpdatedAt: it.now}).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	resent, err := it.service.Resend(it.ctx, it.fx.TenantID, before.ID)
	if err != nil || resent.ID != before.ID || !resent.DeadlineAt.Equal(before.DeadlineAt) || resent.DeliveryGeneration != 2 || string(resent.TokenHash) == string(before.TokenHash) {
		t.Fatalf("UT-304 resend on PostgreSQL=%+v err=%v", resent, err)
	}
	if len(it.notifications.requests) != 2 || !strings.HasSuffix(it.notifications.requests[1].CorrelationID, "-g2") || it.notifications.requests[1].IdempotencyKey == it.notifications.requests[0].IdempotencyKey {
		t.Fatalf("UT-304 resend delivery intents=%+v", it.notifications.requests)
	}
	if _, err := it.service.ValidateSession(it.ctx, session.Token, session.CSRF); !apperror.Is(err, apperror.Unauthenticated) {
		t.Fatalf("UT-304 previous session still valid: %v", err)
	}
	if hash := security.HashToken(it.notifications.requests[1].Execution.Token); string(hash[:]) != string(it.invitation(t, it.versionID, a).TokenHash) {
		t.Fatal("UT-304 stored hash does not match the resent link")
	}
}

func TestIT_UT296_CounterpartyProjectionEqualsCustomerProjection(t *testing.T) {
	it := newCounterpartyIT(t, 0)
	it.invite(t, it.versionID, it.participants[0])
	invitation := it.invitation(t, it.versionID, it.participants[0])
	owner := SessionOwner{TenantID: it.fx.TenantID, InvitationID: invitation.ID}
	view, err := it.service.View(it.ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	customer, err := it.inventory.BaselineInventory(it.ctx, it.fx.TenantID, it.fx.AssetID, &it.versionID, true)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(view.Inventory, customer) {
		t.Fatalf("UT-296 counterparty projection differs from customer projection:\ncounterparty=%+v\ncustomer=%+v", view.Inventory, customer)
	}
	var environments []string
	var items []identity.ID
	for _, env := range view.Inventory.Environments {
		environments = append(environments, env.Environment.Name)
		for _, item := range env.Items {
			items = append(items, item.Item.ID)
			if item.Item.Suggested != nil || item.Item.ConfidenceBps != nil {
				t.Fatalf("UT-296 item exposes review data: %+v", item.Item)
			}
		}
	}
	want := []identity.ID{it.items["accepted"].ID, it.items["edited"].ID, it.items["added"].ID}
	sort.Slice(items, func(i, j int) bool { return items[i].String() < items[j].String() })
	sort.Slice(want, func(i, j int) bool { return want[i].String() < want[j].String() })
	if !reflect.DeepEqual(environments, []string{"Cozinha", "Sala"}) || !reflect.DeepEqual(items, want) {
		t.Fatalf("UT-296 projection environments=%v items=%v, want [Cozinha Sala] and only included items %v", environments, items, want)
	}
	if view.Invitation.ID != invitation.ID || !view.Invitation.DeadlineAt.Equal(invitation.DeadlineAt) || view.Invitation.Response != "PENDING" || view.Invitation.RespondedAt != nil || view.ReadOnly || view.Newer {
		t.Fatalf("UT-296 deadline/response fields=%+v readOnly=%v newer=%v", view.Invitation, view.ReadOnly, view.Newer)
	}
	if _, err := it.service.Respond(it.ctx, owner, "ACKNOWLEDGED", "", "ack"); err != nil {
		t.Fatal(err)
	}
	if view, err = it.service.View(it.ctx, owner); err != nil || view.Invitation.Response != "ACKNOWLEDGED" || view.Invitation.RespondedAt == nil || !view.ReadOnly {
		t.Fatalf("UT-296 response not reflected: %+v err=%v", view.Invitation, err)
	}
}

func TestIT_UT298_300_305_ResolveAcceptCreatesContestationDraft(t *testing.T) {
	it := newCounterpartyIT(t, 2)
	a, b, c := it.participants[0], it.participants[1], it.participants[2]
	it.invite(t, it.versionID, a, b, c)
	ownerA, ownerB, ownerC := it.contest(t, a), it.contest(t, b), it.contest(t, c)

	resolved, err := it.service.Resolve(it.ctx, it.fx.TenantID, ownerA.InvitationID, it.fx.MembershipID, "ACCEPT", "Armário será revisto.")
	if err != nil || resolved.Resolution != "ACCEPTED" || resolved.AdjustmentVersionID == nil {
		t.Fatalf("UT-298 ACCEPT=%+v err=%v", resolved, err)
	}
	draft := it.version(t, *resolved.AdjustmentVersionID)
	if draft.Source != "CONTESTATION" || draft.SourceVersionID == nil || *draft.SourceVersionID != it.versionID || draft.Status != "DRAFT" || draft.DraftState == nil || *draft.DraftState != "READY" || !draft.HasInventory || draft.OriginID != it.originID {
		t.Fatalf("UT-298 adjustment draft=%+v", draft)
	}
	if stored := it.invitation(t, it.versionID, a); stored.AdjustmentVersionID == nil || *stored.AdjustmentVersionID != draft.ID {
		t.Fatalf("UT-298 adjustment_version_id=%v, want %s", stored.AdjustmentVersionID, draft.ID)
	}
	if active := it.version(t, it.versionID); active.Status != "ACTIVE" {
		t.Fatalf("UT-298 contested version status=%s, want ACTIVE", active.Status)
	}
	var copied []database.InventoryItem
	if err := it.h.WithinTenant(it.ctx, it.fx.TenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND origin_version_id=?", it.fx.TenantID, draft.ID).Find(&copied).Error
	}); err != nil {
		t.Fatal(err)
	}
	copiedIdentities := map[identity.ID]bool{}
	for _, item := range copied {
		copiedIdentities[item.IdentityID] = true
	}
	if len(copied) != 3 || !copiedIdentities[it.items["accepted"].IdentityID] || !copiedIdentities[it.items["edited"].IdentityID] || !copiedIdentities[it.items["added"].IdentityID] {
		t.Fatalf("UT-298 copied items=%d identities=%v, want the 3 included rows", len(copied), copiedIdentities)
	}
	if got := it.count(t, &database.InventoryEnvironment{}, "origin_version_id=?", draft.ID); got != 2 {
		t.Fatalf("UT-298 copied environments=%d, want 2", got)
	}

	view, err := it.service.View(it.ctx, ownerA)
	if err != nil || view.Invitation.Resolution != "ACCEPTED" || view.Invitation.ResolutionReason == nil || *view.Invitation.ResolutionReason != "Armário será revisto." || view.AdjustmentStatus != "DRAFT" {
		t.Fatalf("UT-305 accepted view=%+v adjustment=%q err=%v", view.Invitation, view.AdjustmentStatus, err)
	}

	_, err = it.service.Resolve(it.ctx, it.fx.TenantID, ownerB.InvitationID, it.fx.MembershipID, "ACCEPT", "")
	if !apperror.Is(err, apperror.Conflict) {
		t.Fatalf("UT-298 ACCEPT with an open draft error=%v, want Conflict", err)
	}
	if code, field, safe := apperror.Public(err); code != apperror.Conflict || field != "counterpartyInvitationId" || safe != "an inventory draft already exists for this asset" {
		t.Fatalf("UT-298 collision code=%s field=%q message=%q", code, field, safe)
	}
	if stored := it.invitation(t, it.versionID, b); stored.Resolution != "OPEN" || stored.AdjustmentVersionID != nil || stored.ResolvedAt != nil {
		t.Fatalf("UT-298 failed ACCEPT persisted a resolution: %+v", stored)
	}

	if _, err := it.inventory.Discard(it.ctx, it.fx.TenantID, draft.ID, it.fx.MembershipID); err != nil {
		t.Fatal(err)
	}
	view, err = it.service.View(it.ctx, ownerA)
	if err != nil || view.Invitation.Resolution != "ACCEPTED" || view.AdjustmentStatus != "DISCARDED" || view.Invitation.AdjustmentVersionID == nil || *view.Invitation.AdjustmentVersionID != draft.ID {
		t.Fatalf("UT-300 view after discarded adjustment=%+v adjustment=%q err=%v", view.Invitation, view.AdjustmentStatus, err)
	}

	resolvedB, err := it.service.Resolve(it.ctx, it.fx.TenantID, ownerB.InvitationID, it.fx.MembershipID, "ACCEPT", "")
	if err != nil || resolvedB.AdjustmentVersionID == nil {
		t.Fatalf("ACCEPT after discard=%+v err=%v", resolvedB, err)
	}
	origin := database.Origin{}
	if err := it.h.WithinTenant(it.ctx, it.fx.TenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND id=?", it.fx.TenantID, it.originID).First(&origin).Error
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := it.inventory.Activate(it.ctx, inventorycore.ActivateInput{TenantID: it.fx.TenantID, OriginVersionID: *resolvedB.AdjustmentVersionID, ActorMembershipID: it.fx.MembershipID, Purpose: "CONTESTATION_ADJUSTMENT", ExpectedOriginVersion: origin.Version}); err != nil {
		t.Fatalf("activate adjustment: %v", err)
	}
	if superseded := it.version(t, it.versionID); superseded.Status != "SUPERSEDED" {
		t.Fatalf("contested version status=%s, want SUPERSEDED", superseded.Status)
	}
	_, err = it.service.Resolve(it.ctx, it.fx.TenantID, ownerC.InvitationID, it.fx.MembershipID, "ACCEPT", "")
	assertAppError(t, err, apperror.InvalidState, "counterpartyInvitationId")
	if stored := it.invitation(t, it.versionID, c); stored.Resolution != "OPEN" {
		t.Fatalf("ACCEPT on superseded version persisted resolution %s", stored.Resolution)
	}
	view, err = it.service.View(it.ctx, ownerC)
	if err != nil || !view.Newer {
		t.Fatalf("UT-295 view after adjustment activation newer=%v err=%v", view.Newer, err)
	}
}

func TestIT_UT299_ConcurrentResolveHasOneWinner(t *testing.T) {
	it := newCounterpartyIT(t, 0)
	it.invite(t, it.versionID, it.participants[0])
	owner := it.contest(t, it.participants[0])
	errs := make([]error, 2)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i, outcome := range []string{"DECLINE", "ACCEPT"} {
		wg.Add(1)
		go func(i int, outcome string) {
			defer wg.Done()
			<-start
			_, errs[i] = it.service.Resolve(it.ctx, it.fx.TenantID, owner.InvitationID, it.fx.MembershipID, outcome, "motivo")
		}(i, outcome)
	}
	close(start)
	wg.Wait()
	winners, losers := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			winners++
		case apperror.Is(err, apperror.Conflict):
			if code, _, _ := apperror.Public(err); code == apperror.AlreadyResolved {
				losers++
			}
		default:
			t.Fatalf("UT-299 unexpected error: %v", err)
		}
	}
	if winners != 1 || losers != 1 {
		t.Fatalf("UT-299 winners=%d already-resolved=%d errors=%v", winners, losers, errs)
	}
	stored := it.invitation(t, it.versionID, it.participants[0])
	drafts := it.count(t, &database.OriginVersion{}, "origin_id=? AND status='DRAFT'", it.originID)
	if (stored.Resolution == "ACCEPTED") != (drafts == 1) || (stored.Resolution == "DECLINED" && drafts != 0) {
		t.Fatalf("UT-299 resolution=%s drafts=%d are inconsistent", stored.Resolution, drafts)
	}
}

func TestIT_InviteNotificationFailureAbortsActivation(t *testing.T) {
	it := newCounterpartyIT(t, 0)
	b := it.seedParticipant(t, 9)
	it.invite(t, it.versionID, b)
	owner := it.contest(t, b)
	resolved, err := it.service.Resolve(it.ctx, it.fx.TenantID, owner.InvitationID, it.fx.MembershipID, "ACCEPT", "")
	if err != nil {
		t.Fatal(err)
	}
	draftID := *resolved.AdjustmentVersionID
	failing := it.service
	failing.Notifications = &invitationNotificationFake{err: errFailingNotification}
	inventory := it.inventory
	inventory.Counterparties = failing
	activate := func(service inventorycore.Service) error {
		var origin database.Origin
		if err := it.h.WithinTenant(it.ctx, it.fx.TenantID, func(tx *gorm.DB) error {
			return tx.Where("tenant_id=? AND id=?", it.fx.TenantID, it.originID).First(&origin).Error
		}); err != nil {
			t.Fatal(err)
		}
		_, err := service.Activate(it.ctx, inventorycore.ActivateInput{TenantID: it.fx.TenantID, OriginVersionID: draftID, ActorMembershipID: it.fx.MembershipID, Purpose: "CONTESTATION_ADJUSTMENT", ExpectedOriginVersion: origin.Version, CounterpartyParticipantIDs: []identity.ID{it.participants[0]}})
		return err
	}
	if err := activate(inventory); err == nil {
		t.Fatal("activation succeeded although the counterparty notification failed")
	}
	if draft := it.version(t, draftID); draft.Status != "DRAFT" {
		t.Fatalf("aborted activation left draft status %s", draft.Status)
	}
	if active := it.version(t, it.versionID); active.Status != "ACTIVE" {
		t.Fatalf("aborted activation changed previous version to %s", active.Status)
	}
	if got := it.count(t, &database.CounterpartyInvitation{}, "origin_version_id=?", draftID); got != 0 {
		t.Fatalf("aborted activation persisted %d invitations", got)
	}
	inventory.Counterparties = it.service
	if err := activate(inventory); err != nil {
		t.Fatalf("activation with working notifications: %v", err)
	}
	if invitation := it.invitation(t, draftID, it.participants[0]); invitation.Response != "PENDING" || invitation.DeliveryGeneration != 1 {
		t.Fatalf("activation invitation=%+v", invitation)
	}
}

func TestIT_UT299_ResolveWaitsForConcurrentResolutionLock(t *testing.T) {
	it := newCounterpartyIT(t, 0)
	it.invite(t, it.versionID, it.participants[0])
	owner := it.contest(t, it.participants[0])
	locked, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- it.h.WithinTenant(it.ctx, it.fx.TenantID, func(tx *gorm.DB) error {
			if err := tx.Exec("SELECT id FROM origins.counterparty_invitations WHERE tenant_id=? AND id=? FOR UPDATE", it.fx.TenantID, owner.InvitationID).Error; err != nil {
				return err
			}
			if err := tx.Exec("UPDATE origins.counterparty_invitations SET resolution='DECLINED', resolution_reason='primeiro operador', resolved_at=now() WHERE tenant_id=? AND id=?", it.fx.TenantID, owner.InvitationID).Error; err != nil {
				return err
			}
			close(locked)
			<-release
			return nil
		})
	}()
	<-locked
	result := make(chan error, 1)
	go func() {
		_, err := it.service.Resolve(it.ctx, it.fx.TenantID, owner.InvitationID, it.fx.MembershipID, "DECLINE", "segundo operador")
		result <- err
	}()
	time.Sleep(300 * time.Millisecond)
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	err := <-result
	if !apperror.Is(err, apperror.Conflict) {
		t.Fatalf("UT-299 second Resolve error=%v, want Conflict kind", err)
	}
	assertAppError(t, err, apperror.AlreadyResolved, "counterpartyInvitationId")
	if stored := it.invitation(t, it.versionID, it.participants[0]); stored.ResolutionReason == nil || *stored.ResolutionReason != "primeiro operador" {
		t.Fatalf("UT-299 first resolution overwritten: %+v", stored)
	}
}
