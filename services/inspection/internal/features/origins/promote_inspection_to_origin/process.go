package promote_inspection_to_origin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/inventory/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ProcessDependencies contains platform services needed while activating a promotion.
type ProcessDependencies struct {
	Inventory core.Service
	Now       func() time.Time
}

type activationFailure struct {
	status, code, reason string
	retryable            bool
}

func (f activationFailure) Error() string { return f.code }

// Process advances one promotion. It is safe to call repeatedly after delivery retries.
func Process(ctx context.Context, db *gorm.DB, deps ProcessDependencies, tenantID, promotionID identity.ID) error {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	start := time.Now()
	var promotion database.InspectionOriginPromotion
	err := (tenanttx.Runner{DB: db}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=?", tenantID, promotionID).Take(&promotion).Error; err != nil {
			return err
		}
		if terminal(promotion.Status) {
			return nil
		}
		return tx.Model(&promotion).Updates(map[string]any{"attempts": gorm.Expr("attempts+1"), "updated_at": deps.Now().UTC()}).Error
	})
	if err != nil {
		return err
	}
	if terminal(promotion.Status) {
		slog.InfoContext(ctx, "inspection origin promotion processed", "promotionId", promotionID.String(), "inspectionId", promotion.InspectionID.String(), "originVersionId", idString(promotion.DraftVersionID), "durationMs", time.Since(start).Milliseconds(), "result", promotion.Status, "failureCode", stringValue(promotion.FailureCode))
		return nil
	}
	if promotion.DraftVersionID == nil {
		return fmt.Errorf("promotion draft missing")
	}
	var result string
	var finalStatus, failureCode string
	err = (tenanttx.Runner{DB: db}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		var draft database.OriginVersion
		if err := tx.Where("tenant_id=? AND id=?", tenantID, *promotion.DraftVersionID).Take(&draft).Error; err != nil {
			return err
		}
		switch {
		case draft.Status != "DRAFT":
			var err error
			finalStatus, failureCode, err = resolveClosedDraft(tx, promotion, draft, deps.Now())
			result = finalStatus
			return err
		case draft.DraftState == nil:
			result, finalStatus = "aguardando", "aguardando"
			return nil
		case *draft.DraftState == "PREPARING":
			result, finalStatus = "aguardando", "aguardando"
			return nil
		case *draft.DraftState == "FAILED":
			err := setFailure(tx, promotion, "FAILED", "CATALOG_FAILED", "A catalogação da vistoria falhou. Tente novamente.", true, deps.Now())
			if err == nil {
				result, finalStatus, failureCode = "FAILED", "FAILED", "CATALOG_FAILED"
			}
			return err
		case *draft.DraftState != "READY":
			result, finalStatus = "aguardando", "aguardando"
			return nil
		}
		// ACTIVATING is durable before the atomic activation transaction, allowing reconciliation to resume it.
		if err := tx.Model(&promotion).Updates(map[string]any{"status": "ACTIVATING", "updated_at": deps.Now().UTC()}).Error; err != nil {
			return err
		}
		result = "READY"
		return nil
	})
	if err != nil {
		return err
	}
	if result != "READY" {
		slog.InfoContext(ctx, "inspection origin promotion processed", "promotionId", promotionID.String(), "inspectionId", promotion.InspectionID.String(), "originVersionId", idString(promotion.DraftVersionID), "durationMs", time.Since(start).Milliseconds(), "result", finalStatus, "failureCode", failureCode)
		return nil
	}

	err = (tenanttx.Runner{DB: db}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		var p database.InspectionOriginPromotion
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=?", tenantID, promotionID).Take(&p).Error; err != nil {
			return err
		}
		if terminal(p.Status) {
			finalStatus, failureCode = p.Status, stringValue(p.FailureCode)
			return nil
		}
		var origin database.Origin
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=?", tenantID, p.OriginID).Take(&origin).Error; err != nil {
			return err
		}
		var draft database.OriginVersion
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=?", tenantID, *p.DraftVersionID).Take(&draft).Error; err != nil {
			return err
		}
		// A draft closed outside this operation decides the outcome before the context check,
		// since its own activation also advances the origin version.
		if draft.Status != "DRAFT" {
			var err error
			finalStatus, failureCode, err = resolveClosedDraft(tx, p, draft, deps.Now())
			return err
		}
		var inspection database.Inspection
		if err := tx.Where("tenant_id=? AND id=?", tenantID, p.InspectionID).Take(&inspection).Error; err != nil {
			return err
		}
		var capture database.CaptureDraft
		if err := tx.Where("tenant_id=? AND responsibility_id IN (SELECT id FROM inspections.responsibilities WHERE tenant_id=? AND inspection_id=?) AND status='SUBMITTED'", tenantID, tenantID, p.InspectionID).Order("created_at DESC").Take(&capture).Error; err != nil {
			return err
		}
		if int(origin.Version) != p.ExpectedOriginVersion || capture.ID != p.CaptureDraftID || (p.SourceKind == "VIDEO" && !sealedPlanMatches(tx, tenantID, inspection, p)) {
			return activationFailure{"NEEDS_CONFIRMATION", "CONTEXT_CHANGED", "O contexto da vistoria ou do imóvel mudou. Revise e confirme a promoção novamente.", false}
		}
		evidenceState := inspected{inspection: inspection, capture: capture}
		if p.SourceKind == "VIDEO" && p.VideoPlanID != nil {
			evidenceState.plan = &database.VideoPlan{ID: *p.VideoPlanID}
		}
		if !hasEvidence(tx, tenantID, evidenceState) {
			return activationFailure{"FAILED", "EVIDENCE_UNAVAILABLE", "As evidências da vistoria não estão disponíveis para criar a origem.", false}
		}
		if draft.DraftState == nil || *draft.DraftState != "READY" {
			return activationFailure{"FAILED", "CATALOG_FAILED", "A catalogação da vistoria falhou. Tente novamente.", true}
		}
		var truncated int64
		if err := tx.Model(&database.InventoryJob{}).Where("tenant_id=? AND origin_version_id=? AND kind='CATALOG' AND error_code='TRUNCATED_ITEMS'", tenantID, draft.ID).Count(&truncated).Error; err != nil {
			return err
		}
		if truncated > 0 {
			return activationFailure{"FAILED", "INCOMPLETE_ANALYSIS", "A análise da vistoria foi limitada e não pode ser ativada. Tente novamente.", false}
		}
		var envCount int64
		if err := tx.Model(&database.InventoryEnvironment{}).Where("tenant_id=? AND origin_version_id=? AND decision<>'REJECTED'", tenantID, draft.ID).Count(&envCount).Error; err != nil {
			return err
		}
		if envCount == 0 {
			return activationFailure{"FAILED", "EMPTY_INVENTORY", "A vistoria não contém ambientes válidos para criar a origem.", false}
		}
		var items []database.InventoryItem
		if err := tx.Joins("JOIN origins.inventory_environments e ON e.tenant_id=inventory_items.tenant_id AND e.id=inventory_items.environment_id AND e.origin_version_id=inventory_items.origin_version_id").Where("inventory_items.tenant_id=? AND inventory_items.origin_version_id=? AND inventory_items.decision='PENDING' AND inventory_items.merged_into_id IS NULL AND e.decision<>'REJECTED'", tenantID, draft.ID).Find(&items).Error; err != nil {
			return err
		}
		var evidence []database.InventoryItemEvidence
		if len(items) > 0 {
			ids := make([]identity.ID, 0, len(items))
			for _, it := range items {
				ids = append(ids, it.ID)
			}
			if err := tx.Where("tenant_id=? AND item_id IN ?", tenantID, ids).Find(&evidence).Error; err != nil {
				return err
			}
		}
		availability, err := core.LoadEvidenceAvailability(tx, tenantID, evidence)
		if err != nil {
			return err
		}
		valid := map[identity.ID]bool{}
		for _, e := range evidence {
			if availability[e.ID] != "UNAVAILABLE" {
				valid[e.ItemID] = true
			}
		}
		autoCount := 0
		for _, it := range items {
			if !valid[it.ID] {
				continue
			}
			accepted := tx.Model(&database.InventoryItem{}).Where("tenant_id=? AND id=? AND decision='PENDING' AND version=?", tenantID, it.ID, it.Version).Updates(map[string]any{"decision": "ACCEPTED", "auto_accepted_by_promotion_id": p.ID, "decided_by": nil, "decided_at": deps.Now().UTC(), "version": gorm.Expr("version+1"), "updated_at": deps.Now().UTC()})
			if accepted.Error != nil {
				return accepted.Error
			}
			autoCount += int(accepted.RowsAffected)
		}
		if autoCount > 0 {
			if err := tx.Create(&database.AuditEvent{ID: identity.NewID(), TenantID: tenantID, ActorID: p.RequestedBy, Action: "AUTO_ACCEPT_BY_PROMOTION", TargetType: "INSPECTION_ORIGIN_PROMOTION", TargetID: p.ID.String(), Outcome: "SUCCESS", Reason: fmt.Sprintf("accepted=%d pending_without_evidence=%d", autoCount, len(items)-autoCount), CorrelationID: "inspection-promotion:auto-accept:" + p.ID.String(), OccurredAt: deps.Now().UTC()}).Error; err != nil {
				return err
			}
		}
		previous := origin.ActiveVersionID
		if p.SourceKind == "VIDEO" && p.VideoPlanID != nil {
			if err := retainPromotionVideoPlan(tx, tenantID, draft.ID, *p.VideoPlanID, deps.Now().UTC()); err != nil {
				return err
			}
		}
		activated, err := deps.Inventory.ActivateWithin(ctx, tx, core.ActivateInput{TenantID: tenantID, OriginVersionID: draft.ID, ActorMembershipID: p.RequestedBy, Purpose: p.Purpose, ExpectedOriginVersion: int64(p.ExpectedOriginVersion), AllowEmptyItems: true})
		if err != nil {
			msg := err.Error()
			if apperror.Is(err, apperror.Conflict) || strings.Contains(msg, "expectedOriginVersion") || strings.Contains(msg, "baseline changed") {
				return activationFailure{"NEEDS_CONFIRMATION", "CONTEXT_CHANGED", "A origem atual mudou. Revise e confirme a promoção novamente.", false}
			}
			if strings.Contains(msg, "ASSET_ARCHIVED") {
				return activationFailure{"FAILED", "ASSET_ARCHIVED", "O imóvel está arquivado.", false}
			}
			if strings.Contains(msg, "LIMIT_") {
				return activationFailure{"FAILED", "INCOMPLETE_ANALYSIS", "O inventário excede os limites permitidos para ativação.", false}
			}
			if strings.Contains(msg, "NO_ENVIRONMENTS") || strings.Contains(msg, "NO_INCLUDED_ITEMS") {
				return activationFailure{"FAILED", "EMPTY_INVENTORY", "A vistoria não contém inventário válido para criar a origem.", false}
			}
			return err
		}
		now := deps.Now().UTC()
		var superseded any
		if previous != nil {
			superseded = *previous
		}
		if err := tx.Model(&p).Updates(map[string]any{"status": "COMPLETED", "origin_version_id": activated.ID, "superseded_version_id": superseded, "completed_at": now, "failure_code": nil, "failure_reason": nil, "retryable": false, "updated_at": now}).Error; err != nil {
			return err
		}
		finalStatus = "COMPLETED"
		return tx.Create(&database.AuditEvent{ID: identity.NewID(), TenantID: tenantID, ActorID: p.RequestedBy, Action: "origin.inspection_promotion_completed", TargetType: "INSPECTION_ORIGIN_PROMOTION", TargetID: p.ID.String(), Outcome: "SUCCESS", CorrelationID: "inspection-promotion:" + p.ID.String(), OccurredAt: now}).Error
	})
	var businessFailure activationFailure
	if errors.As(err, &businessFailure) {
		err = (tenanttx.Runner{DB: db}).Within(ctx, tenantID, func(tx *gorm.DB) error {
			var current database.InspectionOriginPromotion
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=?", tenantID, promotionID).Take(&current).Error; err != nil {
				return err
			}
			if terminal(current.Status) {
				finalStatus, failureCode = current.Status, stringValue(current.FailureCode)
				return nil
			}
			if err := setFailure(tx, current, businessFailure.status, businessFailure.code, businessFailure.reason, businessFailure.retryable, deps.Now()); err != nil {
				return err
			}
			finalStatus, failureCode = businessFailure.status, businessFailure.code
			return nil
		})
	}
	if err == nil && finalStatus == "" {
		finalStatus = "aguardando"
	}
	slog.InfoContext(ctx, "inspection origin promotion processed", "promotionId", promotionID.String(), "inspectionId", promotion.InspectionID.String(), "originVersionId", idString(promotion.DraftVersionID), "durationMs", time.Since(start).Milliseconds(), "result", finalStatus, "failureCode", failureCode, "error", err)
	return err
}

// ProcessForDraft resumes the promotion linked to a newly ready inventory draft.
func ProcessForDraft(ctx context.Context, db *gorm.DB, tenantID, draftVersionID identity.ID, deps ProcessDependencies) error {
	var promotion database.InspectionOriginPromotion
	err := (tenanttx.Runner{DB: db}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND draft_version_id=? AND status IN ('PREPARING','ACTIVATING')", tenantID, draftVersionID).Take(&promotion).Error
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return Process(ctx, db, deps, tenantID, promotion.ID)
}

// resolveClosedDraft ends a promotion whose draft left DRAFT outside it. A human
// activation fulfils the promotion; any other closure cancels it.
func resolveClosedDraft(tx *gorm.DB, p database.InspectionOriginPromotion, draft database.OriginVersion, now time.Time) (string, string, error) {
	switch draft.Status {
	case "ACTIVE", "SUPERSEDED":
		now = now.UTC()
		var superseded any
		if draft.SupersedesID != nil {
			superseded = *draft.SupersedesID
		}
		if err := tx.Model(&p).Updates(map[string]any{"status": "COMPLETED", "origin_version_id": draft.ID, "superseded_version_id": superseded, "completed_at": now, "failure_code": nil, "failure_reason": nil, "retryable": false, "updated_at": now}).Error; err != nil {
			return "", "", err
		}
		return "COMPLETED", "", tx.Create(&database.AuditEvent{ID: identity.NewID(), TenantID: p.TenantID, ActorID: p.RequestedBy, Action: "origin.inspection_promotion_completed", TargetType: "INSPECTION_ORIGIN_PROMOTION", TargetID: p.ID.String(), Outcome: "SUCCESS", Reason: "draft activated outside the promotion", CorrelationID: "inspection-promotion:" + p.ID.String(), OccurredAt: now}).Error
	case "DISCARDED":
		return "CANCELED", "DRAFT_DISCARDED", setFailure(tx, p, "CANCELED", "DRAFT_DISCARDED", "O rascunho desta promoção foi descartado.", false, now)
	default:
		return "CANCELED", "DRAFT_CLOSED", setFailure(tx, p, "CANCELED", "DRAFT_CLOSED", "O rascunho desta promoção foi encerrado fora da promoção.", false, now)
	}
}

func terminal(status string) bool {
	return status == "COMPLETED" || status == "FAILED" || status == "CANCELED" || status == "NEEDS_CONFIRMATION"
}
func setFailure(tx *gorm.DB, p database.InspectionOriginPromotion, status, code, reason string, retryable bool, now time.Time) error {
	return tx.Model(&p).Updates(map[string]any{"status": status, "failure_code": code, "failure_reason": reason, "retryable": retryable, "updated_at": now.UTC()}).Error
}
func sealedPlanMatches(tx *gorm.DB, tenant identity.ID, inspection database.Inspection, p database.InspectionOriginPromotion) bool {
	if p.VideoPlanID == nil {
		return false
	}
	var plan database.VideoPlan
	return tx.Where("tenant_id=? AND id=? AND inspection_id=? AND status='SEALED'", tenant, *p.VideoPlanID, inspection.ID).Take(&plan).Error == nil && inspection.ActiveVideoPlanID != nil && *inspection.ActiveVideoPlanID == *p.VideoPlanID
}
