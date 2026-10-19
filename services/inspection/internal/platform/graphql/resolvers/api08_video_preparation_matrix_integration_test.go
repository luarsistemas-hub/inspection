//go:build integration

package resolvers

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	reanalyzevideo "inspection/services/inspection/internal/features/analysis/reanalyze_video_inspection"
	mediavideoprep "inspection/services/inspection/internal/features/media/manage_video_preparation"
	reportcore "inspection/services/inspection/internal/features/reports/core"
	"inspection/services/inspection/internal/features/reports/generate_snapshot"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	graph "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/testfailure"
	"inspection/services/inspection/internal/platform/video"

	"github.com/99designs/gqlgen/graphql/handler"
	"gorm.io/gorm"
)

var api08Operations = []string{"videoPreparation", "retryVideoPreparation", "reprocessVideoPreparation", "reanalyzeVideoInspection"}

func TestVV_IT_458RetryReprocessAndReanalyzeEachReturnOneAuditedExecution(t *testing.T) {
	fx := newAPI08Fixture(t)
	failed := seedAPI08Preparation(t, fx, "FAILED", 4, "RETAINED")
	status := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api08Query(fx, "videoPreparation", failed.mediaID, 0, "", ""), fx.ctx, "", "")
	if strings.Contains(status, `"errors"`) || !strings.Contains(status, `"status":"FAILED"`) || !strings.Contains(status, `"retryable":true`) || !strings.Contains(status, `"preparationVersion":4`) {
		t.Fatalf("failed C1 status = %s", status)
	}
	retried := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api08Query(fx, "retryVideoPreparation", failed.mediaID, 4, "", "vv-it-458-retry"), fx.ctx, "", "")
	if strings.Contains(retried, `"errors"`) || !strings.Contains(retried, `"preparationId":"`+failed.preparationID.String()+`"`) || !strings.Contains(retried, `"preparationVersion":5`) || !strings.Contains(retried, `"status":"QUEUED"`) {
		t.Fatalf("retry did not queue one new execution of C1: %s", retried)
	}
	// The worker finishes the retried execution with the pinned manifest.
	if err := fx.h.AdminDB.Model(&database.VideoPreparationJob{}).Where("tenant_id=? AND id=?", fx.seed.TenantID, failed.preparationID).Updates(map[string]any{"status": "READY", "phase": "READY", "manifest_digest": failed.manifest}).Error; err != nil {
		t.Fatal(err)
	}
	digest := video.DefaultProfile().Digest()
	reprocessed := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api08Query(fx, "reprocessVideoPreparation", failed.mediaID, 5, digest, "vv-it-458-reprocess"), fx.ctx, "", "")
	var payload struct {
		Data struct {
			Reprocess struct {
				Preparation *struct {
					PreparationID string `json:"preparationId"`
					Status        string `json:"status"`
				} `json:"preparation"`
			} `json:"reprocessVideoPreparation"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(reprocessed), &payload); err != nil || payload.Data.Reprocess.Preparation == nil || payload.Data.Reprocess.Preparation.PreparationID == failed.preparationID.String() || payload.Data.Reprocess.Preparation.Status != "QUEUED" {
		t.Fatalf("reprocess did not create a new immutable preparation: %s (%v)", reprocessed, err)
	}
	var historical database.VideoPreparationJob
	if err := fx.h.AdminDB.Where("tenant_id=? AND id=?", fx.seed.TenantID, failed.preparationID).Take(&historical).Error; err != nil {
		t.Fatal(err)
	}
	if historical.ManifestDigest != failed.manifest || historical.Status != "READY" {
		t.Fatalf("historical C1 manifest moved: %+v", historical)
	}
	plan := seedAPI08SealedPlan(t, fx)
	reanalyzed := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api08Query(fx, "reanalyzeVideoInspection", identity.ID{}, 0, plan.String(), "vv-it-458-reanalyze"), fx.ctx, "", "")
	if strings.Contains(reanalyzed, `"errors"`) || !strings.Contains(reanalyzed, `"generation":2`) || strings.Contains(reanalyzed, `"planId":null`) || strings.Contains(reanalyzed, `"planId":"`+plan.String()+`"`) {
		t.Fatalf("reanalysis did not return one new plan generation: %s", reanalyzed)
	}
	var sealed database.VideoPlan
	if err := fx.h.AdminDB.Where("tenant_id=? AND id=?", fx.seed.TenantID, plan).Take(&sealed).Error; err != nil {
		t.Fatal(err)
	}
	if sealed.Status != "SEALED" || sealed.PlanDigest == "" {
		t.Fatalf("historical V1 plan changed: %+v", sealed)
	}
	for action, want := range map[string]int64{"video.preparation_retried": 1, "video.preparation_reprocessed": 1, "video.inspection_reanalyzed": 1} {
		var audits int64
		if err := fx.h.AdminDB.Model(&database.AuditEvent{}).Where("tenant_id=? AND action=?", fx.seed.TenantID, action).Count(&audits).Error; err != nil {
			t.Fatal(err)
		}
		if audits != want {
			t.Fatalf("audit %s count=%d, want %d", action, audits, want)
		}
	}
}

func TestVV_IT_459VideoPreparationOperationsRequireAuthentication(t *testing.T) {
	fx := newAPI08Fixture(t)
	resources := seedAPI08Resources(t, fx)
	for _, operation := range api08Operations {
		t.Run(operation, func(t *testing.T) {
			response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, resources.query(fx, operation, "vv-it-459-"+operation), context.Background(), "", "")
			assertFixedOriginTransportError(t, response, "UNAUTHENTICATED")
		})
	}
	resources.assertUnchanged(t, fx)
}

func TestVV_IT_460VideoPreparationOperationsRequireCapability(t *testing.T) {
	fx := newAPI08Fixture(t)
	resources := seedAPI08Resources(t, fx)
	for _, operation := range api08Operations {
		t.Run(operation, func(t *testing.T) {
			// Viewer reads preparation state but cannot change it; a customer
			// viewer lacks the internal read capability.
			role := auth.Viewer
			if operation == "videoPreparation" {
				role = auth.CustomerViewer
			}
			actor := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: fx.seed.TenantID, Principal: requestctx.Principal{IdentityID: fx.seed.IdentityID, TenantID: fx.seed.TenantID, Roles: []string{role}}, CorrelationID: "vv-it-460"})
			response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, resources.query(fx, operation, "vv-it-460-"+operation), actor, "", "")
			assertFixedOriginTransportError(t, response, "FORBIDDEN")
		})
	}
	resources.assertUnchanged(t, fx)
}

func TestVV_IT_461ForeignTenantPreparationResourcesAreNotFound(t *testing.T) {
	fx := newAPI08Fixture(t)
	resources := seedAPI08Resources(t, fx)
	foreignFx := newAPI08Fixture(t)
	foreign := seedAPI08Resources(t, foreignFx)
	for _, operation := range api08Operations {
		t.Run(operation, func(t *testing.T) {
			query := foreign.query(foreignFx, operation, "vv-it-461-"+operation)
			response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, query, fx.ctx, "", "")
			assertAPI06Rejected(t, response, "NOT_FOUND")
			for _, leaked := range []string{foreignFx.seed.TenantID.String(), foreign.failed.preparationID.String(), foreign.ready.preparationID.String(), foreign.plan.String()} {
				if strings.Contains(response, leaked) {
					t.Fatalf("foreign tenant metadata leaked: %s", response)
				}
			}
		})
	}
	resources.assertUnchanged(t, fx)
	foreign.assertUnchanged(t, foreignFx)
	t.Run("tenant selector is not part of the schema", func(t *testing.T) {
		query := `query { videoPreparation(mediaId:"` + resources.failed.mediaID.String() + `",tenantId:"` + foreignFx.seed.TenantID.String() + `") { preparationId } }`
		response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, query, fx.ctx, "", "")
		if !strings.Contains(response, `"errors"`) || !strings.Contains(response, "tenantId") || strings.Contains(response, `"preparationId"`) {
			t.Fatalf("injected tenant selector was not rejected by schema validation: %s", response)
		}
	})
}

func TestVV_IT_462VideoPreparationAdapterUnavailable(t *testing.T) {
	for _, operation := range api08Operations {
		t.Run(operation, func(t *testing.T) {
			fx := newAPI08Fixture(t)
			resources := seedAPI08Resources(t, fx)
			if err := testfailure.Install(fx.h.DB); err != nil {
				t.Fatal(err)
			}
			response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, resources.query(fx, operation, "vv-it-462-"+operation), fx.ctx, operation, "dependency")
			assertFixedOriginTransportError(t, response, "DEPENDENCY_UNAVAILABLE")
			resources.assertUnchanged(t, fx)
		})
	}
}

func TestVV_IT_463VideoPreparationInternalErrorHidesSecret(t *testing.T) {
	for _, operation := range api08Operations {
		t.Run(operation, func(t *testing.T) {
			fx := newAPI08Fixture(t)
			resources := seedAPI08Resources(t, fx)
			if err := testfailure.Install(fx.h.DB); err != nil {
				t.Fatal(err)
			}
			response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, resources.query(fx, operation, "vv-it-463-"+operation), fx.ctx, operation, "internal")
			assertFixedOriginTransportError(t, response, "INTERNAL")
			if strings.Contains(response, testfailure.Secret) {
				t.Fatalf("internal adapter error leaked secret: %s", response)
			}
			resources.assertUnchanged(t, fx)
		})
	}
}

func TestVV_IT_464ActiveOrReadyWorkIsNotRetryable(t *testing.T) {
	fx := newAPI08Fixture(t)
	ready := seedAPI08Preparation(t, fx, "READY", 2, "RETAINED")
	active := seedAPI08Preparation(t, fx, "QUEUED", 3, "RETAINED")
	cases := map[string]string{
		"retryVideoPreparation/READY":      api08Query(fx, "retryVideoPreparation", ready.mediaID, 2, "", "vv-it-464-retry-ready"),
		"retryVideoPreparation/QUEUED":     api08Query(fx, "retryVideoPreparation", active.mediaID, 3, "", "vv-it-464-retry-active"),
		"reprocessVideoPreparation/QUEUED": api08Query(fx, "reprocessVideoPreparation", active.mediaID, 3, video.DefaultProfile().Digest(), "vv-it-464-reprocess-active"),
	}
	for name, query := range cases {
		t.Run(name, func(t *testing.T) {
			assertAPI08Rejected(t, executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, query, fx.ctx, "", ""), "INVALID_STATE")
		})
	}
	ready.assertUnchanged(t, fx, "READY", 2)
	active.assertUnchanged(t, fx, "QUEUED", 3)
}

func TestVV_IT_465DecodingAfterSourceExpiryIsInvalidState(t *testing.T) {
	fx := newAPI08Fixture(t)
	failedExpired := seedAPI08Preparation(t, fx, "FAILED", 1, "EXPIRED")
	readyExpired := seedAPI08Preparation(t, fx, "READY", 1, "EXPIRED")
	cases := map[string]string{
		"retryVideoPreparation":     api08Query(fx, "retryVideoPreparation", failedExpired.mediaID, 1, "", "vv-it-465-retry"),
		"reprocessVideoPreparation": api08Query(fx, "reprocessVideoPreparation", readyExpired.mediaID, 1, video.DefaultProfile().Digest(), "vv-it-465-reprocess"),
	}
	for name, query := range cases {
		t.Run(name, func(t *testing.T) {
			assertAPI08Rejected(t, executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, query, fx.ctx, "", ""), "INVALID_STATE")
		})
	}
	failedExpired.assertUnchanged(t, fx, "FAILED", 1)
	readyExpired.assertUnchanged(t, fx, "READY", 1)
}

func TestVV_IT_466StaleExpectedVersionIsConflict(t *testing.T) {
	fx := newAPI08Fixture(t)
	resources := seedAPI08Resources(t, fx)
	cases := map[string]string{
		"retryVideoPreparation":     api08Query(fx, "retryVideoPreparation", resources.failed.mediaID, 3, "", "vv-it-466-retry"),
		"reprocessVideoPreparation": api08Query(fx, "reprocessVideoPreparation", resources.ready.mediaID, 1, video.DefaultProfile().Digest(), "vv-it-466-reprocess"),
		"reanalyzeVideoInspection":  api08Query(fx, "reanalyzeVideoInspection", identity.ID{}, 0, identity.NewID().String(), "vv-it-466-reanalyze"),
	}
	for name, query := range cases {
		t.Run(name, func(t *testing.T) {
			assertAPI08Rejected(t, executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, query, fx.ctx, "", ""), "CONFLICT")
		})
	}
	resources.assertUnchanged(t, fx)
}

func TestVV_IT_467UnknownProfileDigestIsInvalidInput(t *testing.T) {
	fx := newAPI08Fixture(t)
	resources := seedAPI08Resources(t, fx)
	query := api08Query(fx, "reprocessVideoPreparation", resources.ready.mediaID, 2, strings.Repeat("0", 64), "vv-it-467-reprocess")
	assertAPI08Rejected(t, executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, query, fx.ctx, "", ""), "INVALID_INPUT")
	resources.assertUnchanged(t, fx)
}

func TestVV_IT_618NewExtractionProfileAfterSourceExpiryKeepsOldReportAndBank(t *testing.T) {
	fx := newAPI08Fixture(t)
	c1 := seedAPI08Preparation(t, fx, "READY", 1, "EXPIRED")
	// C1 was extracted by an earlier profile; the deployed one is new.
	if err := fx.h.AdminDB.Model(&database.VideoPreparationJob{}).Where("tenant_id=? AND id=?", fx.seed.TenantID, c1.preparationID).Update("profile_digest", strings.Repeat("e", 64)).Error; err != nil {
		t.Fatal(err)
	}
	var frame database.VideoFrame
	if err := fx.h.AdminDB.Where("tenant_id=? AND preparation_id=?", fx.seed.TenantID, c1.preparationID).Take(&frame).Error; err != nil {
		t.Fatal(err)
	}
	pts, ms := frame.PTSMicros, frame.PTSMicros/1000
	report, err := generate_snapshot.Create(fx.ctx, fx.h.AdminDB, generate_snapshot.Input{TenantID: fx.seed.TenantID, InspectionID: fx.seed.InspectionID, Mode: "CONSOLIDATED", Classification: "ATTENTION", TemplateVersionID: "template-v1", ReferenceVersionID: "current-only", PromptDigest: "prompt-v1", Evidence: []reportcore.Evidence{{ID: frame.ID.String(), RequirementKey: "video:main", Role: "CURRENT", EvidenceKind: "VIDEO_FRAME", FrameID: frame.ID.String(), SourceMediaID: c1.mediaID.String(), PreparationID: c1.preparationID.String(), PTSMicros: &pts, TimestampMS: &ms, FrameDigest: frame.SHA256}}, Context: reportcore.Context{Asset: reportcore.AssetContext{ID: fx.seed.AssetID.String(), Name: "QA", Address: "QA"}, Participant: reportcore.ParticipantContext{ID: "qa", Name: "QA"}, Template: reportcore.TemplateContext{ID: "template-v1", Name: "QA", Version: 1}, Inspection: reportcore.InspectionContext{GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano)}}}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	query := api08Query(fx, "reprocessVideoPreparation", c1.mediaID, 1, video.DefaultProfile().Digest(), "vv-it-618-reprocess")
	assertAPI08Rejected(t, executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, query, fx.ctx, "", ""), "INVALID_STATE")
	c1.assertUnchanged(t, fx, "READY", 1)
	var frames int64
	if err := fx.h.AdminDB.Model(&database.VideoFrame{}).Where("tenant_id=? AND preparation_id=?", fx.seed.TenantID, c1.preparationID).Count(&frames).Error; err != nil {
		t.Fatal(err)
	}
	var snapshot database.ReportSnapshot
	if err := fx.h.AdminDB.Where("tenant_id=? AND inspection_id=?", fx.seed.TenantID, fx.seed.InspectionID).Order("version_number DESC").Take(&snapshot).Error; err != nil {
		t.Fatal(err)
	}
	if frames != 1 || snapshot.ID != report.SnapshotID || snapshot.JSONDigest != report.JSONDigest {
		t.Fatalf("rejected reprocess changed the old bank or report: frames=%d snapshot=%s digest=%s", frames, snapshot.ID, snapshot.JSONDigest)
	}
}

type api08Fixture struct {
	fixedOriginGraphQLFixture
}

func newAPI08Fixture(t *testing.T) api08Fixture {
	t.Helper()
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	seed, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{InspectionStatus: "COMPLETED"})
	if err != nil {
		t.Fatal(err)
	}
	bus := mediator.New()
	if err := mediavideoprep.Setup(mediavideoprep.Dependencies{DB: h.DB, Bus: bus, Authorizer: auth.Authorizer{}, Within: h.WithinTenant}); err != nil {
		t.Fatal(err)
	}
	if err := reanalyzevideo.Setup(reanalyzevideo.Dependencies{DB: h.DB, Bus: bus, Authorizer: auth.Authorizer{}, Within: h.WithinTenant}); err != nil {
		t.Fatal(err)
	}
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{DB: h.DB, Bus: bus}}))
	server.SetErrorPresenter(graph.PresentError)
	meta := requestctx.Metadata{TenantID: seed.TenantID, Principal: requestctx.Principal{IdentityID: seed.IdentityID, MembershipID: seed.MembershipID, TenantID: seed.TenantID, Roles: []string{auth.TenantAdmin}}, CorrelationID: "vv-api08"}
	return api08Fixture{fixedOriginGraphQLFixture{ctx: requestctx.WithMetadata(ctx, meta), h: h, seed: seed, server: server, bus: bus}}
}

func api08Query(fx api08Fixture, operation string, mediaID identity.ID, expectedVersion int, extra, clientID string) string {
	status := `preparation { mediaId preparationId status stage preparationVersion retryable sourceAvailable manifestDigest } userErrors { code field } clientMutationId`
	switch operation {
	case "videoPreparation":
		return `query { videoPreparation(mediaId:"` + mediaID.String() + `") { mediaId preparationId status stage preparationVersion attempts errorCode retryable sourceAvailable manifestDigest } }`
	case "retryVideoPreparation":
		return `mutation { retryVideoPreparation(input:{mediaId:"` + mediaID.String() + `",expectedPreparationVersion:` + strconv.Itoa(expectedVersion) + `,clientMutationId:"` + clientID + `"}) { ` + status + ` } }`
	case "reprocessVideoPreparation":
		return `mutation { reprocessVideoPreparation(input:{mediaId:"` + mediaID.String() + `",expectedPreparationVersion:` + strconv.Itoa(expectedVersion) + `,profileDigest:"` + extra + `",clientMutationId:"` + clientID + `"}) { ` + status + ` } }`
	case "reanalyzeVideoInspection":
		return `mutation { reanalyzeVideoInspection(input:{inspectionId:"` + fx.seed.InspectionID.String() + `",expectedPlanId:"` + extra + `",clientMutationId:"` + clientID + `"}) { planId generation userErrors { code field } clientMutationId } }`
	}
	panic("unknown API08 operation " + operation)
}

type api08Preparation struct {
	mediaID, preparationID identity.ID
	manifest               string
}

// seedAPI08Preparation stores one VIDEO source with its latest preparation in
// the given state and a retained frame bank when the job reached READY.
func seedAPI08Preparation(t *testing.T, fx api08Fixture, status string, version int, sourceStatus string) api08Preparation {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	firstReady := now.Add(-time.Hour)
	prep := api08Preparation{mediaID: identity.NewID(), preparationID: identity.NewID(), manifest: strings.Repeat("c", 64)}
	if err := fx.h.WithinTenant(fx.ctx, fx.seed.TenantID, func(tx *gorm.DB) error {
		if err := tx.Create(&database.MediaObject{ID: prep.mediaID, TenantID: fx.seed.TenantID, ResponsibilityID: fx.seed.ResponsibilityID, ObjectKey: "vv-api08/" + prep.mediaID.String() + ".mp4", ContentType: "video/mp4", SHA256: prep.manifest, SizeBytes: 10, Status: "READY", MediaKind: "VIDEO", RequirementKey: "video:main", IdempotencyKey: "vv-api08-" + prep.mediaID.String(), AttentionItems: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), CaptureSource: "CAMERA", CreatedAt: now}).Error; err != nil {
			return err
		}
		job := database.VideoPreparationJob{ID: prep.preparationID, TenantID: fx.seed.TenantID, MediaID: prep.mediaID, Status: status, Phase: status, PreparationVersion: version, Generation: 1, NextAttemptAt: now, SourceSHA256: prep.manifest, ProfileDigest: video.DefaultProfile().Digest(), PendingObjectKeys: []byte(`[]`), CreatedAt: now}
		switch status {
		case "READY":
			job.ManifestDigest, job.FirstReadyAt = prep.manifest, &firstReady
		case "FAILED":
			job.Phase, job.ErrorCode = "EXTRACTING", "PREPARATION_FAILED"
		default:
			job.Phase = "RECEIVED"
		}
		if err := tx.Create(&job).Error; err != nil {
			return err
		}
		source := database.VideoSource{ID: identity.NewID(), TenantID: fx.seed.TenantID, MediaID: prep.mediaID, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 1_000_000, Width: 2, Height: 2, FrameRate: 30, ScreeningStatus: "APPROVED", Status: sourceStatus, CreatedAt: now, UpdatedAt: now}
		if status == "READY" {
			days, expiry := 30, firstReady.Add(30*24*time.Hour)
			source.FirstFrameReadyAt, source.RetentionDays, source.SourceExpiryAt = &firstReady, &days, &expiry
		}
		if sourceStatus == "EXPIRED" {
			source.OriginalDeletedAt = &now
		}
		if err := tx.Create(&source).Error; err != nil {
			return err
		}
		if status != "READY" {
			return nil
		}
		frameID := identity.NewID()
		if err := tx.Create(&database.VideoFrame{ID: frameID, TenantID: fx.seed.TenantID, SourceMediaID: prep.mediaID, PreparationID: prep.preparationID, PTSMicros: 0, SHA256: prep.manifest, ObjectKey: "vv-api08/frame/" + frameID.String(), Width: 2, Height: 2, Role: "BASELINE", SizeBytes: 10, CreatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.VideoScopeItem{ID: identity.NewID(), TenantID: fx.seed.TenantID, PreparationID: prep.preparationID, FrameID: frameID, EnvironmentCandidate: "kitchen", ComponentLabel: "sink", ViewLabel: "wide", Visibility: "VISIBLE", IdentityStatus: "RESOLVED", Landmarks: json.RawMessage(`[]`), CreatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	return prep
}

func (p api08Preparation) assertUnchanged(t *testing.T, fx api08Fixture, status string, version int) {
	t.Helper()
	var jobs []database.VideoPreparationJob
	if err := fx.h.AdminDB.Where("tenant_id=? AND media_id=?", fx.seed.TenantID, p.mediaID).Find(&jobs).Error; err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].Status != status || jobs[0].PreparationVersion != version {
		t.Fatalf("rejected preparation mutation changed jobs: %+v", jobs)
	}
}

// seedAPI08SealedPlan makes the fixture inspection a VIDEO inspection with one
// sealed active plan V1.
func seedAPI08SealedPlan(t *testing.T, fx api08Fixture) identity.ID {
	t.Helper()
	ready := seedAPI08Preparation(t, fx, "READY", 1, "RETAINED")
	now := time.Now().UTC().Truncate(time.Microsecond)
	planID, submissionID := identity.NewID(), identity.NewID()
	if err := fx.h.WithinTenant(fx.ctx, fx.seed.TenantID, func(tx *gorm.DB) error {
		if err := tx.Create(&database.SubmissionVersion{ID: submissionID, TenantID: fx.seed.TenantID, DraftID: fx.seed.DraftID, ResponsibilityID: fx.seed.ResponsibilityID, VersionNumber: 1, Complete: true, Payload: json.RawMessage(`{}`), PayloadDigest: ready.manifest, SubmittedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPlan{ID: planID, TenantID: fx.seed.TenantID, InspectionID: fx.seed.InspectionID, SubmissionID: submissionID, CurrentPreparationID: ready.preparationID, Generation: 1, PlanDigest: ready.manifest, Status: "SEALED", SealedAt: &now, NextAttemptAt: now, CreatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", fx.seed.TenantID, fx.seed.InspectionID).Updates(map[string]any{"media_kind": "VIDEO", "active_video_plan_id": planID}).Error
	}); err != nil {
		t.Fatal(err)
	}
	return planID
}

// api08Resources holds a valid target for every operation.
type api08Resources struct {
	failed, ready api08Preparation
	plan          identity.ID
}

func seedAPI08Resources(t *testing.T, fx api08Fixture) api08Resources {
	t.Helper()
	return api08Resources{failed: seedAPI08Preparation(t, fx, "FAILED", 4, "RETAINED"), ready: seedAPI08Preparation(t, fx, "READY", 2, "RETAINED"), plan: seedAPI08SealedPlan(t, fx)}
}

func (r api08Resources) query(fx api08Fixture, operation, clientID string) string {
	switch operation {
	case "videoPreparation":
		return api08Query(fx, operation, r.failed.mediaID, 0, "", "")
	case "retryVideoPreparation":
		return api08Query(fx, operation, r.failed.mediaID, 4, "", clientID)
	case "reprocessVideoPreparation":
		return api08Query(fx, operation, r.ready.mediaID, 2, video.DefaultProfile().Digest(), clientID)
	default:
		return api08Query(fx, operation, identity.ID{}, 0, r.plan.String(), clientID)
	}
}

func (r api08Resources) assertUnchanged(t *testing.T, fx api08Fixture) {
	t.Helper()
	r.failed.assertUnchanged(t, fx, "FAILED", 4)
	r.ready.assertUnchanged(t, fx, "READY", 2)
	var plans, audits int64
	if err := fx.h.AdminDB.Model(&database.VideoPlan{}).Where("tenant_id=? AND inspection_id=?", fx.seed.TenantID, fx.seed.InspectionID).Count(&plans).Error; err != nil {
		t.Fatal(err)
	}
	if err := fx.h.AdminDB.Model(&database.AuditEvent{}).Where("tenant_id=? AND action LIKE 'video.%'", fx.seed.TenantID).Count(&audits).Error; err != nil {
		t.Fatal(err)
	}
	if plans != 1 || audits != 0 {
		t.Fatalf("rejected API08 operation mutated state: plans=%d audits=%d", plans, audits)
	}
}

// assertAPI08Rejected also accepts the userError envelope used by reprocess
// and reanalysis, whose result fields must stay null.
func assertAPI08Rejected(t *testing.T, response, code string) {
	t.Helper()
	assertAPI06Rejected(t, response, code)
	for _, field := range []string{`"preparation":{`, `"planId":"`} {
		if strings.Contains(response, field) {
			t.Fatalf("rejected API08 operation reported a result: %s", response)
		}
	}
}
