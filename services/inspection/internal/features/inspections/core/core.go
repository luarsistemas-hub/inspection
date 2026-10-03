// Package core owns immutable occurrence snapshots and inspection lifecycle rules.
package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"inspection/libs/identity"
	resolveprompt "inspection/services/inspection/internal/features/analysis/resolve_prompt"
	assetcore "inspection/services/inspection/internal/features/assets/core"
	assetget "inspection/services/inspection/internal/features/assets/get_asset"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	originresolve "inspection/services/inspection/internal/features/origins/resolve_reference"
	participantcore "inspection/services/inspection/internal/features/participants/core"
	participantget "inspection/services/inspection/internal/features/participants/get_participant"
	projectcore "inspection/services/inspection/internal/features/projects/core"
	"inspection/services/inspection/internal/features/templates/catalog"
	templateresolve "inspection/services/inspection/internal/features/templates/resolve_template"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/pagination"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/tenanttx"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	SourceScheduled = "SCHEDULED"
	SourceMilestone = "MILESTONE"
	SourceManual    = "MANUAL"
)

type CreateInput struct {
	TenantID, AssetID, ParticipantID identity.ID
	TemplateID                       *identity.ID
	TemplateVersionID                *identity.ID
	ComparisonMode                   catalog.ComparisonMode
	ReferenceVersionID               *identity.ID
	ProjectID, StageID               *identity.ID
	Source, SourceKey, Reason        string
	DueAt, DeadlineAt                time.Time
	ReminderInstants                 []time.Time
}

type View struct {
	Inspection       database.Inspection
	Responsibility   database.Responsibility
	Policy           database.PolicySnapshot
	Reference        database.ReferenceSnapshot
	EventID          identity.ID
	AssetName        *string
	AssetAddress     *string
	AssetExternalKey *string
	ParticipantName  *string
}

type ListInput struct {
	TenantID    identity.ID
	First       int
	After       string
	History     bool
	Search      string
	StatusGroup string
}

type ListResult struct {
	Items       []View
	EndCursor   string
	HasNextPage bool
}

type Service struct {
	DB         *gorm.DB
	Bus        *mediator.Bus
	Authorizer auth.Authorizer
	Now        func() time.Time
}

func (s Service) Create(ctx context.Context, in CreateInput) (View, error) {
	if in.TenantID == (identity.ID{}) || in.AssetID == (identity.ID{}) || in.ParticipantID == (identity.ID{}) || strings.TrimSpace(in.SourceKey) == "" {
		return View{}, apperror.New(apperror.InvalidInput, "input", "tenant, asset, participant, and source key are required")
	}
	if in.Source != SourceScheduled && in.Source != SourceMilestone && in.Source != SourceManual {
		return View{}, apperror.New(apperror.InvalidInput, "source", "invalid inspection source")
	}
	if in.Source == SourceManual && strings.TrimSpace(in.Reason) == "" {
		return View{}, apperror.New(apperror.InvalidInput, "reason", "manual inspection reason is required")
	}
	if utf8.RuneCountInString(in.Reason) > catalog.MaxTextCodePoints {
		return View{}, apperror.New(apperror.InvalidInput, "reason", "reason is too long")
	}
	if in.DeadlineAt.IsZero() || (!in.DueAt.IsZero() && in.DeadlineAt.Before(in.DueAt)) {
		return View{}, apperror.New(apperror.InvalidInput, "deadlineAt", "deadline must not precede occurrence")
	}
	reminders, err := NormalizeReminders(in.ReminderInstants, in.DueAt, in.DeadlineAt)
	if err != nil {
		return View{}, err
	}
	if s.Bus == nil {
		return View{}, fmt.Errorf("inspection create: missing mediator")
	}
	assetRaw, err := s.Bus.Ask(ctx, assetget.Query{TenantID: in.TenantID, AssetID: in.AssetID})
	if err != nil {
		return View{}, err
	}
	asset := assetRaw.(assetcore.View)
	if asset.Asset.Status != "ACTIVE" {
		return View{}, apperror.New(apperror.InvalidState, "assetId", "asset is not active")
	}
	participantRaw, err := s.Bus.Ask(ctx, participantget.Query{TenantID: in.TenantID, ParticipantID: in.ParticipantID})
	if err != nil {
		return View{}, err
	}
	participant := participantRaw.(participantcore.ParticipantView)
	if participant.Participant.Status != "ACTIVE" || len(participant.Selected) == 0 {
		return View{}, apperror.New(apperror.InvalidState, "participantId", "participant requires an active verified delivery channel")
	}
	templateID := in.TemplateID
	if templateID == nil {
		templateID = asset.Asset.TemplateID
	}
	if templateID == nil {
		return View{}, apperror.New(apperror.InvalidState, "templateId", "active template is required")
	}
	templateRaw, err := s.Bus.Ask(ctx, templateresolve.Query{TenantID: in.TenantID, TemplateID: *templateID, VersionID: in.TemplateVersionID})
	if err != nil {
		return View{}, err
	}
	template := templateRaw.(templateresolve.Result)
	var document catalog.TemplateDocument
	if err := json.Unmarshal(template.Version.DefinitionJSON, &document); err != nil {
		return View{}, fmt.Errorf("decode template snapshot: %w", err)
	}
	comparisonMode := document.DefaultComparisonMode
	if in.ComparisonMode != "" {
		comparisonMode = in.ComparisonMode
	}
	if comparisonMode != catalog.ChecklistOnly && comparisonMode != catalog.FixedOrigin {
		return View{}, apperror.New(apperror.InvalidInput, "comparisonMode", "unsupported comparison mode")
	}
	var originReference *originresolve.Result
	if comparisonMode == catalog.FixedOrigin {
		referenceRaw, err := s.Bus.Ask(ctx, originresolve.Query{TenantID: in.TenantID, AssetID: in.AssetID, VersionID: in.ReferenceVersionID})
		if err != nil {
			return View{}, err
		}
		resolved := referenceRaw.(originresolve.Result)
		originReference = &resolved
		referenceID := resolved.Version.ID
		in.ReferenceVersionID = &referenceID
	} else if requiresReference(comparisonMode) && in.ReferenceVersionID == nil {
		return View{}, apperror.New(apperror.InvalidState, "referenceVersionId", "effective reference is required")
	}
	if !strings.EqualFold(document.AnalysisType, "REAL_ESTATE") {
		return View{}, apperror.New(apperror.InvalidInput, "analysisType", "unknown analysis type")
	}
	if in.Source != SourceScheduled {
		roles := []string{auth.TenantAdmin, auth.Manager, auth.Employee}
		if _, err := s.Authorizer.Authorize(ctx, in.TenantID, roles, &requestctx.Scope{Kind: "BUSINESS_UNIT", ID: asset.Asset.BusinessUnitID}, true); err != nil {
			return View{}, err
		}
		if _, err := s.Authorizer.Authorize(ctx, in.TenantID, roles, &requestctx.Scope{Kind: "BUSINESS_UNIT", ID: participant.Participant.BusinessUnitID}, true); err != nil {
			return View{}, err
		}
	}
	policyPayload, policyDigest, err := snapshotPolicy(document, asset, comparisonMode, in.ReferenceVersionID)
	if err != nil {
		return View{}, err
	}
	reminderJSON, _ := json.Marshal(reminders)
	now := s.now()
	var out View
	err = (tenanttx.Runner{DB: s.DB}).Within(ctx, in.TenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND source=? AND source_key=?", in.TenantID, in.Source, in.SourceKey).First(&out.Inspection).Error; err == nil {
			out.EventID = createdEventID(out.Inspection.ID)
			return s.load(tx, &out)
		} else if err != gorm.ErrRecordNotFound {
			return err
		}
		if err := projectcore.ValidateAssociation(tx, in.TenantID, in.AssetID, in.ProjectID, in.StageID); err != nil {
			return err
		}
		var count int64
		if err := tx.Model(&database.Inspection{}).Where("tenant_id=?", in.TenantID).Count(&count).Error; err != nil {
			return err
		}
		if count >= catalog.MaxInspections {
			return apperror.New(apperror.InvalidState, "inspections", "inspection capacity reached")
		}
		resolvedPrompt, err := resolveprompt.Resolve(ctx, tx, document.AnalysisType, now)
		if err != nil {
			return err
		}
		contextSnapshot, _ := json.Marshal(map[string]any{
			"tenantId": in.TenantID, "businessUnitId": asset.Asset.BusinessUnitID,
			"assetId": asset.Asset.ID, "assetVersion": asset.Asset.Version,
			"participantId": participant.Participant.ID, "participantVersion": participant.Participant.Version,
			"templateId": *templateID, "templateVersionId": template.Version.ID,
			"analysisType": document.AnalysisType, "analysisPromptSnapshotId": resolvedPrompt.Snapshot.ID, "source": in.Source,
		})
		inspection := database.Inspection{ID: identity.NewID(), TenantID: in.TenantID, BusinessUnitID: asset.Asset.BusinessUnitID, AssetID: in.AssetID, ParticipantID: in.ParticipantID, TemplateID: *templateID, TemplateVersionID: template.Version.ID, AnalysisPromptSnapshotID: resolvedPrompt.Snapshot.ID, ProjectID: in.ProjectID, StageID: in.StageID, Source: in.Source, SourceKey: in.SourceKey, SourceReason: strings.TrimSpace(in.Reason), Status: "PLANNED", DueAt: in.DueAt.UTC(), DeadlineAt: in.DeadlineAt.UTC(), ReminderInstants: reminderJSON, ContextSnapshot: contextSnapshot, Version: 1, CreatedAt: now, UpdatedAt: now}
		responsibility := database.Responsibility{ID: identity.NewID(), TenantID: in.TenantID, InspectionID: inspection.ID, ParticipantID: in.ParticipantID, Status: "PENDING", Version: 1, CreatedAt: now, UpdatedAt: now}
		policy := database.PolicySnapshot{ID: identity.NewID(), TenantID: in.TenantID, InspectionID: inspection.ID, SchemaVersion: 1, Payload: policyPayload, CanonicalDigest: policyDigest, CreatedAt: now}
		referenceDocument := map[string]any{"referenceVersionId": in.ReferenceVersionID, "comparisonMode": comparisonMode}
		if originReference != nil {
			referenceDocument["originVersionId"] = originReference.Version.ID
			referenceDocument["items"] = originReference.Evidence
		}
		referencePayload, _ := json.Marshal(referenceDocument)
		reference := database.ReferenceSnapshot{ID: identity.NewID(), TenantID: in.TenantID, InspectionID: inspection.ID, ReferenceVersionID: in.ReferenceVersionID, ComparisonMode: string(comparisonMode), Payload: referencePayload, CreatedAt: now}
		requirements := make([]capturecore.Requirement, 0, len(document.Requirements))
		if originReference != nil {
			requirements = append(requirements, originReference.Requirements...)
		} else {
			for _, requirement := range document.Requirements {
				requirements = append(requirements, capturecore.Requirement{
					Key: requirement.Key, Section: requirement.Section, Label: requirement.Label,
					Instructions: requirement.Instructions, EvidenceKind: requirement.EvidenceKind,
					Required: requirement.Required, ImpossibilityAllowed: true,
					MinimumMedia: requirement.MinimumCount, MaximumMedia: requirement.MaximumCount,
					DescriptionRequired: requirement.DescriptionRequired,
					CaptureSourcePolicy: requirement.CaptureSourcePolicy,
					ComparisonTarget:    string(comparisonMode),
				})
			}
		}
		requirementPayload, _ := json.Marshal(requirements)
		draft := database.CaptureDraft{ID: identity.NewID(), TenantID: in.TenantID, ResponsibilityID: responsibility.ID, Kind: "INSPECTION", TemplateVersionID: template.Version.ID, ReferencePayload: referencePayload, PolicyPayload: policyPayload, Requirements: requirementPayload, Status: "OPEN", Version: 1, CreatedAt: now, UpdatedAt: now}
		for _, row := range []any{&inspection, &responsibility, &policy, &reference, &draft} {
			if err := tx.Create(row).Error; err != nil {
				return err
			}
		}
		eventID := createdEventID(inspection.ID)
		if err := createOutbox(tx, eventID, in.TenantID, inspection.ID, "inspection.created.v1", map[string]any{"inspectionId": inspection.ID, "responsibilityId": responsibility.ID, "assetId": inspection.AssetID, "participantId": inspection.ParticipantID, "templateVersionId": inspection.TemplateVersionID, "source": inspection.Source, "dueAt": in.DueAt, "deadlineAt": inspection.DeadlineAt}, ctx, now); err != nil {
			return err
		}
		out = View{Inspection: inspection, Responsibility: responsibility, Policy: policy, Reference: reference, EventID: eventID}
		return nil
	})
	if err != nil {
		return View{}, err
	}
	return out, nil
}

func (s Service) Cancel(ctx context.Context, tenantID, inspectionID identity.ID, expectedVersion int64) (View, error) {
	return s.transition(ctx, tenantID, inspectionID, expectedVersion, "CANCELED", "")
}

func (s Service) Invalidate(ctx context.Context, tenantID, inspectionID identity.ID, expectedVersion int64, reason string) (View, error) {
	return s.transition(ctx, tenantID, inspectionID, expectedVersion, "INVALIDATED", reason)
}

func (s Service) transition(ctx context.Context, tenantID, inspectionID identity.ID, expectedVersion int64, target, reason string) (View, error) {
	if target == "INVALIDATED" && strings.TrimSpace(reason) == "" {
		return View{}, apperror.New(apperror.InvalidInput, "reason", "invalidation reason is required")
	}
	if utf8.RuneCountInString(reason) > catalog.MaxTextCodePoints {
		return View{}, apperror.New(apperror.InvalidInput, "reason", "reason is too long")
	}
	current, err := s.Get(ctx, tenantID, inspectionID)
	if err != nil {
		return View{}, err
	}
	if _, err := s.Authorizer.Authorize(ctx, tenantID, []string{auth.TenantAdmin, auth.Manager, auth.Employee}, &requestctx.Scope{Kind: "BUSINESS_UNIT", ID: current.Inspection.BusinessUnitID}, true); err != nil {
		return View{}, err
	}
	if current.Inspection.Status == target {
		return current, nil
	}
	if err := ValidateInspectionTransition(current.Inspection.Status, target, current.Inspection.EvidenceCount, reason); err != nil {
		return View{}, err
	}
	now := s.now()
	err = (tenanttx.Runner{DB: s.DB}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		r := tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=? AND version=?", tenantID, inspectionID, expectedVersion).Updates(map[string]any{"status": target, "state_reason": strings.TrimSpace(reason), "version": expectedVersion + 1, "updated_at": now})
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return apperror.New(apperror.Conflict, "version", "stale inspection version")
		}
		if err := tx.Model(&database.Responsibility{}).Where("tenant_id=? AND inspection_id=? AND status NOT IN ?", tenantID, inspectionID, []string{"SUBMITTED", "EXPIRED", "REVOKED", "CANCELED"}).Updates(map[string]any{"status": map[bool]string{true: "CANCELED", false: "REVOKED"}[target == "CANCELED"], "version": gorm.Expr("version + 1"), "updated_at": now}).Error; err != nil {
			return err
		}
		return createOutbox(tx, identity.NewID(), tenantID, inspectionID, "inspection.state_changed.v1", map[string]any{"inspectionId": inspectionID, "responsibilityId": current.Responsibility.ID, "from": current.Inspection.Status, "to": target, "reason": strings.TrimSpace(reason), "revokeSessions": true}, ctx, now)
	})
	if err != nil {
		return View{}, err
	}
	return s.Get(ctx, tenantID, inspectionID)
}

func (s Service) Get(ctx context.Context, tenantID, inspectionID identity.ID) (View, error) {
	var out View
	err := (tenanttx.Runner{DB: s.DB}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND id=?", tenantID, inspectionID).First(&out.Inspection).Error; err != nil {
			return apperror.New(apperror.NotFound, "inspectionId", "inspection not found")
		}
		if err := s.load(tx, &out); err != nil {
			return err
		}
		var display struct {
			AssetName        *string `gorm:"column:asset_name"`
			AssetAddress     *string `gorm:"column:asset_address"`
			AssetExternalKey *string `gorm:"column:asset_external_key"`
			ParticipantName  *string `gorm:"column:participant_name"`
		}
		if err := tx.Table("inspections.inspections AS i").
			Select("a.name AS asset_name, a.address AS asset_address, a.external_key AS asset_external_key, p.name AS participant_name").
			Joins("LEFT JOIN assets.assets AS a ON a.tenant_id=i.tenant_id AND a.id=i.asset_id").
			Joins("LEFT JOIN participants.participants AS p ON p.tenant_id=i.tenant_id AND p.id=i.participant_id").
			Where("i.tenant_id=? AND i.id=?", tenantID, inspectionID).
			Scan(&display).Error; err != nil {
			return err
		}
		out.AssetName = display.AssetName
		out.AssetAddress = display.AssetAddress
		out.AssetExternalKey = display.AssetExternalKey
		out.ParticipantName = display.ParticipantName
		return nil
	})
	if err != nil {
		return View{}, err
	}
	if _, err := s.Authorizer.Authorize(ctx, tenantID, []string{auth.TenantAdmin, auth.Manager, auth.Employee, auth.Viewer}, &requestctx.Scope{Kind: "BUSINESS_UNIT", ID: out.Inspection.BusinessUnitID}, false); err != nil {
		return View{}, err
	}
	return out, nil
}

func (s Service) List(ctx context.Context, in ListInput) (ListResult, error) {
	if in.First <= 0 {
		in.First = 25
	}
	if in.First > 100 {
		return ListResult{}, apperror.New(apperror.InvalidInput, "first", "page size cannot exceed 100")
	}
	principal, err := s.Authorizer.Authorize(ctx, in.TenantID, []string{auth.TenantAdmin, auth.Manager, auth.Employee, auth.Viewer}, nil, false)
	if err != nil {
		return ListResult{}, err
	}
	if !validInspectionStatusGroup(in.StatusGroup) {
		return ListResult{}, apperror.New(apperror.InvalidInput, "statusGroup", "invalid inspection status group")
	}
	var rows []struct {
		ID        identity.ID
		CreatedAt time.Time
	}
	allowedUnits := businessUnitScopes(principal)
	if !hasInspectionRole(principal.Roles, auth.TenantAdmin) && len(allowedUnits) == 0 {
		return ListResult{}, nil
	}
	err = (tenanttx.Runner{DB: s.DB}).Within(ctx, in.TenantID, func(tx *gorm.DB) error {
		q := tx.Table("inspections.inspections AS i").
			Select("i.id, i.created_at").
			Joins("LEFT JOIN assets.assets AS a ON a.tenant_id=i.tenant_id AND a.id=i.asset_id").
			Joins("LEFT JOIN participants.participants AS p ON p.tenant_id=i.tenant_id AND p.id=i.participant_id").
			Where("i.tenant_id=?", in.TenantID)
		if !hasInspectionRole(principal.Roles, auth.TenantAdmin) {
			q = q.Where("i.business_unit_id IN ?", allowedUnits)
		}
		if !in.History {
			q = q.Where("i.status <> 'INVALIDATED'")
		}
		if statuses := statusesForGroup(in.StatusGroup); len(statuses) > 0 {
			q = q.Where("i.status IN ?", statuses)
		}
		if search := strings.TrimSpace(in.Search); search != "" {
			term := "%" + escapeInspectionSearch(search) + "%"
			operator := "ILIKE"
			idExpression := "i.id::text"
			if s.DB.Dialector.Name() == "sqlite" {
				operator = "LIKE"
				idExpression = "CAST(i.id AS TEXT)"
			}
			q = q.Where("("+idExpression+" "+operator+" ? ESCAPE '\\' OR a.name "+operator+" ? ESCAPE '\\' OR a.address "+operator+" ? ESCAPE '\\' OR a.external_key "+operator+" ? ESCAPE '\\' OR p.name "+operator+" ? ESCAPE '\\' OR i.source "+operator+" ? ESCAPE '\\' OR i.status "+operator+" ? ESCAPE '\\')", term, term, term, term, term, term, term)
		}
		if in.After != "" {
			at, id, err := inspectionListCursor(tx, in.TenantID, in.After, allowedUnits, hasInspectionRole(principal.Roles, auth.TenantAdmin))
			if err != nil {
				return err
			}
			q = q.Where("(i.created_at, i.id) > (?, ?)", at, id)
		}
		return q.Order("i.created_at, i.id").Limit(in.First + 1).Find(&rows).Error
	})
	if err != nil {
		return ListResult{}, err
	}
	result := ListResult{HasNextPage: len(rows) > in.First}
	if result.HasNextPage {
		rows = rows[:in.First]
	}
	for _, row := range rows {
		view, err := s.Get(ctx, in.TenantID, row.ID)
		if err != nil {
			return ListResult{}, err
		}
		result.Items = append(result.Items, view)
		result.EndCursor = pagination.Encode(row.CreatedAt, row.ID)
	}
	return result, nil
}

func validInspectionStatusGroup(group string) bool {
	switch group {
	case "", "PLANNING", "EXECUTION", "COMPLETED", "CLOSED":
		return true
	default:
		return false
	}
}

func statusesForGroup(group string) []string {
	switch group {
	case "PLANNING":
		return []string{"PLANNED", "INVITED"}
	case "EXECUTION":
		return []string{"IN_PROGRESS", "SUBMITTED", "ANALYZING", "RECAPTURE_PENDING"}
	case "COMPLETED":
		return []string{"COMPLETED"}
	case "CLOSED":
		return []string{"CANCELED", "INVALIDATED"}
	default:
		return nil
	}
}

func businessUnitScopes(principal requestctx.Principal) []identity.ID {
	if hasInspectionRole(principal.Roles, auth.TenantAdmin) {
		return nil
	}
	ids := make([]identity.ID, 0, len(principal.Scopes))
	for _, scope := range principal.Scopes {
		if scope.Kind == "BUSINESS_UNIT" {
			ids = append(ids, scope.ID)
		}
	}
	return ids
}

func hasInspectionRole(roles []string, wanted string) bool {
	for _, role := range roles {
		if role == wanted {
			return true
		}
	}
	return false
}

func escapeInspectionSearch(search string) string {
	return strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(search)
}

func inspectionListCursor(tx *gorm.DB, tenantID identity.ID, after string, allowedUnits []identity.ID, tenantAdmin bool) (time.Time, identity.ID, error) {
	if at, id, err := pagination.After(after); err == nil {
		return at, id, nil
	}
	legacyID, err := identity.ParseID(after)
	if err != nil {
		return time.Time{}, identity.ID{}, apperror.New(apperror.InvalidInput, "after", "invalid cursor")
	}
	var createdAt time.Time
	q := tx.Table("inspections.inspections").Select("created_at").Where("tenant_id=? AND id=?", tenantID, legacyID)
	if !tenantAdmin {
		q = q.Where("business_unit_id IN ?", allowedUnits)
	}
	if err := q.Take(&createdAt).Error; err != nil {
		return time.Time{}, identity.ID{}, apperror.New(apperror.InvalidInput, "after", "invalid cursor")
	}
	return createdAt, legacyID, nil
}

func ValidateInspectionTransition(current, target string, evidenceCount int, reason string) error {
	if target == "CANCELED" {
		if evidenceCount > 0 || current == "SUBMITTED" || current == "ANALYZING" || current == "RECAPTURE_PENDING" || current == "COMPLETED" {
			return apperror.New(apperror.InvalidState, "status", "submitted evidence requires invalidation")
		}
		if current == "CANCELED" || current == "INVALIDATED" {
			return apperror.New(apperror.InvalidState, "status", "inspection is terminal")
		}
		return nil
	}
	if target == "INVALIDATED" {
		if evidenceCount == 0 {
			return apperror.New(apperror.InvalidState, "status", "inspection without evidence must be canceled")
		}
		if strings.TrimSpace(reason) == "" {
			return apperror.New(apperror.InvalidInput, "reason", "invalidation reason is required")
		}
		if current == "CANCELED" || current == "INVALIDATED" {
			return apperror.New(apperror.InvalidState, "status", "inspection is terminal")
		}
		return nil
	}
	allowed := map[string]map[string]bool{"PLANNED": {"INVITED": true}, "INVITED": {"IN_PROGRESS": true}, "IN_PROGRESS": {"SUBMITTED": true}, "SUBMITTED": {"ANALYZING": true}, "ANALYZING": {"RECAPTURE_PENDING": true, "COMPLETED": true}, "RECAPTURE_PENDING": {"ANALYZING": true}}
	if !allowed[current][target] {
		return apperror.New(apperror.InvalidState, "status", "invalid inspection transition")
	}
	return nil
}

func NormalizeReminders(values []time.Time, dueAt, deadlineAt time.Time) ([]time.Time, error) {
	if len(values) > 3 {
		return nil, apperror.New(apperror.InvalidInput, "reminders", "at most three reminders are allowed")
	}
	seen := map[int64]struct{}{}
	out := make([]time.Time, 0, len(values))
	for _, value := range values {
		value = value.UTC()
		if value.IsZero() || (!dueAt.IsZero() && value.Before(dueAt)) || value.After(deadlineAt) {
			return nil, apperror.New(apperror.InvalidInput, "reminders", "reminder must fall between invitation and deadline")
		}
		if _, exists := seen[value.UnixNano()]; exists {
			continue
		}
		seen[value.UnixNano()] = struct{}{}
		out = append(out, value)
	}
	return out, nil
}

func (s Service) load(tx *gorm.DB, out *View) error {
	if err := tx.Where("tenant_id=? AND inspection_id=?", out.Inspection.TenantID, out.Inspection.ID).First(&out.Responsibility).Error; err != nil {
		return err
	}
	if err := tx.Where("tenant_id=? AND inspection_id=?", out.Inspection.TenantID, out.Inspection.ID).First(&out.Policy).Error; err != nil {
		return err
	}
	return tx.Where("tenant_id=? AND inspection_id=?", out.Inspection.TenantID, out.Inspection.ID).First(&out.Reference).Error
}

func (s Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func requiresReference(mode catalog.ComparisonMode) bool {
	return mode == catalog.FixedOrigin || mode == catalog.PlannedStage || mode == catalog.BeforeAfter
}

func snapshotPolicy(document catalog.TemplateDocument, asset assetcore.View, mode catalog.ComparisonMode, referenceID *identity.ID) ([]byte, string, error) {
	var overrides catalog.PolicyOverride
	if len(asset.Asset.PolicyOverrides) != 0 {
		if err := json.Unmarshal(asset.Asset.PolicyOverrides, &overrides); err != nil {
			return nil, "", fmt.Errorf("decode asset policy: %w", err)
		}
	}
	reference := ""
	if referenceID != nil {
		reference = referenceID.String()
	}
	effective, err := catalog.ResolvePolicy(document.Policy, overrides, mode, reference)
	if err != nil {
		return nil, "", apperror.New(apperror.InvalidState, "policy", "effective capture policy is invalid")
	}
	payload, err := json.Marshal(map[string]any{
		"gpsRequired": effective.GPSRequired, "geofenceMeters": effective.GeofenceMeters,
		"allowGallery": effective.AllowGallery, "comparisonMode": effective.ComparisonMode,
		"referenceId":     effective.ReferenceID,
		"assetLatitudeE6": asset.Asset.LatitudeE6, "assetLongitudeE6": asset.Asset.LongitudeE6,
	})
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(payload)
	return payload, hex.EncodeToString(digest[:]), nil
}

func createdEventID(inspectionID identity.ID) identity.ID {
	return uuid.NewSHA1(inspectionID, []byte("inspection.created.v1"))
}

func createOutbox(tx *gorm.DB, eventID, tenantID, aggregateID identity.ID, eventType string, payload any, ctx context.Context, now time.Time) error {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	metadata, _ := requestctx.FromContext(ctx)
	correlationID := metadata.CorrelationID
	if correlationID == "" {
		correlationID = eventID.String()
	}
	envelope, err := json.Marshal(map[string]any{"id": eventID, "type": eventType, "schemaVersion": 1, "occurredAt": now, "tenantId": tenantID, "aggregateId": aggregateID, "correlationId": correlationID, "causationId": metadata.CausationID, "payload": json.RawMessage(payloadBytes)})
	if err != nil {
		return err
	}
	return tx.Create(&database.OutboxIntent{ID: eventID, TenantID: tenantID, Type: eventType, SchemaVersion: 1, Payload: envelope, CorrelationID: correlationID, CausationID: metadata.CausationID, Status: "PENDING", NextAttemptAt: now, CreatedAt: now}).Error
}
