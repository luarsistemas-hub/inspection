package process_inventory_job

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"inspection/libs/identity"
)

// CatalogNote is one immutable text revision admitted to a single room catalog run.
type CatalogNote struct {
	NoteID           identity.ID `json:"noteId"`
	RevisionID       identity.ID `json:"revisionId"`
	EnvironmentID    identity.ID `json:"environmentId"`
	TargetKind       string      `json:"targetKind"`
	SourceMediaID    identity.ID `json:"sourceMediaId"`
	SourceGeneration int         `json:"sourceGeneration"`
	StartMS          *int64      `json:"startMs,omitempty"`
	EndMS            *int64      `json:"endMs,omitempty"`
	Text             string      `json:"text"`
}

// CatalogInput fixes all data that can affect one catalog generation.
type CatalogInput struct {
	ProfileDigest     string
	MediaGenerationID identity.ID
	EnvironmentID     identity.ID
	Notes             []CatalogNote
}

// ValidateCatalogInput rejects stale, malformed, cross-room and ambiguous citations.
func ValidateCatalogInput(in CatalogInput) error {
	if strings.TrimSpace(in.ProfileDigest) == "" || in.MediaGenerationID == (identity.ID{}) || in.EnvironmentID == (identity.ID{}) {
		return fmt.Errorf("catalog input profile, media generation, and environment are required")
	}
	seen := make(map[identity.ID]struct{}, len(in.Notes))
	for _, note := range in.Notes {
		if note.NoteID == (identity.ID{}) || note.RevisionID == (identity.ID{}) || note.EnvironmentID != in.EnvironmentID || note.SourceMediaID == (identity.ID{}) || strings.TrimSpace(note.Text) == "" {
			return fmt.Errorf("catalog note citation is incomplete or belongs to another environment")
		}
		if _, exists := seen[note.NoteID]; exists {
			return fmt.Errorf("catalog note appears more than once")
		}
		seen[note.NoteID] = struct{}{}
		switch note.TargetKind {
		case "PHOTO":
			if note.SourceGeneration < 1 || note.StartMS != nil || note.EndMS != nil {
				return fmt.Errorf("photo citation has invalid media lineage")
			}
		case "VIDEO_INTERVAL":
			if note.SourceGeneration < 1 || note.StartMS == nil || note.EndMS == nil || *note.StartMS < 0 || *note.EndMS <= *note.StartMS {
				return fmt.Errorf("video citation has invalid interval or media lineage")
			}
		default:
			return fmt.Errorf("catalog note target kind is invalid")
		}
	}
	return nil
}

// Digest returns a stable digest over the fixed input, independent of note ordering.
func (in CatalogInput) Digest() (string, error) {
	if err := ValidateCatalogInput(in); err != nil {
		return "", err
	}
	notes := append([]CatalogNote(nil), in.Notes...)
	sort.Slice(notes, func(i, j int) bool {
		if notes[i].NoteID != notes[j].NoteID {
			return notes[i].NoteID.String() < notes[j].NoteID.String()
		}
		return notes[i].RevisionID.String() < notes[j].RevisionID.String()
	})
	payload, err := json.Marshal(struct {
		ProfileDigest     string        `json:"profileDigest"`
		MediaGenerationID identity.ID   `json:"mediaGenerationId"`
		EnvironmentID     identity.ID   `json:"environmentId"`
		Notes             []CatalogNote `json:"notes"`
	}{in.ProfileDigest, in.MediaGenerationID, in.EnvironmentID, notes})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}
