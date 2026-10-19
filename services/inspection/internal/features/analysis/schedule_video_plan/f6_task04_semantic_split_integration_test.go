//go:build integration

package schedule_video_plan

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	buildplan "inspection/services/inspection/internal/features/analysis/prepare_video_comparisons"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/llm"

	"gorm.io/gorm"
)

type recordingSplitGateway struct {
	requests []llm.StructuredRequest
}

func (g *recordingSplitGateway) CompleteStructured(_ context.Context, request llm.StructuredRequest) (llm.StructuredResult, error) {
	g.requests = append(g.requests, request)
	return llm.StructuredResult{Provider: "test", Model: "fixture", GatewayRequestID: "vv-it616", JSON: []byte(`{"pairs":[]}`)}, nil
}

func TestVV_IT_616OversizedSemanticRequestIsSplitWithFullBaselineCoverage(t *testing.T) {
	fixture := newTask04PlanFixture(t, false, true)
	var originPrep database.VideoPreparationJob
	if err := fixture.h.AdminDB.Where("tenant_id=? AND id=?", fixture.tenantID, fixture.originPreparation).Take(&originPrep).Error; err != nil {
		t.Fatal(err)
	}
	// Twelve more origin and current "cabinet" frames form one 12x12 component
	// bank, whose single 24-image batch exceeds the 24 MiB request limit.
	now := time.Now().UTC()
	inserted := map[string][]identity.ID{}
	if err := fixture.h.WithinTenant(fixture.ctx, fixture.tenantID, func(tx *gorm.DB) error {
		for i := 1; i <= 12; i++ {
			for _, side := range []struct {
				media, preparation identity.ID
				role               string
			}{{originPrep.MediaID, fixture.originPreparation, "BASELINE"}, {fixture.currentMediaID(), fixture.currentPreparation, "CURRENT"}} {
				frameID := identity.NewID()
				inserted[side.role] = append(inserted[side.role], frameID)
				if err := tx.Create(&database.VideoFrame{ID: frameID, TenantID: fixture.tenantID, SourceMediaID: side.media, PreparationID: side.preparation, PTSMicros: int64(100+i) * 1_000_000, SHA256: strings.Repeat("a", 64), ObjectKey: "vv-it616/" + frameID.String(), Width: 1, Height: 1, Role: side.role, SizeBytes: 1, CreatedAt: now}).Error; err != nil {
					return err
				}
				if err := tx.Create(&database.VideoScopeItem{ID: identity.NewID(), TenantID: fixture.tenantID, PreparationID: side.preparation, FrameID: frameID, EnvironmentCandidate: "kitchen", ComponentLabel: "cabinet", ViewLabel: "wide", Visibility: "VISIBLE", IdentityStatus: "RESOLVED", Landmarks: json.RawMessage(`[]`), CreatedAt: now}).Error; err != nil {
					return err
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Each image decodes to 1.5 MiB, so 24 images would also exceed 24 MiB.
	encoded := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(make([]byte, 3<<19))
	images := buildplan.FrameImageFunc(func(_ context.Context, frames []buildplan.Frame) ([]llm.NormalizedImage, error) {
		out := make([]llm.NormalizedImage, len(frames))
		for i := range frames {
			out[i] = llm.NormalizedImage{EvidenceID: frames[i].ID.String(), DataURL: encoded}
		}
		return out, nil
	})
	gateway := &recordingSplitGateway{}
	matcher, err := buildplan.NewGatewayMatcher(gateway, images)
	if err != nil {
		t.Fatal(err)
	}
	scheduler := newTask04Scheduler(t, fixture, matcher, matcher)
	if err := fixture.h.WithinTenant(fixture.ctx, fixture.tenantID, func(tx *gorm.DB) error { return scheduler.Handle(fixture.ctx, tx, fixture.submittedEvent(t)) }); err != nil {
		t.Fatal(err)
	}
	parkTask04Plans(t, fixture)
	// RunDue claims the oldest due plan of any tenant; repeat until this
	// fixture's plan has run.
	for attempt := 0; attempt < 5; attempt++ {
		if _, err := scheduler.RunDue(fixture.ctx); err != nil {
			t.Fatalf("RunDue: %v", err)
		}
		var plan database.VideoPlan
		if err := fixture.h.AdminDB.Where("tenant_id=? AND submission_id=?", fixture.tenantID, fixture.submissionID).Take(&plan).Error; err != nil {
			t.Fatal(err)
		}
		if plan.Attempts > 0 || plan.Status == "SEALED" {
			break
		}
	}
	covered := map[string]bool{}
	componentRequests := 0
	for _, request := range gateway.requests {
		var bytes int
		for _, image := range request.Images {
			bytes += base64.StdEncoding.DecodedLen(len(strings.TrimPrefix(image.DataURL, "data:image/jpeg;base64,")))
		}
		if len(request.Images) > 24 || bytes > 24<<20 {
			t.Fatalf("dispatched request exceeds bounds: images=%d bytes=%d", len(request.Images), bytes)
		}
		if len(request.Images) > 2 {
			componentRequests++
		}
		for _, a := range request.Images {
			for _, b := range request.Images {
				covered[a.EvidenceID+"|"+b.EvidenceID] = true
			}
		}
	}
	if componentRequests < 4 {
		t.Fatalf("oversized 12x12 batch was not split before dispatch: componentRequests=%d", componentRequests)
	}
	for _, origin := range inserted["BASELINE"] {
		for _, current := range inserted["CURRENT"] {
			if !covered[origin.String()+"|"+current.String()] {
				t.Fatalf("origin frame %s was never compared with current frame %s", origin, current)
			}
		}
	}
}
