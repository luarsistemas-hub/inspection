package process_verified

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/jpeg"
	"io"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/sensitivecontent"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type processingClient struct {
	body []byte
	puts int
}

func (c *processingClient) CreateMultipart(context.Context, string, string, string) (string, error) {
	return "upload", nil
}
func (c *processingClient) PresignPart(context.Context, string, string, string, int, time.Duration) (string, error) {
	return "", nil
}
func (c *processingClient) CompleteMultipart(context.Context, string, string, string, []objectstore.Part) error {
	return nil
}
func (c *processingClient) AbortMultipart(context.Context, string, string, string) error { return nil }
func (c *processingClient) Head(context.Context, string, string) (objectstore.Head, error) {
	return objectstore.Head{}, nil
}
func (c *processingClient) Get(context.Context, string, string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(c.body)), nil
}
func (c *processingClient) Put(_ context.Context, _ string, _ string, reader io.Reader, _ int64, _ string) error {
	c.puts++
	_, _ = io.ReadAll(reader)
	return nil
}

func TestIT192IT196IT200VerifiedConsumerCreatesPrivateDerivativesAndResumes(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"media", "capture", "messaging"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, model := range []any{&database.MediaObject{}, &database.CaptureDraft{}, &database.MediaDerivative{}, &database.ScreeningRun{}, &database.OutboxIntent{}} {
		if err := db.AutoMigrate(model); err != nil && !strings.Contains(err.Error(), "no such table: main.") {
			t.Fatal(err)
		}
	}
	tenantID, responsibilityID, mediaID := identity.NewID(), identity.NewID(), identity.NewID()
	if err := db.Create(&database.CaptureDraft{ID: identity.NewID(), TenantID: tenantID, ResponsibilityID: responsibilityID, Kind: "INSPECTION", Requirements: json.RawMessage(`[]`), ReferencePayload: json.RawMessage(`{}`), PolicyPayload: json.RawMessage(`{}`), Status: "OPEN", Version: 1}).Error; err != nil {
		t.Fatal(err)
	}
	body := blackJPEG(t)
	if err := db.Create(&database.MediaObject{ID: mediaID, TenantID: tenantID, ResponsibilityID: responsibilityID, ObjectKey: "private/original", ContentType: "image/jpeg", SHA256: "hash", SizeBytes: int64(len(body)), Status: "VERIFIED", Flags: json.RawMessage(`[]`)}).Error; err != nil {
		t.Fatal(err)
	}
	client := &processingClient{body: body}
	handler, err := Setup(Dependencies{Store: objectstore.Store{Bucket: "private", Client: client}, Detector: sensitivecontent.NewEmbeddedDetector()})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"mediaId": mediaID})
	envelope := events.RawEnvelope{ID: identity.NewID(), Type: "media.verified.v1", SchemaVersion: 1, OccurredAt: time.Now().UTC(), TenantID: tenantID, AggregateID: mediaID, CorrelationID: "process-test", Payload: payload}
	for range 2 {
		if err := handler(context.Background(), db, envelope); err != nil {
			t.Fatal(err)
		}
	}
	var derivatives []database.MediaDerivative
	if err := db.Where("tenant_id=? AND media_id=?", tenantID, mediaID).Find(&derivatives).Error; err != nil || len(derivatives) != 2 {
		t.Fatalf("derivatives=%d err=%v", len(derivatives), err)
	}
	var run database.ScreeningRun
	if err := db.Where("tenant_id=? AND media_id=?", tenantID, mediaID).First(&run).Error; err != nil || run.Status != "CLEARED" {
		t.Fatalf("screening=%+v err=%v", run, err)
	}
	var media database.MediaObject
	if err := db.First(&media, "id=?", mediaID).Error; err != nil || media.Status != "READY" {
		t.Fatalf("media not ready: %+v %v", media, err)
	}
	if client.puts != 2 {
		t.Fatalf("duplicate event rewrote derivatives %d times", client.puts)
	}
}

func blackJPEG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 16, 16)), nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
