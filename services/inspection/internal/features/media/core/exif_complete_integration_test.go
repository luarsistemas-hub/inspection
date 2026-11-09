//go:build integration

package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/objectstore"

	"gorm.io/gorm"
)

type exifMultipartClient struct {
	data        []byte
	contentType string
}

func (c exifMultipartClient) CreateMultipart(context.Context, string, string, string) (string, error) {
	return "", nil
}
func (c exifMultipartClient) PresignPart(context.Context, string, string, string, int, time.Duration) (string, error) {
	return "", nil
}
func (c exifMultipartClient) CompleteMultipart(context.Context, string, string, string, []objectstore.Part) error {
	return nil
}
func (c exifMultipartClient) AbortMultipart(context.Context, string, string, string) error {
	return nil
}
func (c exifMultipartClient) Head(context.Context, string, string) (objectstore.Head, error) {
	sum := sha256.Sum256(c.data)
	return objectstore.Head{Size: int64(len(c.data)), ContentType: c.contentType, ChecksumSHA256: hex.EncodeToString(sum[:])}, nil
}
func (c exifMultipartClient) Get(context.Context, string, string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(c.data)), nil
}
func (c exifMultipartClient) Put(context.Context, string, string, io.Reader, int64, string) error {
	return nil
}

func TestIT026CompleteExifThenSaveReservedMetadata(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	fixture, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{SchemaVersion: 3, RequirementKey: "env:kitchen", CaptureDraftStatus: "OPEN", CapturePolicyPayload: []byte(`{"allowGallery":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	data := jpegWithExifTime(t, time.Now().UTC().Add(-2*time.Hour).Format("2006:01:02 15:04:05"))
	sum := sha256.Sum256(data)
	now := time.Now().UTC()
	media := database.MediaObject{ID: identity.NewID(), TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, ObjectKey: "it026-complete/" + identity.NewID().String(), IdempotencyKey: "it026-complete-" + identity.NewID().String(), ContentType: "image/jpeg", SHA256: hex.EncodeToString(sum[:]), SizeBytes: int64(len(data)), Status: "UPLOADING", MediaKind: "PHOTO", RequirementKey: "env:kitchen", Flags: []byte("[]"), AttentionItems: []byte("[]"), CreatedAt: now}
	upload := database.MultipartUpload{ID: identity.NewID(), TenantID: fixture.TenantID, MediaID: media.ID, UploadID: "it026-upload-" + media.ID.String(), ObjectKey: media.ObjectKey, Status: "ACTIVE", ExpiresAt: now.Add(time.Hour), CreatedAt: now}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		if err := tx.Create(&media).Error; err != nil {
			return err
		}
		return tx.Create(&upload).Error
	}); err != nil {
		t.Fatal(err)
	}
	store := objectstore.Store{Client: exifMultipartClient{data: data, contentType: "image/jpeg"}, Bucket: "it"}
	completed, err := (Service{DB: h.DB, Store: store, Within: h.WithinTenant}).Complete(ctx, fixture.TenantID, fixture.ResponsibilityID, media.ID, []objectstore.Part{{Number: 1, ETag: "part"}})
	if err != nil || completed.CapturedAtSource != "EXIF" || completed.CapturedAt == nil || completed.ReceivedAt == nil || completed.LatitudeE6 != nil || completed.LongitudeE6 != nil || !strings.Contains(string(completed.Flags), "CAPTURE_TIME_DEVICE_LOCAL") {
		t.Fatalf("Complete EXIF receipt=%+v err=%v", completed, err)
	}
	// The existing screening pipeline moves VERIFIED media to READY before metadata is attached.
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.MediaObject{}).Where("tenant_id=? AND id=?", fixture.TenantID, media.ID).Update("status", "READY").Error
	}); err != nil {
		t.Fatal(err)
	}
	saved, err := (capturecore.Service{DB: h.DB, Within: h.WithinTenant}).SaveMetadata(ctx, capturecore.MetadataInput{TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, MediaID: media.ID, RequirementKey: "env:kitchen", CaptureSource: "GALLERY"})
	if err != nil || saved.CaptureSource != "GALLERY" || saved.CapturedAtSource != "EXIF" || saved.CapturedAt == nil {
		t.Fatalf("SaveMetadata treated EXIF timestamp as already-saved metadata: %+v err=%v", saved, err)
	}
}

func jpegWithExifTime(t *testing.T, stamp string) []byte {
	t.Helper()
	var base bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.White)
	if err := jpeg.Encode(&base, img, nil); err != nil {
		t.Fatal(err)
	}
	const ifd0, exif = 8, 38
	tiff := make([]byte, 96)
	copy(tiff, "II")
	binary.LittleEndian.PutUint16(tiff[2:], 42)
	binary.LittleEndian.PutUint32(tiff[4:], ifd0)
	binary.LittleEndian.PutUint16(tiff[ifd0:], 2)
	entry := tiff[ifd0+2:]
	binary.LittleEndian.PutUint16(entry, 0x0132)
	binary.LittleEndian.PutUint16(entry[2:], 2)
	binary.LittleEndian.PutUint32(entry[4:], 20)
	binary.LittleEndian.PutUint32(entry[8:], 70)
	entry = tiff[ifd0+14:]
	binary.LittleEndian.PutUint16(entry, 0x8769)
	binary.LittleEndian.PutUint16(entry[2:], 4)
	binary.LittleEndian.PutUint32(entry[4:], 1)
	binary.LittleEndian.PutUint32(entry[8:], exif)
	binary.LittleEndian.PutUint16(tiff[exif:], 1)
	entry = tiff[exif+2:]
	binary.LittleEndian.PutUint16(entry, 0x9003)
	binary.LittleEndian.PutUint16(entry[2:], 2)
	binary.LittleEndian.PutUint32(entry[4:], uint32(len(stamp)+1))
	binary.LittleEndian.PutUint32(entry[8:], 70)
	copy(tiff[70:], stamp+"\x00")
	segment := append([]byte("Exif\x00\x00"), tiff...)
	var result bytes.Buffer
	src := base.Bytes()
	result.Write(src[:2])
	result.Write([]byte{0xff, 0xe1})
	_ = binary.Write(&result, binary.BigEndian, uint16(len(segment)+2))
	result.Write(segment)
	result.Write(src[2:])
	return result.Bytes()
}
