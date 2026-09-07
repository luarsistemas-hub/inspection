package resolvers

import (
	"context"
	"encoding/json"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"
	graphql1 "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
)

func mapReport(row database.ReportSnapshot) *graphql1.Report {
	var canonical map[string]any
	_ = json.Unmarshal(row.CanonicalJSON, &canonical)
	html := string(row.HTML)
	var decodedHTML string
	if json.Unmarshal(row.HTML, &decodedHTML) == nil {
		html = decodedHTML
	}
	var projectID *string
	if row.ProjectID != nil {
		value := row.ProjectID.String()
		projectID = &value
	}
	return &graphql1.Report{ID: row.ID.String(), InspectionID: row.InspectionID.String(), ProjectID: projectID, Version: row.VersionNumber, Mode: row.Mode, Classification: row.Classification, JSONDigest: row.JSONDigest, HTMLDigest: row.HTMLDigest, CanonicalJSON: canonical, HTML: html, CreatedAt: row.CreatedAt.Format("2006-01-02T15:04:05.999999999Z07:00")}
}

func mapRetentionPolicy(row database.RetentionPolicy) *graphql1.RetentionPolicy {
	return &graphql1.RetentionPolicy{ID: row.ID.String(), EvidenceDays: row.EvidenceDays, OperationalDays: row.OperationalDays, SecurityDays: row.SecurityDays, Version: int(row.Version), CreatedAt: row.CreatedAt.Format("2006-01-02T15:04:05.999999999Z07:00"), UpdatedAt: row.UpdatedAt.Format("2006-01-02T15:04:05.999999999Z07:00")}
}

func mapTriage(row database.DashboardInspection) *graphql1.TriageInspection {
	var projectID, assetID *string
	if row.ProjectID != nil {
		value := row.ProjectID.String()
		projectID = &value
	}
	if row.AssetID != (identity.ID{}) {
		value := row.AssetID.String()
		assetID = &value
	}
	return &graphql1.TriageInspection{InspectionID: row.InspectionID.String(), ProjectID: projectID, AssetID: assetID, Classification: row.Classification, Status: row.Status, UpdatedAt: row.UpdatedAt.Format("2006-01-02T15:04:05.999999999Z07:00")}
}

func strptr(value string) *string { return &value }

// withTask06Tenant keeps direct report/projection/retention queries under the
// same forced-RLS boundary as the feature slices. SQLite resolver tests do not
// expose PostgreSQL role metadata, so they use the equivalent scoped handle.
func withTask06Tenant(ctx context.Context, db *gorm.DB, tenantID identity.ID, fn func(*gorm.DB) error) error {
	if db == nil {
		return gorm.ErrInvalidDB
	}
	if db.Dialector.Name() == "postgres" {
		return (tenanttx.Runner{DB: db}).Within(ctx, tenantID, fn)
	}
	return fn(db.WithContext(ctx).Where("tenant_id=?", tenantID))
}
