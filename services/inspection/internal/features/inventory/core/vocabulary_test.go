package core

import (
	"strings"
	"testing"

	"inspection/services/inspection/internal/platform/apperror"
)

func TestInventoryVocabulary_UT001ToUT008(t *testing.T) {
	t.Run("UT001 blank name wins over OTHER type", func(t *testing.T) { assertInvalidField(t, ValidateEnvironment("OTHER", "  "), "name") })
	t.Run("UT002 control characters are rejected", func(t *testing.T) { assertInvalidField(t, ValidateName("Sala\u0007"), "name") })
	t.Run("UT003 name rune boundary", func(t *testing.T) {
		if err := ValidateName(strings.Repeat("a", 80)); err != nil {
			t.Fatal(err)
		}
		assertInvalidField(t, ValidateName(strings.Repeat("a", 81)), "name")
	})
	t.Run("UT004 unknown environment code", func(t *testing.T) { assertInvalidField(t, ValidateEnvironment("GARDEN_X", "Jardim"), "typeCode") })
	t.Run("UT005 quantity boundaries", func(t *testing.T) {
		for _, n := range []int{0, -1, 1000} {
			q := n
			assertInvalidField(t, ValidateItemPatch(ItemPatch{Quantity: &q}), "quantity")
		}
		for _, n := range []int{1, 999} {
			q := n
			if err := ValidateItemPatch(ItemPatch{Quantity: &q}); err != nil {
				t.Fatalf("quantity %d rejected: %v", n, err)
			}
		}
	})
	t.Run("UT006 condition outside scale", func(t *testing.T) {
		c := "EXCELLENT"
		assertInvalidField(t, ValidateItemPatch(ItemPatch{Condition: &c}), "condition")
	})
	t.Run("UT color column rune boundary", func(t *testing.T) {
		atLimit, tooLong := strings.Repeat("a", 100), strings.Repeat("a", 101)
		if err := ValidateItemPatch(ItemPatch{Color: &atLimit}); err != nil {
			t.Fatalf("100-rune color rejected: %v", err)
		}
		assertInvalidField(t, ValidateItemPatch(ItemPatch{Color: &tooLong}), "color")
	})
	t.Run("attribute validation is deterministic and rejects formatting controls", func(t *testing.T) {
		long := strings.Repeat("a", 201)
		for i := 0; i < 20; i++ {
			assertInvalidField(t, ValidateItemPatch(ItemPatch{Material: &long, Color: &long}), "material")
		}
		for _, tc := range []struct {
			field string
			patch ItemPatch
		}{{"material", ItemPatch{Material: ptr("madeira\nlaqueada")}}, {"color", ItemPatch{Color: ptr("azul\u202e")}}, {"limitation", ItemPatch{Limitation: ptr("obstruído\t")}}} {
			assertInvalidField(t, ValidateItemPatch(tc.patch), tc.field)
		}
	})
	t.Run("UT007 nil attributes persist as SQL NULL", func(t *testing.T) {
		if err := ValidateItemPatch(ItemPatch{}); err != nil {
			t.Fatalf("nil material/color/quantity should be accepted as unknown: %v", err)
		}
	})
	t.Run("UT008 condition comparison", func(t *testing.T) {
		cases := []struct {
			before, after string
			want          ConditionComparison
		}{{"GOOD", "FAIR", ConditionWorsened}, {"POOR", "GOOD", ConditionImproved}, {"GOOD", "GOOD", ConditionUnchanged}, {"NOT_ASSESSABLE", "GOOD", ConditionNotComparable}, {"GOOD", "NOT_ASSESSABLE", ConditionNotComparable}}
		for _, c := range cases {
			if got := CompareCondition(c.before, c.after); got != c.want {
				t.Errorf("CompareCondition(%s,%s)=%s, want %s", c.before, c.after, got, c.want)
			}
		}
	})
}

func TestInventoryVocabularyMatchesTechSpec(t *testing.T) {
	if got := len(EnvironmentTypes()); got != 19 {
		t.Fatalf("environment types=%d, want 19", got)
	}
	if got := len(ItemTypes()); got != 30 {
		t.Fatalf("item types=%d, want 30", got)
	}
	var scale []string
	for _, level := range Conditions() {
		if level.Label == "" || level.Description == "" {
			t.Fatalf("condition %s lacks pt-BR label or definition", level.Code)
		}
		scale = append(scale, string(level.Code))
	}
	if strings.Join(scale, ",") != "NEW,GOOD,FAIR,POOR,NOT_ASSESSABLE" {
		t.Fatalf("condition scale order=%v", scale)
	}
	if label, ok := EnvironmentLabel("TOILET"); !ok || label != "Lavabo" {
		t.Fatalf("TOILET label=%q ok=%v", label, ok)
	}
	if label, ok := ItemLabel("TOILET_FIXTURE"); !ok || label != "Louça sanitária" {
		t.Fatalf("TOILET_FIXTURE label=%q ok=%v", label, ok)
	}
	environments := EnvironmentTypes()
	environments[0].Label = "mutated"
	if label, _ := EnvironmentLabel(string(environments[0].Code)); label == "mutated" {
		t.Fatal("EnvironmentTypes exposes the shared vocabulary slice")
	}
	if err := ValidateItem("SOFA", "Sofá da sala"); err != nil {
		t.Fatalf("valid item rejected: %v", err)
	}
	assertInvalidField(t, ValidateItem("SPACESHIP", "Nave"), "typeCode")
	assertInvalidField(t, ValidateItem("OTHER", " "), "name")
	for _, observation := range Observations() {
		value := string(observation)
		if err := ValidateItemPatch(ItemPatch{Observation: &value}); err != nil {
			t.Fatalf("observation %s rejected: %v", observation, err)
		}
	}
	unknown := "SEEN"
	assertInvalidField(t, ValidateItemPatch(ItemPatch{Observation: &unknown}), "observation")
}

func ptr(value string) *string { return &value }

func assertInvalidField(t *testing.T, err error, want string) {
	t.Helper()
	code, field, _ := apperror.Public(err)
	if code != apperror.InvalidInput || field != want {
		t.Fatalf("error=(%s,%q), want (%s,%q): %v", code, field, apperror.InvalidInput, want, err)
	}
}
