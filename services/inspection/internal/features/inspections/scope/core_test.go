package scope

import (
	"testing"

	"inspection/services/inspection/internal/features/templates/catalog"
)

func TestResolveScope_UT008_UT009_UT010_UT108(t *testing.T) {
	template := catalog.TemplateDocument{
		SchemaVersion: catalog.DescriptiveSchemaVersion,
		AssetProfile:  ProfilePropertyComponents,
		ExpectedRooms: []catalog.ExpectedRoom{
			{Key: "kitchen", TypeCode: "KITCHEN", Name: "Cozinha", Components: []catalog.ExpectedComponent{{Key: "floor", TypeCode: "FLOOR", Label: "Piso", Views: []string{"geral", "detalhe"}}, {Key: "cabinet", TypeCode: "CABINET", Label: "Armários", Views: []string{"geral"}}}},
			{Key: "bathroom", TypeCode: "BATHROOM", Name: "Banheiro", Components: []catalog.ExpectedComponent{{Key: "sink", TypeCode: "SINK", Label: "Pia"}}},
		},
	}
	t.Run("UT-008 effective profile and scope", func(t *testing.T) {
		resolved, err := Resolve(template, "template-v1", &Override{Profile: ProfileFurnishedProperty})
		if err != nil {
			t.Fatal(err)
		}
		if resolved.Profile != ProfileFurnishedProperty || len(resolved.Rooms) != 2 || resolved.Digest == "" {
			t.Fatalf("unexpected effective scope: %+v", resolved)
		}
	})
	t.Run("UT-009 unknown profile and component", func(t *testing.T) {
		if _, err := Resolve(template, "template-v1", &Override{Profile: "UNKNOWN"}); err == nil {
			t.Fatal("unknown profile accepted")
		}
		if _, err := Resolve(template, "template-v1", &Override{Profile: ProfileSelectedItems, Selections: []Selection{{RoomKey: "kitchen", ComponentKey: "unknown"}}}); err == nil {
			t.Fatal("unknown component accepted")
		}
	})
	t.Run("UT-010 empty scope remains explicit", func(t *testing.T) {
		emptyTemplate := template
		emptyTemplate.ExpectedRooms = nil
		resolved, err := Resolve(emptyTemplate, "template-v1", nil)
		if err != nil || len(resolved.Rooms) != 0 || resolved.Digest == "" {
			t.Fatalf("empty scope was not preserved: %+v, %v", resolved, err)
		}
	})
	t.Run("rooms without components remain unless a selection narrows the scope", func(t *testing.T) {
		withEmptyRoom := template
		withEmptyRoom.ExpectedRooms = append(append([]catalog.ExpectedRoom(nil), template.ExpectedRooms...), catalog.ExpectedRoom{Key: "attic", TypeCode: "ATTIC", Name: "Sótão"})
		resolved, err := Resolve(withEmptyRoom, "template-v1", nil)
		if err != nil || len(resolved.Rooms) != 3 || resolved.Rooms[2].Key != "attic" {
			t.Fatalf("unselected empty room was dropped: rooms=%+v err=%v", resolved.Rooms, err)
		}
		selected, err := Resolve(withEmptyRoom, "template-v1", &Override{Profile: ProfileSelectedItems, Selections: []Selection{{RoomKey: "kitchen", ComponentKey: "floor"}}})
		if err != nil || len(selected.Rooms) != 1 {
			t.Fatalf("narrowed scope retained an unselected room: rooms=%+v err=%v", selected.Rooms, err)
		}
	})
	t.Run("UT-108 selected items are the denominator", func(t *testing.T) {
		resolved, err := Resolve(template, "template-v1", &Override{Profile: ProfileSelectedItems, Selections: []Selection{{RoomKey: "kitchen", ComponentKey: "floor"}, {RoomKey: "bathroom", ComponentKey: "sink"}}})
		if err != nil {
			t.Fatal(err)
		}
		if resolved.Profile != ProfileSelectedItems || len(resolved.Rooms) != 2 || len(resolved.Rooms[0].Components) != 1 || resolved.Rooms[0].Components[0].Key != "floor" || resolved.Rooms[1].Components[0].Key != "sink" {
			t.Fatalf("selected components differ from the requested denominator: %+v", resolved)
		}
	})
}
