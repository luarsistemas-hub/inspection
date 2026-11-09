package core

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"inspection/libs/identity"
	catalognotes "inspection/services/inspection/internal/features/analysis/catalog_notes"
	"inspection/services/inspection/internal/features/analysis/prompt"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// MaxRegenerationRevisions bounds the note revisions of one explicit regeneration.
const MaxRegenerationRevisions = catalognotes.MaxNotes

// RegenerateInput asks to refresh the undecided suggestions of one room with
// selected, current transcript revisions.
type RegenerateInput struct {
	TenantID, OriginVersionID, EnvironmentID, ActorMembershipID identity.ID
	// ExpectedVersion is the origin revision (the version exposed as
	// OriginVersion.originRevision) the caller saw. A change of the property's
	// origin since then is a conflict, as for activation.
	ExpectedVersion int
	// RevisionIDs are the current transcript revisions to include.
	RevisionIDs      []identity.ID
	ClientMutationID string
}

// RegenerateResult identifies the catalog run an explicit regeneration scheduled.
type RegenerateResult struct {
	JobID  identity.ID
	Status string
	// Replayed reports that the request repeated an earlier one.
	Replayed bool
}

// RegenerateSuggestions schedules a new catalog generation for one room of an
// editable draft. It fixes a new note manifest from the selected current
// revisions (so late or corrected text may enter, ADR-005) and leaves everything
// a person decided, and any active version, untouched: the catalog write only
// replaces pending suggestions. The draft stays reviewable meanwhile. Repeating
// the same clientMutationId returns the same run.
func (s Service) RegenerateSuggestions(ctx context.Context, in RegenerateInput) (RegenerateResult, error) {
	revisions, err := validateRegeneration(in)
	if err != nil {
		return RegenerateResult{}, err
	}
	var out RegenerateResult
	err = s.within(ctx, in.TenantID, func(tx *gorm.DB) error {
		var env database.InventoryEnvironment
		if err := tx.Where("tenant_id=? AND origin_version_id=? AND id=?", in.TenantID, in.OriginVersionID, in.EnvironmentID).Take(&env).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return apperror.New(apperror.NotFound, "environmentId", "O ambiente não foi encontrado neste rascunho.")
			}
			return err
		}
		jobID := identity.NewDeterministicID("inventory-regeneration", in.TenantID.String()+"|"+in.OriginVersionID.String()+"|"+in.EnvironmentID.String()+"|"+in.ClientMutationID)
		replayed, found, err := s.replayRegeneration(ctx, tx, in, env, jobID, revisions)
		if err != nil || found {
			out = replayed
			return err
		}
		version, err := s.editable(tx, in.TenantID, in.OriginVersionID)
		if err != nil {
			return regenerationStateError(err)
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND origin_version_id=? AND id=?", in.TenantID, in.OriginVersionID, in.EnvironmentID).Take(&env).Error; err != nil {
			return err
		}
		// A concurrent retry of the same mutation waited on the locks above: the
		// first attempt has committed by now, so this is a replay, not a conflict.
		if replayed, found, err := s.replayRegeneration(ctx, tx, in, env, jobID, revisions); err != nil || found {
			out = replayed
			return err
		}
		if env.Decision == "REJECTED" || env.RequirementKey == nil || version.SourceInspectionID == nil {
			return apperror.New(apperror.InvalidState, "environmentId", "Este ambiente não pode ser regenerado: ele foi excluído ou não veio de uma captura.")
		}
		var origin database.Origin
		if err := tx.Select("version").Where("tenant_id=? AND id=?", in.TenantID, version.OriginID).Take(&origin).Error; err != nil {
			return err
		}
		if origin.Version != int64(in.ExpectedVersion) {
			return apperror.New(apperror.Conflict, "expectedVersion", "A origem do imóvel foi alterada. Atualize a tela e tente novamente.")
		}
		var latest database.InventoryJob
		if err := tx.Where("tenant_id=? AND inspection_id=? AND kind='CATALOG' AND requirement_key=?", in.TenantID, *version.SourceInspectionID, *env.RequirementKey).Order("generation DESC").Take(&latest).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return apperror.New(apperror.InvalidState, "environmentId", "Este ambiente ainda não foi catalogado.")
			}
			return err
		}
		if latest.OriginVersionID != version.ID {
			return apperror.New(apperror.InvalidState, "environmentId", "Este ambiente não pertence ao rascunho informado.")
		}
		if latest.Status != "SUCCEEDED" && latest.Status != "FAILED" && latest.Status != "CANCELED" {
			return apperror.New(apperror.InvalidState, "environmentId", "Já existe uma regeneração em andamento para este ambiente.")
		}
		room, err := regenerationRoom(tx, in.TenantID, *version.SourceInspectionID, *env.RequirementKey, latest.Generation+1)
		if err != nil {
			return err
		}
		if _, err := catalognotes.SealSelected(ctx, tx, room, revisions, s.now()); err != nil {
			return regenerationSelectionError(err)
		}
		if latest.Status == "SUCCEEDED" {
			// Nothing new to incorporate: the selection is exactly what the
			// last generation of this room already used. The selection was
			// validated first, so an ineligible note is reported as such.
			previous, err := catalognotes.Load(ctx, tx, catalognotes.Room{TenantID: in.TenantID, InspectionID: latest.InspectionID, RequirementKey: latest.RequirementKey, Generation: latest.Generation})
			if err != nil {
				return err
			}
			if previous != nil && sameRevisions(previous.Included(), revisions) {
				return apperror.New(apperror.InvalidState, "revisionIds", "Não há notas novas ou corrigidas para regenerar neste ambiente.")
			}
		}
		now := s.now()
		identityID := env.IdentityID
		var snapshot database.AnalysisPromptSnapshot
		if latest.PromptSnapshotID == nil {
			return apperror.New(apperror.InvalidState, "environmentId", "Não foi possível localizar a definição da análise para regenerar este ambiente.")
		}
		if err := tx.Where("id=?", *latest.PromptSnapshotID).Take(&snapshot).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return apperror.New(apperror.InvalidState, "environmentId", "Não foi possível localizar a definição da análise para regenerar este ambiente.")
			}
			return err
		}
		schemaVersion, schemaDigest := prompt.SchemaPin(snapshot.SchemaVersion, snapshot.CanonicalDigest)
		job := database.InventoryJob{ID: jobID, TenantID: in.TenantID, InspectionID: *version.SourceInspectionID, OriginVersionID: version.ID, Kind: "CATALOG", RequirementKey: *env.RequirementKey, Generation: latest.Generation + 1, Status: "PENDING", NextAttemptAt: now, EnvironmentIdentityID: &identityID, PromptSnapshotID: latest.PromptSnapshotID, SchemaVersion: schemaVersion, SchemaDigest: schemaDigest, CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&job).Error; err != nil {
			return err
		}
		replaced, err := pendingSuggestionSnapshot(tx, in.TenantID, version.ID, env.ID)
		if err != nil {
			return err
		}
		if err := s.audit(tx, in.TenantID, version.ID, "ENVIRONMENT", env.ID, "REGENERATE", map[string]any{"replacedSuggestions": replaced}, marshal(map[string]any{"jobId": job.ID, "generation": job.Generation, "revisionIds": revisions}), "", in.ActorMembershipID, now); err != nil {
			return err
		}
		out = RegenerateResult{JobID: job.ID, Status: job.Status}
		return nil
	})
	return out, err
}

// validateRegeneration checks the request shape and returns the revisions in
// request order without duplicates.
func validateRegeneration(in RegenerateInput) ([]identity.ID, error) {
	if in.TenantID == (identity.ID{}) || in.OriginVersionID == (identity.ID{}) {
		return nil, apperror.New(apperror.InvalidInput, "originVersionId", "O rascunho do inventário é obrigatório.")
	}
	if in.EnvironmentID == (identity.ID{}) {
		return nil, apperror.New(apperror.InvalidInput, "environmentId", "O ambiente é obrigatório.")
	}
	if id := strings.TrimSpace(in.ClientMutationID); id == "" || len(id) > 200 {
		return nil, apperror.New(apperror.InvalidInput, "clientMutationId", "O identificador da operação é inválido.")
	}
	if in.ExpectedVersion < 1 {
		return nil, apperror.New(apperror.InvalidInput, "expectedVersion", "A versão esperada da origem é inválida.")
	}
	if len(in.RevisionIDs) < 1 || len(in.RevisionIDs) > MaxRegenerationRevisions {
		return nil, apperror.New(apperror.InvalidInput, "revisionIds", "Selecione de 1 a 20 notas para regenerar as sugestões.")
	}
	seen := make(map[identity.ID]bool, len(in.RevisionIDs))
	out := make([]identity.ID, 0, len(in.RevisionIDs))
	for _, id := range in.RevisionIDs {
		if id == (identity.ID{}) || seen[id] {
			return nil, apperror.New(apperror.InvalidInput, "revisionIds", "A seleção de notas contém itens inválidos ou repetidos.")
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, nil
}

// replayRegeneration returns the earlier run of the same clientMutationId. The
// replay must carry the same room and the same selected revisions; anything
// else reusing the identifier is a conflict.
func (s Service) replayRegeneration(ctx context.Context, tx *gorm.DB, in RegenerateInput, env database.InventoryEnvironment, jobID identity.ID, revisions []identity.ID) (RegenerateResult, bool, error) {
	var job database.InventoryJob
	err := tx.Where("tenant_id=? AND id=?", in.TenantID, jobID).Take(&job).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return RegenerateResult{}, false, nil
	}
	if err != nil {
		return RegenerateResult{}, false, err
	}
	manifest, err := catalognotes.Load(ctx, tx, catalognotes.Room{TenantID: in.TenantID, InspectionID: job.InspectionID, RequirementKey: job.RequirementKey, Generation: job.Generation})
	if err != nil {
		return RegenerateResult{}, false, err
	}
	conflict := apperror.New(apperror.Conflict, "clientMutationId", "O identificador da operação já foi usado com outros dados.")
	if manifest == nil || job.OriginVersionID != in.OriginVersionID || env.RequirementKey == nil || job.RequirementKey != *env.RequirementKey {
		return RegenerateResult{}, false, conflict
	}
	if !sameRevisions(manifest.Included(), revisions) {
		return RegenerateResult{}, false, conflict
	}
	return RegenerateResult{JobID: job.ID, Status: job.Status, Replayed: true}, true, nil
}

// regenerationRoom reads the room of the submitted capture: its responsibility
// and the media currently captured for the requirement.
func regenerationRoom(tx *gorm.DB, tenant, inspectionID identity.ID, requirementKey string, generation int) (catalognotes.Room, error) {
	var capture database.CaptureDraft
	if err := tx.Where("tenant_id=? AND responsibility_id IN (SELECT id FROM inspections.responsibilities WHERE tenant_id=? AND inspection_id=?) AND status='SUBMITTED'", tenant, tenant, inspectionID).Order("created_at DESC").Take(&capture).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return catalognotes.Room{}, apperror.New(apperror.InvalidState, "environmentId", "A captura deste ambiente não está disponível.")
		}
		return catalognotes.Room{}, err
	}
	var answer database.RequirementAnswer
	if err := tx.Where("tenant_id=? AND draft_id=? AND requirement_key=?", tenant, capture.ID, requirementKey).Take(&answer).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return catalognotes.Room{}, apperror.New(apperror.InvalidState, "environmentId", "O ambiente não tem mídia capturada.")
		}
		return catalognotes.Room{}, err
	}
	var mediaIDs []identity.ID
	if answer.ImpossibilityReason != "" || json.Unmarshal(answer.MediaIDs, &mediaIDs) != nil || len(mediaIDs) == 0 {
		return catalognotes.Room{}, apperror.New(apperror.InvalidState, "environmentId", "O ambiente não tem mídia capturada.")
	}
	return catalognotes.Room{TenantID: tenant, InspectionID: inspectionID, ResponsibilityID: capture.ResponsibilityID, RequirementKey: requirementKey, MediaIDs: mediaIDs, Generation: generation}, nil
}

// regenerationStateError gives the draft state failures of an editable check a
// user-facing message.
func regenerationStateError(err error) error {
	var public *apperror.Error
	if !errors.As(err, &public) {
		return err
	}
	switch public.Code {
	case apperror.NotFound:
		return apperror.New(apperror.NotFound, "originVersionId", "O rascunho do inventário não foi encontrado.")
	case apperror.InvalidState:
		return apperror.New(apperror.InvalidState, "originVersionId", "Só é possível regenerar sugestões de um rascunho pronto e ainda não ativado.")
	}
	return err
}

// regenerationSelectionError maps a rejected selection to its scope message.
func regenerationSelectionError(err error) error {
	switch {
	case errors.Is(err, catalognotes.ErrSelectionOutOfScope):
		return apperror.New(apperror.InvalidInput, "revisionIds", "Uma das notas selecionadas não pertence a este ambiente.")
	case errors.Is(err, catalognotes.ErrSelectionStale):
		return apperror.New(apperror.Conflict, "revisionIds", "Uma das notas foi revisada depois da seleção. Atualize a tela e selecione o texto atual.")
	case errors.Is(err, catalognotes.ErrSelectionNotReady):
		return apperror.New(apperror.InvalidState, "revisionIds", "Uma das notas ainda está sendo processada ou aguarda a validação da mídia.")
	case errors.Is(err, catalognotes.ErrSelectionIneligible):
		return apperror.New(apperror.InvalidInput, "revisionIds", "Uma das notas selecionadas não pode ser usada: foi descartada, não tem fala ou aponta para uma mídia inválida.")
	}
	return err
}

// sameRevisions reports whether a manifest's included entries are exactly the given revisions.
func sameRevisions(included []catalognotes.Entry, revisions []identity.ID) bool {
	if len(included) != len(revisions) {
		return false
	}
	have := make(map[identity.ID]bool, len(included))
	for _, entry := range included {
		have[*entry.RevisionID] = true
	}
	for _, id := range revisions {
		if !have[id] {
			return false
		}
	}
	return true
}

// suggestionSnapshot is what the audit trail keeps of a pending suggestion a
// regeneration is about to replace: identity, name and every cited source.
type suggestionSnapshot struct {
	ItemID   identity.ID      `json:"itemId"`
	Name     string           `json:"name"`
	TypeCode string           `json:"typeCode"`
	Quantity *int             `json:"quantity,omitempty"`
	Sources  []sourceSnapshot `json:"sources"`
}

type sourceSnapshot struct {
	Attribute        string       `json:"attribute"`
	Kind             string       `json:"kind"`
	NoteID           *identity.ID `json:"noteId,omitempty"`
	RevisionID       *identity.ID `json:"revisionId,omitempty"`
	VisualEvidenceID *identity.ID `json:"visualEvidenceId,omitempty"`
}

// pendingSuggestionSnapshot lists the pending AI suggestions of the room with
// their sources, in two reads.
func pendingSuggestionSnapshot(tx *gorm.DB, tenant, version, environment identity.ID) ([]suggestionSnapshot, error) {
	var items []database.InventoryItem
	if err := tx.Select("id,name,type_code,quantity").Where("tenant_id=? AND origin_version_id=? AND environment_id=? AND origin='AI' AND decision='PENDING'", tenant, version, environment).Order("name,id").Find(&items).Error; err != nil {
		return nil, err
	}
	ids := make([]identity.ID, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	sources, err := LoadAttributeSources(tx, tenant, ids)
	if err != nil {
		return nil, err
	}
	out := make([]suggestionSnapshot, 0, len(items))
	for _, item := range items {
		snapshot := suggestionSnapshot{ItemID: item.ID, Name: item.Name, TypeCode: item.TypeCode, Quantity: item.Quantity, Sources: []sourceSnapshot{}}
		for _, source := range sources[item.ID] {
			snapshot.Sources = append(snapshot.Sources, sourceSnapshot{Attribute: source.AttributeName, Kind: source.SourceKind, NoteID: source.NoteID, RevisionID: source.RevisionID, VisualEvidenceID: source.VisualEvidenceID})
		}
		out = append(out, snapshot)
	}
	return out, nil
}
