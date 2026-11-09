// Package core owns immutable occurrence snapshots and inspection lifecycle rules.
package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"inspection/libs/identity"
	analysisprompt "inspection/services/inspection/internal/features/analysis/prompt"
	resolveprompt "inspection/services/inspection/internal/features/analysis/resolve_prompt"
	assetcore "inspection/services/inspection/internal/features/assets/core"
	assetget "inspection/services/inspection/internal/features/assets/get_asset"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	inspectionscope "inspection/services/inspection/internal/features/inspections/scope"
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
	"gorm.io/gorm/clause"
)

const (
	StatusPlanned            = "PLANNED"
	StatusInvited            = "INVITED"
	StatusInProgress         = "IN_PROGRESS"
	StatusSubmitted          = "SUBMITTED"
	StatusAnalyzing          = "ANALYZING"
	StatusRecapturePending   = "RECAPTURE_PENDING"
	StatusReviewRequired     = "REVIEW_REQUIRED"
	StatusProcessingFailed   = "PROCESSING_FAILED"
	StatusReplacementPending = "REPLACEMENT_PENDING"
	StatusCompleted          = "COMPLETED"
	StatusCanceled           = "CANCELED"
	StatusInvalidated        = "INVALIDATED"
)

const (
	SourceScheduled          = "SCHEDULED"
	SourceMilestone          = "MILESTONE"
	SourceManual             = "MANUAL"
	resolvepromptTaskCatalog = "INVENTORY_CATALOG"
	resolvepromptTaskCompare = "INVENTORY_COMPARE"
)

type CreateInput struct {
	TenantID, AssetID, ParticipantID identity.ID
	TemplateID                       *identity.ID
	TemplateVersionID                *identity.ID
	ComparisonMode                   catalog.ComparisonMode
	ReferenceVersionID               *identity.ID
	ReviewerMembershipID             *identity.ID
	ProjectID, StageID               *identity.ID
	Source, SourceKey, Reason        string
	DueAt, DeadlineAt                time.Time
	ReminderInstants                 []time.Time
	ScopeOverride                    *inspectionscope.Override
}

type View struct {
	Inspection             database.Inspection
	Scope                  database.InspectionScopeSnapshot
	Responsibility         database.Responsibility
	Policy                 database.PolicySnapshot
	Reference              database.ReferenceSnapshot
	ReferenceVersion       *database.OriginVersion
	ReferenceIsActive      bool
	OriginPromotionStatus  string
	OriginPromotionFailure string
	PromotedOriginVersion  *database.OriginVersion
	PromotedOriginIsActive bool
	EventID                identity.ID
	AssetName              *string
	AssetAddress           *string
	AssetExternalKey       *string
	ParticipantName        *string
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
	if err := validateComparisonMode(document, comparisonMode); err != nil {
		return View{}, err
	}
	if comparisonMode == catalog.FixedOrigin && document.MediaKind() == "PHOTO" && document.SchemaVersion >= catalog.DescriptiveSchemaVersion && document.AssetProfile == "" {
		return View{}, apperror.New(apperror.InvalidInput, "templateVersionId", "O modelo precisa definir um perfil de escopo válido.")
	}
	if !strings.EqualFold(document.AnalysisType, "REAL_ESTATE") {
		return View{}, apperror.New(apperror.InvalidInput, "analysisType", "unknown analysis type")
	}
	if in.Source != SourceScheduled {
		roles := []string{auth.TenantAdmin, auth.Manager, auth.Employee}
		if _, err := s.Authorizer.Authorize(ctx, auth.AuthorizationRequest{TenantID: in.TenantID, Product: auth.DashboardProduct, Roles: roles, Mutate: true}); err != nil {
			return View{}, err
		}
		if _, err := s.Authorizer.Authorize(ctx, auth.AuthorizationRequest{TenantID: in.TenantID, Roles: roles, Resource: &requestctx.Scope{Kind: "BUSINESS_UNIT", ID: asset.Asset.BusinessUnitID}, Mutate: true, NewWork: true}); err != nil {
			if apperror.Is(err, apperror.Forbidden) {
				return View{}, apperror.New(apperror.NotFound, "", "resource not found")
			}
			return View{}, err
		}
		if _, err := s.Authorizer.Authorize(ctx, auth.AuthorizationRequest{TenantID: in.TenantID, Roles: roles, Resource: &requestctx.Scope{Kind: "BUSINESS_UNIT", ID: participant.Participant.BusinessUnitID}, Mutate: true, NewWork: true}); err != nil {
			if apperror.Is(err, apperror.Forbidden) {
				return View{}, apperror.New(apperror.NotFound, "", "resource not found")
			}
			return View{}, err
		}
	}
	if in.ReviewerMembershipID != nil {
		if err := validateReviewerMembership(ctx, s.DB, in.TenantID, *in.ReviewerMembershipID, asset.Asset.BusinessUnitID, in.AssetID); err != nil {
			return View{}, err
		}
	}
	var originReference *originresolve.Result
	explicitReference := in.ReferenceVersionID != nil
	if comparisonMode == catalog.FixedOrigin {
		referenceRaw, err := s.Bus.Ask(ctx, originresolve.Query{TenantID: in.TenantID, AssetID: in.AssetID, VersionID: in.ReferenceVersionID, MediaKind: document.MediaKind()})
		if err != nil {
			if document.MediaKind() == "PHOTO" {
				var appErr *apperror.Error
				if errors.As(err, &appErr) && appErr.Field == "referenceVersionId" {
					if appErr.Code == apperror.Conflict {
						return View{}, apperror.New(apperror.Conflict, "referenceVersionId", "A versão da origem foi alterada. Selecione novamente uma versão aprovada.")
					}
					return View{}, apperror.New(apperror.InvalidState, "referenceVersionId", "A origem precisa ser catalogada e aprovada antes de iniciar uma vistoria de saída.")
				}
			}
			return View{}, err
		}
		resolved := referenceRaw.(originresolve.Result)
		if document.MediaKind() == "PHOTO" {
			if !resolved.Version.HasInventory {
				return View{}, apperror.New(apperror.InvalidState, "referenceVersionId", "A origem precisa ser catalogada e aprovada antes de iniciar uma vistoria de saída.")
			}
			if resolved.Version.MediaKind == "VIDEO" {
				return View{}, apperror.New(apperror.InvalidState, "referenceVersionId", "A vistoria de saída exige uma referência fotográfica aprovada.")
			}
			if resolved.Version.Status != "ACTIVE" && !(explicitReference && resolved.Version.Status == "SUPERSEDED") {
				return View{}, apperror.New(apperror.Conflict, "referenceVersionId", "A versão da origem foi alterada. Selecione novamente a versão aprovada.")
			}
		}
		if document.SchemaVersion < catalog.EnvironmentSchemaVersion && resolved.Version.HasInventory {
			return View{}, apperror.New(apperror.InvalidState, "referenceVersionId", "Cadastre e aprove o inventário da origem antes de iniciar uma vistoria de saída.")
		}
		originReference = &resolved
		referenceID := resolved.Version.ID
		in.ReferenceVersionID = &referenceID
	} else if requiresReference(comparisonMode) && in.ReferenceVersionID == nil {
		return View{}, apperror.New(apperror.InvalidState, "referenceVersionId", "effective reference is required")
	}
	policyPayload, policyDigest, err := snapshotPolicy(document, asset, comparisonMode, in.ReferenceVersionID)
	if err != nil {
		return View{}, err
	}
	effectiveScope, err := inspectionscope.Resolve(document, template.Version.ID.String(), in.ScopeOverride)
	if err != nil {
		return View{}, apperror.New(apperror.InvalidInput, "scope", err.Error())
	}
	scopeDocument, err := json.Marshal(effectiveScope)
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
		if originReference != nil && document.MediaKind() == "PHOTO" {
			var origin database.Origin
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=? AND asset_id=?", in.TenantID, originReference.Version.OriginID, in.AssetID).Take(&origin).Error; err != nil {
				return apperror.New(apperror.InvalidInput, "referenceVersionId", "A versão aprovada precisa pertencer ao mesmo imóvel.")
			}
			var pinned database.OriginVersion
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=?", in.TenantID, originReference.Version.ID).Take(&pinned).Error; err != nil {
				if err == gorm.ErrRecordNotFound {
					return apperror.New(apperror.Conflict, "referenceVersionId", "A versão da origem não está mais aprovada. Selecione novamente uma versão aprovada.")
				}
				return err
			}
			if !pinned.HasInventory || pinned.Status == "DRAFT" || pinned.Status == "REJECTED" || pinned.Status == "DISCARDED" {
				return apperror.New(apperror.InvalidState, "referenceVersionId", "A origem precisa ser catalogada e aprovada antes de iniciar uma vistoria de saída.")
			}
			if pinned.MediaKind == "VIDEO" || (pinned.Status != "ACTIVE" && !(explicitReference && pinned.Status == "SUPERSEDED")) {
				return apperror.New(apperror.Conflict, "referenceVersionId", "A versão da origem foi alterada. Selecione novamente uma versão aprovada.")
			}
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
		currentFindings, err := resolveprompt.ResolveTask(ctx, tx, document.AnalysisType, string(analysisprompt.FindingsCurrent), now)
		if err != nil {
			return err
		}
		compareFindings, err := resolveprompt.ResolveTask(ctx, tx, document.AnalysisType, string(analysisprompt.FindingsCompare), now)
		if err != nil {
			return err
		}
		var inventoryPromptID *identity.ID
		if document.SchemaVersion >= catalog.EnvironmentSchemaVersion {
			task := inventoryPromptTask(originReference)
			inventoryPrompt, err := resolveprompt.ResolveTask(ctx, tx, document.AnalysisType, task, now)
			if err != nil {
				return err
			}
			id := inventoryPrompt.Snapshot.ID
			inventoryPromptID = &id
		}
		contextSnapshot, _ := json.Marshal(map[string]any{
			"tenantId": in.TenantID, "businessUnitId": asset.Asset.BusinessUnitID,
			"assetId": asset.Asset.ID, "assetVersion": asset.Asset.Version,
			"participantId": participant.Participant.ID, "participantVersion": participant.Participant.Version,
			"templateId": *templateID, "templateVersionId": template.Version.ID,
			"analysisType": document.AnalysisType, "analysisPromptSnapshotId": resolvedPrompt.Snapshot.ID, "source": in.Source,
		})
		currentSnapshotID, compareSnapshotID := currentFindings.Snapshot.ID, compareFindings.Snapshot.ID
		inspection := database.Inspection{ID: identity.NewID(), TenantID: in.TenantID, BusinessUnitID: asset.Asset.BusinessUnitID, AssetID: in.AssetID, ParticipantID: in.ParticipantID, TemplateID: *templateID, TemplateVersionID: template.Version.ID, AnalysisPromptSnapshotID: resolvedPrompt.Snapshot.ID, FindingsCurrentSnapshotID: &currentSnapshotID, FindingsCompareSnapshotID: &compareSnapshotID, InventoryPromptSnapshotID: inventoryPromptID, ProjectID: in.ProjectID, StageID: in.StageID, Source: in.Source, SourceKey: in.SourceKey, SourceReason: strings.TrimSpace(in.Reason), Status: "PLANNED", MediaKind: document.MediaKind(), DueAt: in.DueAt.UTC(), DeadlineAt: in.DeadlineAt.UTC(), ReminderInstants: reminderJSON, ContextSnapshot: contextSnapshot, Version: 1, CreatedAt: now, UpdatedAt: now}
		responsibility := database.Responsibility{ID: identity.NewID(), TenantID: in.TenantID, InspectionID: inspection.ID, ParticipantID: in.ParticipantID, Status: "PENDING", Version: 1, CreatedAt: now, UpdatedAt: now}
		policy := database.PolicySnapshot{ID: identity.NewID(), TenantID: in.TenantID, InspectionID: inspection.ID, SchemaVersion: 1, Payload: policyPayload, CanonicalDigest: policyDigest, CreatedAt: now}
		referenceDocument := map[string]any{"referenceVersionId": in.ReferenceVersionID, "comparisonMode": comparisonMode}
		if originReference != nil {
			referenceDocument["originVersionId"] = originReference.Version.ID
			if originReference.Version.MediaKind == "VIDEO" {
				referenceDocument["mediaKind"] = "VIDEO"
				referenceDocument["scope"] = originReference.VideoScope
			} else {
				referenceDocument["mediaKind"] = "PHOTO"
				referenceDocument["items"] = originReference.Evidence
			}
		}
		referencePayload, _ := json.Marshal(referenceDocument)
		reference := database.ReferenceSnapshot{ID: identity.NewID(), TenantID: in.TenantID, InspectionID: inspection.ID, ReferenceVersionID: in.ReferenceVersionID, ComparisonMode: string(comparisonMode), Payload: referencePayload, CreatedAt: now}
		scopeSnapshot := database.InspectionScopeSnapshot{ID: identity.NewID(), TenantID: in.TenantID, InspectionID: inspection.ID, TemplateVersionID: template.Version.ID, Profile: effectiveScope.Profile, Document: scopeDocument, Digest: effectiveScope.Digest, Version: effectiveScope.Version, CreatedAt: now, UpdatedAt: now}
		requirements := CaptureRequirementsForCreation(document, comparisonMode, originReference)
		if document.SchemaVersion >= catalog.DescriptiveSchemaVersion {
			limits := document.EnvironmentCapture
			if limits == nil {
				limits = catalog.DefaultEnvironmentCapture()
			}
			var baselineKeys map[string]string
			if originReference != nil {
				baselineKeys = originReference.EnvironmentRequirementKeys
			}
			requirements = descriptiveEnvironmentRequirements(requirements, effectiveScope.Rooms, limits, comparisonMode, originReference != nil, baselineKeys)
		}
		requirementPayload, _ := json.Marshal(requirements)
		draft := database.CaptureDraft{ID: identity.NewID(), TenantID: in.TenantID, ResponsibilityID: responsibility.ID, Kind: "INSPECTION", MediaKind: document.MediaKind(), TemplateVersionID: template.Version.ID, ReferencePayload: referencePayload, PolicyPayload: policyPayload, Requirements: requirementPayload, Status: "OPEN", Version: 1, CreatedAt: now, UpdatedAt: now}
		for _, row := range []any{&inspection, &responsibility, &policy, &reference, &scopeSnapshot, &draft} {
			if err := tx.Create(row).Error; err != nil {
				return err
			}
		}
		eventID := createdEventID(inspection.ID)
		payload := map[string]any{"inspectionId": inspection.ID, "responsibilityId": responsibility.ID, "assetId": inspection.AssetID, "participantId": inspection.ParticipantID, "templateVersionId": inspection.TemplateVersionID, "source": inspection.Source, "dueAt": in.DueAt, "deadlineAt": inspection.DeadlineAt}
		if in.ReviewerMembershipID != nil {
			payload["reviewerMembershipId"] = *in.ReviewerMembershipID
		}
		if err := createOutbox(tx, eventID, in.TenantID, inspection.ID, "inspection.created.v1", payload, ctx, now); err != nil {
			return err
		}
		out = View{Inspection: inspection, Responsibility: responsibility, Policy: policy, Reference: reference, Scope: scopeSnapshot, EventID: eventID}
		return nil
	})
	if err != nil {
		return View{}, err
	}
	return out, nil
}

// descriptiveEnvironmentRequirements maps the pinned scope rooms of a
// descriptive (v3) inspection to capture requirements. A room is linked to a
// baseline environment first by the key the environment was captured with
// ("env:<room key>") or by its identity, then by name and type only when that
// match is unique in both directions. Each baseline environment backs at most
// one room: an ambiguous or missing match leaves the room without baseline
// link and the baseline environment unmatched, never two rooms reading one
// baseline environment.
func descriptiveEnvironmentRequirements(requirements []capturecore.Requirement, rooms []catalog.ExpectedRoom, limits *catalog.EnvironmentCapture, comparisonMode catalog.ComparisonMode, matchBaseline bool, baselineKeys map[string]string) []capturecore.Requirement {
	matched := matchBaselineRooms(requirements, rooms, matchBaseline, baselineKeys)
	mapped := make([]capturecore.Requirement, 0, len(rooms))
	for index, room := range rooms {
		if baseline := matched[index]; baseline >= 0 {
			current := requirements[baseline]
			current.Key = "env:" + room.Key
			current.EnvironmentTypeCode, current.EnvironmentName = room.TypeCode, room.Name
			current.AllowConfirmedGap = true
			mapped = append(mapped, current)
			continue
		}
		mapped = append(mapped, capturecore.Requirement{Key: "env:" + room.Key, Section: room.TypeCode, Label: room.Name, EvidenceKind: "PHOTO", Required: true, Expected: true, AllowConfirmedGap: true, ImpossibilityAllowed: true, MinimumMedia: limits.Photo.Minimum, MaximumMedia: limits.Photo.Maximum, CaptureSourcePolicy: "CAMERA_DEFAULT", ComparisonTarget: string(comparisonMode), EnvironmentIdentityID: room.Key, EnvironmentTypeCode: room.TypeCode, EnvironmentName: room.Name})
	}
	return mapped
}

// matchBaselineRooms returns, per scope room, the index of the baseline
// requirement it is linked to, or -1. Every baseline requirement is used at
// most once.
func matchBaselineRooms(requirements []capturecore.Requirement, rooms []catalog.ExpectedRoom, matchBaseline bool, baselineKeys map[string]string) []int {
	matched := make([]int, len(rooms))
	for index := range matched {
		matched[index] = -1
	}
	if !matchBaseline {
		return matched
	}
	used := make([]bool, len(requirements))
	for index, room := range rooms {
		for candidate := range requirements {
			req := requirements[candidate]
			if used[candidate] || !strings.HasPrefix(req.Key, "env:") {
				continue
			}
			captured := baselineKeys[req.EnvironmentIdentityID]
			if captured == "env:"+room.Key || req.EnvironmentIdentityID == room.Key || strings.TrimPrefix(req.Key, "env:") == room.Key {
				matched[index], used[candidate] = candidate, true
				break
			}
		}
	}
	sameRoom := func(req capturecore.Requirement, room catalog.ExpectedRoom) bool {
		return strings.EqualFold(strings.TrimSpace(req.EnvironmentTypeCode), strings.TrimSpace(room.TypeCode)) && strings.EqualFold(strings.TrimSpace(req.EnvironmentName), strings.TrimSpace(room.Name))
	}
	for index, room := range rooms {
		if matched[index] >= 0 {
			continue
		}
		found := -1
		for candidate, req := range requirements {
			if used[candidate] || !strings.HasPrefix(req.Key, "env:") || !sameRoom(req, room) {
				continue
			}
			if found >= 0 {
				found = -2
				break
			}
			found = candidate
		}
		if found < 0 {
			continue
		}
		rivals := 0
		for other, otherRoom := range rooms {
			if matched[other] < 0 && sameRoom(requirements[found], otherRoom) {
				rivals++
			}
		}
		if rivals == 1 {
			matched[index], used[found] = found, true
		}
	}
	return matched
}

// ConfirmScope updates the inspection's effective scope with optimistic concurrency.
func (s Service) ConfirmScope(ctx context.Context, tenantID, inspectionID identity.ID, override inspectionscope.Override, expectedVersion int) (database.InspectionScopeSnapshot, error) {
	if tenantID == (identity.ID{}) || inspectionID == (identity.ID{}) || expectedVersion < 1 {
		return database.InspectionScopeSnapshot{}, apperror.New(apperror.InvalidInput, "expectedVersion", "A versão esperada do escopo é obrigatória.")
	}
	var out database.InspectionScopeSnapshot
	err := (tenanttx.Runner{DB: s.DB}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		var inspection database.Inspection
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=?", tenantID, inspectionID).Take(&inspection).Error; err != nil {
			return apperror.New(apperror.NotFound, "inspectionId", "vistoria não encontrada")
		}
		roles := []string{auth.TenantAdmin, auth.Manager, auth.Employee}
		if _, err := s.Authorizer.Authorize(ctx, auth.AuthorizationRequest{TenantID: tenantID, Product: auth.DashboardProduct, Roles: roles, Mutate: true}); err != nil {
			return err
		}
		if _, err := s.Authorizer.Authorize(ctx, auth.AuthorizationRequest{TenantID: tenantID, Roles: roles, Resource: &requestctx.Scope{Kind: "BUSINESS_UNIT", ID: inspection.BusinessUnitID}, Mutate: true, NewWork: true}); err != nil {
			return err
		}
		var responsibility database.Responsibility
		if err := tx.Where("tenant_id=? AND inspection_id=?", tenantID, inspectionID).Take(&responsibility).Error; err != nil {
			return err
		}
		if inspection.EvidenceCount > 0 || (inspection.Status != StatusPlanned && inspection.Status != StatusInvited) || responsibility.Status != "PENDING" {
			return apperror.New(apperror.InvalidState, "scope", "O escopo não pode ser alterado depois do início da captura.")
		}
		// Capture flows may persist activity without updating inspection status or
		// evidence counters, so inspect the durable records under the row lock.
		for _, check := range []struct {
			model any
			query string
			args  []any
		}{
			{&database.ProcessingAcceptance{}, "tenant_id=? AND responsibility_id=?", []any{tenantID, responsibility.ID}},
			{&database.MediaObject{}, "tenant_id=? AND responsibility_id=?", []any{tenantID, responsibility.ID}},
			{&database.RequirementAnswer{}, "tenant_id=? AND draft_id IN (SELECT id FROM capture.capture_drafts WHERE tenant_id=? AND responsibility_id=?)", []any{tenantID, tenantID, responsibility.ID}},
			{&database.ComponentAnswer{}, "tenant_id=? AND draft_id IN (SELECT id FROM capture.capture_drafts WHERE tenant_id=? AND responsibility_id=?)", []any{tenantID, tenantID, responsibility.ID}},
			{&database.HandoverRecord{}, "tenant_id=? AND draft_id IN (SELECT id FROM capture.capture_drafts WHERE tenant_id=? AND responsibility_id=?)", []any{tenantID, tenantID, responsibility.ID}},
			{&database.SubmissionVersion{}, "tenant_id=? AND responsibility_id=?", []any{tenantID, responsibility.ID}},
		} {
			var count int64
			if err := tx.Model(check.model).Where(check.query, check.args...).Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				return apperror.New(apperror.InvalidState, "scope", "O escopo não pode ser alterado depois do início da captura.")
			}
		}
		var template database.TemplateVersion
		if err := tx.Where("tenant_id=? AND id=?", tenantID, inspection.TemplateVersionID).Take(&template).Error; err != nil {
			return err
		}
		var document catalog.TemplateDocument
		if err := json.Unmarshal(template.DefinitionJSON, &document); err != nil {
			return fmt.Errorf("decode template scope: %w", err)
		}
		resolved, err := inspectionscope.Resolve(document, template.ID.String(), &override)
		if err != nil {
			return apperror.New(apperror.InvalidInput, "scopeOverride", err.Error())
		}
		if err := tx.Where("tenant_id=? AND inspection_id=?", tenantID, inspectionID).Take(&out).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return apperror.New(apperror.InvalidState, "scope", "O escopo desta vistoria não está disponível para revisão.")
			}
			return err
		}
		if out.Version != expectedVersion {
			if out.Digest == resolved.Digest {
				return nil
			}
			return apperror.New(apperror.Conflict, "expectedVersion", "O escopo foi alterado por outra pessoa. Atualize a vistoria e tente novamente.")
		}
		resolved.Version = expectedVersion + 1
		payload, err := json.Marshal(resolved)
		if err != nil {
			return err
		}
		if document.SchemaVersion >= catalog.DescriptiveSchemaVersion {
			if err := refreshDescriptiveCaptureRequirements(ctx, tx, tenantID, inspectionID, responsibility.ID, resolved.Rooms, document, s.now()); err != nil {
				return err
			}
		}
		result := tx.Model(&database.InspectionScopeSnapshot{}).
			Where("tenant_id=? AND inspection_id=? AND version=?", tenantID, inspectionID, expectedVersion).
			Updates(map[string]any{"profile": resolved.Profile, "document": payload, "digest": resolved.Digest, "version": resolved.Version, "confirmed_by": requestIdentity(ctx), "updated_at": s.now()})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return apperror.New(apperror.Conflict, "expectedVersion", "O escopo foi alterado por outra pessoa. Atualize a vistoria e tente novamente.")
		}
		out.Profile, out.Document, out.Digest, out.Version = resolved.Profile, payload, resolved.Digest, resolved.Version
		return nil
	})
	return out, err
}

// refreshDescriptiveCaptureRequirements keeps the participant checklist in
// sync with a scope confirmed before capture begins. Non-environment
// requirements are retained while environment requirements are rebuilt from
// the newly confirmed rooms.
func refreshDescriptiveCaptureRequirements(ctx context.Context, tx *gorm.DB, tenantID, inspectionID, responsibilityID identity.ID, rooms []catalog.ExpectedRoom, document catalog.TemplateDocument, now time.Time) error {
	var draft database.CaptureDraft
	if err := tx.WithContext(ctx).Where("tenant_id=? AND responsibility_id=?", tenantID, responsibilityID).Take(&draft).Error; err != nil {
		return err
	}
	var requirements []capturecore.Requirement
	if err := json.Unmarshal(draft.Requirements, &requirements); err != nil {
		return fmt.Errorf("decode capture requirements: %w", err)
	}
	environments := make([]capturecore.Requirement, 0, len(requirements))
	retained := make([]capturecore.Requirement, 0, len(requirements))
	for _, requirement := range requirements {
		if strings.HasPrefix(requirement.Key, "env:") {
			environments = append(environments, requirement)
		} else {
			retained = append(retained, requirement)
		}
	}
	var reference database.ReferenceSnapshot
	if err := tx.WithContext(ctx).Where("tenant_id=? AND inspection_id=?", tenantID, inspectionID).Take(&reference).Error; err != nil {
		return err
	}
	var referencePayload struct {
		OriginVersionID string `json:"originVersionId"`
		MediaKind       string `json:"mediaKind"`
	}
	if err := json.Unmarshal(reference.Payload, &referencePayload); err != nil {
		return fmt.Errorf("decode reference snapshot: %w", err)
	}
	environmentsRequirements := environments
	baselineKeys := map[string]string{}
	if referencePayload.OriginVersionID != "" {
		originVersionID, err := identity.ParseID(referencePayload.OriginVersionID)
		if err != nil {
			return fmt.Errorf("decode pinned origin version: %w", err)
		}
		var originEnvironments []database.InventoryEnvironment
		if err := tx.WithContext(ctx).Where("tenant_id=? AND origin_version_id=? AND decision<>'REJECTED'", tenantID, originVersionID).Order("position,id").Find(&originEnvironments).Error; err != nil {
			return err
		}
		mediaKind := "PHOTO"
		maximumMedia := 20
		if referencePayload.MediaKind == "VIDEO" {
			mediaKind, maximumMedia = "VIDEO", 3
		}
		environmentsRequirements = make([]capturecore.Requirement, 0, len(originEnvironments))
		for _, environment := range originEnvironments {
			if environment.RequirementKey != nil && *environment.RequirementKey != "" {
				baselineKeys[environment.IdentityID.String()] = *environment.RequirementKey
			}
			requirement := capturecore.Requirement{
				Key: "env:" + environment.IdentityID.String(), Section: environment.TypeCode, Label: environment.Name,
				EnvironmentIdentityID: environment.IdentityID.String(), EnvironmentTypeCode: environment.TypeCode, EnvironmentName: environment.Name,
				Expected: true, EvidenceKind: mediaKind, Required: true, ImpossibilityAllowed: true, MinimumMedia: 1, MaximumMedia: maximumMedia,
				CaptureSourcePolicy: "CAMERA_DEFAULT", ComparisonTarget: string(catalog.FixedOrigin),
			}
			environmentsRequirements = append(environmentsRequirements, requirement)
		}
	}
	limits := document.EnvironmentCapture
	if limits == nil {
		limits = catalog.DefaultEnvironmentCapture()
	}
	comparisonMode := catalog.ComparisonMode(reference.ComparisonMode)
	var originReference *originresolve.Result
	if comparisonMode == catalog.FixedOrigin {
		if referencePayload.OriginVersionID == "" {
			return apperror.New(apperror.InvalidState, "referenceVersionId", "A versão da origem fixada não está disponível. Atualize a vistoria e tente novamente.")
		}
		originVersionID, err := identity.ParseID(referencePayload.OriginVersionID)
		if err != nil {
			return apperror.New(apperror.InvalidState, "referenceVersionId", "A versão da origem fixada não está disponível. Atualize a vistoria e tente novamente.")
		}
		var version database.OriginVersion
		if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", tenantID, originVersionID).Take(&version).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return apperror.New(apperror.InvalidState, "referenceVersionId", "A versão da origem fixada não está disponível. Atualize a vistoria e tente novamente.")
			}
			return err
		}
		if !version.HasInventory || len(environmentsRequirements) == 0 {
			return apperror.New(apperror.InvalidState, "referenceVersionId", "A versão da origem fixada não está disponível. Atualize a vistoria e tente novamente.")
		}
		originReference = &originresolve.Result{Version: version, Requirements: environmentsRequirements, EnvironmentRequirementKeys: baselineKeys}
	}
	environmentsRequirements = CaptureRequirementsForCreation(document, comparisonMode, originReference)
	environmentsRequirements = descriptiveEnvironmentRequirements(environmentsRequirements, rooms, limits, comparisonMode, comparisonMode == catalog.FixedOrigin, baselineKeys)
	requirements = append(retained, environmentsRequirements...)
	payload, err := json.Marshal(requirements)
	if err != nil {
		return err
	}
	return tx.WithContext(ctx).Model(&database.CaptureDraft{}).Where("tenant_id=? AND id=?", tenantID, draft.ID).Updates(map[string]any{"requirements": payload, "updated_at": now.UTC()}).Error
}

func requestIdentity(ctx context.Context) *identity.ID {
	meta, ok := requestctx.FromContext(ctx)
	if !ok || meta.Principal.IdentityID == (identity.ID{}) {
		return nil
	}
	id := meta.Principal.IdentityID
	return &id
}

func inventoryPromptTask(reference *originresolve.Result) string {
	if reference != nil && reference.Version.HasInventory {
		return string(resolvepromptTaskCompare)
	}
	return string(resolvepromptTaskCatalog)
}

func validateReviewerMembership(ctx context.Context, db *gorm.DB, tenantID, membershipID, businessUnitID, assetID identity.ID) error {
	if db == nil {
		return apperror.New(apperror.DependencyUnavailable, "reviewerMembershipId", "reviewer validation unavailable")
	}
	return (tenanttx.Runner{DB: db}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		allowed := []requestctx.Scope{{Kind: "BUSINESS_UNIT", ID: businessUnitID}, {Kind: "ASSET", ID: assetID}}
		matches, err := auth.ReviewerMembershipMatchesScopes(ctx, tx, tenantID, membershipID, allowed)
		if err != nil {
			return err
		}
		if !matches {
			return apperror.New(apperror.InvalidInput, "reviewerMembershipId", "reviewer membership is unavailable or outside the asset scope")
		}
		return nil
	})
}

// CaptureRequirementsForCreation derives the initial capture requirements from
// the pinned template document and optional origin reference.
func CaptureRequirementsForCreation(document catalog.TemplateDocument, comparisonMode catalog.ComparisonMode, originReference *originresolve.Result) []capturecore.Requirement {
	requirements := make([]capturecore.Requirement, 0, len(document.Requirements))
	if originReference != nil {
		requirements = append(requirements, originReference.Requirements...)
		if document.SchemaVersion >= catalog.EnvironmentSchemaVersion {
			limits := document.EnvironmentCapture
			if limits == nil {
				limits = catalog.DefaultEnvironmentCapture()
			}
			for index := range requirements {
				if requirements[index].EvidenceKind == "VIDEO" {
					requirements[index].MinimumMedia = 1
					requirements[index].MaximumMedia = limits.Video.MaximumClips
				} else {
					requirements[index].MinimumMedia = limits.Photo.Minimum
					requirements[index].MaximumMedia = limits.Photo.Maximum
				}
			}
		}
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
	if document.SchemaVersion >= catalog.EnvironmentSchemaVersion {
		if originReference == nil || !originReference.Version.HasInventory {
			return nil
		}
		return requirements
	}
	return inspectionCaptureRequirements(document.MediaKind(), string(comparisonMode), requirements)
}

func inspectionCaptureRequirements(mediaKind, comparisonTarget string, requirements []capturecore.Requirement) []capturecore.Requirement {
	if len(requirements) > 0 && strings.HasPrefix(requirements[0].Key, "env:") {
		return requirements
	}
	if mediaKind == "VIDEO" {
		return []capturecore.Requirement{capturecore.VideoMainRequirement(comparisonTarget)}
	}
	return requirements
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
	if _, err := s.Authorizer.Authorize(ctx, auth.AuthorizationRequest{TenantID: tenantID, Product: auth.DashboardProduct, Roles: []string{auth.TenantAdmin, auth.Manager, auth.Employee}, Resource: &requestctx.Scope{Kind: "BUSINESS_UNIT", ID: current.Inspection.BusinessUnitID}, Mutate: true}); err != nil {
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
		if err := createOutbox(tx, identity.NewID(), tenantID, inspectionID, "inspection.state_changed.v1", map[string]any{"inspectionId": inspectionID, "responsibilityId": current.Responsibility.ID, "from": current.Inspection.Status, "to": target, "reason": strings.TrimSpace(reason), "revokeSessions": true}, ctx, now); err != nil {
			return err
		}
		// The last open inspection of a project stage may end by cancellation or
		// invalidation; the stage then completes (when another one was completed).
		closed := current.Inspection
		closed.Status = target
		metadata, _ := requestctx.FromContext(ctx)
		_, err := projectcore.CompleteStageOfInspection(ctx, tx, closed, now, metadata.CorrelationID)
		return err
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
	if _, err := s.Authorizer.Authorize(ctx, auth.AuthorizationRequest{TenantID: tenantID, Product: auth.DashboardProduct, Roles: []string{auth.TenantAdmin, auth.Manager, auth.Employee, auth.Viewer}, Resource: &requestctx.Scope{Kind: "BUSINESS_UNIT", ID: out.Inspection.BusinessUnitID}}); err != nil {
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
	principal, err := s.Authorizer.Authorize(ctx, auth.AuthorizationRequest{TenantID: in.TenantID, Product: auth.DashboardProduct, Roles: []string{auth.TenantAdmin, auth.Manager, auth.Employee, auth.Viewer}})
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
	case "", "PLANNING", "EXECUTION", "ATTENTION", "COMPLETED", "CLOSED":
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
	case "ATTENTION":
		return []string{StatusReviewRequired, StatusProcessingFailed, StatusReplacementPending}
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
	allowed := map[string]map[string]bool{
		StatusPlanned:            {StatusInvited: true},
		StatusInvited:            {StatusInProgress: true},
		StatusInProgress:         {StatusSubmitted: true},
		StatusSubmitted:          {StatusAnalyzing: true},
		StatusAnalyzing:          {StatusRecapturePending: true, StatusReviewRequired: true, StatusProcessingFailed: true, StatusCompleted: true},
		StatusReviewRequired:     {StatusAnalyzing: true, StatusReplacementPending: true},
		StatusProcessingFailed:   {StatusAnalyzing: true, StatusReplacementPending: true},
		StatusReplacementPending: {StatusAnalyzing: true, StatusReviewRequired: true},
		StatusRecapturePending:   {StatusAnalyzing: true},
	}
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
	if err := tx.Where("tenant_id=? AND inspection_id=?", out.Inspection.TenantID, out.Inspection.ID).First(&out.Scope).Error; err != nil && err != gorm.ErrRecordNotFound {
		return err
	}
	err := tx.Where("tenant_id=? AND inspection_id=?", out.Inspection.TenantID, out.Inspection.ID).First(&out.Reference).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil
		}
		return err
	}
	var origin database.Origin
	if out.Reference.ReferenceVersionID != nil {
		var version database.OriginVersion
		if err := tx.Where("tenant_id=? AND id=?", out.Inspection.TenantID, *out.Reference.ReferenceVersionID).First(&version).Error; err != nil {
			if err != gorm.ErrRecordNotFound {
				return err
			}
		} else {
			out.ReferenceVersion = &version
			if err := tx.Where("tenant_id=? AND id=?", out.Inspection.TenantID, version.OriginID).First(&origin).Error; err == nil {
				out.ReferenceIsActive = origin.ActiveVersionID != nil && *origin.ActiveVersionID == version.ID
			}
		}
	}
	if out.Inspection.MediaKind == "VIDEO" {
		var promoted database.OriginVersion
		if err := tx.Where("tenant_id=? AND idempotency_key=?", out.Inspection.TenantID, "video-promotion:"+out.Inspection.ID.String()).First(&promoted).Error; err == nil {
			out.PromotedOriginVersion = &promoted
			out.OriginPromotionStatus = promoted.Status
			if origin.ID == (identity.ID{}) {
				_ = tx.Where("tenant_id=? AND id=?", out.Inspection.TenantID, promoted.OriginID).First(&origin).Error
			}
			out.PromotedOriginIsActive = origin.ActiveVersionID != nil && *origin.ActiveVersionID == promoted.ID
		}
	}
	var promotion database.OriginPromotion
	if err := tx.Where("tenant_id=? AND inspection_id=?", out.Inspection.TenantID, out.Inspection.ID).First(&promotion).Error; err == nil {
		out.OriginPromotionStatus = promotion.Status
		out.OriginPromotionFailure = promotion.FailureReason
		if promotion.OriginVersionID != nil {
			var promoted database.OriginVersion
			if err := tx.Where("tenant_id=? AND id=?", out.Inspection.TenantID, *promotion.OriginVersionID).First(&promoted).Error; err == nil {
				out.PromotedOriginVersion = &promoted
				if origin.ID == (identity.ID{}) {
					_ = tx.Where("tenant_id=? AND id=?", out.Inspection.TenantID, promoted.OriginID).First(&origin).Error
				}
				out.PromotedOriginIsActive = origin.ActiveVersionID != nil && *origin.ActiveVersionID == promoted.ID
			}
		}
	}
	return nil
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

func validateComparisonMode(document catalog.TemplateDocument, mode catalog.ComparisonMode) error {
	if mode != catalog.ChecklistOnly && mode != catalog.FixedOrigin {
		return apperror.New(apperror.InvalidInput, "comparisonMode", "unsupported comparison mode")
	}
	if document.DefaultComparisonMode == catalog.FixedOrigin && mode == catalog.ChecklistOnly {
		return apperror.New(apperror.InvalidInput, "comparisonMode", "template requires a fixed origin")
	}
	return nil
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
