// Package scope resolves and versions the effective checklist scope of an inspection.
package scope

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"inspection/services/inspection/internal/features/templates/catalog"
)

const (
	ProfilePropertyComponents = "PROPERTY_COMPONENTS"
	ProfileFurnishedProperty  = "FURNISHED_PROPERTY"
	ProfileSelectedItems      = "SELECTED_ITEMS"
	ProfileUndefined          = "UNDEFINED"
)

// Selection identifies a template component selected by a manager.
type Selection struct {
	RoomKey      string `json:"roomKey"`
	ComponentKey string `json:"componentKey"`
}

// Override changes the profile and optionally narrows selected-item scope.
type Override struct {
	Profile    string      `json:"profile"`
	Selections []Selection `json:"selections,omitempty"`
}

// Effective is the immutable, inspection-specific expected scope.
type Effective struct {
	Profile           string                   `json:"profile"`
	TemplateVersionID string                   `json:"templateVersionId,omitempty"`
	Rooms             []catalog.ExpectedRoom   `json:"rooms"`
	HandoverRecords   []catalog.HandoverRecord `json:"handoverRecords"`
	Digest            string                   `json:"digest"`
	Version           int                      `json:"version"`
}

// Resolve copies the template scope, applies a validated manager selection, and hashes the canonical document.
func Resolve(document catalog.TemplateDocument, templateVersionID string, override *Override) (Effective, error) {
	if document.SchemaVersion < catalog.DescriptiveSchemaVersion {
		if override != nil {
			return Effective{}, fmt.Errorf("este modelo não possui escopo de componentes configurável")
		}
		return resolveUndefined(templateVersionID)
	}
	profile := document.AssetProfile
	selected := map[string]bool(nil)
	if override != nil {
		if override.Profile != ProfilePropertyComponents && override.Profile != ProfileFurnishedProperty && override.Profile != ProfileSelectedItems {
			return Effective{}, fmt.Errorf("perfil de escopo inválido")
		}
		profile = override.Profile
		if profile == ProfileSelectedItems && len(override.Selections) == 0 {
			return Effective{}, fmt.Errorf("selecione ao menos um componente")
		}
		if len(override.Selections) > 0 || profile == ProfileSelectedItems {
			selected = make(map[string]bool, len(override.Selections))
			for _, item := range override.Selections {
				key := item.RoomKey + "." + item.ComponentKey
				if item.RoomKey == "" || item.ComponentKey == "" || selected[key] {
					return Effective{}, fmt.Errorf("seleção de componente inválida")
				}
				selected[key] = true
			}
		}
	}
	if profile == "" {
		profile = ProfilePropertyComponents
	}
	if profile != ProfilePropertyComponents && profile != ProfileFurnishedProperty && profile != ProfileSelectedItems {
		return Effective{}, fmt.Errorf("perfil de escopo inválido")
	}
	rooms := make([]catalog.ExpectedRoom, 0, len(document.ExpectedRooms))
	for _, room := range document.ExpectedRooms {
		copyRoom := room
		copyRoom.Components = make([]catalog.ExpectedComponent, 0, len(room.Components))
		for _, component := range room.Components {
			key := room.Key + "." + component.Key
			if selected != nil && !selected[key] {
				continue
			}
			copyComponent := component
			copyComponent.Views = append([]string(nil), component.Views...)
			copyRoom.Components = append(copyRoom.Components, copyComponent)
			delete(selected, key)
		}
		if selected == nil || len(copyRoom.Components) > 0 {
			rooms = append(rooms, copyRoom)
		}
	}
	if len(selected) > 0 {
		return Effective{}, fmt.Errorf("seleção contém componente desconhecido")
	}
	documentBytes, err := json.Marshal(struct {
		Profile           string                   `json:"profile"`
		TemplateVersionID string                   `json:"templateVersionId"`
		Rooms             []catalog.ExpectedRoom   `json:"rooms"`
		HandoverRecords   []catalog.HandoverRecord `json:"handoverRecords"`
	}{profile, templateVersionID, rooms, append([]catalog.HandoverRecord(nil), document.HandoverRecords...)})
	if err != nil {
		return Effective{}, err
	}
	sum := sha256.Sum256(documentBytes)
	return Effective{Profile: profile, TemplateVersionID: templateVersionID, Rooms: rooms, HandoverRecords: append([]catalog.HandoverRecord(nil), document.HandoverRecords...), Digest: hex.EncodeToString(sum[:]), Version: 1}, nil
}

func resolveUndefined(templateVersionID string) (Effective, error) {
	encoded, err := json.Marshal(struct {
		Profile           string `json:"profile"`
		TemplateVersionID string `json:"templateVersionId"`
		Rooms             []any  `json:"rooms"`
		HandoverRecords   []any  `json:"handoverRecords"`
	}{ProfileUndefined, templateVersionID, []any{}, []any{}})
	if err != nil {
		return Effective{}, err
	}
	sum := sha256.Sum256(encoded)
	return Effective{Profile: ProfileUndefined, TemplateVersionID: templateVersionID, Rooms: []catalog.ExpectedRoom{}, HandoverRecords: []catalog.HandoverRecord{}, Digest: hex.EncodeToString(sum[:]), Version: 1}, nil
}

// IsValidProfile reports whether profile is one of the supported asset profiles.
func IsValidProfile(profile string) bool {
	return profile == ProfilePropertyComponents || profile == ProfileFurnishedProperty || profile == ProfileSelectedItems
}
