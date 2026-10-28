package resolvers

import (
	"time"

	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	graphql1 "inspection/services/inspection/internal/platform/graphql"
)

func deliverySuccessful(status string) bool {
	return status == "DELIVERED" || status == "SENT" || status == "ACCEPTED"
}

func deliveryFailed(status string) bool { return status == "FAILED" || status == "CANCELED" }

// deliveryRank orders notification states from failed to confirmed, so the
// best channel of a generation represents it.
func deliveryRank(status string) int {
	switch status {
	case "DELIVERED":
		return 6
	case "SENT":
		return 5
	case "ACCEPTED":
		return 4
	case "PROCESSING":
		return 3
	case "QUEUED":
		return 2
	case "UNKNOWN":
		return 1
	default:
		return 0
	}
}

func counterpartyMutationErrors(err error, mutationID string) ([]*graphql1.UserError, bool) {
	code, _, _ := apperror.Public(err)
	if code == apperror.Internal || code == apperror.DependencyUnavailable || code == apperror.Unauthenticated || code == apperror.Forbidden || code == apperror.RateLimited || code == apperror.SessionExpired {
		return nil, false
	}
	return []*graphql1.UserError{userErrorFrom(err)}, true
}

func counterpartyResponsePayload(row database.CounterpartyInvitation, mutationID string) *graphql1.CounterpartyResponsePayload {
	respondedAt := ""
	if row.RespondedAt != nil {
		respondedAt = row.RespondedAt.UTC().Format(time.RFC3339Nano)
	}
	var timestamp *string
	if respondedAt != "" {
		timestamp = &respondedAt
	}
	return &graphql1.CounterpartyResponsePayload{Response: row.Response, RespondedAt: timestamp, UserErrors: []*graphql1.UserError{}, ClientMutationID: mutationID}
}

func counterpartyResponseErrorPayload(errors []*graphql1.UserError, mutationID string) *graphql1.CounterpartyResponsePayload {
	return &graphql1.CounterpartyResponsePayload{Response: "PENDING", UserErrors: errors, ClientMutationID: mutationID}
}

func nonEmptyStringPointer(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func counterpartyInvitationPayload(row database.CounterpartyInvitation, mutationID string) *graphql1.CounterpartyInvitationPayload {
	var adjustment *string
	if row.AdjustmentVersionID != nil {
		value := row.AdjustmentVersionID.String()
		adjustment = &value
	}
	return &graphql1.CounterpartyInvitationPayload{InvitationID: row.ID.String(), Response: row.Response, DeadlineAt: row.DeadlineAt.UTC().Format(time.RFC3339Nano), Resolution: row.Resolution, AdjustmentVersionID: adjustment, UserErrors: []*graphql1.UserError{}, ClientMutationID: mutationID}
}

func counterpartyInvitationErrorPayload(errors []*graphql1.UserError, mutationID string) *graphql1.CounterpartyInvitationPayload {
	return &graphql1.CounterpartyInvitationPayload{InvitationID: "00000000-0000-0000-0000-000000000000", Response: "PENDING", DeadlineAt: time.Unix(0, 0).UTC().Format(time.RFC3339Nano), Resolution: "OPEN", UserErrors: errors, ClientMutationID: mutationID}
}
