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
	} else if err != nil {
		return Result{}, apperror.New(apperror.InvalidInput, "referenceVersionId", "active origin is required")
	}
	selectedID := origin.ActiveVersionID
	if query.VersionID != nil {
		selectedID = query.VersionID
	}
	if selectedID == nil {
		return Result{}, apperror.New(apperror.InvalidInput, "referenceVersionId", "active origin is required")
	}
	if err := tx.Where("tenant_id=? AND id=? AND origin_id=?", query.TenantID, *selectedID, origin.ID).Take(&result.Version).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return Result{}, apperror.New(apperror.NotFound, "referenceVersionId", "resource not found")
		}
		return Result{}, err
	}
	if result.Version.HasInventory {
		if result.Version.Status != "ACTIVE" && result.Version.Status != "SUPERSEDED" {
			return Result{}, apperror.New(apperror.InvalidState, "referenceVersionId", "inventory reference version is not pinnable")
		}
		var environments []database.InventoryEnvironment
		if err := tx.Where("tenant_id=? AND origin_version_id=? AND decision<>'REJECTED'", query.TenantID, result.Version.ID).Order("position,id").Find(&environments).Error; err != nil {
			return Result{}, err
		}
		if len(environments) == 0 || len(environments) > 60 {
			return Result{}, apperror.New(apperror.InvalidState, "referenceVersionId", "inventory version has no usable environments")
		}
		kind := query.MediaKind
		if kind != "PHOTO" && kind != "VIDEO" {
			kind = "PHOTO"
		}
		maximum := 20
		if kind == "VIDEO" {
			maximum = 3
		}
		for _, environment := range environments {
			key := "env:" + environment.IdentityID.String()
			requirement := capturecore.Requirement{Key: key, Section: environment.TypeCode, Label: environment.Name, EnvironmentIdentityID: environment.IdentityID.String(), EnvironmentTypeCode: environment.TypeCode, EnvironmentName: environment.Name, Expected: true, EvidenceKind: kind, Required: true, ImpossibilityAllowed: true, MinimumMedia: 1, MaximumMedia: maximum, CaptureSourcePolicy: "CAMERA_DEFAULT", ComparisonTarget: "FIXED_ORIGIN"}
			result.Requirements = append(result.Requirements, requirement)
		}
		// Inventory environments are the complete cross-modality reference.
		// Do not mix legacy origin:* requirements or video-scope evidence in.
		return result, nil
	} else {
		if origin.ActiveVersionID == nil || *selectedID != *origin.ActiveVersionID {
			return Result{}, apperror.New(apperror.Conflict, "referenceVersionId", "origin reference is no longer active")
		}
		if result.Version.Status != "ACTIVE" {
			return Result{}, apperror.New(apperror.InvalidState, "referenceVersionId", "active origin is unavailable")
		}
	}
	versionKind := result.Version.MediaKind
	if versionKind == "" {
		versionKind = "PHOTO"
	}
	if !result.Version.HasInventory && query.MediaKind != "" && query.MediaKind != versionKind {
		return Result{}, apperror.New(apperror.InvalidState, "referenceVersionId", "CATALOG_ORIGIN_FIRST")
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
		if !result.Version.HasInventory {
			result.Requirements = []capturecore.Requirement{capturecore.VideoMainRequirement("FIXED_ORIGIN")}
		}
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
