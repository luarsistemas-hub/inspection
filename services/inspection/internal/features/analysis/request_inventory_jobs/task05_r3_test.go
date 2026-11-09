package request_inventory_jobs

import (
	"testing"

	"inspection/libs/identity"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	"inspection/services/inspection/internal/platform/database"
)

// TestTask05R3CompareJobReadsTheMatchedDescriptiveRequirement: a descriptive
// (v3) move-out keys a baseline room by its scope key ("env:sala") and records
// the matched baseline identity; the room's COMPARE job must read that
// requirement. Captures keyed by the identity keep their historical key, and
// a room without a matched requirement keeps "env:<identity>".
func TestTask05R3CompareJobReadsTheMatchedDescriptiveRequirement(t *testing.T) {
	env := database.InventoryEnvironment{IdentityID: identity.NewID()}
	identityKey := "env:" + env.IdentityID.String()
	cases := []struct {
		name         string
		requirements []capturecore.Requirement
		want         string
	}{
		{"descriptive scope key", []capturecore.Requirement{{Key: "env:sala", EnvironmentIdentityID: env.IdentityID.String()}}, "env:sala"},
		{"historical identity key", []capturecore.Requirement{{Key: identityKey, EnvironmentIdentityID: env.IdentityID.String()}}, identityKey},
		{"room added during capture is not the baseline room", []capturecore.Requirement{{Key: "env:new:sala", EnvironmentIdentityID: env.IdentityID.String(), AddedDuringCapture: true}}, identityKey},
		{"unmatched baseline room", []capturecore.Requirement{{Key: "env:quarto", EnvironmentIdentityID: identity.NewID().String()}}, identityKey},
	}
	for _, tc := range cases {
		if got := baselineRequirementKey(env, tc.requirements); got != tc.want {
			t.Errorf("%s: key = %q; want %q", tc.name, got, tc.want)
		}
	}
}
