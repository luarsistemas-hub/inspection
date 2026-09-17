package resolvers

import (
	"context"
	"io"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"
	graphql1 "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/objectstore"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestHydrateReportMediaSignsAvailableDisplayDerivative(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS media").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TABLE media.derivatives (id blob primary key, tenant_id blob not null, media_id blob not null, object_key text not null, kind text not null, sha256 text not null, created_at datetime)").Error; err != nil {
		t.Fatal(err)
	}

	tenantID, mediaID := identity.NewID(), identity.NewID()
	if err := db.Create(&database.MediaDerivative{ID: identity.NewID(), TenantID: tenantID, MediaID: mediaID, ObjectKey: "tenant/display.jpg", Kind: "DISPLAY", SHA256: "digest"}).Error; err != nil {
		t.Fatal(err)
	}
	report := &graphql1.Report{Evidence: []*graphql1.ReportEvidence{{ID: mediaID.String(), Availability: "AVAILABLE"}}}
	store := objectstore.Store{Bucket: "private", Client: reportMediaClient{}}

	if err := hydrateReportMedia(context.Background(), db, store, tenantID, report); err != nil {
		t.Fatal(err)
	}
	if report.Evidence[0].Availability != "AVAILABLE" || report.Evidence[0].URL == nil || *report.Evidence[0].URL != "https://signed.invalid/display.jpg" {
		t.Fatalf("media was not hydrated: %+v", report.Evidence[0])
	}
}

type reportMediaClient struct{}

func (reportMediaClient) CreateMultipart(context.Context, string, string, string) (string, error) {
	return "", nil
}
func (reportMediaClient) PresignPart(context.Context, string, string, string, int, time.Duration) (string, error) {
	return "", nil
}
func (reportMediaClient) CompleteMultipart(context.Context, string, string, string, []objectstore.Part) error {
	return nil
}
func (reportMediaClient) AbortMultipart(context.Context, string, string, string) error { return nil }
func (reportMediaClient) Head(context.Context, string, string) (objectstore.Head, error) {
	return objectstore.Head{}, nil
}
func (reportMediaClient) Get(context.Context, string, string) (io.ReadCloser, error) {
	return io.NopCloser(nilReader{}), nil
}
func (reportMediaClient) Put(context.Context, string, string, io.Reader, int64, string) error {
	return nil
}
func (reportMediaClient) PresignGet(_ context.Context, _ string, key string, _ time.Duration) (string, error) {
	return "https://signed.invalid/" + key[len("tenant/"):], nil
}

type nilReader struct{}

func (nilReader) Read([]byte) (int, error) { return 0, io.EOF }
func (nilReader) Close() error             { return nil }
