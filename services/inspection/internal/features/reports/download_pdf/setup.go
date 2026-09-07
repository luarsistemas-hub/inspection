package download_pdf

import (
	"context"
	"gorm.io/gorm"
	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/objectstore"
	"time"
)

type Dependencies struct {
	DB    *gorm.DB
	Store objectstore.Store
}
type Input struct{ TenantID, SnapshotID identity.ID }
type Result struct {
	Artifact database.ReportArtifact
	URL      string
}

func Setup(deps Dependencies) (func(context.Context, Input) (Result, error), error) {
	if deps.DB == nil {
		return nil, gorm.ErrInvalidDB
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
