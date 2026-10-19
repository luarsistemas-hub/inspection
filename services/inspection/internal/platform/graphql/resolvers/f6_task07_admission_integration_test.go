//go:build integration

package resolvers

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	graph "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/requestctx"

	"github.com/99designs/gqlgen/graphql/handler"
	"gorm.io/gorm"
)

func TestVV_IT_371AdmissionEnabledStringFailsGraphQLCoercion(t *testing.T) {
	fx := newAPI06Fixture(t)
	before := configureVVAdmissionRow(t, fx, true, 7, "vv-it-371-baseline")
	response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture,
		`mutation { configureVideoAdmission(input:{enabled:"x",expectedVersion:7,reason:"bad scalar",clientMutationId:"vv-it-371-invalid"}) { admission { enabled version } } }`, fx.ctx, "", "")
	if !strings.Contains(response, "Boolean") && !strings.Contains(response, "Boolean!") {
		t.Fatalf("GraphQL did not reject a string for enabled: %s", response)
	}
	var after database.VideoAdmission
	if err := fx.h.AdminDB.Where("id=1").Take(&after).Error; err != nil {
		t.Fatal(err)
	}
	if after.Enabled != before.Enabled || after.Version != before.Version || after.MutationID != before.MutationID {
		t.Fatalf("coercion failure changed admission: before=%+v after=%+v", before, after)
	}
}

func TestVV_IT_374TenantAdministratorCannotConfigurePlatformAdmission(t *testing.T) {
	fx := newAPI06Fixture(t)
	before := configureVVAdmissionRow(t, fx, true, 2, "vv-it-374-baseline")
	response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture,
		`mutation { configureVideoAdmission(input:{enabled:false,expectedVersion:2,reason:"tenant request",clientMutationId:"vv-it-374-denied"}) { admission { enabled version } } }`, fx.ctx, "", "")
	assertFixedOriginTransportError(t, response, "FORBIDDEN")
	var after database.VideoAdmission
	if err := fx.h.AdminDB.Where("id=1").Take(&after).Error; err != nil {
		t.Fatal(err)
	}
	if after.Enabled != before.Enabled || after.Version != before.Version || after.MutationID != before.MutationID {
		t.Fatalf("tenant admin changed platform admission: before=%+v after=%+v", before, after)
	}
}

func TestVV_IT_367DisableRejectsNewVideoOriginAndCurrentResponsibilities(t *testing.T) {
	t.Run("origin invitation", func(t *testing.T) {
		fx := newAPI06Fixture(t)
		disableVVAdmission(t, fx, "vv-it-367-origin")
		before := vvOriginWorkCounts(t, fx)
		response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api06Query(fx, "inviteOriginCapture", "vv-it-367-origin-invite", ""), fx.ctx, "", "")
		assertFixedOriginTransportError(t, response, "INTEGRATION_DISABLED")
		if after := vvOriginWorkCounts(t, fx); after != before {
			t.Fatalf("disabled origin invitation created responsibility work: before=%+v after=%+v", before, after)
		}
	})
	t.Run("current inspection responsibility", func(t *testing.T) {
		fx := newFixedOriginGraphQLFixture(t, false)
		setFixedVideoAdmission(t, fx)
		originVersionID := seedFixedVideoOrigin(t, fx)
		admissionFixture := api06Fixture{fixedOriginGraphQLFixture: fx}
		disableVVAdmission(t, admissionFixture, "vv-it-367-current")
		clientID := "vv-it-367-current-create"
		response := executeFixedOriginEntryWithReference(t, fx, "createInspection", clientID, originVersionID.String())
		assertFixedOriginTransportError(t, response, "INTEGRATION_DISABLED")
		assertFixedOriginNoSideEffects(t, fx, clientID)
	})
}

func TestVV_IT_372DisabledOriginInviteWithNoPendingJobsCreatesNoWork(t *testing.T) {
	fx := newAPI06Fixture(t)
	disableVVAdmission(t, fx, "vv-it-372")
	var pendingBefore int64
	if err := fx.h.AdminDB.Model(&database.VideoPreparationJob{}).Where("tenant_id=? AND status IN ?", fx.seed.TenantID, []string{"QUEUED", "PROCESSING"}).Count(&pendingBefore).Error; err != nil {
		t.Fatal(err)
	}
	if pendingBefore != 0 {
		t.Fatalf("fixture has %d pending video jobs, want zero", pendingBefore)
	}
	before := vvOriginWorkCounts(t, fx)
	response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api06Query(fx, "inviteOriginCapture", "vv-it-372-invite", ""), fx.ctx, "", "")
	assertFixedOriginTransportError(t, response, "INTEGRATION_DISABLED")
	if after := vvOriginWorkCounts(t, fx); after != before {
		t.Fatalf("disabled invitation created fictitious work: before=%+v after=%+v", before, after)
	}
}

func TestVV_IT_378DisabledVideoInviteIsRejectedBeforeResponsibilityCreation(t *testing.T) {
	fx := newAPI06Fixture(t)
	disableVVAdmission(t, fx, "vv-it-378")
	before := vvOriginWorkCounts(t, fx)
	response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api06Query(fx, "inviteOriginCapture", "vv-it-378-invite", ""), fx.ctx, "", "")
	assertFixedOriginTransportError(t, response, "INTEGRATION_DISABLED")
	if after := vvOriginWorkCounts(t, fx); after != before {
		t.Fatalf("disabled invitation created a responsibility: before=%+v after=%+v", before, after)
	}
}

func TestVV_IT_379ResumeUsesCurrentPolicyAndKeepsT0PlusFiveDayDeadline(t *testing.T) {
	fixture := newAPI03FailureFixture(t, "beginVideoCapture")
	now := time.Now().UTC().Truncate(time.Microsecond)
	t0 := now.Add(-24 * time.Hour)
	deadline := t0.Add(5 * 24 * time.Hour)
	retentionDays := 5
	mediaID := identity.NewID()
	if err := fixture.harness.AdminDB.Create(&database.VideoSource{ID: identity.NewID(), TenantID: fixture.tenantID, MediaID: mediaID, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 1_000_000, Width: 2, Height: 2, FrameRate: 30, ScreeningStatus: "APPROVED", RetentionDays: &retentionDays, SourceExpiryAt: &deadline, Status: "RETAINED", CreatedAt: t0, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.harness.AdminDB.Exec(`INSERT INTO media.video_policies (tenant_id, max_duration_minutes, origin_retention_days, current_retention_days, version, last_mutation_id, last_payload_digest, updated_at) VALUES (?, 15, 30, 7, 2, 'vv-it-379-policy', '', ?) ON CONFLICT (tenant_id) DO UPDATE SET current_retention_days=7, version=2, updated_at=EXCLUDED.updated_at`, fixture.tenantID, now).Error; err != nil {
		t.Fatal(err)
	}
	if err := fixture.harness.AdminDB.Exec(`INSERT INTO platform.video_admission (id, enabled, version, changed_at, reason, mutation_id, payload_hash) VALUES (1, FALSE, 2, ?, 'suspended', 'vv-it-379-suspend', '') ON CONFLICT (id) DO UPDATE SET enabled=FALSE, version=2, changed_at=EXCLUDED.changed_at, reason=EXCLUDED.reason, mutation_id=EXCLUDED.mutation_id, payload_hash=''`, now).Error; err != nil {
		t.Fatal(err)
	}
	issuer, subject := "vv-it-379-issuer", "vv-it-379-operator"
	principal := requestctx.Principal{IdentityID: fixture.identityID, TenantID: fixture.tenantID, Issuer: issuer, Subject: subject, Product: auth.AdminProduct, ProductEntitlements: []string{auth.AdminProduct}, Roles: []string{auth.TenantAdmin}}
	operatorCtx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: fixture.tenantID, Principal: principal, CorrelationID: "vv-it-379"})
	operator := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{DB: fixture.harness.DB, Bus: fixture.bus, Authorizer: auth.Authorizer{}, SuperAdminIssuer: issuer, SuperAdminSubject: subject}}))
	operator.SetErrorPresenter(graph.PresentError)
	resume := `mutation { configureVideoAdmission(input:{enabled:true,expectedVersion:2,reason:"resume after one day",clientMutationId:"vv-it-379-resume"}) { admission { enabled version } } }`
	resumed := executeVideoAdmissionGraphQL(t, operator, resume, operatorCtx)
	if strings.Contains(resumed, `"errors"`) || !strings.Contains(resumed, `"enabled":true`) || !strings.Contains(resumed, `"version":3`) {
		t.Fatalf("admission did not resume at T0+1d: %s", resumed)
	}
	begin := `mutation { beginVideoCapture(input:{acquisitionSource:"CAMERA",clientMutationId:"vv-it-379-current-attempt"}) { attempt { kind policyVersion status } } }`
	started := executeAPI03WithCredentials(t, fixture, begin)
	if strings.Contains(started, `"errors"`) || !strings.Contains(started, `"kind":"CURRENT"`) || !strings.Contains(started, `"policyVersion":2`) {
		t.Fatalf("new attempt did not use the current VIDEO policy after resume: %s", started)
	}
	var source database.VideoSource
	if err := fixture.harness.AdminDB.Where("tenant_id=? AND media_id=?", fixture.tenantID, mediaID).Take(&source).Error; err != nil {
		t.Fatal(err)
	}
	if source.SourceExpiryAt == nil || !source.SourceExpiryAt.Equal(deadline) || !deadline.Equal(now.Add(4*24*time.Hour)) {
		t.Fatalf("C1 deadline moved after resume: expiry=%v, want T0+5d=%s", source.SourceExpiryAt, deadline)
	}
}

func TestVV_IT_368AcceptedVideoContinuesAndPermittedRecaptureKeepsVideoKind(t *testing.T) {
	fixture := newAPI03FailureFixture(t, "beginVideoCapture")
	if err := fixture.harness.AdminDB.Model(&database.VideoAdmission{}).Where("id=1").Updates(map[string]any{"enabled": false, "version": 2, "reason": "suspended after V1 acceptance", "mutation_id": "vv-it-368-suspend"}).Error; err != nil {
		t.Fatal(err)
	}
	continueV1 := `mutation { beginVideoCapture(input:{acquisitionSource:"CAMERA",clientMutationId:"vv-it-368-accepted-v1"}) { attempt { kind status } } }`
	continued := executeAPI03WithCredentials(t, fixture, continueV1)
	if strings.Contains(continued, `"errors"`) || !strings.Contains(continued, `"kind":"CURRENT"`) || !strings.Contains(continued, `"status":"ACTIVE"`) {
		t.Fatalf("already accepted V1 could not continue during suspension: %s", continued)
	}
	var attemptID identity.ID
	if err := fixture.harness.WithinTenant(context.Background(), fixture.tenantID, func(tx *gorm.DB) error {
		var attempt database.VideoAttempt
		if err := tx.Where("tenant_id=? AND responsibility_id=? AND idempotency_key=?", fixture.tenantID, fixture.responsibilityID, "vv-it-368-accepted-v1").Take(&attempt).Error; err != nil {
			return err
		}
		attemptID = attempt.ID
		if err := tx.Model(&attempt).Update("status", "REJECTED").Error; err != nil {
			return err
		}
		return tx.Model(&database.CaptureDraft{}).Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).Update("kind", "RECAPTURE").Error
	}); err != nil {
		t.Fatal(err)
	}
	issuer, subject := "vv-it-368-issuer", "vv-it-368-operator"
	principal := requestctx.Principal{IdentityID: fixture.identityID, TenantID: fixture.tenantID, Issuer: issuer, Subject: subject, Product: auth.AdminProduct, ProductEntitlements: []string{auth.AdminProduct}, Roles: []string{auth.TenantAdmin}}
	operatorCtx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: fixture.tenantID, Principal: principal, CorrelationID: "vv-it-368"})
	operator := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{DB: fixture.harness.DB, Bus: fixture.bus, Authorizer: auth.Authorizer{}, SuperAdminIssuer: issuer, SuperAdminSubject: subject}}))
	operator.SetErrorPresenter(graph.PresentError)
	resume := `mutation { configureVideoAdmission(input:{enabled:true,expectedVersion:2,reason:"resume accepted VIDEO work",clientMutationId:"vv-it-368-resume"}) { admission { enabled version } } }`
	resumed := executeVideoAdmissionGraphQL(t, operator, resume, operatorCtx)
	if strings.Contains(resumed, `"errors"`) || !strings.Contains(resumed, `"enabled":true`) {
		t.Fatalf("VIDEO admission did not resume: %s", resumed)
	}
	newRecapture := `mutation { beginVideoCapture(input:{acquisitionSource:"CAMERA",clientMutationId:"vv-it-368-recapture"}) { attempt { kind status } } }`
	recaptured := executeAPI03WithCredentials(t, fixture, newRecapture)
	if strings.Contains(recaptured, `"errors"`) || !strings.Contains(recaptured, `"kind":"SUPPLEMENT"`) || !strings.Contains(recaptured, `"status":"ACTIVE"`) {
		t.Fatalf("permitted recapture lost its VIDEO modality after resume: %s", recaptured)
	}
	var attempts []database.VideoAttempt
	var draft database.CaptureDraft
	if err := fixture.harness.WithinTenant(context.Background(), fixture.tenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).Order("created_at").Find(&attempts).Error; err != nil {
			return err
		}
		return tx.Where("tenant_id=? AND responsibility_id=?", fixture.tenantID, fixture.responsibilityID).Take(&draft).Error
	}); err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 2 || attempts[0].ID != attemptID || attempts[1].Kind != "SUPPLEMENT" || draft.MediaKind != "VIDEO" || draft.Kind != "RECAPTURE" {
		t.Fatalf("V1/recapture modality changed: attempts=%+v draft=%+v", attempts, draft)
	}
}

func TestVV_IT_369StoppedPreparationRemainsPendingOrFailedInStatus(t *testing.T) {
	fx := newAPI08Fixture(t)
	if err := fx.h.WithinTenant(fx.ctx, fx.seed.TenantID, func(tx *gorm.DB) error {
		if err := tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", fx.seed.TenantID, fx.seed.InspectionID).Update("status", "ANALYZING").Error; err != nil {
			return err
		}
		return tx.Model(&database.Responsibility{}).Where("tenant_id=? AND id=?", fx.seed.TenantID, fx.seed.ResponsibilityID).Update("status", "SUBMITTED").Error
	}); err != nil {
		t.Fatal(err)
	}
	for _, state := range []struct{ status, expected string }{{"PROCESSING", `"status":"PROCESSING"`}, {"FAILED", `"status":"FAILED"`}} {
		t.Run(state.status, func(t *testing.T) {
			preparation := seedAPI08Preparation(t, fx, state.status, 1, "RETAINED")
			response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api08Query(fx, "videoPreparation", preparation.mediaID, 0, "", ""), fx.ctx, "", "")
			if strings.Contains(response, `"errors"`) || !strings.Contains(response, state.expected) || strings.Contains(response, `"status":"COMPLETED"`) {
				t.Fatalf("stopped decoder/gateway state was reported as completed analysis: %s", response)
			}
			var snapshots int64
			if err := fx.h.AdminDB.Model(&database.ReportSnapshot{}).Where("tenant_id=? AND inspection_id=?", fx.seed.TenantID, fx.seed.InspectionID).Count(&snapshots).Error; err != nil {
				t.Fatal(err)
			}
			if snapshots != 0 {
				t.Fatalf("pending V1 unexpectedly has %d completed report snapshots", snapshots)
			}
		})
	}
}

func TestVV_IT_375VideoOriginInviteRacesAdmissionDisableWithLinearizedOutcome(t *testing.T) {
	fx := newAPI06Fixture(t)
	configureVVAdmissionRow(t, fx, true, 1, "vv-it-375-baseline")
	operator, operatorServer := vvAdmissionOperator(fx, "vv-it-375")
	fx.fixedOriginGraphQLFixture.server = operatorServer
	invitationBefore := vvOriginWorkCounts(t, fx)
	var start sync.WaitGroup
	start.Add(1)
	type result struct{ response string }
	inviteResult := make(chan result, 1)
	disableResult := make(chan result, 1)
	go func() {
		start.Wait()
		inviteResult <- result{response: executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, api06Query(fx, "inviteOriginCapture", "vv-it-375-racing-invite", ""), fx.ctx, "", "")}
	}()
	go func() {
		start.Wait()
		query := `mutation { configureVideoAdmission(input:{enabled:false,expectedVersion:1,reason:"race test",clientMutationId:"vv-it-375-disable"}) { admission { enabled version } } }`
		disableResult <- result{response: executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, query, operator, "", "")}
	}()
	start.Done()
	invite := <-inviteResult
	disabled := <-disableResult
	if strings.Contains(disabled.response, `"errors"`) || !strings.Contains(disabled.response, `"enabled":false`) {
		t.Fatalf("admission disable did not commit in the race: %s", disabled.response)
	}
	accepted := !strings.Contains(invite.response, "INTEGRATION_DISABLED") && !strings.Contains(invite.response, `"errors"`)
	if !accepted && !strings.Contains(invite.response, "INTEGRATION_DISABLED") {
		t.Fatalf("racing invitation returned an unexpected result: %s", invite.response)
	}
	after := vvOriginWorkCounts(t, fx)
	if accepted {
		if after.Versions != invitationBefore.Versions+1 || after.Invitations != invitationBefore.Invitations+1 || after.Responsibilities != invitationBefore.Responsibilities+1 {
			t.Fatalf("accepted invite was not fully persisted atomically: before=%+v after=%+v", invitationBefore, after)
		}
	} else if after != invitationBefore {
		t.Fatalf("rejected invite left partial work: before=%+v after=%+v", invitationBefore, after)
	}
}

func TestVV_IT_376LostAdmissionDisableResponseRefreshesCommittedVersion(t *testing.T) {
	fx := newAPI06Fixture(t)
	configureVVAdmissionRow(t, fx, true, 4, "vv-it-376-baseline")
	operator, server := vvAdmissionOperator(fx, "vv-it-376")
	fx.fixedOriginGraphQLFixture.server = server
	mutation := `mutation { configureVideoAdmission(input:{enabled:false,expectedVersion:4,reason:"incident response",clientMutationId:"vv-it-376-disable"}) { admission { enabled version } } }`
	_ = executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, mutation, operator, "", "") // Simulate a response lost after commit.
	refreshed := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, `query { videoAdmission { enabled version } }`, operator, "", "")
	if strings.Contains(refreshed, `"errors"`) || !strings.Contains(refreshed, `"enabled":false`) || !strings.Contains(refreshed, `"version":5`) {
		t.Fatalf("authoritative refresh did not expose committed disable version: %s", refreshed)
	}
}

func TestVV_IT_377AdmissionDisableReplayPreservesAuditAndSourceDeadline(t *testing.T) {
	fx := newAPI06Fixture(t)
	configureVVAdmissionRow(t, fx, true, 9, "vv-it-377-baseline")
	origin, err := fx.h.SeedVideoOriginFixture(context.Background(), harness.VideoOriginFixtureOptions{Active: false})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().UTC().Add(5 * 24 * time.Hour).Truncate(time.Microsecond)
	if err := fx.h.AdminDB.Model(&database.VideoSource{}).Where("tenant_id=? AND media_id=?", origin.TenantID, origin.MediaID).Update("source_expiry_at", deadline).Error; err != nil {
		t.Fatal(err)
	}
	operator, server := vvAdmissionOperator(fx, "vv-it-377")
	fx.fixedOriginGraphQLFixture.server = server
	mutation := `mutation { configureVideoAdmission(input:{enabled:false,expectedVersion:9,reason:"incident response",clientMutationId:"vv-it-377-disable"}) { admission { enabled version } } }`
	first := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, mutation, operator, "", "")
	if strings.Contains(first, `"errors"`) || !strings.Contains(first, `"version":10`) {
		t.Fatalf("first disable failed: %s", first)
	}
	var auditBefore int64
	if err := fx.h.AdminDB.Model(&database.AuditEvent{}).Where("action=? AND target_id=?", "video.admission_configured", "1").Count(&auditBefore).Error; err != nil {
		t.Fatal(err)
	}
	_ = executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, mutation, operator, "", "")
	var row database.VideoAdmission
	var source database.VideoSource
	var auditAfter int64
	if err := fx.h.AdminDB.Where("id=1").Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	if err := fx.h.AdminDB.Where("tenant_id=? AND media_id=?", origin.TenantID, origin.MediaID).Take(&source).Error; err != nil {
		t.Fatal(err)
	}
	if err := fx.h.AdminDB.Model(&database.AuditEvent{}).Where("action=? AND target_id=?", "video.admission_configured", "1").Count(&auditAfter).Error; err != nil {
		t.Fatal(err)
	}
	if row.Enabled || row.Version != 10 || auditBefore != auditAfter || source.SourceExpiryAt == nil || !source.SourceExpiryAt.Equal(deadline) {
		t.Fatalf("idempotent replay changed admission/audit/source deadline: admission=%+v audits=%d→%d expiry=%v", row, auditBefore, auditAfter, source.SourceExpiryAt)
	}
}

func configureVVAdmissionRow(t *testing.T, fx api06Fixture, enabled bool, version int64, mutationID string) database.VideoAdmission {
	t.Helper()
	row := database.VideoAdmission{ID: 1, Enabled: enabled, Version: version, ChangedAt: time.Now().UTC(), MutationID: mutationID, PayloadHash: "baseline"}
	if err := fx.h.AdminDB.Exec(`INSERT INTO platform.video_admission (id, enabled, version, changed_at, reason, mutation_id, payload_hash) VALUES (1, ?, ?, ?, 'fixture', ?, 'baseline') ON CONFLICT (id) DO UPDATE SET enabled=EXCLUDED.enabled, version=EXCLUDED.version, changed_at=EXCLUDED.changed_at, reason=EXCLUDED.reason, mutation_id=EXCLUDED.mutation_id, payload_hash=EXCLUDED.payload_hash`, enabled, version, row.ChangedAt, mutationID).Error; err != nil {
		t.Fatal(err)
	}
	return row
}

func disableVVAdmission(t *testing.T, fx api06Fixture, tag string) {
	t.Helper()
	baseline := configureVVAdmissionRow(t, fx, true, 1, tag+"-baseline")
	operator, server := vvAdmissionOperator(fx, tag)
	fx.fixedOriginGraphQLFixture.server = server
	query := `mutation { configureVideoAdmission(input:{enabled:false,expectedVersion:` + fmt.Sprint(baseline.Version) + `,reason:"` + tag + `",clientMutationId:"` + tag + `-disable"}) { admission { enabled version } } }`
	response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, query, operator, "", "")
	if strings.Contains(response, `"errors"`) || !strings.Contains(response, `"enabled":false`) || !strings.Contains(response, `"version":2`) {
		t.Fatalf("platform operator could not disable admission: %s", response)
	}
}

type vvOriginWorkCount struct{ Versions, Invitations, Responsibilities int64 }

func vvOriginWorkCounts(t *testing.T, fx api06Fixture) vvOriginWorkCount {
	t.Helper()
	var count vvOriginWorkCount
	queries := []struct {
		model any
		set   *int64
	}{
		{&database.OriginVersion{}, &count.Versions},
		{&database.Invitation{}, &count.Invitations},
		{&database.Responsibility{}, &count.Responsibilities},
	}
	for _, query := range queries {
		if err := fx.h.AdminDB.Model(query.model).Where("tenant_id=?", fx.seed.TenantID).Count(query.set).Error; err != nil {
			t.Fatal(err)
		}
	}
	return count
}

func vvAdmissionOperator(fx api06Fixture, tag string) (context.Context, *handler.Server) {
	issuer, subject := "vv-platform-issuer", "vv-platform-operator"
	principal := requestctx.Principal{IdentityID: fx.seed.IdentityID, TenantID: fx.seed.TenantID, Issuer: issuer, Subject: subject, Product: auth.AdminProduct, ProductEntitlements: []string{auth.AdminProduct}, Roles: []string{auth.TenantAdmin}}
	ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: fx.seed.TenantID, Principal: principal, CorrelationID: tag})
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{DB: fx.h.DB, Bus: fx.bus, Authorizer: auth.Authorizer{}, SuperAdminIssuer: issuer, SuperAdminSubject: subject}}))
	server.SetErrorPresenter(graph.PresentError)
	return ctx, server
}
