package resolvers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"inspection/libs/identity"
	reportcore "inspection/services/inspection/internal/features/reports/core"
	customerreport "inspection/services/inspection/internal/features/reports/customer/core"
	"inspection/services/inspection/internal/features/reports/customer/screening"
	customertimeline "inspection/services/inspection/internal/features/reports/customer_timeline"
	publicationcore "inspection/services/inspection/internal/features/reports/publication/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	graphql1 "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/gorm"
)

// customerMediaTTL is the lifetime of a signed customer media URL.
const customerMediaTTL = 5 * time.Minute

// customerTimeline lists the customer-safe release history of an asset or
// project, ordered by when each event occurred.
func (r *queryResolver) customerTimeline(ctx context.Context, assetID, projectID *string, first *int, after *string) (*graphql1.CustomerTimelineConnection, error) {
	meta, ok := requestctx.FromContext(ctx)
	if !ok {
		return nil, unauthenticated()
	}
	if _, err := r.authorizeOperation(ctx, meta, auth.DashboardProduct, false, auth.Manager, auth.Employee, auth.Viewer, auth.CustomerViewer); err != nil {
		return nil, err
	}
	limit, err := pageLimit(first)
	if err != nil {
		return nil, err
	}
	input := customertimeline.Input{TenantID: meta.TenantID, TenantAdmin: hasRole(meta, auth.TenantAdmin), First: limit, After: stringValue(after)}
	if assetID != nil && *assetID != "" {
		id, err := identity.ParseID(*assetID)
		if err != nil {
			return nil, invalidID("assetId")
		}
		input.AssetID = &id
	}
	if projectID != nil && *projectID != "" {
		id, err := identity.ParseID(*projectID)
		if err != nil {
			return nil, invalidID("projectId")
		}
		input.ProjectID = &id
	}
	for _, scope := range meta.Principal.Scopes {
		switch scope.Kind {
		case "BUSINESS_UNIT":
			input.BusinessUnitScopes = append(input.BusinessUnitScopes, scope.ID)
		case "ASSET":
			input.AssetScopes = append(input.AssetScopes, scope.ID)
		case "PROJECT":
			input.ProjectScopes = append(input.ProjectScopes, scope.ID)
		case "INSPECTION":
			input.InspectionScopes = append(input.InspectionScopes, scope.ID)
		}
	}
	var result customertimeline.Result
	err = withTask06Tenant(ctx, r.DB, meta.TenantID, func(tx *gorm.DB) error {
		operation, setupErr := customertimeline.Setup(customertimeline.Dependencies{DB: tx.Session(&gorm.Session{NewDB: true})})
		if setupErr != nil {
			return setupErr
		}
		value, operationErr := operation(ctx, input)
		result = value
		return operationErr
	})
	switch {
	case errors.Is(err, customertimeline.ErrNotFound):
		return nil, apperror.New(apperror.NotFound, "timeline", "Linha do tempo não encontrada.")
	case errors.Is(err, customertimeline.ErrInvalidCursor):
		return nil, graphql1Error("after")
	case err != nil:
		return nil, err
	}
	nodes := make([]*graphql1.CustomerTimelineEntry, 0, len(result.Entries))
	for _, entry := range result.Entries {
		nodes = append(nodes, &graphql1.CustomerTimelineEntry{ID: entry.ID.String(), Kind: entry.Kind, Status: entry.Status, OccurredAt: entry.OccurredAt.Format(time.RFC3339Nano), Historical: entry.Historical})
	}
	return &graphql1.CustomerTimelineConnection{Nodes: nodes, PageInfo: pageInfo(result.EndCursor, result.HasNextPage)}, nil
}

// customerReport returns the current published report, or an explicitly
// requested historical version. A superseded version keeps its safe body and is
// labeled historical; an invalidated one exposes status metadata only.
func (r *queryResolver) customerReport(ctx context.Context, inspectionID string, version *int) (*graphql1.CustomerReport, error) {
	return r.customerReportWithComparison(ctx, inspectionID, version, nil)
}

// customerReportWithComparison is customerReport with an optional comparison
// outcome filter. The filter only selects the displayed comparison items; the
// counts keep the whole baseline denominator. An invalid filter is rejected
// before any report is read.
func (r *queryResolver) customerReportWithComparison(ctx context.Context, inspectionID string, version *int, comparisonOutcome *graphql1.ComparisonOutcome) (*graphql1.CustomerReport, error) {
	outcome, err := comparisonOutcomeFilter(comparisonOutcome)
	if err != nil {
		return nil, err
	}
	meta, ok := requestctx.FromContext(ctx)
	if !ok {
		return nil, unauthenticated()
	}
	if _, err := r.authorizeOperation(ctx, meta, auth.DashboardProduct, false, auth.Manager, auth.Employee, auth.Viewer, auth.CustomerViewer); err != nil {
		return nil, err
	}
	id, err := identity.ParseID(inspectionID)
	if err != nil {
		return nil, invalidID("inspectionId")
	}
	// Published visibility does not replace business-unit/asset scope checks;
	// customer views answer NOT_FOUND so out-of-scope reports stay undisclosed.
	if err := authorizeReportResource(ctx, r.DB, meta, id, "inspectionId", "Laudo não encontrado."); err != nil {
		return nil, err
	}
	row, publication, err := r.customerSnapshot(ctx, meta.TenantID, id, version)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if publication.Status == publicationcore.Invalidated {
		return &graphql1.CustomerReport{InspectionID: row.InspectionID.String(), SnapshotID: row.ID.String(), Version: row.VersionNumber, Classification: "", ClassificationLabel: "", Limited: false, Status: publication.Status, Historical: true, EvidenceNotice: "", ScopeProfile: "Escopo não informado", ScopeStatements: []string{}, ReasonLabels: []string{}, FindingCounts: mapFindingCounts(nil), Context: &graphql1.ReportContext{Asset: &graphql1.ReportAssetContext{}, Participant: &graphql1.ReportParticipantContext{}, Template: &graphql1.ReportTemplateContext{}, Inspection: &graphql1.ReportInspectionContext{}}, Requirements: []*graphql1.ReportRequirement{}}, nil
	}
	view := mapReport(row)
	var snapshot reportcore.Snapshot
	if err := json.Unmarshal(row.CanonicalJSON, &snapshot); err != nil {
		return nil, apperror.New(apperror.NotFound, "inspectionId", "Laudo não encontrado.")
	}
	// Customer notices and video wording must follow the same triaged,
	// localized presentation used by the customer HTML/PDF.
	screened := screening.RedactBlocked(snapshot)
	if err := withTask06Tenant(ctx, r.DB, meta.TenantID, func(tx *gorm.DB) error {
		return screening.ApplyDecisions(ctx, tx.Session(&gorm.Session{NewDB: true}), meta.TenantID, &screened)
	}); err != nil {
		return nil, err
	}
	screened = reportcore.LocalizeVideoPresentation(screened)
	descriptive := customerreport.ProjectChecklist(screened)
	// The paired images of a comparison follow the same customer triage as
	// the customer PDF: the snapshot's own blocks plus the screening
	// decisions in force now (screening package).
	comparison, err := customerreport.ProjectComparison(screened, outcome)
	if err != nil {
		return nil, invalidComparisonFilter()
	}
	return &graphql1.CustomerReport{InspectionID: row.InspectionID.String(), SnapshotID: row.ID.String(), Version: row.VersionNumber, Classification: row.Classification, ClassificationLabel: reportcore.ClassificationLabelForSnapshot(screened), Limited: reportcore.ClassificationLimitedForSnapshot(screened), Advisory: view.Advisory, Status: publication.Status, Historical: publication.Status != publicationcore.Published, EvidenceNotice: reportcore.EvidenceNotice(screened), ScopeProfile: reportcore.ScopeProfileLabel(screened), ScopeStatements: append([]string{}, screened.ScopeStatements...), ReasonLabels: customerReasonLabels(screened), FindingCounts: mapFindingCounts(screened.Findings), Context: view.Context, Requirements: view.Requirements, Inventory: customerInventoryFromSnapshot(row.CanonicalJSON), Checklist: mapCustomerChecklist(descriptive, screened), Processing: func() *graphql1.ReportProcessing {
		if snapshot.SchemaVersion >= 3 {
			return mapCustomerReportProcessing(snapshot.Processing)
		}
		return mapReportProcessing(snapshot.Processing)
	}(), Handover: mapCustomerHandover(descriptive), Comparison: mapCustomerComparison(comparison)}, nil
}

// customerSnapshot resolves the snapshot and ledger row a customer may see.
// Without a version it is the active publication; with a version it is that
// version's publication in any lifecycle state. A version never released is
// answered as not found.
func (r *queryResolver) customerSnapshot(ctx context.Context, tenantID, inspectionID identity.ID, version *int) (database.ReportSnapshot, database.ReportPublication, error) {
	var row database.ReportSnapshot
	var publication database.ReportPublication
	err := withTask06Tenant(ctx, r.DB, tenantID, func(tx *gorm.DB) error {
		base := tx.Session(&gorm.Session{NewDB: true})
		if version == nil {
			if err := base.Joins("JOIN reports.report_publications p ON p.snapshot_id=reports.report_snapshots.id AND p.tenant_id=reports.report_snapshots.tenant_id").
				Where("reports.report_snapshots.tenant_id=? AND reports.report_snapshots.inspection_id=? AND p.status=?", tenantID, inspectionID, publicationcore.Published).
				Order("reports.report_snapshots.version_number DESC").First(&row).Error; err != nil {
				return err
			}
		} else if err := base.Where("tenant_id=? AND inspection_id=? AND version_number=?", tenantID, inspectionID, *version).Take(&row).Error; err != nil {
			return err
		}
		return tx.Session(&gorm.Session{NewDB: true}).Where("tenant_id=? AND snapshot_id=?", tenantID, row.ID).Take(&publication).Error
	})
	return row, publication, err
}

// customerEvidence pages the allowlisted evidence of the active publication.
// SIMPLE returns approved current items; ADVANCED adds replaced and discarded
// lineage. Visual URLs are minted only for visible current items.
func (r *queryResolver) customerEvidence(ctx context.Context, inspectionID string, mode *graphql1.EvidenceMode, first *int, after *string) (*graphql1.CustomerEvidenceConnection, error) {
	meta, ok := requestctx.FromContext(ctx)
	if !ok {
		return nil, unauthenticated()
	}
	if _, err := r.authorizeOperation(ctx, meta, auth.DashboardProduct, false, auth.Manager, auth.Employee, auth.Viewer, auth.CustomerViewer); err != nil {
		return nil, err
	}
	id, err := identity.ParseID(inspectionID)
	if err != nil {
		return nil, invalidID("inspectionId")
	}
	// Published visibility does not replace business-unit/asset scope checks;
	// customer views answer NOT_FOUND so out-of-scope reports stay undisclosed.
	if err := authorizeReportResource(ctx, r.DB, meta, id, "inspectionId", "Laudo não encontrado."); err != nil {
		return nil, err
	}
	row, publication, err := r.customerSnapshot(ctx, meta.TenantID, id, nil)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &graphql1.CustomerEvidenceConnection{Nodes: []*graphql1.CustomerEvidenceItem{}, PageInfo: pageInfo("", false)}, nil
	}
	if err != nil {
		return nil, err
	}
	limit, err := pageLimit(first)
	if err != nil {
		return nil, err
	}
	evidenceMode := customerreport.Simple
	if mode != nil && *mode == graphql1.EvidenceModeAdvanced {
		evidenceMode = customerreport.Advanced
	}
	mapper := customerreport.ReportMapper{}
	if evidenceMode == customerreport.Advanced {
		if err := withTask06Tenant(ctx, r.DB, meta.TenantID, func(tx *gorm.DB) error {
			// Lineage is limited to versions that were once released to customers
			// (still active or superseded). Never-published and invalidated
			// snapshots are not customer-visible and must not leak through it.
			return tx.Session(&gorm.Session{NewDB: true}).Select("reports.report_snapshots.id", "reports.report_snapshots.version_number", "reports.report_snapshots.canonical_json").
				Joins("JOIN reports.report_publications p ON p.snapshot_id=reports.report_snapshots.id AND p.tenant_id=reports.report_snapshots.tenant_id AND p.status IN ?", []string{publicationcore.Published, publicationcore.Superseded}).
				Where("reports.report_snapshots.tenant_id=? AND reports.report_snapshots.inspection_id=? AND reports.report_snapshots.version_number<?", meta.TenantID, id, row.VersionNumber).
				Order("reports.report_snapshots.version_number DESC").Limit(50).Find(&mapper.Prior).Error
		}); err != nil {
			return nil, err
		}
	}
	items, err := mapper.Map(ctx, row, evidenceMode)
	if err != nil {
		return nil, err
	}
	var referenceSnapshot reportcore.Snapshot
	if err := json.Unmarshal(row.CanonicalJSON, &referenceSnapshot); err != nil {
		return nil, apperror.New(apperror.NotFound, "inspectionId", "Laudo não encontrado.")
	}
	referenceSnapshot = screening.RedactBlocked(referenceSnapshot)
	if err := withTask06Tenant(ctx, r.DB, meta.TenantID, func(tx *gorm.DB) error {
		return screening.ApplyDecisions(ctx, tx.Session(&gorm.Session{NewDB: true}), meta.TenantID, &referenceSnapshot)
	}); err != nil {
		return nil, err
	}
	referenceSnapshot = reportcore.LocalizeVideoPresentation(referenceSnapshot)
	start, err := decodeEvidenceCursor(stringValue(after), meta.TenantID, row.ID)
	if err != nil {
		return nil, graphql1Error("after")
	}
	page, hasNext, err := sliceCustomerEvidence(items, row.ID, limit, start)
	if err != nil {
		return nil, graphql1Error("after")
	}
	view := mapReport(row)
	labels := make(map[string]*graphql1.ReportRequirement, len(view.Requirements))
	for _, requirement := range view.Requirements {
		labels[requirement.Key] = requirement
	}
	media, err := r.mintCustomerMedia(ctx, meta.TenantID, row.ID, publication.ID, page)
	if err != nil {
		return nil, err
	}
	blockedFrames := make(map[string]bool)
	for _, item := range items {
		if item.EvidenceKind == "VIDEO_FRAME" && item.MediaAvailability == customerreport.BlockedAvailability {
			blockedFrames[item.ID] = true
		}
	}
	nodes := make([]*graphql1.CustomerEvidenceItem, 0, len(page))
	for _, item := range page {
		var section, label *string
		if requirement := labels[item.RequirementKey]; requirement != nil {
			section, label = optional(requirement.Section), optional(requirement.Label)
		}
		availability, sourceAvailability := item.MediaAvailability, optional(item.SourceAvailability)
		var url *string
		if outcome, ok := media[item.ID]; ok {
			if outcome.availability != "" {
				availability = outcome.availability
			}
			if outcome.sourceAvailability != "" {
				sourceAvailability = optional(outcome.sourceAvailability)
			}
			if availability != customerreport.BlockedAvailability {
				url = outcome.url
			}
		}
		var planID *string
		if item.EvidenceKind == "VIDEO_FRAME" {
			if value, _ := view.CanonicalJSON["videoPlanId"].(string); value != "" {
				planID = optional(value)
			}
		}
		// A blocked video frame is metadata only. Nothing that addresses its
		// pixels or its provenance leaves the API: the frame, preparation and
		// source identifiers and its position in the video are withheld, and the
		// item id (the frame id itself) becomes an opaque, non-addressable one.
		nodeID, lineageID, replacedBy := item.ID, item.LineageID, optional(item.ReplacedBy)
		frameID, preparationID, sourceMediaID, ptsMicros, timestampMs := optional(item.FrameID), optional(item.PreparationID), optional(item.SourceMediaID), optionalMicros(item.PTSMicros), item.TimestampMS
		if item.EvidenceKind == "VIDEO_FRAME" && availability == customerreport.BlockedAvailability {
			nodeID = blockedFrameNodeID(row.ID, item.ID)
			if lineageID == item.ID {
				lineageID = nodeID
			}
			frameID, preparationID, sourceMediaID, ptsMicros, timestampMs = nil, nil, nil, nil, nil
		}
		if item.ReplacedBy != "" && blockedFrames[item.ReplacedBy] {
			replacedBy = optional(blockedFrameNodeID(row.ID, item.ReplacedBy))
		}
		node := &graphql1.CustomerEvidenceItem{ID: nodeID, RequirementKey: item.RequirementKey, Section: section, Label: label, Reference: reportcore.EvidenceReference(referenceSnapshot, item.ID), Role: item.Role, Description: optional(item.Description), CaptureSource: optional(item.CaptureSource), CapturedAt: optional(item.CapturedAt), State: item.State, LineageID: lineageID, ReplacedBy: replacedBy, MediaAvailability: availability, SourceAvailability: sourceAvailability, EvidenceKind: optional(item.EvidenceKind), FrameID: frameID, PtsMicros: ptsMicros, PreparationID: preparationID, PlanID: planID, SourceMediaID: sourceMediaID, Component: label, Flags: item.Flags, URL: url}
		if timestampMs != nil {
			value := int(*timestampMs)
			node.TimestampMs = &value
		}
		if node.Flags == nil {
			node.Flags = []string{}
		}
		nodes = append(nodes, node)
	}
	end := ""
	if len(nodes) > 0 {
		end = encodeEvidenceCursor(meta.TenantID, row.ID, nodes[len(nodes)-1].ID)
	}
	return &graphql1.CustomerEvidenceConnection{Nodes: nodes, PageInfo: pageInfo(end, hasNext)}, nil
}

func optionalMicros(value *int64) *string {
	if value == nil {
		return nil
	}
	text := fmt.Sprintf("%d", *value)
	return &text
}

// blockedFrameNodeID is the opaque id a blocked video frame shows to customers.
// It is stable per snapshot but cannot be turned back into the frame id.
func blockedFrameNodeID(snapshotID identity.ID, frameID string) string {
	return identity.NewDeterministicID("customer-evidence-blocked", snapshotID.String()+":"+frameID).String()
}

// sliceCustomerEvidence cuts one page after the cursor item. The cursor carries
// the id shown to the customer, which for a blocked video frame is the opaque one.
func sliceCustomerEvidence(items []customerreport.Item, snapshotID identity.ID, first int, after string) ([]customerreport.Item, bool, error) {
	start := 0
	if after != "" {
		start = -1
		for i, item := range items {
			if item.ID == after || item.EvidenceKind == "VIDEO_FRAME" && blockedFrameNodeID(snapshotID, item.ID) == after {
				start = i + 1
				break
			}
		}
		if start < 0 {
			return nil, false, fmt.Errorf("cursor evidence not found")
		}
	}
	end := start + first
	if end > len(items) {
		end = len(items)
	}
	return items[start:end], end < len(items), nil
}

// customerMediaState carries the batch-loaded facts a signing decision needs,
// so one page costs a constant number of queries.
type customerMediaState struct {
	keys    map[string]string
	blocked map[string]bool
	// cleared holds the photos whose latest terminal screening decision allows
	// customers to see them (CLEARED, OVERRIDDEN or DISABLED). A photo without
	// any decision is never signed.
	cleared   map[string]bool
	published bool
}

// mediaOutcome overrides what the snapshot recorded for one evidence item.
type mediaOutcome struct {
	url                              *string
	availability, sourceAvailability string
}

// storePresigner adapts the API object store to the customer URL issuer.
type storePresigner struct{ store objectstore.Store }

func (p storePresigner) PresignGet(ctx context.Context, _ string, key string, ttl time.Duration) (string, error) {
	return p.store.PresignGet(ctx, key, ttl)
}

// mintCustomerMedia rechecks, at signing time, that the publication is still
// active and that each visual is not sensitive-blocked, then returns the URL
// and availability of every visible current item. Lineage items are metadata
// only and never receive a URL.
func (r *queryResolver) mintCustomerMedia(ctx context.Context, tenantID, snapshotID, publicationID identity.ID, items []customerreport.Item) (map[string]mediaOutcome, error) {
	outcomes := make(map[string]mediaOutcome)
	var photoIDs, frameIDs []identity.ID
	photoByID := make(map[identity.ID]string)
	frameItems := make(map[identity.ID]customerreport.Item)
	for _, item := range items {
		if item.State != customerreport.StateCurrent || item.MediaAvailability != "AVAILABLE" {
			continue
		}
		if item.EvidenceKind == "VIDEO_FRAME" {
			frameID, err := identity.ParseID(item.FrameID)
			if err != nil {
				outcomes[item.ID] = mediaOutcome{availability: "MISSING"}
				continue
			}
			frameIDs = append(frameIDs, frameID)
			frameItems[frameID] = item
			continue
		}
		mediaID, err := identity.ParseID(item.ID)
		if err != nil {
			outcomes[item.ID] = mediaOutcome{availability: "MISSING"}
			continue
		}
		photoIDs = append(photoIDs, mediaID)
		photoByID[mediaID] = item.ID
	}
	if len(photoIDs) == 0 && len(frameIDs) == 0 {
		return outcomes, nil
	}
	state := customerMediaState{keys: map[string]string{}, blocked: map[string]bool{}, cleared: map[string]bool{}}
	frames := make(map[identity.ID]database.VideoFrame, len(frameIDs))
	sources := make(map[identity.ID]database.VideoSource)
	err := withTask06Tenant(ctx, r.DB, tenantID, func(tx *gorm.DB) error {
		base := func() *gorm.DB { return tx.Session(&gorm.Session{NewDB: true}).WithContext(ctx) }
		var publication database.ReportPublication
		if err := base().Where("tenant_id=? AND id=? AND snapshot_id=?", tenantID, publicationID, snapshotID).Take(&publication).Error; err == nil {
			state.published = publication.Status == publicationcore.Published
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if len(photoIDs) > 0 {
			var derivatives []database.MediaDerivative
			if err := base().Where("tenant_id=? AND media_id IN ? AND kind=?", tenantID, photoIDs, "DISPLAY").Find(&derivatives).Error; err != nil {
				return err
			}
			for _, derivative := range derivatives {
				state.keys[derivative.MediaID.String()] = derivative.ObjectKey
			}
			var runs []database.ScreeningRun
			if err := base().Select("id", "media_id", "status", "created_at").Where("tenant_id=? AND media_id IN ? AND status IN ?", tenantID, photoIDs, []string{"CLEARED", "BLOCKED", "OVERRIDDEN", "DISABLED"}).Order("created_at DESC, id DESC").Find(&runs).Error; err != nil {
				return err
			}
			decided := make(map[identity.ID]bool, len(runs))
			for _, run := range runs {
				if decided[run.MediaID] {
					continue
				}
				decided[run.MediaID] = true
				if run.Status == "BLOCKED" {
					state.blocked[run.MediaID.String()] = true
				} else {
					state.cleared[run.MediaID.String()] = true
				}
			}
		}
		if len(frameIDs) > 0 {
			var rows []database.VideoFrame
			if err := base().Where("tenant_id=? AND id IN ?", tenantID, frameIDs).Find(&rows).Error; err != nil {
				return err
			}
			var sourceIDs []identity.ID
			for _, frame := range rows {
				frames[frame.ID] = frame
				sourceIDs = append(sourceIDs, frame.SourceMediaID)
			}
			if len(sourceIDs) > 0 {
				var sourceRows []database.VideoSource
				if err := base().Where("tenant_id=? AND media_id IN ?", tenantID, sourceIDs).Find(&sourceRows).Error; err != nil {
					return err
				}
				for _, source := range sourceRows {
					sources[source.MediaID] = source
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !state.published {
		// The release was withdrawn after the page was selected: nothing is signed.
		return outcomes, nil
	}
	issuer := customerreport.URLIssuer{Store: storePresigner{store: r.Store}, Bucket: r.Store.Bucket, TTL: customerMediaTTL, Authorized: func(_ context.Context, mediaID string) (string, bool, bool, error) {
		key, found := state.keys[mediaID]
		return key, found && state.published, state.blocked[mediaID] || !state.cleared[mediaID], nil
	}}
	for mediaID, evidenceID := range photoByID {
		if state.blocked[evidenceID] {
			outcomes[evidenceID] = mediaOutcome{availability: customerreport.BlockedAvailability}
			continue
		}
		if _, found := state.keys[evidenceID]; !found || !state.cleared[evidenceID] {
			outcomes[evidenceID] = mediaOutcome{availability: "MISSING"}
			continue
		}
		if r.Store.Client == nil {
			continue
		}
		signed, signErr := issuer.IssueVisible(ctx, tenantID, mediaID)
		if apperror.Is(signErr, apperror.NotFound) {
			continue
		}
		if signErr != nil {
			return nil, signErr
		}
		outcomes[evidenceID] = mediaOutcome{url: &signed}
	}
	for frameID, item := range frameItems {
		frame, found := frames[frameID]
		if !found || item.ID != item.FrameID || frame.SourceMediaID.String() != item.SourceMediaID || frame.PreparationID.String() != item.PreparationID || !strings.EqualFold(frame.SHA256, item.FrameDigest) || !strings.EqualFold(frame.SHA256, item.ContentDigest) {
			outcomes[item.ID] = mediaOutcome{availability: "MISSING"}
			continue
		}
		source, hasSource := sources[frame.SourceMediaID]
		if hasSource && source.ScreeningStatus == "BLOCKED" {
			outcomes[item.ID] = mediaOutcome{availability: customerreport.BlockedAvailability}
			continue
		}
		url := fmt.Sprintf("/media/video-evidence/%s?snapshotId=%s", frameID, snapshotID)
		outcome := mediaOutcome{url: &url, availability: "AVAILABLE", sourceAvailability: "MISSING"}
		if hasSource {
			outcome.sourceAvailability = "AVAILABLE"
			if source.Status == "EXPIRED" || source.Status == "DELETING" || source.OriginalDeletedAt != nil || source.SourceExpiryAt != nil && !source.SourceExpiryAt.After(time.Now().UTC()) {
				outcome.sourceAvailability = "EXPIRED"
			}
		}
		outcomes[item.ID] = outcome
	}
	return outcomes, nil
}

// authorizePublicationInspection checks the caller's scope over the inspection
// a publication belongs to. A missing publication and an out-of-scope one answer
// the same NOT_FOUND on publicationId, so the mutation reveals neither.
func authorizePublicationInspection(ctx context.Context, db *gorm.DB, meta requestctx.Metadata, publicationID identity.ID) error {
	const message = "Publicação não encontrada."
	var inspectionID identity.ID
	err := withTask06Tenant(ctx, db, meta.TenantID, func(tx *gorm.DB) error {
		var row database.ReportPublication
		if err := tx.Session(&gorm.Session{NewDB: true}).WithContext(ctx).Select("id", "inspection_id").Where("tenant_id=? AND id=?", meta.TenantID, publicationID).Take(&row).Error; err != nil {
			return err
		}
		inspectionID = row.InspectionID
		return nil
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return apperror.New(apperror.NotFound, "publicationId", message)
	}
	if err != nil {
		return err
	}
	return authorizeReportResource(ctx, db, meta, inspectionID, "publicationId", message)
}

func customerReasonLabels(snapshot reportcore.Snapshot) []string {
	labels := make([]string, 0, len(snapshot.ReasonCodes))
	for _, code := range snapshot.ReasonCodes {
		labels = append(labels, reportcore.ReasonLabel(code))
	}
	return labels
}
