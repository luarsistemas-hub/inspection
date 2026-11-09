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
	"log/slog"
	"strings"

	"inspection/libs/identity"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	"inspection/services/inspection/internal/features/templates/catalog"
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
	inspection     database.Inspection
	snapshot       database.AnalysisPromptSnapshot
	environment    database.InventoryEnvironment
	items          []database.InventoryItem
	originRows     []database.InventoryItemEvidence
	origin         []evidenceInput
	current        []evidenceInput
	imageTruncated int
	impossible     bool
	// legacy marks a CATALOG job over a legacy origin version: the model
	// proposes the environments itself.
	legacy bool
	// voice is the fixed voice-note input of a CATALOG job; nil for visual-only jobs.
	voice               *voiceInput
	profile             string
	components          []catalogComponent
	scopeRoomUnresolved bool
	// strict marks a COMPARE job of a photo inspection with a pinned
	// descriptive scope (report v3): conclusions need paired evidence, a
	// baseline view and, for movable absence, whole-room coverage.
	strict bool
	// roomCovered reports that every applicable expected component of the
	// job's room, with its expected views, was observed in a submitted capture.
	roomCovered bool
	// staffPairs are the pairings decided by authorized staff for this job.
	staffPairs []database.InventoryEvidencePair
	// revised lists the items whose staff decision was recorded for this
	// very job by a pairing revision; only they are judged again when the
	// previous generation of the room is carried over.
	revised map[identity.ID]bool
	// carryOver is the previous successful generation of a revised room.
	carryOver *carryOver
	// unmatched marks a strict COMPARE job whose baseline environment was not
	// matched to any room of the pinned scope: no capture requirement reads
	// it, so its items are "not matched", never "not captured".
	unmatched bool
	// answerRef identifies the submitted answer the job read (see
	// captureAnswerRef); it is recorded with the job's results.
	answerRef string
}

// compareScopeRoom is the part of a pinned scope room that decides whether
// the room was covered as a whole.
type compareScopeRoom = catalog.ExpectedRoom

type catalogScopeRoom struct {
	Key        string `json:"key"`
	Name       string `json:"name"`
	TypeCode   string `json:"typeCode"`
	Components []struct {
		Key      string `json:"key"`
		Label    string `json:"label"`
		TypeCode string `json:"typeCode"`
	} `json:"components"`
}

func (p *Processor) within(ctx context.Context, tenantID identity.ID, fn func(*gorm.DB) error) error {
	if p.deps.Within != nil {
		return p.deps.Within(ctx, tenantID, fn)
	}
	return (tenanttx.Runner{DB: p.deps.DB}).Within(ctx, tenantID, fn)
}

func (p *Processor) load(ctx context.Context, job database.InventoryJob) (jobInput, error) {
	var in jobInput
	var scopeRooms []catalogScopeRoom
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
		if job.SchemaVersion > 0 && job.SchemaVersion != in.snapshot.SchemaVersion {
			return fmt.Errorf("%w: pinned analysis schema version mismatch", messaging.ErrPermanent)
		}
		if job.SchemaDigest != nil && *job.SchemaDigest != in.snapshot.CanonicalDigest {
			return fmt.Errorf("%w: pinned analysis schema digest mismatch", messaging.ErrPermanent)
		}
		if job.Kind == "CATALOG" {
			var scope database.InspectionScopeSnapshot
			if err := tx.WithContext(ctx).Where("tenant_id=? AND inspection_id=?", job.TenantID, job.InspectionID).Take(&scope).Error; err == nil {
				in.profile = scope.Profile
				var document struct {
					Rooms []catalogScopeRoom `json:"rooms"`
				}
				if json.Unmarshal(scope.Document, &document) != nil {
					return fmt.Errorf("%w: invalid pinned inspection scope", messaging.ErrPermanent)
				}
				scopeRooms = document.Rooms
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		if job.Kind == "CATALOG" && job.EnvironmentIdentityID == nil {
			in.legacy = true
			current, err := loadLegacyOriginEvidence(tx, job)
			in.current = current
			return err
		}
		strictCandidate := false
		if job.Kind == "COMPARE" && in.inspection.MediaKind == "PHOTO" {
			var scopes int64
			if err := tx.WithContext(ctx).Model(&database.InspectionScopeSnapshot{}).Where("tenant_id=? AND inspection_id=?", job.TenantID, job.InspectionID).Count(&scopes).Error; err != nil {
				return err
			}
			strictCandidate = scopes > 0
		}
		load := func() (*capturecore.Requirement, *database.RequirementAnswer, error) { return loadCapture(tx, job) }
		if strictCandidate {
			load = func() (*capturecore.Requirement, *database.RequirementAnswer, error) {
				return loadSubmittedCapture(ctx, tx, job)
			}
		}
		requirement, answer, err := load()
		if err != nil {
			return err
		}
		if strictCandidate {
			in.answerRef = captureAnswerRef(answer)
		}
		if job.Kind == "CATALOG" {
			room, ok := resolveCatalogScopeRoom(scopeRooms, job.RequirementKey, requirement)
			if ok {
				for _, component := range room.Components {
					in.components = append(in.components, catalogComponent{Key: room.Key + "." + component.Key, Label: component.Label, TypeCode: component.TypeCode})
				}
			} else if len(scopeRooms) > 0 {
				in.scopeRoomUnresolved = true
			}
		}
		if job.Kind == "COMPARE" && in.inspection.MediaKind == "PHOTO" {
			if err := loadStrictCompare(ctx, tx, job, requirement, answer != nil && answer.ImpossibilityReason != "", &in); err != nil {
				return err
			}
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
		var current []evidenceInput
		if job.Kind == "CATALOG" && in.inspection.MediaKind == "VIDEO" {
			planID := catalogPlanID(tx, job.TenantID, in.inspection, job.OriginVersionID)
			if planID != nil {
				current, err = loadPlannedVideoInputs(tx, job.TenantID, *planID, job.RequirementKey, mediaIDs)
				if err == nil && len(current) == 0 {
					current, err = loadMediaInputs(tx, job.TenantID, mediaIDs, job.RequirementKey, true)
				}
			} else {
				current, err = loadMediaInputs(tx, job.TenantID, mediaIDs, job.RequirementKey, true)
			}
		} else {
			current, err = loadMediaInputs(tx, job.TenantID, mediaIDs, job.RequirementKey, in.inspection.MediaKind == "VIDEO")
		}
		in.current = current
		if err != nil {
			return err
		}
		if job.Kind == "CATALOG" {
			if err := p.loadVoice(ctx, tx, job, mediaIDs, &in); err != nil {
				return err
			}
			if in.voice.hasNotes() {
				// Choose the frames before reading any bytes: the notes'
				// intervals come first and the room overview stays.
				extra, err := loadIntervalFrames(tx, job, in.voice.Notes)
				if err != nil {
					return err
				}
				var truncated int
				in.current, truncated = selectCatalogImages(append(in.current, extra...), in.voice.Notes, maxRequestImages)
				in.imageTruncated += truncated
			}
		}
		return nil
	})
	if err == nil && in.scopeRoomUnresolved {
		slog.Warn("inventory catalog scope room could not be resolved; using empty component scope", "inspection_id", job.InspectionID, "requirement_key", job.RequirementKey)
	}
	return in, err
}

func resolveCatalogScopeRoom(rooms []catalogScopeRoom, requirementKey string, requirement *capturecore.Requirement) (catalogScopeRoom, bool) {
	for _, room := range rooms {
		if requirementKey == "env:"+room.Key {
			return room, true
		}
	}
	if requirement == nil {
		return catalogScopeRoom{}, false
	}
	var match *catalogScopeRoom
	for i := range rooms {
		room := &rooms[i]
		if strings.EqualFold(strings.TrimSpace(room.Name), strings.TrimSpace(requirement.EnvironmentName)) && (room.TypeCode == "" || requirement.EnvironmentTypeCode == "" || room.TypeCode == requirement.EnvironmentTypeCode) {
			if match != nil {
				return catalogScopeRoom{}, false
			}
			match = room
		}
	}
	if match == nil {
		return catalogScopeRoom{}, false
	}
	return *match, true
}

func catalogPlanID(tx *gorm.DB, tenant identity.ID, inspection database.Inspection, draftID identity.ID) *identity.ID {
	var promotion database.InspectionOriginPromotion
	if tx.Where("tenant_id=? AND draft_version_id=? AND video_plan_id IS NOT NULL", tenant, draftID).Order("created_at DESC").Take(&promotion).Error == nil {
		return promotion.VideoPlanID
	}
	if inspection.ActiveVideoPlanID == nil {
		return nil
	}
	var plan database.VideoPlan
	if tx.Where("tenant_id=? AND id=? AND inspection_id=? AND status='SEALED'", tenant, *inspection.ActiveVideoPlanID, inspection.ID).Take(&plan).Error != nil {
		return nil
	}
	return &plan.ID
}

func loadPlannedVideoInputs(tx *gorm.DB, tenant, planID identity.ID, requirementKey string, mediaIDs []identity.ID) ([]evidenceInput, error) {
	var preparations []database.VideoPlanPreparation
	if len(mediaIDs) == 0 {
		return nil, nil
	}
	if err := tx.Table("analysis.video_plan_preparations AS vpp").
		Select("vpp.*").
		Joins("JOIN media.video_preparation_jobs AS vpj ON vpj.tenant_id=vpp.tenant_id AND vpj.id=vpp.preparation_id").
		Where("vpp.tenant_id=? AND vpp.plan_id=? AND (vpp.requirement_key=? OR (vpp.requirement_key IS NULL AND vpj.media_id IN ?))", tenant, planID, requirementKey, mediaIDs).
		Find(&preparations).Error; err != nil {
		return nil, err
	}
	var out []evidenceInput
	for _, planned := range preparations {
		var prep database.VideoPreparationJob
		if err := tx.Where("tenant_id=? AND id=? AND status='READY'", tenant, planned.PreparationID).Take(&prep).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			return nil, err
		}
		frames, err := representativeFrames(tx, tenant, prep.ID)
		if err != nil {
			return nil, err
		}
		for _, frame := range frames {
			out = append(out, frameInput(frame, prep.MediaID, requirementKey))
		}
	}
	return out, nil
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
	requirement, err := draftRequirement(draft, job.RequirementKey)
	if err != nil {
		return nil, nil, err
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

// loadSubmittedCapture is the capture input of a strict (report v3) COMPARE
// job: the newest answer to the job's room among SUBMITTED drafts only, by the
// same rule as the report and the pairing revision
// (capturecore.LatestSubmittedAnswer). An open recapture never replaces it.
func loadSubmittedCapture(ctx context.Context, tx *gorm.DB, job database.InventoryJob) (*capturecore.Requirement, *database.RequirementAnswer, error) {
	var answer database.RequirementAnswer
	var draft database.CaptureDraft
	err := capturecore.LatestSubmittedAnswer(ctx, tx, job.TenantID, job.InspectionID, job.RequirementKey, &answer)
	switch {
	case err == nil:
		if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", job.TenantID, answer.DraftID).Take(&draft).Error; err != nil {
			return nil, nil, err
		}
	case errors.Is(err, gorm.ErrRecordNotFound):
		if err := tx.WithContext(ctx).Where("tenant_id=? AND kind='INSPECTION' AND status='SUBMITTED' AND responsibility_id IN (SELECT id FROM inspections.responsibilities WHERE tenant_id=? AND inspection_id=?)", job.TenantID, job.TenantID, job.InspectionID).Order("created_at DESC").Take(&draft).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, nil, nil
			}
			return nil, nil, err
		}
	default:
		return nil, nil, err
	}
	requirement, err := draftRequirement(draft, job.RequirementKey)
	if err != nil || answer.ID == (identity.ID{}) {
		return requirement, nil, err
	}
	return requirement, &answer, nil
}

// captureAnswerRef identifies the submitted answer a strict COMPARE job read:
// "<answer id>:<version>", or "none" when the room has no submitted answer.
func captureAnswerRef(answer *database.RequirementAnswer) string {
	if answer == nil {
		return "none"
	}
	return fmt.Sprintf("%s:%d", answer.ID, answer.Version)
}

// draftRequirement returns the active requirement of a capture draft.
func draftRequirement(draft database.CaptureDraft, key string) (*capturecore.Requirement, error) {
	var requirements []capturecore.Requirement
	if err := json.Unmarshal(draft.Requirements, &requirements); err != nil {
		return nil, fmt.Errorf("%w: invalid capture requirements", messaging.ErrPermanent)
	}
	requirements = capturecore.ActiveRequirements(requirements)
	for i := range requirements {
		if requirements[i].Key == key {
			return &requirements[i], nil
		}
	}
	return nil, nil
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
