package process_note

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"

	"inspection/services/inspection/internal/platform/objectstore"
)

// NewAudioStore adapts the private object store to process_note.AudioStore.
func NewAudioStore(store objectstore.Store) (AudioStore, error) {
	if store.Client == nil || strings.TrimSpace(store.Bucket) == "" {
		return nil, errors.New("process_note: missing audio object store")
	}
	return audioStore{store: store}, nil
}

type audioStore struct{ store objectstore.Store }

func (s audioStore) Open(ctx context.Context, key string) (io.ReadSeekCloser, error) {
	if key == "" {
		return nil, objectstore.ErrInvalid
	}
	r, err := s.store.Client.Get(ctx, s.store.Bucket, key)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	data, err := io.ReadAll(io.LimitReader(r, 5*1024*1024+1))
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || len(data) > 5*1024*1024 {
		return nil, objectstore.ErrInvalid
	}
	return &seekReadCloser{Reader: bytes.NewReader(data)}, nil
}

type seekReadCloser struct{ *bytes.Reader }

func (*seekReadCloser) Close() error { return nil }
