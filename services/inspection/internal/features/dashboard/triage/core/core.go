// Package core contains the state rules for human triage review.
package core

import "strings"

const (
	New              = "NEW"
	InReview         = "IN_REVIEW"
	AwaitingEvidence = "AWAITING_EVIDENCE"
	Reviewed         = "REVIEWED"
	Archived         = "ARCHIVED"
)

// Transition validates an action and returns the next review state.
func Transition(status, action string, hasAssignee, hasBody, hasDisposition bool) (string, bool) {
	switch action {
	case "TAKE":
		if status == New && !hasAssignee {
			return InReview, true
		}
	case "ASSIGN":
		if status != Reviewed && status != Archived && hasAssignee {
			return status, true
		}
	case "NOTE":
		if status != Archived && hasBody {
			return status, true
		}
	case "COMPLETE":
		if status == InReview && hasBody && hasDisposition {
			return Reviewed, true
		}
	case "REOPEN":
		if status == Reviewed && !hasAssignee {
			return New, true
		}
	case "WAIT_FOR_EVIDENCE":
		if status == InReview && hasBody {
			return AwaitingEvidence, true
		}
	}
	return status, false
}

// ValidDisposition checks the supported human review outcomes.
func ValidDisposition(value string) bool {
	switch value {
	case "NO_ACTION", "REFERRED", "EXTERNAL_FOLLOWUP":
		return true
	default:
		return false
	}
}

// NormalizeBody trims review notes and decisions before they are persisted.
func NormalizeBody(value string) string { return strings.TrimSpace(value) }
