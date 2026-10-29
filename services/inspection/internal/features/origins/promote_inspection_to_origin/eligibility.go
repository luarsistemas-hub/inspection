package promote_inspection_to_origin

import (
	"context"
	"errors"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type inspected struct {
	inspection database.Inspection
	asset      database.Asset
	origin     database.Origin
	capture    database.CaptureDraft
	plan       *database.VideoPlan
	draft      *database.OriginVersion
}

func status(ctx context.Context, d Dependencies, q Query) (StatusView, error) {
	var view StatusView
	err := (tenanttx.Runner{DB: d.DB}).Within(ctx, q.TenantID, func(tx *gorm.DB) error {
		state, err := loadState(tx, q.TenantID, q.InspectionID, false)
		if err != nil {
			return err
		}
		if _, err = d.Authorizer.Authorize(ctx, q.TenantID, []string{auth.TenantAdmin, auth.Manager}, &requestctx.Scope{Kind: "BUSINESS_UNIT", ID: state.asset.BusinessUnitID}, false); err != nil {
			return gorm.ErrRecordNotFound
		}
		view = buildStatus(tx, q.TenantID, state)
		return nil
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return StatusView{}, apperror.New(apperror.NotFound, "inspectionId", "vistoria não encontrada")
	}
	if err != nil {
		return StatusView{}, err
	}
	return view, nil
}

func loadState(tx *gorm.DB, tenantID, inspectionID identity.ID, lock bool) (inspected, error) {
	var s inspected
	query := tx
	if lock {
		query = query.Clauses(clauseUpdate)
	}
	if err := query.Where("tenant_id=? AND id=?", tenantID, inspectionID).First(&s.inspection).Error; err != nil {
		return s, err
	}
	if err := tx.Where("tenant_id=? AND id=?", tenantID, s.inspection.AssetID).First(&s.asset).Error; err != nil {
		return s, err
	}
	err := tx.Where("tenant_id=? AND asset_id=?", tenantID, s.asset.ID).First(&s.origin).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		s.origin.Version = 1
	} else if err != nil {
		return s, err
	}
	if s.origin.ID != (identity.ID{}) && lock {
		if err := tx.Clauses(clauseUpdate).Where("tenant_id=? AND id=?", tenantID, s.origin.ID).First(&s.origin).Error; err != nil {
			return s, err
		}
	}
	tx.Where("tenant_id=? AND responsibility_id IN (SELECT id FROM inspections.responsibilities WHERE tenant_id=? AND inspection_id=?) AND status='SUBMITTED'", tenantID, tenantID, inspectionID).Order("created_at DESC").First(&s.capture)
	if s.inspection.MediaKind == "VIDEO" {
		if s.inspection.ActiveVideoPlanID != nil {
			var p database.VideoPlan
			if err := tx.Where("tenant_id=? AND id=? AND inspection_id=? AND status='SEALED'", tenantID, *s.inspection.ActiveVideoPlanID, inspectionID).First(&p).Error; err == nil {
				s.plan = &p
			}
		}
	}
	if s.origin.ID != (identity.ID{}) {
		draftQuery := tx
		if lock {
			draftQuery = draftQuery.Clauses(clauseUpdate)
		}
		err := draftQuery.Where("tenant_id=? AND origin_id=? AND status='DRAFT' AND has_inventory", tenantID, s.origin.ID).First(&s.draft).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return s, err
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			s.draft = nil
		}
	}
	return s, nil
}

func buildStatus(tx *gorm.DB, tenantID identity.ID, s inspected) StatusView {
	v := StatusView{Impediments: make([]Impediment, 0), AutomaticCatalogSkipped: false}
	if s.inspection.Status != "COMPLETED" {
		v.Impediments = append(v.Impediments, Impediment{"INSPECTION_NOT_COMPLETED", "A vistoria precisa estar concluída."})
	}
	if s.capture.ID == (identity.ID{}) {
		v.Impediments = append(v.Impediments, Impediment{"NO_SUBMITTED_CAPTURE", "A vistoria não possui uma captura submetida."})
	}
	if s.asset.Status == "ARCHIVED" {
		v.Impediments = append(v.Impediments, Impediment{"ASSET_ARCHIVED", "O imóvel está arquivado."})
	}
	if s.inspection.MediaKind == "VIDEO" && s.plan == nil {
		v.Impediments = append(v.Impediments, Impediment{"VIDEO_ANALYSIS_NOT_SEALED", "A análise de vídeo da vistoria ainda não foi finalizada."})
	}
	if s.capture.ID != (identity.ID{}) && !hasEvidence(tx, tenantID, s) {
		v.Impediments = append(v.Impediments, Impediment{"EVIDENCE_UNAVAILABLE", "Não há evidências disponíveis na captura submetida."})
	}
	if s.capture.ID != (identity.ID{}) && !requirementsHaveEnvironment(s.capture) {
		v.Impediments = append(v.Impediments, Impediment{"EVIDENCE_UNAVAILABLE", "A captura submetida não possui ambientes com requisitos de evidência."})
	}
	var active *database.OriginVersion
	if s.origin.ActiveVersionID != nil {
		var version database.OriginVersion
		if tx.Where("tenant_id=? AND id=?", tenantID, *s.origin.ActiveVersionID).First(&version).Error == nil {
			active = &version
			v.ActiveOrigin = &ActiveOrigin{VersionID: version.ID, Number: version.VersionNumber, MediaKind: version.MediaKind, HasInventory: version.HasInventory}
		}
	}
	if s.draft != nil {
		v.ConflictingDraft = &ConflictingDraft{ID: s.draft.ID, Source: s.draft.Source, SourceInspectionID: s.draft.SourceInspectionID, DraftState: s.draft.DraftState, OwnInspection: s.draft.SourceInspectionID != nil && *s.draft.SourceInspectionID == s.inspection.ID && (s.draft.Source == "CAPTURE" || s.draft.Source == "INSPECTION_PROMOTION")}
	}
	var ownInventory, skipped int64
	if tx.Model(&database.OriginVersion{}).Where("tenant_id=? AND source_inspection_id=? AND has_inventory", tenantID, s.inspection.ID).Count(&ownInventory).Error == nil && ownInventory == 0 && tx.Model(&database.AuditEvent{}).Where("tenant_id=? AND action='inventory.catalog_skipped' AND target_type='INSPECTION' AND target_id=?", tenantID, s.inspection.ID.String()).Count(&skipped).Error == nil && skipped > 0 {
		v.AutomaticCatalogSkipped = true
	}
	if s.origin.ID != (identity.ID{}) {
		var inProgress database.InspectionOriginPromotion
		if tx.Where("tenant_id=? AND asset_id=? AND status IN ('PREPARING','ACTIVATING')", tenantID, s.asset.ID).First(&inProgress).Error == nil {
			v.Impediments = append(v.Impediments, Impediment{"PROMOTION_IN_PROGRESS", "Já existe uma promoção em andamento para este imóvel."})
		}
	}
	var last database.InspectionOriginPromotion
	if tx.Where("tenant_id=? AND inspection_id=?", tenantID, s.inspection.ID).Order("created_at DESC").First(&last).Error == nil {
		v.CurrentPromotion = &last
	}
	if active != nil && active.SourceInspectionID != nil && *active.SourceInspectionID == s.inspection.ID && (active.Source == "INSPECTION_PROMOTION" || active.Source == "CAPTURE") {
		v.GeneratedVersionActive = true
	}
	if active != nil && !v.GeneratedVersionActive {
		var count int64
		if tx.Model(&database.InspectionOriginPromotion{}).Where("tenant_id=? AND inspection_id=? AND status='COMPLETED' AND origin_version_id=?", tenantID, s.inspection.ID, active.ID).Count(&count).Error == nil && count > 0 {
			v.GeneratedVersionActive = true
		}
	}
	v.ConfirmationDigest = ConfirmationDigest(digestInput(s))
	v.Eligible = len(v.Impediments) == 0
	return v
}

func digestInput(s inspected) DigestInput {
	draftStatus, draftState := stringOrEmpty(s.draft, func(x database.OriginVersion) string { return x.Status }), stringPtrOrEmpty(s.draft)
	if s.draft != nil && s.draft.SourceInspectionID != nil && *s.draft.SourceInspectionID == s.inspection.ID && (s.draft.Source == "CAPTURE" || s.draft.Source == "INSPECTION_PROMOTION") {
		draftStatus, draftState = "", ""
	}
	return DigestInput{OriginVersion: s.origin.Version, ActiveVersionID: idString(s.origin.ActiveVersionID), DraftID: idString(draftID(s.draft)), DraftStatus: draftStatus, DraftState: draftState, CaptureDraftID: identityString(s.capture.ID), VideoPlanID: planID(s.plan), VideoGeneration: planGeneration(s.plan)}
}

func hasEvidence(tx *gorm.DB, tenantID identity.ID, s inspected) bool {
	if s.inspection.MediaKind == "VIDEO" {
		if s.plan == nil {
			return false
		}
		var count int64
		return tx.Table("analysis.video_plan_preparations pp").Joins("JOIN media.video_preparation_jobs p ON p.tenant_id=pp.tenant_id AND p.id=pp.preparation_id AND p.status='READY'").Joins("JOIN media.video_frames f ON f.tenant_id=pp.tenant_id AND f.preparation_id=pp.preparation_id").Where("pp.tenant_id=? AND pp.plan_id=? AND f.sha256<>'' AND f.object_key<>''", tenantID, s.plan.ID).Count(&count).Error == nil && count > 0
	}
	var count int64
	return tx.Table("media.media_objects m").Joins("JOIN inspections.responsibilities r ON r.tenant_id=m.tenant_id AND r.id=m.responsibility_id").Joins("JOIN capture.capture_drafts d ON d.tenant_id=r.tenant_id AND d.responsibility_id=r.id AND d.status='SUBMITTED'").Where("m.tenant_id=? AND r.inspection_id=? AND m.status='READY' AND m.media_kind='PHOTO'", tenantID, s.inspection.ID).Count(&count).Error == nil && count > 0
}

var clauseUpdate = clause.Locking{Strength: "UPDATE"}

func idString(id *identity.ID) string {
	if id == nil {
		return ""
	}
	return id.String()
}
func draftID(v *database.OriginVersion) *identity.ID {
	if v == nil {
		return nil
	}
	return &v.ID
}
func identityString(id identity.ID) string {
	if id == (identity.ID{}) {
		return ""
	}
	return id.String()
}
func stringOrEmpty(v *database.OriginVersion, f func(database.OriginVersion) string) string {
	if v == nil {
		return ""
	}
	return f(*v)
}
func stringPtrOrEmpty(v *database.OriginVersion) string {
	if v == nil || v.DraftState == nil {
		return ""
	}
	return *v.DraftState
}
func planID(v *database.VideoPlan) string {
	if v == nil {
		return ""
	}
	return v.ID.String()
}
func planGeneration(v *database.VideoPlan) int {
	if v == nil {
		return 0
	}
	return v.Generation
}
