package core

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

// voiceItem stores a pending voice-declared item with one VOICE source per
// attribute, all citing the same note revision.
func voiceItem(t *testing.T, db *gorm.DB, tenant, version, env identity.ID, name string, attributes ...string) (database.InventoryItem, identity.ID, identity.ID) {
	t.Helper()
	item := database.InventoryItem{ID: identity.NewID(), TenantID: tenant, OriginVersionID: version, EnvironmentID: env, IdentityID: identity.NewID(), TypeCode: "OTHER", Name: name, Quantity: testPtr(5), Condition: "GOOD", Observation: "OBSERVED", Origin: "AI", Declared: true, Decision: "PENDING", ConfidenceBps: testPtr(9500), Version: 1}
	if err := db.Create(&item).Error; err != nil {
		t.Fatal(err)
	}
	note, revision := identity.NewID(), identity.NewID()
	for _, attribute := range attributes {
		n, r := note, revision
		if err := db.Create(&database.InventoryItemAttributeSource{ID: identity.NewID(), TenantID: tenant, ItemID: item.ID, OriginVersionID: version, AttributeName: attribute, SourceKind: SourceVoice, NoteID: &n, RevisionID: &r, CreatedAt: time.Now().UTC()}).Error; err != nil {
			t.Fatal(err)
		}
	}
	return item, note, revision
}

func undecidedVoice(t *testing.T, db *gorm.DB, tenant, item identity.ID) bool {
	t.Helper()
	pending, err := UndecidedVoiceItems(db, tenant, []identity.ID{item})
	if err != nil {
		t.Fatal(err)
	}
	return pending[item]
}

func sourcesOf(t *testing.T, db *gorm.DB, item identity.ID, kind string) map[string]int {
	t.Helper()
	var rows []database.InventoryItemAttributeSource
	if err := db.Where("item_id=? AND source_kind=?", item, kind).Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	out := map[string]int{}
	for _, row := range rows {
		out[row.AttributeName]++
	}
	return out
}

func TestDecideRecordsHumanReviewWithoutErasingTheSpokenSource(t *testing.T) {
	t.Run("IT-195 IT-196 explicit acceptance reviews every voice attribute and keeps the voice source", func(t *testing.T) {
		db := inventoryTestDB(t)
		tenant, version, env, actor := seedReadyDraft(t, db)
		item, _, _ := voiceItem(t, db, tenant, version, env, "Pratos", "name", "quantity")
		service := testService(db)
		if pending := undecidedVoice(t, db, tenant, item.ID); !pending {
			t.Fatal("before the decision the item must await review")
		}
		if _, err := service.Decide(context.Background(), DecideInput{TenantID: tenant, OriginVersionID: version, ActorMembershipID: actor, Decisions: []Decision{{ItemID: item.ID, Action: "ACCEPT", ExpectedVersion: 1}}}); err != nil {
			t.Fatal(err)
		}
		if voice := sourcesOf(t, db, item.ID, SourceVoice); voice["name"] != 1 || voice["quantity"] != 1 {
			t.Fatalf("voice sources were erased: %v", voice)
		}
		if review := sourcesOf(t, db, item.ID, SourceHumanReview); review["name"] != 1 || review["quantity"] != 1 {
			t.Fatalf("human review sources = %v, want name and quantity", review)
		}
		if undecidedVoice(t, db, tenant, item.ID) {
			t.Fatal("after the decision nothing may await review")
		}
		var reviews []database.InventoryItemAttributeSource
		if err := db.Where("item_id=? AND source_kind='HUMAN_REVIEW'", item.ID).Find(&reviews).Error; err != nil {
			t.Fatal(err)
		}
		for _, review := range reviews {
			if review.ReviewedBy == nil || *review.ReviewedBy != actor || review.NoteID != nil || review.RevisionID != nil {
				t.Fatalf("review shape = %+v", review)
			}
		}
	})
	t.Run("IT-169 a correction records the review of the changed attribute", func(t *testing.T) {
		db := inventoryTestDB(t)
		tenant, version, env, actor := seedReadyDraft(t, db)
		item, _, _ := voiceItem(t, db, tenant, version, env, "Pratos", "quantity")
		six := 6
		if _, err := testService(db).Decide(context.Background(), DecideInput{TenantID: tenant, OriginVersionID: version, ActorMembershipID: actor, Decisions: []Decision{{ItemID: item.ID, Action: "EDIT", ExpectedVersion: 1, Patch: &ItemPatch{Quantity: &six, Color: testPtr("branco")}}}}); err != nil {
			t.Fatal(err)
		}
		review := sourcesOf(t, db, item.ID, SourceHumanReview)
		if review["quantity"] != 1 || review["color"] != 1 {
			t.Fatalf("review sources = %v, want quantity and the edited color", review)
		}
		if voice := sourcesOf(t, db, item.ID, SourceVoice); voice["quantity"] != 1 {
			t.Fatalf("the spoken source must stay: %v", voice)
		}
	})
	t.Run("IT-197 a rejection is reviewed and stays auditable", func(t *testing.T) {
		db := inventoryTestDB(t)
		tenant, version, env, actor := seedReadyDraft(t, db)
		item, _, _ := voiceItem(t, db, tenant, version, env, "Pratos", "name")
		if _, err := testService(db).Decide(context.Background(), DecideInput{TenantID: tenant, OriginVersionID: version, ActorMembershipID: actor, Decisions: []Decision{{ItemID: item.ID, Action: "REJECT", ExpectedVersion: 1, Reason: "NOT_EXISTS"}}}); err != nil {
			t.Fatal(err)
		}
		var stored database.InventoryItem
		if err := db.First(&stored, "id=?", item.ID).Error; err != nil || stored.Decision != "REJECTED" {
			t.Fatalf("item = %+v err=%v", stored, err)
		}
		if review := sourcesOf(t, db, item.ID, SourceHumanReview); review["name"] != 1 {
			t.Fatalf("rejection did not record the review: %v", review)
		}
		var audits int64
		if err := db.Model(&database.InventoryDecision{}).Where("target_id=? AND action='REJECT'", item.ID).Count(&audits).Error; err != nil || audits != 1 {
			t.Fatalf("rejection audit rows = %d err=%v", audits, err)
		}
	})
	t.Run("IT-199 an invalid decision keeps the item pending and unreviewed", func(t *testing.T) {
		db := inventoryTestDB(t)
		tenant, version, env, actor := seedReadyDraft(t, db)
		item, _, _ := voiceItem(t, db, tenant, version, env, "Pratos", "quantity")
		zero := 0
		_, err := testService(db).Decide(context.Background(), DecideInput{TenantID: tenant, OriginVersionID: version, ActorMembershipID: actor, Decisions: []Decision{{ItemID: item.ID, Action: "EDIT", ExpectedVersion: 1, Patch: &ItemPatch{Quantity: &zero}}}})
		if !apperror.Is(err, apperror.InvalidInput) {
			t.Fatalf("err = %v, want INVALID_INPUT", err)
		}
		if review := sourcesOf(t, db, item.ID, SourceHumanReview); len(review) != 0 {
			t.Fatalf("an invalid decision recorded a review: %v", review)
		}
		if !undecidedVoice(t, db, tenant, item.ID) {
			t.Fatal("the item must stay pending")
		}
	})
	t.Run("IT-205 a repeated acceptance does not repeat the review", func(t *testing.T) {
		db := inventoryTestDB(t)
		tenant, version, env, actor := seedReadyDraft(t, db)
		item, _, _ := voiceItem(t, db, tenant, version, env, "Pratos", "name")
		service := testService(db)
		for i := 0; i < 2; i++ {
			if _, err := service.Decide(context.Background(), DecideInput{TenantID: tenant, OriginVersionID: version, ActorMembershipID: actor, Decisions: []Decision{{ItemID: item.ID, Action: "ACCEPT", ExpectedVersion: 1 + i}}}); err != nil {
				t.Fatalf("attempt %d: %v", i, err)
			}
		}
		if review := sourcesOf(t, db, item.ID, SourceHumanReview); review["name"] != 1 {
			t.Fatalf("review rows = %v, want exactly one", review)
		}
	})
}

func TestUndecidedVoiceItemsReadsOnceAndCountsPendingVoiceOnly(t *testing.T) {
	db := inventoryTestDB(t)
	tenant, version, env, actor := seedReadyDraft(t, db)
	spoken, _, _ := voiceItem(t, db, tenant, version, env, "Pratos", "quantity")
	reviewed, _, _ := voiceItem(t, db, tenant, version, env, "Copos", "quantity")
	plain := database.InventoryItem{ID: identity.NewID(), TenantID: tenant, OriginVersionID: version, EnvironmentID: env, IdentityID: identity.NewID(), TypeCode: "OTHER", Name: "Mesa", Condition: "GOOD", Observation: "OBSERVED", Origin: "AI", Decision: "PENDING", Version: 1}
	if err := db.Create(&plain).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.InventoryItemAttributeSource{ID: identity.NewID(), TenantID: tenant, ItemID: reviewed.ID, OriginVersionID: version, AttributeName: "quantity", SourceKind: SourceHumanReview, ReviewedBy: &actor, CreatedAt: time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}
	pending, err := UndecidedVoiceItems(db, tenant, []identity.ID{spoken.ID, reviewed.ID, plain.ID})
	if err != nil || !pending[spoken.ID] || pending[reviewed.ID] || pending[plain.ID] || len(pending) != 1 {
		t.Fatalf("pending = %v err=%v, want only the unreviewed voice item", pending, err)
	}
	empty, err := UndecidedVoiceItems(db, tenant, nil)
	if err != nil || len(empty) != 0 {
		t.Fatalf("empty input = %v err=%v", empty, err)
	}
	// IT-210/IT-215: the count is separate and covers only unreviewed voice items.
	count, err := VoicePendingCount(db, tenant, version)
	if err != nil || count != 1 {
		t.Fatalf("voice pending count = %d err=%v, want 1", count, err)
	}
	// IT-222: counts stay scoped to their own tenant and version.
	if other, err := VoicePendingCount(db, identity.NewID(), version); err != nil || other != 0 {
		t.Fatalf("another tenant sees %d (err=%v)", other, err)
	}
}

func TestLoadAttributeSourcesGroupsByItemInOnePass(t *testing.T) {
	db := inventoryTestDB(t)
	tenant, version, env, _ := seedReadyDraft(t, db)
	a, _, _ := voiceItem(t, db, tenant, version, env, "Pratos", "quantity", "name")
	b, _, _ := voiceItem(t, db, tenant, version, env, "Copos", "color")
	grouped, err := LoadAttributeSources(db, tenant, []identity.ID{a.ID, b.ID})
	if err != nil || len(grouped[a.ID]) != 2 || len(grouped[b.ID]) != 1 {
		t.Fatalf("grouped = %v err=%v", grouped, err)
	}
	if grouped[a.ID][0].AttributeName != "name" || grouped[a.ID][1].AttributeName != "quantity" {
		t.Fatalf("sources are not ordered by attribute: %+v", grouped[a.ID])
	}
	if none, err := LoadAttributeSources(db, tenant, nil); err != nil || len(none) != 0 {
		t.Fatalf("empty = %v err=%v", none, err)
	}
}

func TestInventoryDraftViewCarriesSourcesAndVoicePendingCount(t *testing.T) {
	db := inventoryTestDB(t)
	tenant, version, env, _ := seedReadyDraft(t, db)
	item, note, revision := voiceItem(t, db, tenant, version, env, "Pratos", "quantity")
	view, err := testService(db).InventoryDraft(context.Background(), tenant, version)
	if err != nil {
		t.Fatal(err)
	}
	if view.Pending != 1 || view.VoicePending != 1 {
		t.Fatalf("pending=%d voicePending=%d, want 1 and 1", view.Pending, view.VoicePending)
	}
	got := view.Environments[0].Items[0]
	if got.Item.ID != item.ID || len(got.Sources) != 1 || *got.Sources[0].NoteID != note || *got.Sources[0].RevisionID != revision {
		t.Fatalf("view sources = %+v", got.Sources)
	}
	// Customer-safe views never carry provenance.
	asset := assetOf(t, db, version)
	seedAsset(t, db, tenant, asset)
	if err := db.Model(&database.OriginVersion{}).Where("id=?", version).Updates(map[string]any{"status": "ACTIVE"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&database.InventoryItem{}).Where("id=?", item.ID).Update("decision", "ACCEPTED").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&database.InventoryEnvironment{}).Where("id=?", env).Update("decision", "ACCEPTED").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&database.Origin{}).Where("asset_id=?", asset).Update("active_version_id", version).Error; err != nil {
		t.Fatal(err)
	}
	customer, err := testService(db).BaselineInventory(context.Background(), tenant, asset, nil, true)
	if err != nil || len(customer.Environments) != 1 || len(customer.Environments[0].Items) != 1 {
		t.Fatalf("customer baseline = %+v err=%v", customer, err)
	}
	if len(customer.Environments[0].Items[0].Sources) != 0 {
		t.Fatalf("customer-safe view carries provenance: %+v", customer.Environments[0].Items[0].Sources)
	}
	internal, err := testService(db).BaselineInventory(context.Background(), tenant, asset, nil, false)
	if err != nil || len(internal.Environments[0].Items[0].Sources) != 1 {
		t.Fatalf("internal baseline sources = %+v err=%v", internal, err)
	}
}

func seedAsset(t *testing.T, db *gorm.DB, tenant, asset identity.ID) {
	t.Helper()
	if err := db.Exec("INSERT INTO assets.assets (id, tenant_id, status) VALUES (?,?,'ACTIVE')", asset, tenant).Error; err != nil {
		t.Fatal(err)
	}
}

func assetOf(t *testing.T, db *gorm.DB, version identity.ID) identity.ID {
	t.Helper()
	var origin database.Origin
	if err := db.Joins("JOIN origins.origin_versions v ON v.origin_id=origins.id AND v.id=?", version).First(&origin).Error; err != nil {
		t.Fatal(err)
	}
	return origin.AssetID
}

func TestMergeKeepsEveryNoteCitationOnceAndRecordsTheReview(t *testing.T) {
	db := inventoryTestDB(t)
	tenant, version, env, actor := seedReadyDraft(t, db)
	first, noteA, revisionA := voiceItem(t, db, tenant, version, env, "Pratos", "quantity")
	second, noteB, revisionB := voiceItem(t, db, tenant, version, env, "Pratos fundos", "quantity")
	// The second item also cites the first item's note: it must not count twice (IT-177).
	if err := db.Create(&database.InventoryItemAttributeSource{ID: identity.NewID(), TenantID: tenant, ItemID: second.ID, OriginVersionID: version, AttributeName: "quantity", SourceKind: SourceVoice, NoteID: &noteA, RevisionID: &revisionA, CreatedAt: time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}
	merged, err := testService(db).MergeItems(context.Background(), MergeItemsInput{TenantID: tenant, OriginVersionID: version, ActorMembershipID: actor, ItemIDs: []identity.ID{first.ID, second.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if merged.ID != first.ID || merged.Decision != "EDITED" {
		t.Fatalf("merged = %+v", merged)
	}
	var rows []database.InventoryItemAttributeSource
	if err := db.Where("item_id=? AND source_kind='VOICE'", merged.ID).Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	cited := map[identity.ID]identity.ID{}
	for _, row := range rows {
		cited[*row.NoteID] = *row.RevisionID
	}
	if len(rows) != 2 || cited[noteA] != revisionA || cited[noteB] != revisionB {
		t.Fatalf("merged voice sources = %+v, want one per distinct note", rows)
	}
	if review := sourcesOf(t, db, merged.ID, SourceHumanReview); review["quantity"] != 1 {
		t.Fatalf("merge did not record the review: %v", review)
	}
	if kept := sourcesOf(t, db, second.ID, SourceVoice); kept["quantity"] != 2 {
		t.Fatalf("the merged-away item must keep its own citations: %v", kept)
	}
}

func TestActivationReportsVoicePendingApartFromNotReviewed(t *testing.T) {
	db := inventoryTestDB(t)
	tenant, version, env, actor := seedReadyDraft(t, db)
	voiceItem(t, db, tenant, version, env, "Pratos", "quantity")
	plain := database.InventoryItem{ID: identity.NewID(), TenantID: tenant, OriginVersionID: version, EnvironmentID: env, IdentityID: identity.NewID(), TypeCode: "OTHER", Name: "Mesa", Condition: "GOOD", Observation: "OBSERVED", Origin: "AI", Decision: "ACCEPTED", Version: 1}
	pendingVisual := database.InventoryItem{ID: identity.NewID(), TenantID: tenant, OriginVersionID: version, EnvironmentID: env, IdentityID: identity.NewID(), TypeCode: "OTHER", Name: "Cadeira", Condition: "GOOD", Observation: "OBSERVED", Origin: "AI", Decision: "PENDING", Version: 1}
	for _, item := range []database.InventoryItem{plain, pendingVisual} {
		if err := db.Create(&item).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Model(&database.InventoryEnvironment{}).Where("id=?", env).Update("decision", "ACCEPTED").Error; err != nil {
		t.Fatal(err)
	}
	seedAsset(t, db, tenant, assetOf(t, db, version))
	if _, err := testService(db).Activate(context.Background(), ActivateInput{TenantID: tenant, OriginVersionID: version, ActorMembershipID: actor, Purpose: "MOVE_IN", ExpectedOriginVersion: 1}); err != nil {
		t.Fatalf("activation with pending voice items must not be blocked (IT-209): %v", err)
	}
	var audit database.InventoryDecision
	if err := db.Where("origin_version_id=? AND action='ACTIVATE'", version).First(&audit).Error; err != nil {
		t.Fatal(err)
	}
	var counts ItemCounts
	if err := json.Unmarshal(audit.After, &counts); err != nil {
		t.Fatal(err)
	}
	if counts.Included != 1 || counts.NotReviewed != 2 || counts.VoicePending != 1 {
		t.Fatalf("counts = %+v, want included=1 notReviewed=2 voicePending=1", counts)
	}
	var stored database.InventoryItem
	if err := db.Where("origin_version_id=? AND name='Pratos'", version).First(&stored).Error; err != nil || stored.Decision != "PENDING" {
		t.Fatalf("the voice item must stay outside the active inventory: %+v err=%v", stored, err)
	}
}

func TestRegenerationRequestValidation(t *testing.T) {
	valid := RegenerateInput{TenantID: identity.NewID(), OriginVersionID: identity.NewID(), EnvironmentID: identity.NewID(), ExpectedVersion: 1, RevisionIDs: []identity.ID{identity.NewID()}, ClientMutationID: "m1"}
	if revisions, err := validateRegeneration(valid); err != nil || len(revisions) != 1 {
		t.Fatalf("valid request = %v, %v", revisions, err)
	}
	tooMany := make([]identity.ID, MaxRegenerationRevisions+1)
	for i := range tooMany {
		tooMany[i] = identity.NewID()
	}
	duplicate := identity.NewID()
	tests := map[string]func(RegenerateInput) RegenerateInput{
		"IT-369 no tenant":        func(in RegenerateInput) RegenerateInput { in.TenantID = identity.ID{}; return in },
		"IT-369 no draft":         func(in RegenerateInput) RegenerateInput { in.OriginVersionID = identity.ID{}; return in },
		"IT-369 no room":          func(in RegenerateInput) RegenerateInput { in.EnvironmentID = identity.ID{}; return in },
		"IT-369 zero version":     func(in RegenerateInput) RegenerateInput { in.ExpectedVersion = 0; return in },
		"IT-369 blank mutation":   func(in RegenerateInput) RegenerateInput { in.ClientMutationID = "  "; return in },
		"IT-369 empty selection":  func(in RegenerateInput) RegenerateInput { in.RevisionIDs = nil; return in },
		"IT-369 selection bound":  func(in RegenerateInput) RegenerateInput { in.RevisionIDs = tooMany; return in },
		"IT-369 zero revision id": func(in RegenerateInput) RegenerateInput { in.RevisionIDs = []identity.ID{{}}; return in },
		"IT-369 repeated revision": func(in RegenerateInput) RegenerateInput {
			in.RevisionIDs = []identity.ID{duplicate, duplicate}
			return in
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := validateRegeneration(mutate(valid))
			if !apperror.Is(err, apperror.InvalidInput) {
				t.Fatalf("err = %v, want INVALID_INPUT", err)
			}
			if _, _, message := apperror.Public(err); message == "" {
				t.Fatal("the failure has no safe message")
			}
		})
	}
	// An invalid request never reaches the database.
	service := Service{Within: func(context.Context, identity.ID, func(*gorm.DB) error) error {
		t.Fatal("invalid input reached persistence")
		return nil
	}}
	if _, err := service.RegenerateSuggestions(context.Background(), RegenerateInput{}); !apperror.Is(err, apperror.InvalidInput) {
		t.Fatalf("err = %v", err)
	}
}

func TestMergeRetryAndConcurrentMergeProduceOneCombinedItem(t *testing.T) {
	db := inventoryTestDB(t)
	tenant, version, env, actor := seedReadyDraft(t, db)
	first, _, _ := voiceItem(t, db, tenant, version, env, "Pratos", "quantity")
	second, _, _ := voiceItem(t, db, tenant, version, env, "Pratos fundos", "quantity")
	service := testService(db)
	input := MergeItemsInput{TenantID: tenant, OriginVersionID: version, ActorMembershipID: actor, ItemIDs: []identity.ID{first.ID, second.ID}}
	if _, err := service.MergeItems(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	// IT-189 IT-191: the retry (or the second reviewer) finds the candidate already merged away.
	if _, err := service.MergeItems(context.Background(), input); !apperror.Is(err, apperror.InvalidState) {
		t.Fatalf("second merge err = %v, want INVALID_STATE", err)
	}
	var included int64
	if err := db.Model(&database.InventoryItem{}).Where("origin_version_id=? AND decision<>'REJECTED' AND merged_into_id IS NULL", version).Count(&included).Error; err != nil || included != 1 {
		t.Fatalf("combined items = %d err=%v, want exactly one", included, err)
	}
	var merged database.InventoryItem
	if err := db.First(&merged, "id=?", second.ID).Error; err != nil || merged.Decision != "REJECTED" || merged.MergedIntoID == nil || *merged.MergedIntoID != first.ID {
		t.Fatalf("the merged-away item must stay auditable: %+v err=%v", merged, err)
	}
}
