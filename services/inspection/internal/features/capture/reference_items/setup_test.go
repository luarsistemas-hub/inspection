package reference_items

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/objectstore"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type signerStub struct{ keys []string }

func (*signerStub) CreateMultipart(context.Context, string, string, string) (string, error) {
	return "", nil
}
func (*signerStub) PresignPart(context.Context, string, string, string, int, time.Duration) (string, error) {
	return "", nil
}
func (*signerStub) CompleteMultipart(context.Context, string, string, string, []objectstore.Part) error {
	return nil
}
func (*signerStub) AbortMultipart(context.Context, string, string, string) error { return nil }
func (*signerStub) Head(context.Context, string, string) (objectstore.Head, error) {
	return objectstore.Head{}, nil
}
func (*signerStub) Get(context.Context, string, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}
func (*signerStub) Put(context.Context, string, string, io.Reader, int64, string) error { return nil }
func (s *signerStub) PresignGet(_ context.Context, _, key string, _ time.Duration) (string, error) {
	s.keys = append(s.keys, key)
	return "https://signed.invalid/" + key, nil
}

func TestSetupRequiresDependencies(t *testing.T) {
	if err := Setup(Dependencies{}); err == nil {
		t.Fatal("missing dependencies accepted")
	}
}

func TestReferenceItemsArePinnedAuthorizedAndDisplayOnly(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"capture", "invitations", "media"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, model := range []any{&database.CaptureDraft{}, &database.ProcessingAcceptance{}, &database.MediaObject{}, &database.MediaDerivative{}} {
		if err := db.AutoMigrate(model); err != nil && !strings.Contains(err.Error(), "no such table: main.") {
			t.Fatal(err)
		}
	}
	parent, child, missing, unrelated := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	tenant, responsibility := identity.NewID(), identity.NewID()
	requirements, _ := json.Marshal([]capturecore.Requirement{
		{Key: "origin:" + child.String(), Section: "Referência", Instructions: "Cozinha", ComparisonTarget: "FIXED_ORIGIN"},
		{Key: "origin:" + missing.String(), Section: "Referência", Instructions: "Sala", ComparisonTarget: "FIXED_ORIGIN"},
	})
	reference, _ := json.Marshal(map[string]any{"comparisonMode": "FIXED_ORIGIN", "items": []map[string]any{{"mediaId": child, "description": "Cozinha"}, {"mediaId": missing, "description": "Sala"}, {"mediaId": parent, "description": "Não solicitado"}}})
	rows := []any{
		&database.CaptureDraft{ID: identity.NewID(), TenantID: tenant, ResponsibilityID: responsibility, Kind: "INSPECTION", Status: "OPEN", Requirements: requirements, ReferencePayload: reference, PolicyPayload: json.RawMessage(`{}`)},
		&database.ProcessingAcceptance{ID: identity.NewID(), TenantID: tenant, ResponsibilityID: responsibility, PhotoProcessing: true, AIAnalysis: true, GPSUse: true},
		&database.MediaObject{ID: child, TenantID: tenant, ResponsibilityID: parent, ObjectKey: "original", ContentType: "image/jpeg", Status: "READY"},
		&database.MediaObject{ID: unrelated, TenantID: tenant, ResponsibilityID: parent, ObjectKey: "unrelated", ContentType: "image/jpeg", Status: "READY"},
		&database.MediaDerivative{ID: identity.NewID(), TenantID: tenant, MediaID: child, ObjectKey: "display/cozinha", Kind: "DISPLAY", SHA256: "hash"},
		&database.MediaDerivative{ID: identity.NewID(), TenantID: tenant, MediaID: unrelated, ObjectKey: "display/unrelated", Kind: "DISPLAY", SHA256: "hash"},
	}
	for _, row := range rows {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	signer := &signerStub{}
	bus := mediator.New()
	if err := Setup(Dependencies{Bus: bus, DB: db, Store: objectstore.Store{Bucket: "private", Client: signer}, Within: func(ctx context.Context, _ identity.ID, fn func(*gorm.DB) error) error {
		return db.WithContext(ctx).Transaction(fn)
	}}); err != nil {
		t.Fatal(err)
	}
	raw, err := bus.Ask(context.Background(), Query{TenantID: tenant, ResponsibilityID: responsibility})
	if err != nil {
		t.Fatal(err)
	}
	items := raw.([]Item)
	if len(items) != 2 || items[0].ImageURL != "https://signed.invalid/display/cozinha" || items[0].Availability != "AVAILABLE" || items[0].ImageURLExpiresAt == nil || items[1].Availability != "UNAVAILABLE" || items[1].ImageURL != "" {
		t.Fatalf("unexpected reference projection: %+v", items)
	}
	if len(signer.keys) != 1 || signer.keys[0] != "display/cozinha" {
		t.Fatalf("signed keys leaked beyond requested display: %v", signer.keys)
	}
	if err := db.Where("tenant_id=? AND responsibility_id=?", tenant, responsibility).Delete(&database.ProcessingAcceptance{}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := bus.Ask(context.Background(), Query{TenantID: tenant, ResponsibilityID: responsibility}); err == nil {
		t.Fatal("references exposed without acceptance")
	}
}
