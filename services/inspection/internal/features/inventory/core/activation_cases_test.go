package core

import (
	"context"
	"encoding/json"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

func TestService_AssignedActivationCases(t *testing.T) {
	t.Run("activation rechecks the per-environment limit", func(t *testing.T) {
		db := inventoryTestDB(t)
		tenant, version, env, actor := seedReadyDraft(t, db)
		seedActivationAsset(t, db, tenant, version)
		seedActivationItem(t, db, tenant, version, env, "ACCEPTED")
		for i := 0; i < MaxItemsPerEnvironment; i++ {
			seedActivationItem(t, db, tenant, version, env, "PENDING")
		}
		_, err := testService(db).Activate(context.Background(), ActivateInput{TenantID: tenant, OriginVersionID: version, ActorMembershipID: actor, Purpose: "MOVE_IN", ExpectedOriginVersion: 1})
		if !apperror.Is(err, apperror.InvalidState) {
			t.Fatalf("activation accepted an oversized environment: %v", err)
		}
	})
	t.Run("UT-056 expected empty environment remains on active version", func(t *testing.T) {
		db := inventoryTestDB(t)
		tenant, version, env, actor := seedReadyDraft(t, db)
		expectedEnv := database.InventoryEnvironment{ID: identity.NewID(), TenantID: tenant, OriginVersionID: version, IdentityID: identity.NewID(), TypeCode: "BEDROOM", Name: "Quarto esperado", Expected: true, Decision: "ACCEPTED"}
		if err := db.Create(&expectedEnv).Error; err != nil {
			t.Fatal(err)
		}
		seedActivationItem(t, db, tenant, version, env, "ACCEPTED")
		seedActivationAsset(t, db, tenant, version)
		row, err := testService(db).Activate(context.Background(), ActivateInput{TenantID: tenant, OriginVersionID: version, ActorMembershipID: actor, Purpose: "MOVE_IN", ExpectedOriginVersion: 1})
		if err != nil {
			t.Fatal(err)
		}
		var stored database.InventoryEnvironment
		if err := db.First(&stored, "id=?", expectedEnv.ID).Error; err != nil {
			t.Fatal(err)
		}
		var count int64
		db.Model(&database.InventoryItem{}).Where("origin_version_id=? AND environment_id=?", row.ID, expectedEnv.ID).Count(&count)
		if !stored.Expected || count != 0 || row.Status != "ACTIVE" {
			t.Fatalf("expected empty environment was dropped: env=%+v itemCount=%d version=%+v", stored, count, row)
		}
	})

	t.Run("UT-060 activates and supersedes prior baseline with counts", func(t *testing.T) {
		db := inventoryTestDB(t)
		tenant, version, env, actor := seedReadyDraft(t, db)
		seedActivationAsset(t, db, tenant, version)
		oldID := identity.NewID()
		var origin database.Origin
		if err := db.Where("tenant_id=? AND asset_id=?", tenant, assetIDForVersion(t, db, tenant, version)).First(&origin).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&database.OriginVersion{}).Where("id=?", version).Update("version_number", 2).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&database.OriginVersion{ID: oldID, TenantID: tenant, OriginID: origin.ID, VersionNumber: 1, Source: "LEGACY_ORIGIN", Status: "ACTIVE", IdempotencyKey: "old-" + oldID.String()}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&database.Origin{}).Where("id=?", origin.ID).Update("active_version_id", oldID).Error; err != nil {
			t.Fatal(err)
		}
		seedActivationItem(t, db, tenant, version, env, "ACCEPTED")
		seedActivationItem(t, db, tenant, version, env, "REJECTED")
		seedActivationItem(t, db, tenant, version, env, "PENDING")
		row, err := testService(db).Activate(context.Background(), ActivateInput{TenantID: tenant, OriginVersionID: version, ActorMembershipID: actor, Purpose: "MOVE_IN", ExpectedOriginVersion: 1})
		if err != nil {
			t.Fatal(err)
		}
		if row.Status != "ACTIVE" || row.Purpose == nil || *row.Purpose != "MOVE_IN" || row.ActivatedByMembershipID == nil || *row.ActivatedByMembershipID != actor || row.SupersedesID == nil || *row.SupersedesID != oldID {
			t.Fatalf("unexpected activation result: %+v", row)
		}
		var old database.OriginVersion
		if err := db.First(&old, "id=?", oldID).Error; err != nil || old.Status != "SUPERSEDED" {
			t.Fatalf("prior active not superseded: %+v %v", old, err)
		}
		if err := db.First(&origin, "id=?", origin.ID).Error; err != nil || origin.ActiveVersionID == nil || *origin.ActiveVersionID != version || origin.Version != 2 {
			t.Fatalf("origin pointer/version not updated: %+v %v", origin, err)
		}
		var audit database.InventoryDecision
		if err := db.Where("origin_version_id=? AND action='ACTIVATE'", version).First(&audit).Error; err != nil {
			t.Fatal(err)
		}
		var counts ItemCounts
		if err := json.Unmarshal(audit.After, &counts); err != nil {
			t.Fatal(err)
		}
		if counts.Included != 1 || counts.Rejected != 1 || counts.NotReviewed != 1 {
			t.Fatalf("unexpected activation counts: %+v", counts)
		}
	})

	for _, tc := range []struct {
		id, purpose, draftState string
		archived, removeEnv     bool
		expected                int64
		wantCode                apperror.Code
		wantField, wantMessage  string
	}{
		{id: "UT-061", purpose: "", wantCode: apperror.InvalidInput, wantField: "purpose", wantMessage: "unknown baseline purpose"},
		{id: "UT-062", purpose: "LEASE", wantCode: apperror.InvalidInput, wantField: "purpose", wantMessage: "unknown baseline purpose"},
		{id: "UT-063", purpose: "MOVE_IN", wantCode: apperror.InvalidState, wantField: "originVersionId", wantMessage: "NO_INCLUDED_ITEMS"},
		{id: "UT-064", purpose: "MOVE_IN", removeEnv: true, wantCode: apperror.InvalidState, wantField: "originVersionId", wantMessage: "NO_ENVIRONMENTS"},
		{id: "UT-066", purpose: "MOVE_IN", draftState: "PREPARING", wantCode: apperror.InvalidState, wantField: "originVersionId", wantMessage: "draft is not ready"},
		{id: "UT-067", purpose: "MOVE_IN", archived: true, wantCode: apperror.InvalidState, wantField: "assetId", wantMessage: "ASSET_ARCHIVED"},
		{id: "UT-068", purpose: "MOVE_IN", expected: 2, wantCode: apperror.Conflict, wantField: "expectedOriginVersion", wantMessage: "baseline changed; refresh and confirm again"},
	} {
		t.Run(tc.id, func(t *testing.T) {
			db := inventoryTestDB(t)
			tenant, version, env, actor := seedReadyDraft(t, db)
			seedActivationAsset(t, db, tenant, version)
			if tc.draftState != "" {
				if err := db.Model(&database.OriginVersion{}).Where("id=?", version).Update("draft_state", tc.draftState).Error; err != nil {
					t.Fatal(err)
				}
			}
			if tc.archived {
				if err := db.Model(&database.Asset{}).Where("tenant_id=? AND id=?", tenant, assetIDForVersion(t, db, tenant, version)).Update("status", "ARCHIVED").Error; err != nil {
					t.Fatal(err)
				}
			}
			if tc.removeEnv {
				if err := db.Where("id=?", env).Delete(&database.InventoryEnvironment{}).Error; err != nil {
					t.Fatal(err)
				}
			}
			if tc.id == "UT-063" {
				seedActivationItem(t, db, tenant, version, env, "PENDING")
			}
			if tc.id != "UT-063" && tc.id != "UT-064" && tc.draftState == "" {
				seedActivationItem(t, db, tenant, version, env, "ACCEPTED")
			}
			want := tc.expected
			if want == 0 {
				want = 1
			}
			_, err := testService(db).Activate(context.Background(), ActivateInput{TenantID: tenant, OriginVersionID: version, ActorMembershipID: actor, Purpose: tc.purpose, ExpectedOriginVersion: want})
			code, field, message := apperror.Public(err)
			if !apperror.Is(err, tc.wantCode) || field != tc.wantField || message != tc.wantMessage {
				t.Fatalf("got %s/%s/%s (%v), want %s/%s/%s", code, field, message, err, tc.wantCode, tc.wantField, tc.wantMessage)
			}
		})
	}

	t.Run("UT-065 activation excludes pending items and records notReviewed", func(t *testing.T) {
		db := inventoryTestDB(t)
		tenant, version, env, actor := seedReadyDraft(t, db)
		seedActivationAsset(t, db, tenant, version)
		seedActivationItem(t, db, tenant, version, env, "ACCEPTED")
		for range 3 {
			seedActivationItem(t, db, tenant, version, env, "PENDING")
		}
		_, err := testService(db).Activate(context.Background(), ActivateInput{TenantID: tenant, OriginVersionID: version, ActorMembershipID: actor, Purpose: "MOVE_IN", ExpectedOriginVersion: 1})
		if err != nil {
			t.Fatal(err)
		}
		var audit database.InventoryDecision
		if err := db.Where("origin_version_id=? AND action='ACTIVATE'", version).First(&audit).Error; err != nil {
			t.Fatal(err)
		}
		var counts ItemCounts
		if err := json.Unmarshal(audit.After, &counts); err != nil {
			t.Fatal(err)
		}
		if counts.Included != 1 || counts.NotReviewed != 3 {
			t.Fatalf("pending items were not omitted/count recorded: %+v", counts)
		}
		var pendingIncluded int64
		db.Model(&database.InventoryItem{}).Where("origin_version_id=? AND decision='PENDING'", version).Count(&pendingIncluded)
		if pendingIncluded != 3 {
			t.Fatalf("activation mutated pending decisions: %d", pendingIncluded)
		}
	})

	t.Run("UT-069 same-purpose activation retry adds no audit or outbox rows", func(t *testing.T) {
		db := inventoryTestDB(t)
		tenant, version, env, actor := seedReadyDraft(t, db)
		seedActivationAsset(t, db, tenant, version)
		seedActivationItem(t, db, tenant, version, env, "ACCEPTED")
		input := ActivateInput{TenantID: tenant, OriginVersionID: version, ActorMembershipID: actor, Purpose: "MOVE_IN", ExpectedOriginVersion: 1}
		if _, err := testService(db).Activate(context.Background(), input); err != nil {
			t.Fatal(err)
		}
		if _, err := testService(db).Activate(context.Background(), input); err != nil {
			t.Fatal(err)
		}
		var audits, outbox int64
		db.Model(&database.InventoryDecision{}).Where("origin_version_id=? AND action='ACTIVATE'", version).Count(&audits)
		db.Model(&database.OutboxIntent{}).Where("tenant_id=? AND type='inventory.version_activated.v1'", tenant).Count(&outbox)
		if audits != 1 || outbox != 1 {
			t.Fatalf("retry duplicated activation effects: audits=%d outbox=%d", audits, outbox)
		}
	})
}

type retentionTransitionSpy struct {
	calls    int
	tenant   identity.ID
	versions []identity.ID
}

func (s *retentionTransitionSpy) Transition(_ context.Context, _ *gorm.DB, tenant identity.ID, sources []identity.ID) error {
	s.calls++
	s.tenant = tenant
	s.versions = append([]identity.ID(nil), sources...)
	return nil
}

type counterpartyInviterSpy struct {
	calls                  int
	tenant, version, actor identity.ID
	participants           []identity.ID
}

func (s *counterpartyInviterSpy) Invite(_ *gorm.DB, tenant, version identity.ID, participants []identity.ID, actor identity.ID) error {
	s.calls++
	s.tenant = tenant
	s.version = version
	s.actor = actor
	s.participants = append([]identity.ID(nil), participants...)
	return nil
}

func TestService_AssignedActivationHooks(t *testing.T) {
	t.Run("UT-070 retention receives new and superseded version sources", func(t *testing.T) {
		db := inventoryTestDB(t)
		tenant, version, env, actor := seedReadyDraft(t, db)
		seedActivationAsset(t, db, tenant, version)
		seedActivationItem(t, db, tenant, version, env, "ACCEPTED")
		spy := &retentionTransitionSpy{}
		svc := testService(db)
		svc.Retention = spy
		if _, err := svc.Activate(context.Background(), ActivateInput{TenantID: tenant, OriginVersionID: version, ActorMembershipID: actor, Purpose: "MOVE_IN", ExpectedOriginVersion: 1}); err != nil {
			t.Fatal(err)
		}
		if spy.calls != 1 || spy.tenant != tenant {
			t.Fatalf("retention hook was not called: %+v", spy)
		}
	})
	t.Run("UT-071 counterparty hook receives selected participants in transaction", func(t *testing.T) {
		db := inventoryTestDB(t)
		tenant, version, env, actor := seedReadyDraft(t, db)
		seedActivationAsset(t, db, tenant, version)
		seedActivationItem(t, db, tenant, version, env, "ACCEPTED")
		participants := []identity.ID{identity.NewID(), identity.NewID()}
		spy := &counterpartyInviterSpy{}
		svc := testService(db)
		svc.Counterparties = spy
		if _, err := svc.Activate(context.Background(), ActivateInput{TenantID: tenant, OriginVersionID: version, ActorMembershipID: actor, Purpose: "MOVE_IN", ExpectedOriginVersion: 1, CounterpartyParticipantIDs: participants}); err != nil {
			t.Fatal(err)
		}
		if spy.calls != 1 || spy.tenant != tenant || spy.version != version || spy.actor != actor || len(spy.participants) != 2 || spy.participants[0] != participants[0] || spy.participants[1] != participants[1] {
			t.Fatalf("counterparty hook arguments differ: %+v", spy)
		}
	})
}

func seedActivationAsset(t *testing.T, db *gorm.DB, tenant, versionID identity.ID) {
	t.Helper()
	var version database.OriginVersion
	if err := db.Where("tenant_id=? AND id=?", tenant, versionID).First(&version).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO assets.assets (id,tenant_id,status) VALUES (?,?,?)", assetIDForVersion(t, db, tenant, versionID), tenant, "ACTIVE").Error; err != nil {
		t.Fatal(err)
	}
}

func assetIDForVersion(t *testing.T, db *gorm.DB, tenant, versionID identity.ID) identity.ID {
	t.Helper()
	var version database.OriginVersion
	if err := db.Where("tenant_id=? AND id=?", tenant, versionID).First(&version).Error; err != nil {
		t.Fatal(err)
	}
	var origin database.Origin
	if err := db.Where("tenant_id=? AND id=?", tenant, version.OriginID).First(&origin).Error; err != nil {
		t.Fatal(err)
	}
	return origin.AssetID
}

func seedActivationItem(t *testing.T, db *gorm.DB, tenant, version, environment identity.ID, decision string) {
	t.Helper()
	item := database.InventoryItem{ID: identity.NewID(), TenantID: tenant, OriginVersionID: version, EnvironmentID: environment, IdentityID: identity.NewID(), TypeCode: "OTHER", Name: "Item", Condition: "GOOD", Observation: "OBSERVED", Origin: "AI", Decision: decision, Version: 1}
	if err := db.Create(&item).Error; err != nil {
		t.Fatal(err)
	}
}
