package delivery_status

import (
	"context"
	"errors"
	"testing"

	"inspection/libs/identity"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestLoadUsesTenantTransactionAndReturnsSubmittedRequest(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{
		"ATTACH DATABASE ':memory:' AS onboarding",
		"CREATE TABLE onboarding.requests (id TEXT, tenant_id TEXT, session_id TEXT, inspection_id TEXT, status TEXT)",
	} {
		if err := db.Exec(sql).Error; err != nil {
			t.Fatal(err)
		}
	}
	tenantID, sessionID, requestID := identity.NewID(), identity.NewID(), identity.NewID()
	if err := db.Exec("INSERT INTO onboarding.requests (id, tenant_id, session_id, status) VALUES (?, ?, ?, ?)", requestID.String(), tenantID.String(), sessionID.String(), "SUBMITTED").Error; err != nil {
		t.Fatal(err)
	}
	called := false
	service := Service{DB: db, Within: func(ctx context.Context, id identity.ID, fn func(*gorm.DB) error) error {
		called = true
		if id != tenantID {
			t.Fatalf("tenant = %s, want %s", id, tenantID)
		}
		return db.WithContext(ctx).Transaction(fn)
	}}
	status, err := service.Load(context.Background(), tenantID, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !called || status.State != "SUBMITTED" || status.RequestID == nil || *status.RequestID != requestID {
		t.Fatalf("tenant transaction or submitted status missing: called=%v status=%+v", called, status)
	}
}

func TestLoadPropagatesTenantTransactionFailure(t *testing.T) {
	want := errors.New("tenant context unavailable")
	service := Service{Within: func(context.Context, identity.ID, func(*gorm.DB) error) error { return want }}
	_, err := service.Load(context.Background(), identity.NewID(), identity.NewID())
	if !errors.Is(err, want) {
		t.Fatalf("Load error = %v, want %v", err, want)
	}
}
