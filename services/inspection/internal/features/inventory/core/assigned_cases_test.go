package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
)

func TestService_AssignedDecisionCases(t *testing.T) {
	t.Run("ACCEPT applies a suggested condition while REJECT preserves the prior condition", func(t *testing.T) {
		db := inventoryTestDB(t)
		tenant, version, env, actor := seedReadyDraft(t, db)
		for i, action := range []string{"ACCEPT", "REJECT"} {
			item := database.InventoryItem{ID: identity.NewID(), TenantID: tenant, OriginVersionID: version, EnvironmentID: env, IdentityID: identity.NewID(), TypeCode: "OTHER", Name: "Comparison", Condition: "GOOD", Observation: "OBSERVED", Origin: "AI", Decision: "PENDING", Suggested: json.RawMessage(`{"condition":"FAIR"}`), Version: 1}
			if err := db.Create(&item).Error; err != nil {
				t.Fatal(err)
			}
			decision := Decision{ItemID: item.ID, Action: action, ExpectedVersion: 1, Reason: "DUPLICATE"}
			result, err := testService(db).Decide(context.Background(), DecideInput{TenantID: tenant, OriginVersionID: version, ActorMembershipID: actor, Decisions: []Decision{decision}})
			if err != nil {
				t.Fatal(err)
			}
			want := "GOOD"
			if i == 0 {
				want = "FAIR"
			}
			if result.Items[0].Condition != want {
				t.Fatalf("%s condition=%s want %s", action, result.Items[0].Condition, want)
			}
			var audit database.InventoryDecision
			if err := db.Where("tenant_id=? AND target_id=? AND action=?", tenant, item.ID, action).First(&audit).Error; err != nil {
				t.Fatal(err)
			}
			if action == "REJECT" && (audit.Reason == nil || *audit.Reason != "DUPLICATE") {
				t.Fatalf("rejection trail lost reason code: %+v", audit)
			}
		}
	})
	t.Run("UT-024 edit condition preserves suggestion and before after trail", func(t *testing.T) {
		db := inventoryTestDB(t)
		tenant, version, env, actor := seedReadyDraft(t, db)
		item := database.InventoryItem{ID: identity.NewID(), TenantID: tenant, OriginVersionID: version, EnvironmentID: env, IdentityID: identity.NewID(), TypeCode: "OTHER", Name: "Item", Condition: "GOOD", Observation: "OBSERVED", Origin: "AI", Decision: "PENDING", Suggested: json.RawMessage(`{"condition":"GOOD"}`), Version: 1}
		if err := db.Create(&item).Error; err != nil {
			t.Fatal(err)
		}
		fair := "FAIR"
		result, err := testService(db).Decide(context.Background(), DecideInput{TenantID: tenant, OriginVersionID: version, ActorMembershipID: actor, Decisions: []Decision{{ItemID: item.ID, Action: "EDIT", ExpectedVersion: 1, Patch: &ItemPatch{Condition: &fair}}}})
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Items) != 1 || result.Items[0].Condition != "FAIR" || result.Items[0].Decision != "EDITED" || string(result.Items[0].Suggested) != string(item.Suggested) {
			t.Fatalf("unexpected edit: %+v", result)
		}
		var trail database.InventoryDecision
		if err := db.Where("target_id=? AND action='EDIT'", item.ID).First(&trail).Error; err != nil {
			t.Fatal(err)
		}
		var before, after map[string]any
		if err := json.Unmarshal(trail.Before, &before); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(trail.After, &after); err != nil {
			t.Fatal(err)
		}
		if before["Condition"] != "GOOD" || after["Condition"] != "FAIR" {
			t.Fatalf("incorrect before/after trail: %v -> %v", before, after)
		}
	})
	t.Run("UT-025 edit material to unknown clears it", func(t *testing.T) {
		db := inventoryTestDB(t)
		tenant, version, env, actor := seedReadyDraft(t, db)
		material := "Madeira"
		item := database.InventoryItem{ID: identity.NewID(), TenantID: tenant, OriginVersionID: version, EnvironmentID: env, IdentityID: identity.NewID(), TypeCode: "OTHER", Name: "Item", Material: &material, Condition: "GOOD", Observation: "OBSERVED", Origin: "AI", Decision: "PENDING", Version: 1}
		if err := db.Create(&item).Error; err != nil {
			t.Fatal(err)
		}
		empty := ""
		result, err := testService(db).Decide(context.Background(), DecideInput{TenantID: tenant, OriginVersionID: version, ActorMembershipID: actor, Decisions: []Decision{{ItemID: item.ID, Action: "EDIT", ExpectedVersion: 1, Patch: &ItemPatch{Material: &empty}}}})
		if err != nil {
			t.Fatal(err)
		}
		if result.Items[0].Material != nil || result.Items[0].Decision != "EDITED" {
			t.Fatalf("material was not cleared: %+v", result.Items[0])
		}
	})
	t.Run("UT-026 edit to another version environment is invalid", func(t *testing.T) {
		db := inventoryTestDB(t)
		tenant, version, env, actor := seedReadyDraft(t, db)
		_, otherVersion, otherEnv, _ := seedReadyDraft(t, db)
		_ = otherVersion
		item := database.InventoryItem{ID: identity.NewID(), TenantID: tenant, OriginVersionID: version, EnvironmentID: env, IdentityID: identity.NewID(), TypeCode: "OTHER", Name: "Item", Condition: "GOOD", Observation: "OBSERVED", Origin: "AI", Decision: "PENDING", Version: 1}
		if err := db.Create(&item).Error; err != nil {
			t.Fatal(err)
		}
		_, err := testService(db).Decide(context.Background(), DecideInput{TenantID: tenant, OriginVersionID: version, ActorMembershipID: actor, Decisions: []Decision{{ItemID: item.ID, Action: "EDIT", ExpectedVersion: 1, Patch: &ItemPatch{EnvironmentID: &otherEnv}}}})
		_, field, _ := apperror.Public(err)
		if !apperror.Is(err, apperror.InvalidInput) || field != "environmentId" {
			t.Fatalf("unexpected cross-version environment result: %v", err)
		}
	})
	t.Run("UT-027 reject with duplicate reason", func(t *testing.T) {
		db := inventoryTestDB(t)
		tenant, version, env, actor := seedReadyDraft(t, db)
		item := database.InventoryItem{ID: identity.NewID(), TenantID: tenant, OriginVersionID: version, EnvironmentID: env, IdentityID: identity.NewID(), TypeCode: "OTHER", Name: "Item", Condition: "GOOD", Observation: "OBSERVED", Origin: "AI", Decision: "PENDING", Version: 1}
		if err := db.Create(&item).Error; err != nil {
			t.Fatal(err)
		}
		result, err := testService(db).Decide(context.Background(), DecideInput{TenantID: tenant, OriginVersionID: version, ActorMembershipID: actor, Decisions: []Decision{{ItemID: item.ID, Action: "REJECT", ExpectedVersion: 1, Reason: "DUPLICATE"}}})
		if err != nil {
			t.Fatal(err)
		}
		if result.Items[0].Decision != "REJECTED" || result.Items[0].RejectionReason == nil || *result.Items[0].RejectionReason != "DUPLICATE" {
			t.Fatalf("unexpected rejection: %+v", result.Items[0])
		}
	})
	t.Run("UT-031 accepts 200 decisions and rejects 201", func(t *testing.T) {
		db := inventoryTestDB(t)
		tenant, version, env, actor := seedReadyDraft(t, db)
		items := make([]database.InventoryItem, 200)
		decisions := make([]Decision, 200)
		for i := range items {
			items[i] = database.InventoryItem{ID: identity.NewID(), TenantID: tenant, OriginVersionID: version, EnvironmentID: env, IdentityID: identity.NewID(), TypeCode: "OTHER", Name: "Item", Condition: "GOOD", Observation: "OBSERVED", Origin: "AI", Decision: "PENDING", Version: 1}
			if err := db.Create(&items[i]).Error; err != nil {
				t.Fatal(err)
			}
			decisions[i] = Decision{ItemID: items[i].ID, Action: "ACCEPT", ExpectedVersion: 1}
		}
		result, err := testService(db).Decide(context.Background(), DecideInput{TenantID: tenant, OriginVersionID: version, ActorMembershipID: actor, Decisions: decisions})
		if err != nil || len(result.Items) != 200 {
			t.Fatalf("200 decisions failed: items=%d err=%v", len(result.Items), err)
		}
		_, err = testService(db).Decide(context.Background(), DecideInput{TenantID: tenant, OriginVersionID: version, ActorMembershipID: actor, Decisions: make([]Decision, 201)})
		_, field, _ := apperror.Public(err)
		if !apperror.Is(err, apperror.InvalidInput) || field != "decisions" {
			t.Fatalf("201 decisions accepted: %v", err)
		}
	})
}

func TestService_UT054RemovedEnvironmentCannotBeReused(t *testing.T) {
	db := inventoryTestDB(t)
	tenant, version, first, actor := seedReadyDraft(t, db)
	second := database.InventoryEnvironment{ID: identity.NewID(), TenantID: tenant, OriginVersionID: version, IdentityID: identity.NewID(), TypeCode: "OTHER", Name: "Removed", Decision: "ADDED", Version: 1}
	if err := db.Create(&second).Error; err != nil {
		t.Fatal(err)
	}
	svc := testService(db)
	if err := svc.RemoveEnvironment(context.Background(), RemoveEnvironmentInput{TenantID: tenant, OriginVersionID: version, EnvironmentID: second.ID, ActorMembershipID: actor}); err != nil {
		t.Fatal(err)
	}
	_, err := svc.AddItem(context.Background(), AddItemInput{TenantID: tenant, OriginVersionID: version, EnvironmentID: second.ID, ActorMembershipID: actor, TypeCode: "OTHER", Name: "Hidden"})
	if !apperror.Is(err, apperror.InvalidState) {
		t.Fatalf("add into removed environment error=%v", err)
	}
	_, err = svc.UpsertEnvironment(context.Background(), UpsertEnvironmentInput{TenantID: tenant, OriginVersionID: version, EnvironmentID: second.ID, ActorMembershipID: actor, TypeCode: "OTHER", Name: "Reopened"})
	if !apperror.Is(err, apperror.InvalidState) {
		t.Fatalf("upsert removed environment error=%v", err)
	}
	_ = first
}

func TestService_UT030EvidenceUnavailableRequiresAllEvidenceUnavailable(t *testing.T) {
	for _, tc := range []struct {
		name     string
		statuses []string
		wantErr  bool
	}{{"all purged", []string{"PURGED", "PURGED"}, true}, {"mixed purged and ready", []string{"PURGED", "READY"}, false}} {
		t.Run(tc.name, func(t *testing.T) {
			db := inventoryTestDB(t)
			tenant, version, env, actor := seedReadyDraft(t, db)
			item := database.InventoryItem{ID: identity.NewID(), TenantID: tenant, OriginVersionID: version, EnvironmentID: env, IdentityID: identity.NewID(), TypeCode: "OTHER", Name: "Evidence", Condition: "GOOD", Observation: "OBSERVED", Origin: "AI", Decision: "PENDING", Version: 1}
			if err := db.Create(&item).Error; err != nil {
				t.Fatal(err)
			}
			for _, status := range tc.statuses {
				media := identity.NewID()
				if err := db.Exec("INSERT INTO media.media_objects (id,tenant_id,responsibility_id,status,media_kind) VALUES (?,?,?,?,?)", media, tenant, identity.NewID(), status, "PHOTO").Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Create(&database.InventoryItemEvidence{ID: identity.NewID(), TenantID: tenant, ItemID: item.ID, Kind: "PHOTO", MediaID: media}).Error; err != nil {
					t.Fatal(err)
				}
			}
			_, err := testService(db).Decide(context.Background(), DecideInput{TenantID: tenant, OriginVersionID: version, ActorMembershipID: actor, Decisions: []Decision{{ItemID: item.ID, Action: "ACCEPT", ExpectedVersion: 1}}})
			if tc.wantErr && !apperror.Is(err, apperror.InvalidState) {
				t.Fatalf("expected EVIDENCE_UNAVAILABLE, got %v", err)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("mixed evidence was blocked: %v", err)
			}
		})
	}
}

func TestService_UT043EvidenceMustBelongToSourceVersion(t *testing.T) {
	t.Run("legacy origin rejects a foreign video frame", func(t *testing.T) {
		db := inventoryTestDB(t)
		tenant, version, env, actor := seedReadyDraft(t, db)
		var origin database.Origin
		if err := db.Model(&database.Origin{}).Where("tenant_id=?", tenant).First(&origin).Error; err != nil {
			t.Fatal(err)
		}
		sourceMedia, foreignMedia, prepID, foreignPrep, frameID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
		source := database.OriginVersion{ID: identity.NewID(), TenantID: tenant, OriginID: origin.ID, VersionNumber: 2, MediaKind: "VIDEO", VideoPreparationID: &prepID, Status: "ACTIVE", IdempotencyKey: "legacy-source-" + identity.NewID().String()}
		if err := db.Create(&source).Error; err != nil {
			t.Fatal(err)
		}
		for _, media := range []identity.ID{sourceMedia, foreignMedia} {
			if err := db.Exec("INSERT INTO media.media_objects (id,tenant_id,responsibility_id,status,media_kind) VALUES (?,?,?,?,?)", media, tenant, identity.NewID(), "READY", "VIDEO").Error; err != nil {
				t.Fatal(err)
			}
		}
		if err := db.Exec("CREATE TABLE media.video_preparation_jobs (id text, tenant_id text, media_id text)").Error; err != nil {
			t.Fatal(err)
		}
		for id, media := range map[identity.ID]identity.ID{prepID: sourceMedia, foreignPrep: foreignMedia} {
			if err := db.Exec("INSERT INTO media.video_preparation_jobs (id,tenant_id,media_id) VALUES (?,?,?)", id, tenant, media).Error; err != nil {
				t.Fatal(err)
			}
		}
		if err := db.Exec("INSERT INTO media.video_frames (id,tenant_id,source_media_id,preparation_id,pts_us,sha256,object_key,width,height) VALUES (?,?,?,?,?,?,?,?,?)", frameID, tenant, foreignMedia, foreignPrep, 1, "foreign", "foreign-frame", 1, 1).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&database.OriginVersion{}).Where("id=?", version).Updates(map[string]any{"source": "LEGACY_ORIGIN", "source_version_id": source.ID}).Error; err != nil {
			t.Fatal(err)
		}
		_, err := testService(db).AddItem(context.Background(), AddItemInput{TenantID: tenant, OriginVersionID: version, EnvironmentID: env, ActorMembershipID: actor, TypeCode: "OTHER", Name: "Foreign frame", Evidence: []EvidenceInput{{Kind: "VIDEO_FRAME", MediaID: foreignMedia, FrameID: &frameID}}})
		if !apperror.Is(err, apperror.InvalidInput) {
			t.Fatalf("foreign legacy frame accepted: %v", err)
		}
	})
	t.Run("contestation rejects a foreign photo", func(t *testing.T) {
		db := inventoryTestDB(t)
		tenant, version, env, actor := seedReadyDraft(t, db)
		var origin database.Origin
		if err := db.Model(&database.Origin{}).Where("tenant_id=?", tenant).First(&origin).Error; err != nil {
			t.Fatal(err)
		}
		sourceID, sourceEnv, sourceItem, sourceMedia, foreignMedia := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
		if err := db.Create(&database.OriginVersion{ID: sourceID, TenantID: tenant, OriginID: origin.ID, VersionNumber: 2, Source: "CAPTURE", HasInventory: true, Status: "ACTIVE", IdempotencyKey: "contest-source-" + identity.NewID().String()}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&database.InventoryEnvironment{ID: sourceEnv, TenantID: tenant, OriginVersionID: sourceID, IdentityID: identity.NewID(), TypeCode: "OTHER", Name: "Room", Decision: "ACCEPTED"}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&database.InventoryItem{ID: sourceItem, TenantID: tenant, OriginVersionID: sourceID, EnvironmentID: sourceEnv, IdentityID: identity.NewID(), TypeCode: "OTHER", Name: "Item", Condition: "GOOD", Observation: "OBSERVED", Origin: "MANUAL", Decision: "ACCEPTED", Version: 1}).Error; err != nil {
			t.Fatal(err)
		}
		for _, media := range []identity.ID{sourceMedia, foreignMedia} {
			if err := db.Exec("INSERT INTO media.media_objects (id,tenant_id,responsibility_id,status,media_kind) VALUES (?,?,?,?,?)", media, tenant, identity.NewID(), "READY", "PHOTO").Error; err != nil {
				t.Fatal(err)
			}
		}
		if err := db.Create(&database.InventoryItemEvidence{ID: identity.NewID(), TenantID: tenant, ItemID: sourceItem, Kind: "PHOTO", MediaID: sourceMedia}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&database.OriginVersion{}).Where("id=?", version).Updates(map[string]any{"source": "CONTESTATION", "source_version_id": sourceID}).Error; err != nil {
			t.Fatal(err)
		}
		_, err := testService(db).AddItem(context.Background(), AddItemInput{TenantID: tenant, OriginVersionID: version, EnvironmentID: env, ActorMembershipID: actor, TypeCode: "OTHER", Name: "Foreign photo", Evidence: []EvidenceInput{{Kind: "PHOTO", MediaID: foreignMedia}}})
		if !apperror.Is(err, apperror.InvalidInput) {
			t.Fatalf("foreign contestation photo accepted: %v", err)
		}
	})
}

func TestService_UT010RequestCatalogingErrorPrecedence(t *testing.T) {
	db := inventoryTestDB(t)
	tenant, asset := identity.NewID(), identity.NewID()
	if err := db.Exec("INSERT INTO assets.assets (id,tenant_id,status) VALUES (?,?,?)", asset, tenant, "ACTIVE").Error; err != nil {
		t.Fatal(err)
	}
	_, err := testService(db).RequestOriginCataloging(context.Background(), tenant, asset, identity.NewID())
	if code, field, _ := apperror.Public(err); code != apperror.InvalidState || field != "assetId" {
		t.Fatalf("missing origin error=(%s,%s), want InvalidState/assetId", code, field)
	}
	origin := database.Origin{ID: identity.NewID(), TenantID: tenant, AssetID: asset, Version: 1}
	if err := db.Create(&origin).Error; err != nil {
		t.Fatal(err)
	}
	state := "READY"
	draft := database.OriginVersion{ID: identity.NewID(), TenantID: tenant, OriginID: origin.ID, VersionNumber: 1, Source: "CAPTURE", HasInventory: true, DraftState: &state, Status: "DRAFT", IdempotencyKey: "open-draft-" + identity.NewID().String()}
	if err := db.Create(&draft).Error; err != nil {
		t.Fatal(err)
	}
	_, err = testService(db).RequestOriginCataloging(context.Background(), tenant, asset, identity.NewID())
	if code, field, message := apperror.Public(err); code != apperror.Conflict || field != "originVersionId" || message != draft.ID.String() {
		t.Fatalf("open draft precedence error=(%s,%s,%s), want Conflict with %s", code, field, message, draft.ID)
	}
}

func TestService_UT017StartFromNonCompletedInspection(t *testing.T) {
	db := inventoryTestDB(t)
	tenant, inspectionID := identity.NewID(), identity.NewID()
	if err := db.Exec("INSERT INTO inspections.inspections (id, tenant_id, status) VALUES (?, ?, ?)", inspectionID, tenant, "ANALYZING").Error; err != nil {
		t.Fatal(err)
	}
	_, err := testService(db).StartFromInspection(context.Background(), tenant, inspectionID, identity.NewID())
	_, field, _ := apperror.Public(err)
	if !apperror.Is(err, apperror.InvalidState) || field != "inspectionId" {
		t.Fatalf("unexpected UT-017 result: %v", err)
	}
}

func TestService_AssignedAddEvidenceCases(t *testing.T) {
	t.Run("UT-040 adds item with evidence from source inspection", func(t *testing.T) {
		db := inventoryTestDB(t)
		tenant, version, env, actor := seedReadyDraft(t, db)
		inspectionID, responsibilityID, mediaID := identity.NewID(), identity.NewID(), identity.NewID()
		if err := db.Model(&database.OriginVersion{}).Where("id=?", version).Update("source_inspection_id", inspectionID).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec("INSERT INTO inspections.inspections (id,tenant_id,status) VALUES (?,?,?)", inspectionID, tenant, "COMPLETED").Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec("INSERT INTO inspections.responsibilities (id,tenant_id,inspection_id) VALUES (?,?,?)", responsibilityID, tenant, inspectionID).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec("INSERT INTO media.media_objects (id,tenant_id,responsibility_id,status,media_kind) VALUES (?,?,?,?,?)", mediaID, tenant, responsibilityID, "READY", "PHOTO").Error; err != nil {
			t.Fatal(err)
		}
		item, err := testService(db).AddItem(context.Background(), AddItemInput{TenantID: tenant, OriginVersionID: version, EnvironmentID: env, ActorMembershipID: actor, TypeCode: "OTHER", Name: "Novo", Evidence: []EvidenceInput{{Kind: "PHOTO", MediaID: mediaID}}})
		if err != nil {
			t.Fatal(err)
		}
		if item.Decision != "ADDED" || item.Origin != "MANUAL" || item.Declared {
			t.Fatalf("unexpected added item: %+v", item)
		}
		var rows int64
		db.Model(&database.InventoryItemEvidence{}).Where("tenant_id=? AND item_id=? AND media_id=?", tenant, item.ID, mediaID).Count(&rows)
		if rows != 1 {
			t.Fatalf("expected evidence row, got %d", rows)
		}
	})
	t.Run("UT-041 adds item without evidence as declared", func(t *testing.T) {
		db := inventoryTestDB(t)
		tenant, version, env, actor := seedReadyDraft(t, db)
		item, err := testService(db).AddItem(context.Background(), AddItemInput{TenantID: tenant, OriginVersionID: version, EnvironmentID: env, ActorMembershipID: actor, TypeCode: "OTHER", Name: "Declarado"})
		if err != nil {
			t.Fatal(err)
		}
		if !item.Declared || item.Decision != "ADDED" {
			t.Fatalf("unexpected declared item: %+v", item)
		}
	})
	t.Run("UT-043 rejects evidence from a different inspection", func(t *testing.T) {
		db := inventoryTestDB(t)
		tenant, version, env, actor := seedReadyDraft(t, db)
		inspectionID, otherInspectionID, responsibilityID, mediaID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
		if err := db.Model(&database.OriginVersion{}).Where("id=?", version).Update("source_inspection_id", inspectionID).Error; err != nil {
			t.Fatal(err)
		}
		for _, id := range []identity.ID{inspectionID, otherInspectionID} {
			if err := db.Exec("INSERT INTO inspections.inspections (id,tenant_id,status) VALUES (?,?,?)", id, tenant, "COMPLETED").Error; err != nil {
				t.Fatal(err)
			}
		}
		if err := db.Exec("INSERT INTO inspections.responsibilities (id,tenant_id,inspection_id) VALUES (?,?,?)", responsibilityID, tenant, otherInspectionID).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec("INSERT INTO media.media_objects (id,tenant_id,responsibility_id,status,media_kind) VALUES (?,?,?,?,?)", mediaID, tenant, responsibilityID, "READY", "PHOTO").Error; err != nil {
			t.Fatal(err)
		}
		_, err := testService(db).AddItem(context.Background(), AddItemInput{TenantID: tenant, OriginVersionID: version, EnvironmentID: env, ActorMembershipID: actor, TypeCode: "OTHER", Name: "Novo", Evidence: []EvidenceInput{{Kind: "PHOTO", MediaID: mediaID}}})
		_, field, _ := apperror.Public(err)
		if !apperror.Is(err, apperror.InvalidInput) || field != "evidence" {
			t.Fatalf("unexpected foreign evidence result: %v", err)
		}
	})
}

func TestService_AssignedComparativeDraftCases(t *testing.T) {
	t.Run("UT-013 copies comparative inventory and adds suggested comparison findings", func(t *testing.T) {
		db := inventoryTestDB(t)
		tenant, assetID, inspectionID, sourceVersionID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
		originID, envID, envIdentity, itemID, itemIdentity := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
		if err := db.Create(&database.Origin{ID: originID, TenantID: tenant, AssetID: assetID, Version: 1}).Error; err != nil {
			t.Fatal(err)
		}
		state := "READY"
		if err := db.Create(&database.OriginVersion{ID: sourceVersionID, TenantID: tenant, OriginID: originID, VersionNumber: 1, Source: "CAPTURE", HasInventory: true, DraftState: &state, Status: "ACTIVE", Purpose: stringPointer("MOVE_IN"), IdempotencyKey: "compare-source"}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&database.InventoryEnvironment{ID: envID, TenantID: tenant, OriginVersionID: sourceVersionID, IdentityID: envIdentity, TypeCode: "KITCHEN", Name: "Cozinha", Decision: "ACCEPTED", Position: 1, Version: 1}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&database.InventoryItem{ID: itemID, TenantID: tenant, OriginVersionID: sourceVersionID, EnvironmentID: envID, IdentityID: itemIdentity, TypeCode: "SINK", Name: "Pia", Condition: "GOOD", Observation: "OBSERVED", Origin: "AI", Decision: "ACCEPTED", Version: 2}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec("INSERT INTO inspections.inspections (id,tenant_id,status) VALUES (?,?,?)", inspectionID, tenant, "COMPLETED").Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec("UPDATE inspections.inspections SET asset_id=? WHERE id=?", assetID, inspectionID).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec("INSERT INTO inspections.reference_snapshots (id,tenant_id,inspection_id,reference_version_id) VALUES (?,?,?,?)", identity.NewID(), tenant, inspectionID, sourceVersionID).Error; err != nil {
			t.Fatal(err)
		}
		jobID, outsideJobID := identity.NewID(), identity.NewID()
		for _, job := range []struct{ id, env identity.ID }{{jobID, envIdentity}, {outsideJobID, envIdentity}} {
			if err := db.Exec("INSERT INTO analysis.inventory_jobs (id,tenant_id,inspection_id,origin_version_id,kind,status,requirement_key,environment_identity_id) VALUES (?,?,?,?,?,?,?,?)", job.id, tenant, inspectionID, sourceVersionID, "COMPARE", "SUCCEEDED", job.id.String(), job.env).Error; err != nil {
				t.Fatal(err)
			}
		}
		conditionAfter := "FAIR"
		if err := db.Exec("INSERT INTO analysis.inventory_item_results (id,tenant_id,job_id,inspection_id,item_identity_id,status,condition_after) VALUES (?,?,?,?,?,?,?)", identity.NewID(), tenant, jobID, inspectionID, itemIdentity, "CONDITION_CHANGED", conditionAfter).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec("INSERT INTO analysis.inventory_item_results (id,tenant_id,job_id,inspection_id,status,outside_type_code,outside_name) VALUES (?,?,?,?,?,?,?)", identity.NewID(), tenant, outsideJobID, inspectionID, "OUTSIDE_INVENTORY", "CHAIR", "Cadeira nova").Error; err != nil {
			t.Fatal(err)
		}
		draft, err := testService(db).StartFromInspection(context.Background(), tenant, inspectionID, identity.NewID())
		if err != nil {
			t.Fatal(err)
		}
		var items []database.InventoryItem
		if err := db.Where("tenant_id=? AND origin_version_id=?", tenant, draft.ID).Order("name").Find(&items).Error; err != nil {
			t.Fatal(err)
		}
		if len(items) != 2 {
			t.Fatalf("wanted copied changed item and outside item, got %+v", items)
		}
		var changed, outside *database.InventoryItem
		for i := range items {
			if items[i].Name == "Pia" {
				changed = &items[i]
			}
			if items[i].Name == "Cadeira nova" {
				outside = &items[i]
			}
		}
		if changed == nil || changed.Decision != "PENDING" || !strings.Contains(string(changed.Suggested), `"condition":"FAIR"`) {
			t.Fatalf("changed item suggestion missing: %+v", changed)
		}
		if outside == nil || outside.Decision != "PENDING" || outside.Origin != "AI" || outside.EnvironmentID == (identity.ID{}) {
			t.Fatalf("outside inventory item missing: %+v", outside)
		}
	})
	t.Run("UT-014 comparison draft stays pinned and warns when newer baseline is active", func(t *testing.T) {
		db := inventoryTestDB(t)
		tenant, assetID, inspectionID := identity.NewID(), identity.NewID(), identity.NewID()
		originID, pinnedID, newerID := identity.NewID(), identity.NewID(), identity.NewID()
		if err := db.Create(&database.Origin{ID: originID, TenantID: tenant, AssetID: assetID, ActiveVersionID: &newerID, Version: 3}).Error; err != nil {
			t.Fatal(err)
		}
		state := "READY"
		for _, row := range []database.OriginVersion{{ID: pinnedID, TenantID: tenant, OriginID: originID, VersionNumber: 2, Source: "CAPTURE", HasInventory: true, DraftState: &state, Status: "SUPERSEDED", Purpose: stringPointer("MOVE_IN"), IdempotencyKey: "pinned"}, {ID: newerID, TenantID: tenant, OriginID: originID, VersionNumber: 3, Source: "CAPTURE", HasInventory: true, DraftState: &state, Status: "ACTIVE", Purpose: stringPointer("MOVE_IN"), IdempotencyKey: "newer"}} {
			if err := db.Create(&row).Error; err != nil {
				t.Fatal(err)
			}
		}
		if err := db.Exec("INSERT INTO inspections.inspections (id,tenant_id,status,asset_id) VALUES (?,?,?,?)", inspectionID, tenant, "COMPLETED", assetID).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Exec("INSERT INTO inspections.reference_snapshots (id,tenant_id,inspection_id,reference_version_id) VALUES (?,?,?,?)", identity.NewID(), tenant, inspectionID, pinnedID).Error; err != nil {
			t.Fatal(err)
		}
		draft, err := testService(db).StartFromInspection(context.Background(), tenant, inspectionID, identity.NewID())
		if err != nil {
			t.Fatal(err)
		}
		if draft.SourceVersionID == nil || *draft.SourceVersionID != pinnedID {
			t.Fatalf("draft was not pinned to inspected version: %+v", draft)
		}
		view, err := testService(db).InventoryDraft(context.Background(), tenant, draft.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(view.Warnings) != 1 || view.Warnings[0] != "NEWER_VERSION_EXISTS" {
			t.Fatalf("newer baseline warning missing: %+v", view.Warnings)
		}
	})
}
