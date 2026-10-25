package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	invitationcore "inspection/services/inspection/internal/features/invitations/core"
	"inspection/services/inspection/internal/features/notifications/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/ratelimit"
	"inspection/services/inspection/internal/platform/security"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type invitationNotificationFake struct {
	requests []core.Notification
	err      error
}

func (f *invitationNotificationFake) Send(_ context.Context, request core.Notification) (core.NotificationResult, error) {
	if f.err != nil {
		return core.NotificationResult{}, f.err
	}
	f.requests = append(f.requests, request)
	return core.NotificationResult{ID: identity.NewID(), State: core.StateQueued}, nil
}

type invitationOTPNotifierFake struct {
	code string
	err  error
}

func (f *invitationOTPNotifierFake) SendOTP(_ context.Context, _, _ identity.ID, code string, _ []invitationcore.DeliveryIntent) error {
	if f.err != nil {
		return f.err
	}
	f.code = code
	return nil
}

type allowOTPLimits struct{}

func (allowOTPLimits) AllowSend(context.Context, string) (ratelimit.Result, error) {
	return ratelimit.Result{Allowed: true}, nil
}
func (allowOTPLimits) AllowResend(context.Context, string) (ratelimit.Result, error) {
	return ratelimit.Result{Allowed: true}, nil
}
func (allowOTPLimits) AllowAttempt(context.Context, string) (ratelimit.Result, error) {
	return ratelimit.Result{Allowed: true}, nil
}

// counterpartyFixture is one tenant with an ACTIVE inventory baseline and
// invited counterparties whose link tokens were captured from the notifier.
type counterpartyFixture struct {
	db            *gorm.DB
	service       Service
	clock         *time.Time
	notifications *invitationNotificationFake
	otp           *invitationOTPNotifierFake
	tenantID      identity.ID
	assetID       identity.ID
	versionID     identity.ID
	participants  []identity.ID
}

func newCounterpartyFixture(t *testing.T, participants int) *counterpartyFixture {
	t.Helper()
	db := inviteTestDB(t)
	clock := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	f := &counterpartyFixture{db: db, clock: &clock, notifications: &invitationNotificationFake{}, otp: &invitationOTPNotifierFake{}, tenantID: identity.NewID(), assetID: identity.NewID(), versionID: identity.NewID()}
	for i := 0; i < participants; i++ {
		f.participants = append(f.participants, identity.NewID())
	}
	seedInviteBaseline(t, db, f.tenantID, f.assetID, f.versionID, f.participants...)
	f.service = Service{DB: db, Clock: func() time.Time { return *f.clock }, Pepper: []byte(strings.Repeat("p", 32)), Limits: allowOTPLimits{}, Notifier: f.otp, Notifications: f.notifications, CaptureBaseURL: "https://capture.example", Within: func(_ context.Context, _ identity.ID, fn func(*gorm.DB) error) error { return fn(db) }}
	return f
}

func (f *counterpartyFixture) invite(t *testing.T, participants ...identity.ID) {
	t.Helper()
	if err := f.service.Invite(f.db, f.tenantID, f.versionID, participants, identity.NewID()); err != nil {
		t.Fatal(err)
	}
}

func (f *counterpartyFixture) invitation(t *testing.T, participantID identity.ID) database.CounterpartyInvitation {
	t.Helper()
	var row database.CounterpartyInvitation
	if err := f.db.Where("tenant_id=? AND origin_version_id=? AND participant_id=?", f.tenantID, f.versionID, participantID).First(&row).Error; err != nil {
		t.Fatal(err)
	}
	return row
}

// tokenFor returns the link token of the latest delivery sent to participantID.
func (f *counterpartyFixture) tokenFor(t *testing.T, participantID identity.ID) string {
	t.Helper()
	for i := len(f.notifications.requests) - 1; i >= 0; i-- {
		if request := f.notifications.requests[i]; request.Recipient.ID == participantID.String() {
			return request.Execution.Token
		}
	}
	t.Fatalf("no delivery for participant %s", participantID)
	return ""
}

func (f *counterpartyFixture) countInvitations(t *testing.T) int64 {
	t.Helper()
	var count int64
	if err := f.db.Model(&database.CounterpartyInvitation{}).Where("tenant_id=?", f.tenantID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	return count
}

func TestUnit_UT280_InviteCreatesPendingInvitationAndDeliveryPerParticipant(t *testing.T) {
	f := newCounterpartyFixture(t, 2)
	f.invite(t, f.participants...)
	if got := f.countInvitations(t); got != 2 || len(f.notifications.requests) != 2 {
		t.Fatalf("UT-280 invitations=%d delivery intents=%d, want 2/2", got, len(f.notifications.requests))
	}
	location, err := time.LoadLocation("America/Sao_Paulo")
	if err != nil {
		t.Fatal(err)
	}
	for i, participantID := range f.participants {
		row := f.invitation(t, participantID)
		if row.Response != "PENDING" || row.Resolution != "OPEN" || row.RespondedAt != nil || !row.DeadlineAt.Equal(f.clock.AddDate(0, 0, DefaultDeadlineDays)) || row.DeliveryGeneration != 1 {
			t.Fatalf("UT-280 invitation=%+v, want PENDING/OPEN, deadline now+%d days, generation 1", row, DefaultDeadlineDays)
		}
		var delivery core.Notification
		matches := 0
		for _, request := range f.notifications.requests {
			if request.Recipient.ID == participantID.String() {
				delivery = request
				matches++
			}
		}
		if matches != 1 {
			t.Fatalf("UT-280 participant %d delivery intents=%d, want 1", i, matches)
		}
		destination := fmt.Sprintf("%c@example.com", 'a'+i)
		if delivery.Template != (core.TemplateRef{Name: "baseline-acknowledgment", Version: "v1"}) || delivery.Channel != core.ChannelEmail || delivery.Recipient.Destination != destination {
			t.Fatalf("UT-280 delivery template/channel/destination=%+v", delivery)
		}
		wantVariables := map[string]string{"assetName": "Apartment 4B", "purposeLabel": "entrada", "deadlineDate": row.DeadlineAt.In(location).Format("02/01/2006 15:04")}
		if fmt.Sprint(delivery.Variables) != fmt.Sprint(wantVariables) {
			t.Fatalf("UT-280 variables=%v, want %v", delivery.Variables, wantVariables)
		}
		if delivery.Execution == nil || delivery.Execution.URLVariable != "link" || delivery.Execution.BaseURL != "https://capture.example" || delivery.Execution.InvitationID != row.ID || delivery.Execution.ExpiresAt != row.DeadlineAt.Unix() {
			t.Fatalf("UT-280 delivery link execution=%+v", delivery.Execution)
		}
		if hash := security.HashToken(delivery.Execution.Token); string(hash[:]) != string(row.TokenHash) {
			t.Fatal("UT-280 stored token hash does not match the delivered link token")
		}
		if delivery.CorrelationID != "counterparty-"+row.ID.String()+"-g1" || !strings.HasSuffix(delivery.CorrelationID, "-g1") {
			t.Fatalf("UT-280 first delivery correlation=%q, want generation 1", delivery.CorrelationID)
		}
		wantKey := core.RecipientIdempotencyKey(identity.NewDeterministicID("counterparty/delivery", row.ID.String()+":1"), core.ChannelEmail, destination)
		if delivery.IdempotencyKey != wantKey {
			t.Fatalf("UT-280 first delivery idempotency key=%q, want generation-1 key %q", delivery.IdempotencyKey, wantKey)
		}
	}
}

func TestUnit_UT281_InviteDeduplicatesParticipants(t *testing.T) {
	f := newCounterpartyFixture(t, 1)
	a := f.participants[0]
	f.invite(t, a, a)
	if got := f.countInvitations(t); got != 1 || len(f.notifications.requests) != 1 {
		t.Fatalf("UT-281 [A, A] invitations=%d deliveries=%d, want 1/1", got, len(f.notifications.requests))
	}
	first := f.invitation(t, a)
	*f.clock = f.clock.Add(time.Hour)
	f.invite(t, a)
	if got := f.countInvitations(t); got != 1 || len(f.notifications.requests) != 1 {
		t.Fatalf("UT-281 repeated invite invitations=%d deliveries=%d, want 1/1", got, len(f.notifications.requests))
	}
	if again := f.invitation(t, a); again.ID != first.ID || !again.DeadlineAt.Equal(first.DeadlineAt) || string(again.TokenHash) != string(first.TokenHash) {
		t.Fatalf("UT-281 repeated invite changed invitation: %+v -> %+v", first, again)
	}
}

func TestUnit_Issue012_DuplicateVerifiedRowsProduceOneDelivery(t *testing.T) {
	f := newCounterpartyFixture(t, 1)
	participantID := f.participants[0]
	var contact struct{ ID identity.ID }
	if err := f.db.Table("participants.contacts").Select("id").Where("tenant_id=? AND participant_id=?", f.tenantID, participantID).Take(&contact).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Exec(`INSERT INTO participants.contact_verifications(id,tenant_id,contact_id,status,created_at) VALUES(?,?,?,?,?)`, identity.NewID(), f.tenantID, contact.ID, "VERIFIED", f.clock.Add(time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	f.invite(t, participantID)
	if got := len(f.notifications.requests); got != 1 {
		t.Fatalf("duplicate VERIFIED rows produced %d delivery intents, want 1", got)
	}
}

func TestUnit_UT282_InviteRejectsParticipantWithoutVerifiedSelectedChannel(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, db *gorm.DB, tenantID, participantID identity.ID)
	}{
		{name: "UT-282 contact verification pending", setup: func(t *testing.T, db *gorm.DB, tenantID, participantID identity.ID) {
			seedParticipantContact(t, db, tenantID, participantID, "PENDING", true, true)
		}},
		{name: "UT-282 verified contact not selected", setup: func(t *testing.T, db *gorm.DB, tenantID, participantID identity.ID) {
			seedParticipantContact(t, db, tenantID, participantID, "VERIFIED", false, true)
		}},
		{name: "UT-282 selected verified contact inactive", setup: func(t *testing.T, db *gorm.DB, tenantID, participantID identity.ID) {
			seedParticipantContact(t, db, tenantID, participantID, "VERIFIED", true, false)
		}},
		{name: "UT-282 participant without contacts", setup: func(*testing.T, *gorm.DB, identity.ID, identity.ID) {}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCounterpartyFixture(t, 1)
			unverified := identity.NewID()
			if err := f.db.Exec(`INSERT INTO participants.participants(id,tenant_id,status) VALUES(?,?,?)`, unverified, f.tenantID, "ACTIVE").Error; err != nil {
				t.Fatal(err)
			}
			tc.setup(t, f.db, f.tenantID, unverified)
			err := f.db.Transaction(func(tx *gorm.DB) error {
				return f.service.Invite(tx, f.tenantID, f.versionID, []identity.ID{f.participants[0], unverified}, identity.NewID())
			})
			if code, field, _ := apperror.Public(err); code != apperror.InvalidInput || field != "counterpartyParticipantIds" {
				t.Fatalf("error=%v code=%s field=%q, want INVALID_INPUT/counterpartyParticipantIds", err, code, field)
			}
			if got := f.countInvitations(t); got != 0 {
				t.Fatalf("rejected selection persisted %d invitations", got)
			}
		})
	}
}

func TestUnit_UT283_PolicyChangeKeepsExistingDeadlineAndAppliesToNewInvitations(t *testing.T) {
	f := newCounterpartyFixture(t, 2)
	a, b := f.participants[0], f.participants[1]
	invitedAt := *f.clock
	f.invite(t, a)
	policy, err := f.service.UpdatePolicy(context.Background(), f.tenantID, 10)
	if err != nil || policy.ContestationDeadlineDays != 10 || policy.Version != 2 {
		t.Fatalf("UT-283 UpdatePolicy 5->10 policy=%+v err=%v, want 10 days version 2", policy, err)
	}
	*f.clock = f.clock.Add(time.Hour)
	f.invite(t, a)
	if existing := f.invitation(t, a); !existing.DeadlineAt.Equal(invitedAt.AddDate(0, 0, 5)) {
		t.Fatalf("UT-283 existing deadline=%s, want original %s", existing.DeadlineAt, invitedAt.AddDate(0, 0, 5))
	}
	f.invite(t, b)
	if created := f.invitation(t, b); !created.DeadlineAt.Equal(f.clock.AddDate(0, 0, 10)) {
		t.Fatalf("UT-283 new invitation deadline=%s, want now+10 days %s", created.DeadlineAt, f.clock.AddDate(0, 0, 10))
	}
}

func TestUnit_InviteNotificationFailureAbortsActivationTransaction(t *testing.T) {
	f := newCounterpartyFixture(t, 2)
	providerErr := errors.New("notification outbox unavailable")
	f.service.Notifications = &invitationNotificationFake{err: providerErr}
	err := f.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("UPDATE origins.origin_versions SET purpose=? WHERE id=?", "POST_RENOVATION", f.versionID).Error; err != nil {
			return err
		}
		return f.service.Invite(tx, f.tenantID, f.versionID, f.participants, identity.NewID())
	})
	if !errors.Is(err, providerErr) {
		t.Fatalf("invite error=%v, want notification failure", err)
	}
	if got := f.countInvitations(t); got != 0 {
		t.Fatalf("aborted activation persisted %d invitations", got)
	}
	var version database.OriginVersion
	if err := f.db.Where("id=?", f.versionID).First(&version).Error; err != nil || version.Purpose == nil || *version.Purpose != "MOVE_IN" {
		t.Fatalf("aborted activation kept its other writes: %+v err=%v", version, err)
	}
	f.service.Notifications = nil
	if err := f.service.Invite(f.db, f.tenantID, f.versionID, f.participants[:1], identity.NewID()); !apperror.Is(err, apperror.DependencyUnavailable) {
		t.Fatalf("invite without notification service error=%v, want DEPENDENCY_UNAVAILABLE", err)
	}
}

func TestUnit_InviteRejectsInactiveVersionAndInvalidParticipant(t *testing.T) {
	f := newCounterpartyFixture(t, 1)
	if err := f.service.Invite(f.db, f.tenantID, f.versionID, []identity.ID{{}}, identity.NewID()); !apperror.Is(err, apperror.InvalidInput) {
		t.Fatalf("zero participant id error=%v, want INVALID_INPUT", err)
	}
	if err := f.service.Invite(f.db, f.tenantID, identity.NewID(), f.participants, identity.NewID()); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("unknown version error=%v, want not found", err)
	}
	if got := f.countInvitations(t); got != 0 {
		t.Fatalf("rejected invite persisted %d invitations", got)
	}
}

func inviteTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	// Attached in-memory schemas belong to one connection.
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	for _, schema := range []string{"origins", "assets", "participants", "messaging", "invitations", "media", "notifications"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, ddl := range []string{
		`CREATE TABLE messaging.outbox(id text primary key,tenant_id text,type text,schema_version integer,payload blob,correlation_id text,causation_id text,status text,attempts integer,next_attempt_at datetime,claimed_at datetime,last_error text,published_at datetime,created_at datetime)`,
		`CREATE TABLE invitations.external_sessions(id text primary key,tenant_id text,kind text,invitation_id text,responsibility_id text,counterparty_invitation_id text,session_digest blob,csrf_digest blob,expires_at datetime,revoked_at datetime,created_at datetime)`,
		`CREATE TABLE origins.origins(id text primary key,tenant_id text,asset_id text,active_version_id text,version integer)`,
		`CREATE TABLE origins.origin_versions(id text primary key,tenant_id text,origin_id text,status text,has_inventory numeric,purpose text,video_preparation_id text)`,
		`CREATE TABLE origins.inventory_environments(id text primary key,tenant_id text,origin_version_id text,decision text,position integer)`,
		`CREATE TABLE origins.origin_evidence(id text,tenant_id text,origin_version_id text,media_id text)`,
		`CREATE TABLE origins.inventory_item_evidence(id text,tenant_id text,item_id text,media_id text)`,
		`CREATE TABLE origins.inventory_items(id text,tenant_id text,origin_version_id text,environment_id text,decision text,merged_into_id text)`,
		`CREATE TABLE media.video_preparation_jobs(id text,tenant_id text,media_id text)`,
		`CREATE TABLE media.video_sources(id text,tenant_id text,media_id text,status text)`,
		`CREATE TABLE notifications.deliveries(id text primary key,tenant_id text,intent_id text,inspection_id text,invitation_id text,status text,logical_template text,template_version text,correlation_id text,idempotency_key text,request_digest text,recipient_id text,selected_provider text,scheduled_at datetime,lease_expires_at datetime,created_at datetime,updated_at datetime)`,
		`CREATE TABLE assets.assets(id text primary key,tenant_id text,name text)`,
		`CREATE TABLE participants.participants(id text primary key,tenant_id text,status text)`,
		`CREATE TABLE participants.contacts(id text primary key,tenant_id text,participant_id text,channel text,value text,active numeric)`,
		`CREATE TABLE participants.contact_verifications(id text primary key,tenant_id text,contact_id text,status text,created_at datetime)`,
		`CREATE TABLE participants.channel_selections(id text primary key,tenant_id text,participant_id text,contact_id text)`,
		`CREATE TABLE origins.baseline_policies(tenant_id text primary key,contestation_deadline_days integer,version integer,updated_at datetime)`,
		`CREATE TABLE origins.counterparty_invitations(id text primary key,tenant_id text,origin_version_id text,participant_id text,token_hash blob,otp_hash blob,otp_expires_at datetime,otp_attempts integer,delivery_generation integer,channels json,deadline_at datetime,response text,statement text,responded_at datetime,response_mutation_id text,resolution text,resolution_reason text,resolved_by text,resolved_at datetime,adjustment_version_id text,created_at datetime,updated_at datetime)`,
	} {
		if err := db.Exec(ddl).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func seedInviteBaseline(t *testing.T, db *gorm.DB, tenantID, assetID, versionID identity.ID, participants ...identity.ID) {
	t.Helper()
	originID := identity.NewID()
	now := time.Now().UTC()
	if err := db.Exec(`INSERT INTO assets.assets(id,tenant_id,name) VALUES(?,?,?)`, assetID, tenantID, "Apartment 4B").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO origins.origins(id,tenant_id,asset_id,active_version_id,version) VALUES(?,?,?,?,?)`, originID, tenantID, assetID, versionID, 1).Error; err != nil {
		t.Fatal(err)
	}
	purpose := "MOVE_IN"
	if err := db.Exec(`INSERT INTO origins.origin_versions(id,tenant_id,origin_id,status,has_inventory,purpose) VALUES(?,?,?,?,?,?)`, versionID, tenantID, originID, "ACTIVE", true, purpose).Error; err != nil {
		t.Fatal(err)
	}
	for i, participantID := range participants {
		if err := db.Exec(`INSERT INTO participants.participants(id,tenant_id,status) VALUES(?,?,?)`, participantID, tenantID, "ACTIVE").Error; err != nil {
			t.Fatal(err)
		}
		seedParticipantContactValue(t, db, tenantID, participantID, string(rune('a'+i))+"@example.com", "VERIFIED", true, true)
	}
	if err := db.Exec(`INSERT INTO origins.baseline_policies(tenant_id,contestation_deadline_days,version,updated_at) VALUES(?,?,?,?)`, tenantID, DefaultDeadlineDays, 1, now).Error; err != nil {
		t.Fatal(err)
	}
}

func seedParticipantContact(t *testing.T, db *gorm.DB, tenantID, participantID identity.ID, verification string, selected, active bool) {
	t.Helper()
	seedParticipantContactValue(t, db, tenantID, participantID, participantID.String()+"@example.com", verification, selected, active)
}

func seedParticipantContactValue(t *testing.T, db *gorm.DB, tenantID, participantID identity.ID, value, verification string, selected, active bool) {
	t.Helper()
	contactID := identity.NewID()
	now := time.Now().UTC()
	if err := db.Exec(`INSERT INTO participants.contacts(id,tenant_id,participant_id,channel,value,active) VALUES(?,?,?,?,?,?)`, contactID, tenantID, participantID, "EMAIL", value, active).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO participants.contact_verifications(id,tenant_id,contact_id,status,created_at) VALUES(?,?,?,?,?)`, identity.NewID(), tenantID, contactID, verification, now).Error; err != nil {
		t.Fatal(err)
	}
	if selected {
		if err := db.Exec(`INSERT INTO participants.channel_selections(id,tenant_id,participant_id,contact_id) VALUES(?,?,?,?)`, identity.NewID(), tenantID, participantID, contactID).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func TestUnit_UT302_UpdatePolicyBoundariesThroughService(t *testing.T) {
	f := newCounterpartyFixture(t, 0)
	tenantID := identity.NewID()
	for _, days := range []int{0, 91, -1} {
		_, err := f.service.UpdatePolicy(context.Background(), tenantID, days)
		assertAppError(t, err, apperror.InvalidInput, "contestationDeadlineDays")
	}
	var count int64
	if err := f.db.Model(&database.BaselinePolicy{}).Where("tenant_id=?", tenantID).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("UT-302 rejected values persisted %d policies err=%v", count, err)
	}
	first, err := f.service.UpdatePolicy(context.Background(), tenantID, 1)
	if err != nil || first.ContestationDeadlineDays != 1 || first.Version != 1 {
		t.Fatalf("UT-302 first update=%+v err=%v, want 1 day version 1", first, err)
	}
	second, err := f.service.UpdatePolicy(context.Background(), tenantID, 90)
	if err != nil || second.ContestationDeadlineDays != 90 || second.Version != 2 {
		t.Fatalf("UT-302 second update=%+v err=%v, want 90 days version 2", second, err)
	}
	var stored database.BaselinePolicy
	if err := f.db.Where("tenant_id=?", tenantID).First(&stored).Error; err != nil || stored.ContestationDeadlineDays != 90 || stored.Version != 2 {
		t.Fatalf("UT-302 stored policy=%+v err=%v", stored, err)
	}
}
