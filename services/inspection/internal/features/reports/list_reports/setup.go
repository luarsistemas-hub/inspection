// Package list_reports lists the latest generated report for each inspection.
package list_reports

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"inspection/libs/identity"

	"gorm.io/gorm"
)

const defaultPageSize = 25

var (
	ErrInvalidCursor         = errors.New("invalid report cursor")
	ErrInvalidClassification = errors.New("invalid report classification")
)

type Input struct {
	TenantID         identity.ID
	Scopes           []identity.ID
	AssetScopes      []identity.ID
	ProjectScopes    []identity.ID
	InspectionScopes []identity.ID
	TenantAdmin      bool
	First            int
	After            string
	Search           string
	Classification   string
}

type Item struct {
	ID, InspectionID                                           identity.ID
	AssetName, AssetAddress, AssetExternalKey, ParticipantName string
	GeneratedAt                                                time.Time
	Classification                                             string
	Version                                                    int
}

type Result struct {
	Items       []Item
	HasNextPage bool
	EndCursor   string
}

type Dependencies struct{ DB *gorm.DB }

// Setup validates its dependencies and returns the report listing operation.
func Setup(deps Dependencies) (func(context.Context, Input) (Result, error), error) {
	if deps.DB == nil {
		return nil, gorm.ErrInvalidDB
	}
	return func(ctx context.Context, in Input) (Result, error) {
		if identity.IsEmpty(&in.TenantID) {
			return Result{}, fmt.Errorf("list reports: missing tenant")
		}
		limit := in.First
		if limit <= 0 || limit > 100 {
			limit = defaultPageSize
		}
		cursorTime, cursorID, err := decodeCursor(in.After)
		if err != nil {
			return Result{}, err
		}
		assetName := "s.canonical_json #>> '{context,asset,name}'"
		assetAddress := "s.canonical_json #>> '{context,asset,address}'"
		assetKey := "s.canonical_json #>> '{context,asset,externalKey}'"
		participantName := "s.canonical_json #>> '{context,participant,name}'"
		searchOperator := "ILIKE"
		if deps.DB.Dialector.Name() == "sqlite" {
			assetName = "json_extract(s.canonical_json, '$.context.asset.name')"
			assetAddress = "json_extract(s.canonical_json, '$.context.asset.address')"
			assetKey = "json_extract(s.canonical_json, '$.context.asset.externalKey')"
			participantName = "json_extract(s.canonical_json, '$.context.participant.name')"
			searchOperator = "LIKE"
		}
		query := deps.DB.WithContext(ctx).Table("reports.report_snapshots AS s").
			Select("s.id, s.inspection_id, s.created_at AS generated_at, s.classification, s.version_number AS version, "+assetName+" AS asset_name, "+assetAddress+" AS asset_address, "+assetKey+" AS asset_external_key, "+participantName+" AS participant_name").
			Joins("JOIN inspections.inspections i ON i.tenant_id=s.tenant_id AND i.id=s.inspection_id").
			Joins("JOIN (SELECT tenant_id, inspection_id, MAX(version_number) AS version_number FROM reports.report_snapshots GROUP BY tenant_id, inspection_id) latest ON latest.tenant_id=s.tenant_id AND latest.inspection_id=s.inspection_id AND latest.version_number=s.version_number").
			Where("s.tenant_id=?", in.TenantID)
		if !in.TenantAdmin {
			var scopeParts []string
			var scopeArgs []any
			if len(in.Scopes) > 0 {
				scopeParts = append(scopeParts, "i.business_unit_id IN ?")
				scopeArgs = append(scopeArgs, in.Scopes)
			}
			if len(in.AssetScopes) > 0 {
				scopeParts = append(scopeParts, "i.asset_id IN ?")
				scopeArgs = append(scopeArgs, in.AssetScopes)
			}
			if len(in.ProjectScopes) > 0 {
				scopeParts = append(scopeParts, "i.project_id IN ?")
				scopeArgs = append(scopeArgs, in.ProjectScopes)
			}
			if len(in.InspectionScopes) > 0 {
				scopeParts = append(scopeParts, "i.id IN ?")
				scopeArgs = append(scopeArgs, in.InspectionScopes)
			}
			if len(scopeParts) == 0 {
				query = query.Where("1=0")
			} else {
				query = query.Where("("+strings.Join(scopeParts, " OR ")+")", scopeArgs...)
			}
		}
		if in.Classification != "" {
			switch in.Classification {
			case "NORMAL", "ATTENTION", "CRITICAL":
			default:
				return Result{}, ErrInvalidClassification
			}
			query = query.Where("s.classification=?", in.Classification)
		}
		if search := strings.TrimSpace(in.Search); search != "" {
			term := "%" + search + "%"
			query = query.Where("("+assetName+" "+searchOperator+" ? OR "+assetAddress+" "+searchOperator+" ? OR "+assetKey+" "+searchOperator+" ? OR "+participantName+" "+searchOperator+" ?)", term, term, term, term)
		}
		if !cursorTime.IsZero() {
			query = query.Where("(s.created_at < ?) OR (s.created_at = ? AND s.id < ?)", cursorTime, cursorTime, cursorID)
		}
		var rows []Item
		err = query.Order("s.created_at DESC, s.id DESC").Limit(limit + 1).Scan(&rows).Error
		if err != nil {
			return Result{}, err
		}
		result := Result{Items: rows}
		if len(rows) > limit {
			result.HasNextPage = true
			result.Items = rows[:limit]
		}
		if len(result.Items) > 0 {
			result.EndCursor = encodeCursor(result.Items[len(result.Items)-1].GeneratedAt, result.Items[len(result.Items)-1].ID)
		}
		return result, nil
	}, nil
}

func encodeCursor(at time.Time, id identity.ID) string {
	value := at.UTC().Format(time.RFC3339Nano) + "|" + id.String()
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

func decodeCursor(value string) (time.Time, identity.ID, error) {
	if value == "" {
		return time.Time{}, identity.ID{}, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return time.Time{}, identity.ID{}, ErrInvalidCursor
	}
	parts := strings.SplitN(string(decoded), "|", 2)
	if len(parts) != 2 {
		return time.Time{}, identity.ID{}, ErrInvalidCursor
	}
	at, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, identity.ID{}, ErrInvalidCursor
	}
	id, err := identity.ParseID(parts[1])
	if err != nil {
		return time.Time{}, identity.ID{}, ErrInvalidCursor
	}
	return at, id, nil
}
