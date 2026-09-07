package objectstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/jpeg"
	"io"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
)

type clientStub struct {
	key, upload, content string
	part                 int
	ttl                  time.Duration
	body                 []byte
	head                 Head
	err                  error
	completeErr          error
}

func (c *clientStub) CreateMultipart(_ context.Context, _, key, content string) (string, error) {
	c.key = key
	c.content = content
	return "upload-1", c.err
}
func (c *clientStub) PresignPart(_ context.Context, _, key, upload string, part int, ttl time.Duration) (string, error) {
	c.key = key
	c.upload = upload
	c.part = part
	c.ttl = ttl
	return "https://signed.invalid", c.err
}
func (c *clientStub) CompleteMultipart(context.Context, string, string, string, []Part) error {
	if c.completeErr != nil {
		return c.completeErr
	}
	return c.err
}
func (c *clientStub) AbortMultipart(context.Context, string, string, string) error { return c.err }
func (c *clientStub) Head(context.Context, string, string) (Head, error)           { return c.head, c.err }
func (c *clientStub) Get(context.Context, string, string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(c.body)), c.err
}
func (c *clientStub) Put(context.Context, string, string, io.Reader, int64, string) error {
	return c.err
}
func TestUT007ExactMultipartMapping(t *testing.T) {
	client := &clientStub{}
	store := Store{Bucket: "private", Client: client, Clock: func() time.Time { return time.Unix(0, 0) }}
	key, upload, err := store.CreateMultipart(context.Background(), identity.NewID(), identity.NewID(), "image/jpeg")
	if err != nil || upload != "upload-1" || !strings.Contains(key, "/original/") {
		t.Fatalf("create: %s %s %v", key, upload, err)
	}
	signed, err := store.PresignPart(context.Background(), key, upload, 2, time.Minute)
	if err != nil || client.part != 2 || client.ttl != time.Minute || signed.ExpiresAt != time.Unix(60, 0) {
		t.Fatalf("presign mismatch: %+v %v", signed, err)
	}
}
func TestUT008StableErrors(t *testing.T) {
	for _, expected := range []error{ErrNotFound, ErrExpired, ErrChecksum, ErrDenied} {
		if !errors.Is(mapError(expected), expected) {
			t.Fatalf("lost stable error %v", expected)
		}
	}
	if !strings.Contains(mapError(context.DeadlineExceeded).Error(), "dependency unavailable") {
		t.Fatal("timeout not mapped")
	}
}
func TestIT363AndIT366Verification(t *testing.T) {
	body := validJPEG(t)
	sum := sha256.Sum256(body)
	client := &clientStub{body: body, head: Head{Size: int64(len(body)), ContentType: "image/jpeg", ChecksumSHA256: hex.EncodeToString(sum[:])}}
	store := Store{Bucket: "private", Client: client}
	if err := store.Complete(context.Background(), "key", "upload", []Part{{Number: 1, ETag: "etag"}}, int64(len(body)), "image/jpeg", hex.EncodeToString(sum[:])); err != nil {
		t.Fatal(err)
	}
	if err := store.Complete(context.Background(), "key", "upload", []Part{{Number: 1, ETag: "etag"}}, int64(len(body)), "image/png", hex.EncodeToString(sum[:])); !errors.Is(err, ErrInvalid) {
		t.Fatalf("spoof accepted: %v", err)
	}
}

func TestUT033RejectsInvalidPartsHashMIMEAndDecode(t *testing.T) {
	body := validJPEG(t)
	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])
	valid := &clientStub{body: body, head: Head{Size: int64(len(body)), ContentType: "image/jpeg", ChecksumSHA256: hash}}
	store := Store{Bucket: "private", Client: valid}
	for name, parts := range map[string][]Part{
		"missing":    nil,
		"unordered":  {{Number: 2, ETag: "etag"}},
		"blank etag": {{Number: 1}},
	} {
		t.Run(name, func(t *testing.T) {
			if !errors.Is(store.Complete(context.Background(), "key", "upload", parts, int64(len(body)), "image/jpeg", hash), ErrInvalid) {
				t.Fatal("invalid parts accepted")
			}
		})
	}
	if err := store.Complete(context.Background(), "key", "upload", []Part{{Number: 1, ETag: "etag"}}, int64(len(body)), "image/jpeg", strings.Repeat("0", 64)); !errors.Is(err, ErrChecksum) {
		t.Fatalf("invalid hash accepted: %v", err)
	}
	invalidDecode := &clientStub{body: []byte("not-an-image"), head: Head{Size: 12, ContentType: "image/jpeg"}}
	if err := (Store{Bucket: "private", Client: invalidDecode}).Complete(context.Background(), "key", "upload", []Part{{Number: 1, ETag: "etag"}}, 12, "image/jpeg", strings.Repeat("0", 64)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid decode accepted: %v", err)
	}
	if !sameMediaType("image/heif", "image/heic") {
		t.Fatal("HEIF and HEIC aliases must verify consistently")
	}
}

func validJPEG(t *testing.T) []byte {
	t.Helper()
	var body bytes.Buffer
	if err := jpeg.Encode(&body, image.NewRGBA(image.Rect(0, 0, 8, 8)), nil); err != nil {
		t.Fatal(err)
	}
	return body.Bytes()
}

func TestIT176CompletedMultipartRetryVerifiesImmutableObject(t *testing.T) {
	body := validJPEG(t)
	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])
	client := &clientStub{body: body, head: Head{Size: int64(len(body)), ContentType: "image/jpeg"}, completeErr: ErrNotFound}
	store := Store{Bucket: "private", Client: client}
	parts := []Part{{Number: 1, ETag: "etag"}}
	if err := store.Complete(context.Background(), "key", "completed-upload", parts, int64(len(body)), "image/jpeg", hash); err != nil {
		t.Fatalf("committed upload could not resume: %v", err)
	}
	if err := store.Complete(context.Background(), "key", "completed-upload", parts, int64(len(body)), "image/jpeg", strings.Repeat("0", 64)); !errors.Is(err, ErrChecksum) {
		t.Fatalf("resume skipped checksum: %v", err)
	}
	client.body = []byte{0xff, 0xd8, 0xff, 0xdb, 0x00, 0x01}
	client.head.Size = int64(len(client.body))
	sum = sha256.Sum256(client.body)
	if err := store.Complete(context.Background(), "key", "completed-upload", parts, int64(len(client.body)), "image/jpeg", hex.EncodeToString(sum[:])); !errors.Is(err, ErrInvalid) {
		t.Fatalf("truncated image accepted by magic bytes: %v", err)
	}
}
func TestIT365TenantKeysArePartitionedAndOpaque(t *testing.T) {
	client := &clientStub{}
	store := Store{Bucket: "private", Client: client}
	a, _, _ := store.CreateMultipart(context.Background(), identity.NewID(), identity.NewID(), "image/jpeg")
	b, _, _ := store.CreateMultipart(context.Background(), identity.NewID(), identity.NewID(), "image/jpeg")
	if a == b || !strings.HasPrefix(a, "tenant/") {
		t.Fatal("keys not isolated")
	}
}

func TestIT364ExpiredPartCanBeRefreshed(t *testing.T) {
	now := time.Unix(1000, 0).UTC()
	client := &clientStub{}
	store := Store{Bucket: "private", Client: client, Clock: func() time.Time { return now }}
	if !errors.Is(mapError(ErrExpired), ErrExpired) {
		t.Fatal("expired part URL did not retain its stable error")
	}
	refreshed, err := store.PresignPart(context.Background(), "tenant/key", "upload", 3, time.Minute)
	if err != nil || refreshed.PartNumber != 3 || refreshed.ExpiresAt != now.Add(time.Minute) {
		t.Fatalf("refreshed part URL: %+v %v", refreshed, err)
	}
}

type catalogStub struct {
	uploads []OrphanUpload
	aborted int
}

func (c *catalogStub) CleanupCandidates(context.Context, time.Time) ([]OrphanUpload, error) {
	return c.uploads, nil
}
func (c *catalogStub) MarkAborted(context.Context, OrphanUpload) error { c.aborted++; return nil }
func TestIT368CleanupRequiresReconciliation(t *testing.T) {
	now := time.Now()
	catalog := &catalogStub{uploads: []OrphanUpload{{Key: "active", UploadID: "1", ExpiresAt: now.Add(-time.Hour), Reconciled: true, Active: true}, {Key: "unreconciled", UploadID: "2", ExpiresAt: now.Add(-time.Hour)}, {Key: "orphan", UploadID: "3", ExpiresAt: now.Add(-time.Hour), Reconciled: true}}}
	count, err := (Store{Bucket: "private", Client: &clientStub{}, Clock: func() time.Time { return now }}).Cleanup(context.Background(), catalog)
	if err != nil || count != 1 || catalog.aborted != 1 {
		t.Fatalf("cleanup=%d marked=%d err=%v", count, catalog.aborted, err)
	}
}
