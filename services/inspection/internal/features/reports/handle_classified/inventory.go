package handle_classified

import (
	"context"
	"encoding/json"
	"sort"

	"inspection/libs/identity"
	inventoryexecutor "inspection/services/inspection/internal/features/analysis/inventory/core"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	"inspection/services/inspection/internal/features/reports/core"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

func buildInventoryReport(ctx context.Context, tx *gorm.DB, tenantID identity.ID, inspection database.Inspection, reference database.ReferenceSnapshot, evidence *[]core.Evidence) (*core.InventorySection, error) {
	if inspection.InventoryPromptSnapshotID == nil {
		return nil, nil
	}
	if reference.ReferenceVersionID == nil {
		return catalogingReport(ctx, tx, tenantID, inspection)
	}
	var version database.OriginVersion
	if err := tx.WithContext(ctx).Where("tenant_id=? AND id=? AND has_inventory", tenantID, *reference.ReferenceVersionID).Take(&version).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	section := &core.InventorySection{Reference: core.InventoryReference{VersionID: version.ID.String(), VersionNumber: version.VersionNumber, Purpose: value(version.Purpose), ActivatedAt: ""}, Problems: []core.InventoryEntry{}, Informational: []core.InventoryEntry{}}
	if version.ActivatedAt != nil {
		section.Reference.ActivatedAt = version.ActivatedAt.UTC().Format("2006-01-02T15:04:05Z07:00")
	}
	requirements, err := captureRequirements(ctx, tx, tenantID, inspection.ID)
	if err != nil {
		return nil, err
	}
	var envs []database.InventoryEnvironment
	if err := tx.WithContext(ctx).Where("tenant_id=? AND origin_version_id=? AND decision<>'REJECTED'", tenantID, version.ID).Order("position ASC").Find(&envs).Error; err != nil {
		return nil, err
	}
	envByIdentity := make(map[identity.ID]database.InventoryEnvironment, len(envs))
	for _, env := range envs {
		envByIdentity[env.IdentityID] = env
	}
	var items []database.InventoryItem
	if err := tx.WithContext(ctx).Where("tenant_id=? AND origin_version_id=? AND decision IN ('ACCEPTED','EDITED','ADDED')", tenantID, version.ID).Find(&items).Error; err != nil {
		return nil, err
	}
	itemByIdentity := make(map[identity.ID]database.InventoryItem, len(items))
	for _, item := range items {
		itemByIdentity[item.IdentityID] = item
	}
	var jobs []database.InventoryJob
	if err := tx.WithContext(ctx).Where("tenant_id=? AND inspection_id=? AND kind='COMPARE'", tenantID, inspection.ID).Find(&jobs).Error; err != nil {
		return nil, err
	}
	jobs = inventoryexecutor.LatestGeneration(jobs)
	jobIDs := make([]identity.ID, 0, len(jobs))
	envNameByJob := make(map[identity.ID]string, len(jobs))
	for _, job := range jobs {
		jobIDs = append(jobIDs, job.ID)
		if job.EnvironmentIdentityID != nil {
			if env, ok := envByIdentity[*job.EnvironmentIdentityID]; ok {
				envNameByJob[job.ID] = env.Name
			}
		}
		if _, named := envNameByJob[job.ID]; !named {
			envNameByJob[job.ID] = requirementName(requirements, job.RequirementKey)
		}
	}
	var rows []database.InventoryItemResult
	pairsByItem := map[string][]database.InventoryEvidencePair{}
	if len(jobIDs) > 0 {
		if err := tx.WithContext(ctx).Where("tenant_id=? AND job_id IN ?", tenantID, jobIDs).Find(&rows).Error; err != nil {
			return nil, err
		}
		var pairs []database.InventoryEvidencePair
		if err := tx.WithContext(ctx).Where("tenant_id=? AND job_id IN ? AND confirmed=true", tenantID, jobIDs).Order("created_at ASC").Find(&pairs).Error; err != nil {
			return nil, err
		}
		for _, pair := range pairs {
			key := pair.JobID.String() + ":" + pair.ItemIdentityID.String()
			pairsByItem[key] = append(pairsByItem[key], pair)
		}
	}
	var answers []database.RequirementAnswer
	if err := tx.WithContext(ctx).Table("capture.requirement_answers AS a").Select("a.*").Joins("JOIN capture.capture_drafts AS d ON d.tenant_id=a.tenant_id AND d.id=a.draft_id").Joins("JOIN inspections.responsibilities AS r ON r.tenant_id=d.tenant_id AND r.id=d.responsibility_id").Where("a.tenant_id=? AND r.inspection_id=? AND a.requirement_key LIKE 'env:%'", tenantID, inspection.ID).Find(&answers).Error; err != nil {
		return nil, err
	}
	impossibleRequirements := make(map[string]bool)
	for _, answer := range answers {
		if answer.ImpossibilityReason != "" {
			impossibleRequirements[answer.RequirementKey] = true
		}
	}
	assessed, notObserved := 0, 0
	for _, row := range rows {
		if row.Status == "OUTSIDE_INVENTORY" {
			entry := core.InventoryEntry{Environment: envNameByJob[row.JobID], Type: value(row.OutsideTypeCode), Name: value(row.OutsideName), Status: row.Status}
			section.Informational = append(section.Informational, entry)
			continue
		}
		if row.ItemIdentityID == nil {
			continue
		}
		item, ok := itemByIdentity[*row.ItemIdentityID]
		if !ok {
			continue
		}
		env := envByIdentity[lookupEnvironmentIdentity(item.EnvironmentID, envs)]
		reason := value(row.Description)
		if row.Status == "NOT_OBSERVED" && row.ReasonCode != nil {
			reason = inventoryReasonLabel(*row.ReasonCode)
		}
		entry := core.InventoryEntry{ItemIdentityID: item.IdentityID.String(), Environment: env.Name, Type: item.TypeCode, Name: item.Name, Status: row.Status, ConditionBefore: value(row.ConditionBefore), ConditionAfter: value(row.ConditionAfter), Reason: reason}
		if row.Status == "IMPROVED" {
			section.Informational = append(section.Informational, entry)
			assessed++
			continue
		}
		if row.Status == "PRESENT_UNCHANGED" || row.Status == "CONDITION_CHANGED" || row.Status == "MISSING" {
			assessed++
		}
		if row.Status == "NOT_OBSERVED" {
			notObserved++
			if impossibleRequirements[environmentKey(env)] {
				continue
			}
		}
		pairs := pairsByItem[row.JobID.String()+":"+row.ItemIdentityID.String()]
		requirementKey := environmentKey(env)
		// Exception-first reports include pair evidence only for emitted problems.
		if row.Status == "PRESENT_UNCHANGED" || row.Status == "IMPROVED" {
			continue
		}
		if !item.Declared {
			for _, pair := range pairs {
				originID, currentID := evidenceIdentity(pair.OriginKind, pair.OriginMediaID, pair.OriginFrameID), evidenceIdentity(pair.CurrentKind, pair.CurrentMediaID, pair.CurrentFrameID)
				if err := appendPairEvidence(ctx, tx, tenantID, pair.OriginKind, pair.OriginMediaID, pair.OriginFrameID, requirementKey, "REFERENCE", originID, evidence); err != nil {
					return nil, err
				}
				if err := appendPairEvidence(ctx, tx, tenantID, pair.CurrentKind, pair.CurrentMediaID, pair.CurrentFrameID, requirementKey, "CURRENT", currentID, evidence); err != nil {
					return nil, err
				}
				sectionEntryPair := core.InventoryEvidencePair{Origin: originID, Current: currentID, Decision: pair.Decision}
				if pair.CurrentFrameID != nil {
					var frame database.VideoFrame
					if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", tenantID, *pair.CurrentFrameID).Take(&frame).Error; err == nil {
						ms := frame.PTSMicros / 1000
						sectionEntryPair.TimestampMS = &ms
					}
				}
				entry.Pairs = append(entry.Pairs, sectionEntryPair)
			}
		}
		section.Problems = append(section.Problems, entry)
	}
	// The pinned version is never modified by a capture, so every included item
	// is expected; environments declared during capture come from the capture.
	expected := len(items)
	notCaptured := make([]string, 0)
	addedEnvironments := make([]string, 0)
	for _, requirement := range requirements {
		if requirement.AddedDuringCapture {
			addedEnvironments = append(addedEnvironments, requirementName(requirements, requirement.Key))
		}
	}
	for _, answer := range answers {
		if answer.ImpossibilityReason == "" {
			continue
		}
		name := requirementName(requirements, answer.RequirementKey)
		for _, env := range envs {
			if environmentKey(env) == answer.RequirementKey {
				name = env.Name
				section.Problems = append(section.Problems, core.InventoryEntry{Environment: env.Name, Type: env.TypeCode, Name: env.Name, Status: "ENVIRONMENT_NOT_CAPTURED", Reason: "Ambiente não capturado conforme informado."})
				break
			}
		}
		notCaptured = append(notCaptured, name)
	}
	section.Coverage = &core.InventoryCoverage{ExpectedItems: expected, AssessedItems: assessed, NotObserved: notObserved, EnvironmentsNotCaptured: notCaptured, EnvironmentsAddedDuringCapture: addedEnvironments}
	sort.SliceStable(section.Problems, func(i, j int) bool {
		left, right := problemRank(section.Problems[i].Status), problemRank(section.Problems[j].Status)
		if left != right {
			return left < right
		}
		if section.Problems[i].Environment != section.Problems[j].Environment {
			return section.Problems[i].Environment < section.Problems[j].Environment
		}
		return section.Problems[i].Name < section.Problems[j].Name
	})
	return section, nil
}

// catalogingReport is the inventory section of a cataloging inspection: it
// references the draft the inspection produced and has no coverage.
func catalogingReport(ctx context.Context, tx *gorm.DB, tenantID identity.ID, inspection database.Inspection) (*core.InventorySection, error) {
	var draft database.OriginVersion
	if err := tx.WithContext(ctx).Where("tenant_id=? AND source_inspection_id=? AND source='CAPTURE' AND has_inventory", tenantID, inspection.ID).Take(&draft).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &core.InventorySection{Reference: core.InventoryReference{VersionID: draft.ID.String(), VersionNumber: draft.VersionNumber}, Problems: []core.InventoryEntry{}, Informational: []core.InventoryEntry{}}, nil
}

func captureRequirements(ctx context.Context, tx *gorm.DB, tenantID, inspectionID identity.ID) ([]capturecore.Requirement, error) {
	var draft database.CaptureDraft
	if err := tx.WithContext(ctx).Where("tenant_id=? AND responsibility_id IN (SELECT id FROM inspections.responsibilities WHERE tenant_id=? AND inspection_id=?)", tenantID, tenantID, inspectionID).Order("created_at DESC").Take(&draft).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	var requirements []capturecore.Requirement
	if err := json.Unmarshal(draft.Requirements, &requirements); err != nil {
		return nil, err
	}
	requirements = capturecore.ActiveRequirements(requirements)
	return requirements, nil
}

// requirementName is the display name of a captured environment; it never
// exposes the internal requirement key.
func requirementName(requirements []capturecore.Requirement, key string) string {
	for _, requirement := range requirements {
		if requirement.Key == key {
			if requirement.EnvironmentName != "" {
				return requirement.EnvironmentName
			}
			if requirement.Label != "" {
				return requirement.Label
			}
		}
	}
	return "Ambiente"
}

// environmentKey is the capture requirement key of a pinned environment.
func environmentKey(env database.InventoryEnvironment) string {
	return "env:" + env.IdentityID.String()
}

func inventoryReasonLabel(code string) string {
	switch code {
	case "OBSTRUCTED":
		return "Visão obstruída."
	case "FRAMING":
		return "Enquadramento insuficiente."
	case "QUALITY":
		return "Qualidade da evidência insuficiente."
	case "NO_CORRESPONDING_VIEW":
		return "Não foi encontrada uma vista correspondente."
	case "ENVIRONMENT_NOT_CAPTURED":
		return "Ambiente não capturado."
	case "ANALYSIS_FAILED":
		return "Não foi possível concluir a análise."
	default:
		return "Não foi possível avaliar o item."
	}
}

func appendPairEvidence(ctx context.Context, tx *gorm.DB, tenantID identity.ID, kind string, mediaID identity.ID, frameID *identity.ID, requirementKey, role, id string, evidence *[]core.Evidence) error {
	item := core.Evidence{ID: id, RequirementKey: requirementKey, Role: role, EvidenceKind: kind}
	if kind == "VIDEO_FRAME" && frameID != nil {
		var frame database.VideoFrame
		if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", tenantID, *frameID).Take(&frame).Error; err != nil {
			return err
		}
		ms := frame.PTSMicros / 1000
		item.FrameID, item.SourceMediaID, item.PreparationID = frame.ID.String(), frame.SourceMediaID.String(), frame.PreparationID.String()
		item.PTSMicros, item.TimestampMS, item.FrameDigest, item.Availability = &frame.PTSMicros, &ms, frame.SHA256, "AVAILABLE"
		var media database.MediaObject
		if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", tenantID, frame.SourceMediaID).Take(&media).Error; err == nil {
			item.SourceAvailability = "AVAILABLE"
			var source database.VideoSource
			if err := tx.WithContext(ctx).Where("tenant_id=? AND media_id=?", tenantID, media.ID).Take(&source).Error; err == nil && source.Status == "EXPIRED" {
				item.SourceAvailability = "EXPIRED"
			}
		}
		if _, exists := evidenceByID(*evidence, item.ID); !exists {
			*evidence = append(*evidence, item)
		}
		return nil
	}
	var media database.MediaObject
	if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", tenantID, mediaID).Take(&media).Error; err != nil {
		return err
	}
	item.Description, item.CaptureSource, item.Availability = media.Description, media.CaptureSource, "AVAILABLE"
	if media.CapturedAt != nil {
		item.CapturedAt = media.CapturedAt.UTC().Format("2006-01-02T15:04:05Z07:00")
	}
	if _, exists := evidenceByID(*evidence, item.ID); !exists {
		*evidence = append(*evidence, item)
	}
	return nil
}

func evidenceByID(evidence []core.Evidence, id string) (int, bool) {
	if id == "" {
		return 0, false
	}
	for i := range evidence {
		if evidence[i].ID == id {
			return i, true
		}
	}
	return 0, false
}

func problemRank(status string) int {
	switch status {
	case "MISSING":
		return 0
	case "CONDITION_CHANGED":
		return 1
	case "NOT_OBSERVED":
		return 2
	case "ENVIRONMENT_NOT_CAPTURED":
		return 3
	default:
		return 4
	}
}

func evidenceIdentity(kind string, mediaID identity.ID, frameID *identity.ID) string {
	if kind == "VIDEO_FRAME" && frameID != nil {
		return frameID.String()
	}
	return mediaID.String()
}
func lookupEnvironmentIdentity(id identity.ID, envs []database.InventoryEnvironment) identity.ID {
	if env, ok := envRowByID(envs, id); ok {
		return env.IdentityID
	}
	return identity.ID{}
}
func envRowByID(envs []database.InventoryEnvironment, id identity.ID) (database.InventoryEnvironment, bool) {
	for _, env := range envs {
		if env.ID == id {
			return env, true
		}
	}
	return database.InventoryEnvironment{}, false
}
func value[T ~string](v *T) string {
	if v == nil {
		return ""
	}
	return string(*v)
}
