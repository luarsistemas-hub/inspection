//go:build integration

package resolvers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/inventory/activate_version"
	"inspection/services/inspection/internal/features/inventory/add_item"
	"inspection/services/inspection/internal/features/inventory/baseline"
	"inspection/services/inspection/internal/features/inventory/bulk_accept_environment"
	"inspection/services/inspection/internal/features/inventory/core"
	"inspection/services/inspection/internal/features/inventory/decide_items"
	"inspection/services/inspection/internal/features/inventory/discard_draft"
	"inspection/services/inspection/internal/features/inventory/draft"
	"inspection/services/inspection/internal/features/inventory/merge_items"
	"inspection/services/inspection/internal/features/inventory/remove_environment"
	"inspection/services/inspection/internal/features/inventory/request_cataloging"
	"inspection/services/inspection/internal/features/inventory/split_item"
	"inspection/services/inspection/internal/features/inventory/trail"
	"inspection/services/inspection/internal/features/inventory/upsert_environment"
	promoteorigin "inspection/services/inspection/internal/features/origins/promote_inspection_to_origin"
	retentionexpire "inspection/services/inspection/internal/features/retention/expire_video_sources"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/objectstore"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/gorm"
)

// These tests use the same disposable-Postgres harness and GraphQL fixture as
// the origin matrix. They are intentionally not runnable without the explicit
// isolated database and runtime-role URLs.
func TestInventoryIT014EmployeeMutationsAppendEveryDecision(t *testing.T) {
	fx := newInventoryITFixture(t, auth.Employee)
	seed := seedInventoryDraft(t, fx, false)
	ctx := fx.ctx
	mutations := 0
	call := func(query string) string {
		t.Helper()
		mutations++
		response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, query, ctx, "", "")
		if strings.Contains(response, `"errors"`) || strings.Contains(response, `"userErrors":[{"code":`) {
			t.Fatalf("inventory mutation %d failed (%s): %s", mutations, query, response)
		}
		return response
	}
	itemA := seed.items[0]
	call(fmt.Sprintf(`mutation { decideInventoryItems(input:{originVersionId:"%s",decisions:[{itemId:"%s",action:"ACCEPT",expectedVersion:1}],clientMutationId:"it014-decide"}) { userErrors { code field } } }`, seed.version, itemA))
	call(fmt.Sprintf(`mutation { bulkAcceptInventoryEnvironment(input:{originVersionId:"%s",environmentId:"%s",minimumConfidenceBps:8500,clientMutationId:"it014-bulk"}) { userErrors { code field } } }`, seed.version, seed.environments[0]))
	added1 := addInventoryITItem(t, fx, seed, "Mesa auxiliar")
	added2 := addInventoryITItem(t, fx, seed, "Mesa de apoio")
	call(fmt.Sprintf(`mutation { mergeInventoryItems(input:{originVersionId:"%s",itemIds:["%s","%s"],clientMutationId:"it014-merge"}) { userErrors { code field } } }`, seed.version, added1, added2))
	splitTarget := addInventoryITItem(t, fx, seed, "Sofá")
	call(fmt.Sprintf(`mutation { splitInventoryItem(input:{originVersionId:"%s",itemId:"%s",expectedVersion:1,parts:[{name:"Sofá A",typeCode:"SOFA",evidenceIds:[]},{name:"Sofá B",typeCode:"SOFA",evidenceIds:[]}],clientMutationId:"it014-split"}) { userErrors { code field } } }`, seed.version, splitTarget))
	call(fmt.Sprintf(`mutation { upsertInventoryEnvironment(input:{originVersionId:"%s",environmentId:"%s",typeCode:"LIVING_ROOM",name:"Sala revisada",expected:true,clientMutationId:"it014-upsert"}) { userErrors { code field } } }`, seed.version, seed.environments[0]))
	newEnvironment := addInventoryITEnvironment(t, fx, seed)
	call(fmt.Sprintf(`mutation { removeInventoryEnvironment(input:{originVersionId:"%s",environmentId:"%s",confirmItemExclusion:true,clientMutationId:"it014-remove"}) { userErrors { code field } } }`, seed.version, newEnvironment))
	var rows int64
	if err := fx.h.AdminDB.Model(&database.InventoryDecision{}).Where("tenant_id=? AND origin_version_id=?", fx.seed.TenantID, seed.version).Count(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if rows != int64(mutations+4) {
		t.Fatalf("inventory_decisions rows=%d, want one per mutation (%d)", rows, mutations+4)
	}
}

func TestInventoryIT015ConcurrentItemDecisionUsesExpectedVersion(t *testing.T) {
	fx := newInventoryITFixture(t, auth.Employee)
	seed := seedInventoryDraft(t, fx, false)
	query := fmt.Sprintf(`mutation { decideInventoryItems(input:{originVersionId:"%s",decisions:[{itemId:"%s",action:"ACCEPT",expectedVersion:1}],clientMutationId:"it015-%s"}) { items { id } userErrors { code field } } }`, seed.version, seed.items[0], "%s")
	responses := make([]string, 2)
	var wg sync.WaitGroup
	for i := range responses {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			responses[i] = executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, fmt.Sprintf(query, i), fx.ctx, "", "")
		}(i)
	}
	wg.Wait()
	success, conflict := 0, 0
	for _, response := range responses {
		if strings.Contains(response, `"items":[{"id":`) && strings.Contains(response, `"userErrors":[]`) {
			success++
		}
		if strings.Contains(response, `"field":"expectedVersion"`) {
			conflict++
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("concurrent decisions success=%d expectedVersion conflicts=%d responses=%v", success, conflict, responses)
	}
}

func TestInventoryIT016ActivationSupersedesButKeepsPinnedReportReference(t *testing.T) {
	fx := newInventoryITFixture(t, auth.TenantAdmin)
	seed := seedInventoryDraft(t, fx, true)
	if err := fx.h.WithinTenant(fx.ctx, fx.seed.TenantID, func(tx *gorm.DB) error {
		return tx.Create(&database.ReportSnapshot{ID: identity.NewID(), TenantID: fx.seed.TenantID, InspectionID: fx.seed.InspectionID, Mode: "CONSOLIDATED", Classification: "NORMAL", JSONDigest: strings.Repeat("a", 64), HTMLDigest: strings.Repeat("b", 64), SchemaVersion: 1, VersionNumber: 1, CanonicalJSON: json.RawMessage(fmt.Sprintf(`{"referenceVersionId":%q}`, seed.previous.String())), HTML: json.RawMessage(`{}`), CreatedAt: time.Now().UTC()}).Error
	}); err != nil {
		t.Fatal(err)
	}
	readRevision := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, fmt.Sprintf(`query { inventoryDraft(originVersionId:"%s") { version { originRevision } } }`, seed.version), fx.ctx, "", "")
	if !strings.Contains(readRevision, `"originRevision":4`) {
		t.Fatalf("could not read current origin revision: %s", readRevision)
	}
	response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, fmt.Sprintf(`mutation { activateBaselineVersion(input:{originVersionId:"%s",purpose:"MOVE_IN",expectedOriginVersion:4,counterpartyParticipantIds:[],clientMutationId:"it016"}) { version { status } userErrors { code field } } }`, seed.version), fx.ctx, "", "")
	if strings.Contains(response, `"userErrors":[{"code":`) || !strings.Contains(response, `"status":"ACTIVE"`) {
		t.Fatalf("activation failed: %s", response)
	}
	assertInventoryVersionStatus(t, fx, seed.previous, "SUPERSEDED")
	var snapshot database.ReportSnapshot
	if err := fx.h.AdminDB.Where("tenant_id=? AND inspection_id=?", fx.seed.TenantID, fx.seed.InspectionID).Take(&snapshot).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(snapshot.CanonicalJSON), seed.previous.String()) {
		t.Fatalf("report snapshot no longer references previous version %s: %s", seed.previous, snapshot.CanonicalJSON)
	}
}

func TestInventoryIT017ConcurrentActivationChecksOriginVersion(t *testing.T) {
	fx := newInventoryITFixture(t, auth.TenantAdmin)
	seed := seedInventoryDraft(t, fx, true)
	query := fmt.Sprintf(`mutation { activateBaselineVersion(input:{originVersionId:"%s",purpose:"%%s",expectedOriginVersion:4,counterpartyParticipantIds:[],clientMutationId:"it017-%%s"}) { version { status } userErrors { code field } } }`, seed.version)
	responses := make([]string, 2)
	var wg sync.WaitGroup
	for i, purpose := range []string{"MOVE_IN", "POST_RENOVATION"} {
		wg.Add(1)
		go func(i int, purpose string) {
			defer wg.Done()
			responses[i] = executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, fmt.Sprintf(query, purpose, purpose), fx.ctx, "", "")
		}(i, purpose)
	}
	wg.Wait()
	active, stale := 0, 0
	for _, response := range responses {
		if strings.Contains(response, `"status":"ACTIVE"`) && strings.Contains(response, `"userErrors":[]`) {
			active++
		}
		if strings.Contains(response, `"field":"expectedOriginVersion"`) {
			stale++
		}
	}
	if active != 1 || stale != 1 {
		t.Fatalf("concurrent activation active=%d expectedOriginVersion conflicts=%d responses=%v", active, stale, responses)
	}
}

func TestInventoryIT018CustomerProjectionAndForeignVersionNotFound(t *testing.T) {
	fx := newInventoryITFixture(t, auth.TenantAdmin)
	seed := seedInventoryDraft(t, fx, true)
	for _, role := range []string{auth.Viewer, auth.CustomerViewer} {
		response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, fmt.Sprintf(`query { baselineInventory(assetId:"%s",versionId:"%s") { hasInventory } }`, fx.seed.AssetID, seed.version), inventoryITContext(fx, role), "", "")
		if !strings.Contains(response, `"code":"NOT_FOUND"`) {
			t.Fatalf("%s baselineInventory must not expose a draft: %s", role, response)
		}
	}
	activateInventoryDraft(t, fx, seed.version, 4, "it018-activate")
	customer := inventoryITContext(fx, auth.CustomerViewer)
	baseline := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, fmt.Sprintf(`query { baselineInventory(assetId:"%s") { hasInventory version { activatedBy source draftState supersedesId } environments { items { name decision suggested confidenceBps } } } }`, fx.seed.AssetID), customer, "", "")
	if strings.Contains(baseline, `"errors"`) || !strings.Contains(baseline, `"hasInventory":true`) || !strings.Contains(baseline, `"name":"Sofá 0"`) || strings.Contains(baseline, `"name":"Sofá 1"`) || !strings.Contains(baseline, `"decision":""`) || !strings.Contains(baseline, `"suggested":null`) || !strings.Contains(baseline, `"confidenceBps":null`) {
		t.Fatalf("customer baseline projection failed: %s", baseline)
	}
	if strings.Contains(baseline, `"activatedBy":"`) || strings.Contains(baseline, `"source":"`) || strings.Contains(baseline, `"draftState":"`) || strings.Contains(baseline, `"supersedesId":"`) {
		t.Fatalf("customer projection leaked internal version fields: %s", baseline)
	}
	draftResponse := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, fmt.Sprintf(`query { inventoryDraft(originVersionId:"%s") { version { id } } }`, seed.version), customer, "", "")
	if !strings.Contains(draftResponse, `"code":"FORBIDDEN"`) {
		t.Fatalf("customer inventoryDraft should be forbidden: %s", draftResponse)
	}
	foreign, err := fx.h.SeedTask06Fixture(context.Background(), harness.Task06FixtureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	foreignVersion := identity.NewID()
	if err := fx.h.WithinTenant(context.Background(), foreign.TenantID, func(tx *gorm.DB) error {
		now := time.Now().UTC()
		origin := database.Origin{ID: identity.NewID(), TenantID: foreign.TenantID, AssetID: foreign.AssetID, Version: 1, CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&origin).Error; err != nil {
			return err
		}
		state := "READY"
		return tx.Create(&database.OriginVersion{ID: foreignVersion, TenantID: foreign.TenantID, OriginID: origin.ID, VersionNumber: 1, Source: "CAPTURE", HasInventory: true, Status: "DRAFT", DraftState: &state, IdempotencyKey: "inventory-it-foreign-" + foreignVersion.String(), CreatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	queries := []string{
		fmt.Sprintf(`query { inventoryDraft(originVersionId:"%s") { version { id } } }`, foreignVersion),
		fmt.Sprintf(`query { baselineInventory(assetId:"%s",versionId:"%s") { hasInventory } }`, fx.seed.AssetID, foreignVersion),
		fmt.Sprintf(`query { inventoryTrail(originVersionId:"%s") { decisions { id } } }`, foreignVersion),
		fmt.Sprintf(`query { baselineInventory(assetId:"%s",versionId:"%s") { hasInventory } }`, foreign.AssetID, foreignVersion),
	}
	for _, query := range queries {
		response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, query, fx.ctx, "", "")
		if !strings.Contains(response, `"code":"NOT_FOUND"`) {
			t.Fatalf("foreign version query must return NOT_FOUND: %s", response)
		}
	}
}

func TestInventoryIT054CustomerPortfolioScopeRoleAndRealProgress(t *testing.T) {
	fx := newInventoryITFixture(t, auth.TenantAdmin)
	seed := seedInventoryDraft(t, fx, true)
	if err := fx.h.WithinTenant(fx.ctx, fx.seed.TenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.InventoryItem{}).Where("tenant_id=? AND id=?", fx.seed.TenantID, seed.items[0]).Update("decision", "ACCEPTED").Error
	}); err != nil {
		t.Fatal(err)
	}
	activateInventoryDraft(t, fx, seed.version, 4, "it054-activate")
	query := `query { customerPortfolio(first:10) { nodes { assetId assetName status progress } pageInfo { hasNextPage } } }`
	customer := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, query, inventoryITContext(fx, auth.CustomerViewer), "", "")
	if strings.Contains(customer, `"errors"`) || !strings.Contains(customer, `"assetName":`) {
		t.Fatalf("IT-054 customer portfolio query failed: %s", customer)
	}
	if !strings.Contains(customer, `"progress":50`) || !strings.Contains(customer, `"hasNextPage":false`) {
		t.Fatalf("IT-054 customer progress/pagination projection is incorrect: %s", customer)
	}
	viewer := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, query, inventoryITContext(fx, auth.Viewer), "", "")
	if !strings.Contains(viewer, `"code":"FORBIDDEN"`) {
		t.Fatalf("IT-054 VIEWER must not access the customer portfolio projection: %s", viewer)
	}
}

func TestInventoryITBaselineQueriesWithoutOriginAndCatalogingWithoutOrigin(t *testing.T) {
	fx := newInventoryITFixture(t, auth.TenantAdmin)
	versions := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, fmt.Sprintf(`query { baselineVersions(assetId:"%s",first:25) { nodes { id } } }`, fx.seed.AssetID), fx.ctx, "", "")
	if strings.Contains(versions, `"errors"`) || !strings.Contains(versions, `"nodes":[]`) {
		t.Fatalf("baselineVersions for asset without origin should be empty: %s", versions)
	}
	baseline := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, fmt.Sprintf(`query { baselineInventory(assetId:"%s") { hasInventory environments { id } } }`, fx.seed.AssetID), fx.ctx, "", "")
	if strings.Contains(baseline, `"errors"`) || !strings.Contains(baseline, `"hasInventory":false`) || !strings.Contains(baseline, `"environments":[]`) {
		t.Fatalf("baselineInventory for asset without origin should be empty: %s", baseline)
	}
	response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, fmt.Sprintf(`mutation { requestOriginCataloging(input:{assetId:"%s",clientMutationId:"it-no-origin"}) { userErrors { code field } } }`, fx.seed.AssetID), fx.ctx, "", "")
	if !strings.Contains(response, `"code":"INVALID_STATE"`) || !strings.Contains(response, `"field":"assetId"`) {
		t.Fatalf("cataloging without an active origin should return INVALID_STATE: %s", response)
	}
}

func TestInventoryITRequestOriginCatalogingFromLegacyOrigin(t *testing.T) {
	fx := newInventoryITFixture(t, auth.Employee)
	legacyVersionID := identity.NewID()
	if err := fx.h.WithinTenant(fx.ctx, fx.seed.TenantID, func(tx *gorm.DB) error {
		now := time.Now().UTC()
		origin := database.Origin{ID: identity.NewID(), TenantID: fx.seed.TenantID, AssetID: fx.seed.AssetID, Version: 1, ActiveVersionID: &legacyVersionID, CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&origin).Error; err != nil {
			return err
		}
		return tx.Create(&database.OriginVersion{ID: legacyVersionID, TenantID: fx.seed.TenantID, OriginID: origin.ID, VersionNumber: 1, Source: "CAPTURE", Status: "ACTIVE", IdempotencyKey: "inventory-it-legacy-origin-" + legacyVersionID.String(), CreatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	request := fmt.Sprintf(`mutation { requestOriginCataloging(input:{assetId:"%s",clientMutationId:"it-legacy-cataloging"}) { version { id status source draftState } userErrors { code field } } }`, fx.seed.AssetID)
	response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, request, fx.ctx, "", "")
	if strings.Contains(response, `"errors"`) || !strings.Contains(response, `"userErrors":[]`) || !strings.Contains(response, `"status":"DRAFT"`) || !strings.Contains(response, `"source":"LEGACY_ORIGIN"`) || !strings.Contains(response, `"draftState":"READY"`) {
		// Without origin evidence there is nothing to catalog: the empty draft is
		// reviewable at once instead of waiting for a job that never exists.
		t.Fatalf("legacy origin cataloging failed: %s", response)
	}
	repeated := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, request, fx.ctx, "", "")
	if !strings.Contains(repeated, `"code":"CONFLICT"`) || !strings.Contains(repeated, `"field":"originVersionId"`) {
		t.Fatalf("repeated legacy cataloging should conflict: %s", repeated)
	}
}

func TestInventoryITStartDraftFromCompletedInspection(t *testing.T) {
	for _, mode := range []string{"FIXED_ORIGIN", "CHECKLIST_ONLY"} {
		t.Run(mode, func(t *testing.T) {
			fx := newInventoryITFixtureWithOptions(t, auth.TenantAdmin, harness.Task06FixtureOptions{InspectionStatus: "COMPLETED", ComparisonMode: mode})
			if err := fx.h.WithinTenant(fx.ctx, fx.seed.TenantID, func(tx *gorm.DB) error {
				return tx.Model(&database.Inspection{}).Where("tenant_id=? AND id=?", fx.seed.TenantID, fx.seed.InspectionID).Update("status", "COMPLETED").Error
			}); err != nil {
				t.Fatal(err)
			}
			response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, fmt.Sprintf(`mutation { startBaselineDraftFromInspection(input:{inspectionId:"%s",clientMutationId:"it-start-from-inspection-%s"}) { version { id status source draftState } userErrors { code field } } }`, fx.seed.InspectionID, strings.ToLower(mode)), fx.ctx, "", "")
			if strings.Contains(response, `"errors"`) || !strings.Contains(response, `"userErrors":[]`) || !strings.Contains(response, `"source":"INSPECTION_PROMOTION"`) {
				t.Fatalf("startBaselineDraftFromInspection failed: %s", response)
			}
			var draft database.OriginVersion
			if err := fx.h.WithinTenant(fx.ctx, fx.seed.TenantID, func(tx *gorm.DB) error {
				return tx.Where("tenant_id=? AND source_inspection_id=? AND source='INSPECTION_PROMOTION' AND status='DRAFT'", fx.seed.TenantID, fx.seed.InspectionID).Take(&draft).Error
			}); err != nil {
				t.Fatal(err)
			}
			var copiedItems int64
			if err := fx.h.WithinTenant(fx.ctx, fx.seed.TenantID, func(tx *gorm.DB) error {
				return tx.Model(&database.InventoryItem{}).Where("tenant_id=? AND origin_version_id=?", fx.seed.TenantID, draft.ID).Count(&copiedItems).Error
			}); err != nil {
				t.Fatal(err)
			}
			if copiedItems != 0 {
				t.Fatalf("draft copied %d items from the reference/comparison", copiedItems)
			}
		})
	}
}

func TestInventoryITActivationRetainsLegacyFrameBankUsedByBaseline(t *testing.T) {
	fx := newInventoryITFixture(t, auth.TenantAdmin)
	seed := seedInventoryDraft(t, fx, true)
	preparationID, mediaID, frameID := identity.NewID(), identity.NewID(), identity.NewID()
	orphanPreparationID, orphanMediaID, orphanFrameID, orphanVersionID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	frameKey := "inventory-it/" + frameID.String() + ".jpg"
	orphanFrameKey := "inventory-it/" + orphanFrameID.String() + ".jpg"
	frameBytes := "retained-frame"
	if err := fx.h.Store.Client.Put(fx.ctx, fx.h.Store.Bucket, frameKey, strings.NewReader(frameBytes), int64(len(frameBytes)), "image/jpeg"); err != nil {
		t.Fatal(err)
	}
	if err := fx.h.Store.Client.Put(fx.ctx, fx.h.Store.Bucket, orphanFrameKey, strings.NewReader(frameBytes), int64(len(frameBytes)), "image/jpeg"); err != nil {
		t.Fatal(err)
	}
	if err := fx.h.WithinTenant(fx.ctx, fx.seed.TenantID, func(tx *gorm.DB) error {
		now := time.Now().UTC()
		if err := tx.Create(&database.MediaObject{ID: mediaID, TenantID: fx.seed.TenantID, ResponsibilityID: fx.seed.ResponsibilityID, ObjectKey: "inventory-it/source-" + mediaID.String(), ContentType: "video/mp4", SHA256: strings.Repeat("a", 64), SizeBytes: 6, Status: "READY", MediaKind: "VIDEO", IdempotencyKey: "inventory-it-source-" + mediaID.String(), CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPreparationJob{ID: preparationID, TenantID: fx.seed.TenantID, MediaID: mediaID, Status: "READY", Phase: "READY", PreparationVersion: 1, Generation: 1, NextAttemptAt: now, SourceSHA256: strings.Repeat("a", 64), ManifestDigest: strings.Repeat("a", 64), PendingObjectKeys: []byte(`[]`), CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.OriginVersion{}).Where("tenant_id=? AND id=?", fx.seed.TenantID, seed.previous).Updates(map[string]any{"media_kind": "VIDEO", "video_preparation_id": preparationID}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoFrame{ID: frameID, TenantID: fx.seed.TenantID, SourceMediaID: mediaID, PreparationID: preparationID, PTSMicros: 0, SHA256: strings.Repeat("b", 64), ObjectKey: frameKey, Width: 2, Height: 2, Role: "BASELINE", SizeBytes: int64(len(frameBytes)), CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.InventoryItemEvidence{ID: identity.NewID(), TenantID: fx.seed.TenantID, ItemID: seed.items[0], Kind: "VIDEO_FRAME", MediaID: mediaID, FrameID: &frameID, Position: 0, CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.MediaObject{ID: orphanMediaID, TenantID: fx.seed.TenantID, ResponsibilityID: fx.seed.ResponsibilityID, ObjectKey: "inventory-it/source-" + orphanMediaID.String(), ContentType: "video/mp4", SHA256: strings.Repeat("c", 64), SizeBytes: 6, Status: "READY", MediaKind: "VIDEO", IdempotencyKey: "inventory-it-source-" + orphanMediaID.String(), CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPreparationJob{ID: orphanPreparationID, TenantID: fx.seed.TenantID, MediaID: orphanMediaID, Status: "READY", Phase: "READY", PreparationVersion: 1, Generation: 1, NextAttemptAt: now, SourceSHA256: strings.Repeat("c", 64), ManifestDigest: strings.Repeat("c", 64), PendingObjectKeys: []byte(`[]`), CreatedAt: now}).Error; err != nil {
			return err
		}
		var previous database.OriginVersion
		if err := tx.Where("tenant_id=? AND id=?", fx.seed.TenantID, seed.previous).First(&previous).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.OriginVersion{ID: orphanVersionID, TenantID: fx.seed.TenantID, OriginID: previous.OriginID, VersionNumber: 3, ResponsibilityID: &fx.seed.ResponsibilityID, Source: "CAPTURE", Status: "INVALIDATED", MediaKind: "VIDEO", VideoPreparationID: &orphanPreparationID, IdempotencyKey: "inventory-it-orphan-version-" + orphanVersionID.String(), CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoFrame{ID: orphanFrameID, TenantID: fx.seed.TenantID, SourceMediaID: orphanMediaID, PreparationID: orphanPreparationID, PTSMicros: 0, SHA256: strings.Repeat("d", 64), ObjectKey: orphanFrameKey, Width: 2, Height: 2, Role: "BASELINE", SizeBytes: int64(len(frameBytes)), CreatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.InventoryItemEvidence{ID: identity.NewID(), TenantID: fx.seed.TenantID, ItemID: seed.items[0], Kind: "VIDEO_FRAME", MediaID: orphanMediaID, FrameID: &orphanFrameID, Position: 1, CreatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	activateInventoryDraft(t, fx, seed.version, 4, "it-retain-legacy-frame")
	var references int64
	if err := fx.h.WithinTenant(fx.ctx, fx.seed.TenantID, func(tx *gorm.DB) error {
		return tx.Model(&database.VideoEvidenceReference{}).Where("tenant_id=? AND preparation_id=? AND owner_type='ORIGIN_VERSION' AND owner_id=?", fx.seed.TenantID, preparationID, seed.version).Count(&references).Error
	}); err != nil || references != 1 {
		t.Fatalf("activated inventory should retain its evidence bank: references=%d err=%v", references, err)
	}
	executor, err := retentionexpire.Setup(retentionexpire.Dependencies{DB: fx.h.DB, Store: fx.h.Store})
	if err != nil {
		t.Fatal(err)
	}
	if purged, err := executor.PurgeInactiveOriginBanks(fx.ctx, fx.seed.TenantID, 10); err != nil || purged != 0 {
		t.Fatalf("purge should preserve the legacy frame bank: purged=%d err=%v", purged, err)
	}
	reader, _, err := (objectstore.VideoStore{Store: fx.h.Store}).Open(fx.ctx, frameKey)
	if err != nil {
		t.Fatalf("legacy frame object was removed: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	reader, _, err = (objectstore.VideoStore{Store: fx.h.Store}).Open(fx.ctx, orphanFrameKey)
	if err != nil {
		t.Fatalf("purge deleted a frame still referenced by inventory evidence: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestInventoryUT085BaselineVersionSummariesAreBatchedAndComplete(t *testing.T) {
	fx := newInventoryITFixture(t, auth.TenantAdmin)
	seed := seedInventoryDraft(t, fx, true)
	response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, fmt.Sprintf(`query { baselineVersions(assetId:"%s",first:25) { nodes { id originRevision counts { included rejected notReviewed } } } }`, fx.seed.AssetID), fx.ctx, "", "")
	var body struct {
		Data struct {
			Connection struct {
				Nodes []struct {
					ID             string `json:"id"`
					OriginRevision int    `json:"originRevision"`
					Counts         struct {
						Included    int `json:"included"`
						Rejected    int `json:"rejected"`
						NotReviewed int `json:"notReviewed"`
					} `json:"counts"`
				} `json:"nodes"`
			} `json:"baselineVersions"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(response), &body); err != nil {
		t.Fatalf("decode baseline summaries: %v (%s)", err, response)
	}
	for _, node := range body.Data.Connection.Nodes {
		if node.ID == seed.version.String() && (node.OriginRevision != 4 || node.Counts.Included != 1 || node.Counts.NotReviewed != 1 || node.Counts.Rejected != 0) {
			t.Fatalf("draft summary mismatch: %+v; response=%s", node, response)
		}
	}
	if !strings.Contains(response, seed.version.String()) {
		t.Fatalf("baselineVersions omitted draft node: %s", response)
	}
}

func TestInventoryIT019AuditorReadsTrailAndCannotMutate(t *testing.T) {
	fx := newInventoryITFixture(t, auth.Auditor)
	seed := seedInventoryDraft(t, fx, false)
	read := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, fmt.Sprintf(`query { inventoryTrail(originVersionId:"%s") { decisions { id } } }`, seed.version), fx.ctx, "", "")
	if strings.Contains(read, `"errors"`) {
		t.Fatalf("auditor could not read inventoryTrail: %s", read)
	}
	mutations := []string{
		fmt.Sprintf(`mutation { decideInventoryItems(input:{originVersionId:"%s",decisions:[{itemId:"%s",action:"ACCEPT",expectedVersion:1}],clientMutationId:"it019-decide"}) { userErrors { code field } } }`, seed.version, seed.items[0]),
		fmt.Sprintf(`mutation { bulkAcceptInventoryEnvironment(input:{originVersionId:"%s",environmentId:"%s",clientMutationId:"it019-bulk"}) { userErrors { code field } } }`, seed.version, seed.environments[0]),
		fmt.Sprintf(`mutation { addInventoryItem(input:{originVersionId:"%s",environmentId:"%s",typeCode:"OTHER",name:"Outro",evidence:[],clientMutationId:"it019-add"}) { userErrors { code field } } }`, seed.version, seed.environments[0]),
		fmt.Sprintf(`mutation { mergeInventoryItems(input:{originVersionId:"%s",itemIds:["%s","%s"],clientMutationId:"it019-merge"}) { userErrors { code field } } }`, seed.version, seed.items[0], seed.items[1]),
		fmt.Sprintf(`mutation { splitInventoryItem(input:{originVersionId:"%s",itemId:"%s",expectedVersion:1,parts:[{name:"A",typeCode:"SOFA",evidenceIds:[]},{name:"B",typeCode:"SOFA",evidenceIds:[]}],clientMutationId:"it019-split"}) { userErrors { code field } } }`, seed.version, seed.items[0]),
		fmt.Sprintf(`mutation { upsertInventoryEnvironment(input:{originVersionId:"%s",typeCode:"OTHER",name:"Outro",expected:false,clientMutationId:"it019-upsert"}) { userErrors { code field } } }`, seed.version),
		fmt.Sprintf(`mutation { removeInventoryEnvironment(input:{originVersionId:"%s",environmentId:"%s",confirmItemExclusion:true,clientMutationId:"it019-remove"}) { userErrors { code field } } }`, seed.version, seed.environments[0]),
		fmt.Sprintf(`mutation { activateBaselineVersion(input:{originVersionId:"%s",purpose:"MOVE_IN",expectedOriginVersion:1,counterpartyParticipantIds:[],clientMutationId:"it019-activate"}) { userErrors { code field } } }`, seed.version),
		fmt.Sprintf(`mutation { discardBaselineDraft(input:{originVersionId:"%s",clientMutationId:"it019-discard"}) { userErrors { code field } } }`, seed.version),
		fmt.Sprintf(`mutation { requestOriginCataloging(input:{assetId:"%s",clientMutationId:"it019-request"}) { userErrors { code field } } }`, fx.seed.AssetID),
		fmt.Sprintf(`mutation { startBaselineDraftFromInspection(input:{inspectionId:"%s",clientMutationId:"it019-start"}) { userErrors { code field } } }`, fx.seed.InspectionID),
	}
	for i, mutation := range mutations {
		response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, mutation, fx.ctx, "", "")
		if !strings.Contains(response, `"code":"FORBIDDEN"`) {
			t.Fatalf("auditor inventory mutation %d should be forbidden: %s", i+1, response)
		}
	}
}

func TestInventoryTenantAdminIsTenantWideWithoutResourceGrant(t *testing.T) {
	fx := newInventoryITFixture(t, auth.TenantAdmin)
	seed := seedInventoryDraft(t, fx, false)
	if err := fx.h.WithinTenant(fx.ctx, fx.seed.TenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND membership_id=?", fx.seed.TenantID, fx.seed.MembershipID).Delete(&database.ResourceScope{}).Error
	}); err != nil {
		t.Fatal(err)
	}
	query := fmt.Sprintf(`query { inventoryDraft(originVersionId:"%s") { version { id } } }`, seed.version)
	read := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, query, fx.ctx, "", "")
	if strings.Contains(read, `"errors"`) || !strings.Contains(read, seed.version.String()) {
		t.Fatalf("tenant admin could not read draft without a grant: %s", read)
	}
	mutation := fmt.Sprintf(`mutation { decideInventoryItems(input:{originVersionId:"%s",decisions:[{itemId:"%s",action:"ACCEPT",expectedVersion:1}],clientMutationId:"it-tenant-admin-scope"}) { userErrors { code field } } }`, seed.version, seed.items[0])
	response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, mutation, fx.ctx, "", "")
	if strings.Contains(response, `"code":"FORBIDDEN"`) || !strings.Contains(response, `"userErrors":[]`) {
		t.Fatalf("tenant admin could not decide without a grant: %s", response)
	}
}

func TestInventoryArchivedAssetRemainsReadableAndActivationReturnsDomainState(t *testing.T) {
	fx := newInventoryITFixture(t, auth.Manager)
	seed := seedInventoryDraft(t, fx, true)
	var asset database.Asset
	if err := fx.h.WithinTenant(fx.ctx, fx.seed.TenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND id=?", fx.seed.TenantID, fx.seed.AssetID).First(&asset).Error; err != nil {
			return err
		}
		if err := tx.Where("tenant_id=? AND membership_id=?", fx.seed.TenantID, fx.seed.MembershipID).Delete(&database.ResourceScope{}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.ResourceScope{ID: identity.NewID(), TenantID: fx.seed.TenantID, MembershipID: fx.seed.MembershipID, Kind: "BUSINESS_UNIT", ResourceID: asset.BusinessUnitID, CreatedAt: time.Now().UTC()}).Error; err != nil {
			return err
		}
		return tx.Model(&database.Asset{}).Where("tenant_id=? AND id=?", fx.seed.TenantID, asset.ID).Update("status", "ARCHIVED").Error
	}); err != nil {
		t.Fatal(err)
	}
	meta, _ := requestctx.FromContext(fx.ctx)
	meta.Principal.Scopes = nil
	scopedContext := requestctx.WithMetadata(context.Background(), meta)
	read := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, fmt.Sprintf(`query { baselineInventory(assetId:"%s") { hasInventory } }`, fx.seed.AssetID), scopedContext, "", "")
	if strings.Contains(read, `"errors"`) {
		t.Fatalf("BU-scoped manager could not read archived asset baseline: %s", read)
	}
	response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, fmt.Sprintf(`mutation { activateBaselineVersion(input:{originVersionId:"%s",purpose:"MOVE_IN",expectedOriginVersion:4,counterpartyParticipantIds:[],clientMutationId:"it-archived-activation"}) { userErrors { code field } } }`, seed.version), scopedContext, "", "")
	if !strings.Contains(response, `"code":"INVALID_STATE"`) || !strings.Contains(response, `"field":"assetId"`) {
		t.Fatalf("archived activation error mismatch: %s", response)
	}
}

func TestInventoryUT088EmployeeWithoutAssetScopeIsForbidden(t *testing.T) {
	fx := newInventoryITFixture(t, auth.Employee)
	meta, _ := requestctx.FromContext(fx.ctx)
	meta.Principal.Scopes = nil
	if err := fx.h.WithinTenant(fx.ctx, fx.seed.TenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND membership_id=?", fx.seed.TenantID, fx.seed.MembershipID).Delete(&database.ResourceScope{}).Error
	}); err != nil {
		t.Fatal(err)
	}
	ctx := requestctx.WithMetadata(context.Background(), meta)
	response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, fmt.Sprintf(`mutation { requestOriginCataloging(input:{assetId:"%s",clientMutationId:"ut088"}) { userErrors { code field } } }`, fx.seed.AssetID), ctx, "", "")
	if !strings.Contains(response, `"code":"FORBIDDEN"`) {
		t.Fatalf("employee without asset scope must be forbidden: %s", response)
	}
}

type inventoryITSeed struct {
	version, previous identity.ID
	environments      []identity.ID
	items             []identity.ID
}

func newInventoryITFixture(t *testing.T, role string) api06Fixture {
	return newInventoryITFixtureWithOptions(t, role, harness.Task06FixtureOptions{})
}

func newInventoryITFixtureWithOptions(t *testing.T, role string, options harness.Task06FixtureOptions) api06Fixture {
	t.Helper()
	fx := newAPI06FixtureWithOptions(t, options)
	service := core.Service{DB: fx.h.DB, Within: fx.h.WithinTenant}
	setups := []func() error{
		func() error { return request_cataloging.Setup(fx.bus, service) },
		func() error {
			return promoteorigin.Setup(promoteorigin.Dependencies{DB: fx.h.DB, Bus: fx.bus, Authorizer: auth.Authorizer{}, Inventory: service})
		},
		func() error { return decide_items.Setup(fx.bus, service) },
		func() error { return activate_version.Setup(fx.bus, service) },
		func() error { return discard_draft.Setup(fx.bus, service) },
		func() error { return remove_environment.Setup(fx.bus, service) },
		func() error { return add_item.Setup(fx.bus, service) },
		func() error { return bulk_accept_environment.Setup(fx.bus, service) },
		func() error { return merge_items.Setup(fx.bus, service) },
		func() error { return split_item.Setup(fx.bus, service) },
		func() error { return upsert_environment.Setup(fx.bus, service) },
		func() error { return draft.Setup(fx.bus, service) },
		func() error { return baseline.Setup(fx.bus, service) },
		func() error { return trail.Setup(fx.bus, service) },
	}
	for _, setup := range setups {
		if err := setup(); err != nil {
			t.Fatal(err)
		}
	}
	fx.ctx = inventoryITContext(fx, role)
	if role == auth.Employee {
		if err := fx.h.WithinTenant(fx.ctx, fx.seed.TenantID, func(tx *gorm.DB) error {
			return tx.Model(&database.Membership{}).Where("tenant_id=? AND id=?", fx.seed.TenantID, fx.seed.MembershipID).Update("role", auth.Employee).Error
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := fx.h.WithinTenant(fx.ctx, fx.seed.TenantID, func(tx *gorm.DB) error {
		return tx.Create(&database.ResourceScope{ID: identity.NewID(), TenantID: fx.seed.TenantID, MembershipID: fx.seed.MembershipID, Kind: "ASSET", ResourceID: fx.seed.AssetID, CreatedAt: time.Now().UTC()}).Error
	}); err != nil {
		t.Fatal(err)
	}
	return fx
}

func inventoryITContext(fx api06Fixture, role string) context.Context {
	target := requestctx.Scope{Kind: "ASSET", ID: fx.seed.AssetID}
	products := auth.ProductsForRole(role)
	return requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: fx.seed.TenantID, Principal: requestctx.Principal{IdentityID: fx.seed.IdentityID, MembershipID: fx.seed.MembershipID, TenantID: fx.seed.TenantID, Roles: []string{role}, Scopes: []requestctx.Scope{target}, ProductEntitlements: products}, CorrelationID: "inventory-it"})
}

func seedInventoryDraft(t *testing.T, fx api06Fixture, withPrevious bool) inventoryITSeed {
	t.Helper()
	seed := inventoryITSeed{version: identity.NewID(), environments: []identity.ID{identity.NewID()}, items: []identity.ID{identity.NewID(), identity.NewID()}}
	if withPrevious {
		seed.previous = identity.NewID()
	}
	if err := fx.h.WithinTenant(fx.ctx, fx.seed.TenantID, func(tx *gorm.DB) error {
		now := time.Now().UTC()
		originRevision := int64(1)
		if withPrevious {
			originRevision = 4
		}
		origin := database.Origin{ID: identity.NewID(), TenantID: fx.seed.TenantID, AssetID: fx.seed.AssetID, Version: originRevision, CreatedAt: now, UpdatedAt: now}
		if withPrevious {
			origin.ActiveVersionID = &seed.previous
			if err := tx.Create(&origin).Error; err != nil {
				return err
			}
			purpose := "MOVE_IN"
			if err := tx.Create(&database.OriginVersion{ID: seed.previous, TenantID: fx.seed.TenantID, OriginID: origin.ID, VersionNumber: 1, Purpose: &purpose, Source: "CAPTURE", HasInventory: true, Status: "ACTIVE", DraftState: inventoryStringPtr("READY"), IdempotencyKey: "inventory-it-prev-" + seed.previous.String(), ActivatedAt: &now, CreatedAt: now}).Error; err != nil {
				return err
			}
		} else if err := tx.Create(&origin).Error; err != nil {
			return err
		}
		state := "READY"
		if err := tx.Create(&database.OriginVersion{ID: seed.version, TenantID: fx.seed.TenantID, OriginID: origin.ID, VersionNumber: 2, Source: "CAPTURE", HasInventory: true, Status: "DRAFT", DraftState: &state, IdempotencyKey: "inventory-it-draft-" + seed.version.String(), CreatedAt: now}).Error; err != nil {
			return err
		}
		env := database.InventoryEnvironment{ID: seed.environments[0], TenantID: fx.seed.TenantID, OriginVersionID: seed.version, IdentityID: identity.NewID(), TypeCode: "LIVING_ROOM", Name: "Sala", Expected: true, Decision: "ACCEPTED", Position: 0, Version: 1, CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&env).Error; err != nil {
			return err
		}
		confidence := 9000
		for i, id := range seed.items {
			decision := "PENDING"
			if withPrevious && i == 0 {
				decision = "ACCEPTED"
			}
			item := database.InventoryItem{ID: id, TenantID: fx.seed.TenantID, OriginVersionID: seed.version, EnvironmentID: env.ID, IdentityID: identity.NewID(), TypeCode: "SOFA", Name: fmt.Sprintf("Sofá %d", i), Quantity: intPtr(1), Condition: "GOOD", Observation: "OBSERVED", ConfidenceBps: &confidence, Origin: "AI", Decision: decision, Version: 1, CreatedAt: now, UpdatedAt: now}
			if err := tx.Create(&item).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return seed
}

func addInventoryITItem(t *testing.T, fx api06Fixture, seed inventoryITSeed, name string) identity.ID {
	t.Helper()
	query := fmt.Sprintf(`mutation { addInventoryItem(input:{originVersionId:"%s",environmentId:"%s",typeCode:"OTHER",name:"%s",quantity:1,evidence:[],clientMutationId:"it014-add-%s"}) { item { id } userErrors { code field } } }`, seed.version, seed.environments[0], name, strings.ReplaceAll(name, " ", "-"))
	response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, query, fx.ctx, "", "")
	var body struct {
		Data struct {
			Add struct {
				Item struct {
					ID string `json:"id"`
				} `json:"item"`
			} `json:"addInventoryItem"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(response), &body); err != nil || body.Data.Add.Item.ID == "" || strings.Contains(response, `"userErrors":[{"code":`) {
		t.Fatalf("addInventoryItem failed: %s (%v)", response, err)
	}
	id, err := identity.ParseID(body.Data.Add.Item.ID)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func addInventoryITEnvironment(t *testing.T, fx api06Fixture, seed inventoryITSeed) identity.ID {
	t.Helper()
	response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, fmt.Sprintf(`mutation { upsertInventoryEnvironment(input:{originVersionId:"%s",typeCode:"BATHROOM",name:"Banheiro extra",expected:false,clientMutationId:"it014-upsert-new"}) { environment { id } userErrors { code field } } }`, seed.version), fx.ctx, "", "")
	var body struct {
		Data struct {
			Upsert struct {
				Environment struct {
					ID string `json:"id"`
				} `json:"environment"`
			} `json:"upsertInventoryEnvironment"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(response), &body); err != nil || body.Data.Upsert.Environment.ID == "" || strings.Contains(response, `"userErrors":[{"code":`) {
		t.Fatalf("create environment failed: %s (%v)", response, err)
	}
	id, err := identity.ParseID(body.Data.Upsert.Environment.ID)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func activateInventoryDraft(t *testing.T, fx api06Fixture, version identity.ID, expected int, clientID string) {
	t.Helper()
	response := executeFixedOriginRequest(t, fx.fixedOriginGraphQLFixture, fmt.Sprintf(`mutation { activateBaselineVersion(input:{originVersionId:"%s",purpose:"CONSTRUCTION_HANDOVER",expectedOriginVersion:%d,counterpartyParticipantIds:[],clientMutationId:"%s"}) { version { status } userErrors { code field } } }`, version, expected, clientID), fx.ctx, "", "")
	if strings.Contains(response, `"userErrors":[{"code":`) || !strings.Contains(response, `"status":"ACTIVE"`) {
		t.Fatalf("activateBaselineVersion failed: %s", response)
	}
}

func assertInventoryVersionStatus(t *testing.T, fx api06Fixture, id identity.ID, status string) {
	t.Helper()
	var version database.OriginVersion
	if err := fx.h.AdminDB.Where("tenant_id=? AND id=?", fx.seed.TenantID, id).Take(&version).Error; err != nil {
		t.Fatal(err)
	}
	if version.Status != status {
		t.Fatalf("version status=%s, want %s", version.Status, status)
	}
}

func intPtr(v int) *int                   { return &v }
func inventoryStringPtr(v string) *string { return &v }
