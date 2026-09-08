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

type Item struct {
	ID, RequirementKey, Description, CaptureSource  string
	State, LineageID, ReplacedBy, MediaAvailability string
	Historical                                      bool
}

type Mapper struct {
	Published bool
	Sensitive map[string]bool
}

// CustomerEvidenceMapper is the application boundary for customer-safe
// evidence. Its input is the immutable database snapshot, while its output is
// an allowlisted representation.
type CustomerEvidenceMapper interface {
	Map(context.Context, database.ReportSnapshot, Mode) ([]Item, error)
}

// ReportMapper adapts a stored snapshot to the safe mapper without exposing
// its JSON or HTML fields to callers.
type ReportMapper struct{ Sensitive map[string]bool }

func (m ReportMapper) Map(_ context.Context, snapshot database.ReportSnapshot, mode Mode) ([]Item, error) {
	var value core.Snapshot
	if err := json.Unmarshal(snapshot.CanonicalJSON, &value); err != nil {
		return nil, apperror.New(apperror.NotFound, "inspectionId", "report not found")
	}
	return (Mapper{Published: true, Sensitive: m.Sensitive}).Map(value, mode)
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
		return "", apperror.New(apperror.NotFound, "mediaId", "media not found")
	}
	key, allowed, sensitive, err := i.Authorized(ctx, mediaID)
	if err != nil {
		return "", err
	}
	if !allowed || sensitive || key == "" {
		return "", apperror.New(apperror.NotFound, "mediaId", "media not found")
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

func (m Mapper) Map(snapshot core.Snapshot, mode Mode) ([]Item, error) {
	if !m.Published {
		return nil, apperror.New(apperror.NotFound, "inspectionId", "report not found")
	}
	if mode != Simple && mode != Advanced {
		return nil, apperror.New(apperror.InvalidInput, "mode", "evidence mode is invalid")
	}
	items := make([]Item, 0, len(snapshot.Evidence))
	for _, e := range snapshot.Evidence {
		item := Item{ID: e.ID, RequirementKey: e.RequirementKey, Description: e.Description, CaptureSource: e.CaptureSource, State: "CURRENT", LineageID: e.ID, MediaAvailability: "AVAILABLE"}
		for _, flag := range e.Flags {
			if strings.EqualFold(flag, "SENSITIVE") || strings.EqualFold(flag, "SENSITIVE_BLOCKED") {
				item.MediaAvailability = "SENSITIVE_BLOCKED"
			}
		}
		if m.Sensitive != nil && m.Sensitive[e.ID] {
			item.MediaAvailability = "SENSITIVE_BLOCKED"
		}
		items = append(items, item)
	}
	return items, nil
}
