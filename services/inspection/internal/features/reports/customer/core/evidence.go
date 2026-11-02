// Package core maps internal evidence to the customer-safe allowlist.
package core

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/reports/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/objectstore"
)

type Mode string

const (
	Simple   Mode = "SIMPLE"
	Advanced Mode = "ADVANCED"
)

// Lineage states of a customer evidence item.
const (
	StateCurrent   = "CURRENT"
	StateReplaced  = "REPLACED"
	StateDiscarded = "DISCARDED"
)

// BlockedAvailability marks visual evidence whose pixels must never be served.
const BlockedAvailability = "SENSITIVE_BLOCKED"

// maxLineageSnapshots bounds how many earlier report versions ADVANCED mode reads.
const maxLineageSnapshots = 50

// Item is the allowlisted customer representation of one evidence item. It has
// no object key, URL, thumbnail or visual payload: media URLs are minted later,
// per request, only for items that remain visible.
type Item struct {
	ID, RequirementKey, Description, CaptureSource   string
	Role, CapturedAt, EvidenceKind, FrameID          string
	PreparationID, SourceMediaID, SourceAvailability string
	// ContentDigest is the immutable content hash recorded when the evidence
	// entered a report version; lineage items keep the hash of their own version.
	ContentDigest, FrameDigest                      string
	TimestampMS, PTSMicros                          *int64
	Flags                                           []string
	State, LineageID, ReplacedBy, MediaAvailability string
	Historical                                      bool
}

// Mapper projects an immutable snapshot through the customer allowlist. Prior
// holds earlier versions of the same inspection (newest first) and is used only
// by ADVANCED mode to reconstruct replaced and discarded evidence lineage.
type Mapper struct {
	Published bool
	Sensitive map[string]bool
	Prior     []core.Snapshot
}

// CustomerEvidenceMapper is the application boundary for customer-safe
// evidence. Its input is the immutable database snapshot, while its output is
// an allowlisted representation.
type CustomerEvidenceMapper interface {
	Map(context.Context, database.ReportSnapshot, Mode) ([]Item, error)
}

// ReportMapper adapts a stored snapshot to the safe mapper without exposing
// its JSON or HTML fields to callers. Prior carries earlier versions of the
// inspection for ADVANCED lineage.
type ReportMapper struct {
	Sensitive map[string]bool
	Prior     []database.ReportSnapshot
}

func (m ReportMapper) Map(_ context.Context, snapshot database.ReportSnapshot, mode Mode) ([]Item, error) {
	var value core.Snapshot
	if err := json.Unmarshal(snapshot.CanonicalJSON, &value); err != nil {
		return nil, apperror.New(apperror.NotFound, "inspectionId", "Relatório não encontrado.")
	}
	var prior []core.Snapshot
	if mode == Advanced {
		for i, row := range m.Prior {
			if i >= maxLineageSnapshots {
				break
			}
			var older core.Snapshot
			if json.Unmarshal(row.CanonicalJSON, &older) == nil {
				prior = append(prior, older)
			}
		}
	}
	return (Mapper{Published: true, Sensitive: m.Sensitive, Prior: prior}).Map(value, mode)
}

// MediaURLIssuer is the narrow authorization seam used by customer media.
// Callers must re-check current access at request time, not page-load time.
type MediaURLIssuer interface {
	IssueVisible(context.Context, identity.ID, identity.ID) (string, error)
}

type URLIssuer struct {
	Store      objectstore.Presigner
	Bucket     string
	TTL        time.Duration
	Authorized func(context.Context, string) (key string, allowed bool, sensitive bool, err error)
}

func (i URLIssuer) Issue(ctx context.Context, mediaID string) (string, error) {
	if i.Store == nil || i.Authorized == nil || strings.TrimSpace(mediaID) == "" {
		return "", apperror.New(apperror.NotFound, "mediaId", "Mídia não encontrada.")
	}
	key, allowed, sensitive, err := i.Authorized(ctx, mediaID)
	if err != nil {
		return "", err
	}
	if !allowed || sensitive || key == "" {
		return "", apperror.New(apperror.NotFound, "mediaId", "Mídia não encontrada.")
	}
	ttl := i.TTL
	if ttl <= 0 || ttl > 15*time.Minute {
		ttl = 5 * time.Minute
	}
	url, err := i.Store.PresignGet(ctx, i.Bucket, key, ttl)
	if err != nil {
		return "", apperror.Wrap(apperror.DependencyUnavailable, err)
	}
	return url, nil
}

func (i URLIssuer) IssueVisible(ctx context.Context, _ identity.ID, mediaID identity.ID) (string, error) {
	return i.Issue(ctx, mediaID.String())
}

// Map returns approved current evidence in SIMPLE mode. ADVANCED mode adds the
// evidence of earlier versions that no longer appears in the current one:
// REPLACED when a current item answers the same requirement, DISCARDED when it
// does not. Lineage items are historical and keep their original content hash.
func (m Mapper) Map(snapshot core.Snapshot, mode Mode) ([]Item, error) {
	if !m.Published {
		return nil, apperror.New(apperror.NotFound, "inspectionId", "Relatório não encontrado.")
	}
	if mode != Simple && mode != Advanced {
		return nil, apperror.New(apperror.InvalidInput, "mode", "Modo de evidências inválido.")
	}
	items := make([]Item, 0, len(snapshot.Evidence))
	current := make(map[string]struct{}, len(snapshot.Evidence))
	currentByRequirement := make(map[string]string, len(snapshot.Evidence))
	for _, e := range snapshot.Evidence {
		current[e.ID] = struct{}{}
		if e.Role == "CURRENT" {
			if _, exists := currentByRequirement[e.RequirementKey]; !exists {
				currentByRequirement[e.RequirementKey] = e.ID
			}
		}
		items = append(items, m.item(e, StateCurrent, e.ID, "", false))
	}
	if mode != Advanced {
		return items, nil
	}
	seen := make(map[string]struct{})
	for _, older := range m.Prior {
		for _, e := range older.Evidence {
			if e.Role != "CURRENT" {
				continue
			}
			if _, isCurrent := current[e.ID]; isCurrent {
				continue
			}
			if _, duplicate := seen[e.ID]; duplicate {
				continue
			}
			seen[e.ID] = struct{}{}
			if replacement, ok := currentByRequirement[e.RequirementKey]; ok {
				items = append(items, m.item(e, StateReplaced, replacement, replacement, true))
			} else {
				items = append(items, m.item(e, StateDiscarded, e.ID, "", true))
			}
		}
	}
	return items, nil
}

func (m Mapper) item(e core.Evidence, state, lineageID, replacedBy string, historical bool) Item {
	item := Item{ID: e.ID, RequirementKey: e.RequirementKey, Description: e.Description, CaptureSource: e.CaptureSource, Role: e.Role, CapturedAt: e.CapturedAt, EvidenceKind: e.EvidenceKind, FrameID: e.FrameID, PreparationID: e.PreparationID, SourceMediaID: e.SourceMediaID, SourceAvailability: e.SourceAvailability, ContentDigest: e.DisplayDigest, FrameDigest: e.FrameDigest, TimestampMS: e.TimestampMS, PTSMicros: e.PTSMicros, Flags: append([]string(nil), e.Flags...), State: state, LineageID: lineageID, ReplacedBy: replacedBy, MediaAvailability: availability(e.Availability), Historical: historical}
	if item.ContentDigest == "" {
		item.ContentDigest = e.FrameDigest
	}
	if EvidenceBlocked(e) {
		item.MediaAvailability = BlockedAvailability
	}
	if m.Sensitive != nil && m.Sensitive[e.ID] {
		item.MediaAvailability = BlockedAvailability
	}
	return item
}

// blockingFlags are the snapshot flags that keep an evidence visual from any
// customer, whatever availability the snapshot recorded.
var blockingFlags = []string{"SENSITIVE", "SENSITIVE_DETECTION", BlockedAvailability}

// EvidenceBlocked reports whether a snapshot evidence item must never be
// served to a customer: it was recorded as sensitive-blocked or carries a
// sensitive flag. Every customer media path decides through this function.
func EvidenceBlocked(e core.Evidence) bool {
	if strings.EqualFold(e.Availability, BlockedAvailability) {
		return true
	}
	for _, flag := range e.Flags {
		for _, blocking := range blockingFlags {
			if strings.EqualFold(flag, blocking) {
				return true
			}
		}
	}
	return false
}

func availability(value string) string {
	if value == "" {
		return "AVAILABLE"
	}
	return value
}
