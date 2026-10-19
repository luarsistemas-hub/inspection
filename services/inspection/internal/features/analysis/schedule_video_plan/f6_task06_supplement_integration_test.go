//go:build integration

package schedule_video_plan

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	"inspection/services/inspection/internal/features/analysis/prepare_video_comparisons"
	invitationcore "inspection/services/inspection/internal/features/invitations/core"
	recapturecore "inspection/services/inspection/internal/features/recapture/core"
	"inspection/services/inspection/internal/features/recapture/evaluate_video_coverage"
	"inspection/services/inspection/internal/features/recapture/process_deadline"
	reportcore "inspection/services/inspection/internal/features/reports/core"
	"inspection/services/inspection/internal/features/reports/generate_snapshot"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

func TestVV_IT_236BathroomOnlySupplementIsAcceptedWithoutKitchenRefilming(t *testing.T) {
	s := newTask06Supplement(t, task06Labels("kitchen/sink", "bathroom/shower"))
	kitchen, bathroom := s.component(t, "kitchen/sink"), s.component(t, "bathroom/shower")
	request := s.request(t, "vv-it-236", time.Now().UTC().Add(24*time.Hour), "bathroom/shower")
	// A 20-second supplement that only films the bathroom.
	supplement := s.submitSupplement(t, request, []task06Shot{{environment: "bathroom", component: "shower", matches: "bathroom/shower", durationMicros: 20_000_000}})
	if job := s.coverageJob(t, supplement.preparation); job.Status != "COMPLETED" {
		t.Fatalf("coverage job status=%s error=%s", job.Status, job.ErrorCode)
	}
	row, statuses := s.recaptureState(t, request)
	if row.Status != "SUBMITTED" || statuses["video:"+bathroom.id.String()] != "CORRECTED" {
		t.Fatalf("bathroom-only supplement request=%s requirements=%v; want submitted with bathroom corrected", row.Status, statuses)
	}
	g2 := s.runPlan(t, 2)
	if g2.Status != "SEALED" {
		t.Fatalf("G2 status=%s error=%s", g2.Status, g2.ErrorCode)
	}
	var items []database.VideoPlanItem
	if err := s.h.AdminDB.Where("tenant_id=? AND plan_id=?", s.fixture.TenantID, g2.ID).Find(&items).Error; err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		switch item.ComponentID {
		case kitchen.id:
			if item.CarriedResultJobID == nil {
				t.Fatalf("kitchen was recomputed instead of carried: %+v", item)
			}
		case bathroom.id:
			if item.CarriedResultJobID != nil || item.Outcome != "MATCHED" {
				t.Fatalf("bathroom was not compared with the supplement: %+v", item)
			}
		}
	}
	var kitchenJobs int64
	if err := s.h.AdminDB.Model(&database.ComparisonJob{}).Where("tenant_id=? AND video_plan_id=? AND requirement_key=?", s.fixture.TenantID, g2.ID, "video:"+kitchen.id.String()).Count(&kitchenJobs).Error; err != nil {
		t.Fatal(err)
	}
	if kitchenJobs != 0 {
		t.Fatalf("G2 created %d kitchen jobs; the participant was not asked to refilm it", kitchenJobs)
	}
}

func TestVV_IT_242SupplementShowingKitchenLeavesRequestedBathroomUnresolved(t *testing.T) {
	s := newTask06Supplement(t, task06Labels("kitchen/sink", "bathroom/shower"))
	bathroom := s.component(t, "bathroom/shower")
	request := s.request(t, "vv-it-242", time.Now().UTC().Add(24*time.Hour), "bathroom/shower")
	supplement := s.submitSupplement(t, request, []task06Shot{{environment: "kitchen", component: "sink", matches: "kitchen/sink"}})
	job := s.coverageJob(t, supplement.preparation)
	var covered []identity.ID
	_ = json.Unmarshal(job.CoveredIDs, &covered)
	if job.Status != "COMPLETED" || len(covered) != 0 {
		t.Fatalf("coverage job status=%s covered=%v; want completed without covering the bathroom", job.Status, covered)
	}
	row, statuses := s.recaptureState(t, request)
	if row.Status == "SUBMITTED" || statuses["video:"+bathroom.id.String()] == "CORRECTED" {
		t.Fatalf("kitchen footage fulfilled the bathroom request: request=%s requirements=%v", row.Status, statuses)
	}
	if _, exists := s.childPlan(t); exists {
		t.Fatal("unmatched supplement created a G2 plan")
	}
}

func TestVV_IT_603SupplementWithoutMatchingScopeKeepsRecaptureUnfulfilled(t *testing.T) {
	s := newTask06Supplement(t, task06Labels("kitchen/sink", "bathroom/shower"))
	bathroom := s.component(t, "bathroom/shower")
	request := s.request(t, "vv-it-603", time.Now().UTC().Add(24*time.Hour), "bathroom/shower")
	// The receipt shows a room that matches no requested scope.
	supplement := s.submitSupplement(t, request, []task06Shot{{environment: "garage", component: "door"}})
	if job := s.coverageJob(t, supplement.preparation); job.Status != "COMPLETED" || string(job.CoveredIDs) != "null" && string(job.CoveredIDs) != "[]" {
		t.Fatalf("coverage job status=%s covered=%s; want completed with nothing covered", job.Status, job.CoveredIDs)
	}
	row, statuses := s.recaptureState(t, request)
	if row.Status != "RECEIVED" || statuses["video:"+bathroom.id.String()] == "CORRECTED" {
		t.Fatalf("unmatched receipt changed recapture to %s requirements=%v; want RECEIVED and unresolved", row.Status, statuses)
	}
}

func TestVV_IT_250SupplementCoveringKitchenAndBathResolvesOnlyThoseAndKeepsBedroomPending(t *testing.T) {
	s := newTask06Supplement(t, task06Labels("kitchen/sink", "bathroom/bath", "bedroom/window"))
	kitchen, bath, bedroom := s.component(t, "kitchen/sink"), s.component(t, "bathroom/bath"), s.component(t, "bedroom/window")
	request := s.request(t, "vv-it-250", time.Now().UTC().Add(24*time.Hour), "kitchen/sink", "bathroom/bath", "bedroom/window")
	supplement := s.submitSupplement(t, request, []task06Shot{
		{environment: "kitchen", component: "sink", matches: "kitchen/sink"},
		{environment: "bathroom", component: "bath", matches: "bathroom/bath"},
	})
	job := s.coverageJob(t, supplement.preparation)
	var covered []identity.ID
	_ = json.Unmarshal(job.CoveredIDs, &covered)
	if len(covered) != 2 {
		t.Fatalf("covered=%v; want kitchen and bath", covered)
	}
	_, statuses := s.recaptureState(t, request)
	if statuses["video:"+kitchen.id.String()] != "CORRECTED" || statuses["video:"+bath.id.String()] != "CORRECTED" || statuses["video:"+bedroom.id.String()] == "CORRECTED" {
		t.Fatalf("requirements=%v; want K and B corrected and D still pending", statuses)
	}
	g2 := s.runPlan(t, 2)
	if g2.Status != "SEALED" {
		t.Fatalf("G2 status=%s error=%s", g2.Status, g2.ErrorCode)
	}
	var bedroomItem database.VideoPlanItem
	if err := s.h.AdminDB.Where("tenant_id=? AND plan_id=? AND component_id=?", s.fixture.TenantID, g2.ID, bedroom.id).Take(&bedroomItem).Error; err != nil {
		t.Fatal(err)
	}
	if bedroomItem.Outcome == "MATCHED" && bedroomItem.CarriedResultJobID == nil {
		t.Fatalf("omitted bedroom was resolved by the supplement: %+v", bedroomItem)
	}
}

// report generates the snapshot of a plan with its own frame evidence.
func (s *task06Supplement) report(t *testing.T, plan database.VideoPlan) generate_snapshot.Result {
	t.Helper()
	result, err := generate_snapshot.Create(s.ctx, s.h.AdminDB, s.reportInput(t, plan), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// reportInput assembles the worker-shaped snapshot input of a plan.
func (s *task06Supplement) reportInput(t *testing.T, plan database.VideoPlan) generate_snapshot.Input {
	t.Helper()
	var requirements []reportcore.Requirement
	var evidence []reportcore.Evidence
	if err := s.h.WithinTenant(s.ctx, s.fixture.TenantID, func(tx *gorm.DB) error {
		var err error
		if requirements, _, err = generate_snapshot.VideoPlanRequirements(s.ctx, tx, s.fixture.TenantID, plan, true); err != nil {
			return err
		}
		var items []database.VideoPlanItem
		if err := tx.Where("tenant_id=? AND plan_id=?", s.fixture.TenantID, plan.ID).Order("requirement_key").Find(&items).Error; err != nil {
			return err
		}
		for _, item := range items {
			var memberships []database.VideoPlanItemFrame
			if err := tx.Where("tenant_id=? AND plan_item_id=?", s.fixture.TenantID, item.ID).Order("core_group ASC,role ASC,pair_order ASC").Find(&memberships).Error; err != nil {
				return err
			}
			for _, membership := range memberships {
				var frame database.VideoFrame
				if err := tx.Where("tenant_id=? AND id=?", s.fixture.TenantID, membership.FrameID).Take(&frame).Error; err != nil {
					return err
				}
				role := "CURRENT"
				if membership.Role == "ORIGIN" {
					role = "REFERENCE"
				}
				pts, ms := frame.PTSMicros, frame.PTSMicros/1000
				evidence = append(evidence, reportcore.Evidence{ID: frame.ID.String(), RequirementKey: item.RequirementKey, Role: role, EvidenceKind: "VIDEO_FRAME", FrameID: frame.ID.String(), SourceMediaID: frame.SourceMediaID.String(), PreparationID: frame.PreparationID.String(), PTSMicros: &pts, TimestampMS: &ms, FrameDigest: frame.SHA256, DisplayDigest: frame.SHA256, Availability: "AVAILABLE"})
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	context := reportcore.Context{Asset: reportcore.AssetContext{ID: s.fixture.AssetID.String(), Name: "QA", Address: "QA"}, Participant: reportcore.ParticipantContext{ID: s.fixture.ParticipantID.String(), Name: "QA"}, Template: reportcore.TemplateContext{ID: "qa-template", Name: "QA", Version: 1}, Inspection: reportcore.InspectionContext{GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano)}}
	return generate_snapshot.Input{TenantID: s.fixture.TenantID, InspectionID: s.inspectionID, VideoPlanID: &plan.ID, Mode: "HISTORICAL", Classification: "ATTENTION", TemplateVersionID: "qa-template-v1", ReferenceVersionID: plan.ReferenceVersionID.String(), PromptDigest: "qa-prompt", Context: context, Requirements: requirements, Evidence: evidence}
}

func storedTask06Snapshot(t *testing.T, s *task06Supplement, id identity.ID) (database.ReportSnapshot, reportcore.Snapshot) {
	t.Helper()
	var row database.ReportSnapshot
	if err := s.h.AdminDB.Where("tenant_id=? AND id=?", s.fixture.TenantID, id).Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	var snapshot reportcore.Snapshot
	if err := json.Unmarshal(row.CanonicalJSON, &snapshot); err != nil {
		t.Fatal(err)
	}
	return row, snapshot
}

func runTask06RPT1ThenRPT2(t *testing.T) (*task06Supplement, generate_snapshot.Result, generate_snapshot.Result, database.VideoPlan, string) {
	t.Helper()
	s := newTask06Supplement(t, task06Labels("kitchen/sink", "bathroom/shower"))
	rpt1 := s.report(t, s.g1)
	row1, _ := storedTask06Snapshot(t, s, rpt1.SnapshotID)
	request := s.request(t, "vv-rpt2", time.Now().UTC().Add(24*time.Hour), "bathroom/shower")
	s.submitSupplement(t, request, []task06Shot{{environment: "bathroom", component: "shower", matches: "bathroom/shower"}})
	g2 := s.runPlan(t, 2)
	if g2.Status != "SEALED" {
		t.Fatalf("G2 status=%s error=%s", g2.Status, g2.ErrorCode)
	}
	rpt2 := s.report(t, g2)
	return s, rpt1, rpt2, g2, row1.JSONDigest
}

func TestVV_IT_238RPT2AfterSupplementKeepsRPT1SeparatelyTraceable(t *testing.T) {
	s, rpt1, rpt2, g2, _ := runTask06RPT1ThenRPT2(t)
	row1, snapshot1 := storedTask06Snapshot(t, s, rpt1.SnapshotID)
	row2, snapshot2 := storedTask06Snapshot(t, s, rpt2.SnapshotID)
	if rpt1.SnapshotID == rpt2.SnapshotID || row1.VersionNumber != 1 || row2.VersionNumber != 2 {
		t.Fatalf("RPT1 %s v%d and RPT2 %s v%d; want two versions", rpt1.SnapshotID, row1.VersionNumber, rpt2.SnapshotID, row2.VersionNumber)
	}
	if snapshot1.VideoPlanID != s.g1.ID.String() || snapshot2.VideoPlanID != g2.ID.String() || row1.VideoPlanID == nil || *row1.VideoPlanID != s.g1.ID || row2.VideoPlanID == nil || *row2.VideoPlanID != g2.ID {
		t.Fatalf("snapshot plans: RPT1=%s RPT2=%s; want G1 %s and G2 %s", snapshot1.VideoPlanID, snapshot2.VideoPlanID, s.g1.ID, g2.ID)
	}
}

func TestVV_IT_252G2ReportRetainsRPT1EvidenceSnapshot(t *testing.T) {
	s, rpt1, rpt2, _, digestBefore := runTask06RPT1ThenRPT2(t)
	row1, snapshot1 := storedTask06Snapshot(t, s, rpt1.SnapshotID)
	_, snapshot2 := storedTask06Snapshot(t, s, rpt2.SnapshotID)
	if row1.JSONDigest != digestBefore {
		t.Fatalf("RPT1 digest changed after RPT2: %s -> %s", digestBefore, row1.JSONDigest)
	}
	bathroom := s.component(t, "bathroom/shower")
	frames1 := map[string]bool{}
	for _, item := range snapshot1.Evidence {
		frames1[item.FrameID] = true
	}
	if !frames1[bathroom.mainFrame.String()] || !frames1[bathroom.originFrame.String()] {
		t.Fatalf("RPT1 lost its own bathroom evidence: %+v", snapshot1.Evidence)
	}
	var supplementCited bool
	for _, item := range snapshot2.Evidence {
		if !frames1[item.FrameID] {
			supplementCited = true
		}
	}
	if !supplementCited {
		t.Fatal("RPT2 does not cite the supplement evidence")
	}
	var references int64
	if err := s.h.AdminDB.Model(&database.VideoEvidenceReference{}).Where("tenant_id=? AND owner_type='REPORT_SNAPSHOT' AND owner_id=?", s.fixture.TenantID, rpt1.SnapshotID).Count(&references).Error; err != nil {
		t.Fatal(err)
	}
	if references == 0 {
		t.Fatal("RPT1 no longer protects its evidence")
	}
}

func TestVV_IT_240SupplementWithoutUsableFramesNeverFulfillsRC1(t *testing.T) {
	s := newTask06Supplement(t, task06Labels("kitchen/sink", "bathroom/shower"))
	bathroom := s.component(t, "bathroom/shower")
	request := s.request(t, "vv-it-240", time.Now().UTC().Add(24*time.Hour), "bathroom/shower")
	var draft database.CaptureDraft
	if err := s.h.AdminDB.Where("tenant_id=? AND responsibility_id=? AND kind='RECAPTURE'", s.fixture.TenantID, request.ResponsibilityID).Take(&draft).Error; err != nil {
		t.Fatal(err)
	}
	media, submission, preparation := identity.NewID(), identity.NewID(), identity.NewID()
	now := time.Now().UTC()
	if err := s.h.WithinTenant(s.ctx, s.fixture.TenantID, func(tx *gorm.DB) error {
		payload, _ := json.Marshal(map[string]any{"mediaKind": "VIDEO", "mediaId": media})
		for _, row := range []any{
			&database.MediaObject{ID: media, TenantID: s.fixture.TenantID, ResponsibilityID: request.ResponsibilityID, ObjectKey: "vv-it-240/" + media.String() + ".mp4", ContentType: "video/mp4", SHA256: s.digest, SizeBytes: 1, Status: "RECEIVED", MediaKind: "VIDEO", RequirementKey: "video:supplement", IdempotencyKey: "vv-it-240-" + media.String(), AttentionItems: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), CreatedAt: now},
			&database.SubmissionVersion{ID: submission, TenantID: s.fixture.TenantID, DraftID: draft.ID, ResponsibilityID: request.ResponsibilityID, VersionNumber: 1, Complete: true, Payload: payload, PayloadDigest: s.digest, SubmittedAt: now},
			// Preparation found no usable frame in the supplement.
			&database.VideoPreparationJob{ID: preparation, TenantID: s.fixture.TenantID, MediaID: media, SubmissionID: &submission, Status: "FAILED", Phase: "DISCOVERING", ErrorCode: "NO_USABLE_FRAMES", PreparationVersion: 1, Generation: 2, Attempts: 1, NextAttemptAt: now, PendingObjectKeys: []byte("[]"), CreatedAt: now},
		} {
			if err := tx.Create(row).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&database.CaptureDraft{}).Where("tenant_id=? AND id=?", s.fixture.TenantID, draft.ID).Update("status", "SUBMITTED").Error; err != nil {
			return err
		}
		_, err := (recapturecore.Service{}).ReceiveVideoTx(tx, s.fixture.TenantID, request.RequestID, request.ResponsibilityID, submission)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	evaluator, err := evaluate_video_coverage.Setup(evaluate_video_coverage.Dependencies{DB: s.h.DB, ClaimDB: s.h.AdminDB, Frames: prepare_video_comparisons.DBFrameReader{DB: s.h.DB}, Matcher: s.matcher, Verifier: s.matcher, PlanRequests: SupplementalRequester{}})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(events.VideoPreparedPayload{MediaID: media, PreparationID: preparation, ManifestDigest: s.digest})
	handleErr := s.h.WithinTenant(s.ctx, s.fixture.TenantID, func(tx *gorm.DB) error {
		return evaluator.HandlePrepared(s.ctx, tx, events.RawEnvelope{Type: "media.video_prepared.v1", TenantID: s.fixture.TenantID, Payload: payload})
	})
	if handleErr == nil {
		t.Fatal("unusable supplement was accepted for coverage evaluation")
	}
	var jobs int64
	if err := s.h.AdminDB.Model(&database.VideoCoverageJob{}).Where("tenant_id=? AND preparation_id=?", s.fixture.TenantID, preparation).Count(&jobs).Error; err != nil {
		t.Fatal(err)
	}
	row, statuses := s.recaptureState(t, request)
	if jobs != 0 || row.Status != "RECEIVED" || statuses["video:"+bathroom.id.String()] == "CORRECTED" {
		t.Fatalf("unusable supplement: coverageJobs=%d request=%s requirements=%v; want no evaluation and RC1 unfulfilled", jobs, row.Status, statuses)
	}
	if _, exists := s.childPlan(t); exists {
		t.Fatal("unusable supplement produced a G2 plan")
	}
}

func TestVV_IT_245RacingSupplementsSealOneGenerationAndKeepBothHistories(t *testing.T) {
	s := newTask06Supplement(t, task06Labels("kitchen/sink", "bathroom/shower"))
	request := s.request(t, "vv-it-245", time.Now().UTC().Add(24*time.Hour), "bathroom/shower")
	shot := []task06Shot{{environment: "bathroom", component: "shower", matches: "bathroom/shower"}}
	first := s.recordSupplement(t, request, shot)
	second := s.recordSupplement(t, request, shot)
	evaluator, err := evaluate_video_coverage.Setup(evaluate_video_coverage.Dependencies{DB: s.h.DB, ClaimDB: s.h.AdminDB, Frames: prepare_video_comparisons.DBFrameReader{DB: s.h.DB}, Matcher: s.matcher, Verifier: s.matcher, PlanRequests: SupplementalRequester{}})
	if err != nil {
		t.Fatal(err)
	}
	for _, supplement := range []task06SupplementResult{first, second} {
		payload, _ := json.Marshal(events.VideoPreparedPayload{MediaID: supplement.media, PreparationID: supplement.preparation, ManifestDigest: s.digest})
		if err := s.h.WithinTenant(s.ctx, s.fixture.TenantID, func(tx *gorm.DB) error {
			return evaluator.HandlePrepared(s.ctx, tx, events.RawEnvelope{Type: "media.video_prepared.v1", TenantID: s.fixture.TenantID, Payload: payload})
		}); err != nil {
			t.Fatal(err)
		}
	}
	// Both coverage evaluations race to finalize the request and open G2.
	start := make(chan struct{})
	done := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			_, err := evaluator.RunDue(s.ctx)
			done <- err
		}()
	}
	close(start)
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Logf("racing coverage evaluation: %v", err)
		}
	}
	for attempt := 0; attempt < 3; attempt++ {
		_, _ = evaluator.RunDue(s.ctx)
	}
	g2 := s.runPlan(t, 2)
	var generations int64
	if err := s.h.AdminDB.Model(&database.VideoPlan{}).Where("tenant_id=? AND inspection_id=? AND generation=2", s.fixture.TenantID, s.inspectionID).Count(&generations).Error; err != nil {
		t.Fatal(err)
	}
	var used []database.VideoPlanPreparation
	if err := s.h.AdminDB.Where("tenant_id=? AND plan_id=? AND role='SUPPLEMENT'", s.fixture.TenantID, g2.ID).Find(&used).Error; err != nil {
		t.Fatal(err)
	}
	if generations != 1 || g2.Status != "SEALED" || len(used) != 1 || (used[0].PreparationID != first.preparation && used[0].PreparationID != second.preparation) {
		t.Fatalf("racing supplements produced generations=%d G2 status=%s supplements=%+v; want one sealed G2 using one accepted supplement", generations, g2.Status, used)
	}
	// Both accepted evidence histories remain.
	for _, supplement := range []task06SupplementResult{first, second} {
		var media, submissions, frames int64
		_ = s.h.AdminDB.Model(&database.MediaObject{}).Where("tenant_id=? AND id=?", s.fixture.TenantID, supplement.media).Count(&media).Error
		_ = s.h.AdminDB.Model(&database.SubmissionVersion{}).Where("tenant_id=? AND id=?", s.fixture.TenantID, supplement.submission).Count(&submissions).Error
		_ = s.h.AdminDB.Model(&database.VideoFrame{}).Where("tenant_id=? AND preparation_id=?", s.fixture.TenantID, supplement.preparation).Count(&frames).Error
		if media != 1 || submissions != 1 || frames != 1 {
			t.Fatalf("supplement %s history lost: media=%d submissions=%d frames=%d", supplement.media, media, submissions, frames)
		}
	}
	row, _ := s.recaptureState(t, request)
	if row.Status != "SUBMITTED" {
		t.Fatalf("RC1 status=%s after racing supplements, want SUBMITTED once", row.Status)
	}
	var completed int64
	if err := s.h.AdminDB.Model(&database.OutboxIntent{}).Where("tenant_id=? AND type='recapture.completed.v1' AND correlation_id=?", s.fixture.TenantID, "recapture-complete-"+request.RequestID.String()).Count(&completed).Error; err != nil {
		t.Fatal(err)
	}
	if completed != 1 {
		t.Fatalf("racing supplements completed RC1 %d times", completed)
	}
}

func TestVV_IT_220FiveUnresolvedRecapturesKeepSinkInconclusiveInLatestReport(t *testing.T) {
	components := task06Labels("kitchen/sink", "bathroom/shower")
	components[0].mainQuality = "LOW_QUALITY" // the sink S is blurred in the main video
	s := newTask06Supplement(t, components)
	sink := s.component(t, "kitchen/sink")
	var g1Sink database.VideoPlanItem
	if err := s.h.AdminDB.Where("tenant_id=? AND plan_id=? AND component_id=?", s.fixture.TenantID, s.g1.ID, sink.id).Take(&g1Sink).Error; err != nil {
		t.Fatal(err)
	}
	if g1Sink.Outcome != "INCONCLUSIVE" {
		t.Fatalf("G1 sink outcome=%s reason=%s, want INCONCLUSIVE", g1Sink.Outcome, g1Sink.Reason)
	}
	deadlineHandler, err := process_deadline.Setup(process_deadline.Dependencies{Now: func() time.Time { return time.Now().UTC().Add(2 * time.Hour) }})
	if err != nil {
		t.Fatal(err)
	}
	for cycle := 1; cycle <= 5; cycle++ {
		request := s.request(t, "vv-it-220-"+strconv.Itoa(cycle), time.Now().UTC().Add(time.Hour), "kitchen/sink")
		if cycle == 1 {
			// The first supplement films the kitchen without the sink S.
			s.submitSupplement(t, request, []task06Shot{{environment: "kitchen", component: "cabinet"}})
		}
		payload, _ := json.Marshal(map[string]any{"requestId": request.RequestID})
		if err := s.h.WithinTenant(s.ctx, s.fixture.TenantID, func(tx *gorm.DB) error {
			return deadlineHandler(s.ctx, tx, events.RawEnvelope{Type: "recapture.deadline_due.v1", TenantID: s.fixture.TenantID, Payload: payload})
		}); err != nil {
			t.Fatalf("cycle %d deadline: %v", cycle, err)
		}
		row, statuses := s.recaptureState(t, request)
		if row.Status != "EXPIRED" || statuses["video:"+sink.id.String()] == "CORRECTED" {
			t.Fatalf("cycle %d: request=%s requirements=%v; want EXPIRED with S unresolved", cycle, row.Status, statuses)
		}
	}
	var cycles int64
	if err := s.h.AdminDB.Model(&database.RecaptureRequest{}).Where("tenant_id=? AND inspection_id=?", s.fixture.TenantID, s.inspectionID).Count(&cycles).Error; err != nil {
		t.Fatal(err)
	}
	if cycles != 5 {
		t.Fatalf("recapture cycles=%d, want 5", cycles)
	}
	// The five-cycle limit is exhausted.
	_, sixth := (recapturecore.Service{DB: s.h.DB}).Request(s.ctx, recapturecore.RequestInput{TenantID: s.fixture.TenantID, InspectionID: s.inspectionID, Items: []recapturecore.Item{{RequirementKey: "video:" + sink.id.String(), OriginalMediaID: &s.mainMedia, Reason: "Sexta tentativa"}}, Delivery: []invitationcore.DeliveryIntent{{Channel: "EMAIL", Destination: "task06@example.test", Verified: true}}, DeadlineAt: time.Now().UTC().Add(time.Hour), IdempotencyKey: "vv-it-220-6"})
	if !apperror.Is(sixth, apperror.InvalidState) {
		t.Fatalf("sixth recapture cycle err=%v, want INVALID_STATE", sixth)
	}
	if _, exists := s.childPlan(t); exists {
		t.Fatal("unresolved cycles produced a child plan")
	}
	report := s.report(t, s.g1)
	_, snapshot := storedTask06Snapshot(t, s, report.SnapshotID)
	for _, requirement := range snapshot.Requirements {
		if requirement.Key == "video:"+sink.id.String() {
			if requirement.AnalysisStatus != "INCONCLUSIVE" {
				t.Fatalf("latest report sink=%+v, want INCONCLUSIVE", requirement)
			}
			return
		}
	}
	t.Fatalf("latest report has no sink requirement: %+v", snapshot.Requirements)
}

func TestVV_IT_216RPT1SealedFromG1WhileSupplementSealsG2(t *testing.T) {
	s := newTask06Supplement(t, task06Labels("kitchen/sink", "bathroom/shower"))
	request := s.request(t, "vv-it-216", time.Now().UTC().Add(24*time.Hour), "bathroom/shower")
	supplement := s.submitSupplement(t, request, []task06Shot{{environment: "bathroom", component: "shower", matches: "bathroom/shower"}})
	g1Input := s.reportInput(t, s.g1)
	// RPT1 generation from G1 races with the scheduler sealing G2.
	start := make(chan struct{})
	reportDone := make(chan error, 1)
	var rpt1 generate_snapshot.Result
	go func() {
		<-start
		var err error
		rpt1, err = generate_snapshot.Create(s.ctx, s.h.AdminDB, g1Input, time.Now())
		reportDone <- err
	}()
	close(start)
	g2 := s.runPlan(t, 2)
	if err := <-reportDone; err != nil {
		t.Fatalf("RPT1 during G2 sealing: %v", err)
	}
	if g2.Status != "SEALED" {
		t.Fatalf("G2 status=%s error=%s", g2.Status, g2.ErrorCode)
	}
	_, snapshot1 := storedTask06Snapshot(t, s, rpt1.SnapshotID)
	if snapshot1.VideoPlanID != s.g1.ID.String() {
		t.Fatalf("RPT1 plan=%s, want G1 %s", snapshot1.VideoPlanID, s.g1.ID)
	}
	for _, item := range snapshot1.Evidence {
		for _, frame := range supplement.frames {
			if item.FrameID == frame.String() {
				t.Fatalf("RPT1 contains G2 supplement frame %s", frame)
			}
		}
	}
	rpt2 := s.report(t, g2)
	row2, snapshot2 := storedTask06Snapshot(t, s, rpt2.SnapshotID)
	if row2.VersionNumber != 2 || snapshot2.VideoPlanID != g2.ID.String() {
		t.Fatalf("later version=%d plan=%s; want version 2 from G2 %s", row2.VersionNumber, snapshot2.VideoPlanID, g2.ID)
	}
}
