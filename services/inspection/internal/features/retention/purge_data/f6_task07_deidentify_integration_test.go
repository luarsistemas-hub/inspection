//go:build integration

package purge_data

import (
	"context"
	"os"
	"testing"
	"time"

	"inspection/libs/identity"
	promoteinspectionvideo "inspection/services/inspection/internal/features/origins/promote_inspection_video"
	retentioncore "inspection/services/inspection/internal/features/retention/core"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/requestctx"
)

func TestVV_IT_606PurgeDeidentifiesSourceParticipantButKeepsPromotedBank(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	bank := seedTask04PromotableBank(t, h)
	bus := mediator.New()
	if err := promoteinspectionvideo.Setup(promoteinspectionvideo.Dependencies{DB: h.DB, Bus: bus, Authorizer: auth.Authorizer{}, Now: time.Now}); err != nil {
		t.Fatal(err)
	}
	promoteCtx := requestctx.WithMetadata(ctx, requestctx.Metadata{TenantID: bank.fixture.TenantID, Principal: requestctx.Principal{IdentityID: bank.fixture.IdentityID, MembershipID: bank.fixture.MembershipID, TenantID: bank.fixture.TenantID, Roles: []string{auth.TenantAdmin}}})
	raw, err := bus.Send(promoteCtx, promoteinspectionvideo.Command{TenantID: bank.fixture.TenantID, InspectionID: bank.fixture.InspectionID, ClientMutationID: "vv-it-606"})
	if err != nil {
		t.Fatal(err)
	}
	promoted := raw.(database.OriginVersion)
	purgeDB := h.MigrationDB
	if purgeDB == nil {
		purgeDB = h.AdminDB
	}
	clock := retentioncore.Clock{Class: retentioncore.EvidenceReports, StartsAt: time.Now().UTC().Add(-time.Hour), DueAt: time.Now().UTC().Add(-time.Minute)}
	if _, err := PurgeWithStore(ctx, purgeDB, h.Store, bank.fixture.TenantID, bank.fixture.InspectionID, clock); err != nil {
		t.Fatalf("former inspection purge: %v", err)
	}
	// P1 stays protected for the active promoted origin.
	var version database.OriginVersion
	if err := h.AdminDB.Where("tenant_id=? AND id=?", bank.fixture.TenantID, promoted.ID).Take(&version).Error; err != nil {
		t.Fatalf("promoted origin version removed by purge: %v", err)
	}
	var frames, references int64
	if err := h.AdminDB.Model(&database.VideoFrame{}).Where("tenant_id=? AND preparation_id=?", bank.fixture.TenantID, bank.preparationID).Count(&frames).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.AdminDB.Model(&database.VideoEvidenceReference{}).Where("tenant_id=? AND preparation_id=? AND owner_type='ORIGIN_VERSION' AND owner_id=?", bank.fixture.TenantID, bank.preparationID, promoted.ID).Count(&references).Error; err != nil {
		t.Fatal(err)
	}
	reader, _, openErr := (objectstore.VideoStore{Store: h.Store}).Open(ctx, bank.frameKey)
	if openErr == nil {
		_ = reader.Close()
	}
	if version.Status != "ACTIVE" || frames != 1 || references != 1 || openErr != nil {
		t.Fatalf("required P1 evidence lost: status=%s frames=%d references=%d objectErr=%v", version.Status, frames, references, openErr)
	}
	// The source responsibility survives only as a de-identified anchor.
	var responsibility database.Responsibility
	if err := h.AdminDB.Where("tenant_id=? AND id=?", bank.fixture.TenantID, version.ResponsibilityID).Take(&responsibility).Error; err != nil {
		t.Fatal(err)
	}
	if responsibility.ParticipantID != (identity.ID{}) || responsibility.InspectionID != (identity.ID{}) {
		t.Fatalf("source participant metadata kept after purge: participant=%s inspection=%s", responsibility.ParticipantID, responsibility.InspectionID)
	}
	var inspections int64
	if err := h.AdminDB.Model(&database.Inspection{}).Where("tenant_id=? AND id=? AND participant_id=?", bank.fixture.TenantID, bank.fixture.InspectionID, bank.fixture.ParticipantID).Count(&inspections).Error; err != nil {
		t.Fatal(err)
	}
	if inspections != 0 {
		t.Fatal("purged inspection still links the source participant")
	}
}
