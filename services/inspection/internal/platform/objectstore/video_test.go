package objectstore

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

type rangeClient struct {
	*clientStub
	truncate bool
}

func (c *rangeClient) GetRange(_ context.Context, _, _ string, start, end int64) (io.ReadCloser, error) {
	last := end + 1
	if c.truncate {
		last = start + 5
	}
	return io.NopCloser(bytes.NewReader(c.body[start:last])), nil
}

func TestVideoStoreOpenRangeUT084(t *testing.T) {
	client := &rangeClient{clientStub: &clientStub{body: []byte("0123456789abcdefghij")}}
	reader, err := (VideoStore{Store: Store{Bucket: "private", Client: client}}).OpenRange(context.Background(), "opaque", 10, 19)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "abcdefghij" || len(got) != 10 {
		t.Fatalf("unexpected range %q", got)
	}
}

func TestVideoStoreShortRangeReturnsIncompleteReadUT085(t *testing.T) {
	client := &rangeClient{clientStub: &clientStub{body: []byte("0123456789abcdefghij")}, truncate: true}
	reader, err := (VideoStore{Store: Store{Bucket: "private", Client: client}}).OpenRange(context.Background(), "opaque", 10, 19)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	got, err := io.ReadAll(reader)
	if !errors.Is(err, io.ErrUnexpectedEOF) || string(got) != "abcde" {
		t.Fatalf("short range looked complete: %q, %v", got, err)
	}
}

func TestCompleteVideoReceiptChecksHeadWithoutReadingContent(t *testing.T) {
	const size = int64(10 * PartSizeBytes)
	parts := make([]Part, ExpectedParts(size))
	for i := range parts {
		parts[i] = Part{Number: i + 1, ETag: "etag"}
	}
	client := &clientStub{head: Head{Size: size, ContentType: "video/mp4"}}
	store := Store{Bucket: "private", Client: client}
	if err := store.CompleteVideoReceipt(context.Background(), "key", "upload", parts, size, "video/mp4", strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	client.head.Size = size - 1
	if err := store.CompleteVideoReceipt(context.Background(), "key", "upload", parts, size, "video/mp4", strings.Repeat("a", 64)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("mismatched object size accepted: %v", err)
	}
}
