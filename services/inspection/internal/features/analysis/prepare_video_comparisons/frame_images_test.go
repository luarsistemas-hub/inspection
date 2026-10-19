package prepare_video_comparisons

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/objectstore"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type frameObjectClient struct {
	data []byte
}

func (c frameObjectClient) CreateMultipart(context.Context, string, string, string) (string, error) {
	return "", nil
}
func (c frameObjectClient) PresignPart(context.Context, string, string, string, int, time.Duration) (string, error) {
	return "", nil
}
func (c frameObjectClient) CompleteMultipart(context.Context, string, string, string, []objectstore.Part) error {
	return nil
}
func (c frameObjectClient) AbortMultipart(context.Context, string, string, string) error { return nil }
func (c frameObjectClient) Head(context.Context, string, string) (objectstore.Head, error) {
	checksum := sha256.Sum256(c.data)
	return objectstore.Head{Size: int64(len(c.data)), ContentType: "image/jpeg", ChecksumSHA256: hex.EncodeToString(checksum[:])}, nil
}
func (c frameObjectClient) Get(context.Context, string, string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(c.data)), nil
}
func (frameObjectClient) Put(context.Context, string, string, io.Reader, int64, string) error {
	return nil
}

func TestStoreFrameImagesChecksChecksumAndNormalizesJPEG(t *testing.T) {
	var source bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 1000, 500))
	for y := 0; y < 500; y++ {
		for x := 0; x < 1000; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 255), G: 60, B: 120, A: 255})
		}
	}
	if err := jpeg.Encode(&source, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	data := source.Bytes()
	hash := sha256.Sum256(data)
	frame := Frame{ID: testID("image-frame"), TenantID: testID("image-tenant"), PreparationID: testID("image-prep"), Digest: hex.EncodeToString(hash[:]), PTS: 987654}
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS media").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TABLE media.video_frames (id TEXT, tenant_id TEXT, preparation_id TEXT, sha256 TEXT, object_key TEXT, size_bytes INTEGER)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO media.video_frames VALUES (?, ?, ?, ?, ?, ?)", frame.ID.String(), frame.TenantID.String(), frame.PreparationID.String(), frame.Digest, "frames/object", len(data)).Error; err != nil {
		t.Fatal(err)
	}
	within := func(ctx context.Context, _ identity.ID, fn func(*gorm.DB) error) error {
		return db.WithContext(ctx).Transaction(fn)
	}
	loader := StoreFrameImages{DB: db, Store: objectstore.Store{Bucket: "frames", Client: frameObjectClient{data: data}}, Within: within}
	images, err := loader.Images(context.Background(), []Frame{frame})
	if err != nil || len(images) != 1 {
		t.Fatalf("load normalized frame: images=%d err=%v", len(images), err)
	}
	if images[0].EvidenceID != frame.ID.String() || images[0].Source != "VIDEO_FRAME" || images[0].Position != "987654" || images[0].Digest != frame.Digest {
		t.Fatalf("image lost frame lineage: %+v", images[0])
	}
	encoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(images[0].DataURL, "data:image/jpeg;base64,"))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := jpeg.Decode(bytes.NewReader(encoded))
	if err != nil || decoded.Bounds().Dx() != 768 || decoded.Bounds().Dy() != 384 {
		t.Fatalf("frame was not resized/re-encoded at 768px: bounds=%v err=%v", decoded.Bounds(), err)
	}
	badHash := frame
	badHash.Digest = "0000000000000000000000000000000000000000000000000000000000000000"
	if _, err := loader.Images(context.Background(), []Frame{badHash}); !errors.Is(err, ErrInvalidPlan) {
		t.Fatalf("mismatched record digest should be rejected: %v", err)
	}
	corrupt := append([]byte(nil), data...)
	corrupt[len(corrupt)/2] ^= 0xff
	corruptLoader := StoreFrameImages{DB: db, Store: objectstore.Store{Bucket: "frames", Client: frameObjectClient{data: corrupt}}, Within: within}
	if _, err := corruptLoader.Images(context.Background(), []Frame{frame}); !errors.Is(err, objectstore.ErrChecksum) {
		t.Fatalf("object bytes that differ from the registered frame digest should fail: %v", err)
	}
}
