// Package customer_timeline lists the customer-safe chronological release
// history of an asset or project from the audited publication ledger.
package customer_timeline

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

const defaultPageSize = 25

// Entry kinds exposed to customers.
const (
	KindPublished   = "REPORT_PUBLISHED"
	KindInvalidated = "REPORT_INVALIDATED"
)

var (
	// ErrInvalidCursor reports a malformed or foreign pagination cursor.
	ErrInvalidCursor = errors.New("invalid timeline cursor")
	// ErrNotFound hides both a missing target and one outside the caller's scope.
	ErrNotFound = errors.New("timeline target not found")
)

// Input is the scoped request. The hierarchy scopes are the caller's explicit
// grants; TenantAdmin sees the whole tenant.
type Input struct {
	TenantID           identity.ID
	AssetID, ProjectID *identity.ID
	TenantAdmin        bool
	BusinessUnitScopes []identity.ID
	AssetScopes        []identity.ID
	ProjectScopes      []identity.ID
	InspectionScopes   []identity.ID
	First              int
	After              string
}

// Entry exposes only status metadata: no report body, evidence or reasoning.
// There is one entry per ledger event: a release and its later withdrawal are
// two entries with their own identity and time. Status is the publication's
// current state; Historical is true once it is no longer the active release.
type Entry struct {
	ID         identity.ID
	Kind       string
	Status     string
	OccurredAt time.Time
	Historical bool
}

// Result is one stable page, ordered newest first.
type Result struct {
	Entries     []Entry
	HasNextPage bool
	EndCursor   string
}

// Dependencies must carry a database handle already scoped to the tenant.
type Dependencies struct{ DB *gorm.DB }

type row struct {
	ID         identity.ID `gorm:"column:id"`
	Kind       string      `gorm:"column:kind"`
	Status     string      `gorm:"column:status"`
	OccurredAt time.Time   `gorm:"column:occurred_at"`
}

// Setup validates its dependencies and returns the timeline operation.
func Setup(deps Dependencies) (func(context.Context, Input) (Result, error), error) {
	if deps.DB == nil {
		return nil, gorm.ErrInvalidDB
	}
	return func(ctx context.Context, in Input) (Result, error) {
		if in.TenantID == (identity.ID{}) {
			return Result{}, fmt.Errorf("customer timeline: missing tenant")
		}
		limit := in.First
		if limit <= 0 || limit > 100 {
			limit = defaultPageSize
		}
		cursorAt, cursorKind, cursorID, err := decodeCursor(in.After)
		if err != nil {
			return Result{}, err
		}
		cursorRank := kindRank(cursorKind)
		db := deps.DB.WithContext(ctx)
		if err := checkTarget(db, in); err != nil {
			return Result{}, err
		}
		// Each event is read from its own immutable column, so invalidating a
		// publication never moves or relabels its release entry.
		args := []any{
			eventQuery(db, in, KindPublished, 1, "COALESCE(rp.published_at, rp.created_at)", ""),
			eventQuery(db, in, KindInvalidated, 2, "COALESCE(rp.invalidated_at, rp.updated_at)", "rp.status='INVALIDATED'"),
		}
		statement := "SELECT id, kind, status, occurred_at FROM ((?) UNION ALL (?)) AS timeline_events"
		if !cursorAt.IsZero() {
			statement += " WHERE (occurred_at < ? OR (occurred_at = ? AND (kind_rank < ? OR (kind_rank = ? AND id < ?))))"
			args = append(args, cursorAt, cursorAt, cursorRank, cursorRank, cursorID)
		}
		statement += " ORDER BY occurred_at DESC, kind_rank DESC, id DESC LIMIT ?"
		args = append(args, limit+1)
		var rows []row
		if err := db.Raw(statement, args...).Scan(&rows).Error; err != nil {
			return Result{}, err
		}
		result := Result{}
		if len(rows) > limit {
			result.HasNextPage = true
			rows = rows[:limit]
		}
		for _, item := range rows {
			result.Entries = append(result.Entries, Entry{ID: entryID(item.ID, item.Kind), Kind: item.Kind, Status: item.Status, OccurredAt: item.OccurredAt.UTC(), Historical: item.Status != "PUBLISHED"})
		}
		if len(rows) > 0 {
			last := rows[len(rows)-1]
			result.EndCursor = encodeCursor(last.OccurredAt, last.Kind, last.ID)
		}
		return result, nil
	}, nil
}

// eventQuery selects one kind of ledger event under the caller's filters.
func eventQuery(db *gorm.DB, in Input, kind string, rank int, occurredAt, condition string) *gorm.DB {
	query := db.Session(&gorm.Session{NewDB: true}).Table("reports.report_publications AS rp").
		Select("rp.id AS id, '"+kind+"' AS kind, "+strconv.Itoa(rank)+" AS kind_rank, rp.status AS status, "+occurredAt+" AS occurred_at").
		Joins("JOIN inspections.inspections i ON i.tenant_id=rp.tenant_id AND i.id=rp.inspection_id").
		Where("rp.tenant_id=?", in.TenantID)
	if condition != "" {
		query = query.Where(condition)
	}
	if in.AssetID != nil {
		query = query.Where("i.asset_id=?", *in.AssetID)
	}
	if in.ProjectID != nil {
		query = query.Where("i.project_id=?", *in.ProjectID)
	}
	return scopeInspections(query, in)
}

// kindRank orders events that share an instant: a withdrawal follows its release.
func kindRank(kind string) int {
	switch kind {
	case KindPublished:
		return 1
	case KindInvalidated:
		return 2
	}
	return 3
}

// entryID gives each event a stable identity: the release keeps the
// publication's ID and its withdrawal a deterministic ID derived from it.
func entryID(publicationID identity.ID, kind string) identity.ID {
	if kind == KindInvalidated {
		return identity.NewDeterministicID("customer-timeline:"+kind, publicationID.String())
	}
	return publicationID
}

// scopeInspections restricts the joined inspections to the caller's grants.
func scopeInspections(query *gorm.DB, in Input) *gorm.DB {
	if in.TenantAdmin {
		return query
	}
	var parts []string
	var args []any
	if len(in.BusinessUnitScopes) > 0 {
		parts = append(parts, "i.business_unit_id IN ?")
		args = append(args, in.BusinessUnitScopes)
	}
	if len(in.AssetScopes) > 0 {
		parts = append(parts, "i.asset_id IN ?")
		args = append(args, in.AssetScopes)
	}
	if len(in.ProjectScopes) > 0 {
		parts = append(parts, "i.project_id IN ?")
		args = append(args, in.ProjectScopes)
	}
	if len(in.InspectionScopes) > 0 {
		parts = append(parts, "i.id IN ?")
		args = append(args, in.InspectionScopes)
	}
	if len(parts) == 0 {
		return query.Where("1=0")
	}
	return query.Where("("+strings.Join(parts, " OR ")+")", args...)
}

// checkTarget answers ErrNotFound for a missing asset/project and for one the
// caller cannot see, so an unshared sibling is indistinguishable from absent.
func checkTarget(db *gorm.DB, in Input) error {
	if in.AssetID != nil {
		var asset database.Asset
		if err := db.Select("id", "business_unit_id").Where("tenant_id=? AND id=?", in.TenantID, *in.AssetID).Take(&asset).Error; err != nil {
			return notFound(err)
		}
		if in.TenantAdmin || contains(in.BusinessUnitScopes, asset.BusinessUnitID) || contains(in.AssetScopes, asset.ID) {
			return nil
		}
		return inspectionVisible(db, in, "i.asset_id=?", asset.ID)
	}
	if in.ProjectID != nil {
		var project database.Project
		if err := db.Select("id", "business_unit_id", "asset_id").Where("tenant_id=? AND id=?", in.TenantID, *in.ProjectID).Take(&project).Error; err != nil {
			return notFound(err)
		}
		if in.TenantAdmin || contains(in.BusinessUnitScopes, project.BusinessUnitID) || contains(in.AssetScopes, project.AssetID) || contains(in.ProjectScopes, project.ID) {
			return nil
		}
		return inspectionVisible(db, in, "i.project_id=?", project.ID)
	}
	return nil
}

// inspectionVisible allows a target the caller reaches only through an
// inspection-level grant, which is the narrowest explicit scope.
func inspectionVisible(db *gorm.DB, in Input, condition string, id identity.ID) error {
	if len(in.InspectionScopes) == 0 {
		return ErrNotFound
	}
	var count int64
	if err := db.Table("inspections.inspections AS i").Where("i.tenant_id=? AND "+condition+" AND i.id IN ?", in.TenantID, id, in.InspectionScopes).Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return ErrNotFound
	}
	return nil
}

func notFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrNotFound
	}
	return err
}

func contains(ids []identity.ID, id identity.ID) bool {
	for _, candidate := range ids {
		if candidate == id {
			return true
		}
	}
	return false
}

func encodeCursor(at time.Time, kind string, id identity.ID) string {
	return base64.RawURLEncoding.EncodeToString([]byte("t2|" + at.UTC().Format(time.RFC3339Nano) + "|" + kind + "|" + id.String()))
}

func decodeCursor(value string) (time.Time, string, identity.ID, error) {
	if value == "" {
		return time.Time{}, "", identity.ID{}, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return time.Time{}, "", identity.ID{}, ErrInvalidCursor
	}
	parts := strings.Split(string(decoded), "|")
	if len(parts) != 4 || parts[0] != "t2" || (parts[2] != KindPublished && parts[2] != KindInvalidated) {
		return time.Time{}, "", identity.ID{}, ErrInvalidCursor
	}
	at, err := time.Parse(time.RFC3339Nano, parts[1])
	if err != nil {
		return time.Time{}, "", identity.ID{}, ErrInvalidCursor
	}
	id, err := identity.ParseID(parts[3])
	if err != nil {
		return time.Time{}, "", identity.ID{}, ErrInvalidCursor
	}
	return at, parts[2], id, nil
}
