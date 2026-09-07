// Package core owns project and stage lifecycle rules.
package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"inspection/libs/identity"
	assetcore "inspection/services/inspection/internal/features/assets/core"
	assetget "inspection/services/inspection/internal/features/assets/get_asset"
	inspectioncore "inspection/services/inspection/internal/features/inspections/core"
	createoccurrence "inspection/services/inspection/internal/features/inspections/create_occurrence"
	participantcore "inspection/services/inspection/internal/features/participants/core"
	participantget "inspection/services/inspection/internal/features/participants/get_participant"
	"inspection/services/inspection/internal/features/templates/catalog"
	templateresolve "inspection/services/inspection/internal/features/templates/resolve_template"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
)

const (
	ProjectActive      = "ACTIVE"
	ProjectClosed      = "CLOSED"
	ProjectInvalidated = "INVALIDATED"

	StagePlanned     = "PLANNED"
	StageAvailable   = "AVAILABLE"
	StageInProgress  = "IN_PROGRESS"
	StageCompleted   = "COMPLETED"
	StageSkipped     = "SKIPPED"
	StageCanceled    = "CANCELED"
	StageInvalidated = "INVALIDATED"
)

type CreateInput struct {
	TenantID, AssetID, ParticipantID identity.ID
	TemplateID                       *identity.ID
	IdempotencyKey                   string
}

type ExceptionalStageInput struct {
	TenantID, ProjectID identity.ID
	Key, Label, Reason  string
	PlannedAt           *time.Time
	ExpectedVersion     int64
	IdempotencyKey      string
}

type StartStageInput struct {
	TenantID, ProjectID, StageID identity.ID
	ExpectedProjectVersion       int64
	ExpectedStageVersion         int64
	ReferenceVersionID           *identity.ID
	DueAt, DeadlineAt            time.Time
	ReminderInstants             []time.Time
}

type TransitionInput struct {
	TenantID, ProjectID identity.ID
	StageID             *identity.ID
	ExpectedVersion     int64
	Reason              string
}

type View struct {
	Project     database.Project
	Stages      []database.ProjectStage
	Transitions []database.StageTransition
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
	if in.TenantID == (identity.ID{}) || in.AssetID == (identity.ID{}) || in.ParticipantID == (identity.ID{}) || strings.TrimSpace(in.IdempotencyKey) == "" {
		return View{}, apperror.New(apperror.InvalidInput, "input", "project prerequisites are required")
	}
	asset, participant, template, document, err := s.prerequisites(ctx, in)
	if err != nil {
		return View{}, err
	}
	if !document.MultiStage || len(document.Stages) == 0 {
		return View{}, apperror.New(apperror.InvalidState, "templateId", "multi-stage template requires planned stages")
	}
	if _, err := s.Authorizer.Authorize(ctx, in.TenantID, []string{auth.TenantAdmin, auth.Manager}, &requestctx.Scope{Kind: "BUSINESS_UNIT", ID: asset.Asset.BusinessUnitID}, true); err != nil {
		return View{}, err
	}
	now := s.now()
	var out View
	err = (tenanttx.Runner{DB: s.DB}).Within(ctx, in.TenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND idempotency_key=?", in.TenantID, in.IdempotencyKey).First(&out.Project).Error; err == nil {
			return s.load(tx, &out)
		} else if err != gorm.ErrRecordNotFound {
			return err
		}
		project := database.Project{ID: identity.NewID(), TenantID: in.TenantID, BusinessUnitID: asset.Asset.BusinessUnitID, AssetID: in.AssetID, ParticipantID: participant.Participant.ID, TemplateID: template.Template.ID, TemplateVersionID: template.Version.ID, ReportMode: document.ReportMode, Status: ProjectActive, Version: 1, IdempotencyKey: in.IdempotencyKey, CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&project).Error; err != nil {
			return err
		}
		requirements, _ := json.Marshal(document.Requirements)
		for index, planned := range document.Stages {
			kind := "INSPECTION"
			if index == 0 && strings.EqualFold(planned.Key, "origin") {
				kind = "ORIGIN"
			}
			reference, _ := json.Marshal(map[string]any{"comparisonMode": document.ComparisonMode, "templateVersionId": template.Version.ID})
			stage := database.ProjectStage{ID: identity.NewID(), TenantID: in.TenantID, ProjectID: project.ID, Key: planned.Key, Label: planned.Label, Kind: kind, Position: planned.Position, Status: map[bool]string{true: StageAvailable, false: StagePlanned}[index == 0], Requirements: requirements, EffectiveReference: reference, Version: 1, CreatedAt: now, UpdatedAt: now}
			if err := tx.Create(&stage).Error; err != nil {
				return err
			}
			if err := s.recordTransition(ctx, tx, project, &stage, "", stage.Status, "", now); err != nil {
				return err
			}
		}
		out.Project = project
		return s.load(tx, &out)
	})
	return out, err
}

func (s Service) AddExceptionalStage(ctx context.Context, in ExceptionalStageInput) (View, error) {
	if !catalog.ValidName(in.Key) || !catalog.ValidName(in.Label) || !validReason(in.Reason) || strings.TrimSpace(in.IdempotencyKey) == "" {
		return View{}, apperror.New(apperror.InvalidInput, "stage", "key, label, reason, and idempotency key are required")
	}
	current, err := s.Get(ctx, in.TenantID, in.ProjectID)
	if err != nil {
		return View{}, err
	}
	if current.Project.Status != ProjectActive {
		return View{}, apperror.New(apperror.InvalidState, "projectId", "project is not active")
	}
	if _, err := s.authorize(ctx, current.Project, true); err != nil {
		return View{}, err
	}
	if len(current.Stages) >= catalog.MaxStages {
		return View{}, apperror.New(apperror.InvalidState, "stages", "stage capacity reached")
	}
	now := s.now()
	err = (tenanttx.Runner{DB: s.DB}).Within(ctx, in.TenantID, func(tx *gorm.DB) error {
		var existing database.ProjectStage
		if err := tx.Where("tenant_id=? AND project_id=? AND idempotency_key=?", in.TenantID, in.ProjectID, in.IdempotencyKey).First(&existing).Error; err == nil {
			return nil
		} else if err != gorm.ErrRecordNotFound {
			return err
		}
		var project database.Project
		if err := tx.Where("tenant_id=? AND id=?", in.TenantID, in.ProjectID).First(&project).Error; err != nil {
			return err
		}
		if project.Status != ProjectActive {
			return apperror.New(apperror.InvalidState, "projectId", "project is not active")
		}
		if in.ExpectedVersion != project.Version {
			return apperror.New(apperror.Conflict, "version", "stale project version")
		}
		var count int64
		if err := tx.Model(&database.ProjectStage{}).Where("tenant_id=? AND project_id=?", in.TenantID, in.ProjectID).Count(&count).Error; err != nil {
			return err
		}
		stage := database.ProjectStage{ID: identity.NewID(), TenantID: in.TenantID, ProjectID: in.ProjectID, Key: strings.TrimSpace(in.Key), Label: strings.TrimSpace(in.Label), Kind: "EXCEPTIONAL", Position: int(count) + 1, Status: StageAvailable, PlannedAt: in.PlannedAt, Requirements: json.RawMessage(`[]`), EffectiveReference: json.RawMessage(`{}`), Reason: strings.TrimSpace(in.Reason), Version: 1, IdempotencyKey: in.IdempotencyKey, CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&stage).Error; err != nil {
			return err
		}
		updated := tx.Model(&database.Project{}).Where("tenant_id=? AND id=? AND version=?", in.TenantID, in.ProjectID, in.ExpectedVersion).Updates(map[string]any{"version": in.ExpectedVersion + 1, "updated_at": now})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return apperror.New(apperror.Conflict, "version", "stale project version")
		}
		return s.recordTransition(ctx, tx, project, &stage, "", StageAvailable, in.Reason, now)
	})
	if err != nil {
		return View{}, err
	}
	return s.Get(ctx, in.TenantID, in.ProjectID)
}

func (s Service) StartStage(ctx context.Context, in StartStageInput) (View, error) {
	current, err := s.Get(ctx, in.TenantID, in.ProjectID)
	if err != nil {
		return View{}, err
	}
	if current.Project.Status != ProjectActive {
		return View{}, apperror.New(apperror.InvalidState, "projectId", "project is not active")
	}
	if current.Project.Version != in.ExpectedProjectVersion {
		return View{}, apperror.New(apperror.Conflict, "version", "stale project version")
	}
	if _, err := s.authorize(ctx, current.Project, true); err != nil {
		return View{}, err
	}
	var selected *database.ProjectStage
	for index := range current.Stages {
		stage := &current.Stages[index]
		if stage.ID == in.StageID {
			selected = stage
			continue
		}
		if selected == nil && !terminalStage(stage.Status) {
			return View{}, apperror.New(apperror.InvalidState, "stageId", "a prior stage requires a terminal decision")
		}
	}
	if selected == nil {
		return View{}, apperror.New(apperror.NotFound, "stageId", "stage not found")
	}
	if selected.Status == StageInProgress && selected.InspectionID != nil {
		return current, nil
	}
	if selected.Status != StageAvailable && selected.Status != StagePlanned {
		return View{}, apperror.New(apperror.InvalidState, "stageId", "stage cannot be started")
	}
	if selected.Version != in.ExpectedStageVersion {
		return View{}, apperror.New(apperror.Conflict, "version", "stale stage version")
	}
	raw, err := s.Bus.Send(ctx, createoccurrence.Command{TenantID: in.TenantID, AssetID: current.Project.AssetID, ParticipantID: current.Project.ParticipantID, TemplateID: &current.Project.TemplateID, ReferenceVersionID: in.ReferenceVersionID, ProjectID: &current.Project.ID, StageID: &selected.ID, Source: inspectioncore.SourceMilestone, SourceKey: selected.ID.String(), DueAt: in.DueAt, DeadlineAt: in.DeadlineAt, ReminderInstants: in.ReminderInstants})
	if err != nil {
		return View{}, err
	}
	inspection := raw.(inspectioncore.View)
	now := s.now()
	err = (tenanttx.Runner{DB: s.DB}).Within(ctx, in.TenantID, func(tx *gorm.DB) error {
		projectUpdate := tx.Model(&database.Project{}).Where("tenant_id=? AND id=? AND version=? AND status=?", in.TenantID, in.ProjectID, in.ExpectedProjectVersion, ProjectActive).Updates(map[string]any{"version": in.ExpectedProjectVersion + 1, "updated_at": now})
		if projectUpdate.Error != nil {
			return projectUpdate.Error
		}
		if projectUpdate.RowsAffected != 1 {
			return apperror.New(apperror.Conflict, "version", "stale project version")
		}
		r := tx.Model(&database.ProjectStage{}).Where("tenant_id=? AND id=? AND version=? AND status IN ?", in.TenantID, in.StageID, in.ExpectedStageVersion, []string{StageAvailable, StagePlanned}).Updates(map[string]any{"status": StageInProgress, "inspection_id": inspection.Inspection.ID, "effective_reference": inspection.Reference.Payload, "version": in.ExpectedStageVersion + 1, "updated_at": now})
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return apperror.New(apperror.Conflict, "version", "stale stage version")
		}
		return s.recordTransition(ctx, tx, current.Project, selected, selected.Status, StageInProgress, "", now)
	})
	if err != nil {
		return View{}, err
	}
	return s.Get(ctx, in.TenantID, in.ProjectID)
}

func (s Service) SkipStage(ctx context.Context, in TransitionInput) (View, error) {
	if in.StageID == nil || !validReason(in.Reason) {
		return View{}, apperror.New(apperror.InvalidInput, "reason", "skip reason is required")
	}
	return s.changeStage(ctx, in, StageSkipped)
}

func (s Service) Close(ctx context.Context, in TransitionInput) (View, error) {
	current, err := s.Get(ctx, in.TenantID, in.ProjectID)
	if err != nil {
		return View{}, err
	}
	if current.Project.Status == ProjectClosed {
		return current, nil
	}
	if current.Project.Status != ProjectActive {
		return View{}, apperror.New(apperror.InvalidState, "projectId", "project cannot be closed")
	}
	for _, stage := range current.Stages {
		if !terminalStage(stage.Status) {
			return View{}, apperror.New(apperror.InvalidState, "stages", "all stages require a terminal decision")
		}
	}
	return s.changeProject(ctx, current, in, ProjectClosed)
}

func (s Service) Reopen(ctx context.Context, in TransitionInput) (View, error) {
	if !validReason(in.Reason) {
		return View{}, apperror.New(apperror.InvalidInput, "reason", "reopen reason is required")
	}
	current, err := s.Get(ctx, in.TenantID, in.ProjectID)
	if err != nil {
		return View{}, err
	}
	if current.Project.Status != ProjectClosed {
		return View{}, apperror.New(apperror.InvalidState, "projectId", "only a closed project can be reopened")
	}
	return s.changeProject(ctx, current, in, ProjectActive)
}

func (s Service) Get(ctx context.Context, tenantID, projectID identity.ID) (View, error) {
	var out View
	err := (tenanttx.Runner{DB: s.DB}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND id=?", tenantID, projectID).First(&out.Project).Error; err != nil {
			return apperror.New(apperror.NotFound, "projectId", "project not found")
		}
		return s.load(tx, &out)
	})
	if err != nil {
		return View{}, err
	}
	if _, err := s.authorize(ctx, out.Project, false); err != nil {
		return View{}, err
	}
	return out, nil
}

func (s Service) List(ctx context.Context, tenantID identity.ID, first int, after string) (ListResult, error) {
	if first <= 0 {
		first = 25
	}
	if first > 100 {
		return ListResult{}, apperror.New(apperror.InvalidInput, "first", "page size cannot exceed 100")
	}
	if _, err := s.Authorizer.Authorize(ctx, tenantID, []string{auth.TenantAdmin, auth.Manager, auth.Employee, auth.Viewer}, nil, false); err != nil {
		return ListResult{}, err
	}
	var rows []database.Project
	err := (tenanttx.Runner{DB: s.DB}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		q := tx.Where("tenant_id=?", tenantID).Order("created_at ASC, id ASC").Limit(first + 1)
		if after != "" {
			q = q.Where("id::text > ?", after)
		}
		return q.Find(&rows).Error
	})
	result := ListResult{HasNextPage: len(rows) > first}
	if result.HasNextPage {
		rows = rows[:first]
	}
	for _, row := range rows {
		view, viewErr := s.Get(ctx, tenantID, row.ID)
		if viewErr != nil {
			return ListResult{}, viewErr
		}
		result.Items = append(result.Items, view)
		result.EndCursor = row.ID.String()
	}
	return result, err
}

func (s Service) changeStage(ctx context.Context, in TransitionInput, target string) (View, error) {
	current, err := s.Get(ctx, in.TenantID, in.ProjectID)
	if err != nil {
		return View{}, err
	}
	if current.Project.Status != ProjectActive {
		return View{}, apperror.New(apperror.InvalidState, "projectId", "project is not active")
	}
	if _, err := s.authorize(ctx, current.Project, true); err != nil {
		return View{}, err
	}
	var stage database.ProjectStage
	for _, candidate := range current.Stages {
		if candidate.ID == *in.StageID {
			stage = candidate
		}
	}
	if stage.ID == (identity.ID{}) {
		return View{}, apperror.New(apperror.NotFound, "stageId", "stage not found")
	}
	if stage.Status == target {
		return current, nil
	}
	if stage.Status != StagePlanned && stage.Status != StageAvailable {
		return View{}, apperror.New(apperror.InvalidState, "stageId", "stage cannot be skipped")
	}
	now := s.now()
	err = (tenanttx.Runner{DB: s.DB}).Within(ctx, in.TenantID, func(tx *gorm.DB) error {
		projectUpdate := tx.Model(&database.Project{}).Where("tenant_id=? AND id=? AND version=? AND status=?", in.TenantID, in.ProjectID, in.ExpectedVersion, ProjectActive).Updates(map[string]any{"version": in.ExpectedVersion + 1, "updated_at": now})
		if projectUpdate.Error != nil {
			return projectUpdate.Error
		}
		if projectUpdate.RowsAffected != 1 {
			return apperror.New(apperror.Conflict, "version", "stale project version")
		}
		r := tx.Model(&database.ProjectStage{}).Where("tenant_id=? AND id=? AND status IN ?", in.TenantID, stage.ID, []string{StagePlanned, StageAvailable}).Updates(map[string]any{"status": target, "reason": strings.TrimSpace(in.Reason), "version": gorm.Expr("version + 1"), "updated_at": now})
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return apperror.New(apperror.Conflict, "version", "stale stage version")
		}
		return s.recordTransition(ctx, tx, current.Project, &stage, stage.Status, target, in.Reason, now)
	})
	if err != nil {
		return View{}, err
	}
	return s.Get(ctx, in.TenantID, in.ProjectID)
}

func (s Service) changeProject(ctx context.Context, current View, in TransitionInput, target string) (View, error) {
	if _, err := s.authorize(ctx, current.Project, true); err != nil {
		return View{}, err
	}
	now := s.now()
	err := (tenanttx.Runner{DB: s.DB}).Within(ctx, in.TenantID, func(tx *gorm.DB) error {
		r := tx.Model(&database.Project{}).Where("tenant_id=? AND id=? AND version=?", in.TenantID, in.ProjectID, in.ExpectedVersion).Updates(map[string]any{"status": target, "version": in.ExpectedVersion + 1, "updated_at": now})
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return apperror.New(apperror.Conflict, "version", "stale project version")
		}
		return s.recordTransition(ctx, tx, current.Project, nil, current.Project.Status, target, in.Reason, now)
	})
	if err != nil {
		return View{}, err
	}
	return s.Get(ctx, in.TenantID, in.ProjectID)
}

func (s Service) prerequisites(ctx context.Context, in CreateInput) (assetcore.View, participantcore.ParticipantView, templateresolve.Result, catalog.TemplateDocument, error) {
	if s.Bus == nil {
		return assetcore.View{}, participantcore.ParticipantView{}, templateresolve.Result{}, catalog.TemplateDocument{}, fmt.Errorf("project: missing mediator")
	}
	assetRaw, err := s.Bus.Ask(ctx, assetget.Query{TenantID: in.TenantID, AssetID: in.AssetID})
	if err != nil {
		return assetcore.View{}, participantcore.ParticipantView{}, templateresolve.Result{}, catalog.TemplateDocument{}, err
	}
	asset := assetRaw.(assetcore.View)
	if asset.Asset.Status != "ACTIVE" {
		return assetcore.View{}, participantcore.ParticipantView{}, templateresolve.Result{}, catalog.TemplateDocument{}, apperror.New(apperror.InvalidState, "assetId", "asset is not active")
	}
	participantRaw, err := s.Bus.Ask(ctx, participantget.Query{TenantID: in.TenantID, ParticipantID: in.ParticipantID})
	if err != nil {
		return assetcore.View{}, participantcore.ParticipantView{}, templateresolve.Result{}, catalog.TemplateDocument{}, err
	}
	participant := participantRaw.(participantcore.ParticipantView)
	if participant.Participant.Status != "ACTIVE" || len(participant.Selected) == 0 {
		return assetcore.View{}, participantcore.ParticipantView{}, templateresolve.Result{}, catalog.TemplateDocument{}, apperror.New(apperror.InvalidState, "participantId", "participant requires an active verified delivery channel")
	}
	assigned := false
	for _, assignment := range asset.Assignments {
		assigned = assigned || (assignment.Active && assignment.ParticipantID == in.ParticipantID)
	}
	if !assigned {
		return assetcore.View{}, participantcore.ParticipantView{}, templateresolve.Result{}, catalog.TemplateDocument{}, apperror.New(apperror.InvalidInput, "participantId", "participant is not assigned to the asset")
	}
	templateID := in.TemplateID
	if templateID == nil {
		templateID = asset.Asset.TemplateID
	}
	if templateID == nil {
		return assetcore.View{}, participantcore.ParticipantView{}, templateresolve.Result{}, catalog.TemplateDocument{}, apperror.New(apperror.InvalidState, "templateId", "active template is required")
	}
	templateRaw, err := s.Bus.Ask(ctx, templateresolve.Query{TenantID: in.TenantID, TemplateID: *templateID})
	if err != nil {
		return assetcore.View{}, participantcore.ParticipantView{}, templateresolve.Result{}, catalog.TemplateDocument{}, err
	}
	template := templateRaw.(templateresolve.Result)
	var document catalog.TemplateDocument
	if err := json.Unmarshal(template.Version.DefinitionJSON, &document); err != nil {
		return assetcore.View{}, participantcore.ParticipantView{}, templateresolve.Result{}, catalog.TemplateDocument{}, fmt.Errorf("decode project template: %w", err)
	}
	return asset, participant, template, document, nil
}

func (s Service) authorize(ctx context.Context, project database.Project, mutate bool) (requestctx.Principal, error) {
	roles := []string{auth.TenantAdmin, auth.Manager, auth.Employee, auth.Viewer}
	if mutate {
		roles = []string{auth.TenantAdmin, auth.Manager}
	}
	return s.Authorizer.Authorize(ctx, project.TenantID, roles, &requestctx.Scope{Kind: "BUSINESS_UNIT", ID: project.BusinessUnitID}, mutate)
}

func (s Service) load(tx *gorm.DB, out *View) error {
	if err := tx.Where("tenant_id=? AND project_id=?", out.Project.TenantID, out.Project.ID).Order("position ASC, id ASC").Find(&out.Stages).Error; err != nil {
		return err
	}
	return tx.Where("tenant_id=? AND project_id=?", out.Project.TenantID, out.Project.ID).Order("occurred_at ASC, id ASC").Find(&out.Transitions).Error
}

func (s Service) recordTransition(ctx context.Context, tx *gorm.DB, project database.Project, stage *database.ProjectStage, from, to, reason string, now time.Time) error {
	metadata, _ := requestctx.FromContext(ctx)
	var stageID *identity.ID
	aggregateID := project.ID
	if stage != nil {
		id := stage.ID
		stageID = &id
		aggregateID = id
	}
	transition := database.StageTransition{ID: identity.NewID(), TenantID: project.TenantID, ProjectID: project.ID, StageID: stageID, ActorID: metadata.Principal.IdentityID, FromState: from, ToState: to, Reason: strings.TrimSpace(reason), CorrelationID: metadata.CorrelationID, OccurredAt: now}
	if transition.ActorID == (identity.ID{}) {
		transition.ActorID = project.ID
	}
	if transition.CorrelationID == "" {
		transition.CorrelationID = transition.ID.String()
	}
	if err := tx.Create(&transition).Error; err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{"projectId": project.ID, "stageId": stageID, "from": from, "to": to, "reason": strings.TrimSpace(reason)})
	eventID := identity.NewID()
	envelope, _ := json.Marshal(map[string]any{"id": eventID, "type": "project.stage_changed.v1", "schemaVersion": 1, "occurredAt": now, "tenantId": project.TenantID, "aggregateId": aggregateID, "correlationId": transition.CorrelationID, "payload": json.RawMessage(payload)})
	return tx.Create(&database.OutboxIntent{ID: eventID, TenantID: project.TenantID, Type: "project.stage_changed.v1", SchemaVersion: 1, Payload: envelope, CorrelationID: transition.CorrelationID, Status: "PENDING", NextAttemptAt: now, CreatedAt: now}).Error
}

func terminalStage(status string) bool {
	return status == StageCompleted || status == StageSkipped || status == StageCanceled || status == StageInvalidated
}

func validReason(value string) bool {
	count := utf8.RuneCountInString(strings.TrimSpace(value))
	return count > 0 && count <= catalog.MaxTextCodePoints
}

func (s Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
