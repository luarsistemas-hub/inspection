package resolvers

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"inspection/libs/identity"
	reportcore "inspection/services/inspection/internal/features/reports/core"
	customerreport "inspection/services/inspection/internal/features/reports/customer/core"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	graphql1 "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
)

func mapReport(row database.ReportSnapshot) *graphql1.Report {
	var canonical map[string]any
	_ = json.Unmarshal(row.CanonicalJSON, &canonical)
	if canonical != nil {
		delete(canonical, "verificationCode")
	}
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
	if snapshot.VerificationCode != "" {
		html = strings.ReplaceAll(html, snapshot.VerificationCode, "")
	}
	snapshot = reportcore.LocalizeVideoPresentation(snapshot)
	context := &graphql1.ReportContext{Asset: &graphql1.ReportAssetContext{ID: snapshot.Context.Asset.ID, Name: snapshot.Context.Asset.Name, ExternalKey: snapshot.Context.Asset.ExternalKey, Address: snapshot.Context.Asset.Address}, Participant: &graphql1.ReportParticipantContext{ID: snapshot.Context.Participant.ID, Name: snapshot.Context.Participant.Name}, Template: &graphql1.ReportTemplateContext{ID: snapshot.Context.Template.ID, Name: snapshot.Context.Template.Name, Version: snapshot.Context.Template.Version}, Inspection: &graphql1.ReportInspectionContext{ProjectID: optional(snapshot.Context.Inspection.ProjectID), StageID: optional(snapshot.Context.Inspection.StageID), StageLabel: optional(snapshot.Context.Inspection.StageLabel), DueAt: optional(snapshot.Context.Inspection.DueAt), SubmittedAt: optional(snapshot.Context.Inspection.SubmittedAt), GeneratedAt: snapshot.Context.Inspection.GeneratedAt}}
	requirements := make([]*graphql1.ReportRequirement, 0, len(snapshot.Requirements))
	for _, value := range snapshot.Requirements {
		requirements = append(requirements, &graphql1.ReportRequirement{Key: value.Key, Section: value.Section, Label: value.Label, Instructions: optional(value.Instructions), Coverage: optional(value.Coverage), ImpossibilityReason: optional(value.ImpossibilityReason), NoRelevantChange: value.NoRelevantChange, AnalysisMode: value.AnalysisMode, AnalysisStatus: value.AnalysisStatus})
	}
	evidence := make([]*graphql1.ReportEvidence, 0, len(snapshot.Evidence))
	labels := make(map[string]string, len(snapshot.Requirements))
	for _, requirement := range snapshot.Requirements {
		labels[requirement.Key] = requirement.Label
	}
	for evidenceIndex, value := range snapshot.Evidence {
		var timestamp *int
		if value.TimestampMS != nil {
			v := int(*value.TimestampMS)
			timestamp = &v
		}
		var pts, frameID, preparationID, sourceID, planID *string
		if value.PTSMicros != nil {
			v := fmt.Sprintf("%d", *value.PTSMicros)
			pts = &v
		}
		if value.FrameID != "" {
			frameID = optional(value.FrameID)
		}
		if value.PreparationID != "" {
			preparationID = optional(value.PreparationID)
		}
		if value.SourceMediaID != "" {
			sourceID = optional(value.SourceMediaID)
		}
		if snapshot.VideoPlanID != "" && value.EvidenceKind == "VIDEO_FRAME" {
			planID = optional(snapshot.VideoPlanID)
		}
		evidence = append(evidence, &graphql1.ReportEvidence{ID: value.ID, RequirementKey: value.RequirementKey, Role: value.Role, Description: optional(value.Description), CaptureSource: optional(value.CaptureSource), CapturedAt: optional(value.CapturedAt), DisplayDigest: optional(value.DisplayDigest), Availability: reportAvailability(value.Availability), SourceAvailability: optional(value.SourceAvailability), EvidenceKind: optional(value.EvidenceKind), FrameID: frameID, TimestampMs: timestamp, PtsMicros: pts, PreparationID: preparationID, PlanID: planID, SourceMediaID: sourceID, Component: optional(labels[value.RequirementKey]), Label: optional(labels[value.RequirementKey]), Reference: optional(fmt.Sprintf("E%03d", evidenceIndex+1)), Flags: value.Flags})
	}
	findings := make([]*graphql1.ReportFinding, 0, len(snapshot.Findings))
	for _, value := range snapshot.Findings {
		findings = append(findings, &graphql1.ReportFinding{ID: optional(value.ID), Category: value.Category, Title: value.Title, Description: value.Description, Severity: value.Severity, Confidence: value.Confidence, Quality: value.Quality, RecommendedAction: value.RecommendedAction, EvidenceIds: value.EvidenceIDs})
	}
	timeline := make([]*graphql1.ReportTimelineEntry, 0, len(snapshot.Timeline))
	for _, value := range snapshot.Timeline {
		timeline = append(timeline, &graphql1.ReportTimelineEntry{StageID: value.StageID, Classification: optional(value.Classification), Status: value.Status, Position: value.Position})
	}
	reasonLabels := make([]string, 0, len(snapshot.ReasonCodes))
	for _, code := range snapshot.ReasonCodes {
		reasonLabels = append(reasonLabels, reportcore.ReasonLabel(code))
	}
	return &graphql1.Report{EvidenceNotice: reportcore.EvidenceNotice(snapshot), ScopeProfile: reportcore.ScopeProfileLabel(snapshot), ScopeStatements: append([]string{}, snapshot.ScopeStatements...), ReasonLabels: reasonLabels, ID: row.ID.String(), InspectionID: row.InspectionID.String(), ProjectID: projectID, Version: row.VersionNumber, Mode: row.Mode, Classification: row.Classification, ClassificationLabel: reportcore.ClassificationLabelForSnapshot(snapshot), Limited: reportcore.ClassificationLimitedForSnapshot(snapshot), JSONDigest: row.JSONDigest, HTMLDigest: row.HTMLDigest, CanonicalJSON: canonical, HTML: html, CreatedAt: row.CreatedAt.Format(time.RFC3339Nano), Advisory: snapshot.Advisory, Context: context, Requirements: requirements, Inventory: mapCustomerInventory(customerreport.MapInventory(snapshot)), Checklist: mapInternalChecklist(snapshot), Processing: mapReportProcessing(snapshot.Processing), Handover: mapInternalHandover(snapshot), Comparison: mapInternalComparison(snapshot.Comparison), Evidence: &graphql1.ReportEvidenceConnection{Nodes: evidence, PageInfo: pageInfo("", false)}, Findings: findings, FindingCounts: mapFindingCounts(snapshot.Findings), Timeline: timeline, PDFStatus: "PENDING"}
}

func mapInternalChecklist(snapshot reportcore.Snapshot) *graphql1.ReportChecklist {
	value := snapshot.Checklist
	if value == nil {
		return nil
	}
	expectedRooms, hasExpectedRooms := reportcore.ExpectedRoomCount(snapshot)
	var expectedRoomsValue *int
	if hasExpectedRooms {
		expectedRoomsValue = &expectedRooms
	}
	out := &graphql1.ReportChecklist{Coverage: &graphql1.ReportChecklistCoverage{Expected: value.Coverage.Expected, ExpectedRooms: expectedRoomsValue, FullyAssessed: value.Coverage.FullyAssessed, PartiallyAssessed: value.Coverage.PartiallyAssessed, NotObserved: value.Coverage.NotObserved, NotApplicable: value.Coverage.NotApplicable, Impossible: value.Coverage.Impossible, UnknownScope: value.Coverage.UnknownScope}, Rooms: []*graphql1.ReportChecklistRoom{}}
	for _, room := range value.Rooms {
		r := &graphql1.ReportChecklistRoom{Key: room.Key, TypeCode: room.TypeCode, Name: room.Name, Components: []*graphql1.ReportChecklistComponent{}}
		for _, component := range room.Components {
			r.Components = append(r.Components, &graphql1.ReportChecklistComponent{Key: component.Key, TypeCode: optional(component.TypeCode), Label: component.Label, Status: component.Status, Description: optional(component.Description), Condition: optional(component.Condition), ReviewStatus: optional(component.ReviewStatus), ReviewLabel: optional(reportcore.PresentationLabel(component.ReviewStatus)), AssessedScope: optional(component.AssessedScope), NotAssessed: component.NotAssessed, EvidenceIds: component.EvidenceIDs, Limitations: component.Limitations, Conflict: component.Conflict, OutsideScope: component.OutsideScope, AttributeSources: formatAttributeSources(snapshot, component)})
		}
		out.Rooms = append(out.Rooms, r)
	}
	return out
}

func formatAttributeSources(snapshot reportcore.Snapshot, component reportcore.ChecklistComponent) []string {
	out := make([]string, 0, len(component.AttributeSources))
	for _, source := range component.AttributeSources {
		out = append(out, reportcore.FormatAttributeSourceForSnapshot(snapshot, source))
	}
	return out
}

func mapReportProcessing(value *reportcore.ProcessingState) *graphql1.ReportProcessing {
	if value == nil {
		return nil
	}
	return &graphql1.ReportProcessing{Status: value.Status, Message: optional(value.Message)}
}

func mapFindingCounts(findings []reportcore.Finding) *graphql1.ReportFindingCounts {
	counts := &graphql1.ReportFindingCounts{Total: len(findings)}
	for _, finding := range findings {
		switch finding.Severity {
		case "CRITICAL":
			counts.Critical++
		case "HIGH":
			counts.High++
		case "MEDIUM":
			counts.Medium++
		case "LOW":
			counts.Low++
		default:
			counts.Informational++
		}
	}
	return counts
}

func mapCustomerReportProcessing(value *reportcore.ProcessingState) *graphql1.ReportProcessing {
	if value == nil {
		return nil
	}
	message := reportcore.CustomerProcessingMessage(value.Status)
	return &graphql1.ReportProcessing{Status: value.Status, Message: optional(message)}
}

func mapInternalHandover(snapshot reportcore.Snapshot) *graphql1.ReportHandover {
	value := snapshot.Handover
	if value == nil {
		return nil
	}
	out := &graphql1.ReportHandover{Entries: []*graphql1.ReportHandoverEntry{}}
	for _, entry := range value.Entries {
		var payload map[string]any
		_ = json.Unmarshal(entry.Payload, &payload)
		if payload == nil {
			payload = map[string]any{}
		}
		payload["presentation"] = reportcore.FormatHandoverForSnapshot(snapshot, entry, true)
		out.Entries = append(out.Entries, &graphql1.ReportHandoverEntry{Key: entry.Key, Kind: entry.Kind, Status: entry.Status, Payload: payload, EvidenceIds: entry.EvidenceIDs, Declarant: optional(entry.Declarant)})
	}
	return out
}

func mapCustomerChecklist(value *customerreport.ChecklistView, snapshot reportcore.Snapshot) *graphql1.CustomerChecklist {
	if value == nil {
		return nil
	}
	expectedRooms, hasExpectedRooms := reportcore.ExpectedRoomCount(snapshot)
	var expectedRoomsValue *int
	if hasExpectedRooms {
		expectedRoomsValue = &expectedRooms
	}
	out := &graphql1.CustomerChecklist{Coverage: &graphql1.ReportChecklistCoverage{Expected: value.Coverage.Expected, ExpectedRooms: expectedRoomsValue, FullyAssessed: value.Coverage.FullyAssessed, PartiallyAssessed: value.Coverage.PartiallyAssessed, NotObserved: value.Coverage.NotObserved, NotApplicable: value.Coverage.NotApplicable, Impossible: value.Coverage.Impossible, UnknownScope: value.Coverage.UnknownScope}, Rooms: []*graphql1.CustomerChecklistRoom{}}
	for _, room := range value.Rooms {
		r := &graphql1.CustomerChecklistRoom{Name: room.Name, Components: []*graphql1.CustomerChecklistComponent{}}
		for _, component := range room.Components {
			r.Components = append(r.Components, &graphql1.CustomerChecklistComponent{Label: component.Label, DisplayGroup: component.DisplayGroup, Status: component.Status, Description: optional(component.Description), Condition: optional(component.Condition), ReviewLabel: optional(component.ReviewLabel), EvidenceIds: component.EvidenceIDs, Limitations: component.Limitations})
		}
		out.Rooms = append(out.Rooms, r)
	}
	return out
}
func mapCustomerHandover(value *customerreport.ChecklistView) *graphql1.CustomerHandover {
	if value == nil {
		return nil
	}
	out := &graphql1.CustomerHandover{Entries: []*graphql1.CustomerHandoverEntry{}}
	for _, entry := range value.Handover {
		out.Entries = append(out.Entries, &graphql1.CustomerHandoverEntry{Label: entry.Label, Kind: entry.Kind, Status: entry.Status, Type: optional(entry.Type), Value: optional(entry.Value), Unit: optional(entry.Unit), Date: optional(entry.Date), Declarant: optional(entry.Declarant), EvidenceIds: entry.EvidenceIDs})
	}
	return out
}

func mapCustomerInventory(section *customerreport.InventoryView) *graphql1.CustomerInventory {
	if section == nil {
		return nil
	}
	out := &graphql1.CustomerInventory{ReferenceVersionNumber: section.ReferenceVersionNumber, Purpose: section.Purpose, Problems: mapCustomerInventoryEntries(section.Problems), Informational: mapCustomerInventoryEntries(section.Informational)}
	if section.Coverage != nil {
		out.Coverage = &graphql1.CustomerInventoryCoverage{ExpectedItems: section.Coverage.ExpectedItems, AssessedItems: section.Coverage.AssessedItems, NotObserved: section.Coverage.NotObserved, NoBasis: section.Coverage.NoBasis, EnvironmentsNotCaptured: section.Coverage.EnvironmentsNotCaptured, EnvironmentsAddedDuringCapture: section.Coverage.EnvironmentsAddedDuringCapture}
	}
	return out
}

func customerInventoryFromSnapshot(raw []byte) *graphql1.CustomerInventory {
	var snapshot reportcore.Snapshot
	if json.Unmarshal(raw, &snapshot) != nil {
		return nil
	}
	return mapCustomerInventory(customerreport.MapInventory(snapshot))
}

func mapCustomerInventoryEntries(entries []customerreport.InventoryEntry) []*graphql1.CustomerInventoryEntry {
	out := make([]*graphql1.CustomerInventoryEntry, 0, len(entries))
	for _, entry := range entries {
		item := &graphql1.CustomerInventoryEntry{Environment: entry.Environment, Type: entry.Type, Name: entry.Name, Status: entry.Status, StatusLabel: entry.StatusLabel, ConditionBefore: entry.ConditionBefore, ConditionAfter: entry.ConditionAfter, Reason: entry.Reason, Pairs: make([]*graphql1.CustomerInventoryPair, 0, len(entry.Pairs))}
		for _, pair := range entry.Pairs {
			var timestamp *int
			if pair.TimestampMS != nil {
				v := int(*pair.TimestampMS)
				timestamp = &v
			}
			item.Pairs = append(item.Pairs, &graphql1.CustomerInventoryPair{OriginEvidenceID: pair.OriginEvidenceID, CurrentEvidenceID: pair.CurrentEvidenceID, OriginSourceAvailability: pair.OriginSourceAvailability, CurrentSourceAvailability: pair.CurrentSourceAvailability, TimestampMs: timestamp})
		}
		out = append(out, item)
	}
	return out
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
	canonical, err := json.Marshal(report.CanonicalJSON)
	if err != nil {
		return err
	}
	var snapshot reportcore.Snapshot
	if err := json.Unmarshal(canonical, &snapshot); err != nil {
		return err
	}
	videoEvidence := make(map[string]reportcore.Evidence, len(snapshot.Evidence))
	for _, item := range snapshot.Evidence {
		if item.EvidenceKind == "VIDEO_FRAME" {
			videoEvidence[item.ID] = item
		}
	}
	return withTask06Tenant(ctx, db, tenantID, func(tx *gorm.DB) error {
		for _, evidence := range report.Evidence.Nodes {
			if pinned, ok := videoEvidence[evidence.ID]; ok {
				if err := hydrateVideoFrame(ctx, tx, tenantID, report.ID, evidence, pinned); err != nil {
					return err
				}
				continue
			}
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

func hydrateVideoFrame(ctx context.Context, tx *gorm.DB, tenantID identity.ID, snapshotID string, view *graphql1.ReportEvidence, pinned reportcore.Evidence) error {
	frameID, err := identity.ParseID(pinned.FrameID)
	if err != nil {
		view.Availability = "MISSING"
		return nil
	}
	var frame database.VideoFrame
	if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", tenantID, frameID).Take(&frame).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			view.Availability = "MISSING"
			return nil
		}
		return err
	}
	if pinned.ID != pinned.FrameID || frame.SourceMediaID.String() != pinned.SourceMediaID || frame.PreparationID.String() != pinned.PreparationID || !strings.EqualFold(frame.SHA256, pinned.FrameDigest) || !strings.EqualFold(frame.SHA256, pinned.DisplayDigest) {
		view.Availability = "MISSING"
		return nil
	}
	view.Availability = "AVAILABLE"
	url := fmt.Sprintf("/media/video-evidence/%s?snapshotId=%s", frameID, snapshotID)
	view.URL = &url
	var source database.VideoSource
	if err := tx.WithContext(ctx).Where("tenant_id=? AND media_id=?", tenantID, frame.SourceMediaID).Take(&source).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			view.SourceAvailability = optional("MISSING")
			return nil
		}
		return err
	}
	availability := "AVAILABLE"
	if source.Status == "EXPIRED" || source.Status == "DELETING" || source.OriginalDeletedAt != nil || source.SourceExpiryAt != nil && !source.SourceExpiryAt.After(time.Now().UTC()) {
		availability = "EXPIRED"
	}
	view.SourceAvailability = &availability
	return nil
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
	action := "NONE"
	priority := "NORMAL"
	switch row.Kind {
	case "INSPECTION_INVITED", "INSPECTION_CREATED", "INSPECTION_OVERDUE", "INSPECTION_DEADLINE", "RECAPTURE_REQUESTED", "RECAPTURE_COMPLETED", "RECAPTURE_EXPIRED":
		action = "OPEN_INSPECTION"
	case "TRIAGE_ASSIGNED", "TRIAGE_REVIEW_REQUIRED":
		action = "OPEN_TRIAGE"
	case "SCHEDULE_CREATED", "SCHEDULE_CHANGED", "SCHEDULE_CANCELED":
		action = "OPEN_SCHEDULE"
	case "REPORT_READY", "REPORT_PUBLISHED":
		action = "OPEN_REPORT"
	case "RESPONSIBLE_EMAIL_DELIVERY":
		action = "OPEN_DELIVERY"
	}
	if row.Kind == "RESPONSIBLE_EMAIL_DELIVERY" || row.Kind == "INSPECTION_OVERDUE" {
		priority = "ATTENTION"
	}
	return &graphql1.RecipientNotification{ID: row.ID.String(), Kind: row.Kind, Title: row.Title, Body: row.Body, ResourceKind: row.ResourceKind, ResourceID: resourceID, CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339Nano), ReadAt: readAt, Action: action, Context: map[string]any{}, Priority: priority}
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

// publicationsBySnapshot loads the release ledger rows of many snapshots in a
// single query, so report lists never issue one lookup per item.
func (r *queryResolver) publicationsBySnapshot(ctx context.Context, tenantID identity.ID, snapshotIDs []identity.ID) (map[identity.ID]database.ReportPublication, error) {
	result := make(map[identity.ID]database.ReportPublication, len(snapshotIDs))
	if len(snapshotIDs) == 0 {
		return result, nil
	}
	var rows []database.ReportPublication
	if err := withTask06Tenant(ctx, r.DB, tenantID, func(tx *gorm.DB) error {
		return tx.Session(&gorm.Session{NewDB: true}).Where("tenant_id=? AND snapshot_id IN ?", tenantID, snapshotIDs).Find(&rows).Error
	}); err != nil {
		return nil, err
	}
	for _, row := range rows {
		result[row.SnapshotID] = row
	}
	return result, nil
}

// validCursorID reports whether the identifier carried by a pagination cursor
// is a UUID. A malformed value must be rejected as invalid input: PostgreSQL
// would otherwise fail the uuid comparison and surface an internal error.
func validCursorID(id string) bool {
	_, err := identity.ParseID(id)
	return err == nil
}

// inspectionScopeCondition limits rows to the inspections an actor may see.
// It returns an empty condition for the tenant administrator, an
// always-false condition for an actor without grants, and otherwise a
// condition on inspectionColumn built from the granted business units, assets,
// projects and inspections of the same tenant.
func inspectionScopeCondition(meta requestctx.Metadata, inspectionColumn string) (string, []any) {
	if hasRole(meta, auth.TenantAdmin) {
		return "", nil
	}
	var units, assets, projects, inspections []identity.ID
	for _, scope := range meta.Principal.Scopes {
		switch scope.Kind {
		case "BUSINESS_UNIT":
			units = append(units, scope.ID)
		case "ASSET":
			assets = append(assets, scope.ID)
		case "PROJECT":
			projects = append(projects, scope.ID)
		case "INSPECTION":
			inspections = append(inspections, scope.ID)
		}
	}
	var clauses []string
	args := []any{meta.TenantID}
	if len(units) > 0 {
		clauses = append(clauses, "s.business_unit_id IN ?")
		args = append(args, units)
	}
	if len(assets) > 0 {
		clauses = append(clauses, "s.asset_id IN ?")
		args = append(args, assets)
	}
	if len(projects) > 0 {
		clauses = append(clauses, "s.project_id IN ?")
		args = append(args, projects)
	}
	if len(inspections) > 0 {
		clauses = append(clauses, "s.id IN ?")
		args = append(args, inspections)
	}
	if len(clauses) == 0 {
		return "1=0", nil
	}
	return inspectionColumn + " IN (SELECT s.id FROM inspections.inspections s WHERE s.tenant_id = ? AND (" + strings.Join(clauses, " OR ") + "))", args
}

func reportHistoryLabels(snapshot reportcore.Snapshot) (string, string) {
	scope := "Escopo não disponível nesta versão"
	if snapshot.Checklist != nil {
		coverage := snapshot.Checklist.Coverage
		if coverage.UnknownScope {
			scope = "Escopo não definido"
		} else {
			noun := "componentes"
			if coverage.Expected == 1 {
				noun = "componente"
			}
			scope = fmt.Sprintf("%d %s no escopo", coverage.Expected, noun)
		}
	}
	statuses := []string{}
	if snapshot.Checklist != nil {
		for _, room := range snapshot.Checklist.Rooms {
			for _, component := range room.Components {
				if component.ReviewStatus == "" {
					continue
				}
				label := reportcore.PresentationLabel(component.ReviewStatus)
				if !slices.Contains(statuses, label) {
					statuses = append(statuses, label)
				}
			}
		}
	}
	review := strings.Join(statuses, " · ")
	if review == "" && snapshot.Processing != nil {
		review = reportcore.AnalysisStatusLabel(snapshot.Processing.Status)
	}
	if review == "" {
		review = "Revisão não informada"
	}
	return scope, review
}

func reportHistoryLabelsFromJSON(canonical []byte) [2]string {
	var facts struct {
		Coverage *struct {
			Expected     int  `json:"expected"`
			UnknownScope bool `json:"unknownScope"`
		} `json:"coverage"`
		ReviewStatuses []string `json:"reviewStatuses"`
		Processing     *struct {
			Status string `json:"status"`
		} `json:"processing"`
	}
	if err := json.Unmarshal(canonical, &facts); err != nil {
		return [2]string{"Escopo não disponível nesta versão", "Revisão não informada"}
	}
	scope := "Escopo não disponível nesta versão"
	if facts.Coverage != nil {
		if facts.Coverage.UnknownScope {
			scope = "Escopo não definido"
		} else {
			noun := "componentes"
			if facts.Coverage.Expected == 1 {
				noun = "componente"
			}
			scope = fmt.Sprintf("%d %s no escopo", facts.Coverage.Expected, noun)
		}
	}
	labels := make([]string, 0, len(facts.ReviewStatuses))
	for _, status := range facts.ReviewStatuses {
		label := reportcore.PresentationLabel(status)
		if !slices.Contains(labels, label) {
			labels = append(labels, label)
		}
	}
	review := strings.Join(labels, " · ")
	if review == "" && facts.Processing != nil {
		review = reportcore.AnalysisStatusLabel(facts.Processing.Status)
	}
	if review == "" {
		review = "Revisão não informada"
	}
	return [2]string{scope, review}
}
