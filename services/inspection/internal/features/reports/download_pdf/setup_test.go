package download_pdf

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/objectstore"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// presigningClient implements only what the slice uses; every other Client
// method would panic on the embedded nil interface and is therefore never called.
type presigningClient struct {
	objectstore.Client
	ttl time.Duration
	key string
}

func (c *presigningClient) PresignGet(_ context.Context, bucket, key string, ttl time.Duration) (string, error) {
	c.ttl, c.key = ttl, key
	return "https://private.example.test/" + bucket + "/" + key + "?signed=1", nil
}

func TestSetupValidatesDatabaseAndPrivateStore(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Setup(Dependencies{Store: objectstore.Store{Bucket: "private", Client: &presigningClient{}}}); !errors.Is(err, gorm.ErrInvalidDB) {
		t.Fatalf("missing database accepted: %v", err)
	}
	// A client without PresignGet (the embedded nil interface only satisfies Client)
	// cannot mint download URLs, and neither can a public client that does not presign.
	plain := struct{ objectstore.Client }{}
	for name, store := range map[string]objectstore.Store{
		"no client":                 {Bucket: "private"},
		"no bucket":                 {Client: &presigningClient{}},
		"client cannot presign":     {Bucket: "private", Client: plain},
		"public client cannot sign": {Bucket: "private", Client: &presigningClient{}, PublicClient: plain},
	} {
		if _, err := Setup(Dependencies{DB: db, Store: store}); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}

func TestDownloadHidesObjectKeyUsesShortTTLAndStaysInsideTheTenant(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS reports").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.ReportArtifact{}); err != nil {
		t.Fatal(err)
	}
	tenant, snapshot := identity.NewID(), identity.NewID()
	artifact := database.ReportArtifact{ID: identity.NewID(), TenantID: tenant, SnapshotID: snapshot, Kind: "PDF", ObjectKey: "tenant/private/report.pdf", SHA256: "digest", Status: "READY", CreatedAt: time.Now().UTC()}
	if err := db.Create(&artifact).Error; err != nil {
		t.Fatal(err)
	}
	client := &presigningClient{}
	query, err := Setup(Dependencies{DB: db, Store: objectstore.Store{Bucket: "private", Client: client}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := query(context.Background(), Input{TenantID: tenant, SnapshotID: snapshot})
	if err != nil || result.Artifact.ObjectKey != "" || result.Artifact.ID != artifact.ID || result.Artifact.SHA256 != "digest" || !strings.Contains(result.URL, "tenant/private/report.pdf") || client.ttl != 5*time.Minute || client.key != "tenant/private/report.pdf" {
		t.Fatalf("result = %+v ttl=%s err=%v", result, client.ttl, err)
	}
	for name, in := range map[string]Input{"other tenant": {TenantID: identity.NewID(), SnapshotID: snapshot}, "unknown snapshot": {TenantID: tenant, SnapshotID: identity.NewID()}} {
		if got, err := query(context.Background(), in); !errors.Is(err, gorm.ErrRecordNotFound) || got.URL != "" {
			t.Fatalf("%s returned %+v (%v)", name, got, err)
		}
	}
}
