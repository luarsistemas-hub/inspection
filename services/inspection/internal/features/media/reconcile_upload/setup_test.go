package reconcile_upload

import (
	"context"
	"io"
	"testing"
	"time"

	"inspection/services/inspection/internal/features/media/core"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/objectstore"

	"gorm.io/gorm"
)

func TestSetupValidatesDependencies(t *testing.T) {
	if err := Setup(Dependencies{}); err == nil {
		t.Fatal("expected missing dependencies to fail")
	}
	if err := Setup(Dependencies{Bus: mediator.New(), Service: core.Service{DB: &gorm.DB{}, Store: objectstore.Store{Client: reconcileClient{}}}}); err != nil {
		t.Fatalf("expected recovery slice registration: %v", err)
	}
}

type reconcileClient struct{}

func (reconcileClient) CreateMultipart(context.Context, string, string, string) (string, error) {
	return "", nil
}
func (reconcileClient) PresignPart(context.Context, string, string, string, int, time.Duration) (string, error) {
	return "", nil
}
func (reconcileClient) CompleteMultipart(context.Context, string, string, string, []objectstore.Part) error {
	return nil
}
func (reconcileClient) AbortMultipart(context.Context, string, string, string) error { return nil }
func (reconcileClient) Head(context.Context, string, string) (objectstore.Head, error) {
	return objectstore.Head{}, nil
}
func (reconcileClient) Get(context.Context, string, string) (io.ReadCloser, error) { return nil, nil }
func (reconcileClient) Put(context.Context, string, string, io.Reader, int64, string) error {
	return nil
}
func (reconcileClient) ListMultipartParts(context.Context, string, string, string) ([]objectstore.UploadedPart, error) {
	return nil, nil
}
