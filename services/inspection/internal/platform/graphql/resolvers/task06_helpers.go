package resolvers

import (
	"context"
	"encoding/json"
	"time"

	"inspection/libs/identity"
	reportcore "inspection/services/inspection/internal/features/reports/core"
	"inspection/services/inspection/internal/platform/database"
	graphql1 "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/objectstore"
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
	var snapshot reportcore.Snapshot
	_ = json.Unmarshal(row.CanonicalJSON, &snapshot)
	context := &graphql1.ReportContext{Asset: &graphql1.ReportAssetContext{ID: snapshot.Context.Asset.ID, Name: snapshot.Context.Asset.Name, ExternalKey: snapshot.Context.Asset.ExternalKey, Address: snapshot.Context.Asset.Address}, Participant: &graphql1.ReportParticipantContext{ID: snapshot.Context.Participant.ID, Name: snapshot.Context.Participant.Name}, Template: &graphql1.ReportTemplateContext{ID: snapshot.Context.Template.ID, Name: snapshot.Context.Template.Name, Version: snapshot.Context.Template.Version}, Inspection: &graphql1.ReportInspectionContext{ProjectID: optional(snapshot.Context.Inspection.ProjectID), StageID: optional(snapshot.Context.Inspection.StageID), StageLabel: optional(snapshot.Context.Inspection.StageLabel), DueAt: optional(snapshot.Context.Inspection.DueAt), SubmittedAt: optional(snapshot.Context.Inspection.SubmittedAt), GeneratedAt: snapshot.Context.Inspection.GeneratedAt}}
	requirements := make([]*graphql1.ReportRequirement, 0, len(snapshot.Requirements))
	for _, value := range snapshot.Requirements {
		requirements = append(requirements, &graphql1.ReportRequirement{Key: value.Key, Section: value.Section, Label: value.Label, Instructions: optional(value.Instructions), Coverage: optional(value.Coverage), ImpossibilityReason: optional(value.ImpossibilityReason), NoRelevantChange: value.NoRelevantChange, AnalysisMode: value.AnalysisMode, AnalysisStatus: value.AnalysisStatus})
	}
	evidence := make([]*graphql1.ReportEvidence, 0, len(snapshot.Evidence))
	for _, value := range snapshot.Evidence {
		evidence = append(evidence, &graphql1.ReportEvidence{ID: value.ID, RequirementKey: value.RequirementKey, Role: value.Role, Description: optional(value.Description), CaptureSource: optional(value.CaptureSource), CapturedAt: optional(value.CapturedAt), DisplayDigest: optional(value.DisplayDigest), Availability: reportAvailability(value.Availability), Flags: value.Flags})
	}
	findings := make([]*graphql1.ReportFinding, 0, len(snapshot.Findings))
	for _, value := range snapshot.Findings {
		findings = append(findings, &graphql1.ReportFinding{ID: optional(value.ID), Category: value.Category, Title: value.Title, Description: value.Description, Severity: value.Severity, Confidence: value.Confidence, Quality: value.Quality, RecommendedAction: value.RecommendedAction, EvidenceIds: value.EvidenceIDs})
	}
	timeline := make([]*graphql1.ReportTimelineEntry, 0, len(snapshot.Timeline))
	for _, value := range snapshot.Timeline {
		timeline = append(timeline, &graphql1.ReportTimelineEntry{StageID: value.StageID, Classification: optional(value.Classification), Status: value.Status, Position: value.Position})
	}
	return &graphql1.Report{ID: row.ID.String(), InspectionID: row.InspectionID.String(), ProjectID: projectID, Version: row.VersionNumber, Mode: row.Mode, Classification: row.Classification, JSONDigest: row.JSONDigest, HTMLDigest: row.HTMLDigest, CanonicalJSON: canonical, HTML: html, CreatedAt: row.CreatedAt.Format(time.RFC3339Nano), Advisory: snapshot.Advisory, Context: context, Requirements: requirements, Evidence: evidence, Findings: findings, Timeline: timeline, PDFStatus: "PENDING"}
}

func optional(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
func reportAvailability(value string) string {
	if value == "" {
		return "AVAILABLE"
	}
	return value
}

func hydrateReportMedia(ctx context.Context, db *gorm.DB, store objectstore.Store, tenantID identity.ID, report *graphql1.Report) error {
	return withTask06Tenant(ctx, db, tenantID, func(tx *gorm.DB) error {
		for _, evidence := range report.Evidence {
			if evidence.Availability != "AVAILABLE" {
				continue
			}
			mediaID, err := identity.ParseID(evidence.ID)
			if err != nil {
				evidence.Availability = "MISSING"
				continue
			}
			var derivative database.MediaDerivative
			if err := tx.WithContext(ctx).Where("tenant_id=? AND media_id=? AND kind=?", tenantID, mediaID, "DISPLAY").First(&derivative).Error; err == gorm.ErrRecordNotFound {
				evidence.Availability = "MISSING"
				continue
			} else if err != nil {
				return err
			}
			if store.Client == nil {
				continue
			}
			url, err := store.PresignGet(ctx, derivative.ObjectKey, 5*time.Minute)
			if err != nil {
				return err
			}
			evidence.URL = &url
		}
		return nil
	})
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

func mapRecipientNotification(row database.RecipientNotification) *graphql1.RecipientNotification {
	var resourceID *string
	if row.ResourceID != nil {
		value := row.ResourceID.String()
		resourceID = &value
	}
	var readAt *string
	if row.ReadAt != nil {
		value := row.ReadAt.UTC().Format(time.RFC3339Nano)
		readAt = &value
	}
	return &graphql1.RecipientNotification{ID: row.ID.String(), Kind: row.Kind, Title: row.Title, Body: row.Body, ResourceKind: row.ResourceKind, ResourceID: resourceID, CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339Nano), ReadAt: readAt}
}

// mapNotificationChannelDelivery deliberately projects only operational state
// and provider correlation metadata. Destinations, templates, variables and
// provider error details are execution data and must not leave the API.
func mapNotificationChannelDelivery(row database.ChannelAttempt) *graphql1.NotificationChannelDelivery {
	var receiptID, lastAttemptAt *string
	if row.ReceiptID != "" {
		receiptID = strptr(row.ReceiptID)
	}
	if row.LastAttemptAt != nil {
		lastAttemptAt = strptr(row.LastAttemptAt.UTC().Format(time.RFC3339Nano))
	}
	return &graphql1.NotificationChannelDelivery{
		ID:            row.ID.String(),
		Channel:       row.Channel,
		Status:        row.Status,
		Provider:      row.Provider,
		ReceiptID:     receiptID,
		Attempts:      row.Attempts,
		LastAttemptAt: lastAttemptAt,
		CreatedAt:     row.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:     row.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func mapReportPublication(row database.ReportPublication) *graphql1.ReportPublication {
	var publishedAt, invalidatedAt *string
	if row.PublishedAt != nil {
		value := row.PublishedAt.UTC().Format(time.RFC3339Nano)
		publishedAt = &value
	}
	if row.InvalidatedAt != nil {
		value := row.InvalidatedAt.UTC().Format(time.RFC3339Nano)
		invalidatedAt = &value
	}
	return &graphql1.ReportPublication{ID: row.ID.String(), SnapshotID: row.SnapshotID.String(), InspectionID: row.InspectionID.String(), Status: row.Status, Version: int(row.Version), PublishedAt: publishedAt, InvalidatedAt: invalidatedAt}
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
