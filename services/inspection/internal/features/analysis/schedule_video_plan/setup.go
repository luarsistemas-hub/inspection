// Package schedule_video_plan consumes video lifecycle events and durably builds
// one immutable comparison plan for each submitted video generation.
package schedule_video_plan

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	buildplan "inspection/services/inspection/internal/features/analysis/prepare_video_comparisons"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"
	"inspection/services/inspection/internal/platform/observability"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrInvalidEvent = errors.New("invalid video plan scheduling event")
var ErrLeaseLost = errors.New("video plan lease lost")

type Dependencies struct {
	DB       *gorm.DB
	ClaimDB  *gorm.DB
	Frames   buildplan.FrameReader
	Matcher  buildplan.Proposer
	Verifier buildplan.Verifier
	Plans    buildplan.PlanWriter
	Now      func() time.Time
	Lease    time.Duration
	Within   func(context.Context, identity.ID, func(*gorm.DB) error) error
	Metrics  *observability.Metrics
	MaxCalls int
}

// SupplementalRequester creates one durable child plan after verified video recapture coverage.
type SupplementalRequester struct{}

// RequestSupplementalPlan records the G+1 build request in the coverage transaction.
func (r SupplementalRequester) RequestSupplementalPlan(ctx context.Context, tx *gorm.DB, tenantID, requestID, parentID, preparationID identity.ID) error {
	if tx == nil || tenantID == (identity.ID{}) || requestID == (identity.ID{}) || parentID == (identity.ID{}) || preparationID == (identity.ID{}) {
		return fmt.Errorf("analysis/schedule_video_plan: invalid supplemental request")
	}
	var parent database.VideoPlan
	// Lock the parent so concurrent supplements serialize on one child plan.
	if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=? AND status='SEALED'", tenantID, parentID).Take(&parent).Error; err != nil {
		return err
	}
	var children int64
	if err := tx.WithContext(ctx).Model(&database.VideoPlan{}).Where("tenant_id=? AND parent_plan_id=? AND status<>'FAILED'", tenantID, parent.ID).Count(&children).Error; err != nil {
		return err
	}
	if children > 0 {
		// One accepted generation per parent; later supplements stay as
		// evidence history of the request.
		return nil
	}
	var request database.RecaptureRequest
	if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", tenantID, requestID).Take(&request).Error; err != nil {
		return err
	}
	if request.InspectionID != parent.InspectionID || request.Status != "SUBMITTED" {
		return fmt.Errorf("analysis/schedule_video_plan: recapture request is not finalized for parent inspection")
	}
	var preparation database.VideoPreparationJob
	if err := tx.WithContext(ctx).Where("tenant_id=? AND id=? AND status='READY'", tenantID, preparationID).Take(&preparation).Error; err != nil {
		return err
	}
	if preparation.SubmissionID == nil {
		return fmt.Errorf("analysis/schedule_video_plan: supplement is not linked to a submission")
	}
	var supplement database.MediaObject
	if err := tx.WithContext(ctx).Where("tenant_id=? AND id=? AND responsibility_id=? AND requirement_key='video:supplement' AND media_kind='VIDEO' AND status='READY'", tenantID, preparation.MediaID, request.ResponsibilityID).Take(&supplement).Error; err != nil {
		return err
	}
	now := time.Now().UTC()
	childID := identity.NewDeterministicID("inspection-video-plan", tenantID.String()+":"+preparation.SubmissionID.String()+":"+fmt.Sprint(parent.Generation+1))
	row := database.VideoPlan{ID: childID, TenantID: tenantID, InspectionID: parent.InspectionID, SubmissionID: *preparation.SubmissionID, ParentPlanID: &parent.ID, ReferenceVersionID: parent.ReferenceVersionID, OriginPreparationID: parent.OriginPreparationID, CurrentPreparationID: parent.CurrentPreparationID, Generation: parent.Generation + 1, Status: "BUILDING", NextAttemptAt: now, CreatedAt: now}
	if err := tx.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "tenant_id"}, {Name: "submission_id"}, {Name: "generation"}}, DoNothing: true}).Create(&row).Error; err != nil {
		return err
	}
	relationship := database.VideoPlanPreparation{TenantID: tenantID, PlanID: childID, PreparationID: preparation.ID, Role: "SUPPLEMENT", CreatedAt: now}
	return tx.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "tenant_id"}, {Name: "plan_id"}, {Name: "preparation_id"}}, DoNothing: true}).Create(&relationship).Error
}

// Scheduler owns the transactional inbox handler and one leased plan executor.
type Scheduler struct {
	db      *gorm.DB
	claimDB *gorm.DB
	build   func(context.Context, buildplan.PlanRequest) (buildplan.Plan, error)
	now     func() time.Time
	lease   time.Duration
	within  func(context.Context, identity.ID, func(*gorm.DB) error) error
}

// Setup validates the ports and constructs the plan builder and durable writer.
func Setup(deps Dependencies) (*Scheduler, error) {
	if deps.DB == nil || deps.Frames == nil || deps.Matcher == nil || deps.Verifier == nil {
		return nil, fmt.Errorf("analysis/schedule_video_plan: missing required dependency")
	}
	if deps.Plans == nil {
		deps.Plans = buildplan.DBPlanWriter{DB: deps.DB, Now: deps.Now}
	}
	build, err := buildplan.Setup(buildplan.Dependencies{Frames: deps.Frames, Matcher: deps.Matcher, Verifier: deps.Verifier, Plans: deps.Plans, Metrics: deps.Metrics, MaxCalls: deps.MaxCalls})
	if err != nil {
		return nil, err
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.Lease <= 0 {
		deps.Lease = 2 * time.Minute
	}
	if deps.ClaimDB == nil {
		deps.ClaimDB = deps.DB
	}
	within := deps.Within
	if within == nil {
		within = tenanttx.Runner{DB: deps.DB}.Within
	}
	return &Scheduler{db: deps.DB, claimDB: deps.ClaimDB, build: build, now: deps.Now, lease: deps.Lease, within: within}, nil
}

// Handle applies a validated event inside messaging.Consumer's inbox transaction.
func (s *Scheduler) Handle(ctx context.Context, tx *gorm.DB, envelope events.RawEnvelope) error {
	if s == nil || tx == nil {
		return errors.New("analysis/schedule_video_plan: missing handler dependency")
	}
	switch envelope.Type {
	case "capture.video_submitted.v1":
		return s.handleSubmission(ctx, tx, envelope)
	case "media.video_prepared.v1":
		return s.handlePrepared(ctx, tx, envelope)
	default:
		return permanent(fmt.Errorf("%w: unsupported message type", ErrInvalidEvent))
	}
}

type videoSubmitted struct {
	DraftID           identity.ID `json:"draftId"`
	SubmissionID      identity.ID `json:"submissionId"`
	ResponsibilityID  identity.ID `json:"responsibilityId"`
	MediaID           identity.ID `json:"mediaId"`
	PreparationStatus string      `json:"preparationStatus"`
}

func (s *Scheduler) handleSubmission(ctx context.Context, tx *gorm.DB, envelope events.RawEnvelope) error {
	var payload videoSubmitted
	if err := strictDecode(envelope.Payload, &payload); err != nil || payload.SubmissionID == (identity.ID{}) || payload.ResponsibilityID == (identity.ID{}) || payload.MediaID == (identity.ID{}) {
		return permanent(fmt.Errorf("%w: malformed submission", ErrInvalidEvent))
	}
	var submission database.SubmissionVersion
	if err := tx.WithContext(ctx).Where("tenant_id=? AND id=? AND responsibility_id=?", envelope.TenantID, payload.SubmissionID, payload.ResponsibilityID).Take(&submission).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return permanent(fmt.Errorf("%w: unknown submission", ErrInvalidEvent))
		}
		return err
	}
	var media database.MediaObject
	if err := tx.WithContext(ctx).Where("tenant_id=? AND id=? AND responsibility_id=? AND media_kind='VIDEO'", envelope.TenantID, payload.MediaID, payload.ResponsibilityID).Take(&media).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return permanent(fmt.Errorf("%w: submission media mismatch", ErrInvalidEvent))
		}
		return err
	}
	var submittedPayload struct {
		MediaKind string      `json:"mediaKind"`
		MediaID   identity.ID `json:"mediaId"`
	}
	if json.Unmarshal(submission.Payload, &submittedPayload) != nil || submittedPayload.MediaKind != "VIDEO" || submittedPayload.MediaID != media.ID {
		return permanent(fmt.Errorf("%w: immutable submission payload mismatch", ErrInvalidEvent))
	}
	var responsibility database.Responsibility
	if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", envelope.TenantID, payload.ResponsibilityID).Take(&responsibility).Error; err != nil {
		return err
	}
	var inspection database.Inspection
	if err := tx.WithContext(ctx).Where("tenant_id=? AND id=? AND media_kind='VIDEO'", envelope.TenantID, responsibility.InspectionID).Take(&inspection).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	var recaptureDraft database.CaptureDraft
	if err := tx.WithContext(ctx).Where("tenant_id=? AND responsibility_id=? AND kind='RECAPTURE' AND media_kind='VIDEO'", envelope.TenantID, responsibility.ID).Take(&recaptureDraft).Error; err == nil {
		return nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	var preparation database.VideoPreparationJob
	if err := tx.WithContext(ctx).Where("tenant_id=? AND media_id=? AND submission_id=?", envelope.TenantID, media.ID, submission.ID).Order("created_at DESC, id DESC").Take(&preparation).Error; err != nil {
		return err
	}
	if preparation.SubmissionID == nil || *preparation.SubmissionID != submission.ID {
		return permanent(fmt.Errorf("%w: preparation is not linked to immutable submission", ErrInvalidEvent))
	}
	return s.ensurePlan(ctx, tx, envelope.TenantID, inspection, submission, preparation)
}

func (s *Scheduler) handlePrepared(ctx context.Context, tx *gorm.DB, envelope events.RawEnvelope) error {
	var payload events.VideoPreparedPayload
	if err := strictDecode(envelope.Payload, &payload); err != nil || payload.MediaID == (identity.ID{}) || payload.PreparationID == (identity.ID{}) {
		return permanent(fmt.Errorf("%w: malformed preparation", ErrInvalidEvent))
	}
	var preparation database.VideoPreparationJob
	if err := tx.WithContext(ctx).Where("tenant_id=? AND id=? AND media_id=?", envelope.TenantID, payload.PreparationID, payload.MediaID).Take(&preparation).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return permanent(fmt.Errorf("%w: unknown preparation", ErrInvalidEvent))
		}
		return err
	}
	if preparation.Status != "READY" {
		return fmt.Errorf("preparation pending: %w", gorm.ErrRecordNotFound)
	}
	if preparation.ManifestDigest == "" || preparation.ManifestDigest != payload.ManifestDigest {
		if preparation.SubmissionID != nil {
			result := tx.WithContext(ctx).Model(&database.VideoPlan{}).Where("tenant_id=? AND submission_id=? AND status='BUILDING'", envelope.TenantID, *preparation.SubmissionID).Updates(map[string]any{"status": "FAILED", "error_code": "VIDEO_MANIFEST_DIGEST_MISMATCH", "lease_token": nil, "lease_until": nil})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected > 0 {
				return permanent(fmt.Errorf("%w: manifest digest mismatch", ErrInvalidEvent))
			}
		}
		return permanent(fmt.Errorf("%w: manifest digest mismatch", ErrInvalidEvent))
	}
	if preparation.SubmissionID == nil {
		return nil
	}
	var submission database.SubmissionVersion
	if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", envelope.TenantID, *preparation.SubmissionID).Take(&submission).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return permanent(fmt.Errorf("%w: unknown submission", ErrInvalidEvent))
		}
		return err
	}
	var responsibility database.Responsibility
	if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", envelope.TenantID, submission.ResponsibilityID).Take(&responsibility).Error; err != nil {
		return err
	}
	var recaptureDraft database.CaptureDraft
	if err := tx.WithContext(ctx).Where("tenant_id=? AND responsibility_id=? AND kind='RECAPTURE' AND media_kind='VIDEO'", envelope.TenantID, responsibility.ID).Take(&recaptureDraft).Error; err == nil {
		return nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	var inspection database.Inspection
	if err := tx.WithContext(ctx).Where("tenant_id=? AND id=? AND media_kind='VIDEO'", envelope.TenantID, responsibility.InspectionID).Take(&inspection).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	return s.ensurePlan(ctx, tx, envelope.TenantID, inspection, submission, preparation)
}

func (s *Scheduler) ensurePlan(ctx context.Context, tx *gorm.DB, tenantID identity.ID, inspection database.Inspection, submission database.SubmissionVersion, preparation database.VideoPreparationJob) error {
	var existing database.VideoPlan
	err := tx.WithContext(ctx).Where("tenant_id=? AND submission_id=? AND generation=1", tenantID, submission.ID).Take(&existing).Error
	if err == nil {
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	var reference database.ReferenceSnapshot
	if err := tx.WithContext(ctx).Where("tenant_id=? AND inspection_id=?", tenantID, inspection.ID).Take(&reference).Error; err != nil {
		return err
	}
	var version *database.OriginVersion
	if reference.ReferenceVersionID != nil {
		var origin database.OriginVersion
		if err := tx.WithContext(ctx).Where("tenant_id=? AND id=? AND media_kind='VIDEO'", tenantID, *reference.ReferenceVersionID).Take(&origin).Error; err != nil {
			return err
		}
		version = &origin
	}
	now := s.now().UTC()
	planID := identity.NewDeterministicID("inspection-video-plan", tenantID.String()+":"+submission.ID.String()+":1")
	row := database.VideoPlan{ID: planID, TenantID: tenantID, InspectionID: inspection.ID, SubmissionID: submission.ID, ReferenceVersionID: reference.ReferenceVersionID, CurrentPreparationID: preparation.ID, Generation: 1, PlanDigest: "", Status: "BUILDING", NextAttemptAt: now, CreatedAt: now}
	if version != nil {
		row.OriginPreparationID = version.VideoPreparationID
	}
	return tx.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "tenant_id"}, {Name: "submission_id"}, {Name: "generation"}}, DoNothing: true}).Create(&row).Error
}

func strictDecode(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func permanent(err error) error { return fmt.Errorf("%w: %w", messaging.ErrPermanent, err) }

// RunDue leases and executes at most one plan.
func (s *Scheduler) RunDue(ctx context.Context) (bool, error) {
	var job database.VideoPlan
	var inspection database.Inspection
	var submission database.SubmissionVersion
	var reference *identity.ID
	var originPreparation *identity.ID
	token := identity.NewID()
	now := s.now().UTC()
	err := s.claimDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		query := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).Where("status='BUILDING' AND next_attempt_at<=? AND (lease_until IS NULL OR lease_until<=?)", now, now).Order("created_at,id").Take(&job)
		if query.Error != nil {
			return query.Error
		}
		if tx.Dialector.Name() == "postgres" {
			if err := tx.Exec("SELECT set_config('app.tenant_id', ?, true)", job.TenantID.String()).Error; err != nil {
				return err
			}
		}
		until := now.Add(s.lease)
		attempts := job.Attempts + 1
		if err := tx.Model(&job).Updates(map[string]any{"lease_token": token, "lease_until": until, "attempts": attempts}).Error; err != nil {
			return err
		}
		job.Attempts = attempts
		if err := tx.Where("tenant_id=? AND id=?", job.TenantID, job.InspectionID).Take(&inspection).Error; err != nil {
			return err
		}
		if err := tx.Where("tenant_id=? AND id=?", job.TenantID, job.SubmissionID).Take(&submission).Error; err != nil {
			return err
		}
		reference = job.ReferenceVersionID
		originPreparation = job.OriginPreparationID
		return nil
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	plan, planErr := s.planRequest(ctx, job, inspection, submission, reference, originPreparation, token)
	if planErr != nil {
		return true, s.finishFailure(ctx, job, token, planErr)
	}
	if err = s.buildWithLease(ctx, job, token, *plan); err != nil {
		return true, s.finishFailure(ctx, job, token, err)
	}
	return true, nil
}

func (s *Scheduler) buildWithLease(ctx context.Context, job database.VideoPlan, token identity.ID, request buildplan.PlanRequest) error {
	buildCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		interval := max(s.lease/3, time.Millisecond)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-buildCtx.Done():
				done <- nil
				return
			case <-ticker.C:
				until := s.now().UTC().Add(s.lease)
				err := s.within(buildCtx, job.TenantID, func(tx *gorm.DB) error {
					result := tx.WithContext(buildCtx).Model(&database.VideoPlan{}).
						Where("tenant_id=? AND id=? AND status='BUILDING' AND lease_token=?", job.TenantID, job.ID, token).
						Update("lease_until", until)
					if result.Error != nil {
						return result.Error
					}
					if result.RowsAffected != 1 {
						return ErrLeaseLost
					}
					return nil
				})
				if err != nil {
					// A renewal interrupted by our own cancellation is not a lost lease.
					if buildCtx.Err() != nil {
						err = nil
					}
					done <- err
					cancel()
					return
				}
			}
		}
	}()
	_, buildErr := s.build(planRunContext(buildCtx, job, token), request)
	cancel()
	leaseErr := <-done
	if buildErr != nil && leaseErr != nil {
		return leaseErr
	}
	return buildErr
}

func planRunContext(ctx context.Context, job database.VideoPlan, leaseToken identity.ID) context.Context {
	correlation := observability.CorrelationFromContext(ctx)
	// A leased reanalysis has no incoming event. The plan is its durable trigger.
	correlation.EventID = job.ID.String()
	correlation.CorrelationID = job.ID.String()
	correlation.TenantID = job.TenantID.String()
	correlation.JobID = job.ID.String()
	correlation.InspectionID = job.InspectionID.String()
	correlation.ExecutionID = leaseToken.String()
	correlation.Attempt = job.Attempts
	correlation.ReplayGeneration = job.Generation
	return observability.WithCorrelation(ctx, correlation)
}

func (s *Scheduler) planRequest(ctx context.Context, job database.VideoPlan, inspection database.Inspection, submission database.SubmissionVersion, reference, originPreparation *identity.ID, token identity.ID) (*buildplan.PlanRequest, error) {
	currentPreparationID := job.CurrentPreparationID
	var supplementPreparationID *identity.ID
	if job.ParentPlanID != nil {
		var linked database.VideoPlanPreparation
		if err := s.within(ctx, job.TenantID, func(tx *gorm.DB) error {
			return tx.Where("tenant_id=? AND plan_id=? AND role='SUPPLEMENT'", job.TenantID, job.ID).Take(&linked).Error
		}); err != nil {
			return nil, err
		}
		supplementPreparationID = &linked.PreparationID
		currentPreparationID = linked.PreparationID
	}
	var current database.VideoPreparationJob
	if err := s.within(ctx, job.TenantID, func(tx *gorm.DB) error {
		return tx.WithContext(ctx).Where("tenant_id=? AND id=?", job.TenantID, currentPreparationID).Take(&current).Error
	}); err != nil {
		return nil, err
	}
	if current.Status == "FAILED" || current.Status == "CANCELED" {
		if current.ErrorCode == "SENSITIVE_CONTENT" {
			return nil, errSensitivePreparation
		}
		return nil, errPreparationFailed
	}
	if current.Status != "READY" {
		return nil, errDeferred
	}
	if current.SubmissionID == nil || *current.SubmissionID != submission.ID {
		return nil, permanent(fmt.Errorf("%w: preparation submission mismatch", ErrInvalidEvent))
	}
	if reference != nil {
		if originPreparation == nil {
			return nil, permanent(fmt.Errorf("%w: pinned origin has no video preparation", ErrInvalidEvent))
		}
		var origin database.VideoPreparationJob
		if err := s.within(ctx, job.TenantID, func(tx *gorm.DB) error {
			return tx.WithContext(ctx).Where("tenant_id=? AND id=?", job.TenantID, *originPreparation).Take(&origin).Error
		}); err != nil {
			return nil, err
		}
		if origin.Status == "FAILED" || origin.Status == "CANCELED" {
			if origin.ErrorCode == "SENSITIVE_CONTENT" {
				return nil, errSensitivePreparation
			}
			return nil, errPreparationFailed
		}
		if origin.Status != "READY" {
			return nil, errDeferred
		}
	}
	componentPreparation := current.ID
	if reference != nil {
		componentPreparation = *originPreparation
	}
	components := make([]buildplan.Component, 0)
	err := s.within(ctx, job.TenantID, func(tx *gorm.DB) error {
		var scopes []database.VideoScopeItem
		if err := tx.WithContext(ctx).Where("tenant_id=? AND preparation_id=?", job.TenantID, componentPreparation).Order("created_at,id").Find(&scopes).Error; err != nil {
			return err
		}
		seen := make(map[identity.ID]bool)
		for _, scope := range scopes {
			if !seen[scope.ID] {
				seen[scope.ID] = true
				components = append(components, buildplan.Component{ID: scope.ID})
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(components) == 0 {
		return nil, permanent(fmt.Errorf("%w: preparation has no scope components", ErrInvalidEvent))
	}
	plan := buildplan.Plan{ID: job.ID, TenantID: job.TenantID, InspectionID: inspection.ID, AssetID: inspection.AssetID, SubmissionID: submission.ID, ReferenceVersionID: reference, ParentPlanID: job.ParentPlanID, OriginPreparationID: originPreparation, CurrentPreparationID: job.CurrentPreparationID, SupplementPreparationID: supplementPreparationID, Generation: job.Generation, CurrentGeneration: current.Generation, LeaseToken: &token}
	request := &buildplan.PlanRequest{Plan: plan, Components: components}
	if job.ParentPlanID != nil {
		var parent database.VideoPlan
		if err := s.within(ctx, job.TenantID, func(tx *gorm.DB) error {
			return tx.Where("tenant_id=? AND id=? AND status='SEALED'", job.TenantID, *job.ParentPlanID).Take(&parent).Error
		}); err != nil {
			return nil, err
		}
		var req database.RecaptureRequest
		if err := s.within(ctx, job.TenantID, func(tx *gorm.DB) error {
			return tx.Where("tenant_id=? AND inspection_id=? AND status IN ('SUBMITTED','RECEIVED')", job.TenantID, inspection.ID).Order("created_at DESC").Take(&req).Error
		}); err != nil {
			return nil, err
		}
		var draft database.CaptureDraft
		if err := s.within(ctx, job.TenantID, func(tx *gorm.DB) error {
			return tx.Where("tenant_id=? AND responsibility_id=? AND kind='RECAPTURE' AND media_kind='VIDEO'", job.TenantID, req.ResponsibilityID).Take(&draft).Error
		}); err != nil {
			return nil, err
		}
		var payload struct {
			VideoRecapture struct {
				PlanID     identity.ID `json:"planId"`
				Generation int         `json:"generation"`
				Selected   []struct {
					ComponentID identity.ID `json:"componentId"`
				} `json:"selected"`
			} `json:"videoRecapture"`
		}
		var ref map[string]json.RawMessage
		if json.Unmarshal(draft.ReferencePayload, &ref) != nil || json.Unmarshal(ref["videoRecapture"], &payload.VideoRecapture) != nil || payload.VideoRecapture.PlanID != *job.ParentPlanID || payload.VideoRecapture.Generation+1 != job.Generation {
			return nil, permanent(fmt.Errorf("%w: invalid supplement projection", ErrInvalidEvent))
		}
		selected := make(map[identity.ID]bool, len(payload.VideoRecapture.Selected))
		var corrected []database.RecaptureRequirement
		if err := s.within(ctx, job.TenantID, func(tx *gorm.DB) error {
			return tx.Where("tenant_id=? AND request_id=? AND status='CORRECTED'", job.TenantID, req.ID).Find(&corrected).Error
		}); err != nil {
			return nil, err
		}
		coveredKeys := make(map[string]bool, len(corrected))
		for _, item := range corrected {
			coveredKeys[item.RequirementKey] = true
		}
		for _, item := range payload.VideoRecapture.Selected {
			if coveredKeys["video:"+item.ComponentID.String()] {
				selected[item.ComponentID] = true
			}
		}
		if len(selected) == 0 {
			return nil, permanent(fmt.Errorf("%w: empty supplement projection", ErrInvalidEvent))
		}
		var parentItems []database.VideoPlanItem
		if err := s.within(ctx, job.TenantID, func(tx *gorm.DB) error {
			return tx.Where("tenant_id=? AND plan_id=?", job.TenantID, *job.ParentPlanID).Order("requirement_key ASC").Find(&parentItems).Error
		}); err != nil {
			return nil, err
		}
		// Each recaptured component is compared with the parent plan's current
		// frames of that component.
		request.Components = request.Components[:0]
		for _, item := range parentItems {
			if !selected[item.ComponentID] {
				continue
			}
			var prior []identity.ID
			_ = json.Unmarshal(item.CurrentFrameIDs, &prior)
			request.Components = append(request.Components, buildplan.Component{ID: item.ComponentID, PriorFrameIDs: prior})
		}
		for _, item := range parentItems {
			if selected[item.ComponentID] {
				continue
			}
			if item.CarriedResultJobID != nil {
				request.CarriedItems = append(request.CarriedItems, buildplan.PlanItem{ComponentID: item.ComponentID, Outcome: item.Outcome, Reason: item.Reason, InputDigest: item.InputDigest, CarriedResultJobID: item.CarriedResultJobID})
				continue
			}
			var prior database.ComparisonJob
			if err := s.within(ctx, job.TenantID, func(tx *gorm.DB) error {
				return tx.Where("tenant_id=? AND video_plan_id=? AND video_plan_item_id=? AND status IN ('COMPLETED','INCONCLUSIVE','FAILED')", job.TenantID, *job.ParentPlanID, item.ID).Order("video_core_group DESC").Take(&prior).Error
			}); err != nil {
				return nil, err
			}
			request.CarriedItems = append(request.CarriedItems, buildplan.PlanItem{ComponentID: item.ComponentID, Outcome: item.Outcome, Reason: item.Reason, InputDigest: item.InputDigest, CarriedResultJobID: &prior.ID})
		}
	}
	return request, nil
}

var errDeferred = errors.New("video plan prerequisite is not ready")
var errSensitivePreparation = errors.New("sensitive content preparation blocked")
var errPreparationFailed = errors.New("video preparation failed")

func (s *Scheduler) finishFailure(ctx context.Context, job database.VideoPlan, token identity.ID, cause error) error {
	now := s.now().UTC()
	terminal := errors.Is(cause, ErrInvalidEvent) || errors.Is(cause, buildplan.ErrInvalidPlan) || errors.Is(cause, buildplan.ErrProviderTerminal)
	updates := map[string]any{"lease_token": nil, "lease_until": nil, "error_code": "VIDEO_PLAN_FAILED"}
	if errors.Is(cause, errSensitivePreparation) || errors.Is(cause, errPreparationFailed) {
		code := "PREPARATION_FAILED"
		inspectionStatus := "PROCESSING_FAILED"
		if errors.Is(cause, errSensitivePreparation) {
			code = "SENSITIVE_CONTENT"
			inspectionStatus = "REVIEW_REQUIRED"
		}
		updates["status"] = "BLOCKED"
		updates["error_code"] = code
		updates["next_attempt_at"] = now
		return s.within(ctx, job.TenantID, func(tx *gorm.DB) error {
			result := tx.WithContext(ctx).Model(&database.VideoPlan{}).Where("tenant_id=? AND id=? AND status='BUILDING' AND lease_token=?", job.TenantID, job.ID, token).Updates(updates)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return fmt.Errorf("%w while recording failure", ErrLeaseLost)
			}
			result = tx.WithContext(ctx).Model(&database.Inspection{}).Where("tenant_id=? AND id=? AND status='ANALYZING'", job.TenantID, job.InspectionID).Updates(map[string]any{"status": inspectionStatus, "version": gorm.Expr("version + 1"), "updated_at": now})
			return result.Error
		})
	} else if errors.Is(cause, errDeferred) {
		updates["next_attempt_at"] = now.Add(10 * time.Second)
		updates["error_code"] = ""
		updates["attempts"] = max(job.Attempts-1, 0)
	} else if terminal || job.Attempts >= 4 {
		updates["status"] = "FAILED"
		updates["next_attempt_at"] = now
		if errors.Is(cause, buildplan.ErrProviderTerminal) {
			updates["error_code"] = "VIDEO_MATCH_CONFIGURATION"
		}
	} else {
		delays := [...]time.Duration{10 * time.Second, time.Minute, 5 * time.Minute}
		updates["next_attempt_at"] = now.Add(delays[min(job.Attempts-1, len(delays)-1)])
		updates["error_code"] = "VIDEO_PLAN_RETRYABLE"
	}
	return s.within(ctx, job.TenantID, func(tx *gorm.DB) error {
		result := tx.WithContext(ctx).Model(&database.VideoPlan{}).Where("tenant_id=? AND id=? AND status='BUILDING' AND lease_token=?", job.TenantID, job.ID, token).Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("%w while recording failure", ErrLeaseLost)
		}
		return nil
	})
}
