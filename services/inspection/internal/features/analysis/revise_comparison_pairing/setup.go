// Package revise_comparison_pairing records a staff decision about the
// evidence pairing of one baseline item of a report v3 comparison and opens a
// new comparison generation that applies it. The issued report versions are
// never rewritten: the new generation is classified and issued as the next
// report version.
package revise_comparison_pairing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	requestinventoryjobs "inspection/services/inspection/internal/features/analysis/request_inventory_jobs"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
)

// maxPairs bounds the pairs of one item decided in one request.
const maxPairs = 10

// pendingTTL matches the window in which a queued reissue blocks another one.
const pendingTTL = 15 * time.Minute

// Pair links one baseline evidence of the item to one current evidence of
// the same room.
type Pair struct {
	BaselineEvidenceID, CurrentEvidenceID identity.ID
}

// Command is an authorized staff decision about one item's pairing. An empty
// Pairs list records that no current view is equivalent to the baseline.
type Command struct {
	TenantID, ActorID, InspectionID, ItemIdentityID identity.ID
	Pairs                                           []Pair
	ExpectedVersion                                 int
	ClientMutationID                                string
}

// Result is the report version the decision was made against.
type Result struct {
	Report   database.ReportSnapshot
	Replayed bool
}

// Dependencies are the adapters of the operation.
type Dependencies struct {
	DB *gorm.DB
	// Within opens the tenant transaction; it defaults to the runtime-role
	// tenant runner over DB.
	Within func(context.Context, identity.ID, func(*gorm.DB) error) error
	Now    func() time.Time
}

// Setup validates the dependencies and returns the operation.
func Setup(deps Dependencies) (func(context.Context, Command) (Result, error), error) {
	if deps.DB == nil {
		return nil, fmt.Errorf("slice analysis/revise_comparison_pairing: missing database")
	}
	if deps.Within == nil {
		deps.Within = tenanttx.Runner{DB: deps.DB}.Within
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return func(ctx context.Context, command Command) (Result, error) {
		if err := validateCommand(command); err != nil {
			return Result{}, err
		}
		var result Result
		err := deps.Within(ctx, command.TenantID, func(tx *gorm.DB) error {
			var err error
			result, err = revise(ctx, tx.WithContext(ctx), command, deps.Now().UTC())
			return err
		})
		return result, err
	}, nil
}

func validateCommand(command Command) error {
	switch {
	case command.TenantID == (identity.ID{}) || command.InspectionID == (identity.ID{}):
		return apperror.New(apperror.InvalidInput, "inspectionId", "Informe a vistoria.")
	case command.ItemIdentityID == (identity.ID{}):
		return apperror.New(apperror.InvalidInput, "itemIdentityId", "Informe o item da referência.")
	case command.ExpectedVersion < 1:
		return apperror.New(apperror.InvalidInput, "expectedVersion", "Informe a versão do laudo que está sendo corrigida.")
	case strings.TrimSpace(command.ClientMutationID) == "":
		return apperror.New(apperror.InvalidInput, "clientMutationId", "Informe o identificador desta solicitação.")
	case len(command.Pairs) > maxPairs:
		return apperror.New(apperror.InvalidInput, "pairs", fmt.Sprintf("Informe no máximo %d pares de evidências por item.", maxPairs))
	}
	seen := map[identity.ID]bool{}
	for _, pair := range command.Pairs {
		if pair.BaselineEvidenceID == (identity.ID{}) || pair.CurrentEvidenceID == (identity.ID{}) {
			return apperror.New(apperror.InvalidInput, "pairs", "Informe as duas evidências de cada par.")
		}
		if seen[pair.BaselineEvidenceID] {
			return apperror.New(apperror.InvalidInput, "pairs", "Cada evidência da referência pode ser pareada uma única vez.")
		}
		seen[pair.BaselineEvidenceID] = true
	}
	return nil
}

func revise(ctx context.Context, tx *gorm.DB, command Command, now time.Time) (Result, error) {
	if tx.Dialector.Name() == "postgres" {
		// The same lock as reissueReport serializes every reissue request.
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", "report-reissue:"+command.TenantID.String()+":"+command.InspectionID.String()).Error; err != nil {
			return Result{}, err
		}
	}
	notFound := apperror.New(apperror.NotFound, "inspectionId", "Laudo não encontrado.")
	var inspection database.Inspection
	if err := tx.Where("tenant_id=? AND id=?", command.TenantID, command.InspectionID).Take(&inspection).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Result{}, notFound
		}
		return Result{}, err
	}
	var current database.ReportSnapshot
	if err := tx.Where("tenant_id=? AND inspection_id=?", command.TenantID, command.InspectionID).Order("version_number DESC").Take(&current).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Result{}, notFound
		}
		return Result{}, err
	}
	// The idempotency key is bound to the inspection, and the request content
	// is fingerprinted: replaying it answers the same result, while reusing
	// the key with another decision is a conflict, never a silent success.
	keySum := sha256.Sum256([]byte(command.TenantID.String() + ":" + command.InspectionID.String() + ":" + command.ClientMutationID))
	correlation := "revise-pairing:" + hex.EncodeToString(keySum[:])
	fingerprint := "digest=" + commandDigest(command)
	var prior database.AuditEvent
	if err := tx.Where("tenant_id=? AND action='report.comparison_pairing_revised' AND correlation_id=?", command.TenantID, correlation).Take(&prior).Error; err == nil {
		if prior.TargetID != command.InspectionID.String() || !strings.HasSuffix(prior.Reason, " "+fingerprint) {
			return Result{}, apperror.New(apperror.Conflict, "clientMutationId", "Este identificador de solicitação já foi usado com outra correção. Gere um novo identificador e tente novamente.")
		}
		return Result{Report: current, Replayed: true}, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return Result{}, err
	}
	if current.VersionNumber != command.ExpectedVersion {
		return Result{}, apperror.New(apperror.Conflict, "expectedVersion", "O laudo foi atualizado. Recarregue a versão mais recente e tente novamente.")
	}
	version, err := comparisonBaseline(tx, command.TenantID, inspection)
	if err != nil {
		return Result{}, err
	}
	var pending int64
	if err := tx.Model(&database.AuditEvent{}).Where("tenant_id=? AND action='report.reissue_requested' AND target_id=? AND outcome='QUEUED' AND occurred_at>=?", command.TenantID, command.InspectionID.String(), now.Add(-pendingTTL)).Count(&pending).Error; err != nil {
		return Result{}, err
	}
	if pending > 0 {
		return Result{}, apperror.New(apperror.Conflict, "expectedVersion", "Já existe uma solicitação de atualização deste laudo. Aguarde a conclusão e tente novamente.")
	}
	var item database.InventoryItem
	if err := tx.Where("tenant_id=? AND origin_version_id=? AND identity_id=? AND decision IN ('ACCEPTED','EDITED','ADDED')", command.TenantID, version.ID, command.ItemIdentityID).Take(&item).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Result{}, apperror.New(apperror.InvalidInput, "itemIdentityId", "O item informado não pertence à referência aprovada desta vistoria.")
		}
		return Result{}, err
	}
	var environment database.InventoryEnvironment
	if err := tx.Where("tenant_id=? AND id=?", command.TenantID, item.EnvironmentID).Take(&environment).Error; err != nil {
		return Result{}, err
	}
	requirementKey, err := roomRequirementKey(tx, command, environment)
	if err != nil {
		return Result{}, err
	}
	staff, err := staffPairs(tx, command, item, requirementKey, now)
	if err != nil {
		return Result{}, err
	}
	// Only the item's room is analysed again; the other rooms keep their
	// generation and results, and inside the room only the revised item is
	// judged again (the processor carries the others over).
	job, err := requestinventoryjobs.ReanalyzeRequirement(ctx, tx, command.TenantID, command.InspectionID, requirementKey, now)
	if err != nil {
		return Result{}, err
	}
	if err := tx.Where("tenant_id=? AND job_id=? AND item_identity_id=? AND source='STAFF'", command.TenantID, job.ID, item.IdentityID).Delete(&database.InventoryEvidencePair{}).Error; err != nil {
		return Result{}, err
	}
	for i := range staff {
		staff[i].JobID = job.ID
		if err := tx.Create(&staff[i]).Error; err != nil {
			return Result{}, err
		}
	}
	actor := command.ActorID
	if actor == (identity.ID{}) {
		actor = command.TenantID
	}
	audit := []database.AuditEvent{
		{ID: identity.NewID(), TenantID: command.TenantID, ActorID: actor, Action: "report.comparison_pairing_revised", TargetType: "inspection", TargetID: command.InspectionID.String(), Outcome: "ACCEPTED", Reason: fmt.Sprintf("itemIdentityId=%s pairs=%d %s", item.IdentityID, len(command.Pairs), fingerprint), CorrelationID: correlation, OccurredAt: now},
		{ID: identity.NewID(), TenantID: command.TenantID, ActorID: actor, Action: "report.reissue_requested", TargetType: "inspection", TargetID: command.InspectionID.String(), Outcome: "QUEUED", Reason: fmt.Sprintf("expectedVersion=%d", command.ExpectedVersion), CorrelationID: correlation, OccurredAt: now},
	}
	for i := range audit {
		if err := tx.Create(&audit[i]).Error; err != nil {
			return Result{}, err
		}
	}
	payload, err := json.Marshal(map[string]any{"inspectionId": command.InspectionID, "force": true})
	if err != nil {
		return Result{}, err
	}
	if err := messaging.AddOutbox(tx, events.Envelope[json.RawMessage]{ID: identity.NewID(), Type: "analysis.comparison_completed.v1", SchemaVersion: 1, OccurredAt: now, TenantID: command.TenantID, AggregateID: command.InspectionID, CorrelationID: correlation, Payload: payload}); err != nil {
		return Result{}, err
	}
	return Result{Report: current}, nil
}

// roomRequirementKey is the capture requirement key the inspection's COMPARE
// jobs use for the item's baseline room (a descriptive capture keys the room
// by its scope key); without a job the room cannot be compared again.
func roomRequirementKey(tx *gorm.DB, command Command, environment database.InventoryEnvironment) (string, error) {
	var job database.InventoryJob
	err := tx.Where("tenant_id=? AND inspection_id=? AND kind='COMPARE' AND environment_identity_id=?", command.TenantID, command.InspectionID, environment.IdentityID).Order("generation DESC, created_at DESC").Take(&job).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", apperror.New(apperror.InvalidState, "itemIdentityId", "O ambiente deste item não tem uma comparação que possa ser refeita.")
	}
	if err != nil {
		return "", err
	}
	return job.RequirementKey, nil
}

// comparisonBaseline returns the approved inventory version a photo
// inspection with a pinned descriptive scope compares against. Legacy,
// video and media-only comparisons cannot be revised.
func comparisonBaseline(tx *gorm.DB, tenantID identity.ID, inspection database.Inspection) (database.OriginVersion, error) {
	unavailable := apperror.New(apperror.InvalidState, "inspectionId", "A revisão de pareamento está disponível apenas em comparações de fotos com inventário aprovado.")
	if inspection.MediaKind != "PHOTO" || inspection.InventoryPromptSnapshotID == nil {
		return database.OriginVersion{}, unavailable
	}
	var scopes int64
	if err := tx.Model(&database.InspectionScopeSnapshot{}).Where("tenant_id=? AND inspection_id=?", tenantID, inspection.ID).Count(&scopes).Error; err != nil {
		return database.OriginVersion{}, err
	}
	var reference database.ReferenceSnapshot
	if err := tx.Where("tenant_id=? AND inspection_id=?", tenantID, inspection.ID).Take(&reference).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return database.OriginVersion{}, unavailable
		}
		return database.OriginVersion{}, err
	}
	if scopes == 0 || reference.ComparisonMode != "FIXED_ORIGIN" || reference.ReferenceVersionID == nil {
		return database.OriginVersion{}, unavailable
	}
	var version database.OriginVersion
	if err := tx.Where("tenant_id=? AND id=? AND has_inventory", tenantID, *reference.ReferenceVersionID).Take(&version).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return database.OriginVersion{}, unavailable
		}
		return database.OriginVersion{}, err
	}
	return version, nil
}

// staffPairs validates every pair against the item and its room and builds
// the STAFF pairs to record. A baseline evidence must be cited by the item; a
// current evidence must be a ready photo submitted for the same room. An
// empty decision records, per baseline evidence of the item, that staff found
// no equivalent current view (its current side repeats the baseline evidence
// and it never supports a conclusion).
func staffPairs(tx *gorm.DB, command Command, item database.InventoryItem, requirementKey string, now time.Time) ([]database.InventoryEvidencePair, error) {
	var evidence []database.InventoryItemEvidence
	if err := tx.Where("tenant_id=? AND item_id=?", command.TenantID, item.ID).Order("position ASC").Find(&evidence).Error; err != nil {
		return nil, err
	}
	baseline := make(map[identity.ID]database.InventoryItemEvidence, len(evidence))
	for _, row := range evidence {
		key := row.MediaID
		if row.Kind == "VIDEO_FRAME" && row.FrameID != nil {
			key = *row.FrameID
		}
		baseline[key] = row
	}
	if len(baseline) == 0 {
		return nil, apperror.New(apperror.InvalidState, "itemIdentityId", "A referência aprovada não tem evidência deste item; não há base para comparar.")
	}
	reason := "Pareamento decidido pela equipe."
	if len(command.Pairs) == 0 {
		out := make([]database.InventoryEvidencePair, 0, len(evidence))
		for _, row := range evidence {
			out = append(out, database.InventoryEvidencePair{ID: identity.NewID(), TenantID: command.TenantID, ItemIdentityID: item.IdentityID, OriginKind: row.Kind, OriginMediaID: row.MediaID, OriginFrameID: row.FrameID, CurrentKind: row.Kind, CurrentMediaID: row.MediaID, CurrentFrameID: row.FrameID, Decision: "NOT_COMPARABLE", Confirmed: false, Reason: &reason, Source: "STAFF", CreatedAt: now})
		}
		return out, nil
	}
	current, superseded, err := submittedRoomMedia(tx, command.TenantID, command.InspectionID, requirementKey)
	if err != nil {
		return nil, err
	}
	out := make([]database.InventoryEvidencePair, 0, len(command.Pairs))
	for _, pair := range command.Pairs {
		row, ok := baseline[pair.BaselineEvidenceID]
		if !ok {
			return nil, apperror.New(apperror.InvalidInput, "pairs", "A evidência da referência não pertence a este item.")
		}
		if superseded[pair.CurrentEvidenceID] {
			return nil, apperror.New(apperror.InvalidInput, "pairs", "A evidência atual foi substituída por um envio mais recente deste ambiente. Escolha uma foto do envio atual.")
		}
		if !current[pair.CurrentEvidenceID] {
			return nil, apperror.New(apperror.InvalidInput, "pairs", "A evidência atual não pertence ao mesmo ambiente nesta vistoria.")
		}
		out = append(out, database.InventoryEvidencePair{ID: identity.NewID(), TenantID: command.TenantID, ItemIdentityID: item.IdentityID, OriginKind: row.Kind, OriginMediaID: row.MediaID, OriginFrameID: row.FrameID, CurrentKind: "PHOTO", CurrentMediaID: pair.CurrentEvidenceID, Decision: "SAME_VIEW", Confirmed: true, Reason: &reason, Source: "STAFF", CreatedAt: now})
	}
	return out, nil
}

// submittedRoomMedia returns the ready photos of the newest submitted answer
// of one room (capturecore.LatestSubmittedAnswer, the same rule the
// comparison executor and the report use), and the photos of older submitted
// answers of that room, which a later submission superseded. An open
// recapture contributes nothing.
func submittedRoomMedia(tx *gorm.DB, tenantID, inspectionID identity.ID, requirementKey string) (map[identity.ID]bool, map[identity.ID]bool, error) {
	latest, err := capturecore.LatestSubmittedMedia(tx.Statement.Context, tx, tenantID, inspectionID, requirementKey)
	if err != nil {
		return nil, nil, err
	}
	var answers []database.RequirementAnswer
	if err := tx.Table("capture.requirement_answers AS a").Select("a.*").
		Joins("JOIN capture.capture_drafts AS d ON d.tenant_id=a.tenant_id AND d.id=a.draft_id").
		Joins("JOIN inspections.responsibilities AS r ON r.tenant_id=d.tenant_id AND r.id=d.responsibility_id").
		Where("a.tenant_id=? AND r.inspection_id=? AND d.status='SUBMITTED' AND a.requirement_key=?", tenantID, inspectionID, requirementKey).
		Find(&answers).Error; err != nil {
		return nil, nil, err
	}
	current := map[identity.ID]bool{}
	superseded := map[identity.ID]bool{}
	inLatest := map[identity.ID]bool{}
	for _, id := range latest {
		inLatest[id] = true
	}
	for _, answer := range answers {
		var media []identity.ID
		if json.Unmarshal(answer.MediaIDs, &media) == nil {
			for _, id := range media {
				if !inLatest[id] {
					superseded[id] = true
				}
			}
		}
	}
	if len(latest) == 0 {
		return current, superseded, nil
	}
	var ready []database.MediaObject
	if err := tx.Where("tenant_id=? AND id IN ? AND status='READY' AND media_kind='PHOTO'", tenantID, latest).Find(&ready).Error; err != nil {
		return nil, nil, err
	}
	for _, media := range ready {
		current[media.ID] = true
	}
	return current, superseded, nil
}

// commandDigest fingerprints the decision of a request: the item and its
// pairs in a stable order.
func commandDigest(command Command) string {
	pairs := make([]string, 0, len(command.Pairs))
	for _, pair := range command.Pairs {
		pairs = append(pairs, pair.BaselineEvidenceID.String()+">"+pair.CurrentEvidenceID.String())
	}
	sort.Strings(pairs)
	sum := sha256.Sum256([]byte(command.ItemIdentityID.String() + "|" + strings.Join(pairs, ",")))
	return hex.EncodeToString(sum[:])
}
