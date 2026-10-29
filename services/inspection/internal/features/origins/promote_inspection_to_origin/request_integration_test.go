//go:build integration

package promote_inspection_to_origin

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"inspection/libs/identity"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	"inspection/services/inspection/internal/features/inventory/core"
	retentioncore "inspection/services/inspection/internal/features/retention/core"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"

	"gorm.io/gorm"
)

func TestRequestCreatesPromotionAndIsIdempotent(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	f, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{InspectionStatus: "COMPLETED"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	var priorDraftID identity.ID
	if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
		draft, err := (core.Service{DB: h.DB, Now: func() time.Time { return now }}).CreateDraft(tx, core.DraftInput{TenantID: f.TenantID, AssetID: f.AssetID, Source: "CAPTURE", SourceInspectionID: f.InspectionID})
		if err != nil {
			return err
		}
		priorDraftID = draft.ID
		if err := tx.Model(&database.OriginVersion{}).Where("tenant_id=? AND id=?", f.TenantID, draft.ID).Updates(map[string]any{"status": "DISCARDED", "discarded_at": now}).Error; err != nil {
			return err
		}
		job := database.InventoryJob{ID: identity.NewID(), TenantID: f.TenantID, InspectionID: f.InspectionID, OriginVersionID: draft.ID, Kind: "CATALOG", RequirementKey: "old-generation", Generation: 4, Status: "SUCCEEDED", NextAttemptAt: now, CreatedAt: now, UpdatedAt: now}
		return tx.Create(&job).Error
	}); err != nil {
		t.Fatal(err)
	}
	bus := mediator.New()
	deps := Dependencies{DB: h.DB, Bus: bus, Authorizer: auth.Authorizer{}, Now: func() time.Time { return now }, Inventory: core.Service{DB: h.DB, Now: func() time.Time { return now }}}
	if err := Setup(deps); err != nil {
		t.Fatal(err)
	}
	ctx = requestctx.WithMetadata(ctx, requestctx.Metadata{TenantID: f.TenantID, Principal: requestctx.Principal{IdentityID: f.IdentityID, MembershipID: f.MembershipID, TenantID: f.TenantID, Roles: []string{auth.TenantAdmin}}})
	queryRaw, err := bus.Ask(ctx, Query{TenantID: f.TenantID, InspectionID: f.InspectionID})
	if err != nil {
		t.Fatal(err)
	}
	view := queryRaw.(StatusView)
	if !view.Eligible {
		t.Fatalf("fixture should be eligible: %+v", view.Impediments)
	}
	if _, err := bus.Ask(ctx, Query{TenantID: identity.NewID(), InspectionID: f.InspectionID}); err == nil {
		t.Fatal("another tenant must not read the inspection")
	} else if code, _, _ := apperror.Public(err); code != apperror.NotFound {
		t.Fatalf("tenant isolation should return not found, got %v", err)
	}
	command := Command{TenantID: f.TenantID, InspectionID: f.InspectionID, ClientMutationID: "promotion-it-1", ConfirmationDigest: view.ConfirmationDigest}
	wrongDigest := command
	wrongDigest.ConfirmationDigest = "stale"
	if _, err := bus.Send(ctx, wrongDigest); err == nil {
		t.Fatal("stale digest should conflict")
	} else if code, field, _ := apperror.Public(err); code != apperror.Conflict || field != "confirmationDigest" {
		t.Fatalf("unexpected digest error: %v", err)
	}
	firstRaw, err := bus.Send(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	first := firstRaw.(Result)
	if first.Promotion.Status != "PREPARING" || first.Promotion.DraftVersionID == nil {
		t.Fatalf("promotion not prepared: %+v", first.Promotion)
	}
	if first.Promotion.CatalogGeneration == nil || *first.Promotion.CatalogGeneration != 5 {
		t.Fatalf("catalog generation=%v; want max+1 after prior draft %s", first.Promotion.CatalogGeneration, priorDraftID)
	}
	secondRaw, err := bus.Send(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	second := secondRaw.(Result)
	if second.Promotion.ID != first.Promotion.ID {
		t.Fatalf("request was not idempotent: %s != %s", second.Promotion.ID, first.Promotion.ID)
	}
	var jobs int64
	if err := h.AdminDB.Model(&database.InventoryJob{}).Where("tenant_id=? AND inspection_id=? AND origin_version_id=? AND kind='CATALOG'", f.TenantID, f.InspectionID, *first.Promotion.DraftVersionID).Count(&jobs).Error; err != nil {
		t.Fatal(err)
	}
	if jobs == 0 {
		t.Fatal("promotion did not create CATALOG jobs")
	}
}

func TestProcessActivatesReadyDraftAndAutomaticallyAcceptsEvidenceBackedItems(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	f, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{InspectionStatus: "COMPLETED"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	bus := mediator.New()
	inventory := core.Service{DB: h.DB, Now: func() time.Time { return now }}
	if err := Setup(Dependencies{DB: h.DB, Bus: bus, Authorizer: auth.Authorizer{}, Now: func() time.Time { return now }, Inventory: inventory}); err != nil {
		t.Fatal(err)
	}
	ctx = requestctx.WithMetadata(ctx, requestctx.Metadata{TenantID: f.TenantID, Principal: requestctx.Principal{IdentityID: f.IdentityID, MembershipID: f.MembershipID, TenantID: f.TenantID, Roles: []string{auth.TenantAdmin}}})
	raw, err := bus.Ask(ctx, Query{TenantID: f.TenantID, InspectionID: f.InspectionID})
	if err != nil {
		t.Fatal(err)
	}
	view := raw.(StatusView)
	requested, err := bus.Send(ctx, Command{TenantID: f.TenantID, InspectionID: f.InspectionID, ClientMutationID: "process-ready-draft", Purpose: "INVENTORY_UPDATE", ConfirmationDigest: view.ConfirmationDigest})
	if err != nil {
		t.Fatal(err)
	}
	p := requested.(Result).Promotion
	if p.DraftVersionID == nil {
		t.Fatal("promotion has no draft")
	}
	if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
		if err := tx.Model(&database.OriginVersion{}).Where("tenant_id=? AND id=?", f.TenantID, *p.DraftVersionID).Update("draft_state", "READY").Error; err != nil {
			return err
		}
		var env database.InventoryEnvironment
		if err := tx.Where("tenant_id=? AND origin_version_id=?", f.TenantID, *p.DraftVersionID).Take(&env).Error; err != nil {
			return err
		}
		item := database.InventoryItem{ID: identity.NewID(), TenantID: f.TenantID, OriginVersionID: *p.DraftVersionID, EnvironmentID: env.ID, IdentityID: identity.NewID(), TypeCode: "SOFA", Name: "Sofá", Condition: "NOT_ASSESSABLE", Observation: "NOT_ASSESSABLE", Origin: "AI", Decision: "PENDING", ConfidenceBps: nil, Version: 1, CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&item).Error; err != nil {
			return err
		}
		return tx.Create(&database.InventoryItemEvidence{ID: identity.NewID(), TenantID: f.TenantID, ItemID: item.ID, Kind: "PHOTO", MediaID: f.MediaID, Position: 0, CreatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	deps := ProcessDependencies{Inventory: core.Service{DB: h.DB, Now: func() time.Time { return now }, Retention: retentioncore.BaselineTransition{}}, Now: func() time.Time { return now }}
	if err := Process(ctx, h.DB, deps, f.TenantID, p.ID); err != nil {
		t.Fatal(err)
	}
	var final database.InspectionOriginPromotion
	if err := h.AdminDB.Where("tenant_id=? AND id=?", f.TenantID, p.ID).Take(&final).Error; err != nil {
		t.Fatal(err)
	}
	if final.Status != "COMPLETED" || final.OriginVersionID == nil || *final.OriginVersionID != *p.DraftVersionID {
		t.Fatalf("promotion=%+v", final)
	}
	var item database.InventoryItem
	if err := h.AdminDB.Where("tenant_id=? AND origin_version_id=?", f.TenantID, *p.DraftVersionID).Take(&item).Error; err != nil {
		t.Fatal(err)
	}
	if item.Decision != "ACCEPTED" || item.AutoAcceptedByPromotionID == nil || *item.AutoAcceptedByPromotionID != p.ID || item.DecidedBy != nil || item.Condition != "NOT_ASSESSABLE" || item.Observation != "NOT_ASSESSABLE" {
		t.Fatalf("auto accepted item lost its analysis data or marker: %+v", item)
	}
}

func TestProcessRollsBackAutomaticAcceptanceWhenActivationFails(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	f, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{InspectionStatus: "COMPLETED"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	bus := mediator.New()
	inventory := core.Service{DB: h.DB, Now: func() time.Time { return now }}
	if err := Setup(Dependencies{DB: h.DB, Bus: bus, Authorizer: auth.Authorizer{}, Now: func() time.Time { return now }, Inventory: inventory}); err != nil {
		t.Fatal(err)
	}
	ctx = requestctx.WithMetadata(ctx, requestctx.Metadata{TenantID: f.TenantID, Principal: requestctx.Principal{IdentityID: f.IdentityID, MembershipID: f.MembershipID, TenantID: f.TenantID, Roles: []string{auth.TenantAdmin}}})
	raw, err := bus.Ask(ctx, Query{TenantID: f.TenantID, InspectionID: f.InspectionID})
	if err != nil {
		t.Fatal(err)
	}
	requested, err := bus.Send(ctx, Command{TenantID: f.TenantID, InspectionID: f.InspectionID, ClientMutationID: "process-limit-rollback", Purpose: "INVENTORY_UPDATE", ConfirmationDigest: raw.(StatusView).ConfirmationDigest})
	if err != nil {
		t.Fatal(err)
	}
	p := requested.(Result).Promotion
	if p.DraftVersionID == nil {
		t.Fatal("promotion has no draft")
	}
	var pendingID identity.ID
	if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
		if err := tx.Model(&database.OriginVersion{}).Where("tenant_id=? AND id=?", f.TenantID, *p.DraftVersionID).Update("draft_state", "READY").Error; err != nil {
			return err
		}
		var env database.InventoryEnvironment
		if err := tx.Where("tenant_id=? AND origin_version_id=?", f.TenantID, *p.DraftVersionID).Take(&env).Error; err != nil {
			return err
		}
		for i := 0; i <= core.MaxItemsPerEnvironment; i++ {
			decision := "ACCEPTED"
			if i == core.MaxItemsPerEnvironment {
				decision = "PENDING"
			}
			item := database.InventoryItem{ID: identity.NewID(), TenantID: f.TenantID, OriginVersionID: *p.DraftVersionID, EnvironmentID: env.ID, IdentityID: identity.NewID(), TypeCode: "OTHER", Name: "Item", Condition: "GOOD", Observation: "OBSERVED", Origin: "AI", Decision: decision, Version: 1, CreatedAt: now, UpdatedAt: now}
			if err := tx.Create(&item).Error; err != nil {
				return err
			}
			if decision == "PENDING" {
				pendingID = item.ID
				if err := tx.Create(&database.InventoryItemEvidence{ID: identity.NewID(), TenantID: f.TenantID, ItemID: item.ID, Kind: "PHOTO", MediaID: f.MediaID, Position: 0, CreatedAt: now}).Error; err != nil {
					return err
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	deps := ProcessDependencies{Inventory: core.Service{DB: h.DB, Now: func() time.Time { return now }, Retention: retentioncore.BaselineTransition{}}, Now: func() time.Time { return now }}
	if err := Process(ctx, h.DB, deps, f.TenantID, p.ID); err != nil {
		t.Fatal(err)
	}
	var item database.InventoryItem
	if err := h.AdminDB.Where("tenant_id=? AND id=?", f.TenantID, pendingID).Take(&item).Error; err != nil {
		t.Fatal(err)
	}
	var final database.InspectionOriginPromotion
	if err := h.AdminDB.Where("tenant_id=? AND id=?", f.TenantID, p.ID).Take(&final).Error; err != nil {
		t.Fatal(err)
	}
	if item.Decision != "PENDING" || item.AutoAcceptedByPromotionID != nil || final.Status != "FAILED" || final.FailureCode == nil || *final.FailureCode != "INCOMPLETE_ANALYSIS" {
		t.Fatalf("activation failure committed auto-acceptance: item=%+v promotion=%+v", item, final)
	}
}

func TestRequestRequiresDigestAndCanReplaceAnotherDraft(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	f, err := h.SeedInventoryBaseline(ctx, harness.Task06FixtureOptions{InspectionStatus: "COMPLETED"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	bus := mediator.New()
	deps := Dependencies{DB: h.DB, Bus: bus, Authorizer: auth.Authorizer{}, Now: func() time.Time { return now }, Inventory: core.Service{DB: h.DB, Now: func() time.Time { return now }}}
	if err := Setup(deps); err != nil {
		t.Fatal(err)
	}
	ctx = requestctx.WithMetadata(ctx, requestctx.Metadata{TenantID: f.TenantID, Principal: requestctx.Principal{IdentityID: f.IdentityID, MembershipID: f.MembershipID, TenantID: f.TenantID, Roles: []string{auth.TenantAdmin}}})
	if _, err := bus.Ask(ctx, Query{TenantID: f.TenantID, InspectionID: f.InspectionID}); err != nil {
		t.Fatal(err)
	}
	var conflicting database.OriginVersion
	if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
		var e error
		conflicting, e = deps.Inventory.CreateDraft(tx, core.DraftInput{TenantID: f.TenantID, AssetID: f.AssetID, Source: "CONTESTATION", SourceInspectionID: f.InspectionID, SourceVersionID: f.OriginVersionID, ActorMembershipID: f.MembershipID})
		if e != nil {
			return e
		}
		job := database.InventoryJob{ID: identity.NewID(), TenantID: f.TenantID, InspectionID: f.InspectionID, OriginVersionID: conflicting.ID, Kind: "CATALOG", RequirementKey: "replace-test", Generation: 1, Status: "RUNNING", NextAttemptAt: now, CreatedAt: now, UpdatedAt: now}
		return tx.Create(&job).Error
	}); err != nil {
		t.Fatal(err)
	}
	statusRaw, err := bus.Ask(ctx, Query{TenantID: f.TenantID, InspectionID: f.InspectionID})
	if err != nil {
		t.Fatal(err)
	}
	status := statusRaw.(StatusView)
	if !status.Eligible || status.ActiveOrigin == nil || status.ConflictingDraft == nil || status.ConflictingDraft.OwnInspection {
		t.Fatalf("expected eligible inspection with another draft: %+v", status)
	}
	command := Command{TenantID: f.TenantID, InspectionID: f.InspectionID, ClientMutationID: "replace-conflict", ConfirmationDigest: status.ConfirmationDigest}
	if _, err := bus.Send(ctx, command); err == nil {
		t.Fatal("request without ReplaceDraftID should conflict")
	} else if code, field, _ := apperror.Public(err); code != apperror.Conflict || field != "replaceDraftId" {
		t.Fatalf("unexpected conflict: %v", err)
	}
	command.ReplaceDraftID = &conflicting.ID
	resultRaw, err := bus.Send(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	result := resultRaw.(Result)
	var copiedItems int64
	if err := h.AdminDB.Model(&database.InventoryItem{}).Where("tenant_id=? AND origin_version_id=?", f.TenantID, *result.Promotion.DraftVersionID).Count(&copiedItems).Error; err != nil {
		t.Fatal(err)
	}
	if copiedItems != 0 {
		t.Fatalf("comparative reference items were copied into promotion: %d", copiedItems)
	}
	var discarded database.OriginVersion
	var job database.InventoryJob
	if err := h.AdminDB.Where("tenant_id=? AND id=?", f.TenantID, conflicting.ID).Take(&discarded).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.AdminDB.Where("tenant_id=? AND origin_version_id=?", f.TenantID, conflicting.ID).Take(&job).Error; err != nil {
		t.Fatal(err)
	}
	if discarded.Status != "DISCARDED" || job.Status != "CANCELED" || job.LeaseToken != nil || job.LeaseUntil != nil {
		t.Fatalf("replacement did not discard/cancel old draft: draft=%s job=%+v", discarded.Status, job)
	}
	if result.Promotion.ReplacedDraftID == nil || *result.Promotion.ReplacedDraftID != conflicting.ID {
		t.Fatalf("replacement lineage missing: %+v", result.Promotion)
	}
}

func TestRequestReusesOwnDraft(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	f, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{InspectionStatus: "COMPLETED"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	inventory := core.Service{DB: h.DB, Now: func() time.Time { return now }}
	var own database.OriginVersion
	if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
		var e error
		own, e = inventory.CreateDraft(tx, core.DraftInput{TenantID: f.TenantID, AssetID: f.AssetID, Source: "CAPTURE", SourceInspectionID: f.InspectionID})
		return e
	}); err != nil {
		t.Fatal(err)
	}
	bus := mediator.New()
	if err := Setup(Dependencies{DB: h.DB, Bus: bus, Authorizer: auth.Authorizer{}, Now: func() time.Time { return now }, Inventory: inventory}); err != nil {
		t.Fatal(err)
	}
	ctx = requestctx.WithMetadata(ctx, requestctx.Metadata{TenantID: f.TenantID, Principal: requestctx.Principal{IdentityID: f.IdentityID, MembershipID: f.MembershipID, TenantID: f.TenantID, Roles: []string{auth.TenantAdmin}}})
	raw, err := bus.Ask(ctx, Query{TenantID: f.TenantID, InspectionID: f.InspectionID})
	if err != nil {
		t.Fatal(err)
	}
	view := raw.(StatusView)
	if !view.Eligible || view.ConflictingDraft == nil || !view.ConflictingDraft.OwnInspection {
		t.Fatalf("own draft not identified: %+v", view)
	}
	requested, err := bus.Send(ctx, Command{TenantID: f.TenantID, InspectionID: f.InspectionID, ClientMutationID: "reuse-own-draft", ConfirmationDigest: view.ConfirmationDigest})
	if err != nil {
		t.Fatal(err)
	}
	result := requested.(Result)
	if result.Promotion.DraftVersionID == nil || *result.Promotion.DraftVersionID != own.ID {
		t.Fatalf("own draft was not reused: %+v", result.Promotion)
	}
	var jobs int64
	if err := h.AdminDB.Model(&database.InventoryJob{}).Where("tenant_id=? AND origin_version_id=? AND kind='CATALOG'", f.TenantID, own.ID).Count(&jobs).Error; err != nil {
		t.Fatal(err)
	}
	if jobs != 0 {
		t.Fatalf("reusing own draft unexpectedly created %d catalog jobs", jobs)
	}
}

func TestRequestCopiesCatalogFromSameSubmissionAndPreservesPendingDecision(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	f, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{InspectionStatus: "COMPLETED"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	inventory := core.Service{DB: h.DB, Now: func() time.Time { return now }}
	var old database.OriginVersion
	var requirementKey string
	if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
		var envData struct{ RequirementKey string }
		if err := tx.Table("capture.requirement_answers").Select("requirement_key").Where("tenant_id=? AND draft_id=?", f.TenantID, f.DraftID).Take(&envData).Error; err != nil {
			return err
		}
		requirementKey = envData.RequirementKey
		var e error
		old, e = inventory.CreateDraft(tx, core.DraftInput{TenantID: f.TenantID, AssetID: f.AssetID, Source: "CAPTURE", SourceInspectionID: f.InspectionID})
		if e != nil {
			return e
		}
		envID, itemID := identity.NewID(), identity.NewID()
		env := database.InventoryEnvironment{ID: envID, TenantID: f.TenantID, OriginVersionID: old.ID, IdentityID: f.EnvironmentIdentityID, TypeCode: "LIVING_ROOM", Name: "Sala", Expected: true, RequirementKey: &requirementKey, Decision: "PENDING", CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&env).Error; err != nil {
			return err
		}
		item := database.InventoryItem{ID: itemID, TenantID: f.TenantID, OriginVersionID: old.ID, EnvironmentID: envID, IdentityID: identity.NewID(), TypeCode: "SOFA", Name: "Sofá observado", Condition: "GOOD", Observation: "OBSERVED", Origin: "AI", Decision: "PENDING", Version: 1, CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&item).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.InventoryItemEvidence{ID: identity.NewID(), TenantID: f.TenantID, ItemID: itemID, Kind: "PHOTO", MediaID: f.MediaID, Position: 0, CreatedAt: now}).Error; err != nil {
			return err
		}
		submittedAt := now.Add(-time.Minute)
		if err := tx.Create(&database.SubmissionVersion{ID: identity.NewID(), TenantID: f.TenantID, DraftID: f.DraftID, ResponsibilityID: f.ResponsibilityID, VersionNumber: 1, Complete: true, Payload: []byte(`{}`), PayloadDigest: "digest", SubmittedAt: submittedAt}).Error; err != nil {
			return err
		}
		job := database.InventoryJob{ID: identity.NewID(), TenantID: f.TenantID, InspectionID: f.InspectionID, OriginVersionID: old.ID, Kind: "CATALOG", RequirementKey: requirementKey, Generation: 1, Status: "SUCCEEDED", NextAttemptAt: submittedAt, CreatedAt: submittedAt.Add(time.Second), UpdatedAt: now}
		if err := tx.Create(&job).Error; err != nil {
			return err
		}
		return tx.Model(&database.OriginVersion{}).Where("tenant_id=? AND id=?", f.TenantID, old.ID).Updates(map[string]any{"status": "DISCARDED", "discarded_at": now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	bus := mediator.New()
	if err := Setup(Dependencies{DB: h.DB, Bus: bus, Authorizer: auth.Authorizer{}, Now: func() time.Time { return now }, Inventory: inventory}); err != nil {
		t.Fatal(err)
	}
	ctx = requestctx.WithMetadata(ctx, requestctx.Metadata{TenantID: f.TenantID, Principal: requestctx.Principal{IdentityID: f.IdentityID, MembershipID: f.MembershipID, TenantID: f.TenantID, Roles: []string{auth.TenantAdmin}}})
	raw, err := bus.Ask(ctx, Query{TenantID: f.TenantID, InspectionID: f.InspectionID})
	if err != nil {
		t.Fatal(err)
	}
	view := raw.(StatusView)
	requested, err := bus.Send(ctx, Command{TenantID: f.TenantID, InspectionID: f.InspectionID, ClientMutationID: "copy-catalog", ConfirmationDigest: view.ConfirmationDigest})
	if err != nil {
		t.Fatal(err)
	}
	result := requested.(Result)
	draftID := *result.Promotion.DraftVersionID
	var draft database.OriginVersion
	if err := h.AdminDB.Where("tenant_id=? AND id=?", f.TenantID, draftID).Take(&draft).Error; err != nil {
		t.Fatal(err)
	}
	var copied database.InventoryItem
	if err := h.AdminDB.Where("tenant_id=? AND origin_version_id=?", f.TenantID, draftID).Take(&copied).Error; err != nil {
		t.Fatal(err)
	}
	var evidenceCount int64
	if err := h.AdminDB.Model(&database.InventoryItemEvidence{}).Where("tenant_id=? AND item_id=?", f.TenantID, copied.ID).Count(&evidenceCount).Error; err != nil {
		t.Fatal(err)
	}
	if draft.DraftState == nil || *draft.DraftState != "READY" || copied.Decision != "PENDING" || evidenceCount != 1 || result.Promotion.CatalogGeneration != nil {
		t.Fatalf("same-submission catalog was not reused correctly: draft=%+v item=%+v evidence=%d promotion=%+v", draft, copied, evidenceCount, result.Promotion)
	}
}

func TestRequestDoesNotReusePartialCatalogGeneration(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	f, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{InspectionStatus: "COMPLETED"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	inventory := core.Service{DB: h.DB, Now: func() time.Time { return now }}
	var prior database.OriginVersion
	var firstRequirement, secondRequirement string
	if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
		var capture database.CaptureDraft
		if err := tx.Where("tenant_id=? AND id=?", f.TenantID, f.DraftID).Take(&capture).Error; err != nil {
			return err
		}
		var requirements []capturecore.Requirement
		if err := json.Unmarshal(capture.Requirements, &requirements); err != nil {
			return err
		}
		if len(requirements) != 1 {
			t.Fatalf("fixture requirements=%d; want one", len(requirements))
		}
		firstRequirement = requirements[0].Key
		second := requirements[0]
		second.Key = "env:partial:" + identity.NewID().String()
		second.EnvironmentIdentityID = identity.NewID().String()
		secondRequirement = second.Key
		requirements = append(requirements, second)
		capture.Requirements, err = json.Marshal(requirements)
		if err != nil {
			return err
		}
		if err := tx.Model(&capture).Update("requirements", capture.Requirements).Error; err != nil {
			return err
		}
		mediaIDs, _ := json.Marshal([]identity.ID{f.MediaID})
		if err := tx.Create(&database.RequirementAnswer{ID: identity.NewID(), TenantID: f.TenantID, DraftID: f.DraftID, RequirementKey: secondRequirement, MediaIDs: mediaIDs, Flags: json.RawMessage(`[]`), Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		var err error
		prior, err = inventory.CreateDraft(tx, core.DraftInput{TenantID: f.TenantID, AssetID: f.AssetID, Source: "CAPTURE", SourceInspectionID: f.InspectionID})
		if err != nil {
			return err
		}
		environmentID := identity.NewID()
		environment := database.InventoryEnvironment{ID: environmentID, TenantID: f.TenantID, OriginVersionID: prior.ID, IdentityID: f.EnvironmentIdentityID, TypeCode: "LIVING_ROOM", Name: "Sala", Expected: true, RequirementKey: &firstRequirement, Decision: "PENDING", CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&environment).Error; err != nil {
			return err
		}
		item := database.InventoryItem{ID: identity.NewID(), TenantID: f.TenantID, OriginVersionID: prior.ID, EnvironmentID: environmentID, IdentityID: identity.NewID(), TypeCode: "SOFA", Name: "Sofá parcial", Condition: "GOOD", Observation: "OBSERVED", Origin: "AI", Decision: "PENDING", Version: 1, CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&item).Error; err != nil {
			return err
		}
		for key, status := range map[string]string{firstRequirement: "SUCCEEDED", secondRequirement: "CANCELED"} {
			job := database.InventoryJob{ID: identity.NewID(), TenantID: f.TenantID, InspectionID: f.InspectionID, OriginVersionID: prior.ID, Kind: "CATALOG", RequirementKey: key, Generation: 1, Status: status, NextAttemptAt: now, CreatedAt: now.Add(time.Second), UpdatedAt: now}
			if err := tx.Create(&job).Error; err != nil {
				return err
			}
		}
		return tx.Model(&database.OriginVersion{}).Where("tenant_id=? AND id=?", f.TenantID, prior.ID).Updates(map[string]any{"status": "DISCARDED", "discarded_at": now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	bus := mediator.New()
	if err := Setup(Dependencies{DB: h.DB, Bus: bus, Authorizer: auth.Authorizer{}, Now: func() time.Time { return now }, Inventory: inventory}); err != nil {
		t.Fatal(err)
	}
	ctx = requestctx.WithMetadata(ctx, requestctx.Metadata{TenantID: f.TenantID, Principal: requestctx.Principal{IdentityID: f.IdentityID, MembershipID: f.MembershipID, TenantID: f.TenantID, Roles: []string{auth.TenantAdmin}}})
	raw, err := bus.Ask(ctx, Query{TenantID: f.TenantID, InspectionID: f.InspectionID})
	if err != nil {
		t.Fatal(err)
	}
	view := raw.(StatusView)
	requested, err := bus.Send(ctx, Command{TenantID: f.TenantID, InspectionID: f.InspectionID, ClientMutationID: "partial-catalog", ConfirmationDigest: view.ConfirmationDigest})
	if err != nil {
		t.Fatal(err)
	}
	promotion := requested.(Result).Promotion
	if promotion.CatalogGeneration == nil || *promotion.CatalogGeneration != 2 {
		t.Fatalf("partial catalog was reused instead of preparing generation 2: %+v", promotion)
	}
	var copiedItems, catalogJobs int64
	if err := h.AdminDB.Model(&database.InventoryItem{}).Where("tenant_id=? AND origin_version_id=?", f.TenantID, *promotion.DraftVersionID).Count(&copiedItems).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.AdminDB.Model(&database.InventoryJob{}).Where("tenant_id=? AND inspection_id=? AND origin_version_id=? AND kind='CATALOG' AND generation=2", f.TenantID, f.InspectionID, *promotion.DraftVersionID).Count(&catalogJobs).Error; err != nil {
		t.Fatal(err)
	}
	if copiedItems != 0 || catalogJobs != 2 {
		t.Fatalf("partial catalog should be discarded and both environments recataloged: copied items=%d jobs=%d prior=%s", copiedItems, catalogJobs, prior.ID)
	}
}
