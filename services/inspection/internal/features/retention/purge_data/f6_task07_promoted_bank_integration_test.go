//go:build integration

package purge_data

import (
	"context"
	"os"
	"testing"
	"time"

	promoteinspectionvideo "inspection/services/inspection/internal/features/origins/promote_inspection_video"
	resolvereference "inspection/services/inspection/internal/features/origins/resolve_reference"
	retentioncore "inspection/services/inspection/internal/features/retention/core"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/requestctx"
)

func TestVV_IT_326PromotedOriginBankStillResolvesForComparisonAfterFormerInspectionPurge(t *testing.T) {
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
	if err := resolvereference.Setup(resolvereference.Dependencies{DB: h.DB, Bus: bus}); err != nil {
		t.Fatal(err)
	}
	actor := requestctx.WithMetadata(ctx, requestctx.Metadata{TenantID: bank.fixture.TenantID, Principal: requestctx.Principal{IdentityID: bank.fixture.IdentityID, MembershipID: bank.fixture.MembershipID, TenantID: bank.fixture.TenantID, Roles: []string{auth.TenantAdmin}}})
	raw, err := bus.Send(actor, promoteinspectionvideo.Command{TenantID: bank.fixture.TenantID, InspectionID: bank.fixture.InspectionID, ClientMutationID: "vv-it-326"})
	if err != nil {
		t.Fatal(err)
	}
	o2 := raw.(database.OriginVersion)
	purgeDB := h.MigrationDB
	if purgeDB == nil {
		purgeDB = h.AdminDB
	}
	clock := retentioncore.Clock{Class: retentioncore.EvidenceReports, StartsAt: time.Now().UTC().Add(-time.Hour), DueAt: time.Now().UTC().Add(-time.Minute)}
	if _, err := PurgeWithStore(ctx, purgeDB, h.Store, bank.fixture.TenantID, bank.fixture.InspectionID, clock); err != nil {
		t.Fatalf("former inspection V1 purge: %v", err)
	}
	var v1 int64
	if err := h.AdminDB.Model(&database.Inspection{}).Where("tenant_id=? AND id=? AND participant_id=?", bank.fixture.TenantID, bank.fixture.InspectionID, bank.fixture.ParticipantID).Count(&v1).Error; err != nil {
		t.Fatal(err)
	}
	if v1 != 0 {
		t.Fatal("former inspection V1 was not purged")
	}
	// A new comparative inspection resolves the active O2 from the retained bank.
	resolvedRaw, err := bus.Ask(actor, resolvereference.Query{TenantID: bank.fixture.TenantID, AssetID: bank.fixture.AssetID, MediaKind: "VIDEO"})
	if err != nil {
		t.Fatalf("O2 no longer supports comparison after V1 purge: %v", err)
	}
	resolved := resolvedRaw.(resolvereference.Result)
	if resolved.Version.ID != o2.ID || resolved.Version.VideoPreparationID == nil || *resolved.Version.VideoPreparationID != bank.preparationID {
		t.Fatalf("resolved reference=%+v; want O2 pinned to P1 %s", resolved.Version, bank.preparationID)
	}
	if len(resolved.VideoScope) != 1 || resolved.VideoScope[0].ComponentLabel != "sink" {
		t.Fatalf("O2 comparison scope=%+v; want the retained sink frame", resolved.VideoScope)
	}
	reader, _, err := (objectstore.VideoStore{Store: h.Store}).Open(ctx, bank.frameKey)
	if err != nil {
		t.Fatalf("O2 frame bytes unavailable after V1 purge: %v", err)
	}
	_ = reader.Close()
}
