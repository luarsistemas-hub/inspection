package core

import "testing"

func TestReviewTransitions(t *testing.T) {
	cases := []struct {
		status, action                     string
		assigned, body, disposition, valid bool
		want                               string
	}{
		{New, "TAKE", false, false, false, true, InReview},
		{New, "TAKE", true, false, false, false, New},
		{InReview, "NOTE", false, true, false, true, InReview},
		{InReview, "COMPLETE", false, true, true, true, Reviewed},
		{InReview, "COMPLETE", false, false, true, false, InReview},
		{InReview, "WAIT_FOR_EVIDENCE", false, true, false, true, AwaitingEvidence},
		{AwaitingEvidence, "COMPLETE", false, true, true, false, AwaitingEvidence},
		{Reviewed, "REOPEN", false, false, false, true, New},
		{Archived, "REOPEN", false, false, false, false, Archived},
	}
	for _, test := range cases {
		got, valid := Transition(test.status, test.action, test.assigned, test.body, test.disposition)
		if valid != test.valid || got != test.want {
			t.Errorf("Transition(%q,%q)=(%q,%t), want (%q,%t)", test.status, test.action, got, valid, test.want, test.valid)
		}
	}
}

func TestValidDisposition(t *testing.T) {
	for _, value := range []string{"NO_ACTION", "REFERRED", "EXTERNAL_FOLLOWUP"} {
		if !ValidDisposition(value) {
			t.Errorf("%q should be accepted", value)
		}
	}
	if ValidDisposition("CORRECTED") {
		t.Fatal("a review decision must not claim the physical issue is corrected")
	}
}
