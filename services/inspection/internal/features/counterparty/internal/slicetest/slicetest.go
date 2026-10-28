// Package slicetest provides small SQLite and authorization fakes shared by the
// counterparty slice setup tests. It is imported only from _test files.
package slicetest

import (
	"context"
	"testing"
	"time"

	"inspection/libs/identity"
	counterpartycore "inspection/services/inspection/internal/features/counterparty/core"
	invitationcore "inspection/services/inspection/internal/features/invitations/core"
	notificationcore "inspection/services/inspection/internal/features/notifications/core"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/ratelimit"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// DB opens a single-connection SQLite database with the counterparty tables.
func DB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	for _, statement := range []string{
		"ATTACH DATABASE ':memory:' AS origins",
		"ATTACH DATABASE ':memory:' AS messaging",
		`CREATE TABLE messaging.outbox(id text primary key,tenant_id text,type text,schema_version integer,payload blob,correlation_id text,causation_id text,status text,attempts integer,next_attempt_at datetime,claimed_at datetime,last_error text,published_at datetime,created_at datetime)`,
		`CREATE TABLE origins.counterparty_invitations(id text primary key,tenant_id text,origin_version_id text,participant_id text,token_hash blob,otp_hash blob,otp_expires_at datetime,otp_attempts integer,delivery_generation integer,channels json,deadline_at datetime,response text,statement text,responded_at datetime,response_mutation_id text,resolution text,resolution_reason text,resolved_by text,resolved_at datetime,adjustment_version_id text,created_at datetime,updated_at datetime)`,
		`CREATE TABLE origins.baseline_policies(tenant_id text primary key,contestation_deadline_days integer,version integer,updated_at datetime)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db
}

// Service returns a counterparty service with every dependency satisfied.
func Service(db *gorm.DB) counterpartycore.Service {
	return counterpartycore.Service{
		DB:             db,
		Pepper:         make([]byte, 32),
		Limits:         AllowLimits{},
		Notifier:       NoopNotifier{},
		Notifications:  NoopNotifications{},
		CaptureBaseURL: "https://capture.example",
		Within: func(_ context.Context, _ identity.ID, fn func(*gorm.DB) error) error {
			return fn(db)
		},
	}
}

// AllowLimits allows every OTP operation.
type AllowLimits struct{}

// AllowSend allows sending a code.
func (AllowLimits) AllowSend(context.Context, string) (ratelimit.Result, error) {
	return ratelimit.Result{Allowed: true}, nil
}

// AllowResend allows resending a code.
func (AllowLimits) AllowResend(context.Context, string) (ratelimit.Result, error) {
	return ratelimit.Result{Allowed: true}, nil
}

// AllowAttempt allows verifying a code.
func (AllowLimits) AllowAttempt(context.Context, string) (ratelimit.Result, error) {
	return ratelimit.Result{Allowed: true}, nil
}

// NoopNotifier accepts every OTP delivery.
type NoopNotifier struct{}

// SendOTP does nothing.
func (NoopNotifier) SendOTP(context.Context, identity.ID, identity.ID, string, []invitationcore.DeliveryIntent) error {
	return nil
}

// NoopNotifications accepts every notification request.
type NoopNotifications struct{}

// Send does nothing.
func (NoopNotifications) Send(context.Context, notificationcore.Notification) (notificationcore.NotificationResult, error) {
	return notificationcore.NotificationResult{}, nil
}

// MembershipStore resolves every caller to the configured roles and products.
type MembershipStore struct {
	Roles    []string
	Products []string
}

// Resolve returns the configured membership.
func (s MembershipStore) Resolve(_ context.Context, tenantID, identityID identity.ID) (requestctx.Principal, error) {
	return requestctx.Principal{IdentityID: identityID, MembershipID: identityID, TenantID: tenantID, Roles: s.Roles, ProductEntitlements: s.Products}, nil
}

// Authorizer builds an authorizer whose current membership has roles and products.
func Authorizer(roles, products []string) auth.Authorizer {
	return auth.Authorizer{Store: MembershipStore{Roles: roles, Products: products}}
}

// Context returns an authenticated internal request context for tenantID.
func Context(tenantID identity.ID) context.Context {
	identityID := identity.NewID()
	return requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenantID, Principal: requestctx.Principal{IdentityID: identityID, TenantID: tenantID}})
}

// Invitation inserts one counterparty invitation with the given deadline and response.
func Invitation(t *testing.T, db *gorm.DB, tenantID identity.ID, deadline time.Time, response string) database.CounterpartyInvitation {
	t.Helper()
	id := identity.NewID()
	row := database.CounterpartyInvitation{ID: id, TenantID: tenantID, OriginVersionID: identity.NewID(), ParticipantID: identity.NewID(), TokenHash: []byte(id.String()), Channels: []byte(`[]`), DeadlineAt: deadline, Response: response, Resolution: "OPEN", DeliveryGeneration: 1, CreatedAt: deadline, UpdatedAt: deadline}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	return row
}
