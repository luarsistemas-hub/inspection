package objectstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"image"
	"image/jpeg"
	"io"
	"math"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
)

func TestPublicSignedURLChangesOnlyOrigin(t *testing.T) {
	signed, err := url.Parse("https://namespace.compat.objectstorage.us-ashburn-1.oraclecloud.com/inspection-private/a%2Fb?X-Amz-Signature=abc&uploadId=opaque")
	if err != nil {
		t.Fatal(err)
	}
	client := &MinIOClient{PublicBaseURL: "https://storage.example.com"}
	got, err := url.Parse(client.publicURL(signed))
	if err != nil {
		t.Fatal(err)
	}
	if got.Scheme != "https" || got.Host != "storage.example.com" || got.EscapedPath() != "/inspection-private/a%2Fb" || got.RawQuery != "X-Amz-Signature=abc&uploadId=opaque" {
		t.Fatalf("rewritten signed URL lost signed request details: %s", got)
	}
}

type clientStub struct {
	key, upload, content string
	part                 int
	ttl                  time.Duration
	body                 []byte
	head                 Head
	err                  error
	completeErr          error
	deleteErr            error
	completedParts       []Part
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
func (c *clientStub) CompleteMultipart(_ context.Context, _, _, _ string, parts []Part) error {
	c.completedParts = append([]Part(nil), parts...)
	if c.completeErr != nil {
		return c.completeErr
	}
	return c.err
}
func (*clientStub) ListMultipartParts(context.Context, string, string, string) ([]UploadedPart, error) {
	return []UploadedPart{{Number: 1, ETag: "provider-md5", SizeBytes: 4}}, nil
}
func (c *clientStub) AbortMultipart(context.Context, string, string, string) error { return c.err }
func (c *clientStub) Head(context.Context, string, string) (Head, error)           { return c.head, c.err }
func (c *clientStub) Get(context.Context, string, string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(c.body)), c.err
}
func (c *clientStub) Put(_ context.Context, _, key string, body io.Reader, _ int64, contentType string) error {
	c.key, c.content = key, contentType
	c.body, _ = io.ReadAll(body)
	return c.err
}
func (c *clientStub) Delete(context.Context, string, string) error { return c.deleteErr }

func TestUT013DeleteMissingObjectIsIdempotent(t *testing.T) {
	client := &clientStub{deleteErr: ErrNotFound}
	store := Store{Bucket: "private", Client: client}
	if err := store.Delete(context.Background(), "audio/note"); err != nil {
		t.Fatalf("deleting a missing object should succeed: %v", err)
	}
}

type abortTrackingClient struct {
	clientStub
	abortErr error
	aborted  []string
	deleted  []string
}

func (c *abortTrackingClient) AbortMultipart(_ context.Context, _, key, uploadID string) error {
	c.aborted = append(c.aborted, key+":"+uploadID)
	return c.abortErr
}

func (c *abortTrackingClient) Delete(_ context.Context, _, key string) error {
	c.deleted = append(c.deleted, key)
	return c.deleteErr
}

func TestUT013DeleteUploadAbortsPendingMultipartAndDeletesObject(t *testing.T) {
	cases := []struct {
		name      string
		uploadID  string
		abortErr  error
		deleteErr error
		aborted   int
	}{
		{name: "pending upload", uploadID: "upload-1", aborted: 1},
		{name: "upload already completed or aborted", uploadID: "upload-1", abortErr: ErrNotFound, aborted: 1},
		{name: "object already removed", uploadID: "upload-1", abortErr: ErrNotFound, deleteErr: ErrNotFound, aborted: 1},
		{name: "no multipart upload", aborted: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := &abortTrackingClient{abortErr: tc.abortErr}
			client.deleteErr = tc.deleteErr
			store := Store{Bucket: "private", Client: client}
			if err := store.DeleteUpload(context.Background(), "audio/note", tc.uploadID); err != nil {
				t.Fatalf("DeleteUpload() error = %v", err)
			}
			if len(client.aborted) != tc.aborted || len(client.deleted) != 1 || client.deleted[0] != "audio/note" {
				t.Fatalf("aborted=%v deleted=%v", client.aborted, client.deleted)
			}
		})
	}
}

func TestUT014DeleteUploadKeepsObjectWhenAbortFails(t *testing.T) {
	client := &abortTrackingClient{abortErr: ErrDenied}
	store := Store{Bucket: "private", Client: client}
	if err := store.DeleteUpload(context.Background(), "audio/note", "upload-1"); !errors.Is(err, ErrDenied) {
		t.Fatalf("DeleteUpload() error = %v, want ErrDenied", err)
	}
	if len(client.deleted) != 0 {
		t.Fatalf("object deleted while multipart abort failed: %v", client.deleted)
	}
	if err := (Store{Bucket: "private", Client: client}).DeleteUpload(context.Background(), "", "upload-1"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty key error = %v, want ErrInvalid", err)
	}
}

func TestDeletePreservesStorageErrors(t *testing.T) {
	client := &clientStub{deleteErr: ErrDenied}
	store := Store{Bucket: "private", Client: client}
	if err := store.Delete(context.Background(), "audio/note"); !errors.Is(err, ErrDenied) {
		t.Fatalf("delete error=%v, want ErrDenied", err)
	}
}

func TestPutOriginalValidatesImageBeforePrivateStorage(t *testing.T) {
	client := &clientStub{}
	store := Store{Bucket: "private", Client: client}
	body := validJPEG(t)
	key, hash, err := store.PutOriginal(context.Background(), identity.NewID(), identity.NewID(), "image/jpeg", body)
	if err != nil || !strings.Contains(key, "/original/") || hash == "" || !bytes.Equal(client.body, body) {
		t.Fatalf("original was not stored: %q %q %v", key, hash, err)
	}
	client.body = nil
	if _, _, err := store.PutOriginal(context.Background(), identity.NewID(), identity.NewID(), "image/png", body); !errors.Is(err, ErrInvalid) || client.body != nil {
		t.Fatalf("mismatched image type reached storage: %v", err)
	}
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
	if err := mapError(nil); err != nil {
		t.Fatalf("successful storage operation mapped to error: %v", err)
	}
	for _, expected := range []error{ErrNotFound, ErrExpired, ErrChecksum, ErrDenied} {
		if !errors.Is(mapError(expected), expected) {
			t.Fatalf("lost stable error %v", expected)
		}
	}
	if !strings.Contains(mapError(context.DeadlineExceeded).Error(), "dependency unavailable") {
		t.Fatal("timeout not mapped")
	}
}

func TestUT004CompleteAudioRejectsDigestMismatchBeforeProbing(t *testing.T) {
	probeCalled := false
	client := &clientStub{body: []byte{0x1a, 0x45, 0xdf, 0xa3}, head: Head{Size: 4, ContentType: "audio/webm"}}
	store := Store{Bucket: "private", Client: client, AudioProbe: func(context.Context, []byte) (time.Duration, error) {
		probeCalled = true
		return time.Second, nil
	}}
	err := store.CompleteAudio(context.Background(), "note-key", "upload-1", "proof", 4, 1_000, "audio/webm", strings.Repeat("0", 64))
	if !errors.Is(err, ErrChecksum) || probeCalled {
		t.Fatalf("digest mismatch should fail before audio probing: err=%v probeCalled=%t", err, probeCalled)
	}
}

func TestCompleteAudioAcceptsVerifiedBytesAndDuration(t *testing.T) {
	body := []byte{0x1a, 0x45, 0xdf, 0xa3}
	digest := sha256.Sum256(body)
	client := &clientStub{body: body, head: Head{Size: int64(len(body)), ContentType: "audio/webm", ChecksumSHA256: hex.EncodeToString(digest[:])}}
	store := Store{Bucket: "private", Client: client, AudioProbe: func(context.Context, []byte) (time.Duration, error) { return time.Second, nil }}
	if err := store.CompleteAudio(context.Background(), "note-key", "upload-1", "proof", int64(len(body)), 1_000, "audio/webm", hex.EncodeToString(digest[:])); err != nil {
		t.Fatalf("verified audio rejected: %v", err)
	}
	if len(client.completedParts) != 1 || client.completedParts[0].ETag != "provider-md5" {
		t.Fatalf("multipart completion used client proof instead of provider ETag: %+v", client.completedParts)
	}
}

func TestCompleteAudioProbeTimeoutIsInternalFailure(t *testing.T) {
	body := []byte{0x1a, 0x45, 0xdf, 0xa3}
	digest := sha256.Sum256(body)
	client := &clientStub{body: body, head: Head{Size: int64(len(body)), ContentType: "audio/webm"}}
	store := Store{Bucket: "private", Client: client, AudioProbe: func(context.Context, []byte) (time.Duration, error) {
		return 0, errors.Join(errAudioProbeUnavailable, context.DeadlineExceeded)
	}}
	if err := store.CompleteAudio(context.Background(), "note-key", "upload-1", "proof", int64(len(body)), 1_000, "audio/webm", hex.EncodeToString(digest[:])); !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrInvalid) {
		t.Fatalf("probe timeout was classified as invalid audio: %v", err)
	}
}

func TestPacketDurationUsesFormatStartTime(t *testing.T) {
	packets := []struct {
		PTS      string `json:"pts_time"`
		Duration string `json:"duration_time"`
	}{{PTS: "12.25", Duration: "0.50"}, {PTS: "12.75", Duration: "0.50"}}
	duration, err := packetDurationFromStart(packets, 10)
	if err != nil || duration != 3250*time.Millisecond {
		t.Fatalf("absolute duration=%s err=%v; want 3.25s from format start", duration, err)
	}
}

func TestProbeAudioDurationWithoutContainerDuration(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is unavailable")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe is unavailable")
	}
	tests := []struct {
		name string
		args []string
	}{
		{name: "WebM stream", args: []string{"-c:a", "libopus", "-f", "webm", "pipe:1"}},
		{name: "fragmented MP4", args: []string{"-c:a", "aac", "-movflags", "frag_keyframe+empty_moov", "-f", "mp4", "pipe:1"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			args := []string{"-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=1000:duration=1"}
			args = append(args, test.args...)
			data, err := exec.Command("ffmpeg", args...).Output()
			if err != nil {
				t.Fatalf("generate audio fixture: %v", err)
			}
			duration, err := probeAudioDuration(context.Background(), data)
			if err != nil {
				t.Fatalf("probe generated audio: %v", err)
			}
			if duration < 500*time.Millisecond || duration > 1500*time.Millisecond {
				t.Fatalf("duration=%s; want approximately one second", duration)
			}
		})
	}
}

func TestProbeAudioDurationRejectsTamperedWebMAndNormalizesShiftedPTS(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is unavailable")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe is unavailable")
	}
	fixture, err := os.CreateTemp(t.TempDir(), "tampered-*.webm")
	if err != nil {
		t.Fatal(err)
	}
	fixturePath := fixture.Name()
	_ = fixture.Close()
	if err := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=1000:duration=180", "-c:a", "libopus", "-f", "webm", fixturePath, "-y").Run(); err != nil {
		t.Fatalf("generate WebM fixture: %v", err)
	}
	data, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read WebM fixture: %v", err)
	}
	mutated := append([]byte(nil), data...)
	tag := bytes.Index(mutated, []byte{0x44, 0x89})
	if tag < 0 || tag+11 > len(mutated) || mutated[tag+2] != 0x88 {
		t.Fatal("WebM Duration float64 tag not found")
	}
	binary.BigEndian.PutUint64(mutated[tag+3:tag+11], math.Float64bits(30))
	if _, err := probeAudioDuration(context.Background(), mutated); err == nil {
		t.Fatal("accepted WebM whose header says 30 s but packets span 180 s")
	}

	shifted, err := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "sine=frequency=1000:duration=1", "-output_ts_offset", "5", "-c:a", "libopus", "-f", "webm", "pipe:1").Output()
	if err != nil {
		t.Fatalf("generate shifted-PTS fixture: %v", err)
	}
	duration, err := probeAudioDuration(context.Background(), shifted)
	if err != nil {
		t.Fatalf("probe shifted-PTS fixture: %v", err)
	}
	if duration < 500*time.Millisecond || duration > 1500*time.Millisecond {
		t.Fatalf("shifted PTS duration=%s; want approximately one second", duration)
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
