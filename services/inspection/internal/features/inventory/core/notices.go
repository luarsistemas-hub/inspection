package core

import (
	"encoding/json"
	"sort"

	"inspection/libs/identity"
	catalognotes "inspection/services/inspection/internal/features/analysis/catalog_notes"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

// Draft warning codes about voice notes and regeneration. The presentation
// layer turns them into text.
const (
	// WarningRegenerationRunning means an explicit regeneration is still scheduled or running.
	WarningRegenerationRunning = "REGENERATION_RUNNING"
	// WarningRegenerationFailed means the last explicit regeneration of a room failed; its previous suggestions remain.
	WarningRegenerationFailed = "REGENERATION_FAILED"
	// WarningVoiceNotIncorporated means a completed, eligible note (or its current text) is not part of its room's latest catalog generation.
	WarningVoiceNotIncorporated = "VOICE_NOT_INCORPORATED"
)

// voiceWarnings reports the state of regenerations and of notes that the
// suggestions do not reflect yet. It reads only the draft's own inspection.
func voiceWarnings(tx *gorm.DB, tenant identity.ID, draft database.OriginVersion) ([]string, error) {
	if draft.SourceInspectionID == nil {
		return nil, nil
	}
	var out []string
	var jobs []database.InventoryJob
	if err := tx.Select("requirement_key,generation,status").Where("tenant_id=? AND origin_version_id=? AND kind='CATALOG'", tenant, draft.ID).Order("requirement_key,generation").Find(&jobs).Error; err != nil {
		return nil, err
	}
	latest := map[string]database.InventoryJob{}
	for _, job := range jobs {
		latest[job.RequirementKey] = job
	}
	running, failed := false, false
	for _, job := range latest {
		if job.Generation < 2 {
			continue
		}
		switch job.Status {
		case "PENDING", "RUNNING":
			running = true
		case "FAILED":
			failed = true
		}
	}
	if running {
		out = append(out, WarningRegenerationRunning)
	}
	if failed {
		out = append(out, WarningRegenerationFailed)
	}
	notIncorporated, err := hasUnincorporatedNotes(tx, tenant, *draft.SourceInspectionID)
	if err != nil {
		return nil, err
	}
	if notIncorporated {
		out = append(out, WarningVoiceNotIncorporated)
	}
	sort.Strings(out)
	return out, nil
}

// hasUnincorporatedNotes reports whether a completed, valid-anchor note of the
// inspection is missing, or has newer text, in the latest manifest of its room.
func hasUnincorporatedNotes(tx *gorm.DB, tenant, inspection identity.ID) (bool, error) {
	var notes []struct {
		ID                identity.ID
		RequirementKey    string
		CurrentRevisionID identity.ID
	}
	if err := tx.Raw(`SELECT id,requirement_key,current_revision_id FROM transcription.audio_notes WHERE tenant_id=? AND inspection_id=? AND status='COMPLETED' AND anchor_status='VALID' AND no_speech=false AND current_revision_id IS NOT NULL`, tenant, inspection).Scan(&notes).Error; err != nil || len(notes) == 0 {
		return false, err
	}
	var manifests []database.CatalogNoteManifest
	if err := tx.Where("tenant_id=? AND inspection_id=?", tenant, inspection).Order("requirement_key,catalog_generation").Find(&manifests).Error; err != nil {
		return false, err
	}
	latest := map[string]database.CatalogNoteManifest{}
	for _, manifest := range manifests {
		latest[manifest.RequirementKey] = manifest
	}
	included := map[string]map[[2]identity.ID]bool{}
	for key, manifest := range latest {
		var entries []catalognotes.Entry
		if err := json.Unmarshal(manifest.NoteRevisions, &entries); err != nil {
			return false, err
		}
		included[key] = map[[2]identity.ID]bool{}
		for _, entry := range entries {
			if entry.State == catalognotes.StateIncluded && entry.RevisionID != nil {
				included[key][[2]identity.ID{entry.NoteID, *entry.RevisionID}] = true
			}
		}
	}
	for _, note := range notes {
		if !included[note.RequirementKey][[2]identity.ID{note.ID, note.CurrentRevisionID}] {
			return true, nil
		}
	}
	return false, nil
}
