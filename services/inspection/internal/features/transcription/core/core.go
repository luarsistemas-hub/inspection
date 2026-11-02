package core

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

// Revision is an immutable text version attached to an audio note.
type Revision struct {
	Number    int
	Text      string
	Kind      string
	CreatedAt time.Time
}

// Note contains lifecycle state kept separate from audio retention state.
type Note struct {
	Status        string
	Version       int
	Highlighted   bool
	TextConfirmed bool
	AudioExpired  bool
	Revisions     []Revision
}

// Revise appends a new immutable participant correction using optimistic versioning.
func (n *Note) Revise(expectedVersion int, text string, now time.Time) error {
	if err := n.checkVersion(expectedVersion); err != nil {
		return err
	}
	if n.Status == "CANCELED" || n.Status == "EXPIRED" || n.Status == "UPLOADING" {
		return ErrInvalidState
	}
	clean, err := NormalizeTranscript(text)
	if err != nil {
		return err
	}
	n.Revisions = append(n.Revisions, Revision{Number: len(n.Revisions) + 1, Text: clean, Kind: "HUMAN_EDIT", CreatedAt: now.UTC()})
	n.TextConfirmed = false
	n.Version++
	return nil
}

// ConfirmText confirms the current transcript without making an inventory decision.
func (n *Note) ConfirmText(expectedVersion int) error {
	if err := n.checkVersion(expectedVersion); err != nil {
		return err
	}
	if n.Status != "COMPLETED" || len(n.Revisions) == 0 {
		return ErrInvalidState
	}
	n.TextConfirmed = true
	n.Version++
	return nil
}

// SetHighlighted updates the note highlight with optimistic versioning.
func (n *Note) SetHighlighted(expectedVersion int, highlighted bool) error {
	if err := n.checkVersion(expectedVersion); err != nil {
		return err
	}
	if n.Status == "CANCELED" || n.Status == "EXPIRED" {
		return ErrInvalidState
	}
	n.Highlighted = highlighted
	n.Version++
	return nil
}

// Discard removes an admitted note from active use while retaining its history.
func (n *Note) Discard(expectedVersion int) error {
	if err := n.checkVersion(expectedVersion); err != nil {
		return err
	}
	if n.Status == "CANCELED" || n.Status == "EXPIRED" {
		return ErrInvalidState
	}
	n.Status = "CANCELED"
	n.Version++
	return nil
}

// Retry requeues a failed note only while its original audio remains available.
func (n *Note) Retry(expectedVersion int) error {
	if err := n.checkVersion(expectedVersion); err != nil {
		return err
	}
	if n.Status != "FAILED" && n.Status != "RETRY_WAIT" {
		return ErrInvalidState
	}
	if n.AudioExpired {
		return ErrInvalidState
	}
	n.Status = "QUEUED"
	n.Version++
	return nil
}

func (n *Note) checkVersion(expectedVersion int) error {
	if expectedVersion != n.Version {
		return ErrConflict
	}
	return nil
}

var (
	ErrInvalidInput = errors.New("invalid input")
	ErrConflict     = errors.New("version conflict")
	ErrInvalidState = errors.New("invalid state")
	ErrLimitReached = errors.New("voice quota reached")
)

// Policy contains the four positive admission ceilings required to enable voice.
type Policy struct {
	Enabled                bool
	TenantNoteLimit        int
	TenantAudioLimitMS     int64
	InspectionNoteLimit    int
	InspectionAudioLimitMS int64
	Version                int
}

// Validate rejects incomplete or non-positive quotas, including disabled policies.
func (p Policy) Validate() error {
	if p.TenantNoteLimit <= 0 || p.TenantAudioLimitMS <= 0 || p.InspectionNoteLimit <= 0 || p.InspectionAudioLimitMS <= 0 {
		return ErrInvalidInput
	}
	return nil
}

// CanReserve checks both tenant and inspection limits before an upload is admitted.
func (p Policy) CanReserve(tenantNotes int, tenantAudioMS int64, inspectionNotes int, inspectionAudioMS int64, durationMS int64) error {
	if !p.Enabled {
		return ErrInvalidState
	}
	if err := p.Validate(); err != nil || durationMS <= 0 {
		return ErrInvalidInput
	}
	if tenantNotes+1 > p.TenantNoteLimit || tenantAudioMS+durationMS > p.TenantAudioLimitMS || inspectionNotes+1 > p.InspectionNoteLimit || inspectionAudioMS+durationMS > p.InspectionAudioLimitMS {
		return ErrLimitReached
	}
	return nil
}

// ValidateAnchor enforces mutually exclusive photo and video interval anchors.
func ValidateAnchor(kind string, photoID, attemptID string, startMS, endMS *int64, timeBasis string) error {
	switch kind {
	case "PHOTO":
		if photoID == "" || attemptID != "" || startMS != nil || endMS != nil || timeBasis != "" {
			return ErrInvalidInput
		}
	case "VIDEO_INTERVAL":
		if photoID != "" || attemptID == "" || startMS == nil || endMS == nil || *startMS < 0 || *endMS <= *startMS || timeBasis != "CAPTURE_CLOCK" {
			return ErrInvalidInput
		}
	default:
		return ErrInvalidInput
	}
	return nil
}

// ValidateAudio checks the admission limits and canonical digest representation.
func ValidateAudio(contentType, digest string, sizeBytes, durationMS int64) error {
	contentType = strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	if contentType != "audio/webm" && contentType != "audio/ogg" && contentType != "audio/mp4" && contentType != "audio/mpeg" {
		return ErrInvalidInput
	}
	if sizeBytes < 1 || sizeBytes > 5*1024*1024 || durationMS < 500 || durationMS > 60_000 {
		return ErrInvalidInput
	}
	decoded, err := hex.DecodeString(digest)
	if err != nil || len(decoded) != sha256.Size || digest != strings.ToLower(digest) {
		return ErrInvalidInput
	}
	return nil
}

// NormalizeTranscript trims unsafe whitespace while preserving the user's words.
func NormalizeTranscript(text string) (string, error) {
	text = strings.TrimSpace(text)
	if len([]rune(text)) == 0 || len([]rune(text)) > 12_000 {
		return "", ErrInvalidInput
	}
	return text, nil
}
