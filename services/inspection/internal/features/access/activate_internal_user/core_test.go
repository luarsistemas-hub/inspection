package activate_internal_user

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/ratelimit"
	"inspection/services/inspection/internal/platform/security"

	"github.com/mattn/go-sqlite3"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

var activationDriverOnce sync.Once
var activationDriverError error

type testLimits struct{}

func (testLimits) AllowSend(context.Context, string) (ratelimit.Result, error) {
	return ratelimit.Result{Allowed: true}, nil
}
func (testLimits) AllowResend(context.Context, string) (ratelimit.Result, error) {
	return ratelimit.Result{Allowed: true}, nil
}
func (testLimits) AllowAttempt(context.Context, string) (ratelimit.Result, error) {
	return ratelimit.Result{Allowed: true}, nil
}

type testNotifier struct{ code string }

func (n *testNotifier) SendOTP(_ context.Context, _, code, _ string) error { n.code = code; return nil }

type testProvider struct {
	verified, password string
	verifyErr          error
}

func (p *testProvider) VerifyEmail(_ context.Context, subject string) error {
	if p.verifyErr != nil {
		return p.verifyErr
	}
	p.verified = subject
	return nil
}
func (p *testProvider) SetInitialPassword(_ context.Context, _, password string) error {
	p.password = password
	return nil
}

func TestInvitationActivationRequiresDeliveredCodeAndPreservesExistingPassword(t *testing.T) {
	service, db, token, notifier, provider, tenantID, membershipID := activationFixture(t, false)
	claim, err := service.ClaimInvitation(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.RequestOTP(context.Background(), claim.Locator, claim.CSRF); err != nil {
		t.Fatal(err)
	}
	if _, err := service.VerifyOTP(context.Background(), claim.Locator, claim.CSRF, "000000"); err == nil {
		t.Fatal("wrong code accepted")
	}
	var challenge database.UserInvitationOTP
	if err := db.Where("tenant_id=?", tenantID).First(&challenge).Error; err != nil {
		t.Fatal(err)
	}
	if challenge.Attempts != 1 {
		t.Fatalf("wrong-code attempt was not persisted: %d", challenge.Attempts)
	}
	if _, err := service.VerifyOTP(context.Background(), claim.Locator, claim.CSRF, notifier.code); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Complete(context.Background(), claim.Locator, claim.CSRF, "ignored"); err != nil {
		t.Fatal(err)
	}
	var membership database.Membership
	if err := db.Where("tenant_id=? AND id=?", tenantID, membershipID).First(&membership).Error; err != nil {
		t.Fatal(err)
	}
	if membership.Status != "ACTIVE" || membership.InvitationStatus != "ACCEPTED" {
		t.Fatalf("membership not activated: %+v", membership)
	}
	if provider.verified != "subject-1" || provider.password != "" {
		t.Fatalf("unexpected Keycloak operations: %+v", provider)
	}
}

func TestNewInvitationCreatesPasswordOnlyAfterCodeAndRejectsExpiredLink(t *testing.T) {
	service, _, token, notifier, provider, _, _ := activationFixture(t, true)
	service.Stage = "dev"
	claim, err := service.ClaimInvitation(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.RequestOTP(context.Background(), claim.Locator, claim.CSRF); err != nil {
		t.Fatal(err)
	}
	if provider.password != "" {
		t.Fatal("password set before code verification")
	}
	if _, err := service.VerifyOTP(context.Background(), claim.Locator, claim.CSRF, notifier.code); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Complete(context.Background(), claim.Locator, claim.CSRF, "Abc123!"); err != nil {
		t.Fatal(err)
	}
	if provider.password != "Abc123!" {
		t.Fatal("new identity password was not set after verification")
	}

	expiredService, _, expiredToken, _, _, _, _ := activationFixture(t, false)
	expiredService.Clock = func() time.Time { return time.Now().UTC().Add(25 * time.Hour) }
	if _, err := expiredService.ClaimInvitation(context.Background(), expiredToken); err == nil {
		t.Fatal("expired invite accepted")
	}
}

func activationFixture(t *testing.T, newIdentity bool) (Service, *gorm.DB, string, *testNotifier, *testProvider, identity.ID, identity.ID) {
	t.Helper()
	const driverName = "sqlite3_activation_test"
	activationDriverOnce.Do(func() {
		sql.Register(driverName, &sqlite3.SQLiteDriver{ConnectHook: func(conn *sqlite3.SQLiteConn) error {
			return conn.RegisterFunc("set_config", func(_, value string, _ bool) string { return value }, true)
		}})
	})
	if activationDriverError != nil {
		t.Fatal(activationDriverError)
	}
	dbConn, err := sql.Open(driverName, "file:"+identity.NewID().String()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	dbConn.SetMaxOpenConns(1)
	db, err := gorm.Open(sqlite.New(sqlite.Config{Conn: dbConn}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS access").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE access.memberships (id blob primary key, tenant_id blob, identity_id blob, issuer text, subject text, name text, email text, role text, status text, invitation_status text, version integer, created_at datetime, updated_at datetime)`).Error; err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE access.user_invitations (id blob primary key, tenant_id blob, membership_id blob, issuer text, subject text, email text, name text, new_identity boolean, status text, token_digest blob, expires_at datetime, claimed_at datetime, session_digest blob, csrf_digest blob, session_expires_at datetime, password_set_at datetime, accepted_at datetime, created_at datetime, updated_at datetime)`,
		`CREATE TABLE access.user_invitation_otp_challenges (id blob primary key, tenant_id blob, invitation_id blob, code_hmac blob, attempts integer, expires_at datetime, verified_at datetime, created_at datetime)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = dbConn.Close() })
	tenantID, membershipID := identity.NewID(), identity.NewID()
	now := time.Now().UTC()
	token, err := security.NewOpaqueToken()
	if err != nil {
		t.Fatal(err)
	}
	digest := security.HashToken(token)
	if err := db.Create(&database.Membership{ID: membershipID, TenantID: tenantID, IdentityID: identity.NewID(), Issuer: "issuer", Subject: "subject-1", Name: "Ada", Email: "ada@example.test", Role: "EMPLOYEE", Status: "INVITED", InvitationStatus: "SENT", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.UserInvitation{ID: identity.NewID(), TenantID: tenantID, MembershipID: membershipID, Issuer: "issuer", Subject: "subject-1", Email: "ada@example.test", Name: "Ada", NewIdentity: newIdentity, Status: "SENT", TokenDigest: digest[:], ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	notifier, provider := &testNotifier{}, &testProvider{}
	service := Service{DB: db, Pepper: []byte("01234567890123456789012345678901"), Limits: testLimits{}, Notifier: notifier, Provider: provider, Clock: func() time.Time { return now }}
	return service, db, token, notifier, provider, tenantID, membershipID
}
