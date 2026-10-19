package read_video_evidence

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"inspection/libs/identity"
	report "inspection/services/inspection/internal/features/reports/core"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestFrameReadVerifiesObjectBytesBeforeResponse(t *testing.T) {
	data := []byte("retained-jpeg-test-bytes")
	sum := sha256.Sum256(data)
	frame := database.VideoFrame{ObjectKey: "frame.jpg", SHA256: hex.EncodeToString(sum[:]), SizeBytes: int64(len(data))}
	client := &frameRangeClient{data: data}
	store := objectstore.Store{Bucket: "private", Client: client}
	if got, err := verifiedFrameBytes(context.Background(), store, frame); err != nil || !bytes.Equal(got, data) {
		t.Fatalf("verified frame = %q, %v", got, err)
	}
	client.data = append([]byte(nil), data...)
	client.data[0] ^= 0xff
	if _, err := verifiedFrameBytes(context.Background(), store, frame); err == nil {
		t.Fatal("corrupted frame bytes were served")
	}
}

func TestRecaptureReferenceAllowsOnlyPinnedOriginFrameAndMedia(t *testing.T) {
	frameID, sourceID := identity.NewID(), identity.NewID()
	payload, err := json.Marshal(map[string]any{"videoRecapture": map[string]any{"selected": []map[string]any{{"originalMediaId": sourceID, "originFrameIds": []identity.ID{frameID}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if !referenceContainsFrame(payload, frameID, sourceID) {
		t.Fatal("selected origin frame was not authorized for the recapture responsibility")
	}
	if referenceContainsFrame(payload, frameID, identity.NewID()) || referenceContainsFrame(payload, identity.NewID(), sourceID) {
		t.Fatal("recapture authorization accepted a different source or an unselected frame")
	}
}

func TestFrameReadDoesNotConsumePlaybackSlot(t *testing.T) {
	data := []byte("frame")
	sum := sha256.Sum256(data)
	frame := database.VideoFrame{ObjectKey: "frame.jpg", SHA256: hex.EncodeToString(sum[:]), SizeBytes: int64(len(data))}
	client := &frameRangeClient{data: data}
	h := &Handler{deps: Dependencies{Store: objectstore.Store{Bucket: "private", Client: client}, StreamTimeout: time.Second}, slots: make(chan struct{}, 4)}
	for range 4 {
		h.slots <- struct{}{}
	}
	item := access{frame: &frame}
	response := httptest.NewRecorder()
	h.stream(response, httptest.NewRequest(http.MethodGet, "/", nil), item, 0, int64(len(data)-1), false, "image/jpeg", int64(len(data)))
	if response.Code != http.StatusOK || response.Body.String() != string(data) {
		t.Fatalf("frame read status=%d body=%q, want 200 and frame bytes", response.Code, response.Body.String())
	}
	if len(h.slots) != 4 {
		t.Fatalf("frame read changed occupied playback slots to %d", len(h.slots))
	}
}

func TestSourceLeaseDependencyFailureReturns503(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{deps: Dependencies{DB: db, Now: time.Now, StreamTimeout: time.Second}, slots: make(chan struct{}, 4)}
	item := access{metadata: requestctx.Metadata{TenantID: identity.NewID()}, media: database.MediaObject{ID: identity.NewID()}}
	response := httptest.NewRecorder()
	h.stream(response, httptest.NewRequest(http.MethodGet, "/", nil), item, 0, 1, false, "video/mp4", 2)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("database failure status=%d, want 503", response.Code)
	}
}

type frameRangeClient struct {
	testObjectClient
	data []byte
}

func (c *frameRangeClient) GetRange(context.Context, string, string, int64, int64) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(c.data)), nil
}

func TestSnapshotFrameLinkRequiresPinnedDigest(t *testing.T) {
	frame := database.VideoFrame{ID: identity.NewID(), SourceMediaID: identity.NewID(), PreparationID: identity.NewID(), SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	evidence := report.Evidence{ID: frame.ID.String(), FrameID: frame.ID.String(), SourceMediaID: frame.SourceMediaID.String(), PreparationID: frame.PreparationID.String(), FrameDigest: frame.SHA256, DisplayDigest: frame.SHA256}
	encode := func() []byte {
		data, _ := json.Marshal(report.Snapshot{Evidence: []report.Evidence{evidence}})
		return data
	}
	if !snapshotContainsEvidence(encode(), &frame, frame.SourceMediaID) {
		t.Fatal("matching pinned frame denied")
	}
	evidence.DisplayDigest = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if snapshotContainsEvidence(encode(), &frame, frame.SourceMediaID) {
		t.Fatal("tampered display digest authorized")
	}
}

func TestParseRangeInclusiveAndSuffixForms(t *testing.T) {
	for _, test := range []struct {
		value      string
		start, end int64
	}{
		{value: "bytes=10-19", start: 10, end: 19},
		{value: "bytes=-10", start: 90, end: 99},
		{value: "bytes=90-", start: 90, end: 99},
	} {
		got, err := ParseRange(test.value, 100)
		if err != nil || got.Start != test.start || got.End != test.end || got.End-got.Start+1 != 10 {
			t.Errorf("ParseRange(%q) = %+v, %v; want %d-%d with length 10", test.value, got, err, test.start, test.end)
		}
	}
}

func TestParseRangeRejectsUnsupportedOrUnsatisfiableRanges(t *testing.T) {
	for _, value := range []string{"bytes=200-300", "bytes=0-1,5-6", "bytes=8-2", "items=0-1"} {
		if _, err := ParseRange(value, 100); err == nil {
			t.Errorf("ParseRange(%q) unexpectedly succeeded", value)
		}
	}
}

func TestParseRangeRejectsHugeRanges(t *testing.T) {
	if _, err := ParseRange("bytes=0-20000000", 20000001); err == nil {
		t.Fatal("oversized range unexpectedly succeeded")
	}
}

func TestSetupValidatesDependenciesBeforeRouteRegistration(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	base := Dependencies{DB: db, Store: objectstore.Store{Bucket: "b", Client: testObjectClient{}}, Authenticate: func(*http.Request) (requestctx.Metadata, bool) { return requestctx.Metadata{}, true }}
	checks := []struct {
		name string
		mux  *http.ServeMux
		deps Dependencies
	}{
		{name: "router", deps: base},
		{name: "database", mux: http.NewServeMux(), deps: func() Dependencies { d := base; d.DB = nil; return d }()},
		{name: "store", mux: http.NewServeMux(), deps: func() Dependencies { d := base; d.Store.Client = nil; return d }()},
		{name: "bucket", mux: http.NewServeMux(), deps: func() Dependencies { d := base; d.Store.Bucket = ""; return d }()},
		{name: "authenticator", mux: http.NewServeMux(), deps: func() Dependencies { d := base; d.Authenticate = nil; return d }()},
	}
	for _, check := range checks {
		if _, err := Setup(check.mux, check.deps); err == nil {
			t.Errorf("Setup missing %s dependency succeeded", check.name)
		}
	}
}

func TestUnauthorizedFrameIDNeverReadsObjectStoreUT063(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	store := &readGuardClient{}
	mux := http.NewServeMux()
	if _, err := Setup(mux, Dependencies{
		DB: db, Store: objectstore.Store{Bucket: "private", Client: store},
		Authenticate: func(*http.Request) (requestctx.Metadata, bool) { return requestctx.Metadata{}, false },
	}); err != nil {
		t.Fatal(err)
	}
	frameID, responsibilityID := identity.NewID(), identity.NewID()
	request := httptest.NewRequest(http.MethodGet, "/media/video-evidence/"+frameID.String()+"?responsibilityId="+responsibilityID.String(), nil)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized frame request status=%d, want 401", response.Code)
	}
	if store.reads != 0 {
		t.Fatalf("unauthorized frame request made %d object-store reads", store.reads)
	}
}

type readGuardClient struct {
	testObjectClient
	reads int
}

func (c *readGuardClient) Head(context.Context, string, string) (objectstore.Head, error) {
	c.reads++
	return objectstore.Head{}, nil
}

func (c *readGuardClient) Get(context.Context, string, string) (io.ReadCloser, error) {
	c.reads++
	return nil, nil
}

func (c *readGuardClient) GetRange(context.Context, string, string, int64, int64) (io.ReadCloser, error) {
	c.reads++
	return nil, nil
}

type testObjectClient struct{}

func (testObjectClient) CreateMultipart(context.Context, string, string, string) (string, error) {
	return "", nil
}
func (testObjectClient) PresignPart(context.Context, string, string, string, int, time.Duration) (string, error) {
	return "", nil
}
func (testObjectClient) CompleteMultipart(context.Context, string, string, string, []objectstore.Part) error {
	return nil
}
func (testObjectClient) AbortMultipart(context.Context, string, string, string) error { return nil }
func (testObjectClient) Head(context.Context, string, string) (objectstore.Head, error) {
	return objectstore.Head{}, nil
}
func (testObjectClient) Get(context.Context, string, string) (io.ReadCloser, error) { return nil, nil }
func (testObjectClient) Put(context.Context, string, string, io.Reader, int64, string) error {
	return nil
}
