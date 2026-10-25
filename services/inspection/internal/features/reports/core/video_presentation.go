package core

import (
	"sort"
	"strconv"
	"strings"
)

var videoEnvironmentNames = map[string]string{
	"bathroom": "Banheiro", "bedroom": "Quarto", "dining_room": "Sala de jantar",
	"entrance": "Entrada", "garage": "Garagem", "hall": "Corredor",
	"hallway": "Corredor", "kitchen": "Cozinha", "laundry_room": "Lavanderia",
	"living_room": "Sala de estar", "office": "Escritório", "outdoor_area": "Área externa",
	"patio": "Pátio", "service_area": "Área de serviço", "storage_room": "Depósito",
	"terrace": "Terraço", "balcony": "Varanda",
}

var videoComponentNames = map[string]string{
	"basin": "Lavatório", "cabinet": "Armário", "ceiling": "Teto",
	"countertop": "Bancada", "door": "Porta", "fireplace": "Lareira",
	"floor": "Piso", "furniture": "Móvel", "mirror": "Espelho",
	"shower": "Chuveiro", "sink": "Pia", "stove": "Fogão",
	"stone_accent_wall": "Parede de destaque em pedra",
	"toilet":            "Vaso sanitário", "wall": "Parede", "window": "Janela",
}

var videoViewNames = map[string]string{
	"close_up": "aproximada", "detail": "de detalhe", "front": "frontal",
	"front_view": "frontal", "side": "lateral", "side_view": "lateral",
	"wall_and_corner": "parede e canto", "wide": "ampla", "wide_view": "ampla",
}

// LocalizeVideoPresentation returns a display copy of a video report. Its
// canonical snapshot and the labels used for video matching stay unchanged.
func LocalizeVideoPresentation(snapshot Snapshot) Snapshot {
	if snapshot.VideoPlanID == "" {
		return snapshot
	}
	snapshot.Requirements = append([]Requirement(nil), snapshot.Requirements...)
	labels := make(map[string]string, len(snapshot.Requirements))
	for i := range snapshot.Requirements {
		requirement := &snapshot.Requirements[i]
		if !strings.HasPrefix(requirement.Key, "video:") && !strings.HasPrefix(requirement.Key, "env:") {
			continue
		}
		original := requirement.Label
		requirement.Label = localizeVideoLabel(original)
		if original != "" {
			requirement.Instructions = strings.ReplaceAll(requirement.Instructions, original, requirement.Label)
			requirement.Coverage = strings.ReplaceAll(requirement.Coverage, original, requirement.Label)
		}
		requirement.Instructions = localizeVideoGuidanceView(requirement.Instructions)
		if original != "" && original != requirement.Label {
			labels[original] = requirement.Label
		}
	}
	snapshot.ScopeStatements = append([]string(nil), snapshot.ScopeStatements...)
	originals := make([]string, 0, len(labels))
	for original := range labels {
		originals = append(originals, original)
	}
	sort.Slice(originals, func(i, j int) bool {
		if len(originals[i]) == len(originals[j]) {
			return originals[i] < originals[j]
		}
		return len(originals[i]) > len(originals[j])
	})
	for i, statement := range snapshot.ScopeStatements {
		for _, original := range originals {
			statement = strings.ReplaceAll(statement, original, labels[original])
		}
		snapshot.ScopeStatements[i] = statement
	}
	return snapshot
}

func localizeVideoLabel(value string) string {
	parts := strings.SplitN(value, " — ", 2)
	if len(parts) != 2 {
		return localizeVideoTerm(value, videoComponentNames)
	}
	return localizeVideoTerm(parts[0], videoEnvironmentNames) + " — " + localizeVideoTerm(parts[1], videoComponentNames)
}

func localizeVideoGuidanceView(instructions string) string {
	const marker = "mostrando a vista "
	index := strings.LastIndex(instructions, marker)
	if index < 0 || !strings.HasSuffix(instructions, ".") {
		return instructions
	}
	start := index + len(marker)
	return instructions[:start] + localizeVideoTerm(instructions[start:len(instructions)-1], videoViewNames) + "."
}

func localizeVideoTerm(value string, names map[string]string) string {
	key := strings.ToLower(strings.TrimSpace(value))
	key = strings.ReplaceAll(strings.ReplaceAll(key, "-", "_"), " ", "_")
	if translated, ok := names[key]; ok {
		return translated
	}
	if separator := strings.LastIndexByte(key, '_'); separator > 0 {
		if translated, ok := names[key[:separator]]; ok {
			if number, err := strconv.Atoi(key[separator+1:]); err == nil && number > 0 {
				return translated + " " + strconv.Itoa(number)
			}
		}
	}
	return value
}
