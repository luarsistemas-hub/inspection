package resolvers

import (
	"context"
	"encoding/base64"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	graphql1 "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/gorm"
)

type preparationEntry struct {
	item *graphql1.VideoPreparationEvidenceItem
	kind string
	key  string
	id   string
}

func (r *queryResolver) videoPreparationEvidence(ctx context.Context, preparationID string, first *int, after *string) (*graphql1.VideoPreparationEvidenceConnection, error) {
	meta, ok := requestctx.FromContext(ctx)
	if !ok {
		return nil, unauthenticated()
	}
	if err := internalRole(meta, auth.Manager, auth.Employee, auth.Viewer); err != nil {
		return nil, err
	}
	prepID, err := identity.ParseID(preparationID)
	if err != nil {
		return nil, invalidID("preparationId")
	}
	limit, err := pageLimit(first)
	if err != nil {
		return nil, err
	}
	cursorKind, cursorKey, cursorID, err := decodePreparationCursor(stringValue(after), meta.TenantID, prepID)
	if err != nil {
		return nil, graphql1Error("after")
	}
	if r.DB == nil {
		return nil, apperror.New(apperror.DependencyUnavailable, "", "database unavailable")
	}
	var job database.VideoPreparationJob
	var frames []database.VideoFrame
	var scopes []database.VideoScopeItem
	err = withTask06Tenant(ctx, r.DB, meta.TenantID, func(tx *gorm.DB) error {
		if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", meta.TenantID, prepID).Take(&job).Error; err != nil {
			return err
		}
		if err := authorizePreparationRead(ctx, tx, meta, job.MediaID); err != nil {
			return err
		}
		frameQuery := tx.WithContext(ctx).Where("tenant_id=? AND preparation_id=?", meta.TenantID, prepID)
		if cursorKind == "SCOPE" {
			frameQuery = frameQuery.Where("1=0")
		}
		if cursorKind == "FRAME" {
			pts, parseErr := strconv.ParseInt(cursorKey, 10, 64)
			if parseErr != nil {
				return parseErr
			}
			frameQuery = frameQuery.Where("(pts_us > ?) OR (pts_us = ? AND id > ?)", pts, pts, cursorID)
		}
		if err := frameQuery.Order("pts_us ASC, id ASC").Limit(limit + 1).Find(&frames).Error; err != nil {
			return err
		}
		scopeQuery := tx.WithContext(ctx).Where("tenant_id=? AND preparation_id=?", meta.TenantID, prepID)
		if cursorKind == "SCOPE" {
			at, parseErr := time.Parse(time.RFC3339Nano, cursorKey)
			if parseErr != nil {
				return parseErr
			}
			scopeQuery = scopeQuery.Where("(created_at > ?) OR (created_at = ? AND id > ?)", at, at, cursorID)
		}
		return scopeQuery.Order("created_at ASC, id ASC").Limit(limit + 1).Find(&scopes).Error
	})
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, apperror.New(apperror.NotFound, "preparationId", "preparation not found")
		}
		return nil, err
	}
	entries := make([]preparationEntry, 0, len(frames)+len(scopes))
	for _, frame := range frames {
		pts := strconv.FormatInt(frame.PTSMicros, 10)
		ms := int(frame.PTSMicros / 1000)
		entries = append(entries, preparationEntry{kind: "FRAME", key: fmt.Sprintf("%020d", frame.PTSMicros), id: frame.ID.String(), item: &graphql1.VideoPreparationEvidenceItem{Kind: "FRAME", ID: frame.ID.String(), FrameID: strPtr(frame.ID.String()), PreparationID: strPtr(prepID.String()), SourceMediaID: strPtr(frame.SourceMediaID.String()), PtsMicros: &pts, TimestampMs: &ms, Role: strPtr(frame.Role)}})
	}
	for _, scope := range scopes {
		key := scope.CreatedAt.UTC().Format(time.RFC3339Nano)
		frameID := scope.FrameID.String()
		entries = append(entries, preparationEntry{kind: "SCOPE", key: key, id: scope.ID.String(), item: &graphql1.VideoPreparationEvidenceItem{Kind: "SCOPE", ID: scope.ID.String(), FrameID: &frameID, PreparationID: strPtr(prepID.String()), Component: strPtr(scope.ComponentLabel), Label: strPtr(scope.ComponentLabel), Environment: strPtr(scope.EnvironmentCandidate), View: strPtr(scope.ViewLabel)}})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].kind != entries[j].kind {
			return entries[i].kind < entries[j].kind
		}
		if entries[i].key != entries[j].key {
			return entries[i].key < entries[j].key
		}
		return entries[i].id < entries[j].id
	})
	more := len(entries) > limit
	if more {
		entries = entries[:limit]
	}
	nodes := make([]*graphql1.VideoPreparationEvidenceItem, 0, len(entries))
	for _, entry := range entries {
		nodes = append(nodes, entry.item)
	}
	end := ""
	if len(entries) > 0 {
		last := entries[len(entries)-1]
		end = encodePreparationCursor(meta.TenantID, prepID, last.kind, last.key, last.id)
	}
	return &graphql1.VideoPreparationEvidenceConnection{Nodes: nodes, PageInfo: pageInfo(end, more)}, nil
}

func authorizePreparationRead(ctx context.Context, tx *gorm.DB, meta requestctx.Metadata, mediaID identity.ID) error {
	if hasRole(meta, auth.TenantAdmin) {
		return nil
	}
	var media database.MediaObject
	if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", meta.TenantID, mediaID).Take(&media).Error; err != nil {
		return err
	}
	var responsibility database.Responsibility
	if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", meta.TenantID, media.ResponsibilityID).Take(&responsibility).Error; err != nil {
		return err
	}
	return authorizeReportInspection(ctx, tx, meta, responsibility.InspectionID)
}

func encodePreparationCursor(tenantID, prepID identity.ID, kind, key, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("p1|%s|%s|%s|%s|%s", tenantID, prepID, kind, key, id)))
}

func decodePreparationCursor(value string, tenantID, prepID identity.ID) (kind, key, id string, err error) {
	if value == "" {
		return "", "", "", nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return "", "", "", err
	}
	parts := strings.SplitN(string(decoded), "|", 6)
	if len(parts) != 6 || parts[0] != "p1" || parts[1] != tenantID.String() || parts[2] != prepID.String() || (parts[3] != "FRAME" && parts[3] != "SCOPE") {
		return "", "", "", fmt.Errorf("invalid cursor")
	}
	if _, err := identity.ParseID(parts[5]); err != nil {
		return "", "", "", err
	}
	if parts[3] == "FRAME" {
		if _, err := strconv.ParseInt(parts[4], 10, 64); err != nil {
			return "", "", "", err
		}
	} else if _, err := time.Parse(time.RFC3339Nano, parts[4]); err != nil {
		return "", "", "", err
	}
	return parts[3], parts[4], parts[5], nil
}
func strPtr(value string) *string { return &value }
