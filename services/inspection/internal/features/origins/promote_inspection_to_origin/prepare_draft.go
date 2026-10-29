package promote_inspection_to_origin

import (
	"context"
	"errors"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/inventory/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
)

func prepareDraft(ctx context.Context, d Dependencies, c PrepareDraftCommand) (database.OriginVersion, error) {
	if c.TenantID == (identity.ID{}) || c.InspectionID == (identity.ID{}) {
		return database.OriginVersion{}, apperror.New(apperror.InvalidInput, "inspectionId", "Informe uma vistoria válida.")
	}
	var result database.OriginVersion
	err := (tenanttx.Runner{DB: d.DB}).Within(ctx, c.TenantID, func(tx *gorm.DB) error {
		s, err := loadState(tx, c.TenantID, c.InspectionID, true)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return apperror.New(apperror.NotFound, "inspectionId", "Vistoria não encontrada.")
		}
		if err != nil {
			return err
		}
		if _, err = d.Authorizer.Authorize(ctx, c.TenantID, []string{auth.TenantAdmin, auth.Manager, auth.Employee}, &requestctx.Scope{Kind: "BUSINESS_UNIT", ID: s.asset.BusinessUnitID}, true); err != nil {
			return apperror.New(apperror.NotFound, "inspectionId", "Vistoria não encontrada.")
		}
		if s.inspection.Status != "COMPLETED" {
			return apperror.New(apperror.InvalidState, "inspectionId", "A vistoria precisa estar concluída.")
		}
		if s.capture.ID == (identity.ID{}) {
			return apperror.New(apperror.InvalidState, "inspectionId", "A vistoria não possui uma captura submetida.")
		}
		if s.draft != nil {
			if s.draft.SourceInspectionID != nil && *s.draft.SourceInspectionID == s.inspection.ID && (s.draft.Source == "CAPTURE" || s.draft.Source == "INSPECTION_PROMOTION") {
				result = *s.draft
				return nil
			}
			return apperror.New(apperror.Conflict, "originVersionId", s.draft.ID.String())
		}
		if s.asset.Status == "ARCHIVED" {
			return apperror.New(apperror.InvalidState, "inspectionId", "O imóvel está arquivado.")
		}
		if s.inspection.MediaKind == "VIDEO" && s.plan == nil {
			return apperror.New(apperror.InvalidState, "inspectionId", "A análise de vídeo da vistoria ainda não foi finalizada.")
		}
		if !hasEvidence(tx, c.TenantID, s) || !requirementsHaveEnvironment(s.capture) {
			return apperror.New(apperror.InvalidState, "inspectionId", "Não há evidências disponíveis para preparar o inventário desta vistoria.")
		}
		draft, err := d.Inventory.CreateDraft(tx, core.DraftInput{TenantID: c.TenantID, AssetID: s.asset.ID, Source: "INSPECTION_PROMOTION", SourceInspectionID: s.inspection.ID, ActorMembershipID: c.ActorID})
		if err != nil {
			return err
		}
		copied, err := copyPreviousCatalog(tx, c.TenantID, s.inspection.ID, s.capture, draft.ID, d.Now().UTC())
		if err != nil {
			return err
		}
		if !copied {
			jobs, _, _, err := prepareCatalog(ctx, tx, d, c.TenantID, s.inspection, s.capture, draft.ID, d.Now().UTC())
			if err != nil {
				return err
			}
			if jobs == 0 {
				return apperror.New(apperror.InvalidState, "inspectionId", "A captura não contém ambientes com evidências para preparar o inventário.")
			}
		}
		result = draft
		return nil
	})
	return result, err
}
