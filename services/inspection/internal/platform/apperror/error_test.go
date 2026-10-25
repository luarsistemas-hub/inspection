package apperror

import "testing"

func TestCounterpartyStateCodesRetainTheirErrorKind(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		kind Code
	}{
		{name: "UT-288 already responded is invalid state", err: New(AlreadyResponded, "response", "already responded"), kind: InvalidState},
		{name: "UT-291 deadline passed is invalid state", err: New(DeadlinePassed, "deadlineAt", "deadline passed"), kind: InvalidState},
		{name: "UT-299 already resolved is conflict", err: New(AlreadyResolved, "counterpartyInvitationId", "already resolved"), kind: Conflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !Is(tc.err, tc.kind) {
				t.Fatalf("Is(error, %s)=false", tc.kind)
			}
			if code, _, _ := Public(tc.err); code == tc.kind {
				t.Fatalf("public business code %s was lost", code)
			}
		})
	}
}
