//go:build integration

package resolvers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/contracts/events"
	"inspection/services/inspection/internal/features/analysis/classify_inspection"
	notificationcore "inspection/services/inspection/internal/features/notifications/core"
	"inspection/services/inspection/internal/features/reports/core"
	generatesnapshot "inspection/services/inspection/internal/features/reports/generate_snapshot"
	"inspection/services/inspection/internal/features/reports/handle_classified"
	"inspection/services/inspection/internal/features/templates/catalog"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	graphql1 "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/gorm"
)

type task04NoopNotificationService struct{}

func (task04NoopNotificationService) Send(context.Context, notificationcore.Notification) (notificationcore.NotificationResult, error) {
	return notificationcore.NotificationResult{}, nil
}

func TestIT016IT044VerifyReportReturnsOnlyPublicFieldsAndHidesUnknownCodes(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewPostgresFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	f, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{ComparisonMode: "CURRENT_ONLY"})
	if err != nil {
		t.Fatal(err)
	}
	input := generatesnapshot.Input{
		TenantID: f.TenantID, InspectionID: f.InspectionID, Mode: "HISTORICAL", Classification: "NORMAL",
		TemplateVersionID: f.TemplateVersionID.String(), ReferenceVersionID: generatesnapshot.ReferenceFree,
		PromptDigest: "verification-test", PublicationMode: "AUTOMATIC", PublicationPolicyVersion: 1,
		Context: core.Context{
			Asset:       core.AssetContext{ID: f.AssetID.String(), Name: "Imóvel QA", ExternalKey: "QA-1", Address: "Rua de teste"},
			Participant: core.ParticipantContext{ID: f.ParticipantID.String(), Name: "Responsável QA"},
			Template:    core.TemplateContext{ID: f.TemplateID.String(), Name: "Modelo QA", Version: 1},
			Inspection:  core.InspectionContext{GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano)},
		},
		Checklist:  &core.ChecklistSection{Rooms: []core.ChecklistRoom{}, Coverage: core.ChecklistCoverage{UnknownScope: true}},
		Processing: &core.ProcessingState{Status: "COMPLETED"}, Scope: &core.ScopeSnapshot{Unknown: true},
		Handover:   &core.HandoverSection{Entries: []core.HandoverEntry{}},
		Provenance: &core.Provenance{PromptRevision: "verification-test"},
	}
	created, err := generatesnapshot.Create(ctx, h.AdminDB, input, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	var snapshot database.ReportSnapshot
	if err := h.AdminDB.Where("id=?", created.SnapshotID).Take(&snapshot).Error; err != nil {
		t.Fatal(err)
	}
	var storedCode string
	if err := h.AdminDB.Raw("SELECT verification_code FROM reports.report_verification_codes WHERE tenant_id=? AND snapshot_id=?", f.TenantID, snapshot.ID).Scan(&storedCode).Error; err != nil || storedCode == "" {
		t.Fatalf("private PDF verification code missing: %v", err)
	}
	projectionJSON, _ := json.Marshal(mapReport(snapshot))
	if strings.Contains(string(snapshot.CanonicalJSON), storedCode) || strings.Contains(string(projectionJSON), storedCode) || strings.Contains(string(projectionJSON), "verificationCode") {
		t.Fatal("verification code was persisted in or projected by the staff report")
	}
	resolver := &queryResolver{Resolver: &Resolver{DB: h.DB}}
	verified, err := resolver.verifyReport(ctx, storedCode, created.Version)
	if err != nil {
		t.Fatal(err)
	}
	if verified.ReportNumber == nil || *verified.ReportNumber == "" || verified.Version == nil || *verified.Version != created.Version || verified.Digest == nil || *verified.Digest != created.JSONDigest || verified.Status == nil || *verified.Status != "PUBLISHED" || verified.IssuedAt == nil {
		t.Fatalf("verification public fields = %+v", verified)
	}
	// Simulate a pre-129 v3 snapshot that retained its verification code in
	// canonical JSON, then execute migration 129's backfill and verify by hash.
	legacyCode := base64.RawURLEncoding.EncodeToString([]byte("legacy-verification-code"))
	var legacyCanonical map[string]any
	if err := json.Unmarshal(snapshot.CanonicalJSON, &legacyCanonical); err != nil {
		t.Fatal(err)
	}
	legacyCanonical["reportId"] = "RPT-LEGACY-V3"
	legacyCanonical["verificationCode"] = legacyCode
	legacyJSON, err := json.Marshal(legacyCanonical)
	if err != nil {
		t.Fatal(err)
	}
	legacyID, now := identity.NewID(), time.Now().UTC()
	legacySnapshot := snapshot
	legacySnapshot.ID, legacySnapshot.VersionNumber = legacyID, created.Version+1
	legacySnapshot.JSONDigest, legacySnapshot.HTMLDigest = "legacy-v3-digest", "legacy-v3-html-digest"
	legacySnapshot.CanonicalJSON, legacySnapshot.HTML, legacySnapshot.CreatedAt = legacyJSON, json.RawMessage(`""`), now
	if err := h.AdminDB.Create(&legacySnapshot).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.AdminDB.Create(&database.ReportPublication{ID: identity.NewID(), TenantID: f.TenantID, SnapshotID: legacyID, InspectionID: f.InspectionID, ClientMutationID: "legacy-v3-backfill", PolicyVersion: 1, Status: "SUPERSEDED", ActorID: f.IdentityID, Version: 1, PublishedAt: &now, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.AdminDB.Exec(`INSERT INTO reports.report_verification_codes(tenant_id,snapshot_id,inspection_id,code_hash)
		SELECT r.tenant_id,r.id,r.inspection_id,encode(public.digest(r.canonical_json->>'verificationCode','sha256'),'hex')
		FROM reports.report_snapshots r WHERE r.id=? AND r.tenant_id=? AND r.schema_version>=3
		AND coalesce(r.canonical_json->>'verificationCode','')<>'' ON CONFLICT DO NOTHING`, legacyID, f.TenantID).Error; err != nil {
		t.Fatalf("simulate migration 129 verification-code backfill: %v", err)
	}
	if err := h.AdminDB.Exec(`UPDATE reports.report_verification_codes c SET verification_code=r.canonical_json->>'verificationCode'
		FROM reports.report_snapshots r WHERE r.id=c.snapshot_id AND r.tenant_id=c.tenant_id AND c.snapshot_id=? AND c.verification_code=''`, legacyID).Error; err != nil {
		t.Fatalf("simulate migration 129 recovery for PDF code: %v", err)
	}
	legacyVerified, err := resolver.verifyReport(ctx, legacyCode, legacySnapshot.VersionNumber)
	if err != nil || legacyVerified.ReportNumber == nil || *legacyVerified.ReportNumber != "RPT-LEGACY-V3" || legacyVerified.Digest == nil || *legacyVerified.Digest != legacySnapshot.JSONDigest {
		t.Fatalf("backfilled v3 snapshot verification=%+v err=%v", legacyVerified, err)
	}
	unknown, err := resolver.verifyReport(ctx, "unknown-verification-code-0123456789", created.Version)
	if err != nil {
		t.Fatal(err)
	}
	if unknown.ReportNumber != nil || unknown.Version != nil || unknown.Digest != nil || unknown.Status != nil || unknown.IssuedAt != nil {
		t.Fatalf("unknown code leaked report fields: %+v", unknown)
	}
	other, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{ComparisonMode: "CURRENT_ONLY"})
	if err != nil {
		t.Fatal(err)
	}
	otherInput := generatesnapshot.Input{
		TenantID: other.TenantID, InspectionID: other.InspectionID, Mode: "HISTORICAL", Classification: "ATTENTION",
		TemplateVersionID: other.TemplateVersionID.String(), ReferenceVersionID: generatesnapshot.ReferenceFree,
		PromptDigest: "verification-other-tenant", PublicationMode: "AUTOMATIC", PublicationPolicyVersion: 1,
		Context: core.Context{
			Asset:       core.AssetContext{ID: other.AssetID.String(), Name: "Imóvel QA privado", ExternalKey: "QA-2", Address: "Endereço privado"},
			Participant: core.ParticipantContext{ID: other.ParticipantID.String(), Name: "Pessoa privada"},
			Template:    core.TemplateContext{ID: other.TemplateID.String(), Name: "Modelo QA", Version: 1},
			Inspection:  core.InspectionContext{GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano)},
		},
		Checklist:  &core.ChecklistSection{Rooms: []core.ChecklistRoom{}, Coverage: core.ChecklistCoverage{UnknownScope: true}},
		Processing: &core.ProcessingState{Status: "COMPLETED"}, Scope: &core.ScopeSnapshot{Unknown: true},
		Handover: &core.HandoverSection{Entries: []core.HandoverEntry{}}, Provenance: &core.Provenance{PromptRevision: "private"},
	}
	otherCreated, err := generatesnapshot.Create(ctx, h.AdminDB, otherInput, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	var otherSnapshot database.ReportSnapshot
	if err := h.AdminDB.Where("tenant_id=? AND id=?", other.TenantID, otherCreated.SnapshotID).Take(&otherSnapshot).Error; err != nil {
		t.Fatal(err)
	}
	var otherCode string
	if err := h.AdminDB.Raw("SELECT verification_code FROM reports.report_verification_codes WHERE tenant_id=? AND snapshot_id=?", other.TenantID, otherSnapshot.ID).Scan(&otherCode).Error; err != nil || otherCode == "" {
		t.Fatalf("other tenant verification code missing: %v", err)
	}
	foreignCode, err := resolver.verifyReport(ctx, otherCode, otherCreated.Version)
	if err != nil || foreignCode.ReportNumber == nil || *foreignCode.ReportNumber != otherSnapshot.ID.String() || foreignCode.Version == nil || foreignCode.Digest == nil || foreignCode.Status == nil || foreignCode.IssuedAt == nil {
		t.Fatalf("cross-tenant code did not verify only the public identity: result=%+v err=%v", foreignCode, err)
	}
	wrongVersion, err := resolver.verifyReport(ctx, otherCode, otherCreated.Version+1)
	if err != nil || wrongVersion.ReportNumber != nil || wrongVersion.Digest != nil || wrongVersion.Status != nil {
		t.Fatalf("cross-tenant code/version mismatch result=%+v err=%v, want empty", wrongVersion, err)
	}
	for label, malformed := range map[string]string{"short": "short", "long": string(make([]byte, 129))} {
		got, err := resolver.verifyReport(ctx, malformed, created.Version)
		if err != nil || got.ReportNumber != nil || got.Version != nil || got.Digest != nil || got.Status != nil || got.IssuedAt != nil {
			t.Fatalf("%s code result=%+v error=%v, want empty payload", label, got, err)
		}
	}
	for _, status := range []string{"SUPERSEDED", "INVALIDATED"} {
		if err := h.AdminDB.Model(&database.ReportPublication{}).Where("tenant_id=? AND snapshot_id=?", f.TenantID, created.SnapshotID).Update("status", status).Error; err != nil {
			t.Fatal(err)
		}
		got, err := resolver.verifyReport(ctx, storedCode, created.Version)
		if err != nil || got.Status == nil || *got.Status != status {
			t.Fatalf("verification status=%+v error=%v, want %s", got, err, status)
		}
	}
}

func TestUT061IT059IT060ReissueReportIsIdempotentAndSerializesExpectedVersion(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewPostgresFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	f, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{ComparisonMode: "CURRENT_ONLY"})
	if err != nil {
		t.Fatal(err)
	}
	input := generatesnapshot.Input{
		TenantID: f.TenantID, InspectionID: f.InspectionID, Mode: "HISTORICAL", Classification: "NORMAL",
		TemplateVersionID: f.TemplateVersionID.String(), ReferenceVersionID: generatesnapshot.ReferenceFree,
		PromptDigest: "reissue-test", PublicationMode: "MANUAL", PublicationPolicyVersion: 1,
		Context: core.Context{
			Asset:       core.AssetContext{ID: f.AssetID.String(), Name: "Imóvel QA", ExternalKey: "QA-1", Address: "Rua de teste"},
			Participant: core.ParticipantContext{ID: f.ParticipantID.String(), Name: "Responsável QA"},
			Template:    core.TemplateContext{ID: f.TemplateID.String(), Name: "Modelo QA", Version: 1},
			Inspection:  core.InspectionContext{GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano)},
		},
		Checklist:  &core.ChecklistSection{Rooms: []core.ChecklistRoom{}, Coverage: core.ChecklistCoverage{UnknownScope: true}},
		Processing: &core.ProcessingState{Status: "COMPLETED"}, Scope: &core.ScopeSnapshot{Unknown: true},
		Handover: &core.HandoverSection{Entries: []core.HandoverEntry{}}, Provenance: &core.Provenance{PromptRevision: "reissue-test"},
	}
	created, err := generatesnapshot.Create(ctx, h.AdminDB, input, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	var inspectionBeforeReissue database.Inspection
	if err := h.AdminDB.Where("tenant_id=? AND id=?", f.TenantID, f.InspectionID).Take(&inspectionBeforeReissue).Error; err != nil {
		t.Fatal(err)
	}
	var compareJobsBefore int64
	if err := h.AdminDB.Model(&database.InventoryJob{}).Where("tenant_id=? AND inspection_id=? AND kind='COMPARE'", f.TenantID, f.InspectionID).Count(&compareJobsBefore).Error; err != nil {
		t.Fatal(err)
	}
	var original database.ReportSnapshot
	if err := h.AdminDB.Where("tenant_id=? AND id=?", f.TenantID, created.SnapshotID).Take(&original).Error; err != nil {
		t.Fatal(err)
	}
	originalCanonical := append([]byte(nil), original.CanonicalJSON...)
	meta := requestctx.Metadata{TenantID: f.TenantID, Principal: requestctx.Principal{IdentityID: f.IdentityID, MembershipID: f.MembershipID, TenantID: f.TenantID, Roles: []string{auth.TenantAdmin}, ProductEntitlements: auth.ProductsForRole(auth.TenantAdmin)}}
	resolver := &mutationResolver{Resolver: &Resolver{DB: h.DB}}
	mutationID := "it059-reissue-retry"
	callCtx := requestctx.WithMetadata(ctx, meta)
	type reissueResult struct {
		id      string
		payload *graphql1.ReissueReportPayload
		err     error
	}
	results := make(chan reissueResult, 2)
	var wg sync.WaitGroup
	for _, clientMutationID := range []string{mutationID, mutationID + "-concurrent"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			payload, callErr := resolver.reissueReport(callCtx, graphql1.ReissueReportInput{InspectionID: f.InspectionID.String(), ExpectedVersion: created.Version, ClientMutationID: id})
			results <- reissueResult{id: id, payload: payload, err: callErr}
		}(clientMutationID)
	}
	wg.Wait()
	close(results)
	var request *graphql1.ReissueReportPayload
	var winnerID string
	conflicts := 0
	for result := range results {
		if apperror.Is(result.err, apperror.Conflict) {
			conflicts++
			continue
		}
		if result.err != nil || result.payload == nil || result.payload.Report == nil || result.payload.Report.Version != created.Version {
			t.Fatalf("concurrent reissue=%+v", result)
		}
		request, winnerID = result.payload, result.id
	}
	if request == nil || conflicts != 1 {
		t.Fatalf("concurrent reissues winner=%+v conflicts=%d; want one queued effect and one conflict", request, conflicts)
	}
	var inspectionAfterReissue database.Inspection
	if err := h.AdminDB.Where("tenant_id=? AND id=?", f.TenantID, f.InspectionID).Take(&inspectionAfterReissue).Error; err != nil {
		t.Fatal(err)
	}
	var compareJobsAfter int64
	if err := h.AdminDB.Model(&database.InventoryJob{}).Where("tenant_id=? AND inspection_id=? AND kind='COMPARE'", f.TenantID, f.InspectionID).Count(&compareJobsAfter).Error; err != nil {
		t.Fatal(err)
	}
	if inspectionAfterReissue.Status != inspectionBeforeReissue.Status || inspectionAfterReissue.Version != inspectionBeforeReissue.Version || compareJobsAfter != compareJobsBefore {
		t.Fatalf("current-state reissue changed inspection lifecycle or scheduled comparison work: before=%s/%d/%d after=%s/%d/%d", inspectionBeforeReissue.Status, inspectionBeforeReissue.Version, compareJobsBefore, inspectionAfterReissue.Status, inspectionAfterReissue.Version, compareJobsAfter)
	}
	var queuedBeforeClassification int64
	if err := h.AdminDB.Model(&database.OutboxIntent{}).Where("tenant_id=? AND type='analysis.comparison_completed.v1' AND correlation_id LIKE 'reissue:%'", f.TenantID).Count(&queuedBeforeClassification).Error; err != nil || queuedBeforeClassification != 1 {
		t.Fatalf("concurrent reissue outbox count=%d err=%v; want exactly one", queuedBeforeClassification, err)
	}
	reissueJSON, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var verificationCode string
	if err := h.AdminDB.Raw("SELECT verification_code FROM reports.report_verification_codes WHERE tenant_id=? AND snapshot_id=?", f.TenantID, created.SnapshotID).Scan(&verificationCode).Error; err != nil || verificationCode == "" {
		t.Fatalf("PDF verification code missing: %v", err)
	}
	if strings.Contains(string(reissueJSON), verificationCode) || strings.Contains(string(reissueJSON), "verificationCode") {
		t.Fatal("reissueReport payload exposed a verification code")
	}
	// The reissued analysis now observes a newly confirmed component scope.
	document, err := json.Marshal(struct {
		Rooms           []catalog.ExpectedRoom   `json:"rooms"`
		HandoverRecords []catalog.HandoverRecord `json:"handoverRecords"`
	}{Rooms: []catalog.ExpectedRoom{{Key: "new:" + f.EnvironmentIdentityID.String(), TypeCode: "KITCHEN", Name: "Cozinha", Components: []catalog.ExpectedComponent{{Key: "floor", TypeCode: "FLOOR", Label: "Piso"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
		return tx.Create(&database.InspectionScopeSnapshot{ID: identity.NewID(), TenantID: f.TenantID, InspectionID: f.InspectionID, TemplateVersionID: f.TemplateVersionID, Profile: "PROPERTY_COMPONENTS", Document: document, Digest: "corrected-scope", Version: 1, CreatedAt: time.Now().UTC()}).Error
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
		now := time.Now().UTC()
		return tx.Create(&database.PublicationPolicy{ID: identity.NewID(), TenantID: f.TenantID, Mode: "MANUAL", Version: 2, CreatedAt: now, UpdatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	classifier, err := classify_inspection.Setup(classify_inspection.Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	classifyEnvelope := events.RawEnvelope{ID: identity.NewID(), Type: "analysis.comparison_completed.v1", SchemaVersion: 1, OccurredAt: time.Now().UTC(), TenantID: f.TenantID, AggregateID: f.InspectionID, CorrelationID: "it059-reissue-analysis", Payload: json.RawMessage(`{"inspectionId":"` + f.InspectionID.String() + `","force":true}`)}
	if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error { return classifier(ctx, tx, classifyEnvelope) }); err != nil {
		t.Fatalf("reissue classification failed: %v", err)
	}
	var classification database.ClassificationRun
	if err := h.AdminDB.Where("tenant_id=? AND inspection_id=?", f.TenantID, f.InspectionID).Order("created_at DESC").Take(&classification).Error; err != nil {
		t.Fatal(err)
	}
	classified, err := handle_classified.Setup(handle_classified.Dependencies{Notifications: task04NoopNotificationService{}, CreateSnapshotInTx: func(ctx context.Context, tx *gorm.DB, input generatesnapshot.Input) (generatesnapshot.Result, error) {
		return generatesnapshot.CreateInTx(ctx, tx, input, time.Now().UTC())
	}})
	if err != nil {
		t.Fatal(err)
	}
	classifiedEnvelope := events.RawEnvelope{ID: identity.NewID(), Type: "inspection.classified.v1", SchemaVersion: 1, OccurredAt: time.Now().UTC(), TenantID: f.TenantID, AggregateID: f.InspectionID, CorrelationID: "it059-reissue-publication", Payload: json.RawMessage(`{"inspectionId":"` + f.InspectionID.String() + `","classificationRunId":"` + classification.ID.String() + `","classification":"` + classification.Classification + `","reasonCodes":[]}`)}
	if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error { return classified(ctx, tx, classifiedEnvelope) }); err != nil {
		t.Fatalf("reissued report publication failed: %v", err)
	}
	retry, err := resolver.reissueReport(callCtx, graphql1.ReissueReportInput{InspectionID: f.InspectionID.String(), ExpectedVersion: created.Version, ClientMutationID: winnerID})
	if err != nil || retry.Report == nil || retry.Report.Version != created.Version+1 {
		t.Fatalf("reissue retry=%+v err=%v, want latest published version", retry, err)
	}
	var versionTwo database.ReportSnapshot
	if err := h.AdminDB.Where("tenant_id=? AND inspection_id=? AND version_number=2", f.TenantID, f.InspectionID).Take(&versionTwo).Error; err != nil {
		t.Fatal(err)
	}
	var versionOneSnapshot, versionTwoSnapshot core.Snapshot
	if json.Unmarshal(original.CanonicalJSON, &versionOneSnapshot) != nil || json.Unmarshal(versionTwo.CanonicalJSON, &versionTwoSnapshot) != nil || versionOneSnapshot.ReportID == "" || versionOneSnapshot.ReportID != versionTwoSnapshot.ReportID {
		t.Fatalf("report number changed between versions: v1=%q v2=%q", versionOneSnapshot.ReportID, versionTwoSnapshot.ReportID)
	}
	unchangedRequest, err := resolver.reissueReport(callCtx, graphql1.ReissueReportInput{InspectionID: f.InspectionID.String(), ExpectedVersion: versionTwo.VersionNumber, ClientMutationID: "it059-unchanged-facts"})
	if err != nil || unchangedRequest.Report == nil || unchangedRequest.Report.Version != versionTwo.VersionNumber {
		t.Fatalf("unchanged-facts request=%+v err=%v", unchangedRequest, err)
	}
	unchangedEnvelope := events.RawEnvelope{ID: identity.NewID(), Type: "analysis.comparison_completed.v1", SchemaVersion: 1, OccurredAt: time.Now().UTC(), TenantID: f.TenantID, AggregateID: f.InspectionID, CorrelationID: "it059-unchanged-analysis", Payload: json.RawMessage(`{"inspectionId":"` + f.InspectionID.String() + `","force":true}`)}
	if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error { return classifier(ctx, tx, unchangedEnvelope) }); err != nil {
		t.Fatalf("unchanged-facts classification failed: %v", err)
	}
	var unchangedClassification database.ClassificationRun
	if err := h.AdminDB.Where("tenant_id=? AND inspection_id=?", f.TenantID, f.InspectionID).Order("created_at DESC").Take(&unchangedClassification).Error; err != nil {
		t.Fatal(err)
	}
	unchangedClassifiedEnvelope := events.RawEnvelope{ID: identity.NewID(), Type: "inspection.classified.v1", SchemaVersion: 1, OccurredAt: time.Now().UTC(), TenantID: f.TenantID, AggregateID: f.InspectionID, CorrelationID: "it059-unchanged-publication", Payload: json.RawMessage(`{"inspectionId":"` + f.InspectionID.String() + `","classificationRunId":"` + unchangedClassification.ID.String() + `","classification":"` + unchangedClassification.Classification + `","reasonCodes":[]}`)}
	if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error { return classified(ctx, tx, unchangedClassifiedEnvelope) }); err != nil {
		t.Fatalf("unchanged-facts publication failed: %v", err)
	}
	var snapshotCount int64
	if err := h.AdminDB.Model(&database.ReportSnapshot{}).Where("tenant_id=? AND inspection_id=?", f.TenantID, f.InspectionID).Count(&snapshotCount).Error; err != nil || snapshotCount != 2 {
		t.Fatalf("unchanged report facts created another snapshot: count=%d err=%v", snapshotCount, err)
	}
	var historical database.ReportSnapshot
	if err := h.AdminDB.Where("tenant_id=? AND id=?", f.TenantID, created.SnapshotID).Take(&historical).Error; err != nil || string(historical.CanonicalJSON) != string(originalCanonical) {
		t.Fatalf("historical snapshot changed: error=%v", err)
	}
	var active int64
	if err := h.AdminDB.Model(&database.ReportPublication{}).Where("tenant_id=? AND inspection_id=? AND status='PUBLISHED'", f.TenantID, f.InspectionID).Count(&active).Error; err != nil || active != 1 {
		t.Fatalf("active published versions=%d err=%v, want 1", active, err)
	}
	staleCtx := requestctx.WithMetadata(ctx, meta)
	if _, err := resolver.reissueReport(staleCtx, graphql1.ReissueReportInput{InspectionID: f.InspectionID.String(), ExpectedVersion: created.Version, ClientMutationID: "it060-stale-version"}); !apperror.Is(err, apperror.Conflict) {
		t.Fatalf("stale expected version error=%v, want conflict", err)
	}
	if _, err := resolver.reissueReport(staleCtx, graphql1.ReissueReportInput{InspectionID: f.InspectionID.String(), ExpectedVersion: 99, ClientMutationID: "ut058-unknown-version"}); !apperror.Is(err, apperror.Conflict) {
		t.Fatalf("unknown expected version error=%v, want conflict", err)
	}
	var snapshots, queued int64
	if err := h.AdminDB.Model(&database.ReportSnapshot{}).Where("tenant_id=? AND inspection_id=?", f.TenantID, f.InspectionID).Count(&snapshots).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.AdminDB.Model(&database.OutboxIntent{}).Where("tenant_id=? AND type='analysis.comparison_completed.v1' AND correlation_id LIKE 'reissue:%'", f.TenantID).Count(&queued).Error; err != nil {
		t.Fatal(err)
	}
	if snapshots != 2 || queued != 2 {
		t.Fatalf("snapshots=%d queued reissue requests=%d, want 2/2", snapshots, queued)
	}
	// A completed request releases the per-inspection queue. Later fact changes
	// can be reissued at the same expected version when the preceding reissue
	// produced no new snapshot.
	if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.InspectionScopeSnapshot{}).Where("tenant_id=? AND inspection_id=?", f.TenantID, f.InspectionID).Updates(map[string]any{"digest": "scope-after-completion", "updated_at": time.Now().UTC()}).Error
	}); err != nil {
		t.Fatal(err)
	}
	stalePending := database.AuditEvent{ID: identity.NewID(), TenantID: f.TenantID, ActorID: f.MembershipID, Action: "report.reissue_requested", TargetType: "inspection", TargetID: f.InspectionID.String(), Outcome: "QUEUED", Reason: fmt.Sprintf("expectedVersion=%d", versionTwo.VersionNumber), CorrelationID: "it060-stale-queue", OccurredAt: time.Now().UTC().Add(-16 * time.Minute)}
	if err := h.AdminDB.Create(&stalePending).Error; err != nil {
		t.Fatal(err)
	}
	newKey := "it059-after-processed-request"
	accepted, err := resolver.reissueReport(callCtx, graphql1.ReissueReportInput{InspectionID: f.InspectionID.String(), ExpectedVersion: versionTwo.VersionNumber, ClientMutationID: newKey})
	if err != nil || accepted == nil || accepted.Report == nil || accepted.Report.Version != versionTwo.VersionNumber {
		t.Fatalf("new reissue after processing=%+v err=%v", accepted, err)
	}
	repeated, err := resolver.reissueReport(callCtx, graphql1.ReissueReportInput{InspectionID: f.InspectionID.String(), ExpectedVersion: versionTwo.VersionNumber, ClientMutationID: newKey})
	if err != nil || repeated == nil || repeated.Report == nil || repeated.Report.Version != versionTwo.VersionNumber {
		t.Fatalf("repeated clientMutationId=%+v err=%v", repeated, err)
	}
	if err := h.AdminDB.Model(&database.OutboxIntent{}).Where("tenant_id=? AND type='analysis.comparison_completed.v1' AND correlation_id LIKE 'reissue:%'", f.TenantID).Count(&queued).Error; err != nil || queued != 3 {
		t.Fatalf("reissue outbox count=%d err=%v; want one new effect for the new key", queued, err)
	}
}

func TestReissueReportWithoutSnapshotReturnsPortugueseNotFound(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("integration database URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewPostgresFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	f, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{ComparisonMode: "CURRENT_ONLY"})
	if err != nil {
		t.Fatal(err)
	}
	meta := requestctx.Metadata{TenantID: f.TenantID, Principal: requestctx.Principal{IdentityID: f.IdentityID, MembershipID: f.MembershipID, TenantID: f.TenantID, Roles: []string{auth.TenantAdmin}, ProductEntitlements: auth.ProductsForRole(auth.TenantAdmin)}}
	resolver := &mutationResolver{Resolver: &Resolver{DB: h.DB}}
	_, err = resolver.reissueReport(requestctx.WithMetadata(ctx, meta), graphql1.ReissueReportInput{InspectionID: f.InspectionID.String(), ExpectedVersion: 1, ClientMutationID: "no-current-snapshot"})
	if !apperror.Is(err, apperror.NotFound) || err.Error() != "Laudo não encontrado." {
		t.Fatalf("missing report error=%v; want safe Portuguese not found", err)
	}
}
