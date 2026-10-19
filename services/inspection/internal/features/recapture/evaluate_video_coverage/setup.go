// Package evaluate_video_coverage verifies recapture supplements against their pinned origin frames.
package evaluate_video_coverage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	match "inspection/services/inspection/internal/features/analysis/prepare_video_comparisons"
	recapturecore "inspection/services/inspection/internal/features/recapture/core"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/messaging"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrNotApplicable = errors.New("prepared video is not a recapture supplement")

type Dependencies struct {
	DB           *gorm.DB
	ClaimDB      *gorm.DB
	Frames       match.FrameReader
	Matcher      match.Proposer
	Verifier     match.Verifier
	Now          func() time.Time
	Lease        time.Duration
	Within       func(context.Context, identity.ID, func(*gorm.DB) error) error
	PlanRequests SupplementalPlanRequester
}

// SupplementalPlanRequester is the F3.4 seam for atomically requesting a child G2 plan.
type SupplementalPlanRequester interface {
	RequestSupplementalPlan(context.Context, *gorm.DB, identity.ID, identity.ID, identity.ID, identity.ID) error
}

type Evaluator struct {
	db, claimDB  *gorm.DB
	frames       match.FrameReader
	matcher      match.Proposer
	verifier     match.Verifier
	now          func() time.Time
	lease        time.Duration
	within       func(context.Context, identity.ID, func(*gorm.DB) error) error
	planRequests SupplementalPlanRequester
}

func Setup(deps Dependencies) (*Evaluator, error) {
	if deps.DB == nil || deps.Frames == nil || deps.Matcher == nil || deps.Verifier == nil {
		return nil, errors.New("recapture/evaluate_video_coverage: missing required dependency")
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
	return &Evaluator{db: deps.DB, claimDB: deps.ClaimDB, frames: deps.Frames, matcher: deps.Matcher, verifier: deps.Verifier, now: deps.Now, lease: deps.Lease, within: within, planRequests: deps.PlanRequests}, nil
}

// HandlePrepared routes recapture supplements to durable evaluation work.
func (e *Evaluator) HandlePrepared(ctx context.Context, tx *gorm.DB, envelope events.RawEnvelope) error {
	if e == nil || tx == nil {
		return errors.New("recapture/evaluate_video_coverage: missing handler dependency")
	}
	if envelope.Type != "media.video_prepared.v1" {
		return ErrNotApplicable
	}
	var payload events.VideoPreparedPayload
	if err := strictDecode(envelope.Payload, &payload); err != nil || payload.MediaID == (identity.ID{}) || payload.PreparationID == (identity.ID{}) {
		return fmt.Errorf("%w: malformed preparation event", messaging.ErrPermanent)
	}
	var preparation database.VideoPreparationJob
	if err := tx.WithContext(ctx).Where("tenant_id=? AND id=? AND media_id=?", envelope.TenantID, payload.PreparationID, payload.MediaID).Take(&preparation).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("%w: unknown preparation", messaging.ErrPermanent)
		}
		return err
	}
	var media database.MediaObject
	if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", envelope.TenantID, preparation.MediaID).Take(&media).Error; err != nil {
		return err
	}
	if media.RequirementKey != "video:supplement" || media.MediaKind != "VIDEO" {
		return ErrNotApplicable
	}
	if preparation.Status != "READY" {
		return fmt.Errorf("video supplement preparation pending: %w", gorm.ErrRecordNotFound)
	}
	if preparation.ManifestDigest == "" || preparation.ManifestDigest != payload.ManifestDigest {
		return fmt.Errorf("%w: supplement manifest digest mismatch", messaging.ErrPermanent)
	}
	var responsibility database.Responsibility
	if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", envelope.TenantID, media.ResponsibilityID).Take(&responsibility).Error; err != nil {
		return err
	}
	var draft database.CaptureDraft
	if err := tx.WithContext(ctx).Where("tenant_id=? AND responsibility_id=? AND kind='RECAPTURE' AND media_kind='VIDEO'", envelope.TenantID, responsibility.ID).Take(&draft).Error; err != nil {
		return ErrNotApplicable
	}
	var request database.RecaptureRequest
	if err := tx.WithContext(ctx).Where("tenant_id=? AND responsibility_id=? AND status IN ('RECEIVED','SUBMITTED')", envelope.TenantID, responsibility.ID).Take(&request).Error; err != nil {
		return err
	}
	job := database.VideoCoverageJob{ID: identity.NewDeterministicID("video-coverage-job", envelope.TenantID.String()+":"+preparation.ID.String()), TenantID: envelope.TenantID, PreparationID: preparation.ID, RequestID: request.ID, Status: "QUEUED", NextAttemptAt: e.now().UTC(), CreatedAt: e.now().UTC()}
	return tx.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "tenant_id"}, {Name: "preparation_id"}}, DoNothing: true}).Create(&job).Error
}

type Selection struct {
	ComponentID    identity.ID   `json:"componentId"`
	OriginFrameIDs []identity.ID `json:"originFrameIds"`
}
type projection struct {
	PlanID     identity.ID `json:"planId"`
	Generation int         `json:"generation"`
	Selected   []Selection `json:"selected"`
}

// CoveredComponents requires the two matcher passes to agree on the exact same visible origin/current pair.
func CoveredComponents(selected []Selection, origin, current []match.Frame, proposed, verified []match.Pair) []identity.ID {
	originByID := make(map[identity.ID]match.Frame, len(origin))
	for _, frame := range origin {
		originByID[frame.ID] = frame
	}
	currentByID := make(map[identity.ID]match.Frame, len(current))
	for _, frame := range current {
		currentByID[frame.ID] = frame
	}
	verifiedPairs := make(map[string]bool, len(verified))
	for _, pair := range verified {
		if pair.Decision == match.SameView {
			verifiedPairs[pair.ComponentID.String()+":"+pair.OriginFrameID.String()+":"+pair.CurrentFrameID.String()] = true
		}
	}
	var covered []identity.ID
	for _, item := range selected {
		allowed := make(map[identity.ID]bool, len(item.OriginFrameIDs))
		for _, id := range item.OriginFrameIDs {
			allowed[id] = true
		}
		found := false
		for _, pair := range proposed {
			o, originOK := originByID[pair.OriginFrameID]
			c, currentOK := currentByID[pair.CurrentFrameID]
			if pair.ComponentID == item.ComponentID && pair.Decision == match.SameView && originOK && currentOK && o.Visible && c.Visible && allowed[o.ID] && verifiedPairs[item.ComponentID.String()+":"+o.ID.String()+":"+c.ID.String()] {
				found = true
				break
			}
		}
		if found {
			covered = append(covered, item.ComponentID)
		}
	}
	return covered
}

func strictDecode(data []byte, target any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	return d.Decode(target)
}

// RunDue evaluates one leased supplement and commits its coverage with lease fencing.
func (e *Evaluator) RunDue(ctx context.Context) (bool, error) {
	var job database.VideoCoverageJob
	now := e.now().UTC()
	err := e.claimDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).Where("(status='QUEUED' AND next_attempt_at<=?) OR (status='PROCESSING' AND lease_until<=?)", now, now).Order("created_at,id").Take(&job).Error; err != nil {
			return err
		}
		if tx.Dialector.Name() == "postgres" {
			if err := tx.Exec("SELECT set_config('app.tenant_id', ?, true)", job.TenantID.String()).Error; err != nil {
				return err
			}
		}
		token := identity.NewID()
		until := now.Add(e.lease)
		if err := tx.Model(&job).Updates(map[string]any{"status": "PROCESSING", "lease_token": token, "lease_until": until, "attempts": job.Attempts + 1}).Error; err != nil {
			return err
		}
		job.LeaseToken = &token
		job.Attempts++
		return nil
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	covered, planID, err := e.evaluate(ctx, job)
	if err != nil {
		return true, e.fail(ctx, job, err)
	}
	return true, e.within(ctx, job.TenantID, func(tx *gorm.DB) error {
		var current database.VideoCoverageJob
		if err := tx.WithContext(ctx).Where("tenant_id=? AND id=? AND status='PROCESSING' AND lease_token=?", job.TenantID, job.ID, *job.LeaseToken).Take(&current).Error; err != nil {
			return fmt.Errorf("video coverage lease lost: %w", err)
		}
		if len(covered) > 0 {
			var request database.RecaptureRequest
			if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", job.TenantID, job.RequestID).Take(&request).Error; err != nil {
				return err
			}
			var active database.VideoPlan
			if err := tx.WithContext(ctx).Where("tenant_id=? AND inspection_id=? AND status='SEALED'", job.TenantID, request.InspectionID).Order("generation DESC").Take(&active).Error; err != nil {
				return err
			}
			if active.ID != planID {
				return errors.New("video coverage plan is no longer active")
			}
			var prepared database.VideoPreparationJob
			if err := tx.WithContext(ctx).Where("tenant_id=? AND id=? AND status='READY'", job.TenantID, job.PreparationID).Take(&prepared).Error; err != nil {
				return err
			}
			var supplement database.MediaObject
			if err := tx.WithContext(ctx).Where("tenant_id=? AND id=? AND responsibility_id=? AND requirement_key='video:supplement' AND status='READY'", job.TenantID, prepared.MediaID, preparedResponsibilityID(tx, ctx, job.TenantID, job.RequestID)).Take(&supplement).Error; err != nil {
				return err
			}
			if _, err := (recapturecore.Service{}).FinalizeVideoTx(tx.WithContext(ctx), job.TenantID, job.RequestID, covered); err != nil {
				return err
			}
			if e.planRequests != nil {
				if err := e.planRequests.RequestSupplementalPlan(ctx, tx.WithContext(ctx), job.TenantID, job.RequestID, planID, job.PreparationID); err != nil {
					return err
				}
			}
		}
		encoded, _ := json.Marshal(covered)
		res := tx.WithContext(ctx).Model(&database.VideoCoverageJob{}).Where("tenant_id=? AND id=? AND status='PROCESSING' AND lease_token=?", job.TenantID, job.ID, *job.LeaseToken).Updates(map[string]any{"status": "COMPLETED", "covered_ids": encoded, "lease_token": nil, "lease_until": nil, "error_code": ""})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return errors.New("video coverage lease lost while committing")
		}
		return nil
	})
}

func preparedResponsibilityID(tx *gorm.DB, ctx context.Context, tenantID, requestID identity.ID) identity.ID {
	var request database.RecaptureRequest
	if err := tx.WithContext(ctx).Select("responsibility_id").Where("tenant_id=? AND id=?", tenantID, requestID).Take(&request).Error; err != nil {
		return identity.ID{}
	}
	return request.ResponsibilityID
}

func (e *Evaluator) evaluate(ctx context.Context, job database.VideoCoverageJob) ([]identity.ID, identity.ID, error) {
	var draft database.CaptureDraft
	var request database.RecaptureRequest
	var preparation database.VideoPreparationJob
	var media database.MediaObject
	var p projection
	var referencePreparations []identity.ID
	err := e.within(ctx, job.TenantID, func(tx *gorm.DB) error {
		if err := tx.WithContext(ctx).Where("tenant_id=? AND id=?", job.TenantID, job.RequestID).Take(&request).Error; err != nil {
			return err
		}
		if err := tx.WithContext(ctx).Where("tenant_id=? AND responsibility_id=? AND kind='RECAPTURE' AND media_kind='VIDEO'", job.TenantID, request.ResponsibilityID).Take(&draft).Error; err != nil {
			return err
		}
		if err := tx.WithContext(ctx).Where("tenant_id=? AND id=? AND status='READY'", job.TenantID, job.PreparationID).Take(&preparation).Error; err != nil {
			return err
		}
		if err := tx.WithContext(ctx).Where("tenant_id=? AND id=? AND requirement_key='video:supplement' AND status='READY'", job.TenantID, preparation.MediaID).Take(&media).Error; err != nil {
			return err
		}
		var ref map[string]json.RawMessage
		if err := json.Unmarshal(draft.ReferencePayload, &ref); err != nil {
			return err
		}
		if err := json.Unmarshal(ref["videoRecapture"], &p); err != nil {
			return err
		}
		if p.PlanID == (identity.ID{}) || len(p.Selected) == 0 {
			return errors.New("invalid recapture video selection")
		}
		var parent database.VideoPlan
		if err := tx.WithContext(ctx).Where("tenant_id=? AND id=? AND inspection_id=? AND status='SEALED' AND generation=?", job.TenantID, p.PlanID, request.InspectionID, p.Generation).Take(&parent).Error; err != nil {
			return err
		}
		// The selection pins frames of the original submission that showed each
		// component; load them from their own preparations of the parent plan.
		ids := make([]identity.ID, 0)
		for _, item := range p.Selected {
			ids = append(ids, item.OriginFrameIDs...)
		}
		var frames []database.VideoFrame
		if err := tx.WithContext(ctx).Where("tenant_id=? AND id IN ?", job.TenantID, ids).Find(&frames).Error; err != nil {
			return err
		}
		seen := make(map[identity.ID]bool)
		for _, frame := range frames {
			if seen[frame.PreparationID] {
				continue
			}
			var member int64
			if err := tx.WithContext(ctx).Model(&database.VideoPlanPreparation{}).Where("tenant_id=? AND plan_id=? AND preparation_id=?", job.TenantID, parent.ID, frame.PreparationID).Count(&member).Error; err != nil {
				return err
			}
			if member == 0 && frame.PreparationID != parent.CurrentPreparationID {
				return fmt.Errorf("%w: recapture frame is outside the parent plan", match.ErrInvalidPlan)
			}
			seen[frame.PreparationID] = true
			referencePreparations = append(referencePreparations, frame.PreparationID)
		}
		return nil
	})
	if err != nil {
		return nil, identity.ID{}, err
	}
	var origin []match.Frame
	for _, preparationID := range referencePreparations {
		frames, err := e.frames.LoadPreparationFrames(ctx, job.TenantID, preparationID)
		if err != nil {
			return nil, identity.ID{}, err
		}
		origin = append(origin, frames...)
	}
	current, err := e.frames.LoadPreparationFrames(ctx, job.TenantID, job.PreparationID)
	if err != nil {
		return nil, identity.ID{}, err
	}
	if len(origin) == 0 || len(current) == 0 {
		return nil, p.PlanID, match.ErrInvalidPlan
	}
	selectedOrigin := make(map[identity.ID]bool)
	for _, item := range p.Selected {
		for _, id := range item.OriginFrameIDs {
			selectedOrigin[id] = true
		}
	}
	filteredOrigin := make([]match.Frame, 0, len(selectedOrigin))
	for _, frame := range origin {
		if selectedOrigin[frame.ID] {
			filteredOrigin = append(filteredOrigin, frame)
		}
	}
	if len(filteredOrigin) == 0 || len(filteredOrigin) != len(selectedOrigin) {
		return nil, p.PlanID, match.ErrInvalidPlan
	}
	proposed, err := e.matcher.Propose(ctx, filteredOrigin, current)
	if err != nil {
		return nil, p.PlanID, err
	}
	verified, err := e.verifier.Verify(ctx, filteredOrigin, current, proposed)
	if err != nil {
		return nil, p.PlanID, err
	}
	return CoveredComponents(p.Selected, filteredOrigin, current, proposed, verified), p.PlanID, nil
}

func (e *Evaluator) fail(ctx context.Context, job database.VideoCoverageJob, cause error) error {
	now := e.now().UTC()
	terminal := errors.Is(cause, match.ErrProviderTerminal) || errors.Is(cause, match.ErrInvalidPlan)
	return e.within(ctx, job.TenantID, func(tx *gorm.DB) error {
		updates := map[string]any{"lease_token": nil, "lease_until": nil, "error_code": "VIDEO_COVERAGE_RETRYABLE"}
		if terminal || job.Attempts >= 4 {
			updates["status"] = "FAILED"
			if terminal {
				updates["error_code"] = "VIDEO_COVERAGE_TERMINAL"
			}
		} else {
			delays := []time.Duration{10 * time.Second, time.Minute, 5 * time.Minute}
			updates["status"] = "QUEUED"
			updates["next_attempt_at"] = now.Add(delays[min(job.Attempts-1, len(delays)-1)])
		}
		res := tx.WithContext(ctx).Model(&database.VideoCoverageJob{}).Where("tenant_id=? AND id=? AND status='PROCESSING' AND lease_token=?", job.TenantID, job.ID, *job.LeaseToken).Updates(updates)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return errors.New("video coverage lease lost while recording failure")
		}
		return nil
	})
}
