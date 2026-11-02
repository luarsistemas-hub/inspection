// Package download_pdf authorizes the download of one stored report PDF by
// minting a short-lived private URL; the object key is never returned.
package download_pdf

import (
	"context"
	"fmt"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/objectstore"

	"gorm.io/gorm"
)

// Dependencies are the database and the private object store.
type Dependencies struct {
	DB    *gorm.DB
	Store objectstore.Store
}

// Input names the snapshot whose PDF is requested.
type Input struct{ TenantID, SnapshotID identity.ID }

// Result is the artifact metadata (without its storage key) and the URL.
type Result struct {
	Artifact database.ReportArtifact
	URL      string
}

// Setup validates the dependencies and returns the download query. A missing
// or foreign artifact is gorm.ErrRecordNotFound.
func Setup(deps Dependencies) (func(context.Context, Input) (Result, error), error) {
	if deps.DB == nil {
		return nil, gorm.ErrInvalidDB
	}
	if deps.Store.Client == nil || deps.Store.Bucket == "" {
		return nil, fmt.Errorf("slice reports/download_pdf: missing private object store")
	}
	// Store.PresignGet signs with the public client when there is one, else
	// with the private client; whichever signs must be a Presigner, otherwise
	// every download would fail at request time.
	signer := deps.Store.Client
	if deps.Store.PublicClient != nil {
		signer = deps.Store.PublicClient
	}
	if _, ok := signer.(objectstore.Presigner); !ok {
		return nil, fmt.Errorf("slice reports/download_pdf: object store client cannot presign downloads")
	}
	return func(ctx context.Context, in Input) (Result, error) {
		var artifact database.ReportArtifact
		if err := deps.DB.WithContext(ctx).Where("tenant_id=? AND snapshot_id=? AND kind='PDF'", in.TenantID, in.SnapshotID).First(&artifact).Error; err != nil {
			return Result{}, err
		}
		url, err := deps.Store.PresignGet(ctx, artifact.ObjectKey, 5*time.Minute)
		if err != nil {
			return Result{}, err
		}
		artifact.ObjectKey = ""
		return Result{Artifact: artifact, URL: url}, nil
	}, nil
}
