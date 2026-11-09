package handle_classified

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"inspection/libs/identity"
	inventoryexecutor "inspection/services/inspection/internal/features/analysis/inventory/core"
	"inspection/services/inspection/internal/features/reports/core"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

// buildComparisonReport assembles the complete report v3 comparison of a
// photo inspection with a pinned descriptive scope against its approved
// baseline: every included baseline item with one evidence-backed outcome,
// its baseline and current conditions kept apart, and counts derived from
// the items. Legacy, video and cataloging inspections return nil and keep
// their historical report shape.
func buildComparisonReport(ctx context.Context, tx *gorm.DB, tenantID identity.ID, inspection database.Inspection, reference database.ReferenceSnapshot, evidence *[]core.Evidence) (*core.ComparisonSection, error) {
	if !shouldBuildDescriptiveReport(inspection.MediaKind) || inspection.InventoryPromptSnapshotID == nil || reference.ReferenceVersionID == nil {
		return nil, nil
	}
	var scopes int64
	if err := tx.WithContext(ctx).Model(&database.InspectionScopeSnapshot{}).Where("tenant_id=? AND inspection_id=?", tenantID, inspection.ID).Count(&scopes).Error; err != nil {
		return nil, err
	}
	if scopes == 0 {
		return nil, nil
	}
	var version database.OriginVersion
	if err := tx.WithContext(ctx).Where("tenant_id=? AND id=? AND has_inventory", tenantID, *reference.ReferenceVersionID).Take(&version).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	// A scope without rooms (template v1/v2: "escopo não definido") can never
	// establish whole-room coverage; its coverage limitation says so.
	var undefinedScope int64
	if err := tx.WithContext(ctx).Model(&database.InspectionScopeSnapshot{}).Where("tenant_id=? AND inspection_id=? AND profile=?", tenantID, inspection.ID, "UNDEFINED").Count(&undefinedScope).Error; err != nil {
		return nil, err
	}
	section := &core.ComparisonSection{Baseline: core.ComparisonBaseline{VersionID: version.ID.String(), VersionNumber: version.VersionNumber, Purpose: value(version.Purpose)}, Status: "COMPLETED", Items: []core.ComparisonItem{}, OutsideBaseline: []core.ComparisonObservation{}}
	if version.ActivatedAt != nil {
		section.Baseline.ActivatedAt = version.ActivatedAt.UTC().Format(time.RFC3339)
	}
	var envs []database.InventoryEnvironment
	if err := tx.WithContext(ctx).Where("tenant_id=? AND origin_version_id=? AND decision<>'REJECTED'", tenantID, version.ID).Order("position ASC, id ASC").Find(&envs).Error; err != nil {
		return nil, err
	}
	envByID := make(map[identity.ID]database.InventoryEnvironment, len(envs))
	envByIdentity := make(map[identity.ID]database.InventoryEnvironment, len(envs))
	position := make(map[identity.ID]int, len(envs))
	for index, env := range envs {
		envByID[env.ID], envByIdentity[env.IdentityID], position[env.ID] = env, env, index
	}
	var items []database.InventoryItem
	if err := tx.WithContext(ctx).Where("tenant_id=? AND origin_version_id=? AND decision IN ('ACCEPTED','EDITED','ADDED')", tenantID, version.ID).Order("created_at ASC, id ASC").Find(&items).Error; err != nil {
		return nil, err
	}
	var jobs []database.InventoryJob
	if err := tx.WithContext(ctx).Where("tenant_id=? AND inspection_id=? AND kind='COMPARE'", tenantID, inspection.ID).Find(&jobs).Error; err != nil {
		return nil, err
	}
	jobs = inventoryexecutor.LatestGeneration(jobs)
	jobIDs := make([]identity.ID, 0, len(jobs))
	jobByID := make(map[identity.ID]database.InventoryJob, len(jobs))
	failedEnvironment := map[identity.ID]bool{}
	roomKeys := comparedRoomKeys(jobs)
	for _, job := range jobs {
		jobIDs = append(jobIDs, job.ID)
		jobByID[job.ID] = job
		switch job.Status {
		case "FAILED":
			section.Status = "FAILED"
			if job.EnvironmentIdentityID != nil {
				failedEnvironment[*job.EnvironmentIdentityID] = true
			}
		case "SUCCEEDED":
		default:
			if section.Status == "COMPLETED" {
				section.Status = "INCONCLUSIVE"
			}
		}
	}
	var rows []database.InventoryItemResult
	var pairs []database.InventoryEvidencePair
	if len(jobIDs) > 0 {
		if err := tx.WithContext(ctx).Where("tenant_id=? AND job_id IN ?", tenantID, jobIDs).Order("created_at ASC, id ASC").Find(&rows).Error; err != nil {
			return nil, err
		}
		if err := tx.WithContext(ctx).Where("tenant_id=? AND job_id IN ?", tenantID, jobIDs).Order("created_at ASC, id ASC").Find(&pairs).Error; err != nil {
			return nil, err
		}
	}
	requirements, err := captureRequirements(ctx, tx, tenantID, inspection.ID)
	if err != nil {
		return nil, err
	}
	rowByItem := make(map[identity.ID]database.InventoryItemResult, len(rows))
	for _, row := range rows {
		if row.Status == "OUTSIDE_INVENTORY" {
			observation := core.ComparisonObservation{Name: value(row.OutsideName), TypeCode: value(row.OutsideTypeCode)}
			if job, ok := jobByID[row.JobID]; ok {
				if job.EnvironmentIdentityID != nil {
					observation.Room = envByIdentity[*job.EnvironmentIdentityID].Name
				}
				if observation.Room == "" {
					observation.Room = requirementName(requirements, job.RequirementKey)
				}
			}
			if observation.Room == "" {
				observation.Room = "Ambiente"
			}
			// No model sentence is ever part of a v3 comparison.
			section.OutsideBaseline = append(section.OutsideBaseline, observation)
			continue
		}
		if row.ItemIdentityID != nil {
			if _, seen := rowByItem[*row.ItemIdentityID]; !seen {
				rowByItem[*row.ItemIdentityID] = row
			}
		}
	}
	pairsByItem := map[string][]database.InventoryEvidencePair{}
	for _, pair := range pairs {
		key := pair.JobID.String() + ":" + pair.ItemIdentityID.String()
		pairsByItem[key] = append(pairsByItem[key], pair)
	}
	ordered := make([]database.InventoryItem, 0, len(items))
	for _, item := range items {
		if _, ok := envByID[item.EnvironmentID]; ok {
			ordered = append(ordered, item)
		}
	}
	// Stable reading order: baseline room position, then item creation.
	sortItemsByRoom(ordered, position)
	for _, item := range ordered {
		env := envByID[item.EnvironmentID]
		roomKey := roomKeyOf(env, roomKeys)
		entry := core.ComparisonItem{ItemIdentityID: item.IdentityID.String(), Room: env.Name, RoomKey: roomKey, TypeCode: item.TypeCode, Name: item.Name, BaselineCondition: item.Condition}
		row, ok := rowByItem[item.IdentityID]
		if !ok {
			entry.Outcome = core.ComparisonNotObserved
			entry.Limitations = []string{core.LimitationComparisonNoResult}
			if failedEnvironment[env.IdentityID] {
				entry.Limitations = []string{core.LimitationComparisonFailed}
			}
			section.Items = append(section.Items, entry)
			continue
		}
		if before := value(row.ConditionBefore); before != "" {
			entry.BaselineCondition = before
		}
		entry.Outcome, entry.Limitations = comparisonOutcome(row)
		if undefinedScope > 0 && value(row.ReasonCode) == "ROOM_COVERAGE_INSUFFICIENT" {
			entry.Limitations = []string{core.LimitationRoomCoverageUndefinedScope}
		}
		if entry.Outcome == core.ComparisonNoBasis {
			// Without basis the baseline condition is not stated anywhere.
			entry.BaselineCondition = ""
		}
		if entry.Outcome == core.ComparisonUnchanged || entry.Outcome == core.ComparisonChanged || entry.Outcome == core.ComparisonImproved {
			entry.CurrentCondition = value(row.ConditionAfter)
		}
		// The item text is generated from the outcome, conditions and controlled
		// labels only: the model's free text never reaches the report.
		for _, pair := range pairsByItem[row.JobID.String()+":"+item.IdentityID.String()] {
			if pair.Source == "STAFF" {
				entry.PairingRevised = true
			}
			// Only pairs that support the stated outcome are listed: a gap
			// (not observed or no basis) never shows a pair as if it had
			// been compared.
			if !pair.Confirmed || !outcomeStatesFact(entry.Outcome) {
				continue
			}
			originID, currentID := evidenceIdentity(pair.OriginKind, pair.OriginMediaID, pair.OriginFrameID), evidenceIdentity(pair.CurrentKind, pair.CurrentMediaID, pair.CurrentFrameID)
			if err := appendComparisonEvidence(ctx, tx, tenantID, pair.OriginKind, pair.OriginMediaID, pair.OriginFrameID, roomKey, "REFERENCE", originID, evidence); err != nil {
				return nil, err
			}
			if err := appendComparisonEvidence(ctx, tx, tenantID, pair.CurrentKind, pair.CurrentMediaID, pair.CurrentFrameID, roomKey, "CURRENT", currentID, evidence); err != nil {
				return nil, err
			}
			entry.Pairs = append(entry.Pairs, core.ComparisonPair{BaselineEvidenceID: originID, CurrentEvidenceID: currentID})
		}
		if len(entry.Pairs) == 0 && outcomeStatesFact(entry.Outcome) {
			// A conclusion without paired evidence is never stated.
			entry.Outcome, entry.CurrentCondition, entry.Limitations = core.ComparisonNotObserved, "", []string{core.LimitationUnsupportedOutcome}
		}
		section.Items = append(section.Items, entry)
	}
	section.Counts = core.CountComparison(section.Items)
	return section, nil
}

// comparedRoomKeys maps each baseline room identity to the capture
// requirement key its COMPARE job read: a descriptive capture keys the room by
// its scope key, older captures by "env:<identity>".
func comparedRoomKeys(jobs []database.InventoryJob) map[identity.ID]string {
	keys := make(map[identity.ID]string, len(jobs))
	for _, job := range jobs {
		if job.EnvironmentIdentityID != nil && job.RequirementKey != "" {
			keys[*job.EnvironmentIdentityID] = job.RequirementKey
		}
	}
	return keys
}

// roomKeyOf is the capture requirement key of a baseline room.
func roomKeyOf(env database.InventoryEnvironment, keys map[identity.ID]string) string {
	if key, ok := keys[env.IdentityID]; ok {
		return key
	}
	return environmentKey(env)
}

// outcomeStatesFact lists the outcomes that state a visual fact and need the
// item's paired baseline and current evidence.
func outcomeStatesFact(outcome string) bool {
	return outcome == core.ComparisonUnchanged || outcome == core.ComparisonChanged || outcome == core.ComparisonImproved || outcome == core.ComparisonMissing
}

// comparisonOutcome maps a stored item result to its report outcome and the
// neutral limitation that explains a gap. "No basis" (the baseline does not
// show the item) stays distinct from "not observed" (no adequate current view).
func comparisonOutcome(row database.InventoryItemResult) (string, []string) {
	switch row.Status {
	case "PRESENT_UNCHANGED":
		return core.ComparisonUnchanged, nil
	case "CONDITION_CHANGED":
		return core.ComparisonChanged, nil
	case "IMPROVED":
		return core.ComparisonImproved, nil
	case "MISSING":
		return core.ComparisonMissing, nil
	}
	switch value(row.ReasonCode) {
	case "NO_BASELINE_VIEW":
		return core.ComparisonNoBasis, []string{core.LimitationNoBaselineView}
	case "BASELINE_CONDITION_UNASSESSED":
		return core.ComparisonNoBasis, []string{core.LimitationNoBaselineCondition}
	case "PAIR_REJECTED":
		return core.ComparisonNotObserved, []string{core.LimitationPairRejected}
	case "ROOM_COVERAGE_INSUFFICIENT":
		return core.ComparisonNotObserved, []string{core.LimitationRoomCoverage}
	case "INCONSISTENT_CLAIM":
		return core.ComparisonNotObserved, []string{core.LimitationInconsistentClaim}
	case "ENVIRONMENT_NOT_CAPTURED":
		return core.ComparisonNotObserved, []string{core.LimitationEnvironmentMissing}
	case "ENVIRONMENT_UNMATCHED":
		return core.ComparisonNotObserved, []string{core.LimitationEnvironmentUnmatched}
	case "ANALYSIS_FAILED":
		return core.ComparisonNotObserved, []string{core.LimitationComparisonFailed}
	case "OBSTRUCTED":
		return core.ComparisonNotObserved, []string{core.LimitationObstructed}
	case "FRAMING":
		return core.ComparisonNotObserved, []string{core.LimitationFraming}
	case "QUALITY":
		return core.ComparisonNotObserved, []string{core.LimitationQuality}
	default:
		return core.ComparisonNotObserved, []string{core.LimitationNoCurrentView}
	}
}

func sortItemsByRoom(items []database.InventoryItem, position map[identity.ID]int) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && position[items[j].EnvironmentID] < position[items[j-1].EnvironmentID]; j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
}

// appendComparisonEvidence pins report-safe metadata of one paired evidence.
// Media that no longer resolves is kept with an unavailable label: the issued
// conclusion stays, only its image is missing.
func appendComparisonEvidence(ctx context.Context, tx *gorm.DB, tenantID identity.ID, kind string, mediaID identity.ID, frameID *identity.ID, requirementKey, role, id string, evidence *[]core.Evidence) error {
	if _, exists := evidenceByID(*evidence, id); exists {
		return nil
	}
	if kind == "VIDEO_FRAME" && frameID != nil {
		var frame database.VideoFrame
		err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", tenantID, *frameID).Take(&frame).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			*evidence = append(*evidence, core.Evidence{ID: id, RequirementKey: requirementKey, Role: role, Availability: "MISSING"})
			return nil
		}
		if err != nil {
			return err
		}
		return appendPairEvidence(ctx, tx, tenantID, kind, mediaID, frameID, requirementKey, role, id, evidence)
	}
	var media database.MediaObject
	err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", tenantID, mediaID).Take(&media).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		*evidence = append(*evidence, core.Evidence{ID: id, RequirementKey: requirementKey, Role: role, EvidenceKind: kind, Availability: "MISSING"})
		return nil
	}
	if err != nil {
		return err
	}
	item := core.Evidence{ID: id, RequirementKey: requirementKey, Role: role, EvidenceKind: kind, Description: media.Description, CaptureSource: media.CaptureSource, Availability: "MISSING"}
	// Sensitive and quality flags travel with the paired image, exactly as for
	// the captured evidence, so every audience applies the same triage.
	var flags []string
	if json.Unmarshal(media.Flags, &flags) == nil && len(flags) > 0 {
		item.Flags = reportFlags(requirementKey, flags)
	}
	if media.CapturedAt != nil {
		item.CapturedAt = media.CapturedAt.UTC().Format("2006-01-02T15:04:05Z07:00")
	}
	var derivative database.MediaDerivative
	if err := tx.WithContext(ctx).Where("tenant_id=? AND media_id=? AND kind='DISPLAY'", tenantID, mediaID).Take(&derivative).Error; err == nil {
		item.DisplayDigest, item.Availability = derivative.SHA256, "AVAILABLE"
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	*evidence = append(*evidence, item)
	return nil
}

// alignInventoryWithComparison makes the exception-first inventory section of
// a report v3 state exactly the comparison's per-item results: one entry per
// baseline item that is changed, missing or not observed (problems) and per
// item that improved or has no basis (informational), with the comparison's
// conditions, limitation and pairs, and coverage counts equal to the
// comparison counts. Room-level entries (a room not captured) and objects
// outside the baseline are kept. Reports without comparison are untouched.
func alignInventoryWithComparison(inventory *core.InventorySection, comparison *core.ComparisonSection) {
	if inventory == nil || comparison == nil {
		return
	}
	previous := map[string]core.InventoryEntry{}
	problems := []core.InventoryEntry{}
	informational := []core.InventoryEntry{}
	for _, entry := range append(append([]core.InventoryEntry{}, inventory.Problems...), inventory.Informational...) {
		if entry.ItemIdentityID != "" {
			previous[entry.ItemIdentityID] = entry
			continue
		}
		if entry.Status == "OUTSIDE_INVENTORY" {
			informational = append(informational, entry)
		} else {
			problems = append(problems, entry)
		}
	}
	for _, item := range comparison.Items {
		entry := core.InventoryEntry{ItemIdentityID: item.ItemIdentityID, Environment: item.Room, Type: item.TypeCode, Name: item.Name, ConditionBefore: item.BaselineCondition, ConditionAfter: item.CurrentCondition}
		if entry.Type == "" {
			entry.Type = previous[item.ItemIdentityID].Type
		}
		if len(item.Limitations) > 0 {
			entry.Reason = item.Limitations[0]
		}
		if outcomeStatesFact(item.Outcome) {
			for _, pair := range item.Pairs {
				entry.Pairs = append(entry.Pairs, core.InventoryEvidencePair{Origin: pair.BaselineEvidenceID, Current: pair.CurrentEvidenceID, Decision: "SAME_VIEW"})
			}
		}
		switch item.Outcome {
		case core.ComparisonChanged:
			entry.Status = "CONDITION_CHANGED"
			problems = append(problems, entry)
		case core.ComparisonMissing:
			entry.Status = "MISSING"
			problems = append(problems, entry)
		case core.ComparisonNotObserved:
			entry.Status, entry.ConditionAfter = "NOT_OBSERVED", ""
			problems = append(problems, entry)
		case core.ComparisonImproved:
			entry.Status = "IMPROVED"
			informational = append(informational, entry)
		case core.ComparisonNoBasis:
			entry.Status, entry.ConditionBefore, entry.ConditionAfter = core.InventoryNoBasis, "", ""
			informational = append(informational, entry)
		}
	}
	sort.SliceStable(problems, func(i, j int) bool {
		left, right := problemRank(problems[i].Status), problemRank(problems[j].Status)
		if left != right {
			return left < right
		}
		if problems[i].Environment != problems[j].Environment {
			return problems[i].Environment < problems[j].Environment
		}
		return problems[i].Name < problems[j].Name
	})
	inventory.Problems, inventory.Informational = problems, informational
	counts := comparison.Counts
	coverage := core.InventoryCoverage{EnvironmentsNotCaptured: []string{}, EnvironmentsAddedDuringCapture: []string{}}
	if inventory.Coverage != nil {
		coverage = *inventory.Coverage
	}
	coverage.ExpectedItems, coverage.AssessedItems, coverage.NotObserved, coverage.NoBasis = counts.Total, counts.Unchanged+counts.Changed+counts.Improved+counts.Missing, counts.NotObserved, counts.NoBasis
	inventory.Coverage = &coverage
}

// dropOrphanInventoryEvidence removes, from a report v3 with comparison, the
// evidence the inventory section added (absent from before) that no item,
// pair, component, record or finding of the report cites any more. Other
// reports are returned unchanged.
func dropOrphanInventoryEvidence(evidence []core.Evidence, before map[string]bool, inventory *core.InventorySection, comparison *core.ComparisonSection, checklist *core.ChecklistSection, handover *core.HandoverSection, findings []core.Finding) []core.Evidence {
	if comparison == nil {
		return evidence
	}
	cited := map[string]bool{}
	for _, item := range comparison.Items {
		for _, pair := range item.Pairs {
			cited[pair.BaselineEvidenceID], cited[pair.CurrentEvidenceID] = true, true
		}
	}
	if inventory != nil {
		for _, entry := range append(append([]core.InventoryEntry{}, inventory.Problems...), inventory.Informational...) {
			for _, pair := range entry.Pairs {
				cited[pair.Origin], cited[pair.Current] = true, true
			}
		}
	}
	if checklist != nil {
		for _, room := range checklist.Rooms {
			for _, component := range room.Components {
				for _, id := range component.EvidenceIDs {
					cited[id] = true
				}
				for _, source := range component.AttributeSources {
					for _, id := range source.EvidenceIDs {
						cited[id] = true
					}
				}
			}
		}
	}
	if handover != nil {
		for _, entry := range handover.Entries {
			for _, id := range entry.EvidenceIDs {
				cited[id] = true
			}
		}
	}
	for _, finding := range findings {
		for _, id := range finding.EvidenceIDs {
			cited[id] = true
		}
	}
	kept := evidence[:0:0]
	for _, item := range evidence {
		if !before[item.ID] && !cited[item.ID] {
			continue
		}
		kept = append(kept, item)
	}
	return kept
}

// keepProvenanceEvidence keeps in the provenance only evidence the report
// still carries.
func keepProvenanceEvidence(provenance *core.Provenance, evidence []core.Evidence) {
	if provenance == nil {
		return
	}
	present := make(map[string]bool, len(evidence))
	for _, item := range evidence {
		present[item.ID] = true
	}
	kept := provenance.EvidenceIDs[:0:0]
	for _, id := range provenance.EvidenceIDs {
		if present[id] {
			kept = append(kept, id)
		}
	}
	provenance.EvidenceIDs = kept
}
