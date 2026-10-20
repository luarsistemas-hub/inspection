package resolvers

import (
	"context"
	"time"

	"inspection/libs/identity"
	mediareview "inspection/services/inspection/internal/features/media/review_sensitive_media"
	"inspection/services/inspection/internal/platform/apperror"
	graphql1 "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/gorm"
)

func sensitiveFalsePositiveFailure(err error, mutationID string) (*graphql1.MediaPayload, error) {
	code, _, _ := apperror.Public(err)
	switch code {
	case apperror.InvalidInput, apperror.InvalidState, apperror.Conflict, apperror.SourceUnavailable:
		return &graphql1.MediaPayload{UserErrors: []*graphql1.UserError{userErrorFrom(err)}, ClientMutationID: mutationID}, nil
	default:
		return nil, err
	}
}
func (r *queryResolver) attentionMediaID(ctx context.Context, tenantID, inspectionID identity.ID) *string {
	if r.DB == nil {
		return nil
	}
	var mediaID identity.ID
	err := withTask06Tenant(ctx, r.DB, tenantID, func(tx *gorm.DB) error {
		return tx.Table("analysis.video_plans vp").Select("vj.media_id").Joins("JOIN media.video_preparation_jobs vj ON vj.tenant_id=vp.tenant_id AND vj.id=vp.current_preparation_id").Where("vp.tenant_id=? AND vp.inspection_id=? AND vp.status='BLOCKED'", tenantID, inspectionID).Order("vp.created_at DESC").Limit(1).Scan(&mediaID).Error
	})
	if err != nil || mediaID == (identity.ID{}) {
		return nil
	}
	value := mediaID.String()
	return &value
}
func (r *queryResolver) sensitiveMediaReviews(ctx context.Context, first *int, after *string) (*graphql1.SensitiveMediaReviewConnection, error) {
	meta, ok := requestctx.FromContext(ctx)
	if !ok {
		return nil, unauthenticated()
	}
	raw, err := r.Bus.Ask(ctx, mediareview.Query{TenantID: meta.TenantID, First: intValue(first), After: stringValue(after)})
	if err != nil {
		return nil, err
	}
	result := raw.(mediareview.Connection)
	nodes := make([]*graphql1.SensitiveMediaReview, 0, len(result.Nodes))
	for _, item := range result.Nodes {
		kind := graphql1.SensitiveMediaKindPhoto
		if item.MediaKind == "VIDEO" {
			kind = graphql1.SensitiveMediaKindVideo
		}
		nodes = append(nodes, &graphql1.SensitiveMediaReview{MediaID: item.MediaID.String(), InspectionID: item.InspectionID.String(), AssetName: item.AssetName, ParticipantName: item.ParticipantName, MediaKind: kind, RequirementKey: item.RequirementKey, BlockedAt: item.BlockedAt, Submitted: item.Submitted, SourceAvailable: item.SourceAvailable})
	}
	return &graphql1.SensitiveMediaReviewConnection{Nodes: nodes, PageInfo: pageInfo(result.EndCursor, result.HasNextPage)}, nil
}
func (r *queryResolver) sensitiveMediaOriginal(ctx context.Context, mediaID string) (*graphql1.SensitiveMediaOriginal, error) {
	meta, ok := requestctx.FromContext(ctx)
	if !ok {
		return nil, unauthenticated()
	}
	id, err := identity.ParseID(mediaID)
	if err != nil {
		return nil, invalidID("mediaId")
	}
	raw, err := r.Bus.Ask(ctx, mediareview.OriginalQuery{TenantID: meta.TenantID, MediaID: id})
	if err != nil {
		return nil, err
	}
	result := raw.(mediareview.Original)
	kind := graphql1.SensitiveMediaKindPhoto
	if result.MediaKind == "VIDEO" {
		kind = graphql1.SensitiveMediaKindVideo
	}
	return &graphql1.SensitiveMediaOriginal{MediaID: result.MediaID.String(), MediaKind: kind, URL: result.URL, ExpiresAt: result.ExpiresAt.Format(time.RFC3339Nano)}, nil
}
func scopedUnitIDs(principal requestctx.Principal) []identity.ID {
	ids := []identity.ID{}
	for _, scope := range principal.Scopes {
		if scope.Kind == "BUSINESS_UNIT" {
			ids = append(ids, scope.ID)
		}
	}
	return ids
}
func inPrincipalScope(principal requestctx.Principal, unit identity.ID) bool {
	units := scopedUnitIDs(principal)
	if len(units) == 0 {
		return true
	}
	for _, candidate := range units {
		if candidate == unit {
			return true
		}
	}
	return false
}

// fillAttention completes the attention summary with the blocked media and the replacement deadline.
func (r *queryResolver) fillAttention(ctx context.Context, tenantID, inspectionID identity.ID, attention *graphql1.InspectionAttention) {
	attention.MediaID = r.attentionMediaID(ctx, tenantID, inspectionID)
	if attention.Kind != graphql1.InspectionAttentionKindReplacementRequested || r.DB == nil {
		return
	}
	var deadline time.Time
	err := withTask06Tenant(ctx, r.DB, tenantID, func(tx *gorm.DB) error {
		return tx.Table("inspections.video_replacements").Select("deadline_at").Where("tenant_id=? AND inspection_id=? AND status='REQUESTED'", tenantID, inspectionID).Order("created_at DESC").Limit(1).Scan(&deadline).Error
	})
	if err == nil && !deadline.IsZero() {
		value := deadline.UTC().Format(time.RFC3339Nano)
		attention.ReplacementDeadlineAt = &value
	}
}
