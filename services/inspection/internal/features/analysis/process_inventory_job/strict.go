package process_inventory_job

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"inspection/libs/identity"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"

	"gorm.io/gorm"
)

// Reason codes recorded only by strict (report v3) comparisons.
const (
	reasonNoBaselineView = "NO_BASELINE_VIEW"
	reasonPairRejected   = "PAIR_REJECTED"
	reasonRoomCoverage   = "ROOM_COVERAGE_INSUFFICIENT"
	// reasonInconsistentClaim marks a claim its own conditions contradict
	// (for example "unchanged" with a worse current condition).
	reasonInconsistentClaim = "INCONSISTENT_CLAIM"
	// reasonBaselineConditionUnassessed marks an item the baseline shows
	// without an assessable condition: there is a view but no condition to
	// compare against.
	reasonBaselineConditionUnassessed = "BASELINE_CONDITION_UNASSESSED"
	// reasonEnvironmentUnmatched marks the items of a baseline environment no
	// room of the pinned scope was matched to (ambiguous or missing match):
	// nothing was asked of the capture for it.
	reasonEnvironmentUnmatched = "ENVIRONMENT_UNMATCHED"
)

// strictModelReasons are the only "not observed" reasons a strict (report
// v3) comparison accepts from the model: they describe the current view.
// Processing facts (room not captured, analysis failed) are recorded by the
// system alone.
var strictModelReasons = map[string]bool{"OBSTRUCTED": true, "FRAMING": true, "QUALITY": true, "NO_CORRESPONDING_VIEW": true}

// loadStrictCompare marks a COMPARE job of a photo inspection with a pinned
// descriptive scope as strict and reads the facts strict rules need: whether
// the job's room was covered as a whole and the pairings decided by staff.
// Legacy (unscoped) inspections keep their historical rules.
func loadStrictCompare(ctx context.Context, tx *gorm.DB, job database.InventoryJob, requirement *capturecore.Requirement, roomImpossible bool, in *jobInput) error {
	var scope database.InspectionScopeSnapshot
	err := tx.WithContext(ctx).Where("tenant_id=? AND inspection_id=?", job.TenantID, job.InspectionID).Take(&scope).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	in.strict = true
	var document struct {
		Rooms []compareScopeRoom `json:"rooms"`
	}
	if json.Unmarshal(scope.Document, &document) != nil {
		return fmt.Errorf("%w: invalid pinned inspection scope", messaging.ErrPermanent)
	}
	room, matched := resolveCompareScopeRoom(document.Rooms, job.RequirementKey, requirement)
	// The reference environment had no corresponding room in the pinned
	// capture scope. This is a baseline mapping limitation, not a failed
	// capture of a room that the participant was asked to record.
	// A v2 move-out has an undefined scope (no pinned rooms), but still asks
	// the participant for each baseline environment under env:<identity>. Its
	// missing room is not a failed baseline mapping when that requirement is
	// present; capture state decides ENVIRONMENT_NOT_CAPTURED later.
	in.unmatched = !matched && requirement == nil
	if matched && !roomImpossible {
		answers, err := submittedCoverageAnswers(ctx, tx, job, "env:"+room.Key)
		if err != nil {
			return err
		}
		in.roomCovered = roomCovered(room, answers)
	}
	// A staff pairing stays in force for later generations of the room: the
	// newest decision per item, up to this job's generation, applies.
	var staff []database.InventoryEvidencePair
	if err := tx.WithContext(ctx).Table("analysis.inventory_evidence_pairs AS p").Select("p.*").
		Joins("JOIN analysis.inventory_jobs AS j ON j.tenant_id=p.tenant_id AND j.id=p.job_id").
		Where("p.tenant_id=? AND p.source='STAFF' AND j.inspection_id=? AND j.kind='COMPARE' AND j.requirement_key=? AND j.generation<=?", job.TenantID, job.InspectionID, job.RequirementKey, job.Generation).
		Order("j.generation DESC, p.created_at ASC, p.id ASC").Find(&staff).Error; err != nil {
		return err
	}
	decidedIn := map[identity.ID]identity.ID{}
	for _, pair := range staff {
		if decided, ok := decidedIn[pair.ItemIdentityID]; ok && decided != pair.JobID {
			continue
		}
		decidedIn[pair.ItemIdentityID] = pair.JobID
		if pair.JobID != job.ID {
			pair.ID, pair.JobID = identity.NewID(), job.ID
		} else {
			if in.revised == nil {
				in.revised = map[identity.ID]bool{}
			}
			in.revised[pair.ItemIdentityID] = true
		}
		in.staffPairs = append(in.staffPairs, pair)
	}
	if len(in.revised) > 0 {
		return loadCarryOver(ctx, tx, job, in)
	}
	return nil
}

// loadCarryOver reads generation N-1 of the room of a job opened by a pairing
// revision (it carries staff decisions recorded for itself), whatever its
// status: the room's newest state is what a revision keeps. Only the revised
// items, and the items the previous generation could not support (see
// carryOver.rejudge), are judged again; every other result of the room, with
// its pairs (staff pairs included) and the informational observations, is
// carried over unchanged so a correction never changes items nobody decided
// on. Without a terminal generation N-1 the whole room is judged.
func loadCarryOver(ctx context.Context, tx *gorm.DB, job database.InventoryJob, in *jobInput) error {
	var previous database.InventoryJob
	err := tx.WithContext(ctx).Where("tenant_id=? AND inspection_id=? AND kind='COMPARE' AND requirement_key=? AND generation=?", job.TenantID, job.InspectionID, job.RequirementKey, job.Generation-1).Take(&previous).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if previous.Status != "SUCCEEDED" && previous.Status != "FAILED" {
		return nil
	}
	// Generation N-1 read one submitted answer of the room. When the room's
	// newest submission is another one (a recapture submitted since), or N-1
	// did not record what it read, nothing of N-1 is carried: the whole room
	// is judged again with the staff decisions.
	var latestAnswer database.RequirementAnswer
	var current *database.RequirementAnswer
	if err := capturecore.LatestSubmittedAnswer(ctx, tx, job.TenantID, job.InspectionID, job.RequirementKey, &latestAnswer); err == nil {
		current = &latestAnswer
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if previous.CaptureAnswerRef == nil || *previous.CaptureAnswerRef != captureAnswerRef(current) {
		return nil
	}
	var results []database.InventoryItemResult
	if err := tx.WithContext(ctx).Where("tenant_id=? AND job_id=?", job.TenantID, previous.ID).Order("created_at ASC, id ASC").Find(&results).Error; err != nil {
		return err
	}
	var pairs []database.InventoryEvidencePair
	if err := tx.WithContext(ctx).Where("tenant_id=? AND job_id=?", job.TenantID, previous.ID).Order("created_at ASC, id ASC").Find(&pairs).Error; err != nil {
		return err
	}
	latest, err := capturecore.LatestSubmittedMedia(ctx, tx, job.TenantID, job.InspectionID, job.RequirementKey)
	if err != nil {
		return err
	}
	carry := &carryOver{results: results, pairs: pairs, rejudge: map[identity.ID]bool{}}
	for id := range in.revised {
		carry.rejudge[id] = true
	}
	submitted := make(map[identity.ID]bool, len(latest))
	for _, id := range latest {
		submitted[id] = true
	}
	staffDecided := map[identity.ID]bool{}
	for _, pair := range pairs {
		if pair.Source == "STAFF" {
			staffDecided[pair.ItemIdentityID] = true
		}
		// A conclusion supported by a current view the room's newest
		// submission replaced is never kept.
		if pair.Confirmed && pair.CurrentFrameID == nil && !submitted[pair.CurrentMediaID] {
			carry.rejudge[pair.ItemIdentityID] = true
		}
	}
	for _, row := range results {
		// A staff decision whose own judgment failed is judged again rather
		// than carried as a failure next to pairs nobody evaluated.
		if row.ItemIdentityID != nil && staffDecided[*row.ItemIdentityID] && row.Status == "NOT_OBSERVED" && value(row.ReasonCode) == "ANALYSIS_FAILED" {
			carry.rejudge[*row.ItemIdentityID] = true
		}
	}
	in.carryOver = carry
	return nil
}

// carryOver is generation N-1 of a revised room: its results and pairs, and
// the items judged again in generation N (the revised items, items whose
// staff decision failed in N-1 and items whose conclusion rests on a replaced
// current view).
type carryOver struct {
	results []database.InventoryItemResult
	pairs   []database.InventoryEvidencePair
	rejudge map[identity.ID]bool
}

// carriedResults copies the previous results and pairs (matcher and staff) of
// every item that is not judged again, plus the informational observations,
// into job.
func carriedResults(job database.InventoryJob, carry *carryOver, now time.Time) ([]database.InventoryItemResult, []database.InventoryEvidencePair) {
	var results []database.InventoryItemResult
	for _, row := range carry.results {
		if row.ItemIdentityID != nil && carry.rejudge[*row.ItemIdentityID] {
			continue
		}
		row.ID, row.JobID, row.CreatedAt = identity.NewID(), job.ID, now
		results = append(results, row)
	}
	var pairs []database.InventoryEvidencePair
	for _, pair := range carry.pairs {
		if carry.rejudge[pair.ItemIdentityID] {
			continue
		}
		pair.ID, pair.JobID, pair.CreatedAt = identity.NewID(), job.ID, now
		pairs = append(pairs, pair)
	}
	return results, pairs
}

// judgedStaffPairs keeps the staff pairs of the items judged in this job: a
// carried item keeps the pairs it was judged with in generation N-1.
func judgedStaffPairs(staff []database.InventoryEvidencePair, carry *carryOver) []database.InventoryEvidencePair {
	if carry == nil {
		return staff
	}
	out := make([]database.InventoryEvidencePair, 0, len(staff))
	for _, pair := range staff {
		if carry.rejudge[pair.ItemIdentityID] {
			out = append(out, pair)
		}
	}
	return out
}

// submittedCoverageAnswers reads the component answers of one room from
// SUBMITTED drafts only (the latest answer per component and view wins) and
// applies the capture rule that an observation needs usable, ready evidence
// captured by the same responsibility.
func submittedCoverageAnswers(ctx context.Context, tx *gorm.DB, job database.InventoryJob, requirementKey string) ([]capturecore.ComponentCoverageAnswer, error) {
	var answers []database.ComponentAnswer
	if err := tx.WithContext(ctx).Table("capture.component_answers AS a").Select("a.*").
		Joins("JOIN capture.capture_drafts AS d ON d.tenant_id=a.tenant_id AND d.id=a.draft_id").
		Joins("JOIN inspections.responsibilities AS r ON r.tenant_id=d.tenant_id AND r.id=d.responsibility_id").
		Where("a.tenant_id=? AND r.inspection_id=? AND d.status='SUBMITTED' AND a.requirement_key=?", job.TenantID, job.InspectionID, requirementKey).
		Order("a.updated_at ASC, a.version ASC").Find(&answers).Error; err != nil {
		return nil, err
	}
	draftIDs := make([]identity.ID, 0, len(answers))
	var evidenceIDs []identity.ID
	evidenceOf := make([][]identity.ID, len(answers))
	for i, answer := range answers {
		draftIDs = append(draftIDs, answer.DraftID)
		ids, err := decodeCoverageEvidenceIDs(answer.EvidenceIDs)
		if err != nil {
			return nil, err
		}
		evidenceOf[i] = ids
		evidenceIDs = append(evidenceIDs, ids...)
	}
	responsibility := map[identity.ID]identity.ID{}
	if len(draftIDs) > 0 {
		var drafts []database.CaptureDraft
		if err := tx.WithContext(ctx).Select("id", "responsibility_id").Where("tenant_id=? AND id IN ?", job.TenantID, draftIDs).Find(&drafts).Error; err != nil {
			return nil, err
		}
		for _, draft := range drafts {
			responsibility[draft.ID] = draft.ResponsibilityID
		}
	}
	ready := map[identity.ID]identity.ID{}
	if len(evidenceIDs) > 0 {
		var media []database.MediaObject
		if err := tx.WithContext(ctx).Select("id", "responsibility_id").Where("tenant_id=? AND id IN ? AND status='READY'", job.TenantID, evidenceIDs).Find(&media).Error; err != nil {
			return nil, err
		}
		for _, item := range media {
			ready[item.ID] = item.ResponsibilityID
		}
	}
	out := make([]capturecore.ComponentCoverageAnswer, 0, len(answers))
	for i, answer := range answers {
		usable := false
		for _, id := range evidenceOf[i] {
			if owner, ok := ready[id]; ok && owner == responsibility[answer.DraftID] {
				usable = true
				break
			}
		}
		out = append(out, capturecore.ComponentCoverageAnswer{RequirementKey: answer.RequirementKey, ComponentKey: answer.ComponentKey, ViewKey: answer.ViewKey, State: capturecore.UsableAnswerState(answer.State, usable)})
	}
	return out, nil
}

func decodeCoverageEvidenceIDs(raw json.RawMessage) ([]identity.ID, error) {
	var ids []identity.ID
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return []identity.ID{}, nil
	}
	if trimmed[0] != '[' || json.Unmarshal(trimmed, &ids) != nil {
		return nil, fmt.Errorf("%w: invalid component evidence IDs", messaging.ErrPermanent)
	}
	for _, id := range ids {
		if id == (identity.ID{}) {
			return nil, fmt.Errorf("%w: invalid component evidence IDs", messaging.ErrPermanent)
		}
	}
	return ids, nil
}

// resolveCompareScopeRoom finds the pinned scope room of a COMPARE job by the
// capture requirement key or the baseline identity anchored during creation.
// An unresolved room is never considered covered.
func resolveCompareScopeRoom(rooms []compareScopeRoom, requirementKey string, requirement *capturecore.Requirement) (compareScopeRoom, bool) {
	identityKey := strings.TrimPrefix(requirementKey, "env:")
	for _, room := range rooms {
		if room.Key == identityKey || strings.TrimPrefix(room.Key, "new:") == identityKey {
			return room, true
		}
		if requirement != nil && requirement.EnvironmentIdentityID != "" && (requirement.EnvironmentIdentityID == room.Key || requirement.EnvironmentIdentityID == strings.TrimPrefix(room.Key, "new:")) {
			return room, true
		}
	}
	// Creation anchors descriptive requirements to the confirmed room key or
	// baseline identity. Name matching is deliberately not repeated here:
	// accepting a weaker pairing during processing could bind a job to a
	// different room than the one selected when the scope was confirmed.
	return compareScopeRoom{}, false
}

// roomCovered reports whether the room was observed as a whole, with the
// capture's own coverage rule (capturecore.RoomCovered); the answers are in
// submission order, so a later answer replaces an earlier one.
func roomCovered(room compareScopeRoom, answers []capturecore.ComponentCoverageAnswer) bool {
	return capturecore.RoomCovered(room, answers)
}

// baselineBasis reports, per item identity, whether the approved baseline
// shows the item: it must have been observed and cite at least one loaded
// origin view. Items without basis are never judged.
func baselineBasis(in jobInput) map[identity.ID]bool {
	loaded := make(map[string]bool, len(in.origin))
	for _, input := range in.origin {
		loaded[input.Image.EvidenceID] = true
	}
	withView := map[identity.ID]bool{}
	for _, row := range in.originRows {
		if loaded[evidenceID(row.Kind, row.MediaID, row.FrameID)] {
			withView[row.ItemID] = true
		}
	}
	basis := make(map[identity.ID]bool, len(in.items))
	for _, item := range in.items {
		basis[item.IdentityID] = withView[item.ID] && item.Observation != "NOT_OBSERVED" && item.Observation != "NOT_ASSESSABLE"
	}
	return basis
}

// staffItems lists the items whose pairing was decided by staff.
func staffItems(pairs []database.InventoryEvidencePair) map[identity.ID]bool {
	out := make(map[identity.ID]bool, len(pairs))
	for _, pair := range pairs {
		out[pair.ItemIdentityID] = true
	}
	return out
}

// withStaffCurrent keeps every current view of a confirmed staff pairing in
// the request budget, so a staff decision is judged with its own images.
func withStaffCurrent(all, selected []evidenceInput, staff []database.InventoryEvidencePair, max int) []evidenceInput {
	chosen := map[string]bool{}
	for _, input := range selected {
		chosen[input.Image.EvidenceID] = true
	}
	var required []evidenceInput
	for _, pair := range staff {
		if !pair.Confirmed {
			continue
		}
		id := evidenceID(pair.CurrentKind, pair.CurrentMediaID, pair.CurrentFrameID)
		if chosen[id] {
			continue
		}
		for _, input := range all {
			if input.Image.EvidenceID == id {
				required = append(required, input)
				chosen[id] = true
				break
			}
		}
	}
	if len(required) == 0 {
		return selected
	}
	out := append(required, selected...)
	if len(out) > max {
		out = out[:max]
	}
	return out
}

// foreignProposal reports a proposal for this origin that names a current
// evidence outside the job's room: such a pair is rejected.
func foreignProposal(proposals []matchPair, originID string, candidates map[string]evidenceInput) bool {
	for _, pair := range proposals {
		if pair.OriginEvidenceID != originID {
			continue
		}
		if _, ok := candidates[pair.CurrentEvidenceID]; !ok {
			return true
		}
	}
	return false
}

// revisionFailure is the carried-over part of a failed pairing revision.
type revisionFailure struct {
	carry *carryOver
}

// failedRevision reads, for a COMPARE job that exhausted its attempts, the
// items revised by staff for this very job and generation N-1 of its room.
// Jobs not opened by a pairing revision carry nothing.
func failedRevision(ctx context.Context, tx *gorm.DB, job database.InventoryJob) (revisionFailure, error) {
	var staff []database.InventoryEvidencePair
	if err := tx.WithContext(ctx).Where("tenant_id=? AND job_id=? AND source='STAFF'", job.TenantID, job.ID).Find(&staff).Error; err != nil {
		return revisionFailure{}, err
	}
	if len(staff) == 0 {
		return revisionFailure{}, nil
	}
	in := jobInput{revised: map[identity.ID]bool{}}
	for _, pair := range staff {
		in.revised[pair.ItemIdentityID] = true
	}
	if err := loadCarryOver(ctx, tx, job, &in); err != nil {
		return revisionFailure{}, err
	}
	return revisionFailure{carry: in.carryOver}, nil
}

// failedJobBasis reports, for a COMPARE job that exhausted its attempts,
// whether it is strict (photo inspection with a pinned scope) and, per item,
// whether the approved baseline shows it (see baselineBasis).
func failedJobBasis(ctx context.Context, tx *gorm.DB, job database.InventoryJob, items []database.InventoryItem) (map[identity.ID]bool, bool, error) {
	var inspection database.Inspection
	if err := tx.WithContext(ctx).Select("id", "media_kind").Where("tenant_id=? AND id=?", job.TenantID, job.InspectionID).Take(&inspection).Error; err != nil {
		return nil, false, err
	}
	if inspection.MediaKind != "PHOTO" {
		return nil, false, nil
	}
	var scopes int64
	if err := tx.WithContext(ctx).Model(&database.InspectionScopeSnapshot{}).Where("tenant_id=? AND inspection_id=?", job.TenantID, job.InspectionID).Count(&scopes).Error; err != nil {
		return nil, false, err
	}
	if scopes == 0 {
		return nil, false, nil
	}
	in := jobInput{items: items}
	if len(items) > 0 {
		ids := make([]identity.ID, 0, len(items))
		for _, item := range items {
			ids = append(ids, item.ID)
		}
		if err := tx.WithContext(ctx).Where("tenant_id=? AND item_id IN ?", job.TenantID, ids).Order("position ASC").Find(&in.originRows).Error; err != nil {
			return nil, false, err
		}
		origin, err := loadOriginEvidenceRefs(tx.WithContext(ctx), job.TenantID, in.originRows, job.RequirementKey)
		if err != nil {
			return nil, false, err
		}
		in.origin = origin
	}
	return baselineBasis(in), true, nil
}
