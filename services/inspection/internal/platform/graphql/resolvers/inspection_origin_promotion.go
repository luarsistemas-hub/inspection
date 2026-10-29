package resolvers

import (
	"context"
	"errors"
	"time"

	"inspection/libs/identity"
	promoteorigin "inspection/services/inspection/internal/features/origins/promote_inspection_to_origin"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	graphql1 "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
)

func (r *queryResolver) inspectionOriginPromotion(ctx context.Context, inspectionID string) (*graphql1.InspectionOriginPromotionStatusView, error) {
	meta, ok := requestctx.FromContext(ctx)
	if !ok {
		return nil, unauthenticated()
	}
	id, err := identity.ParseID(inspectionID)
	if err != nil {
		return nil, invalidID("inspectionId")
	}
	raw, err := r.Bus.Ask(ctx, promoteorigin.Query{TenantID: meta.TenantID, InspectionID: id})
	if err != nil {
		return nil, err
	}
	view := raw.(promoteorigin.StatusView)
	out := &graphql1.InspectionOriginPromotionStatusView{InspectionID: id.String(), Eligible: view.Eligible, ConfirmationDigest: view.ConfirmationDigest, AutomaticCatalogSkipped: view.AutomaticCatalogSkipped, GeneratedVersionActive: view.GeneratedVersionActive, Impediments: make([]*graphql1.InspectionOriginPromotionImpediment, 0, len(view.Impediments))}
	for _, item := range view.Impediments {
		out.Impediments = append(out.Impediments, &graphql1.InspectionOriginPromotionImpediment{Code: item.Code, Message: item.Message})
	}
	if view.ActiveOrigin != nil {
		out.ActiveOrigin = &graphql1.InspectionOriginPromotionActiveOrigin{ID: view.ActiveOrigin.VersionID.String(), VersionNumber: view.ActiveOrigin.Number, MediaKind: view.ActiveOrigin.MediaKind, HasInventory: view.ActiveOrigin.HasInventory}
	}
	if view.ConflictingDraft != nil {
		draft := view.ConflictingDraft
		out.ConflictingDraft = &graphql1.InspectionOriginPromotionConflictingDraft{ID: draft.ID.String(), Source: draft.Source, DraftState: draft.DraftState, OwnInspection: draft.OwnInspection}
		if draft.SourceInspectionID != nil {
			id := draft.SourceInspectionID.String()
			out.ConflictingDraft.SourceInspectionID = &id
		}
	}
	if view.CurrentPromotion != nil {
		out.CurrentPromotion, err = r.inspectionPromotionOperation(ctx, meta.TenantID, *view.CurrentPromotion)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (r *mutationResolver) promoteInspectionToOrigin(ctx context.Context, input graphql1.PromoteInspectionToOriginInput) (*graphql1.PromoteInspectionToOriginPayload, error) {
	meta, ok := requestctx.FromContext(ctx)
	if !ok {
		return nil, unauthenticated()
	}
	out := &graphql1.PromoteInspectionToOriginPayload{ClientMutationID: input.ClientMutationID, UserErrors: []*graphql1.UserError{}}
	id, err := identity.ParseID(input.InspectionID)
	if err != nil {
		out.UserErrors = append(out.UserErrors, userErrorFrom(invalidID("inspectionId")))
		return out, nil
	}
	var replaceID *identity.ID
	if input.ReplaceDraftID != nil {
		parsed, parseErr := identity.ParseID(*input.ReplaceDraftID)
		if parseErr != nil {
			out.UserErrors = append(out.UserErrors, userErrorFrom(invalidID("replaceDraftId")))
			return out, nil
		}
		replaceID = &parsed
	}
	purpose := "INVENTORY_UPDATE"
	if input.Purpose != nil {
		purpose = *input.Purpose
	}
	resultRaw, err := r.Bus.Send(ctx, promoteorigin.Command{TenantID: meta.TenantID, InspectionID: id, ClientMutationID: requestctx.IdempotencyKey(ctx, input.ClientMutationID), Purpose: purpose, ReplaceDraftID: replaceID, ConfirmationDigest: input.ConfirmationDigest})
	if err != nil {
		code, _, _ := apperror.Public(err)
		switch code {
		case apperror.InvalidInput, apperror.InvalidState, apperror.Conflict, apperror.NotFound:
			out.UserErrors = append(out.UserErrors, userErrorFrom(err))
			return out, nil
		default:
			return nil, err
		}
	}
	result := resultRaw.(promoteorigin.Result)
	out.Promotion, err = r.inspectionPromotionOperation(ctx, meta.TenantID, result.Promotion)
	if err != nil {
		return nil, err
	}
	if result.OriginVersion != nil {
		version, projectErr := r.projectOriginVersion(ctx, meta.TenantID, *result.OriginVersion)
		if projectErr != nil {
			return nil, projectErr
		}
		out.OriginVersion = version
	}
	return out, nil
}

func (r *Resolver) inspectionPromotionOperation(ctx context.Context, tenant identity.ID, row database.InspectionOriginPromotion) (*graphql1.InspectionOriginPromotionOperation, error) {
	out := &graphql1.InspectionOriginPromotionOperation{ID: row.ID.String(), Status: graphql1.InspectionOriginPromotionStatus(row.Status), Purpose: row.Purpose, Retryable: row.Retryable}
	out.FailureCode, out.FailureReason = row.FailureCode, row.FailureReason
	out.DraftVersionID = idStringPointer(row.DraftVersionID)
	out.ReplacedDraftID = idStringPointer(row.ReplacedDraftID)
	out.SupersededVersionID = idStringPointer(row.SupersededVersionID)
	requested := row.CreatedAt.UTC().Format(time.RFC3339Nano)
	out.RequestedAt = &requested
	if row.CompletedAt != nil {
		completed := row.CompletedAt.UTC().Format(time.RFC3339Nano)
		out.CompletedAt = &completed
	}
	if row.OriginVersionID != nil {
		var version database.OriginVersion
		err := (tenanttx.Runner{DB: r.DB}).Within(ctx, tenant, func(tx *gorm.DB) error {
			return tx.Where("tenant_id=? AND id=?", tenant, *row.OriginVersionID).First(&version).Error
		})
		if err == nil {
			mapped, err := r.projectOriginVersion(ctx, tenant, version)
			if err != nil {
				return nil, err
			}
			out.OriginVersion = mapped
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	}
	return out, nil
}

func idStringPointer(id *identity.ID) *string {
	if id == nil {
		return nil
	}
	value := id.String()
	return &value
}
