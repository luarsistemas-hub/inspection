package resolvers

import (
	"encoding/json"
	"strings"

	"inspection/libs/identity"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	"inspection/services/inspection/internal/platform/database"

	graphql1 "inspection/services/inspection/internal/platform/graphql"
)

func captureReviewHandoff(invitation database.Invitation, inspectionID identity.ID, originVersionID *identity.ID) *graphql1.CaptureReviewHandoff {
	if invitation.ReviewerMembershipID == nil {
		return nil
	}
	handoff := &graphql1.CaptureReviewHandoff{InspectionID: inspectionID.String()}
	if originVersionID != nil {
		versionID := originVersionID.String()
		handoff.OriginVersionID = &versionID
	}
	return handoff
}

func mapCaptureEnvironments(view capturecore.Bootstrap) []*graphql1.CaptureEnvironment {
	answers := make(map[string]database.RequirementAnswer, len(view.Answers))
	for _, answer := range view.Answers {
		answers[answer.RequirementKey] = answer
	}
	result := make([]*graphql1.CaptureEnvironment, 0)
	for _, req := range view.Requirements {
		if !strings.HasPrefix(req.Key, "env:") {
			continue
		}
		answer := answers[req.Key]
		var mediaIDs []identity.ID
		_ = json.Unmarshal(answer.MediaIDs, &mediaIDs)
		status := "PENDING"
		if strings.TrimSpace(answer.ImpossibilityReason) != "" {
			status = "IMPOSSIBLE"
		} else if len(mediaIDs) > 0 {
			status = "CAPTURED"
		}
		result = append(result, &graphql1.CaptureEnvironment{RequirementKey: req.Key, IdentityID: req.EnvironmentIdentityID, TypeCode: req.EnvironmentTypeCode, Name: req.EnvironmentName, Expected: req.Expected, AddedDuringCapture: req.AddedDuringCapture, MediaCount: len(mediaIDs), Status: status})
	}
	return result
}
