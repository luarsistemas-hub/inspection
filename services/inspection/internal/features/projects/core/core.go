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
	"inspection/services/inspection/internal/features/templates/catalog"
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

type PlannedStageInput struct {
	Key, Label string
	PlannedAt  *time.Time
}

type CreateInput struct {
	TenantID, AssetID identity.ID
	Name              string
	OrderedStages     bool
	Stages            []PlannedStageInput
	IdempotencyKey    string
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

// ValidateAssociation ensures an inspection or schedule belongs to the chosen
// active project and optional stage without imposing a project-wide template.
func ValidateAssociation(tx *gorm.DB, tenantID, assetID identity.ID, projectID, stageID *identity.ID) error {
	if stageID != nil && projectID == nil {
		return apperror.New(apperror.InvalidInput, "stageId", "A etapa exige um projeto.")
	}
	if projectID == nil {
		return nil
	}
	var project database.Project
	if err := tx.Where("tenant_id=? AND id=?", tenantID, *projectID).First(&project).Error; err != nil {
		return apperror.New(apperror.NotFound, "projectId", "Projeto não encontrado.")
	}
	if project.AssetID != assetID || project.Status != ProjectActive {
		return apperror.New(apperror.InvalidState, "projectId", "O projeto precisa estar ativo e pertencer ao ativo selecionado.")
	}
	if stageID == nil {
		return nil
	}
	var stage database.ProjectStage
	if err := tx.Where("tenant_id=? AND project_id=? AND id=?", tenantID, *projectID, *stageID).First(&stage).Error; err != nil {
		return apperror.New(apperror.NotFound, "stageId", "Etapa não encontrada no projeto.")
	}
	if terminalStage(stage.Status) {
		return apperror.New(apperror.InvalidState, "stageId", "A etapa já foi encerrada.")
	}
	return nil
}

func (s Service) Create(ctx context.Context, in CreateInput) (View, error) {
	if in.TenantID == (identity.ID{}) || in.AssetID == (identity.ID{}) || !catalog.ValidName(strings.TrimSpace(in.Name)) || strings.TrimSpace(in.IdempotencyKey) == "" {
		return View{}, apperror.New(apperror.InvalidInput, "input", "Informe o nome do projeto, o ativo e a chave de idempotência.")
	}
	if len(in.Stages) > catalog.MaxStages {
		return View{}, apperror.New(apperror.InvalidInput, "stages", "O limite de etapas do projeto foi atingido.")
	}
	for i, stage := range in.Stages {
		if !catalog.ValidName(strings.TrimSpace(stage.Key)) || !catalog.ValidName(strings.TrimSpace(stage.Label)) {
			return View{}, apperror.New(apperror.InvalidInput, "stages", "Toda etapa exige chave e rótulo válidos.")
		}
		for j := 0; j < i; j++ {
			if strings.EqualFold(in.Stages[j].Key, stage.Key) {
				return View{}, apperror.New(apperror.InvalidInput, "stages", "As chaves das etapas devem ser únicas.")
			}
		}
	}
	if s.Bus == nil {
		return View{}, fmt.Errorf("project: missing mediator")
	}
	assetRaw, err := s.Bus.Ask(ctx, assetget.Query{TenantID: in.TenantID, AssetID: in.AssetID})
	if err != nil {
		return View{}, err
	}
	asset := assetRaw.(assetcore.View)
	if asset.Asset.Status != "ACTIVE" {
		return View{}, apperror.New(apperror.InvalidState, "assetId", "O ativo não está ativo.")
	}
	roles := []string{auth.TenantAdmin, auth.Manager}
	if _, err := s.Authorizer.Authorize(ctx, auth.AuthorizationRequest{TenantID: in.TenantID, Roles: roles, Resource: &requestctx.Scope{Kind: "BUSINESS_UNIT", ID: asset.Asset.BusinessUnitID}, Mutate: true, NewWork: true}); err != nil {
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
		project := database.Project{ID: identity.NewID(), TenantID: in.TenantID, BusinessUnitID: asset.Asset.BusinessUnitID, AssetID: in.AssetID, Name: strings.TrimSpace(in.Name), OrderedStages: in.OrderedStages, Status: ProjectActive, Version: 1, IdempotencyKey: in.IdempotencyKey, CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&project).Error; err != nil {
			return err
		}
		for index, planned := range in.Stages {
			stage := database.ProjectStage{ID: identity.NewID(), TenantID: in.TenantID, ProjectID: project.ID, Key: strings.TrimSpace(planned.Key), Label: strings.TrimSpace(planned.Label), Kind: "INSPECTION", Position: index + 1, Status: map[bool]string{true: StageAvailable, false: StagePlanned}[index == 0], PlannedAt: planned.PlannedAt, Requirements: json.RawMessage(`[]`), EffectiveReference: json.RawMessage(`{}`), Version: 1, CreatedAt: now, UpdatedAt: now}
			if err := tx.Create(&stage).Error; err != nil {
				return err
			}
			if err := s.recordTransition(tx.Statement.Context, tx, project, &stage, "", stage.Status, "", now); err != nil {
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
		return View{}, apperror.New(apperror.InvalidInput, "stage", "Informe a chave, o rótulo, o motivo e a chave de idempotência.")
	}
	current, err := s.Get(ctx, in.TenantID, in.ProjectID)
	if err != nil {
		return View{}, err
	}
	if current.Project.Status != ProjectActive {
		return View{}, apperror.New(apperror.InvalidState, "projectId", "O projeto não está ativo.")
	}
	if _, err := s.authorize(ctx, current.Project, true, true); err != nil {
		return View{}, err
	}
	if len(current.Stages) >= catalog.MaxStages {
		return View{}, apperror.New(apperror.InvalidState, "stages", "O limite de etapas do projeto foi atingido.")
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
			return apperror.New(apperror.InvalidState, "projectId", "O projeto não está ativo.")
		}
		if in.ExpectedVersion != project.Version {
			return apperror.New(apperror.Conflict, "version", "O projeto foi alterado. Atualize os dados e tente novamente.")
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
			return apperror.New(apperror.Conflict, "version", "O projeto foi alterado. Atualize os dados e tente novamente.")
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
		return View{}, apperror.New(apperror.InvalidState, "projectId", "O projeto não está ativo.")
	}
	if current.Project.Version != in.ExpectedProjectVersion {
		return View{}, apperror.New(apperror.Conflict, "version", "O projeto foi alterado. Atualize os dados e tente novamente.")
	}
	if _, err := s.authorize(ctx, current.Project, true, true); err != nil {
		return View{}, err
	}
	var selected *database.ProjectStage
	for index := range current.Stages {
		stage := &current.Stages[index]
		if stage.ID == in.StageID {
			selected = stage
			continue
		}
		if current.Project.OrderedStages && selected == nil && !terminalStage(stage.Status) {
			return View{}, apperror.New(apperror.InvalidState, "stageId", "Uma etapa anterior precisa ser concluída ou dispensada antes.")
		}
	}
	if selected == nil {
		return View{}, apperror.New(apperror.NotFound, "stageId", "Etapa não encontrada.")
	}
	if selected.Status == StageInProgress {
		return current, nil
	}
	if selected.Status != StageAvailable && selected.Status != StagePlanned {
		return View{}, apperror.New(apperror.InvalidState, "stageId", "A etapa não pode ser iniciada.")
	}
	if selected.Version != in.ExpectedStageVersion {
		return View{}, apperror.New(apperror.Conflict, "version", "A etapa foi alterada. Atualize os dados e tente novamente.")
	}
	now := s.now()
	err = (tenanttx.Runner{DB: s.DB}).Within(ctx, in.TenantID, func(tx *gorm.DB) error {
		projectUpdate := tx.Model(&database.Project{}).Where("tenant_id=? AND id=? AND version=? AND status=?", in.TenantID, in.ProjectID, in.ExpectedProjectVersion, ProjectActive).Updates(map[string]any{"version": in.ExpectedProjectVersion + 1, "updated_at": now})
		if projectUpdate.Error != nil {
			return projectUpdate.Error
		}
		if projectUpdate.RowsAffected != 1 {
			return apperror.New(apperror.Conflict, "version", "O projeto foi alterado. Atualize os dados e tente novamente.")
		}
		r := tx.Model(&database.ProjectStage{}).Where("tenant_id=? AND id=? AND version=? AND status IN ?", in.TenantID, in.StageID, in.ExpectedStageVersion, []string{StageAvailable, StagePlanned}).Updates(map[string]any{"status": StageInProgress, "version": in.ExpectedStageVersion + 1, "updated_at": now})
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return apperror.New(apperror.Conflict, "version", "A etapa foi alterada. Atualize os dados e tente novamente.")
		}
		return s.recordTransition(tx.Statement.Context, tx, current.Project, selected, selected.Status, StageInProgress, "", now)
	})
	if err != nil {
		return View{}, err
	}
	return s.Get(ctx, in.TenantID, in.ProjectID)
}

func (s Service) SkipStage(ctx context.Context, in TransitionInput) (View, error) {
	if in.StageID == nil || !validReason(in.Reason) {
		return View{}, apperror.New(apperror.InvalidInput, "reason", "Informe o motivo para dispensar a etapa.")
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
		return View{}, apperror.New(apperror.InvalidState, "projectId", "O projeto não pode ser encerrado.")
	}
	for _, stage := range current.Stages {
		if !terminalStage(stage.Status) {
			return View{}, apperror.New(apperror.InvalidState, "stages", "Todas as etapas precisam ser concluídas ou dispensadas antes de encerrar o projeto.")
		}
	}
	return s.changeProject(ctx, current, in, ProjectClosed)
}

func (s Service) Reopen(ctx context.Context, in TransitionInput) (View, error) {
	if !validReason(in.Reason) {
		return View{}, apperror.New(apperror.InvalidInput, "reason", "Informe o motivo para reabrir o projeto.")
	}
	current, err := s.Get(ctx, in.TenantID, in.ProjectID)
	if err != nil {
		return View{}, err
	}
	if current.Project.Status != ProjectClosed {
		return View{}, apperror.New(apperror.InvalidState, "projectId", "Somente um projeto encerrado pode ser reaberto.")
	}
	return s.changeProject(ctx, current, in, ProjectActive)
}

func (s Service) Get(ctx context.Context, tenantID, projectID identity.ID) (View, error) {
	var out View
	err := (tenanttx.Runner{DB: s.DB}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND id=?", tenantID, projectID).First(&out.Project).Error; err != nil {
			return apperror.New(apperror.NotFound, "projectId", "Projeto não encontrado.")
		}
		return s.load(tx, &out)
	})
	if err != nil {
		return View{}, err
	}
	if _, err := s.authorize(ctx, out.Project, false, false); err != nil {
		return View{}, err
	}
	return out, nil
}

func (s Service) List(ctx context.Context, tenantID identity.ID, first int, after string) (ListResult, error) {
	if first <= 0 {
		first = 25
	}
	if first > 100 {
		return ListResult{}, apperror.New(apperror.InvalidInput, "first", "O tamanho da página não pode passar de 100.")
	}
	if _, err := s.Authorizer.Authorize(ctx, auth.AuthorizationRequest{TenantID: tenantID, Product: auth.DashboardProduct, Roles: []string{auth.TenantAdmin, auth.Manager, auth.Employee, auth.Viewer}}); err != nil {
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
		return View{}, apperror.New(apperror.InvalidState, "projectId", "O projeto não está ativo.")
	}
	if _, err := s.authorize(ctx, current.Project, true, false); err != nil {
		return View{}, err
	}
	var stage database.ProjectStage
	for _, candidate := range current.Stages {
		if candidate.ID == *in.StageID {
			stage = candidate
		}
	}
	if stage.ID == (identity.ID{}) {
		return View{}, apperror.New(apperror.NotFound, "stageId", "Etapa não encontrada.")
	}
	if stage.Status == target {
		return current, nil
	}
	if stage.Status != StagePlanned && stage.Status != StageAvailable {
		return View{}, apperror.New(apperror.InvalidState, "stageId", "A etapa não pode ser dispensada.")
	}
	now := s.now()
	err = (tenanttx.Runner{DB: s.DB}).Within(ctx, in.TenantID, func(tx *gorm.DB) error {
		projectUpdate := tx.Model(&database.Project{}).Where("tenant_id=? AND id=? AND version=? AND status=?", in.TenantID, in.ProjectID, in.ExpectedVersion, ProjectActive).Updates(map[string]any{"version": in.ExpectedVersion + 1, "updated_at": now})
		if projectUpdate.Error != nil {
			return projectUpdate.Error
		}
		if projectUpdate.RowsAffected != 1 {
			return apperror.New(apperror.Conflict, "version", "O projeto foi alterado. Atualize os dados e tente novamente.")
		}
		r := tx.Model(&database.ProjectStage{}).Where("tenant_id=? AND id=? AND status IN ?", in.TenantID, stage.ID, []string{StagePlanned, StageAvailable}).Updates(map[string]any{"status": target, "reason": strings.TrimSpace(in.Reason), "version": gorm.Expr("version + 1"), "updated_at": now})
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return apperror.New(apperror.Conflict, "version", "A etapa foi alterada. Atualize os dados e tente novamente.")
		}
		if target == StageSkipped {
			if err := tx.Model(&database.Schedule{}).Where("tenant_id=? AND stage_id=? AND status='ACTIVE'", in.TenantID, stage.ID).Updates(map[string]any{"status": "CANCELED", "version": gorm.Expr("version + 1"), "updated_at": now}).Error; err != nil {
				return err
			}
		}
		return s.recordTransition(ctx, tx, current.Project, &stage, stage.Status, target, in.Reason, now)
	})
	if err != nil {
		return View{}, err
	}
	return s.Get(ctx, in.TenantID, in.ProjectID)
}

func (s Service) changeProject(ctx context.Context, current View, in TransitionInput, target string) (View, error) {
	if _, err := s.authorize(ctx, current.Project, true, false); err != nil {
		return View{}, err
	}
	now := s.now()
	err := (tenanttx.Runner{DB: s.DB}).Within(ctx, in.TenantID, func(tx *gorm.DB) error {
		updates := map[string]any{"status": target, "version": in.ExpectedVersion + 1, "updated_at": now}
		// Closure starts the retention clock of the project's inspections; a
		// reopened project leaves retention until it is closed again.
		if target == ProjectClosed {
			updates["closed_at"] = now
		} else {
			updates["closed_at"] = nil
		}
		r := tx.Model(&database.Project{}).Where("tenant_id=? AND id=? AND version=?", in.TenantID, in.ProjectID, in.ExpectedVersion).Updates(updates)
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return apperror.New(apperror.Conflict, "version", "O projeto foi alterado. Atualize os dados e tente novamente.")
		}
		if target == ProjectClosed {
			if err := tx.Model(&database.Schedule{}).Where("tenant_id=? AND project_id=? AND status='ACTIVE'", in.TenantID, in.ProjectID).Updates(map[string]any{"status": "CANCELED", "version": gorm.Expr("version + 1"), "updated_at": now}).Error; err != nil {
				return err
			}
		}
		return s.recordTransition(ctx, tx, current.Project, nil, current.Project.Status, target, in.Reason, now)
	})
	if err != nil {
		return View{}, err
	}
	return s.Get(ctx, in.TenantID, in.ProjectID)
}

func (s Service) authorize(ctx context.Context, project database.Project, mutate, newWork bool) (requestctx.Principal, error) {
	roles := []string{auth.TenantAdmin, auth.Manager, auth.Employee, auth.Viewer}
	if mutate {
		roles = []string{auth.TenantAdmin, auth.Manager}
	}
	principal, err := s.Authorizer.Authorize(ctx, auth.AuthorizationRequest{TenantID: project.TenantID, Roles: roles, Resource: &requestctx.Scope{Kind: "BUSINESS_UNIT", ID: project.BusinessUnitID}, Mutate: mutate})
	if err != nil || !newWork {
		return principal, err
	}
	return s.Authorizer.Authorize(ctx, auth.AuthorizationRequest{TenantID: project.TenantID, Roles: roles, Resource: &requestctx.Scope{Kind: "PROJECT", ID: project.ID}, Mutate: mutate, NewWork: true})
}

func (s Service) load(tx *gorm.DB, out *View) error {
	if err := tx.Where("tenant_id=? AND project_id=?", out.Project.TenantID, out.Project.ID).Order("position ASC, id ASC").Find(&out.Stages).Error; err != nil {
		return err
	}
	if len(out.Stages) > 0 {
		stageIDs := make([]identity.ID, len(out.Stages))
		stageIndex := make(map[identity.ID]int, len(out.Stages))
		for index := range out.Stages {
			stageIDs[index] = out.Stages[index].ID
			stageIndex[out.Stages[index].ID] = index
		}
		var inspections []database.Inspection
		if err := tx.Select("id", "stage_id").Where("tenant_id=? AND stage_id IN ?", out.Project.TenantID, stageIDs).Order("created_at ASC, id ASC").Find(&inspections).Error; err != nil {
			return err
		}
		for _, inspection := range inspections {
			if inspection.StageID != nil {
				index := stageIndex[*inspection.StageID]
				out.Stages[index].InspectionIDs = append(out.Stages[index].InspectionIDs, inspection.ID)
			}
		}
	}
	return tx.Where("tenant_id=? AND project_id=?", out.Project.TenantID, out.Project.ID).Order("occurred_at ASC, id ASC").Find(&out.Transitions).Error
}

func (s Service) recordTransition(ctx context.Context, tx *gorm.DB, project database.Project, stage *database.ProjectStage, from, to, reason string, now time.Time) error {
	metadata, _ := requestctx.FromContext(ctx)
	return writeTransition(tx, project, stage, from, to, reason, now, metadata.Principal.IdentityID, metadata.CorrelationID, identity.NewID())
}

// writeTransition appends the transition row and the project.stage_changed.v1
// outbox event of one project or stage state change. An empty actor falls back
// to the project and an empty correlation to the transition itself.
func writeTransition(tx *gorm.DB, project database.Project, stage *database.ProjectStage, from, to, reason string, now time.Time, actorID identity.ID, correlationID string, eventID identity.ID) error {
	var stageID *identity.ID
	aggregateID := project.ID
	if stage != nil {
		id := stage.ID
		stageID = &id
		aggregateID = id
	}
	transition := database.StageTransition{ID: identity.NewID(), TenantID: project.TenantID, ProjectID: project.ID, StageID: stageID, ActorID: actorID, FromState: from, ToState: to, Reason: strings.TrimSpace(reason), CorrelationID: correlationID, OccurredAt: now}
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
