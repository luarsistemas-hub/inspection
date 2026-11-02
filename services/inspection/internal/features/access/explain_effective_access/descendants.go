package explain_effective_access

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	graph "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
)

// MaxPageSize is the largest page returned by the paginated access queries.
const MaxPageSize = 100

const defaultPageSize = 25

// Entry is one effective resource reached through an explicit grant.
type Entry struct {
	Kind          string
	ResourceID    identity.ID
	Name          string
	Status        string
	Historical    bool
	InheritedFrom Scope
}

// Grantable reports whether the resource can still receive new work or a new
// grant. Archived resources, or resources under an archived ancestor, are kept
// for history only.
func (e Entry) Grantable() bool { return !e.Historical }

// Resource describes one explicit grant or preview target.
type Resource struct {
	Kind       string
	ResourceID identity.ID
	Name       string
	Status     string
	Historical bool
}

// Grantable reports whether the resource can still receive a new grant.
func (r Resource) Grantable() bool { return !r.Historical }

// Page is one cursor page of effective descendants.
type Page struct {
	Entries     []Entry
	EndCursor   string
	HasNextPage bool
}

// descendantRank orders kinds the same way across pages: assets, projects and
// inspections after the grant that reaches them.
func descendantRank(kind string) int {
	switch kind {
	case "ASSET":
		return 1
	case "PROJECT":
		return 2
	default:
		return 3
	}
}

func specificity(kind string) int {
	switch kind {
	case "PROJECT":
		return 3
	case "ASSET":
		return 2
	default:
		return 1
	}
}

// descendantsSQL expands the grant relation `g(kind, resource_id)` into every
// current descendant through canonical relationships. Resources are never
// materialized: a move or archive changes the next read immediately.
const descendantsSQL = `
WITH g AS (%s),
d AS (
  SELECT 'ASSET' AS kind, a.id AS id, a.name AS name, a.status AS status,
         (a.status='ARCHIVED' OR bu.status='ARCHIVED') AS historical,
         g.kind AS grant_kind, g.resource_id AS grant_id
    FROM g
    JOIN assets.assets a ON a.tenant_id=@tenant AND a.business_unit_id=g.resource_id
    JOIN tenancy.business_units bu ON bu.tenant_id=a.tenant_id AND bu.id=a.business_unit_id
   WHERE g.kind='BUSINESS_UNIT'
  UNION ALL
  SELECT 'PROJECT', p.id, p.name, p.status,
         (p.status='ARCHIVED' OR a.status='ARCHIVED' OR bu.status='ARCHIVED'),
         g.kind, g.resource_id
    FROM g
    JOIN projects.projects p ON p.tenant_id=@tenant AND p.business_unit_id=g.resource_id
    JOIN assets.assets a ON a.tenant_id=p.tenant_id AND a.id=p.asset_id
    JOIN tenancy.business_units bu ON bu.tenant_id=p.tenant_id AND bu.id=p.business_unit_id
   WHERE g.kind='BUSINESS_UNIT'
  UNION ALL
  SELECT 'PROJECT', p.id, p.name, p.status,
         (p.status='ARCHIVED' OR a.status='ARCHIVED' OR bu.status='ARCHIVED'),
         g.kind, g.resource_id
    FROM g
    JOIN projects.projects p ON p.tenant_id=@tenant AND p.asset_id=g.resource_id
    JOIN assets.assets a ON a.tenant_id=p.tenant_id AND a.id=p.asset_id
    JOIN tenancy.business_units bu ON bu.tenant_id=p.tenant_id AND bu.id=p.business_unit_id
   WHERE g.kind='ASSET'
  UNION ALL
  SELECT 'INSPECTION', i.id, a.name, i.status,
         (i.status='ARCHIVED' OR COALESCE(p.status='ARCHIVED', false) OR a.status='ARCHIVED' OR bu.status='ARCHIVED'),
         g.kind, g.resource_id
    FROM g
    JOIN inspections.inspections i ON i.tenant_id=@tenant AND (
         (g.kind='BUSINESS_UNIT' AND i.business_unit_id=g.resource_id)
      OR (g.kind='ASSET' AND i.asset_id=g.resource_id)
      OR (g.kind='PROJECT' AND i.project_id=g.resource_id))
    LEFT JOIN projects.projects p ON p.tenant_id=i.tenant_id AND p.id=i.project_id
    JOIN assets.assets a ON a.tenant_id=i.tenant_id AND a.id=i.asset_id
    JOIN tenancy.business_units bu ON bu.tenant_id=i.tenant_id AND bu.id=i.business_unit_id
   WHERE g.kind IN ('BUSINESS_UNIT','ASSET','PROJECT')
),
ranked AS (
  SELECT DISTINCT ON (kind, id) kind, id, name, status, historical, grant_kind, grant_id,
         CASE kind WHEN 'ASSET' THEN 1 WHEN 'PROJECT' THEN 2 ELSE 3 END AS rank
    FROM d
   ORDER BY kind, id,
         CASE grant_kind WHEN 'PROJECT' THEN 3 WHEN 'ASSET' THEN 2 ELSE 1 END DESC, grant_id
)
SELECT kind, id, name, status, historical, grant_kind, grant_id
  FROM ranked
 WHERE (rank, id) > (@after_rank, CAST(@after_id AS uuid))
 ORDER BY rank, id
 LIMIT @page_limit`

type descendantRow struct {
	Kind       string
	ID         identity.ID
	Name       string
	Status     string
	Historical bool
	GrantKind  string
	GrantID    identity.ID
}

func normalizePage(first int) (int, error) {
	if first < 0 {
		return 0, apperror.New(apperror.InvalidInput, "first", "O tamanho da página deve ser positivo.")
	}
	if first == 0 {
		return defaultPageSize, nil
	}
	if first > MaxPageSize {
		return MaxPageSize, nil
	}
	return first, nil
}

func decodeAfter(after string) (int, identity.ID, error) {
	if after == "" {
		return 0, identity.ID{}, nil
	}
	cursor, err := graph.DecodeCursor(after)
	if err != nil {
		return 0, identity.ID{}, apperror.New(apperror.PaginationInvalid, "after", "O cursor informado não é válido.")
	}
	rank, rankErr := strconv.Atoi(cursor.Time)
	id, idErr := identity.ParseID(cursor.ID)
	if rankErr != nil || idErr != nil || rank < 1 || rank > 3 {
		return 0, identity.ID{}, apperror.New(apperror.PaginationInvalid, "after", "O cursor informado não é válido.")
	}
	return rank, id, nil
}

// pageDescendants reads one stable page of descendants for the grant relation
// (a SQL fragment selecting kind and resource_id) inside the tenant
// transaction.
func pageDescendants(tx *gorm.DB, tenantID identity.ID, grantRelation string, args []any, first int, after string) (Page, error) {
	rank, id, err := decodeAfter(after)
	if err != nil {
		return Page{}, err
	}
	named := append([]any{
		sql.Named("tenant", tenantID),
		sql.Named("after_rank", rank),
		sql.Named("after_id", id.String()),
		sql.Named("page_limit", first+1),
	}, args...)
	var rows []descendantRow
	if err := tx.Raw(fmt.Sprintf(descendantsSQL, grantRelation), named...).Scan(&rows).Error; err != nil {
		return Page{}, err
	}
	page := Page{HasNextPage: len(rows) > first}
	if page.HasNextPage {
		rows = rows[:first]
	}
	page.Entries = make([]Entry, 0, len(rows))
	for _, row := range rows {
		page.Entries = append(page.Entries, Entry{Kind: row.Kind, ResourceID: row.ID, Name: row.Name, Status: row.Status, Historical: row.Historical, InheritedFrom: Scope{Kind: row.GrantKind, ID: row.GrantID}})
	}
	if len(page.Entries) > 0 {
		last := page.Entries[len(page.Entries)-1]
		page.EndCursor = graph.EncodeCursor(graph.Cursor{Time: strconv.Itoa(descendantRank(last.Kind)), ID: last.ResourceID.String()})
	}
	return page, nil
}

// resourceSQL resolves name, status and archive state of one grant target.
const resourceSQL = `
SELECT CASE @kind
         WHEN 'BUSINESS_UNIT' THEN (SELECT name FROM tenancy.business_units WHERE tenant_id=@tenant AND id=@id)
         WHEN 'ASSET' THEN (SELECT name FROM assets.assets WHERE tenant_id=@tenant AND id=@id)
         WHEN 'PROJECT' THEN (SELECT name FROM projects.projects WHERE tenant_id=@tenant AND id=@id)
         ELSE (SELECT a.name FROM inspections.inspections i JOIN assets.assets a ON a.tenant_id=i.tenant_id AND a.id=i.asset_id WHERE i.tenant_id=@tenant AND i.id=@id)
       END AS name,
       CASE @kind
         WHEN 'BUSINESS_UNIT' THEN (SELECT status FROM tenancy.business_units WHERE tenant_id=@tenant AND id=@id)
         WHEN 'ASSET' THEN (SELECT status FROM assets.assets WHERE tenant_id=@tenant AND id=@id)
         WHEN 'PROJECT' THEN (SELECT status FROM projects.projects WHERE tenant_id=@tenant AND id=@id)
         ELSE (SELECT status FROM inspections.inspections WHERE tenant_id=@tenant AND id=@id)
       END AS status`

// loadResource returns the grant target, labelling it historical when it, or
// any ancestor, is archived. A missing or foreign target reports found=false.
func loadResource(tx *gorm.DB, tenantID identity.ID, scope Scope) (Resource, bool, error) {
	var row struct {
		Name   sql.NullString
		Status sql.NullString
	}
	if err := tx.Raw(resourceSQL, sql.Named("tenant", tenantID), sql.Named("kind", scope.Kind), sql.Named("id", scope.ID)).Scan(&row).Error; err != nil {
		return Resource{}, false, err
	}
	if !row.Status.Valid {
		return Resource{}, false, nil
	}
	archived, err := auth.ArchivedResource(tx, tenantID, requestScope(scope))
	if err != nil {
		return Resource{}, false, err
	}
	return Resource{Kind: scope.Kind, ResourceID: scope.ID, Name: row.Name.String, Status: row.Status.String, Historical: archived || row.Status.String == "ARCHIVED"}, true, nil
}

func within(ctx context.Context, db *gorm.DB, tenantID identity.ID, fn func(*gorm.DB) error) error {
	return (tenanttx.Runner{DB: db}).Within(ctx, tenantID, fn)
}
