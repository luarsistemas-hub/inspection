package generate_snapshot

import (
	"context"
	"fmt"
	"strings"

	"inspection/libs/identity"
	report "inspection/services/inspection/internal/features/reports/core"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

const (
	// VideoUnobservedCoverage explains a visual gap without implying that the
	// expected component is absent from the property.
	VideoUnobservedCoverage = "Não observado no vídeo atual; isto não indica ausência do item."
	// VideoObstructedCoverage describes an obscured view without implying absence.
	VideoObstructedCoverage = "Visão de %s obstruída no vídeo atual; isto não indica ausência do item."
	// VideoBlurredCoverage describes a frame without enough sharpness to assess.
	VideoBlurredCoverage = "Imagem de %s sem nitidez suficiente no vídeo atual para avaliar o item."
	// VideoAmbiguousCoverage describes an observation whose room identity is unresolved.
	VideoAmbiguousCoverage = "Não foi possível identificar com segurança qual ambiente corresponde à referência de %s."
	// VideoObstructedGuidance is the focused recapture instruction for an obstructed component.
	VideoObstructedGuidance = "Para recaptura: filme %s sem objetos na frente, mostrando toda a área."
	// VideoBlurredGuidance is the focused recapture instruction for an unclear component.
	VideoBlurredGuidance = "Para recaptura: filme %s, mantenha a câmera estável e aproxime-se até a imagem ficar nítida."
	// VideoAmbiguousGuidance is the focused recapture instruction for an ambiguous room.
	VideoAmbiguousGuidance = "Para recaptura: filme %s e mostre a porta ou o corredor de acesso para diferenciar o ambiente."
	// VideoUnprocessedCoverage explains a component left without analysis by
	// a technical failure, such as an exhausted matching budget.
	VideoUnprocessedCoverage   = "Não processado por falha técnica; a análise deste item pode ser refeita."
	unidentifiedVideoComponent = "Componente sem identificação"
)

// VideoPlanRequirements projects the sealed items of a video plan into report
// requirements. Gap items keep an explicit INCONCLUSIVE or FAILED status with
// a localized coverage note; the returned set names those gap keys so callers
// do not overwrite them with the pending status reserved for analysed items.
func VideoPlanRequirements(ctx context.Context, tx *gorm.DB, tenantID identity.ID, plan database.VideoPlan, hasReference bool) ([]report.Requirement, map[string]struct{}, error) {
	var items []database.VideoPlanItem
	if err := tx.WithContext(ctx).Where("tenant_id=? AND plan_id=?", tenantID, plan.ID).Order("requirement_key ASC").Find(&items).Error; err != nil {
		return nil, nil, err
	}
	requirements := make([]report.Requirement, 0, len(items))
	gaps := make(map[string]struct{})
	indexes := make(map[string]int, len(items))
	for _, item := range items {
		scope, found, err := videoPlanScope(ctx, tx, tenantID, plan, item)
		if err != nil {
			return nil, nil, err
		}
		label := unidentifiedVideoComponent
		if found {
			label = VideoScopeLabel(scope)
		}
		index, exists := indexes[item.RequirementKey]
		if !exists {
			index = len(requirements)
			indexes[item.RequirementKey] = index
			requirements = append(requirements, report.Requirement{Key: item.RequirementKey, Section: "Vídeo", Label: label, Coverage: item.Reason, AnalysisMode: "CURRENT_ONLY", AnalysisStatus: "PENDING"})
		} else if label != "" && !strings.Contains(" · "+requirements[index].Label+" · ", " · "+label+" · ") {
			requirements[index].Label += " · " + label
		}
		requirement := &requirements[index]
		if hasReference && item.Outcome == "MATCHED" {
			requirement.AnalysisMode = "COMPARE_ORIGIN_CURRENT"
		}
		switch item.Outcome {
		case "MATCHED", "CURRENT_ONLY":
		case "TECHNICAL_FAILURE":
			requirement.AnalysisStatus, requirement.Coverage = "FAILED", VideoUnprocessedCoverage
			gaps[item.RequirementKey] = struct{}{}
		default:
			if requirement.AnalysisStatus != "FAILED" {
				requirement.AnalysisStatus = "INCONCLUSIVE"
				requirement.Coverage, requirement.Instructions = VideoGapGuidance(item.Reason, label, scope.ViewLabel)
			}
			gaps[item.RequirementKey] = struct{}{}
		}
	}
	return requirements, gaps, nil
}

// VideoGapGuidance maps known visual limitations to named coverage and
// targeted filming instructions; unknown reasons keep the generic wording.
func VideoGapGuidance(reason, label, view string) (coverage, instructions string) {
	switch reason {
	case "OBSTRUCTED_VIEW":
		return fmt.Sprintf(VideoObstructedCoverage, label), fmt.Sprintf(VideoObstructedGuidance, label)
	case "BLURRED_VIEW":
		return fmt.Sprintf(VideoBlurredCoverage, label), fmt.Sprintf(VideoBlurredGuidance, label)
	case "AMBIGUOUS_IDENTITY":
		return fmt.Sprintf(VideoAmbiguousCoverage, label), fmt.Sprintf(VideoAmbiguousGuidance, label)
	default:
		return VideoUnobservedCoverage, VideoRecaptureGuidance(label, view)
	}
}

// videoPlanScope loads the scope item that defines a plan component in the
// preparation pinned for it.
func videoPlanScope(ctx context.Context, tx *gorm.DB, tenantID identity.ID, plan database.VideoPlan, item database.VideoPlanItem) (database.VideoScopeItem, bool, error) {
	var scope database.VideoScopeItem
	query := tx.WithContext(ctx).Where("tenant_id=? AND id=?", tenantID, item.ComponentID)
	if plan.OriginPreparationID == nil {
		query = query.Where("preparation_id=? OR preparation_id IN (SELECT preparation_id FROM analysis.video_plan_preparations WHERE tenant_id=? AND plan_id=?)", plan.CurrentPreparationID, tenantID, plan.ID)
	} else if item.Outcome != "CURRENT_ONLY" {
		query = query.Where("preparation_id=?", VideoScopePreparationID(plan, item))
	}
	err := query.Take(&scope).Error
	if err == gorm.ErrRecordNotFound {
		return database.VideoScopeItem{}, false, nil
	}
	return scope, err == nil, err
}

// VideoRecaptureGuidance names the component and the view an operator may
// request in a focused recapture of a visual gap.
func VideoRecaptureGuidance(label, view string) string {
	guidance := "Para recaptura: filme " + label + " de perto, com boa luz e sem obstrução"
	if view = strings.TrimSpace(view); view != "" {
		guidance += ", mostrando a vista " + view
	}
	return guidance + "."
}

// VideoScopePreparationID returns the pinned origin preparation for expected
// components and the current preparation for current-only discoveries.
func VideoScopePreparationID(plan database.VideoPlan, item database.VideoPlanItem) identity.ID {
	if item.Outcome != "CURRENT_ONLY" && plan.OriginPreparationID != nil {
		return *plan.OriginPreparationID
	}
	return plan.CurrentPreparationID
}

// VideoScopeLabel formats a scope item as "Ambiente — Componente".
func VideoScopeLabel(scope database.VideoScopeItem) string {
	component := strings.TrimSpace(scope.ComponentLabel)
	if component == "" {
		return unidentifiedVideoComponent
	}
	environment := strings.TrimSpace(scope.EnvironmentCandidate)
	if environment == "" {
		return component
	}
	return environment + " — " + component
}
