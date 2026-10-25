package process_inventory_job

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"inspection/libs/identity"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"
	"inspection/services/inspection/internal/platform/messaging"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
)

const (
	// maxImageBytes bounds one evidence image read from object storage.
	maxImageBytes = 8 << 20
	// maxFramesPerMedia keeps only representative frames of one clip.
	maxFramesPerMedia = 12
	// maxRequestImages is the gateway image cap of inventory modes.
	maxRequestImages = 24
)

type evidenceInput struct {
	ID             identity.ID         `json:"id"`
	Kind           string              `json:"kind"`
	MediaID        identity.ID         `json:"mediaId"`
	FrameID        *identity.ID        `json:"frameId,omitempty"`
	RequirementKey string              `json:"requirementKey"`
	Digest         string              `json:"digest"`
	TimestampMS    *int64              `json:"-"`
	ObjectKey      string              `json:"-"`
	Image          llm.NormalizedImage `json:"-"`
}

// jobInput is everything a job needs, read in one short tenant transaction.
type jobInput struct {
	inspection  database.Inspection
	snapshot    database.AnalysisPromptSnapshot
	environment database.InventoryEnvironment
	items       []database.InventoryItem
	originRows  []database.InventoryItemEvidence
	origin      []evidenceInput
	current     []evidenceInput
	impossible  bool
	// legacy marks a CATALOG job over a legacy origin version: the model
	// proposes the environments itself.
	legacy bool
}

func (p *Processor) within(ctx context.Context, tenantID identity.ID, fn func(*gorm.DB) error) error {
	if p.deps.Within != nil {
		return p.deps.Within(ctx, tenantID, fn)
	}
	return (tenanttx.Runner{DB: p.deps.DB}).Within(ctx, tenantID, fn)
}

func (p *Processor) load(ctx context.Context, job database.InventoryJob) (jobInput, error) {
	var in jobInput
	err := p.within(ctx, job.TenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND id=?", job.TenantID, job.InspectionID).Take(&in.inspection).Error; err != nil {
			return permanentIfMissing(err)
		}
		if job.PromptSnapshotID == nil {
			return fmt.Errorf("%w: inventory prompt snapshot missing", messaging.ErrPermanent)
		}
		task := "INVENTORY_COMPARE"
		if job.Kind == "CATALOG" {
			task = "INVENTORY_CATALOG"
		}
		if err := tx.Where("id=? AND task=?", *job.PromptSnapshotID, task).Take(&in.snapshot).Error; err != nil {
			return permanentIfMissing(err)
		}
		if job.Kind == "CATALOG" && job.EnvironmentIdentityID == nil {
			in.legacy = true
			current, err := loadLegacyOriginEvidence(tx, job)
			in.current = current
			return err
		}
		requirement, answer, err := loadCapture(tx, job)
		if err != nil {
			return err
		}
		if err := tx.Where("tenant_id=? AND origin_version_id=? AND identity_id=?", job.TenantID, job.OriginVersionID, job.EnvironmentIdentityID).Take(&in.environment).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) || job.Kind != "COMPARE" || requirement == nil {
				return permanentIfMissing(err)
			}
			// An environment declared during a comparative capture has no row in
			// the pinned reference: it has no baseline items and only yields
			// outside-inventory observations.
			in.environment = database.InventoryEnvironment{IdentityID: *job.EnvironmentIdentityID, TypeCode: requirement.EnvironmentTypeCode, Name: requirement.EnvironmentName, AddedDuringCapture: true, RequirementKey: &requirement.Key}
			if in.environment.TypeCode == "" {
				in.environment.TypeCode = "OTHER"
			}
			if in.environment.Name == "" {
				in.environment.Name = requirement.Label
			}
		} else if job.Kind == "COMPARE" {
			// Rejected and pending suggestions are not part of the baseline.
			if err := tx.Where("tenant_id=? AND origin_version_id=? AND environment_id=? AND decision IN ('ACCEPTED','EDITED','ADDED')", job.TenantID, job.OriginVersionID, in.environment.ID).Order("created_at ASC, id ASC").Find(&in.items).Error; err != nil {
				return err
			}
			if len(in.items) > 0 {
				ids := make([]identity.ID, 0, len(in.items))
				for _, item := range in.items {
					ids = append(ids, item.ID)
				}
				if err := tx.Where("tenant_id=? AND item_id IN ?", job.TenantID, ids).Order("position ASC").Find(&in.originRows).Error; err != nil {
					return err
				}
				origin, err := loadOriginEvidenceRefs(tx, job.TenantID, in.originRows, job.RequirementKey)
				if err != nil {
					return err
				}
				in.origin = origin
			}
		}
		if answer == nil {
			return nil
		}
		if answer.ImpossibilityReason != "" {
			in.impossible = true
			return nil
		}
		var mediaIDs []identity.ID
		if err := json.Unmarshal(answer.MediaIDs, &mediaIDs); err != nil {
			return fmt.Errorf("%w: invalid answer media", messaging.ErrPermanent)
		}
		current, err := loadMediaInputs(tx, job.TenantID, mediaIDs, job.RequirementKey, in.inspection.MediaKind == "VIDEO")
		in.current = current
		return err
	})
	return in, err
}

// loadCapture returns the submitted requirement and answer for the job's
// environment. Both are nil when the capture did not answer it.
func loadCapture(tx *gorm.DB, job database.InventoryJob) (*capturecore.Requirement, *database.RequirementAnswer, error) {
	var draft database.CaptureDraft
	if err := tx.Where("tenant_id=? AND responsibility_id IN (SELECT id FROM inspections.responsibilities WHERE tenant_id=? AND inspection_id=?)", job.TenantID, job.TenantID, job.InspectionID).Order("created_at DESC").Take(&draft).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	var requirements []capturecore.Requirement
	if err := json.Unmarshal(draft.Requirements, &requirements); err != nil {
		return nil, nil, fmt.Errorf("%w: invalid capture requirements", messaging.ErrPermanent)
	}
	var requirement *capturecore.Requirement
	for i := range requirements {
		if requirements[i].Key == job.RequirementKey {
			requirement = &requirements[i]
			break
		}
	}
	var answer database.RequirementAnswer
	if err := tx.Where("tenant_id=? AND draft_id=? AND requirement_key=?", job.TenantID, draft.ID, job.RequirementKey).Take(&answer).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return requirement, nil, nil
		}
		return nil, nil, err
	}
	return requirement, &answer, nil
}

// loadMediaInputs resolves the current evidence of one environment. Media that
// is not READY, or a clip whose latest preparation is not READY, is skipped:
// it simply contributes no view.
func loadMediaInputs(tx *gorm.DB, tenant identity.ID, mediaIDs []identity.ID, key string, video bool) ([]evidenceInput, error) {
	var out []evidenceInput
	for _, id := range mediaIDs {
		var media database.MediaObject
		if err := tx.Where("tenant_id=? AND id=?", tenant, id).Take(&media).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			return nil, err
		}
		if media.Status != "READY" {
			continue
		}
		if media.MediaKind == "VIDEO" || video {
			var prep database.VideoPreparationJob
			if err := tx.Where("tenant_id=? AND media_id=?", tenant, id).Order("generation DESC").Take(&prep).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					continue
				}
				return nil, err
			}
			if prep.Status != "READY" {
				continue
			}
			frames, err := representativeFrames(tx, tenant, prep.ID)
			if err != nil {
				return nil, err
			}
			for _, frame := range frames {
				out = append(out, frameInput(frame, id, key))
			}
			continue
		}
		var derivative database.MediaDerivative
		if err := tx.Where("tenant_id=? AND media_id=? AND kind='DISPLAY'", tenant, id).Take(&derivative).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			return nil, err
		}
		out = append(out, photoInput(id, derivative.ObjectKey, key, "CURRENT"))
	}
	return out, nil
}

func representativeFrames(tx *gorm.DB, tenant, preparationID identity.ID) ([]database.VideoFrame, error) {
	var frames []database.VideoFrame
	if err := tx.Where("tenant_id=? AND preparation_id=? AND representative=true", tenant, preparationID).Order("pts_us ASC").Limit(maxFramesPerMedia).Find(&frames).Error; err != nil {
		return nil, err
	}
	return frames, nil
}

func frameInput(frame database.VideoFrame, mediaID identity.ID, key string) evidenceInput {
	frameID := frame.ID
	ms := frame.PTSMicros / 1000
	return evidenceInput{ID: frame.ID, Kind: "VIDEO_FRAME", MediaID: mediaID, FrameID: &frameID, RequirementKey: key, Digest: frame.SHA256, TimestampMS: &ms, ObjectKey: frame.ObjectKey, Image: llm.NormalizedImage{EvidenceID: frame.ID.String(), Source: "VIDEO_FRAME", Position: fmt.Sprint(frame.PTSMicros), Digest: frame.SHA256}}
}

func photoInput(mediaID identity.ID, objectKey, key, source string) evidenceInput {
	return evidenceInput{ID: mediaID, Kind: "PHOTO", MediaID: mediaID, RequirementKey: key, ObjectKey: objectKey, Image: llm.NormalizedImage{EvidenceID: mediaID.String(), Source: source}}
}

// loadLegacyOriginEvidence returns the evidence of the legacy origin version a
// LEGACY_ORIGIN draft catalogs: its photos, or the retained representative
// frames of its video even when the source itself has expired.
func loadLegacyOriginEvidence(tx *gorm.DB, job database.InventoryJob) ([]evidenceInput, error) {
	var draft database.OriginVersion
	if err := tx.Where("tenant_id=? AND id=?", job.TenantID, job.OriginVersionID).Take(&draft).Error; err != nil {
		return nil, permanentIfMissing(err)
	}
	if draft.SourceVersionID == nil {
		return nil, fmt.Errorf("%w: legacy draft without source version", messaging.ErrPermanent)
	}
	var legacy database.OriginVersion
	if err := tx.Where("tenant_id=? AND id=?", job.TenantID, *draft.SourceVersionID).Take(&legacy).Error; err != nil {
		return nil, permanentIfMissing(err)
	}
	var out []evidenceInput
	if legacy.MediaKind == "VIDEO" && legacy.VideoPreparationID != nil {
		var prep database.VideoPreparationJob
		if err := tx.Where("tenant_id=? AND id=?", job.TenantID, *legacy.VideoPreparationID).Take(&prep).Error; err != nil {
			return nil, permanentIfMissing(err)
		}
		var frames []database.VideoFrame
		if err := tx.Where("tenant_id=? AND preparation_id=? AND representative=true", job.TenantID, prep.ID).Order("pts_us ASC").Limit(maxRequestImages).Find(&frames).Error; err != nil {
			return nil, err
		}
		for _, frame := range frames {
			out = append(out, frameInput(frame, prep.MediaID, job.RequirementKey))
		}
		return out, nil
	}
	var evidence []database.OriginEvidence
	if err := tx.Where("tenant_id=? AND origin_version_id=?", job.TenantID, legacy.ID).Order("created_at ASC").Find(&evidence).Error; err != nil {
		return nil, err
	}
	for _, row := range evidence {
		var derivative database.MediaDerivative
		if err := tx.Where("tenant_id=? AND media_id=? AND kind='DISPLAY'", job.TenantID, row.MediaID).Take(&derivative).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			return nil, err
		}
		out = append(out, photoInput(row.MediaID, derivative.ObjectKey, job.RequirementKey, "ORIGIN"))
	}
	return out, nil
}

func loadOriginEvidenceRefs(tx *gorm.DB, tenant identity.ID, evidence []database.InventoryItemEvidence, key string) ([]evidenceInput, error) {
	inputs := make([]evidenceInput, 0, len(evidence))
	seen := map[string]bool{}
	for _, row := range evidence {
		id := evidenceID(row.Kind, row.MediaID, row.FrameID)
		if seen[id] {
			continue
		}
		if row.Kind == "VIDEO_FRAME" && row.FrameID != nil {
			var frame database.VideoFrame
			if err := tx.Where("tenant_id=? AND id=?", tenant, *row.FrameID).Take(&frame).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					continue
				}
				return nil, err
			}
			input := frameInput(frame, row.MediaID, key)
			input.Image.Source = "ORIGIN"
			inputs = append(inputs, input)
		} else {
			var derivative database.MediaDerivative
			if err := tx.Where("tenant_id=? AND media_id=? AND kind='DISPLAY'", tenant, row.MediaID).Take(&derivative).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					continue
				}
				return nil, err
			}
			inputs = append(inputs, photoInput(row.MediaID, derivative.ObjectKey, key, "ORIGIN"))
		}
		seen[id] = true
	}
	return inputs, nil
}

// loadEvidenceImages reads image bytes outside any database transaction.
func loadEvidenceImages(ctx context.Context, store objectstore.Store, inputs []evidenceInput) ([]evidenceInput, error) {
	for i := range inputs {
		data, err := readObject(ctx, store, inputs[i].ObjectKey, maxImageBytes)
		if err != nil {
			return nil, err
		}
		if inputs[i].Digest == "" {
			sum := sha256.Sum256(data)
			inputs[i].Digest = hex.EncodeToString(sum[:])
		}
		inputs[i].Image.Digest = inputs[i].Digest
		inputs[i].Image.DataURL = "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(data)
	}
	return inputs, nil
}

func readObject(ctx context.Context, store objectstore.Store, key string, limit int64) ([]byte, error) {
	r, err := store.Client.Get(ctx, store.Bucket, key)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%w: inventory image exceeds limit", messaging.ErrPermanent)
	}
	return data, nil
}

func permanentIfMissing(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("%w: %v", messaging.ErrPermanent, err)
	}
	return err
}

func evidenceID(kind string, media identity.ID, frame *identity.ID) string {
	if kind == "VIDEO_FRAME" && frame != nil {
		return frame.String()
	}
	return media.String()
}

func evidenceRef(media identity.ID, frame *identity.ID) string {
	if frame != nil {
		return media.String() + ":" + frame.String()
	}
	return media.String()
}

// representativeImages spreads the request budget across media round-robin so
// one long clip cannot crowd out the other views.
func representativeImages(inputs []evidenceInput, max int) []evidenceInput {
	if len(inputs) <= max {
		return inputs
	}
	groups := make([][]evidenceInput, 0)
	groupIndex := map[identity.ID]int{}
	for _, input := range inputs {
		index, exists := groupIndex[input.MediaID]
		if !exists {
			index = len(groups)
			groupIndex[input.MediaID] = index
			groups = append(groups, nil)
		}
		groups[index] = append(groups[index], input)
	}
	selected := make([]evidenceInput, 0, max)
	for round := 0; len(selected) < max; round++ {
		added := false
		for _, group := range groups {
			if round < len(group) && len(selected) < max {
				selected = append(selected, group[round])
				added = true
			}
		}
		if !added {
			break
		}
	}
	return selected
}
