package core

import (
	"context"
	"encoding/json"
	"errors"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/templates/catalog"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

// LatestSubmittedAnswer loads the newest answer to one requirement among the
// inspection's SUBMITTED capture drafts. An OPEN (in progress or abandoned)
// recapture and an answer superseded by a later submission never count. It
// is the single rule shared by the report, the comparison executor and the
// pairing revision; answer is left untouched and gorm.ErrRecordNotFound is
// returned when no submitted draft answered the requirement.
func LatestSubmittedAnswer(ctx context.Context, tx *gorm.DB, tenantID, inspectionID identity.ID, requirementKey string, answer *database.RequirementAnswer) error {
	return tx.WithContext(ctx).Where("tenant_id=? AND requirement_key=? AND draft_id IN (SELECT id FROM capture.capture_drafts WHERE tenant_id=? AND status='SUBMITTED' AND responsibility_id IN (SELECT id FROM inspections.responsibilities WHERE tenant_id=? AND inspection_id=?))", tenantID, requirementKey, tenantID, tenantID, inspectionID).Order("updated_at DESC").First(answer).Error
}

// LatestSubmittedMedia returns the media of the newest submitted answer to
// one requirement (see LatestSubmittedAnswer), or nil when there is none or
// the room was declared impossible to capture.
func LatestSubmittedMedia(ctx context.Context, tx *gorm.DB, tenantID, inspectionID identity.ID, requirementKey string) ([]identity.ID, error) {
	var answer database.RequirementAnswer
	if err := LatestSubmittedAnswer(ctx, tx, tenantID, inspectionID, requirementKey, &answer); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if answer.ImpossibilityReason != "" {
		return nil, nil
	}
	var media []identity.ID
	if err := json.Unmarshal(answer.MediaIDs, &media); err != nil {
		return nil, err
	}
	return media, nil
}

// UsableAnswerState applies the capture coverage rule to one component
// answer: an OBSERVED or PARTIAL answer without any usable (ready) evidence
// does not observe anything.
func UsableAnswerState(state string, hasUsableEvidence bool) string {
	if (state == "OBSERVED" || state == "PARTIAL") && !hasUsableEvidence {
		return "NOT_OBSERVED"
	}
	return state
}

// RoomCovered reports whether a pinned room was observed as a whole under the
// same component rule the capture coverage uses (EvaluateCoverage): every
// expected component is OBSERVED, either by a component-level answer or in
// each of its expected views, or justified NOT_APPLICABLE, and at least one
// component is OBSERVED. Partial, impossible or missing components leave the
// room uncovered.
func RoomCovered(room catalog.ExpectedRoom, answers []ComponentCoverageAnswer) bool {
	requirementKey := "env:" + room.Key
	answerBy := make(map[string]ComponentCoverageAnswer, len(answers))
	for _, answer := range answers {
		answerBy[answer.RequirementKey+"|"+answer.ComponentKey+"|"+answer.ViewKey] = answer
	}
	observed := 0
	for _, component := range room.Components {
		state, _ := evaluateComponentState(requirementKey, component, answerBy)
		switch state {
		case "OBSERVED":
			observed++
		case "NOT_APPLICABLE":
		default:
			return false
		}
	}
	return observed > 0
}
