package publication

import (
	"context"
	"strings"
	"time"

	"gorm.io/gorm"
	"inspection/libs/identity"
	publicationcore "inspection/services/inspection/internal/features/reports/publication/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/tenanttx"
)

type Service struct {
	DB  *gorm.DB
	Now func() time.Time
}
type ConfigureInput struct {
	TenantID        identity.ID
	Mode            string
	ExpectedVersion int64
}
type PublishInput struct {
	TenantID, InspectionID, SnapshotID, ActorID identity.ID
	ClientMutationID                            string
	Final                                       bool
}
type InvalidateInput struct {
	TenantID, PublicationID, ActorID identity.ID
	Reason                           string
	ExpectedVersion                  int64
}

func (s Service) clock() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s Service) Configure(ctx context.Context, in ConfigureInput) (database.PublicationPolicy, error) {
	if s.DB == nil || in.TenantID == (identity.ID{}) {
		return database.PublicationPolicy{}, apperror.New(apperror.InvalidInput, "tenantId", "tenant is required")
	}
	var out database.PublicationPolicy
	err := (tenanttx.Runner{DB: s.DB}).Within(ctx, in.TenantID, func(tx *gorm.DB) error {
		var row database.PublicationPolicy
		err := tx.Where("tenant_id = ?", in.TenantID).First(&row).Error
		current := int64(0)
		if err != nil && err != gorm.ErrRecordNotFound {
			return err
		}
		if err == nil {
			current = row.Version
		}
		policy, err := publicationcore.Configure(in.Mode, in.ExpectedVersion, current)
		if err != nil {
			return err
		}
		if row.ID == (identity.ID{}) {
			row.ID = identity.NewID()
			row.TenantID = in.TenantID
			row.CreatedAt = s.clock()
		}
		row.Mode = policy.Mode
		row.Version = policy.Version
		row.UpdatedAt = s.clock()
		out = row
		return tx.Save(&row).Error
	})
	return out, err
}

func (s Service) Publish(ctx context.Context, in PublishInput) (database.ReportPublication, error) {
	if s.DB == nil || in.TenantID == (identity.ID{}) || in.SnapshotID == (identity.ID{}) || strings.TrimSpace(in.ClientMutationID) == "" {
		return database.ReportPublication{}, apperror.New(apperror.InvalidInput, "clientMutationId", "publication input is required")
	}
	var out database.ReportPublication
	err := (tenanttx.Runner{DB: s.DB}).Within(ctx, in.TenantID, func(tx *gorm.DB) error {
		return tx.Transaction(func(tx *gorm.DB) error {
			if err := tx.Where("tenant_id=? AND client_mutation_id=?", in.TenantID, in.ClientMutationID).First(&out).Error; err == nil {
				return nil
			}
			var snap database.ReportSnapshot
			if err := tx.Where("tenant_id=? AND id=? AND inspection_id=?", in.TenantID, in.SnapshotID, in.InspectionID).First(&snap).Error; err != nil {
				return apperror.New(apperror.InvalidState, "snapshotId", "final snapshot not found")
			}
			if err := publicationcore.CanPublish(in.Final, "PUBLISHED"); err != nil {
				return err
			}
			now := s.clock()
			out = database.ReportPublication{ID: identity.NewID(), TenantID: in.TenantID, SnapshotID: snap.ID, InspectionID: snap.InspectionID, PolicyVersion: snap.PublicationPolicyVersion, ClientMutationID: in.ClientMutationID, Status: publicationcore.Published, ActorID: in.ActorID, Version: 1, PublishedAt: &now, CreatedAt: now, UpdatedAt: now}
			var previous database.ReportPublication
			if err := tx.Where("tenant_id=? AND inspection_id=? AND status=?", in.TenantID, in.InspectionID, publicationcore.Published).First(&previous).Error; err == nil {
				previous.Status = publicationcore.Superseded
				previous.Version++
				previous.SupersededBy = &out.ID
				previous.UpdatedAt = now
				if err := tx.Save(&previous).Error; err != nil {
					return err
				}
			}
			return tx.Create(&out).Error
		})
	})
	return out, err
}

func (s Service) Invalidate(ctx context.Context, in InvalidateInput) (database.ReportPublication, error) {
	var out database.ReportPublication
	if s.DB == nil {
		return out, apperror.New(apperror.Internal, "", "internal error")
	}
	err := (tenanttx.Runner{DB: s.DB}).Within(ctx, in.TenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND id=?", in.TenantID, in.PublicationID).First(&out).Error; err != nil {
			return apperror.New(apperror.NotFound, "publicationId", "publication not found")
		}
		status, at, err := publicationcore.Invalidate(out.Status, in.Reason, in.ExpectedVersion, out.Version, s.clock())
		if err != nil {
			return err
		}
		if status == out.Status {
			return nil
		}
		out.Status = status
		out.InvalidatedAt = at
		out.Version++
		out.UpdatedAt = s.clock()
		return tx.Save(&out).Error
	})
	return out, err
}
