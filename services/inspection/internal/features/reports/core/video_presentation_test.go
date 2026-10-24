package core

import (
	"strings"
	"testing"
)

func TestLocalizeVideoPresentationKeepsCanonicalLabels(t *testing.T) {
	snapshot := Snapshot{
		VideoPlanID: "plan-1",
		Requirements: []Requirement{
			{Key: "video:fireplace", Label: "living_room — fireplace", Instructions: "Para recaptura: filme living_room — fireplace de perto, com boa luz e sem obstrução, mostrando a vista front_view.", Coverage: "Visão de living_room — fireplace obstruída no vídeo atual."},
			{Key: "video:custom", Label: "atelier — custom fixture", Instructions: "Filme atelier — custom fixture."},
			{Key: "other", Label: "living_room — fireplace"},
		},
		ScopeStatements: []string{"Conclusões limitadas ao escopo observado: living_room — fireplace."},
	}
	localized := LocalizeVideoPresentation(snapshot)
	if got := localized.Requirements[0].Label; got != "Sala de estar — Lareira" {
		t.Fatalf("video label = %q", got)
	}
	if got := localized.Requirements[0].Instructions; got != "Para recaptura: filme Sala de estar — Lareira de perto, com boa luz e sem obstrução, mostrando a vista frontal." {
		t.Fatalf("guidance = %q", got)
	}
	if got := localized.Requirements[0].Coverage; got != "Visão de Sala de estar — Lareira obstruída no vídeo atual." {
		t.Fatalf("coverage = %q", got)
	}
	if got := localized.ScopeStatements[0]; !strings.Contains(got, "Sala de estar — Lareira") {
		t.Fatalf("scope statement = %q", got)
	}
	if localized.Requirements[1].Label != "Ambiente não identificado 1 — Componente não identificado" || localized.Requirements[2].Label != snapshot.Requirements[2].Label {
		t.Fatal("unknown video or non-video label was not handled")
	}
	if snapshot.Requirements[0].Label != "living_room — fireplace" || strings.Contains(snapshot.Requirements[0].Instructions, "frontal") || strings.Contains(snapshot.ScopeStatements[0], "Sala de estar") {
		t.Fatal("canonical snapshot mutated")
	}
}

func TestVideoHTMLUsesPortuguesePresentation(t *testing.T) {
	snapshot := validSnapshot()
	snapshot.VideoPlanID = "plan-1"
	snapshot.Requirements = []Requirement{{Key: "video:fireplace", Section: "Vídeo", Label: "living_room — fireplace", Instructions: "Para recaptura: filme living_room — fireplace, mostrando a vista front_view.", AnalysisMode: "CURRENT_ONLY", AnalysisStatus: "INCONCLUSIVE"}}
	html, err := HTML(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(html), "Sala de estar — Lareira") || !strings.Contains(string(html), "vista frontal") || strings.Contains(string(html), "living_room") {
		t.Fatalf("video HTML is not localized: %s", html)
	}
}

func TestVideoEnvironmentKeepsRoomNumber(t *testing.T) {
	if got := localizeVideoLabel("bedroom_2 — window", map[string]string{}); got != "Quarto 2 — Janela" {
		t.Fatalf("numbered room = %q", got)
	}
	if got := localizeVideoGuidanceView("Para recaptura: filme Quarto 2 — Janela, mostrando a vista front view."); got != "Para recaptura: filme Quarto 2 — Janela, mostrando a vista frontal." {
		t.Fatalf("view with spaces = %q", got)
	}
}

func TestLocalizeUnknownVideoEnvironmentsWithPortugueseFallback(t *testing.T) {
	snapshot := Snapshot{VideoPlanID: "plan", Requirements: []Requirement{
		{Key: "video:a", Label: "outdoor_kitchen — sink"},
		{Key: "video:b", Label: "atelier — fixture"},
		{Key: "video:c", Label: "custom_room — door"},
	}}
	localized := LocalizeVideoPresentation(snapshot)
	want := []string{"Área gourmet — Pia", "Ambiente não identificado 1 — Componente não identificado", "Ambiente não identificado 2 — Porta"}
	for i, requirement := range localized.Requirements {
		if requirement.Label != want[i] {
			t.Fatalf("requirement %s label = %q, want %q", requirement.Key, requirement.Label, want[i])
		}
	}
}

func TestLocalizedVideoLabelsArePreserved(t *testing.T) {
	snapshot := Snapshot{VideoPlanID: "plan", Requirements: []Requirement{{Key: "video:outdoor", Label: "Cozinha externa — Churrasqueira", PresentationLocalized: true}}}
	got := LocalizeVideoPresentation(snapshot).Requirements[0].Label
	if got != "Cozinha externa — Churrasqueira" {
		t.Fatalf("LLM display label = %q", got)
	}
}

func TestLocalizeVideoPresentationTranslatesStoneAccentWall(t *testing.T) {
	snapshot := Snapshot{
		VideoPlanID: "plan-1",
		Requirements: []Requirement{{
			Key:          "video:stone-accent-wall",
			Label:        "living_room — stone_accent_wall",
			Instructions: "Para recaptura: filme living_room — stone_accent_wall de perto, com boa luz e sem obstrução, mostrando a vista wall_and_corner.",
			Coverage:     "Visão de living_room — stone_accent_wall obstruída no vídeo atual.",
		}},
	}

	localized := LocalizeVideoPresentation(snapshot)
	requirement := localized.Requirements[0]
	if requirement.Label != "Sala de estar — Parede de destaque em pedra" {
		t.Fatalf("video label = %q", requirement.Label)
	}
	if !strings.Contains(requirement.Instructions, "Sala de estar — Parede de destaque em pedra") || !strings.HasSuffix(requirement.Instructions, "mostrando a vista parede e canto.") {
		t.Fatalf("video instructions = %q", requirement.Instructions)
	}
	if requirement.Coverage != "Visão de Sala de estar — Parede de destaque em pedra obstruída no vídeo atual." {
		t.Fatalf("video coverage = %q", requirement.Coverage)
	}
	if snapshot.Requirements[0].Label != "living_room — stone_accent_wall" {
		t.Fatal("canonical snapshot mutated")
	}
}
