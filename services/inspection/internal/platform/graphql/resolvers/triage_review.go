package resolvers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"inspection/libs/identity"
	triagecore "inspection/services/inspection/internal/features/dashboard/triage/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	graphql1 "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type triageQueueRow struct {
	database.TriageCase
	AssetID          identity.ID `gorm:"column:asset_id"`
	AssetName        string      `gorm:"column:asset_name"`
	Address          string      `gorm:"column:address"`
	InspectionStatus string      `gorm:"column:inspection_status"`
	FindingCount     int         `gorm:"column:finding_count"`
}

func triageScopeQuery(db *gorm.DB, meta requestctx.Metadata) *gorm.DB {
	query := db.Model(&database.TriageCase{}).
		Joins("JOIN inspections.inspections i ON i.tenant_id=dashboard.triage_cases.tenant_id AND i.id=dashboard.triage_cases.inspection_id").
		Joins("JOIN assets.assets a ON a.tenant_id=i.tenant_id AND a.id=i.asset_id").
		Where("dashboard.triage_cases.tenant_id=?", meta.TenantID)
	if hasRole(meta, auth.TenantAdmin) {
		return query
	}
	ids := make([]identity.ID, 0)
	assetIDs := make([]identity.ID, 0)
	projectIDs := make([]identity.ID, 0)
	inspectionIDs := make([]identity.ID, 0)
	for _, scope := range meta.Principal.Scopes {
		switch scope.Kind {
		case "BUSINESS_UNIT":
			ids = append(ids, scope.ID)
		case "ASSET":
			assetIDs = append(assetIDs, scope.ID)
		case "PROJECT":
			projectIDs = append(projectIDs, scope.ID)
		case "INSPECTION":
			inspectionIDs = append(inspectionIDs, scope.ID)
		}
	}
	if len(ids)+len(assetIDs)+len(projectIDs)+len(inspectionIDs) == 0 {
		return query.Where("1=0")
	}
	var parts []string
	var args []any
	if len(ids) > 0 {
		parts = append(parts, "i.business_unit_id IN ?")
		args = append(args, ids)
	}
	if len(assetIDs) > 0 {
		parts = append(parts, "i.asset_id IN ?")
		args = append(args, assetIDs)
	}
	if len(projectIDs) > 0 {
		parts = append(parts, "i.project_id IN ?")
		args = append(args, projectIDs)
	}
	if len(inspectionIDs) > 0 {
		parts = append(parts, "i.id IN ?")
		args = append(args, inspectionIDs)
	}
	return query.Where("("+strings.Join(parts, " OR ")+")", args...)
}

func triageSelect() string {
	return "dashboard.triage_cases.*, i.asset_id, a.name AS asset_name, a.address, i.status AS inspection_status, " +
		"(SELECT count(*) FROM analysis.findings f WHERE f.tenant_id=i.tenant_id AND f.analysis_run_id=(SELECT ar.id FROM analysis.analysis_runs ar WHERE ar.tenant_id=i.tenant_id AND ar.job_id IN (SELECT j.id FROM analysis.comparison_jobs j WHERE j.tenant_id=i.tenant_id AND j.inspection_id=i.id) ORDER BY ar.created_at DESC LIMIT 1)) AS finding_count"
}

func (r *queryResolver) triageWorkspace(ctx context.Context, first *int, after *string, status *graphql1.TriageReviewStatus, classification *string, search *string, assigneeID *string, reason *string) (*graphql1.TriageWorkspace, error) {
	meta, ok := requestctx.FromContext(ctx)
	if !ok {
		return nil, unauthenticated()
	}
	if err := internalRole(meta, auth.TenantAdmin, auth.Manager, auth.Employee, auth.Viewer); err != nil {
		return nil, err
	}
	limit := intValue(first)
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	base := triageScopeQuery(r.DB.WithContext(ctx), meta)
	if classification != nil && *classification != "" {
		base = base.Where("dashboard.triage_cases.classification=?", *classification)
	}
	if search != nil && strings.TrimSpace(*search) != "" {
		term := "%" + strings.TrimSpace(*search) + "%"
		base = base.Where("(a.name ILIKE ? OR a.address ILIKE ? OR i.id::text ILIKE ?)", term, term, term)
	}
	if assigneeID != nil && *assigneeID != "" {
		if *assigneeID == "UNASSIGNED" {
			base = base.Where("dashboard.triage_cases.assignee_id IS NULL")
		} else if *assigneeID == "ME" {
			base = base.Where("dashboard.triage_cases.assignee_id=?", meta.Principal.IdentityID)
		} else {
			id, err := identity.ParseID(*assigneeID)
			if err != nil {
				return nil, invalidID("assigneeId")
			}
			base = base.Where("dashboard.triage_cases.assignee_id=?", id)
		}
	}
	if reason != nil && *reason != "" {
		base = base.Where("dashboard.triage_cases.reason_codes::text LIKE ?", "%\""+strings.TrimSpace(*reason)+"\"%")
	}
	countQuery := base.Session(&gorm.Session{}).Where("i.status <> 'INVALIDATED' AND i.status <> 'CANCELED'")
	var groups []struct {
		Status         string
		Classification string
		Count          int
	}
	if err := countQuery.Select("dashboard.triage_cases.status, dashboard.triage_cases.classification, count(*) AS count").Group("dashboard.triage_cases.status, dashboard.triage_cases.classification").Find(&groups).Error; err != nil {
		return nil, err
	}
	counts := &graphql1.TriageCounts{}
	for _, row := range groups {
		switch row.Status {
		case triagecore.New:
			counts.New += row.Count
		case triagecore.InReview:
			counts.InReview += row.Count
		case triagecore.AwaitingEvidence:
			counts.AwaitingEvidence += row.Count
		}
		if row.Classification == "CRITICAL" && row.Status != triagecore.Reviewed && row.Status != triagecore.Archived {
			counts.CriticalOpen += row.Count
		}
	}
	query := base.Session(&gorm.Session{})
	if status != nil {
		query = query.Where("dashboard.triage_cases.status=?", string(*status))
		if *status != graphql1.TriageReviewStatusArchived {
			query = query.Where("i.status <> 'INVALIDATED' AND i.status <> 'CANCELED'")
		}
	} else {
		query = query.Where("dashboard.triage_cases.status IN ? AND i.status <> 'INVALIDATED' AND i.status <> 'CANCELED'", []string{triagecore.New, triagecore.InReview, triagecore.AwaitingEvidence})
	}
	if after != nil && *after != "" {
		offset, err := decodeTriageOffset(*after)
		if err != nil {
			return nil, graphql1Error("after")
		}
		query = query.Offset(offset)
	}
	var rows []triageQueueRow
	err := query.Select(triageSelect()).Order("CASE dashboard.triage_cases.classification WHEN 'CRITICAL' THEN 0 WHEN 'ATTENTION' THEN 1 ELSE 2 END ASC, dashboard.triage_cases.created_at ASC, dashboard.triage_cases.inspection_id ASC").Limit(limit + 1).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	hasNext := len(rows) > limit
	if hasNext {
		rows = rows[:limit]
	}
	nodes := make([]*graphql1.TriageQueueItem, 0, len(rows))
	for _, row := range rows {
		nodes = append(nodes, mapTriageQueueItem(row))
	}
	end := ""
	if hasNext {
		offset := 0
		if after != nil {
			offset, _ = decodeTriageOffset(*after)
		}
		end = encodeTriageOffset(offset + limit)
	}
	return &graphql1.TriageWorkspace{Counts: counts, Nodes: nodes, PageInfo: pageInfo(end, hasNext)}, nil
}

func (r *queryResolver) triageCase(ctx context.Context, inspectionID string) (*graphql1.TriageCase, error) {
	meta, ok := requestctx.FromContext(ctx)
	if !ok {
		return nil, unauthenticated()
	}
	if err := internalRole(meta, auth.TenantAdmin, auth.Manager, auth.Employee, auth.Viewer); err != nil {
		return nil, err
	}
	id, err := identity.ParseID(inspectionID)
	if err != nil {
		return nil, invalidID("inspectionId")
	}
	var row triageQueueRow
	err = triageScopeQuery(r.DB.WithContext(ctx), meta).Where("dashboard.triage_cases.inspection_id=?", id).Select(triageSelect()).Take(&row).Error
	if err != nil {
		return nil, err
	}
	result := mapTriageCase(row)
	var events []database.TriageCaseEvent
	if err := withTask06Tenant(ctx, r.DB, meta.TenantID, func(tx *gorm.DB) error {
		return tx.Where("case_id=?", row.ID).Order("created_at ASC").Find(&events).Error
	}); err != nil {
		return nil, err
	}
	for _, event := range events {
		result.Events = append(result.Events, &graphql1.TriageCaseEvent{ID: event.ID.String(), ActorID: event.ActorID.String(), Kind: event.Kind, Body: event.Body, CreatedAt: event.CreatedAt.UTC().Format(time.RFC3339Nano)})
	}
	if row.ReportVersion > 0 {
		var snapshot database.ReportSnapshot
		if err := withTask06Tenant(ctx, r.DB, meta.TenantID, func(tx *gorm.DB) error {
			return tx.Where("inspection_id=? AND version_number=?", id, row.ReportVersion).Take(&snapshot).Error
		}); err == nil {
			result.Report = mapReport(snapshot)
			if err := hydrateReportMedia(ctx, r.DB, r.Store, meta.TenantID, result.Report); err != nil {
				return nil, err
			}
		} else if err != gorm.ErrRecordNotFound {
			return nil, err
		}
	}
	return result, nil
}

func (r *queryResolver) triageAssignees(ctx context.Context, inspectionID string) ([]*graphql1.TriageAssignee, error) {
	meta, ok := requestctx.FromContext(ctx)
	if !ok {
		return nil, unauthenticated()
	}
	if err := internalRole(meta, auth.TenantAdmin, auth.Manager, auth.Employee, auth.Viewer); err != nil {
		return nil, err
	}
	id, err := identity.ParseID(inspectionID)
	if err != nil {
		return nil, invalidID("inspectionId")
	}
	var inspection database.Inspection
	if err := withTask06Tenant(ctx, r.DB, meta.TenantID, func(tx *gorm.DB) error { return tx.Where("id=?", id).Take(&inspection).Error }); err != nil {
		return nil, err
	}
	if _, err := r.Authorizer.Authorize(ctx, meta.TenantID, []string{auth.TenantAdmin, auth.Manager, auth.Employee, auth.Viewer}, &requestctx.Scope{Kind: "INSPECTION", ID: inspection.ID}, false); err != nil {
		return nil, err
	}
	var members []database.Membership
	err = withTask06Tenant(ctx, r.DB, meta.TenantID, func(tx *gorm.DB) error {
		return tx.Where("status='ACTIVE' AND role IN ?", []string{auth.TenantAdmin, auth.Manager, auth.Employee}).Order("role ASC, id ASC").Find(&members).Error
	})
	if err != nil {
		return nil, err
	}
	out := make([]*graphql1.TriageAssignee, 0, len(members))
	for _, member := range members {
		if member.IdentityID == meta.Principal.IdentityID || member.Role == auth.TenantAdmin {
			out = append(out, &graphql1.TriageAssignee{ID: member.IdentityID.String(), Role: member.Role, Current: member.IdentityID == meta.Principal.IdentityID})
			continue
		}
		var scopes int64
		if err := withTask06Tenant(ctx, r.DB, meta.TenantID, func(tx *gorm.DB) error {
			return tx.Model(&database.ResourceScope{}).Where("membership_id=? AND ((kind='BUSINESS_UNIT' AND resource_id=?) OR (kind='ASSET' AND resource_id=?) OR (kind='PROJECT' AND resource_id=?) OR (kind='INSPECTION' AND resource_id=?))", member.ID, inspection.BusinessUnitID, inspection.AssetID, inspection.ProjectID, inspection.ID).Count(&scopes).Error
		}); err != nil {
			return nil, err
		}
		if scopes > 0 {
			out = append(out, &graphql1.TriageAssignee{ID: member.IdentityID.String(), Role: member.Role, Current: member.IdentityID == meta.Principal.IdentityID})
		}
	}
	return out, nil
}

func (r *mutationResolver) updateTriageCase(ctx context.Context, input graphql1.UpdateTriageCaseInput) (*graphql1.TriageCasePayload, error) {
	meta, ok := requestctx.FromContext(ctx)
	if !ok {
		return nil, unauthenticated()
	}
	if err := internalRole(meta, auth.TenantAdmin, auth.Manager, auth.Employee); err != nil {
		return nil, err
	}
	id, err := identity.ParseID(input.InspectionID)
	if err != nil {
		return nil, invalidID("inspectionId")
	}
	var inspection database.Inspection
	if err := withTask06Tenant(ctx, r.DB, meta.TenantID, func(tx *gorm.DB) error { return tx.Where("id=?", id).Take(&inspection).Error }); err != nil {
		return nil, err
	}
	if _, err := r.Authorizer.Authorize(ctx, meta.TenantID, []string{auth.TenantAdmin, auth.Manager, auth.Employee}, &requestctx.Scope{Kind: "INSPECTION", ID: inspection.ID}, true); err != nil {
		return nil, err
	}
	var assignee *identity.ID
	if input.AssigneeID != nil {
		parsed, err := identity.ParseID(*input.AssigneeID)
		if err != nil {
			return nil, invalidID("assigneeId")
		}
		assignee = &parsed
	}
	body := ""
	if input.Body != nil {
		body = strings.TrimSpace(*input.Body)
	}
	disposition := ""
	if input.Disposition != nil {
		disposition = string(*input.Disposition)
	}
	var row database.TriageCase
	err = withTask06Tenant(ctx, r.DB, meta.TenantID, func(tx *gorm.DB) error {
		var prior database.TriageCaseEvent
		if input.ClientMutationID != "" && tx.Where("client_mutation_id=?", input.ClientMutationID).Take(&prior).Error == nil {
			return tx.Where("id=?", prior.CaseID).Take(&row).Error
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("inspection_id=?", id).Take(&row).Error; err != nil {
			return err
		}
		if row.Version != int64(input.ExpectedVersion) {
			return graphql1Error("expectedVersion")
		}
		if !hasAnyRole(meta, auth.Manager, auth.TenantAdmin) && input.Action == graphql1.TriageCaseActionAssign {
			return apperror.New(apperror.Forbidden, "action", "access denied")
		}
		if input.Action == graphql1.TriageCaseActionComplete && row.AssigneeID != nil && *row.AssigneeID != meta.Principal.IdentityID && !hasAnyRole(meta, auth.Manager, auth.TenantAdmin) {
			return apperror.New(apperror.Forbidden, "action", "access denied")
		}
		if input.Action == graphql1.TriageCaseActionTake && row.AssigneeID != nil && *row.AssigneeID != meta.Principal.IdentityID {
			return graphql1Error("assigneeId")
		}
		next, valid := triagecore.Transition(row.Status, string(input.Action), assignee != nil, body != "", triagecore.ValidDisposition(disposition))
		if !valid {
			return graphql1Error("action")
		}
		if input.Action == graphql1.TriageCaseActionComplete && row.ReportVersion == 0 {
			return graphql1Error("report")
		}
		if input.Action == graphql1.TriageCaseActionComplete {
			if inspection.Status != "COMPLETED" {
				return graphql1Error("inspectionStatus")
			}
			var latest database.ReportSnapshot
			if err := tx.Where("inspection_id=?", id).Order("version_number DESC").Take(&latest).Error; err != nil || latest.VersionNumber != row.ReportVersion {
				return graphql1Error("reportVersion")
			}
		}
		if input.Action == graphql1.TriageCaseActionAssign {
			var member database.Membership
			if err := tx.Where("identity_id=? AND status='ACTIVE' AND role IN ?", *assignee, []string{auth.TenantAdmin, auth.Manager, auth.Employee}).Take(&member).Error; err != nil {
				return graphql1Error("assigneeId")
			}
			if !hasRole(meta, auth.TenantAdmin) {
				var scoped int64
				if err := tx.Model(&database.ResourceScope{}).Where("membership_id=? AND ((kind='BUSINESS_UNIT' AND resource_id=?) OR (kind='ASSET' AND resource_id=?) OR (kind='PROJECT' AND resource_id=?) OR (kind='INSPECTION' AND resource_id=?))", member.ID, inspection.BusinessUnitID, inspection.AssetID, inspection.ProjectID, inspection.ID).Count(&scoped).Error; err != nil {
					return err
				}
				if scoped == 0 {
					return graphql1Error("assigneeId")
				}
			}
		}
		now := time.Now().UTC()
		actor := meta.Principal.IdentityID
		if input.Action == graphql1.TriageCaseActionTake {
			assignee = &actor
		}
		if input.Action == graphql1.TriageCaseActionReopen {
			assignee = nil
		}
		if input.Action == graphql1.TriageCaseActionAssign {
			row.AssigneeID = assignee
		}
		if input.Action == graphql1.TriageCaseActionTake || input.Action == graphql1.TriageCaseActionReopen {
			row.AssigneeID = assignee
		}
		row.Status = next
		row.Version++
		row.UpdatedAt = now
		if err := tx.Model(&row).Updates(map[string]any{"status": row.Status, "assignee_id": row.AssigneeID, "version": row.Version, "updated_at": now}).Error; err != nil {
			return err
		}
		kind := string(input.Action)
		eventBody := body
		if input.Action == graphql1.TriageCaseActionComplete {
			eventBody = disposition + ": " + body
		}
		event := database.TriageCaseEvent{ID: identity.NewID(), TenantID: meta.TenantID, CaseID: row.ID, ActorID: actor, Kind: kind, Body: eventBody, ClientMutationID: input.ClientMutationID, CreatedAt: now}
		if err := tx.Create(&event).Error; err != nil {
			return err
		}
		if input.Action == graphql1.TriageCaseActionAssign && row.AssigneeID != nil && *row.AssigneeID != actor {
			var recipient database.Membership
			if err := tx.Where("tenant_id=? AND identity_id=? AND status='ACTIVE'", meta.TenantID, *row.AssigneeID).Take(&recipient).Error; err != nil {
				return err
			}
			var asset database.Asset
			if err := tx.Where("tenant_id=? AND id=?", meta.TenantID, inspection.AssetID).Take(&asset).Error; err != nil {
				return err
			}
			notification := database.RecipientNotification{ID: identity.NewDeterministicID("inspection/triage-assigned-notification", event.ID.String()), TenantID: meta.TenantID, RecipientMembershipID: recipient.ID, EventID: event.ID, Kind: "TRIAGE_ASSIGNED", Title: "Caso de vistoria atribuído a você", Body: asset.Name + " · " + asset.Address + ". Abra a triagem para revisar o caso.", ResourceKind: "INSPECTION", ResourceID: &inspection.ID, CreatedAt: now}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&notification).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	result, err := (&queryResolver{r.Resolver}).triageCase(ctx, id.String())
	if err != nil {
		return nil, err
	}
	return &graphql1.TriageCasePayload{TriageCase: result, UserErrors: []*graphql1.UserError{}, ClientMutationID: input.ClientMutationID}, nil
}

func recordTriageRecaptureRequest(ctx context.Context, db *gorm.DB, meta requestctx.Metadata, inspectionID identity.ID, mutationID string) error {
	if db == nil || !db.Migrator().HasTable(&database.TriageCase{}) {
		return nil
	}
	return withTask06Tenant(ctx, db, meta.TenantID, func(tx *gorm.DB) error {
		var prior database.TriageCaseEvent
		if mutationID != "" {
			if err := tx.Where("client_mutation_id=?", mutationID).Take(&prior).Error; err == nil {
				return nil
			} else if err != gorm.ErrRecordNotFound {
				return err
			}
		}
		var row database.TriageCase
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("inspection_id=?", inspectionID).Take(&row).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return nil
			}
			return err
		}
		if row.Status != triagecore.InReview {
			return nil
		}
		now := time.Now().UTC()
		row.Status = triagecore.AwaitingEvidence
		row.Version++
		row.UpdatedAt = now
		if err := tx.Model(&row).Updates(map[string]any{"status": row.Status, "version": row.Version, "updated_at": now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.TriageCaseEvent{ID: identity.NewID(), TenantID: meta.TenantID, CaseID: row.ID, ActorID: meta.Principal.IdentityID, Kind: "WAIT_FOR_EVIDENCE", Body: "Complemento solicitado para revisão.", ClientMutationID: mutationID, CreatedAt: now}).Error
	})
}

func mapTriageQueueItem(row triageQueueRow) *graphql1.TriageQueueItem {
	var reasons []string
	_ = json.Unmarshal(row.ReasonCodes, &reasons)
	var assignee *string
	if row.AssigneeID != nil {
		value := row.AssigneeID.String()
		assignee = &value
	}
	return &graphql1.TriageQueueItem{InspectionID: row.InspectionID.String(), AssetID: row.AssetID.String(), AssetName: row.AssetName, Address: row.Address, Classification: row.Classification, Status: row.InspectionStatus, ReviewStatus: graphql1.TriageReviewStatus(row.Status), AssigneeID: assignee, ReportVersion: row.ReportVersion, Version: int(row.Version), FindingCount: row.FindingCount, ReasonCodes: reasons, CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339Nano), UpdatedAt: row.UpdatedAt.UTC().Format(time.RFC3339Nano)}
}

func mapTriageCase(row triageQueueRow) *graphql1.TriageCase {
	item := mapTriageQueueItem(row)
	return &graphql1.TriageCase{InspectionID: item.InspectionID, AssetID: item.AssetID, AssetName: item.AssetName, Address: item.Address, Classification: item.Classification, Status: item.Status, ReviewStatus: item.ReviewStatus, AssigneeID: item.AssigneeID, ReportVersion: item.ReportVersion, Version: item.Version, ReasonCodes: item.ReasonCodes, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt, Events: []*graphql1.TriageCaseEvent{}}
}

func encodeTriageOffset(offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(offset)))
}

func hasAnyRole(meta requestctx.Metadata, roles ...string) bool {
	for _, role := range roles {
		if hasRole(meta, role) {
			return true
		}
	}
	return false
}
func decodeTriageOffset(value string) (int, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return 0, err
	}
	offset, err := strconv.Atoi(string(decoded))
	if err != nil || offset < 0 {
		return 0, fmt.Errorf("invalid cursor")
	}
	return offset, nil
}
