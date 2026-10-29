// Package promote_inspection_video promotes a retained video frame bank without restoring source bytes.
package promote_inspection_video

import (
	"context"
	"errors"
	"fmt"
	"time"

	"inspection/libs/identity"
	retentioncore "inspection/services/inspection/internal/features/retention/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Command struct {
	TenantID, InspectionID identity.ID
	ClientMutationID       string
}

type Dependencies struct {
	DB         *gorm.DB
	Bus        *mediator.Bus
	Authorizer auth.Authorizer
	Now        func() time.Time
}

func Setup(d Dependencies) error {
	if d.DB == nil || d.Bus == nil {
		return fmt.Errorf("slice origins/promote_inspection_video: missing dependency")
	}
	if d.Now == nil {
		d.Now = func() time.Time { return time.Now().UTC() }
	}
	return d.Bus.RegisterCommand(Command{}, func(ctx context.Context, raw any) (any, error) {
		command := raw.(Command)
		if command.TenantID == (identity.ID{}) || command.InspectionID == (identity.ID{}) || command.ClientMutationID == "" {
			return nil, apperror.New(apperror.InvalidInput, "input", "A vistoria e o identificador da solicitação são obrigatórios.")
		}
		var inspection database.Inspection
		var asset database.Asset
		if err := (tenanttx.Runner{DB: d.DB}).Within(ctx, command.TenantID, func(tx *gorm.DB) error {
			if err := tx.Where("tenant_id=? AND id=?", command.TenantID, command.InspectionID).Take(&inspection).Error; err != nil {
				return err
			}
			return tx.Where("tenant_id=? AND id=?", command.TenantID, inspection.AssetID).Take(&asset).Error
		}); errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperror.New(apperror.NotFound, "inspectionId", "Vistoria não encontrada.")
		} else if err != nil {
			return nil, err
		}
		if _, err := d.Authorizer.Authorize(ctx, command.TenantID, []string{auth.TenantAdmin, auth.Manager}, &requestctx.Scope{Kind: "BUSINESS_UNIT", ID: asset.BusinessUnitID}, true); err != nil {
			return nil, err
		}
		var promoted database.OriginVersion
		err := (tenanttx.Runner{DB: d.DB}).Within(ctx, command.TenantID, func(tx *gorm.DB) error {
			var current database.Inspection
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=?", command.TenantID, command.InspectionID).Take(&current).Error; errors.Is(err, gorm.ErrRecordNotFound) {
				return apperror.New(apperror.NotFound, "inspectionId", "Vistoria não encontrada.")
			} else if err != nil {
				return err
			}
			if current.Status != "COMPLETED" || current.MediaKind != "VIDEO" {
				return apperror.New(apperror.InvalidState, "inspectionId", "É necessário concluir a vistoria em vídeo antes de usá-la como origem.")
			}
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=?", command.TenantID, current.AssetID).Take(&asset).Error; err != nil {
				return err
			}
			idempotency := "video-promotion:" + command.InspectionID.String()
			if err := tx.Where("tenant_id=? AND idempotency_key=?", command.TenantID, idempotency).Take(&promoted).Error; err == nil {
				return nil
			} else if err != gorm.ErrRecordNotFound {
				return err
			}
			if current.ActiveVideoPlanID == nil {
				return apperror.New(apperror.InvalidState, "inspectionId", "A análise do vídeo ainda não foi concluída. Aguarde a finalização e tente novamente.")
			}
			var analysisPlan database.VideoPlan
			if err := tx.Where("tenant_id=? AND id=? AND inspection_id=? AND status='SEALED'", command.TenantID, *current.ActiveVideoPlanID, current.ID).Take(&analysisPlan).Error; err != nil {
				return apperror.New(apperror.InvalidState, "inspectionId", "A análise do vídeo ainda não foi concluída. Aguarde a finalização e tente novamente.")
			}
			var planPreparations []database.VideoPlanPreparation
			if err := tx.Where("tenant_id=? AND plan_id=? AND role IN ?", command.TenantID, analysisPlan.ID, []string{"MAIN", "SUPPLEMENT"}).Find(&planPreparations).Error; err != nil {
				return err
			}
			if len(planPreparations) > 1 {
				return apperror.New(apperror.InvalidState, "inspectionId", "Esta vistoria tem mais de um vídeo. Use a ação “Usar esta vistoria como origem” para promover todos os dados.")
			}
			if len(planPreparations) == 0 {
				return apperror.New(apperror.InvalidState, "inspectionId", "A preparação dos quadros do vídeo ainda não foi concluída. Tente novamente em alguns instantes.")
			}
			var preparation database.VideoPreparationJob
			if err := tx.Where("tenant_id=? AND id=? AND status='READY' AND manifest_digest<>''", command.TenantID, planPreparations[0].PreparationID).Take(&preparation).Error; err != nil {
				return apperror.New(apperror.InvalidState, "inspectionId", "A preparação dos quadros do vídeo ainda não foi concluída. Tente novamente em alguns instantes.")
			}
			var media database.MediaObject
			if err := tx.Where("tenant_id=? AND id=? AND media_kind='VIDEO'", command.TenantID, preparation.MediaID).Take(&media).Error; err != nil {
				return apperror.New(apperror.InvalidState, "inspectionId", "O vídeo da vistoria não está disponível.")
			}
			var source database.VideoSource
			if err := tx.Where("tenant_id=? AND media_id=?", command.TenantID, media.ID).Take(&source).Error; err != nil || source.FirstFrameReadyAt == nil || (source.RetentionAnchor != "ACTIVE_BASELINE" && source.SourceExpiryAt == nil) {
				return apperror.New(apperror.InvalidState, "inspectionId", "Não foi possível localizar uma referência válida para as evidências retidas do vídeo.")
			}
			if source.ScreeningStatus != "APPROVED" && source.ScreeningStatus != "DISABLED" {
				return apperror.New(apperror.InvalidState, "inspectionId", "O vídeo ainda não foi aprovado na verificação de segurança.")
			}
			var frames, scope int64
			if err := tx.Model(&database.VideoFrame{}).Where("tenant_id=? AND source_media_id=? AND preparation_id=? AND sha256<>'' AND object_key<>''", command.TenantID, media.ID, preparation.ID).Count(&frames).Error; err != nil {
				return err
			}
			if err := tx.Model(&database.VideoScopeItem{}).Where("tenant_id=? AND preparation_id=?", command.TenantID, preparation.ID).Count(&scope).Error; err != nil {
				return err
			}
			if frames == 0 || scope == 0 {
				return apperror.New(apperror.InvalidState, "inspectionId", "A origem do vídeo não possui evidências retidas disponíveis para uso.")
			}
			var origin database.Origin
			originErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND asset_id=?", command.TenantID, current.AssetID).Take(&origin).Error
			now := d.Now().UTC()
			if originErr == gorm.ErrRecordNotFound {
				origin = database.Origin{ID: identity.NewID(), TenantID: command.TenantID, AssetID: current.AssetID, Version: 1, CreatedAt: now, UpdatedAt: now}
				if err := tx.Create(&origin).Error; err != nil {
					return err
				}
			} else if originErr != nil {
				return originErr
			}
			var count int64
			if err := tx.Model(&database.OriginVersion{}).Where("tenant_id=? AND origin_id=?", command.TenantID, origin.ID).Count(&count).Error; err != nil {
				return err
			}
			versionID := identity.NewID()
			responsibilityID := media.ResponsibilityID
			promoted = database.OriginVersion{ID: versionID, TenantID: command.TenantID, OriginID: origin.ID, VersionNumber: int(count) + 1, ResponsibilityID: &responsibilityID, SupersedesID: origin.ActiveVersionID, Status: "ACTIVE", MediaKind: "VIDEO", VideoPreparationID: &preparation.ID, VideoPlanID: &analysisPlan.ID, ScopeManifestDigest: preparation.ManifestDigest, IdempotencyKey: idempotency, CreatedAt: now, SubmittedAt: &now, ActivatedAt: &now}
			if err := tx.Create(&promoted).Error; err != nil {
				return err
			}
			if origin.ActiveVersionID != nil {
				if err := tx.Model(&database.OriginVersion{}).Where("tenant_id=? AND id=? AND status='ACTIVE'", command.TenantID, *origin.ActiveVersionID).Updates(map[string]any{"status": "SUPERSEDED", "superseded_at": now}).Error; err != nil {
					return err
				}
			}
			if err := tx.Model(&origin).Updates(map[string]any{"active_version_id": promoted.ID, "version": origin.Version + 1, "updated_at": now}).Error; err != nil {
				return err
			}
			reference := database.VideoEvidenceReference{ID: identity.NewID(), TenantID: command.TenantID, PreparationID: preparation.ID, OwnerType: "ORIGIN_VERSION", OwnerID: promoted.ID, CreatedAt: now}
			if err := tx.Where("tenant_id=? AND preparation_id=? AND owner_type=? AND owner_id=?", command.TenantID, preparation.ID, reference.OwnerType, reference.OwnerID).FirstOrCreate(&reference).Error; err != nil {
				return err
			}
			if source.Status != "EXPIRED" && source.RetentionAnchor != "ACTIVE_BASELINE" && source.SourceExpiryAt != nil {
				days := 30
				var policy database.VideoPolicy
				if err := tx.Where("tenant_id=?", command.TenantID).Take(&policy).Error; err == nil && policy.OriginRetentionDays > 0 {
					days = policy.OriginRetentionDays
				} else if err != nil && err != gorm.ErrRecordNotFound {
					return err
				}
				deadline := PromotedExpiry(*source.FirstFrameReadyAt, *source.SourceExpiryAt, days)
				if deadline.After(*source.SourceExpiryAt) {
					source.SourceExpiryAt = &deadline
					if err := tx.Model(&source).Update("source_expiry_at", deadline).Error; err != nil {
						return err
					}
				}
			}
			versions := []identity.ID{promoted.ID}
			if promoted.SupersedesID != nil {
				versions = append(versions, *promoted.SupersedesID)
			}
			return (retentioncore.BaselineTransition{Now: d.Now}).TransitionVersions(ctx, tx, command.TenantID, versions)
		})
		return promoted, err
	})
}

// PromotedExpiry extends a retained source to its origin window without shortening
// an existing deadline. Callers must leave EXPIRED sources untouched.
func PromotedExpiry(firstReady, current time.Time, originDays int) time.Time {
	deadline := firstReady.UTC().Add(time.Duration(originDays) * 24 * time.Hour)
	if current.After(deadline) {
		return current
	}
	return deadline
}
