// Package resolve_reference exposes the active immutable origin snapshot to
// occurrence creation without leaking origin persistence across capabilities.
package resolve_reference

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"inspection/libs/identity"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/tenanttx"
	"inspection/services/inspection/internal/platform/video"

	"gorm.io/gorm"
)

type Query struct {
	TenantID, AssetID identity.ID
	VersionID         *identity.ID
	MediaKind         string
}

type Result struct {
	Version      database.OriginVersion
	Evidence     []Evidence
	VideoScope   []VideoEvidence
	Requirements []capturecore.Requirement
}

// Evidence is the public, immutable origin projection used by capture.
type Evidence struct {
	MediaID        identity.ID `json:"mediaId"`
	Category       string      `json:"category"`
	Description    string      `json:"description"`
	AttentionItems []string    `json:"attentionItems"`
}

// VideoEvidence identifies a retained representative frame and its derived scope.
type VideoEvidence struct {
	ScopeItemID          identity.ID `json:"scopeItemId"`
	FrameID              identity.ID `json:"frameId"`
	SourceMediaID        identity.ID `json:"sourceMediaId"`
	PTSMicros            int64       `json:"ptsMicros"`
	EnvironmentCandidate string      `json:"environmentCandidate"`
	ComponentLabel       string      `json:"componentLabel"`
	ViewLabel            string      `json:"viewLabel"`
	Visibility           string      `json:"visibility"`
	Gap                  bool        `json:"gap"`
	Ambiguous            bool        `json:"ambiguous"`
}

type Dependencies struct {
	DB  *gorm.DB
	Bus *mediator.Bus
}

func Setup(d Dependencies) error {
	if d.DB == nil || d.Bus == nil {
		return fmt.Errorf("slice origins/resolve_reference: missing dependency")
	}
	return d.Bus.RegisterQuery(Query{}, func(ctx context.Context, raw any) (any, error) {
		query := raw.(Query)
		var result Result
		err := (tenanttx.Runner{DB: d.DB}).Within(ctx, query.TenantID, func(tx *gorm.DB) error {
			var resolveErr error
			result, resolveErr = resolve(tx, query)
			return resolveErr
		})
		return result, err
	})
}

func resolve(tx *gorm.DB, query Query) (Result, error) {
	var result Result
	var origin database.Origin
	if err := tx.Where("tenant_id=? AND asset_id=?", query.TenantID, query.AssetID).First(&origin).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return Result{}, err
	} else if err != nil || origin.ActiveVersionID == nil {
		return Result{}, apperror.New(apperror.InvalidInput, "referenceVersionId", "active origin is required")
	}
	if query.VersionID != nil && *query.VersionID != *origin.ActiveVersionID {
		var requested database.OriginVersion
		if err := tx.Where("tenant_id=? AND id=?", query.TenantID, *query.VersionID).Take(&requested).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return Result{}, apperror.New(apperror.NotFound, "", "resource not found")
			}
			return Result{}, err
		}
		return Result{}, apperror.New(apperror.Conflict, "referenceVersionId", "origin reference is no longer active")
	}
	if err := tx.Where("tenant_id=? AND id=? AND status='ACTIVE'", query.TenantID, *origin.ActiveVersionID).First(&result.Version).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return Result{}, apperror.New(apperror.InvalidState, "referenceVersionId", "active origin is unavailable")
	} else if err != nil {
		return Result{}, err
	}
	versionKind := result.Version.MediaKind
	if versionKind == "" {
		versionKind = "PHOTO"
	}
	if query.MediaKind != "" && query.MediaKind != versionKind {
		return Result{}, apperror.New(apperror.InvalidInput, "referenceVersionId", "reference modality must match the inspection modality")
	}
	if versionKind == "VIDEO" {
		if result.Version.VideoPreparationID == nil || result.Version.ScopeManifestDigest == "" {
			return Result{}, apperror.New(apperror.InvalidState, "referenceVersionId", "video origin has no pinned prepared scope")
		}
		var prep database.VideoPreparationJob
		if err := tx.Where("tenant_id=? AND id=? AND status='READY' AND manifest_digest=?", query.TenantID, *result.Version.VideoPreparationID, result.Version.ScopeManifestDigest).Take(&prep).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return Result{}, apperror.New(apperror.InvalidState, "referenceVersionId", "video origin preparation is unavailable")
		} else if err != nil {
			return Result{}, err
		}
		var rows []database.VideoScopeItem
		if err := tx.Where("tenant_id=? AND preparation_id=?", query.TenantID, prep.ID).Order("created_at,id").Find(&rows).Error; err != nil {
			return Result{}, err
		}
		if len(rows) == 0 {
			return Result{}, apperror.New(apperror.InvalidState, "referenceVersionId", "video origin has no usable reference scope")
		}
		frameIDs := make([]identity.ID, 0, len(rows))
		for _, item := range rows {
			frameIDs = append(frameIDs, item.FrameID)
		}
		var frames []database.VideoFrame
		if err := tx.Where("tenant_id=? AND preparation_id=? AND source_media_id=? AND id IN ? AND sha256<>'' AND object_key<>''", query.TenantID, prep.ID, prep.MediaID, frameIDs).Find(&frames).Error; err != nil {
			return Result{}, err
		}
		byID := make(map[identity.ID]database.VideoFrame, len(frames))
		for _, frame := range frames {
			byID[frame.ID] = frame
		}
		for _, item := range rows {
			frame, ok := byID[item.FrameID]
			if !ok {
				return Result{}, apperror.New(apperror.InvalidState, "referenceVersionId", "video origin frame lineage is incomplete")
			}
			result.VideoScope = append(result.VideoScope, VideoEvidence{ScopeItemID: item.ID, FrameID: frame.ID, SourceMediaID: prep.MediaID, PTSMicros: frame.PTSMicros, EnvironmentCandidate: item.EnvironmentCandidate, ComponentLabel: item.ComponentLabel, ViewLabel: item.ViewLabel, Visibility: item.Visibility, Ambiguous: item.IdentityStatus == "AMBIGUOUS", Gap: item.Visibility == video.VisibilityObscured})
		}
		result.Requirements = []capturecore.Requirement{capturecore.VideoMainRequirement("FIXED_ORIGIN")}
		return result, nil
	}
	var rows []database.OriginEvidence
	if err := tx.Where("tenant_id=? AND origin_version_id=?", query.TenantID, result.Version.ID).Order("created_at,id").Find(&rows).Error; err != nil {
		return Result{}, err
	}
	if len(rows) == 0 || len(rows) > capturecore.MaxActivePhotos {
		return Result{}, apperror.New(apperror.InvalidState, "referenceVersionId", "origin evidence must fit the capture limit")
	}
	for _, item := range rows {
		attentionItems := []string{}
		if len(item.AttentionItems) > 0 && string(item.AttentionItems) != "null" {
			if err := json.Unmarshal(item.AttentionItems, &attentionItems); err != nil {
				return Result{}, fmt.Errorf("origin evidence has invalid attention items")
			}
		}
		result.Evidence = append(result.Evidence, Evidence{MediaID: item.MediaID, Category: item.Category, Description: item.Description, AttentionItems: attentionItems})
		result.Requirements = append(result.Requirements, capturecore.Requirement{
			Key:                  "origin:" + item.MediaID.String(),
			Section:              item.Category,
			Label:                item.Category,
			Instructions:         item.Description,
			EvidenceKind:         "PHOTO",
			Required:             true,
			ImpossibilityAllowed: true,
			MinimumMedia:         1,
			MaximumMedia:         1,
			CaptureSourcePolicy:  "CAMERA_DEFAULT",
			ComparisonTarget:     "FIXED_ORIGIN",
		})
	}
	return result, nil
}
