package catalog_notes

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

// ReasonNotSelected marks a note that was eligible but that the operator did
// not select for an explicit regeneration.
const ReasonNotSelected = "NOT_SELECTED"

// Errors of an explicit selection. The caller maps them to user-facing text.
var (
	// ErrSelectionOutOfScope reports a revision that belongs to no note of the room.
	ErrSelectionOutOfScope = errors.New("selected revision does not belong to the room")
	// ErrSelectionStale reports a revision that is no longer the note's current text.
	ErrSelectionStale = errors.New("selected revision is not the current text of its note")
	// ErrSelectionNotReady reports a note whose transcript or anchor is not final yet.
	ErrSelectionNotReady = errors.New("selected note is not ready")
	// ErrSelectionIneligible reports a note that cannot feed a catalog (discarded, no speech, invalid anchor, outside the room).
	ErrSelectionIneligible = errors.New("selected note is not eligible")
)

// SealSelected creates and seals the manifest of an explicit regeneration. It
// classifies the room's notes as of now, so late and corrected text can be
// included, and keeps as INCLUDED only the selected current revisions; every
// other eligible note is recorded as excluded with ReasonNotSelected. The new
// generation never touches an earlier manifest.
func SealSelected(ctx context.Context, tx *gorm.DB, room Room, selected []identity.ID, now time.Time) (*Manifest, error) {
	tx = tx.WithContext(ctx)
	now = now.UTC()
	if existing, err := load(tx, room, true); err != nil || existing != nil {
		if err != nil {
			return nil, err
		}
		return nil, errors.New("catalog note manifest of this generation already exists")
	}
	if err := ResolveAnchors(ctx, tx, room, now); err != nil {
		return nil, err
	}
	entries, err := classify(tx, room)
	if err != nil {
		return nil, err
	}
	byNote := make(map[identity.ID]int, len(entries))
	for i, entry := range entries {
		byNote[entry.NoteID] = i
	}
	chosen := make(map[identity.ID]bool, len(selected))
	for _, revisionID := range selected {
		var owner struct{ NoteID identity.ID }
		if err := tx.Raw("SELECT note_id FROM transcription.transcript_revisions WHERE tenant_id=? AND id=?", room.TenantID, revisionID).Scan(&owner).Error; err != nil {
			return nil, err
		}
		position, inRoom := byNote[owner.NoteID]
		if owner.NoteID == (identity.ID{}) || !inRoom {
			return nil, ErrSelectionOutOfScope
		}
		entry := entries[position]
		switch {
		case entry.State == StateMissing:
			return nil, ErrSelectionNotReady
		case entry.State != StateIncluded:
			return nil, ErrSelectionIneligible
		case entry.RevisionID == nil || *entry.RevisionID != revisionID:
			return nil, ErrSelectionStale
		}
		chosen[entry.NoteID] = true
	}
	for i, entry := range entries {
		if entry.State == StateIncluded && !chosen[entry.NoteID] {
			entries[i] = Entry{NoteID: entry.NoteID, State: StateExcluded, Reason: ReasonNotSelected}
		}
	}
	raw, err := json.Marshal(entries)
	if err != nil {
		return nil, err
	}
	manifest := Manifest{CatalogNoteManifest: database.CatalogNoteManifest{ID: identity.NewID(), TenantID: room.TenantID, InspectionID: room.InspectionID, RequirementKey: room.RequirementKey, CatalogGeneration: room.Generation, NoteRevisions: raw, DeadlineAt: now, MissingCount: countState(entries, StateMissing), Digest: digestOf(room, entries), SealedAt: &now, CreatedAt: now}, Entries: entries}
	if err := tx.Create(&manifest.CatalogNoteManifest).Error; err != nil {
		return nil, err
	}
	return &manifest, nil
}
