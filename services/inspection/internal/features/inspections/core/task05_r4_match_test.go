package core

import (
	"testing"

	capturecore "inspection/services/inspection/internal/features/capture/core"
	"inspection/services/inspection/internal/features/templates/catalog"
)

// Item 2 (round 4): a scope room is linked to a baseline environment first by
// the key the environment was captured with, then by name and type only when
// the match is unique in both directions, and each baseline environment backs
// at most one room.
func TestTask05R4ScopeRoomsMatchBaselineEnvironmentsOneToOne(t *testing.T) {
	const id1, id2, id3, id4 = "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222", "33333333-3333-3333-3333-333333333333", "44444444-4444-4444-4444-444444444444"
	baseline := func(id, typeCode, name string) capturecore.Requirement {
		return capturecore.Requirement{Key: "env:" + id, EnvironmentIdentityID: id, EnvironmentTypeCode: typeCode, EnvironmentName: name, EvidenceKind: "PHOTO"}
	}
	limits := catalog.DefaultEnvironmentCapture()
	identities := func(got []capturecore.Requirement) map[string]string {
		out := map[string]string{}
		for _, requirement := range got {
			out[requirement.Key] = requirement.EnvironmentIdentityID
		}
		return out
	}
	quartos := []capturecore.Requirement{baseline(id1, "BEDROOM", "Quarto"), baseline(id2, "BEDROOM", "Quarto")}
	rooms := []catalog.ExpectedRoom{{Key: "quarto1", TypeCode: "BEDROOM", Name: "Quarto"}, {Key: "quarto2", TypeCode: "BEDROOM", Name: "Quarto"}}
	for _, tc := range []struct {
		name     string
		baseline []capturecore.Requirement
		rooms    []catalog.ExpectedRoom
		keys     map[string]string
		want     map[string]string
	}{
		{"duplicate names without captured keys stay unmatched", quartos, rooms, nil, map[string]string{"env:quarto1": "quarto1", "env:quarto2": "quarto2"}},
		{"duplicate names resolved by the captured keys", quartos, rooms, map[string]string{id1: "env:quarto2", id2: "env:quarto1"}, map[string]string{"env:quarto1": id2, "env:quarto2": id1}},
		{"one captured key leaves the other duplicate unique", quartos, rooms, map[string]string{id1: "env:quarto1"}, map[string]string{"env:quarto1": id1, "env:quarto2": id2}},
		{"renamed baseline room matched by its captured key", []capturecore.Requirement{baseline(id1, "LIVING_ROOM", "Sala de estar")}, []catalog.ExpectedRoom{{Key: "sala", TypeCode: "LIVING_ROOM", Name: "Sala"}}, map[string]string{id1: "env:sala"}, map[string]string{"env:sala": id1}},
		{"renamed baseline room without key is not guessed", []capturecore.Requirement{baseline(id1, "LIVING_ROOM", "Sala de estar")}, []catalog.ExpectedRoom{{Key: "sala", TypeCode: "LIVING_ROOM", Name: "Sala"}}, nil, map[string]string{"env:sala": "sala"}},
		{"same name of another type is not a match", []capturecore.Requirement{baseline(id1, "OFFICE", "Sala")}, []catalog.ExpectedRoom{{Key: "sala", TypeCode: "LIVING_ROOM", Name: "Sala"}}, nil, map[string]string{"env:sala": "sala"}},
		{"extra baseline room and extra scope room", []capturecore.Requirement{baseline(id1, "KITCHEN", "Cozinha"), baseline(id3, "BALCONY", "Varanda")}, []catalog.ExpectedRoom{{Key: "cozinha", TypeCode: "KITCHEN", Name: "cozinha "}, {Key: "lavanderia", TypeCode: "LAUNDRY", Name: "Lavanderia"}}, nil, map[string]string{"env:cozinha": id1, "env:lavanderia": "lavanderia"}},
		{"a captured key wins over a name match of another room", []capturecore.Requirement{baseline(id1, "BEDROOM", "Quarto"), baseline(id4, "BEDROOM", "Suíte")}, []catalog.ExpectedRoom{{Key: "quarto", TypeCode: "BEDROOM", Name: "Quarto"}, {Key: "suite", TypeCode: "BEDROOM", Name: "Suíte"}}, map[string]string{id4: "env:quarto"}, map[string]string{"env:quarto": id4, "env:suite": "suite"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := identities(descriptiveEnvironmentRequirements(tc.baseline, tc.rooms, limits, catalog.FixedOrigin, true, tc.keys))
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			used := map[string]string{}
			for key, want := range tc.want {
				if got[key] != want {
					t.Fatalf("%s -> %q, want %q (all %v)", key, got[key], want, got)
				}
				if other, dup := used[got[key]]; dup {
					t.Fatalf("%s and %s read the same baseline environment %s", key, other, got[key])
				}
				used[got[key]] = key
			}
		})
	}
}
