package promote_inspection_to_origin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	"inspection/services/inspection/internal/features/inventory/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var validPurposes = map[string]bool{"MOVE_IN": true, "CONSTRUCTION_HANDOVER": true, "POST_RENOVATION": true, "INVENTORY_UPDATE": true, "CONTESTATION_ADJUSTMENT": true}

func request(ctx context.Context, d Dependencies, c Command) (Result, error) {
	if c.TenantID == (identity.ID{}) || c.InspectionID == (identity.ID{}) || strings.TrimSpace(c.ClientMutationID) == "" {
		return Result{}, apperror.New(apperror.InvalidInput, "input", "vistoria e identificador da solicitação são obrigatórios")
	}
	if c.Purpose == "" {
		c.Purpose = "INVENTORY_UPDATE"
	}
	if !validPurposes[c.Purpose] {
		return Result{}, apperror.New(apperror.InvalidInput, "purpose", "Finalidade de inventário inválida.")
	}
	if c.ReplaceDraftID != nil && *c.ReplaceDraftID == (identity.ID{}) {
		return Result{}, apperror.New(apperror.InvalidInput, "replaceDraftId", "Rascunho inválido.")
	}
	now := d.Now().UTC()
	var result Result
	err := (tenanttx.Runner{DB: d.DB}).Within(ctx, c.TenantID, func(tx *gorm.DB) error {
		s, err := loadState(tx, c.TenantID, c.InspectionID, true)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return apperror.New(apperror.NotFound, "inspectionId", "vistoria não encontrada")
		}
		if err != nil {
			return err
		}
		principal, err := d.Authorizer.Authorize(ctx, c.TenantID, []string{auth.TenantAdmin, auth.Manager}, &requestctx.Scope{Kind: "BUSINESS_UNIT", ID: s.asset.BusinessUnitID}, true)
		if err != nil {
			return apperror.New(apperror.NotFound, "inspectionId", "vistoria não encontrada")
		}
		var existing database.InspectionOriginPromotion
		err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND inspection_id=? AND client_mutation_id=?", c.TenantID, c.InspectionID, c.ClientMutationID).First(&existing).Error
		if err == nil {
			if existing.Status == "FAILED" && existing.Retryable {
				// A retry reopens the operation, so the inspection must still be promotable.
				if err := impedimentError(buildStatus(tx, c.TenantID, s)); err != nil {
					return err
				}
				if existing.DraftVersionID != nil {
					if _, err := restartFailedDraft(tx, c.TenantID, *existing.DraftVersionID, now); err != nil {
						return err
					}
				}
				var active database.InspectionOriginPromotion
				activeErr := tx.Where("tenant_id=? AND asset_id=? AND id<>? AND status IN ('PREPARING','ACTIVATING')", c.TenantID, s.asset.ID, existing.ID).First(&active).Error
				if activeErr == nil {
					return apperror.New(apperror.Conflict, "inspectionId", "Já existe uma promoção em andamento para este imóvel.")
				}
				if !errors.Is(activeErr, gorm.ErrRecordNotFound) {
					return activeErr
				}
				if err := tx.Model(&existing).Updates(map[string]any{"status": "PREPARING", "failure_code": nil, "failure_reason": nil, "retryable": false, "updated_at": now, "started_at": now}).Error; err != nil {
					return translateUnique(err)
				}
				existing.Status = "PREPARING"
				existing.FailureCode = nil
				existing.FailureReason = nil
				existing.Retryable = false
				result.Promotion = existing
				return enqueue(tx, c.TenantID, existing.ID, now)
			}
			result.Promotion = existing
			if existing.OriginVersionID != nil {
				var version database.OriginVersion
				if tx.Where("tenant_id=? AND id=?", c.TenantID, *existing.OriginVersionID).First(&version).Error == nil {
					result.OriginVersion = &version
				}
			}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if s.origin.ID == (identity.ID{}) {
			s.origin = database.Origin{ID: identity.NewID(), TenantID: c.TenantID, AssetID: s.asset.ID, Version: 1, CreatedAt: now, UpdatedAt: now}
			if err := tx.Create(&s.origin).Error; err != nil {
				return translateUnique(err)
			}
		}
		view := buildStatus(tx, c.TenantID, s)
		if view.GeneratedVersionActive && s.origin.ActiveVersionID != nil {
			var active database.OriginVersion
			if err := tx.Where("tenant_id=? AND id=?", c.TenantID, *s.origin.ActiveVersionID).First(&active).Error; err == nil {
				p := newPromotion(c, s, principal.MembershipID, now, view.ConfirmationDigest)
				p.Status = "COMPLETED"
				p.OriginVersionID = &active.ID
				p.CompletedAt = &now
				if err := tx.Create(&p).Error; err != nil {
					return translateUnique(err)
				}
				result = Result{Promotion: p, OriginVersion: &active}
				return nil
			}
		}
		if err := impedimentError(view); err != nil {
			return err
		}
		if c.ConfirmationDigest != view.ConfirmationDigest {
			return apperror.New(apperror.Conflict, "confirmationDigest", "As informações do imóvel mudaram. Consulte novamente e confirme a promoção.")
		}
		var activePromotion database.InspectionOriginPromotion
		if err := tx.Where("tenant_id=? AND asset_id=? AND status IN ('PREPARING','ACTIVATING')", c.TenantID, s.asset.ID).First(&activePromotion).Error; err == nil {
			return apperror.New(apperror.Conflict, "inspectionId", "Já existe uma promoção em andamento para este imóvel.")
		}
		ownDraft := s.draft != nil && s.draft.SourceInspectionID != nil && *s.draft.SourceInspectionID == s.inspection.ID && (s.draft.Source == "CAPTURE" || s.draft.Source == "INSPECTION_PROMOTION")
		if ownDraft {
			stale, err := failedEmptyPromotionDraft(tx, c.TenantID, *s.draft)
			if err != nil {
				return err
			}
			if stale {
				if err := discardDraft(tx, c.TenantID, *s.draft, principal.MembershipID, now); err != nil {
					return err
				}
				s.draft, ownDraft = nil, false
			}
		}
		var replacedID *identity.ID
		var replacedState *string
		if s.draft != nil && !ownDraft {
			if c.ReplaceDraftID == nil || *c.ReplaceDraftID != s.draft.ID {
				return apperror.New(apperror.Conflict, "replaceDraftId", "Confirme a substituição do rascunho aberto deste imóvel.")
			}
			id := s.draft.ID
			replacedID = &id
			if s.draft.DraftState != nil {
				st := *s.draft.DraftState
				replacedState = &st
			}
			if err := discardDraft(tx, c.TenantID, *s.draft, principal.MembershipID, now); err != nil {
				return err
			}
		}
		promotion := newPromotion(c, s, principal.MembershipID, now, view.ConfirmationDigest)
		promotion.ReplacedDraftID = replacedID
		promotion.ReplacedDraftStateSeen = replacedState
		if s.inspection.MediaKind == "VIDEO" && s.plan != nil {
			pid := s.plan.ID
			generation := s.plan.Generation
			promotion.VideoPlanID = &pid
			promotion.AnalysisGeneration = &generation
		}
		if err := tx.Create(&promotion).Error; err != nil {
			return translateUnique(err)
		}
		if ownDraft {
			id := s.draft.ID
			promotion.DraftVersionID = &id
			// A failed own draft keeps its human decisions; its failed jobs run again.
			if _, err := restartFailedDraft(tx, c.TenantID, id, now); err != nil {
				return err
			}
		} else {
			capture, err := submittedCapture(tx, c.TenantID, c.InspectionID)
			if err != nil {
				return err
			}
			promotion.CaptureDraftID = capture.ID
			draft, err := d.Inventory.CreateDraft(tx, core.DraftInput{TenantID: c.TenantID, AssetID: s.asset.ID, Source: "INSPECTION_PROMOTION", SourceInspectionID: s.inspection.ID, ActorMembershipID: principal.MembershipID})
			if err != nil {
				return err
			}
			promotion.DraftVersionID = &draft.ID
			copied, err := copyPreviousCatalog(tx, c.TenantID, s.inspection.ID, capture, draft.ID, now)
			if err != nil {
				return err
			}
			if !copied {
				jobs, prompt, generation, err := prepareCatalog(ctx, tx, d, c.TenantID, s.inspection, capture, draft.ID, now)
				if err != nil {
					return err
				}
				if jobs == 0 {
					return apperror.New(apperror.InvalidState, "inspectionId", "A captura não contém ambientes com evidências para preparar o inventário.")
				}
				promotion.CatalogPromptSnapshotID = prompt
				promotion.CatalogGeneration = &generation
			}
		}
		if err := tx.Model(&promotion).Updates(map[string]any{"capture_draft_id": promotion.CaptureDraftID, "draft_version_id": promotion.DraftVersionID, "catalog_prompt_snapshot_id": promotion.CatalogPromptSnapshotID, "catalog_generation": promotion.CatalogGeneration, "video_plan_id": promotion.VideoPlanID, "analysis_generation": promotion.AnalysisGeneration}).Error; err != nil {
			return err
		}
		result.Promotion = promotion
		if err := tx.Create(&database.AuditEvent{ID: identity.NewID(), TenantID: c.TenantID, ActorID: principal.MembershipID, Action: "origin.inspection_promotion_requested", TargetType: "INSPECTION_ORIGIN_PROMOTION", TargetID: promotion.ID.String(), Outcome: "SUCCESS", CorrelationID: "inspection-promotion:" + promotion.ID.String(), OccurredAt: now}).Error; err != nil {
			return err
		}
		return enqueue(tx, c.TenantID, promotion.ID, now)
	})
	return result, err
}

func newPromotion(c Command, s inspected, requestedBy identity.ID, now time.Time, digest string) database.InspectionOriginPromotion {
	return database.InspectionOriginPromotion{ID: identity.NewID(), TenantID: c.TenantID, AssetID: s.asset.ID, OriginID: s.origin.ID, InspectionID: s.inspection.ID, ClientMutationID: c.ClientMutationID, RequestedBy: requestedBy, Purpose: c.Purpose, Status: "PREPARING", SourceKind: s.inspection.MediaKind, CaptureDraftID: s.capture.ID, ExpectedOriginVersion: int(s.origin.Version), ConfirmationDigest: digest, CreatedAt: now, UpdatedAt: now, StartedAt: &now}
}

func discardDraft(tx *gorm.DB, tenant identity.ID, draft database.OriginVersion, actor identity.ID, now time.Time) error {
	before := draft
	draft.Status = "DISCARDED"
	draft.DiscardedAt = &now
	if err := tx.Model(&database.OriginVersion{}).Where("tenant_id=? AND id=? AND status='DRAFT'", tenant, draft.ID).Updates(map[string]any{"status": "DISCARDED", "discarded_at": now}).Error; err != nil {
		return err
	}
	if err := tx.Model(&database.InventoryJob{}).Where("tenant_id=? AND origin_version_id=? AND status IN ('PENDING','RUNNING')", tenant, draft.ID).Updates(map[string]any{"status": "CANCELED", "lease_token": nil, "lease_until": nil, "updated_at": now}).Error; err != nil {
		return err
	}
	return tx.Create(&database.InventoryDecision{ID: identity.NewID(), TenantID: tenant, OriginVersionID: draft.ID, TargetType: "VERSION", TargetID: draft.ID, Action: "DISCARD", Before: []byte(fmt.Sprintf(`{"status":%q,"draftState":%q}`, before.Status, stringValue(before.DraftState))), After: []byte(fmt.Sprintf(`{"status":"DISCARDED","discardedAt":%q}`, now.Format(time.RFC3339Nano))), ActorMembershipID: &actor, OccurredAt: now, CreatedAt: now}).Error
}

// failedEmptyPromotionDraft reports whether draft was produced by a promotion that failed and holds no items.
// Such a draft carries no human decision, so a new request must catalogue again instead of reusing it.
func failedEmptyPromotionDraft(tx *gorm.DB, tenant identity.ID, draft database.OriginVersion) (bool, error) {
	if draft.Source != "INSPECTION_PROMOTION" {
		return false, nil
	}
	var failed int64
	if err := tx.Model(&database.InspectionOriginPromotion{}).Where("tenant_id=? AND draft_version_id=? AND status='FAILED'", tenant, draft.ID).Count(&failed).Error; err != nil || failed == 0 {
		return false, err
	}
	var items int64
	if err := tx.Model(&database.InventoryItem{}).Where("tenant_id=? AND origin_version_id=?", tenant, draft.ID).Count(&items).Error; err != nil {
		return false, err
	}
	return items == 0, nil
}

// impedimentError translates the first blocking condition into the public error.
func impedimentError(view StatusView) error {
	if len(view.Impediments) == 0 {
		return nil
	}
	first := view.Impediments[0]
	if first.Code == "PROMOTION_IN_PROGRESS" {
		return apperror.New(apperror.Conflict, "inspectionId", "Já existe uma promoção em andamento para este imóvel.")
	}
	return apperror.New(apperror.InvalidState, "inspectionId", first.Message)
}

// restartFailedDraft requeues the failed catalog jobs of a failed draft. It
// reports false and leaves the draft FAILED when no job could be requeued, so
// the promotion fails again instead of waiting for jobs that do not exist.
func restartFailedDraft(tx *gorm.DB, tenant, draftID identity.ID, now time.Time) (bool, error) {
	var draft database.OriginVersion
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=?", tenant, draftID).Take(&draft).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	if draft.Status != "DRAFT" || draft.DraftState == nil || *draft.DraftState != "FAILED" {
		return false, nil
	}
	jobs := tx.Model(&database.InventoryJob{}).Where("tenant_id=? AND origin_version_id=? AND status='FAILED'", tenant, draftID).Updates(map[string]any{"status": "PENDING", "attempts": 0, "next_attempt_at": now, "error_code": nil, "lease_token": nil, "lease_until": nil, "updated_at": now})
	if jobs.Error != nil || jobs.RowsAffected == 0 {
		return false, jobs.Error
	}
	return true, tx.Model(&database.OriginVersion{}).Where("tenant_id=? AND id=? AND draft_state='FAILED'", tenant, draftID).Update("draft_state", "PREPARING").Error
}

func enqueue(tx *gorm.DB, tenant, promotion identity.ID, now time.Time) error {
	return messaging.AddOutbox(tx, events.Envelope[map[string]any]{ID: identity.NewID(), Type: "origin.inspection_promotion_requested.v1", SchemaVersion: 1, OccurredAt: now, TenantID: tenant, AggregateID: promotion, CorrelationID: "inspection-promotion:" + promotion.String(), Payload: map[string]any{"promotionId": promotion}})
}

func translateUnique(err error) error {
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "duplicate key") {
		return apperror.New(apperror.Conflict, "inspectionId", "Já existe uma promoção em andamento para este imóvel.")
	}
	return err
}
func stringValue(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
