package process_inventory_job

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"inspection/libs/identity"
	catalognotes "inspection/services/inspection/internal/features/analysis/catalog_notes"
	"inspection/services/inspection/internal/features/analysis/prompt"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"

	"gorm.io/gorm"
)

// MaxVoiceTextRunes bounds the total spoken text (in runes) of the notes sent
// with one room catalog request: 20 notes of up to 12000 characters would
// otherwise let a single room dominate the model context. Notes are admitted
// in manifest order; those that would exceed the ceiling are excluded with
// reason NOTE_TEXT_LIMIT_EXCEEDED and never truncated.
const MaxVoiceTextRunes = 24000

// reasonOutOfScope marks a note that does not belong to the room's own capture.
const reasonOutOfScope = "NOTE_OUT_OF_SCOPE"

// voiceInput is the fixed voice-note input of one room catalog run. Its notes
// are the transcript revisions the sealed manifest fixed; text that appears or
// changes later is never part of it.
type voiceInput struct {
	// Notes are the eligible revisions sent to the model.
	Notes []CatalogNote
	// Skipped lists manifest notes that were excluded, with the reason.
	Skipped []catalognotes.Entry
	// MissingCount is the number of notes still unresolved when the manifest sealed.
	MissingCount int
	// ManifestDigest identifies the sealed manifest.
	ManifestDigest string
	// Input is the fixed catalog input; InputDigest is its digest.
	Input       CatalogInput
	InputDigest string
}

// sent returns the exact (note, revision) pairs the request carries.
func (v *voiceInput) sent() map[string]CatalogNote {
	if v == nil {
		return map[string]CatalogNote{}
	}
	out := make(map[string]CatalogNote, len(v.Notes))
	for _, note := range v.Notes {
		out[note.NoteID.String()+":"+note.RevisionID.String()] = note
	}
	return out
}

// hasNotes reports whether the request is furnished.
func (v *voiceInput) hasNotes() bool { return v != nil && len(v.Notes) > 0 }

// noteInput is one manifest note joined with its fixed revision.
type noteInput struct {
	ID               identity.ID
	InspectionID     identity.ID
	ResponsibilityID identity.ID
	RequirementKey   string
	Status           string
	AnchorStatus     string
	TargetKind       string
	PhotoMediaID     *identity.ID
	SourceMediaID    *identity.ID
	SourceGeneration *int
	StartMS          *int64
	EndMS            *int64
	NoSpeech         bool
	RevisionID       identity.ID
	Text             string
}

// loadVoice seals the room's note manifest and loads the fixed revisions it
// includes. A room whose catalog was created without a manifest stays purely
// visual.
func (p *Processor) loadVoice(ctx context.Context, tx *gorm.DB, job database.InventoryJob, mediaIDs []identity.ID, in *jobInput) error {
	responsibility, err := submittedResponsibility(tx, job)
	if err != nil {
		return err
	}
	room := catalognotes.Room{TenantID: job.TenantID, InspectionID: job.InspectionID, ResponsibilityID: responsibility, RequirementKey: job.RequirementKey, MediaIDs: mediaIDs, Generation: job.Generation}
	manifest, err := catalognotes.Seal(ctx, tx, room, p.deps.Now())
	if err != nil || manifest == nil {
		return err
	}
	voice := &voiceInput{MissingCount: manifest.MissingCount, ManifestDigest: manifest.Digest}
	inRoom := make(map[identity.ID]bool, len(mediaIDs))
	for _, id := range mediaIDs {
		inRoom[id] = true
	}
	for _, entry := range manifest.Entries {
		if entry.State != catalognotes.StateIncluded {
			voice.Skipped = append(voice.Skipped, entry)
		}
	}
	runes := 0
	for _, entry := range manifest.Included() {
		note, reason, err := fixedNote(tx, job, entry, responsibility, inRoom, in.environment.ID)
		if err != nil {
			return err
		}
		if reason == "" {
			// The total spoken text of one request is bounded; the notes that
			// would exceed it are excluded, in manifest order, and said so.
			size := utf8.RuneCountInString(note.Text)
			if runes+size > MaxVoiceTextRunes {
				reason = catalognotes.ReasonTextLimitExceeded
			} else {
				runes += size
			}
		}
		if reason != "" {
			voice.Skipped = append(voice.Skipped, catalognotes.Entry{NoteID: entry.NoteID, RevisionID: entry.RevisionID, State: catalognotes.StateExcluded, Reason: reason})
			continue
		}
		voice.Notes = append(voice.Notes, note)
	}
	if len(voice.Notes) > 0 {
		version := in.snapshot.SchemaVersion
		if version == 0 {
			version = 1
		}
		profileDigest := prompt.CatalogVoiceDigestForVersion(in.snapshot.CanonicalDigest, version)
		if in.profile == "FURNISHED_PROPERTY" || in.profile == "" {
			profileDigest = prompt.FurnishedProfileDigestForVersion(in.snapshot.CanonicalDigest, version)
		}
		voice.Input = CatalogInput{ProfileDigest: profileDigest, MediaGenerationID: mediaGeneration(job.RequirementKey, in.current), EnvironmentID: in.environment.ID, Notes: voice.Notes}
		digest, err := voice.Input.Digest()
		if err != nil {
			// The fixed input is deterministic: retrying cannot repair it.
			return fmt.Errorf("%w: %v", messaging.ErrPermanent, err)
		}
		voice.InputDigest = digest
	}
	in.voice = voice
	return nil
}

// fixedNote loads one manifest note with exactly the revision the manifest
// fixed and re-validates it against the room. It never follows the note's
// current revision.
func fixedNote(tx *gorm.DB, job database.InventoryJob, entry catalognotes.Entry, responsibility identity.ID, inRoom map[identity.ID]bool, environmentID identity.ID) (CatalogNote, string, error) {
	var row noteInput
	if err := tx.Raw(`SELECT n.id,n.inspection_id,n.responsibility_id,n.requirement_key,n.status,n.anchor_status,n.target_kind,n.photo_media_id,n.source_media_id,n.source_generation,n.start_ms,n.end_ms,n.no_speech,r.id AS revision_id,r.text
FROM transcription.audio_notes n
JOIN transcription.transcript_revisions r ON r.tenant_id=n.tenant_id AND r.note_id=n.id AND r.id=?
WHERE n.tenant_id=? AND n.id=?`, entry.RevisionID, job.TenantID, entry.NoteID).Scan(&row).Error; err != nil {
		return CatalogNote{}, "", err
	}
	switch {
	case row.ID == (identity.ID{}) || strings.TrimSpace(row.Text) == "":
		return CatalogNote{}, catalognotes.ReasonRevisionUnavailable, nil
	case row.InspectionID != job.InspectionID || row.RequirementKey != job.RequirementKey || (responsibility != (identity.ID{}) && row.ResponsibilityID != responsibility):
		return CatalogNote{}, reasonOutOfScope, nil
	case row.Status != "COMPLETED":
		return CatalogNote{}, statusReason(row.Status), nil
	case row.NoSpeech:
		return CatalogNote{}, catalognotes.ReasonNoSpeech, nil
	case row.AnchorStatus != "VALID":
		return CatalogNote{}, catalognotes.ReasonAnchorInvalid, nil
	}
	note := CatalogNote{NoteID: row.ID, RevisionID: row.RevisionID, EnvironmentID: environmentID, TargetKind: row.TargetKind, Text: strings.TrimSpace(row.Text)}
	switch row.TargetKind {
	case "PHOTO":
		if row.PhotoMediaID == nil || !inRoom[*row.PhotoMediaID] {
			return CatalogNote{}, catalognotes.ReasonAnchorOutsideRoom, nil
		}
		note.SourceMediaID, note.SourceGeneration = *row.PhotoMediaID, 1
	case "VIDEO_INTERVAL":
		if row.SourceMediaID == nil || !inRoom[*row.SourceMediaID] {
			return CatalogNote{}, catalognotes.ReasonAnchorOutsideRoom, nil
		}
		if row.SourceGeneration == nil || *row.SourceGeneration < 1 || row.StartMS == nil || row.EndMS == nil {
			return CatalogNote{}, catalognotes.ReasonAnchorInvalid, nil
		}
		note.SourceMediaID, note.SourceGeneration, note.StartMS, note.EndMS = *row.SourceMediaID, *row.SourceGeneration, row.StartMS, row.EndMS
	default:
		return CatalogNote{}, catalognotes.ReasonAnchorInvalid, nil
	}
	return note, "", nil
}

// statusReason maps a note status that no longer allows its fixed revision to
// be sent (it changed after the manifest sealed) to the manifest reason code.
func statusReason(status string) string {
	switch status {
	case "CANCELED":
		return catalognotes.ReasonNoteDiscarded
	case "EXPIRED":
		return catalognotes.ReasonNoteExpired
	case "FAILED":
		return catalognotes.ReasonTranscriptionFailed
	case "UPLOADING", "QUEUED", "PROCESSING", "RETRY_WAIT":
		return catalognotes.ReasonTranscriptionPending
	default:
		return catalognotes.ReasonRevisionUnavailable
	}
}

// submittedResponsibility is the responsibility of the capture a job catalogs.
func submittedResponsibility(tx *gorm.DB, job database.InventoryJob) (identity.ID, error) {
	var draft database.CaptureDraft
	err := tx.Where("tenant_id=? AND responsibility_id IN (SELECT id FROM inspections.responsibilities WHERE tenant_id=? AND inspection_id=?)", job.TenantID, job.TenantID, job.InspectionID).Order("created_at DESC").Take(&draft).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return identity.ID{}, nil
	}
	return draft.ResponsibilityID, err
}

// mediaGeneration identifies the room's current media set, so replaced media
// yields a different generation than the one a note was fixed against.
func mediaGeneration(requirementKey string, inputs []evidenceInput) identity.ID {
	seen := map[string]bool{}
	var media []string
	for _, input := range inputs {
		id := input.MediaID.String()
		if !seen[id] {
			seen[id] = true
			media = append(media, id)
		}
	}
	sort.Strings(media)
	sum := sha256.Sum256([]byte(requirementKey + "|" + strings.Join(media, ",")))
	return identity.NewDeterministicID("catalog-media-generation", hex.EncodeToString(sum[:]))
}

// loadIntervalFrames returns retained frames that fall inside the interval of
// each video note, from the latest ready preparation of that exact media. The
// interval is approximate: these frames are candidates, never proof.
func loadIntervalFrames(tx *gorm.DB, job database.InventoryJob, notes []CatalogNote) ([]evidenceInput, error) {
	var out []evidenceInput
	preparation := map[identity.ID]*database.VideoPreparationJob{}
	for _, note := range notes {
		if note.TargetKind != "VIDEO_INTERVAL" || note.StartMS == nil || note.EndMS == nil {
			continue
		}
		prep, loaded := preparation[note.SourceMediaID]
		if !loaded {
			var row database.VideoPreparationJob
			err := tx.Where("tenant_id=? AND media_id=? AND status='READY'", job.TenantID, note.SourceMediaID).Order("generation DESC").Take(&row).Error
			if err == nil {
				prep = &row
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, err
			}
			preparation[note.SourceMediaID] = prep
		}
		if prep == nil {
			continue
		}
		var frames []database.VideoFrame
		if err := tx.Where("tenant_id=? AND preparation_id=? AND pts_us>=? AND pts_us<=?", job.TenantID, prep.ID, (*note.StartMS-intervalToleranceMS)*1000, (*note.EndMS+intervalToleranceMS)*1000).Order("pts_us ASC").Find(&frames).Error; err != nil {
			return nil, err
		}
		for _, frame := range spreadFrames(frames, maxFramesPerMedia) {
			out = append(out, frameInput(frame, note.SourceMediaID, job.RequirementKey))
		}
	}
	return out, nil
}

// lowQualityFrame is the preparation's quality mark of a frame that is unlikely
// to show its content (blur, darkness).
const lowQualityFrame = "LOW_QUALITY"

// spreadFrames picks up to limit frames spread evenly over the interval, in
// time order, instead of the first ones. Frames marked as low quality are
// skipped unless nothing else is left.
func spreadFrames(frames []database.VideoFrame, limit int) []database.VideoFrame {
	good := make([]database.VideoFrame, 0, len(frames))
	for _, frame := range frames {
		if frame.Quality != lowQualityFrame {
			good = append(good, frame)
		}
	}
	if len(good) == 0 {
		good = frames
	}
	sort.SliceStable(good, func(i, j int) bool { return good[i].PTSMicros < good[j].PTSMicros })
	if limit <= 0 || len(good) <= limit {
		return good
	}
	picked := make([]database.VideoFrame, 0, limit)
	for i := 0; i < limit; i++ {
		// Midpoints of limit equal slices: the first and the last frame are
		// not favored over the middle of the interval.
		picked = append(picked, good[(2*i+1)*len(good)/(2*limit)])
	}
	return picked
}

// intervalToleranceMS widens a note interval to absorb the approximation
// between the capture clock and the prepared timeline.
const intervalToleranceMS = 500

// selectCatalogImages chooses the images of a furnished request within the
// per-call budget. Frames inside a note's interval (and the photo a note is
// attached to) come first, spread across the notes; a quarter of the budget is
// kept for the room overview. Without notes it is the existing selection.
func selectCatalogImages(candidates []evidenceInput, notes []CatalogNote, max int) ([]evidenceInput, int) {
	if len(notes) == 0 {
		selected := representativeImages(candidates, max)
		return selected, positiveDifference(len(candidates), len(selected))
	}
	unique := make([]evidenceInput, 0, len(candidates))
	known := map[string]bool{}
	for _, candidate := range candidates {
		ref := evidenceRef(candidate.MediaID, candidate.FrameID)
		if !known[ref] {
			known[ref] = true
			unique = append(unique, candidate)
		}
	}
	groups := make([][]evidenceInput, len(notes))
	for i, note := range notes {
		for _, candidate := range unique {
			if candidate.MediaID != note.SourceMediaID {
				continue
			}
			if note.TargetKind == "PHOTO" || (candidate.TimestampMS != nil && note.StartMS != nil && note.EndMS != nil && *candidate.TimestampMS >= *note.StartMS-intervalToleranceMS && *candidate.TimestampMS <= *note.EndMS+intervalToleranceMS) {
				groups[i] = append(groups[i], candidate)
			}
		}
	}
	budget := max - max/4
	// Each clip keeps part of its frame cap for the room overview: priority
	// frames alone must not fill it, or a room with a single clip and long
	// intervals would end up without any overview frame.
	priorityCap := maxFramesPerMedia - overviewReserve(unique, max)
	chosen := map[string]bool{}
	perMedia := map[identity.ID]int{}
	var priority []evidenceInput
	for round := 0; len(priority) < budget; round++ {
		progressed := false
		for _, group := range groups {
			if round >= len(group) {
				continue
			}
			progressed = true
			candidate := group[round]
			ref := evidenceRef(candidate.MediaID, candidate.FrameID)
			if chosen[ref] || len(priority) >= budget || (candidate.Kind == "VIDEO_FRAME" && perMedia[candidate.MediaID] >= priorityCap) {
				continue
			}
			chosen[ref] = true
			perMedia[candidate.MediaID]++
			priority = append(priority, candidate)
		}
		if !progressed {
			break
		}
	}
	rest := make([]evidenceInput, 0, len(unique))
	for _, candidate := range unique {
		if !chosen[evidenceRef(candidate.MediaID, candidate.FrameID)] {
			rest = append(rest, candidate)
		}
	}
	overview := representativeImages(rest, max-len(priority))
	capped := overview[:0:0]
	for _, candidate := range overview {
		if candidate.Kind == "VIDEO_FRAME" {
			if perMedia[candidate.MediaID] >= maxFramesPerMedia {
				continue
			}
			perMedia[candidate.MediaID]++
		}
		capped = append(capped, candidate)
	}
	selected := append(capped, priority...)
	return selected, positiveDifference(len(unique), len(selected))
}

func positiveDifference(total, selected int) int {
	if total <= selected {
		return 0
	}
	return total - selected
}

// overviewReserve is the share of one clip's frame cap kept for the room
// overview: a quarter of the request budget split across the clips present,
// at least one frame and never the whole cap.
func overviewReserve(candidates []evidenceInput, max int) int {
	clips := map[identity.ID]bool{}
	for _, candidate := range candidates {
		if candidate.Kind == "VIDEO_FRAME" {
			clips[candidate.MediaID] = true
		}
	}
	reserve := 1
	if len(clips) > 0 && max/4/len(clips) > reserve {
		reserve = max / 4 / len(clips)
	}
	if reserve >= maxFramesPerMedia {
		reserve = maxFramesPerMedia - 1
	}
	return reserve
}

// VoiceCitation records that one note revision declares attributes of an item.
// It is stored in the item's suggestion so the provenance of a generated item
// survives until the item-attribute source records are written.
type VoiceCitation struct {
	NoteID     identity.ID `json:"noteId"`
	RevisionID identity.ID `json:"revisionId"`
	Attributes []string    `json:"attributes"`
}

// VoiceSource is the provenance contract of one voice-declared item attribute:
// the exact note revision that declared it. A writer of
// inventory.item_attribute_sources maps each value to a VOICE source row.
type VoiceSource struct {
	ItemID          identity.ID
	OriginVersionID identity.ID
	Attribute       string
	NoteID          identity.ID
	RevisionID      identity.ID
}

// ExtractVoiceSources lists the voice-declared attributes of a generated item.
// Items without voice citations (and every visual-only item) yield nothing.
func ExtractVoiceSources(item database.InventoryItem) []VoiceSource {
	if len(item.Suggested) == 0 {
		return nil
	}
	var suggested struct {
		Voice []VoiceCitation `json:"voice"`
	}
	if json.Unmarshal(item.Suggested, &suggested) != nil {
		return nil
	}
	var out []VoiceSource
	for _, citation := range suggested.Voice {
		for _, attribute := range citation.Attributes {
			out = append(out, VoiceSource{ItemID: item.ID, OriginVersionID: item.OriginVersionID, Attribute: attribute, NoteID: citation.NoteID, RevisionID: citation.RevisionID})
		}
	}
	return out
}

// acceptedVoice keeps only citations that name a note and revision exactly as
// sent in the request, with allowed attributes; duplicates are merged.
func acceptedVoice(citations []voiceCitation, sent map[string]CatalogNote) []VoiceCitation {
	allowed := make(map[string]bool, len(prompt.VoiceAttributes))
	for _, attribute := range prompt.VoiceAttributes {
		allowed[attribute] = true
	}
	var out []VoiceCitation
	index := map[string]int{}
	for _, citation := range citations {
		noteID, noteErr := identity.ParseID(citation.NoteID)
		revisionID, revisionErr := identity.ParseID(citation.RevisionID)
		if noteErr != nil || revisionErr != nil {
			continue
		}
		key := noteID.String() + ":" + revisionID.String()
		if _, ok := sent[key]; !ok {
			continue
		}
		position, exists := index[key]
		if !exists {
			position = len(out)
			index[key] = position
			out = append(out, VoiceCitation{NoteID: noteID, RevisionID: revisionID})
		}
		for _, attribute := range citation.Attributes {
			if allowed[attribute] && !containsString(out[position].Attributes, attribute) {
				out[position].Attributes = append(out[position].Attributes, attribute)
			}
		}
		if len(out[position].Attributes) == 0 {
			out = append(out[:position], out[position+1:]...)
			delete(index, key)
		}
	}
	return out
}

// declaresName reports whether a note declares the item itself.
func declaresName(citations []VoiceCitation) bool {
	for _, citation := range citations {
		if containsString(citation.Attributes, "name") {
			return true
		}
	}
	return false
}

// declaredOnlyByVoice removes everything speech did not declare from an item
// with no visual evidence, and states the missing visual coverage.
func declaredOnlyByVoice(item catalogItem, citations []VoiceCitation) catalogItem {
	declared := func(attribute string) bool {
		for _, citation := range citations {
			if containsString(citation.Attributes, attribute) {
				return true
			}
		}
		return false
	}
	if !declared("quantity") {
		item.Quantity = nil
	}
	if !declared("material") {
		item.Material = nil
	}
	if !declared("color") {
		item.Color = nil
	}
	if !declared("condition") {
		item.Condition = "NOT_ASSESSABLE"
	}
	item.Observation = "NOT_ASSESSABLE"
	if item.Limitation == nil || strings.TrimSpace(*item.Limitation) == "" {
		limitation := "Informado por voz; sem evidência visual."
		item.Limitation = &limitation
	}
	return item
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
