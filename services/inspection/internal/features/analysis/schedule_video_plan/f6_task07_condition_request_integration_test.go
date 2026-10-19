//go:build integration

package schedule_video_plan

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	buildrequest "inspection/services/inspection/internal/features/analysis/build_request"
	processcomparison "inspection/services/inspection/internal/features/analysis/process_comparison"
	analysisprompt "inspection/services/inspection/internal/features/analysis/prompt"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

func TestVV_IT_180VideoConditionPromptCarriesFrameSourceAndPTS(t *testing.T) {
	fixture := newTask04PlanFixture(t, false, true)
	plan := runTask04Plan(t, fixture)
	prepareTask04FrameObjects(t, fixture)
	job := task04PlanJob(t, fixture, plan)
	gateway := processTask04Job(t, fixture, job)
	request := gateway.request
	for _, image := range request.Images {
		id, err := identity.ParseID(image.EvidenceID)
		if err != nil {
			t.Fatal(err)
		}
		var frame database.VideoFrame
		if err := fixture.h.AdminDB.Where("tenant_id=? AND id=?", fixture.tenantID, id).Take(&frame).Error; err != nil {
			t.Fatal(err)
		}
		line := image.EvidenceID + " · " + image.Source + " · " + strconv.FormatInt(frame.PTSMicros/1000, 10) + " ms"
		if !strings.Contains(request.UserPrompt, line) {
			t.Fatalf("prompt lacks frame context %q:\n%s", line, request.UserPrompt)
		}
	}
	var inspection database.Inspection
	if err := fixture.h.AdminDB.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.inspectionID).Take(&inspection).Error; err != nil {
		t.Fatal(err)
	}
	var snapshot database.AnalysisPromptSnapshot
	if err := fixture.h.AdminDB.Where("id=?", inspection.AnalysisPromptSnapshotID).Take(&snapshot).Error; err != nil {
		t.Fatal(err)
	}
	var definition analysisprompt.Definition
	if err := json.Unmarshal(snapshot.DefinitionJSON, &definition); err != nil {
		t.Fatal(err)
	}
	pinned, err := json.Marshal(definition.OutputSchema)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := json.Marshal(analysisprompt.OutputSchema())
	if err != nil {
		t.Fatal(err)
	}
	if string(request.JSONSchema) != string(pinned) || string(pinned) != string(canonical) {
		t.Fatalf("video request schema diverged from the existing condition response schema: %s", request.JSONSchema)
	}
}

func TestVV_IT_197CurrentOnlyRequestWithOriginFrameIsRejectedBeforeDispatch(t *testing.T) {
	fixture := newTask04PlanFixture(t, false, true)
	plan := runTask04Plan(t, fixture)
	prepareTask04FrameObjects(t, fixture)
	job := task04PlanJob(t, fixture, plan)
	var originFrame database.VideoFrame
	if err := fixture.h.AdminDB.Where("tenant_id=? AND preparation_id=?", fixture.tenantID, fixture.originPreparation).Take(&originFrame).Error; err != nil {
		t.Fatal(err)
	}
	// A tampered current-only item points at the origin frame F01.
	if err := fixture.h.AdminDB.Model(&database.VideoPlanItem{}).Where("tenant_id=? AND id=?", fixture.tenantID, *job.VideoPlanItemID).Updates(map[string]any{"outcome": "CURRENT_ONLY", "current_groups": json.RawMessage(`[["` + originFrame.ID.String() + `"]]`)}).Error; err != nil {
		t.Fatal(err)
	}
	builder, err := buildrequest.Setup(buildrequest.Dependencies{Store: fixture.h.Store})
	if err != nil {
		t.Fatal(err)
	}
	var buildErr error
	if err := fixture.h.WithinTenant(fixture.ctx, fixture.tenantID, func(tx *gorm.DB) error {
		_, buildErr = builder(fixture.ctx, tx, job, processcomparison.Payload{JobID: job.ID})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !apperror.Is(buildErr, apperror.InvalidInput) {
		t.Fatalf("current-only build with origin frame error = %v, want INVALID_INPUT", buildErr)
	}
	gateway := &countingTask04Gateway{inner: &task04ConditionGateway{categories: []string{"CONSERVATION"}}}
	processor, err := processcomparison.Setup(processcomparison.Dependencies{Gateway: gateway, BuildRequest: builder, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(processcomparison.Payload{JobID: job.ID})
	if err := fixture.h.WithinTenant(fixture.ctx, fixture.tenantID, func(tx *gorm.DB) error { return processor(context.Background(), tx, payload, fixture.tenantID) }); err != nil {
		t.Fatal(err)
	}
	var got database.ComparisonJob
	if err := fixture.h.AdminDB.Where("tenant_id=? AND id=?", fixture.tenantID, job.ID).Take(&got).Error; err != nil {
		t.Fatal(err)
	}
	if gateway.calls != 0 || got.Status == "COMPLETED" {
		t.Fatalf("origin frame reached the gateway: calls=%d status=%s", gateway.calls, got.Status)
	}
}
