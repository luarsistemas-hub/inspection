package capture_integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/objectstore"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func captureDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"audit", "messaging", "invitations", "origins", "capture", "media", "recapture", "inspections", "templates"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	models := []any{&database.AuditEvent{}, &database.OutboxIntent{}, &database.Invitation{}, &database.ExternalSession{}, &database.ProcessingAcceptance{}, &database.Origin{}, &database.OriginVersion{}, &database.OriginEvidence{}, &database.CaptureDraft{}, &database.RequirementAnswer{}, &database.SubmissionVersion{}, &database.MediaObject{}, &database.MultipartUpload{}, &database.MediaDerivative{}, &database.ScreeningRun{}, &database.RecaptureRequest{}, &database.RecaptureRequirement{}, &database.Inspection{}, &database.Responsibility{}, &database.TemplateVersion{}, &database.VideoSource{}, &database.VideoPreparationJob{}, &database.InventoryEnvironment{}, &database.InventoryItem{}, &database.InventoryItemEvidence{}}
	for _, model := range models {
		if err := db.AutoMigrate(model); err != nil && !strings.Contains(err.Error(), "no such table: main.") {
			t.Fatal(err)
		}
	}
	return db
}

type objectClient struct {
	body        []byte
	contentType string
	checksum    string
}

func (c *objectClient) CreateMultipart(context.Context, string, string, string) (string, error) {
	return identity.NewID().String(), nil
}
func (c *objectClient) PresignPart(context.Context, string, string, string, int, time.Duration) (string, error) {
	return "https://signed.invalid", nil
}
func (c *objectClient) CompleteMultipart(context.Context, string, string, string, []objectstore.Part) error {
	return nil
}
func (c *objectClient) AbortMultipart(context.Context, string, string, string) error { return nil }
func (c *objectClient) Head(context.Context, string, string) (objectstore.Head, error) {
	return objectstore.Head{Size: int64(len(c.body)), ContentType: c.contentType, ChecksumSHA256: c.checksum}, nil
}
func (c *objectClient) Get(context.Context, string, string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(c.body)), nil
}
func (c *objectClient) Put(context.Context, string, string, io.Reader, int64, string) error {
	return nil
}

func jpegBody(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.White)
	var output bytes.Buffer
	if err := jpeg.Encode(&output, img, nil); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func findDraft(t *testing.T, db *gorm.DB, responsibilityID identity.ID) database.CaptureDraft {
	t.Helper()
	var draft database.CaptureDraft
	if err := db.Where("responsibility_id=?", responsibilityID).First(&draft).Error; err != nil {
		t.Fatal(err)
	}
	return draft
}

func mustJSON(value any) json.RawMessage { payload, _ := json.Marshal(value); return payload }

func code(err error) apperror.Code {
	if err == nil {
		return ""
	}
	value, _, _ := apperror.Public(err)
	return value
}
