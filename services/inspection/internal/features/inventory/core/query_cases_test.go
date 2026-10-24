package core

import (
	"context"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"
)

func TestService_UT083AndUT084DraftOrderingAndProgress(t *testing.T) {
	db := inventoryTestDB(t)
	tenant, version, seedEnv, _ := seedReadyDraft(t, db)
	for envIndex := 0; envIndex < 20; envIndex++ {
		env := database.InventoryEnvironment{ID: seedEnv, TenantID: tenant, OriginVersionID: version, IdentityID: identity.NewID(), TypeCode: "OTHER", Name: "Environment", Decision: "ACCEPTED", Position: envIndex, Version: 1}
		if envIndex > 0 {
			env.ID = identity.NewID()
			if err := db.Create(&env).Error; err != nil {
				t.Fatal(err)
			}
		}
		for itemIndex := 0; itemIndex < 15; itemIndex++ {
			decision := "ACCEPTED"
			if envIndex == 0 && itemIndex < 3 {
				decision = "PENDING"
			}
			confidence := 1000 + (14-itemIndex)*100
			item := database.InventoryItem{ID: identity.NewID(), TenantID: tenant, OriginVersionID: version, EnvironmentID: env.ID, IdentityID: identity.NewID(), TypeCode: "OTHER", Name: "Item", Condition: "GOOD", Observation: "OBSERVED", Origin: "AI", Decision: decision, ConfidenceBps: &confidence, Version: 1, CreatedAt: time.Date(2026, 1, 1, 0, 0, itemIndex, 0, time.UTC)}
			if err := db.Create(&item).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	view, err := testService(db).InventoryDraft(context.Background(), tenant, version)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Environments) != 20 {
		t.Fatalf("expected all 20 populated environments: %d", len(view.Environments))
	}
	if view.Decided != 297 || view.Pending != 3 {
		t.Fatalf("unexpected progress: decided=%d pending=%d", view.Decided, view.Pending)
	}
	for _, env := range view.Environments {
		if len(env.Items) == 0 {
			continue
		}
		if len(env.Items) != 15 {
			t.Fatalf("environment did not retain its group: %d items", len(env.Items))
		}
		for i := 1; i < len(env.Items); i++ {
			if *env.Items[i-1].Item.ConfidenceBps > *env.Items[i].Item.ConfidenceBps {
				t.Fatalf("items not ordered by ascending confidence: %+v", env.Items)
			}
		}
	}
}

func TestService_UT086InventoryTrailHasSuggestionsDecisionsAndCounterparty(t *testing.T) {
	db := inventoryTestDB(t)
	tenant, version, env, actor := seedReadyDraft(t, db)
	item := database.InventoryItem{ID: identity.NewID(), TenantID: tenant, OriginVersionID: version, EnvironmentID: env, IdentityID: identity.NewID(), TypeCode: "OTHER", Name: "Suggestion", Condition: "GOOD", Observation: "OBSERVED", Origin: "AI", Decision: "PENDING", Suggested: []byte(`{"condition":"FAIR"}`), Version: 1}
	if err := db.Create(&item).Error; err != nil {
		t.Fatal(err)
	}
	mediaID := identity.NewID()
	if err := db.Create(&database.InventoryItemEvidence{ID: identity.NewID(), TenantID: tenant, ItemID: item.ID, Kind: "PHOTO", MediaID: mediaID, Position: 0}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := testService(db).Decide(context.Background(), DecideInput{TenantID: tenant, OriginVersionID: version, ActorMembershipID: actor, Decisions: []Decision{{ItemID: item.ID, Action: "REJECT", ExpectedVersion: 1, Reason: "DUPLICATE"}}}); err != nil {
		t.Fatal(err)
	}
	accepted := database.InventoryItem{ID: identity.NewID(), TenantID: tenant, OriginVersionID: version, EnvironmentID: env, IdentityID: identity.NewID(), TypeCode: "OTHER", Name: "Included", Condition: "GOOD", Observation: "OBSERVED", Origin: "MANUAL", Declared: true, Decision: "ADDED", Version: 1}
	if err := db.Create(&accepted).Error; err != nil {
		t.Fatal(err)
	}
	participant := identity.NewID()
	if err := db.Exec("INSERT INTO origins.counterparty_invitations (id,tenant_id,origin_version_id,participant_id,token_hash,channels,deadline_at,response,resolution) VALUES (?,?,?,?,?,?,?,?,?)", identity.NewID(), tenant, version, participant, []byte("hash"), []byte(`[]`), time.Now().UTC().Add(time.Hour), "ACKNOWLEDGED", "OPEN").Error; err != nil {
		t.Fatal(err)
	}
	seedActivationAsset(t, db, tenant, version)
	if _, err := testService(db).Activate(context.Background(), ActivateInput{TenantID: tenant, OriginVersionID: version, ActorMembershipID: actor, Purpose: "MOVE_IN", ExpectedOriginVersion: 1}); err != nil {
		t.Fatal(err)
	}
	view, err := testService(db).InventoryTrail(context.Background(), tenant, version)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Suggestions) != 1 || len(view.Decisions) != 2 || len(view.CounterpartyEvents) != 1 || len(view.SuggestionEvidence[item.ID]) != 1 || view.SuggestionEvidence[item.ID][0] != mediaID {
		t.Fatalf("trail incomplete: %+v", view)
	}
	var rejected, activated bool
	for _, row := range view.Decisions {
		if row.Action == "REJECT" {
			rejected = row.Reason != nil && *row.Reason == "DUPLICATE" && row.ActorMembershipID != nil && *row.ActorMembershipID == actor && !row.OccurredAt.IsZero()
		}
		if row.Action == "ACTIVATE" {
			activated = true
		}
	}
	if !rejected || !activated {
		t.Fatalf("trail missing rejection attribution or activation event: %+v", view.Decisions)
	}
}

func TestService_UT087EvidenceAvailabilityLabels(t *testing.T) {
	db := inventoryTestDB(t)
	tenant := identity.NewID()
	purgedMedia, expiredSource, frameID := identity.NewID(), identity.NewID(), identity.NewID()
	if err := db.Exec("INSERT INTO media.media_objects (id,tenant_id,responsibility_id,status,media_kind) VALUES (?,?,?,?,?)", purgedMedia, tenant, identity.NewID(), "PURGED", "PHOTO").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO media.video_sources (id,tenant_id,media_id,status) VALUES (?,?,?,?)", identity.NewID(), tenant, expiredSource, "EXPIRED").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO media.video_frames (id,tenant_id,source_media_id,preparation_id,pts_us,sha256,object_key,width,height) VALUES (?,?,?,?,?,?,?,?,?)", frameID, tenant, expiredSource, identity.NewID(), 100, "sha", "frame", 10, 10).Error; err != nil {
		t.Fatal(err)
	}
	photoEvidenceID, frameEvidenceID := identity.NewID(), identity.NewID()
	frameKind := "VIDEO_FRAME"
	evidence := []database.InventoryItemEvidence{{ID: photoEvidenceID, TenantID: tenant, Kind: "PHOTO", MediaID: purgedMedia}, {ID: frameEvidenceID, TenantID: tenant, Kind: frameKind, MediaID: expiredSource, FrameID: &frameID}}
	availability, err := LoadEvidenceAvailability(db, tenant, evidence)
	if err != nil {
		t.Fatal(err)
	}
	if availability[photoEvidenceID] != "UNAVAILABLE" || availability[frameEvidenceID] != "FRAME_ONLY" {
		t.Fatalf("unexpected availability: %+v", availability)
	}
}

func TestService_UT089DecisionsSurviveSubsequentRead(t *testing.T) {
	db := inventoryTestDB(t)
	tenant, version, env, actor := seedReadyDraft(t, db)
	item := database.InventoryItem{ID: identity.NewID(), TenantID: tenant, OriginVersionID: version, EnvironmentID: env, IdentityID: identity.NewID(), TypeCode: "OTHER", Name: "Persistent", Condition: "GOOD", Observation: "OBSERVED", Origin: "AI", Decision: "PENDING", Version: 1}
	if err := db.Create(&item).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := testService(db).Decide(context.Background(), DecideInput{TenantID: tenant, OriginVersionID: version, ActorMembershipID: actor, Decisions: []Decision{{ItemID: item.ID, Action: "ACCEPT", ExpectedVersion: 1}}}); err != nil {
		t.Fatal(err)
	}
	var storedEnvCount int64
	db.Model(&database.InventoryEnvironment{}).Where("tenant_id=? AND origin_version_id=?", tenant, version).Count(&storedEnvCount)
	newSessionService := testService(db)
	view, err := newSessionService.InventoryDraft(context.Background(), tenant, version)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Environments) != 1 || len(view.Environments[0].Items) != 1 || view.Environments[0].Items[0].Item.Decision != "ACCEPTED" || view.Environments[0].Items[0].Item.Version != 2 {
		t.Fatalf("decision did not persist to next read (db environments=%d): %+v", storedEnvCount, view)
	}
}
