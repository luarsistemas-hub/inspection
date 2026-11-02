// Package catalog_notes fixes which voice-note transcript revisions feed one
// room catalog generation and bounds how long the catalog waits for them.
//
// A manifest is created when a room's visual evidence is ready. It lists every
// submitted note of the room with an explicit state, fixes the transcript
// revision of each included note and carries the two-minute deadline after which
// the catalog proceeds without notes that are still being processed. A sealed
// manifest is immutable: late or corrected text never changes it.
package catalog_notes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	// VoiceWait is the longest the catalog waits, after visual evidence is
	// ready, for notes that are still being transcribed.
	VoiceWait = 2 * time.Minute
	// MaxNotes bounds the notes sent with one room catalog request.
	MaxNotes = 20
)

// States of one manifest entry.
const (
	// StateIncluded marks a note whose fixed revision feeds the catalog.
	StateIncluded = "INCLUDED"
	// StateMissing marks a note that was still being processed when the manifest sealed.
	StateMissing = "MISSING"
	// StateExcluded marks a note that cannot feed this catalog; Reason says why.
	StateExcluded = "EXCLUDED"
)

// Stable reason codes. User-facing text is produced by the presentation layer.
const (
	ReasonTranscriptionPending = "TRANSCRIPTION_PENDING"
	ReasonAnchorPending        = "ANCHOR_PENDING"
	ReasonNoteDiscarded        = "NOTE_DISCARDED"
	ReasonNoteExpired          = "NOTE_EXPIRED"
	ReasonTranscriptionFailed  = "TRANSCRIPTION_FAILED"
	ReasonNoSpeech             = "NO_SPEECH"
	ReasonRevisionUnavailable  = "REVISION_UNAVAILABLE"
	ReasonAnchorInvalid        = "ANCHOR_INVALID"
	ReasonAnchorOutsideRoom    = "ANCHOR_OUTSIDE_ROOM"
	ReasonNoteLimitExceeded    = "NOTE_LIMIT_EXCEEDED"
	// ReasonTextLimitExceeded marks a note left out because the total spoken
	// text of the request would exceed its ceiling.
	ReasonTextLimitExceeded = "NOTE_TEXT_LIMIT_EXCEEDED"
)

// Entry is one note of a manifest.
type Entry struct {
	NoteID     identity.ID  `json:"noteId"`
	RevisionID *identity.ID `json:"revisionId,omitempty"`
	State      string       `json:"state"`
	Reason     string       `json:"reason,omitempty"`
}

// Manifest is a catalog note manifest with its decoded entries.
type Manifest struct {
	database.CatalogNoteManifest
	Entries []Entry
}

// Sealed reports whether the manifest is fixed.
func (m Manifest) Sealed() bool { return m.SealedAt != nil }

// Included returns the entries whose fixed revision feeds the catalog.
func (m Manifest) Included() []Entry {
	var out []Entry
	for _, entry := range m.Entries {
		if entry.State == StateIncluded && entry.RevisionID != nil {
			out = append(out, entry)
		}
	}
	return out
}

// Room identifies one catalog generation of one captured environment.
type Room struct {
	TenantID     identity.ID
	InspectionID identity.ID
	// ResponsibilityID, when set, restricts the room to notes of that capture:
	// a note recorded in another capture is never presented.
	ResponsibilityID identity.ID
	RequirementKey   string
	// MediaIDs are the media currently captured for the room; a note anchored
	// anywhere else belongs to another room or to replaced media.
	MediaIDs   []identity.ID
	Generation int
}

// Outcome is the result of evaluating a room.
type Outcome struct {
	// Manifest is nil when the room has no submitted note: the catalog is purely visual.
	Manifest *Manifest
	// Ready reports that the catalog may start now.
	Ready bool
}

// Evaluate resolves pending anchors, classifies the room's submitted notes and
// creates or updates its manifest. The manifest seals as soon as no note is
// missing, or when final is true or the deadline has passed. A room without any
// submitted note leaves no row and is ready at once.
func Evaluate(ctx context.Context, tx *gorm.DB, room Room, now time.Time, final bool) (Outcome, error) {
	tx = tx.WithContext(ctx)
	now = now.UTC()
	existing, err := load(tx, room, true)
	if err != nil {
		return Outcome{}, err
	}
	if existing != nil && existing.Sealed() {
		return Outcome{Manifest: existing, Ready: true}, nil
	}
	if err := ResolveAnchors(ctx, tx, room, now); err != nil {
		return Outcome{}, err
	}
	if existing == nil {
		started, err := catalogStarted(tx, room)
		if err != nil {
			return Outcome{}, err
		}
		if started {
			// The generation's catalog job already left its first wait: text
			// that appears now (a note that was still uploading when the room
			// was submitted) never joins it. It only enters through an
			// explicit regeneration, so no manifest is created late.
			return Outcome{Ready: true}, nil
		}
	}
	entries, err := classify(tx, room)
	if err != nil {
		return Outcome{}, err
	}
	if existing == nil && len(entries) == 0 {
		return Outcome{Ready: true}, nil
	}
	missing := countState(entries, StateMissing)
	deadline := now.Add(VoiceWait)
	if existing != nil {
		deadline = existing.DeadlineAt
	}
	seal := missing == 0 || final || !now.Before(deadline)
	manifest := Manifest{CatalogNoteManifest: database.CatalogNoteManifest{ID: identity.NewID(), TenantID: room.TenantID, InspectionID: room.InspectionID, RequirementKey: room.RequirementKey, CatalogGeneration: room.Generation, DeadlineAt: deadline, CreatedAt: now}}
	if existing != nil {
		manifest = *existing
	}
	manifest.Entries = entries
	manifest.MissingCount = missing
	manifest.Digest = digestOf(room, entries)
	if seal {
		manifest.SealedAt = &now
	}
	raw, err := json.Marshal(entries)
	if err != nil {
		return Outcome{}, err
	}
	manifest.NoteRevisions = raw
	if existing == nil {
		created := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&manifest.CatalogNoteManifest)
		if created.Error != nil {
			return Outcome{}, created.Error
		}
		if created.RowsAffected == 0 {
			// A concurrent evaluation created the row first: use its state.
			winner, err := load(tx, room, true)
			if err != nil || winner == nil {
				return Outcome{}, errors.Join(err, errors.New("catalog note manifest vanished"))
			}
			return Outcome{Manifest: winner, Ready: winner.Sealed()}, nil
		}
		return Outcome{Manifest: &manifest, Ready: seal}, nil
	}
	updates := map[string]any{"note_revisions": json.RawMessage(raw), "missing_count": missing, "digest": manifest.Digest}
	if seal {
		updates["sealed_at"] = now
	}
	result := tx.Model(&database.CatalogNoteManifest{}).Where("tenant_id=? AND id=? AND sealed_at IS NULL", room.TenantID, manifest.ID).Updates(updates)
	if result.Error != nil {
		return Outcome{}, result.Error
	}
	if result.RowsAffected == 0 {
		winner, err := load(tx, room, false)
		if err != nil || winner == nil {
			return Outcome{}, errors.Join(err, errors.New("catalog note manifest vanished"))
		}
		return Outcome{Manifest: winner, Ready: winner.Sealed()}, nil
	}
	return Outcome{Manifest: &manifest, Ready: seal}, nil
}

// catalogStarted reports whether the room's CATALOG job of this generation is
// past its first wait (running, finished, or retried), so it can no longer
// take a manifest that did not exist when it was created.
func catalogStarted(tx *gorm.DB, room Room) (bool, error) {
	var count int64
	err := tx.Model(&database.InventoryJob{}).Where("tenant_id=? AND inspection_id=? AND kind='CATALOG' AND requirement_key=? AND generation=? AND NOT (status='PENDING' AND attempts=0)", room.TenantID, room.InspectionID, room.RequirementKey, room.Generation).Count(&count).Error
	return count > 0, err
}

// Seal returns the sealed manifest of a room, sealing an open one with the
// notes that are ready now. It returns nil when the room never had a manifest,
// so catalogs created without one stay purely visual.
func Seal(ctx context.Context, tx *gorm.DB, room Room, now time.Time) (*Manifest, error) {
	tx = tx.WithContext(ctx)
	current, err := load(tx, room, true)
	if err != nil || current == nil {
		return nil, err
	}
	if current.Sealed() {
		return current, nil
	}
	outcome, err := Evaluate(ctx, tx, room, now, true)
	if err != nil {
		return nil, err
	}
	return outcome.Manifest, nil
}

// Load returns the room's manifest, or nil when it has none.
func Load(ctx context.Context, tx *gorm.DB, room Room) (*Manifest, error) {
	return load(tx.WithContext(ctx), room, false)
}

func load(tx *gorm.DB, room Room, lock bool) (*Manifest, error) {
	var row database.CatalogNoteManifest
	query := tx
	if lock {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	err := query.Where("tenant_id=? AND inspection_id=? AND requirement_key=? AND catalog_generation=?", room.TenantID, room.InspectionID, room.RequirementKey, room.Generation).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	manifest := Manifest{CatalogNoteManifest: row}
	if err := json.Unmarshal(row.NoteRevisions, &manifest.Entries); err != nil {
		return nil, err
	}
	return &manifest, nil
}

// noteRow is the part of an audio note that decides eligibility.
type noteRow struct {
	ID                identity.ID
	Status            string
	AnchorStatus      string
	TargetKind        string
	PhotoMediaID      *identity.ID
	SourceMediaID     *identity.ID
	NoSpeech          bool
	CurrentRevisionID *identity.ID
	RevisionID        *identity.ID
}

// classify gives every submitted note of the room its state. A note that is
// still uploading has no durable receipt yet and is not part of any manifest.
func classify(tx *gorm.DB, room Room) ([]Entry, error) {
	var rows []noteRow
	query := `SELECT n.id,n.status,n.anchor_status,n.target_kind,n.photo_media_id,n.source_media_id,n.no_speech,n.current_revision_id,r.id AS revision_id
FROM transcription.audio_notes n
LEFT JOIN transcription.transcript_revisions r ON r.tenant_id=n.tenant_id AND r.note_id=n.id AND r.id=n.current_revision_id
WHERE n.tenant_id=? AND n.inspection_id=? AND n.requirement_key=? AND n.status<>'UPLOADING'`
	args := []any{room.TenantID, room.InspectionID, room.RequirementKey}
	if room.ResponsibilityID != (identity.ID{}) {
		query += " AND n.responsibility_id=?"
		args = append(args, room.ResponsibilityID)
	}
	if err := tx.Raw(query+" ORDER BY n.created_at ASC, n.id ASC", args...).Scan(&rows).Error; err != nil {
		return nil, err
	}
	inRoom := make(map[identity.ID]bool, len(room.MediaIDs))
	for _, id := range room.MediaIDs {
		inRoom[id] = true
	}
	entries := make([]Entry, 0, len(rows))
	included := 0
	for _, row := range rows {
		entry := Entry{NoteID: row.ID}
		switch row.Status {
		case "CANCELED":
			entry.State, entry.Reason = StateExcluded, ReasonNoteDiscarded
		case "EXPIRED":
			entry.State, entry.Reason = StateExcluded, ReasonNoteExpired
		case "FAILED":
			entry.State, entry.Reason = StateExcluded, ReasonTranscriptionFailed
		case "QUEUED", "PROCESSING", "RETRY_WAIT":
			entry.State, entry.Reason = StateMissing, ReasonTranscriptionPending
		case "COMPLETED":
			entry = classifyCompleted(row, inRoom)
		default:
			entry.State, entry.Reason = StateExcluded, ReasonRevisionUnavailable
		}
		if entry.State == StateIncluded {
			included++
			if included > MaxNotes {
				entry = Entry{NoteID: row.ID, State: StateExcluded, Reason: ReasonNoteLimitExceeded}
			}
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func classifyCompleted(row noteRow, inRoom map[identity.ID]bool) Entry {
	entry := Entry{NoteID: row.ID}
	switch {
	case row.NoSpeech:
		entry.State, entry.Reason = StateExcluded, ReasonNoSpeech
	case row.CurrentRevisionID == nil || row.RevisionID == nil:
		entry.State, entry.Reason = StateExcluded, ReasonRevisionUnavailable
	case row.AnchorStatus == "PENDING":
		entry.State, entry.Reason = StateMissing, ReasonAnchorPending
	case row.AnchorStatus != "VALID":
		entry.State, entry.Reason = StateExcluded, ReasonAnchorInvalid
	default:
		media := row.SourceMediaID
		if row.TargetKind == "PHOTO" {
			media = row.PhotoMediaID
		}
		if media == nil || !inRoom[*media] {
			entry.State, entry.Reason = StateExcluded, ReasonAnchorOutsideRoom
			return entry
		}
		entry.State = StateIncluded
		entry.RevisionID = row.RevisionID
	}
	return entry
}

func countState(entries []Entry, state string) int {
	count := 0
	for _, entry := range entries {
		if entry.State == state {
			count++
		}
	}
	return count
}

// digestOf is a stable digest over the room scope and its ordered entries.
func digestOf(room Room, entries []Entry) string {
	ordered := append([]Entry(nil), entries...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].NoteID.String() < ordered[j].NoteID.String() })
	payload, _ := json.Marshal(struct {
		RequirementKey string  `json:"requirementKey"`
		Generation     int     `json:"generation"`
		Entries        []Entry `json:"entries"`
	}{room.RequirementKey, room.Generation, ordered})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}
