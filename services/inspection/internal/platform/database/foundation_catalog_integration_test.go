//go:build integration

package database

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database/migrations"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestFoundationCatalog_IT001ToIT007AndIT040(t *testing.T) {
	adminURL := os.Getenv("INSPECTION_TEST_CATALOG_DATABASE_URL")
	if adminURL == "" {
		adminURL = os.Getenv("INSPECTION_TEST_DATABASE_URL")
	}
	if adminURL == "" {
		t.Skip("fresh disposable catalog database URL is required")
	}
	admin, err := gorm.Open(postgres.Open(adminURL), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	var exists *string
	if err := admin.Raw("SELECT to_regclass('platform.schema_migrations')").Scan(&exists).Error; err != nil {
		t.Fatal(err)
	}
	if exists != nil {
		t.Fatal("catalog migration database must be fresh")
	}
	migrator := Migrator{DB: admin}
	if err := migrator.Migrate(ctx); err != nil {
		t.Fatalf("initial migration: %v", err)
	}
	if err := migrator.Migrate(ctx); err != nil {
		t.Fatalf("idempotent second migration: %v", err)
	}
	var latest int
	if err := admin.Raw("SELECT MAX(version) FROM platform.schema_migrations").Scan(&latest).Error; err != nil {
		t.Fatal(err)
	}
	if latest != 85 || migrations.LatestVersion() != 85 {
		t.Fatalf("max migration=%d, catalog=%d, want 85", latest, migrations.LatestVersion())
	}
	t.Run("IT001 migration catalog and forced RLS", func(t *testing.T) {
		for _, table := range []string{"origins.inventory_environments", "origins.inventory_items", "origins.inventory_item_evidence", "origins.inventory_decisions", "analysis.inventory_jobs", "analysis.inventory_evidence_pairs", "analysis.inventory_item_results", "origins.counterparty_invitations", "origins.baseline_policies"} {
			var enabled, forced bool
			var policy string
			if err := admin.Raw(`SELECT c.relrowsecurity,c.relforcerowsecurity,coalesce(p.polname,'') FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace LEFT JOIN pg_policy p ON p.polrelid=c.oid AND p.polname='tenant_isolation' WHERE n.nspname||'.'||c.relname=?`, table).Row().Scan(&enabled, &forced, &policy); err != nil {
				t.Fatal(err)
			}
			if !enabled || !forced || policy != "tenant_isolation" {
				t.Errorf("%s RLS=(%v,%v,%q)", table, enabled, forced, policy)
			}
		}
	})
	t.Run("IT002 version status and inventory purpose checks", func(t *testing.T) {
		tenant, origin := identity.NewID(), identity.NewID()
		if _, err := insertOriginVersion(admin, tenant, origin, "FOO", false, nil); err == nil {
			t.Fatal("FOO status accepted")
		}
		if _, err := insertOriginVersion(admin, tenant, identity.NewID(), "DISCARDED", false, nil); err != nil {
			t.Fatalf("DISCARDED rejected: %v", err)
		}
		if _, err := insertOriginVersion(admin, tenant, identity.NewID(), "ACTIVE", true, nil); err == nil {
			t.Fatal("ACTIVE inventory without purpose accepted")
		}
	})
	t.Run("IT003 one inventory draft per origin", func(t *testing.T) {
		tenant, origin := identity.NewID(), identity.NewID()
		if _, err := insertOriginVersion(admin, tenant, origin, "DRAFT", true, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := insertOriginVersion(admin, tenant, origin, "DRAFT", true, nil); err == nil {
			t.Fatal("second inventory draft accepted")
		}
	})
	t.Run("IT004 tenant RLS hides another tenant", func(t *testing.T) {
		tenantA, tenantB, originID := identity.NewID(), identity.NewID(), identity.NewID()
		versionID, err := insertOriginVersion(admin, tenantB, originID, "DRAFT", true, nil)
		if err != nil {
			t.Fatal(err)
		}
		env := InventoryEnvironment{ID: identity.NewID(), TenantID: tenantB, OriginVersionID: versionID, IdentityID: identity.NewID(), TypeCode: "KITCHEN", Name: "Cozinha", Decision: "PENDING", Version: 1}
		if err := admin.Create(&env).Error; err != nil {
			t.Fatal(err)
		}
		var count int64
		err = admin.Transaction(func(tx *gorm.DB) error {
			if err := tx.Exec("SET LOCAL ROLE inspection_runtime").Error; err != nil {
				return err
			}
			if err := tx.Exec("SELECT set_config('app.tenant_id', ?, true)", tenantA.String()).Error; err != nil {
				return err
			}
			return tx.Model(&InventoryEnvironment{}).Where("id=?", env.ID).Count(&count).Error
		})
		if err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("tenant B inventory visible to A: %d", count)
		}
	})
	t.Run("InventoryEnvironment.Expected false and nullable item attributes persist", func(t *testing.T) {
		tenant, origin := identity.NewID(), identity.NewID()
		versionID, err := insertOriginVersion(admin, tenant, origin, "DRAFT", true, nil)
		if err != nil {
			t.Fatal(err)
		}
		environment := InventoryEnvironment{ID: identity.NewID(), TenantID: tenant, OriginVersionID: versionID, IdentityID: identity.NewID(), TypeCode: "KITCHEN", Name: "Cozinha", Expected: false, AddedDuringCapture: true, Decision: "PENDING", Version: 1}
		if err := admin.Create(&environment).Error; err != nil {
			t.Fatal(err)
		}
		var storedEnvironment InventoryEnvironment
		if err := admin.First(&storedEnvironment, "id=?", environment.ID).Error; err != nil {
			t.Fatal(err)
		}
		if storedEnvironment.Expected || !storedEnvironment.AddedDuringCapture {
			t.Fatalf("environment flags after persistence: expected=%v added=%v", storedEnvironment.Expected, storedEnvironment.AddedDuringCapture)
		}
		item := InventoryItem{ID: identity.NewID(), TenantID: tenant, OriginVersionID: versionID, EnvironmentID: environment.ID, IdentityID: identity.NewID(), TypeCode: "SINK", Name: "Pia", Condition: "GOOD", Observation: "OBSERVED", Origin: "AI", Decision: "PENDING", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
		if err := admin.Create(&item).Error; err != nil {
			t.Fatal(err)
		}
		var storedItem InventoryItem
		if err := admin.First(&storedItem, "id=?", item.ID).Error; err != nil {
			t.Fatal(err)
		}
		if storedItem.Material != nil || storedItem.Color != nil || storedItem.Quantity != nil {
			t.Fatalf("nil inventory attributes did not persist as NULL: %+v", storedItem)
		}
	})
	t.Run("IT005 runtime cannot update decision trail", func(t *testing.T) {
		err := admin.Transaction(func(tx *gorm.DB) error {
			if err := tx.Exec("SET LOCAL ROLE inspection_runtime").Error; err != nil {
				return err
			}
			return tx.Exec("UPDATE origins.inventory_decisions SET action=action WHERE false").Error
		})
		if err == nil || !strings.Contains(err.Error(), "42501") {
			t.Fatalf("runtime UPDATE error=%v, want PostgreSQL insufficient_privilege SQLSTATE 42501", err)
		}
	})
	t.Run("photo evidence pairs deduplicate NULL frame ids", func(t *testing.T) {
		tenant, jobID := identity.NewID(), identity.NewID()
		originMedia, currentMedia, itemID := identity.NewID(), identity.NewID(), identity.NewID()
		err := admin.Transaction(func(tx *gorm.DB) error {
			if err := tx.Exec("SET LOCAL session_replication_role = replica").Error; err != nil {
				return err
			}
			if err := tx.Exec(`INSERT INTO analysis.inventory_jobs(id,tenant_id,inspection_id,origin_version_id,kind,requirement_key,status,attempts,next_attempt_at,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, jobID, tenant, identity.NewID(), identity.NewID(), "COMPARE", "photo", "PENDING", 0, time.Now().UTC(), time.Now().UTC()).Error; err != nil {
				return err
			}
			insert := func() error {
				return tx.Exec(`INSERT INTO analysis.inventory_evidence_pairs(id,tenant_id,job_id,item_identity_id,origin_kind,origin_media_id,current_kind,current_media_id,decision,confirmed,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, identity.NewID(), tenant, jobID, itemID, "PHOTO", originMedia, "PHOTO", currentMedia, "UNCERTAIN", false, time.Now().UTC()).Error
			}
			if err := insert(); err != nil {
				return err
			}
			duplicateErr := insert()
			if duplicateErr == nil || !strings.Contains(duplicateErr.Error(), "uq_inventory_pair_identity") {
				return fmt.Errorf("duplicate photo pair error=%v", duplicateErr)
			}
			return duplicateErr
		})
		if err == nil || !strings.Contains(err.Error(), "uq_inventory_pair_identity") {
			t.Fatalf("duplicate photo pair error=%v", err)
		}
	})
	t.Run("IT006 external session has exactly one owner", func(t *testing.T) {
		tenant := identity.NewID()
		err := admin.Exec(`INSERT INTO invitations.external_sessions(id,tenant_id,invitation_id,responsibility_id,kind,counterparty_invitation_id,session_digest,csrf_digest,expires_at) VALUES(?,?,?,?,?,?,?,?,?)`, identity.NewID(), tenant, identity.NewID(), identity.NewID(), "COUNTERPARTY", identity.NewID(), []byte("session"), []byte("csrf"), time.Now().Add(time.Hour)).Error
		if err == nil || !strings.Contains(err.Error(), "chk_external_session_owner") {
			t.Fatal("session with two owners accepted")
		}
		insertSession := func(kind string, invitationID, responsibilityID, counterpartyID *identity.ID) error {
			// Replica mode skips FK triggers only; CHECK constraints still apply.
			return admin.Transaction(func(tx *gorm.DB) error {
				if err := tx.Exec("SET LOCAL session_replication_role = replica").Error; err != nil {
					return err
				}
				return tx.Exec(`INSERT INTO invitations.external_sessions(id,tenant_id,invitation_id,responsibility_id,kind,counterparty_invitation_id,session_digest,csrf_digest,expires_at) VALUES(?,?,?,?,?,?,?,?,?)`, identity.NewID(), tenant, invitationID, responsibilityID, kind, counterpartyID, []byte(identity.NewID().String()), []byte("csrf"), time.Now().Add(time.Hour)).Error
			})
		}
		if err := insertSession("COUNTERPARTY", nil, nil, identity.Ptr(identity.NewID())); err != nil {
			t.Fatalf("counterparty session with its only owner rejected: %v", err)
		}
		if err := insertSession("CAPTURE", nil, identity.Ptr(identity.NewID()), nil); err == nil || !strings.Contains(err.Error(), "chk_external_session_owner") {
			t.Fatalf("capture session without invitation accepted: %v", err)
		}
		if err := insertSession("CAPTURE", identity.Ptr(identity.NewID()), identity.Ptr(identity.NewID()), nil); err != nil {
			t.Fatalf("capture session with both capture owners rejected: %v", err)
		}
	})
	t.Run("outside inventory results require type and name", func(t *testing.T) {
		tenant := identity.NewID()
		insertResult := func(status string, itemIdentityID *identity.ID, outsideType, outsideName *string) error {
			return admin.Transaction(func(tx *gorm.DB) error {
				if err := tx.Exec("SET LOCAL session_replication_role = replica").Error; err != nil {
					return err
				}
				return tx.Exec(`INSERT INTO analysis.inventory_item_results(id,tenant_id,job_id,inspection_id,item_identity_id,status,outside_type_code,outside_name,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, identity.NewID(), tenant, identity.NewID(), identity.NewID(), itemIdentityID, status, outsideType, outsideName, time.Now().UTC()).Error
			})
		}
		sofa, name := "SOFA", "Sofá retrátil"
		if err := insertResult("OUTSIDE_INVENTORY", nil, nil, nil); err == nil || !strings.Contains(err.Error(), "chk_inventory_result_outside_details") {
			t.Fatalf("outside result without type and name accepted: %v", err)
		}
		if err := insertResult("OUTSIDE_INVENTORY", nil, &sofa, &name); err != nil {
			t.Fatalf("outside result rejected: %v", err)
		}
		if err := insertResult("MISSING", identity.Ptr(identity.NewID()), &sofa, &name); err == nil {
			t.Fatal("baseline result with outside details accepted")
		}
		if err := insertResult("MISSING", nil, nil, nil); err == nil || !strings.Contains(err.Error(), "chk_inventory_result_outside_identity") {
			t.Fatalf("baseline result without item identity accepted: %v", err)
		}
	})
	t.Run("IT007 FIXED video source requires expiry", func(t *testing.T) {
		insertSource := func(anchor string, ready, expiry *time.Time) error {
			tenant, mediaID := identity.NewID(), identity.NewID()
			media := MediaObject{ID: mediaID, TenantID: tenant, ResponsibilityID: identity.NewID(), ObjectKey: "it007/" + mediaID.String(), ContentType: "video/mp4", SHA256: strings.Repeat("a", 64), SizeBytes: 1, Status: "READY", MediaKind: "VIDEO", IdempotencyKey: "it007-" + mediaID.String(), CreatedAt: time.Now().UTC()}
			if err := admin.Create(&media).Error; err != nil {
				return err
			}
			return admin.Create(&VideoSource{ID: identity.NewID(), TenantID: tenant, MediaID: mediaID, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 1, Width: 1, Height: 1, FrameRate: 30, ScreeningStatus: "APPROVED", Status: "RETAINED", RetentionAnchor: anchor, FirstFrameReadyAt: ready, SourceExpiryAt: expiry}).Error
		}
		now := time.Now().UTC()
		if err := insertSource("FIXED", &now, nil); err == nil || !strings.Contains(err.Error(), "chk_video_source_retention_expiry") {
			t.Fatalf("ready FIXED retained source without expiry accepted: %v", err)
		}
		if err := insertSource("FIXED", nil, nil); err != nil {
			t.Fatalf("pre-ready FIXED source without expiry rejected: %v", err)
		}
		future := now.Add(30 * 24 * time.Hour)
		if err := insertSource("FIXED", nil, &future); err != nil {
			t.Fatalf("pre-ready fixed source with expiry rejected: %v", err)
		}
		if err := insertSource("FIXED", &now, &future); err != nil {
			t.Fatalf("ready fixed source with expiry rejected: %v", err)
		}
		if err := insertSource("ACTIVE_BASELINE", nil, nil); err != nil {
			t.Fatalf("active baseline source without expiry rejected: %v", err)
		}
	})
	t.Run("prompt snapshots exist independently by task", func(t *testing.T) {
		var snapshots int64
		if err := admin.Model(&AnalysisPromptSnapshot{}).Where("analysis_type='REAL_ESTATE'").Distinct("task").Count(&snapshots).Error; err != nil {
			t.Fatal(err)
		}
		if snapshots < 3 {
			t.Fatalf("task snapshots=%d, want 3", snapshots)
		}
	})
	t.Run("GORM and SQL catalog parity", func(t *testing.T) {
		assertVideoPlanPreparationCatalog(t, admin)
		var currentDefault string
		if err := admin.Raw(`SELECT column_default FROM information_schema.columns WHERE table_schema='media' AND table_name='video_policies' AND column_name='current_retention_days'`).Scan(&currentDefault).Error; err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(currentDefault, "30") {
			t.Fatalf("current_retention_days default=%q, want 30", currentDefault)
		}
		var jobIndex, pairIndex string
		if err := admin.Raw(`SELECT indexdef FROM pg_indexes WHERE schemaname='analysis' AND indexname='uq_inventory_job_input'`).Scan(&jobIndex).Error; err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(jobIndex, "kind") || !strings.Contains(jobIndex, "requirement_key") {
			t.Fatalf("inventory job unique index=%q", jobIndex)
		}
		if err := admin.Raw(`SELECT indexdef FROM pg_indexes WHERE schemaname='analysis' AND indexname='uq_inventory_pair_identity'`).Scan(&pairIndex).Error; err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(pairIndex, "NULLS NOT DISTINCT") {
			t.Fatalf("evidence pair index=%q", pairIndex)
		}
		for _, column := range []struct{ schema, table, name string }{
			{"invitations", "invitations", "reviewer_membership_id"}, {"capture", "video_attempts", "requirement_key"},
			{"media", "video_preparation_jobs", "requirement_key"}, {"analysis", "video_plan_preparations", "requirement_key"},
		} {
			var count int64
			if err := admin.Raw(`SELECT count(*) FROM information_schema.columns WHERE table_schema=? AND table_name=? AND column_name=?`, column.schema, column.table, column.name).Scan(&count).Error; err != nil {
				t.Fatal(err)
			}
			if count != 1 {
				t.Errorf("missing column %s.%s.%s", column.schema, column.table, column.name)
			}
		}
		for _, constraint := range []struct{ table, name string }{
			{"origins.inventory_items", "chk_inventory_item_rejection_reason"}, {"origins.inventory_items", "chk_inventory_item_name"},
			{"origins.inventory_environments", "chk_inventory_environment_name"}, {"analysis.inventory_item_results", "chk_inventory_result_reason"},
			{"analysis.inventory_evidence_pairs", "chk_inventory_pair_origin_frame"}, {"analysis.inventory_evidence_pairs", "chk_inventory_pair_current_frame"},
			{"origins.inventory_items", "chk_inventory_item_rejected_reason"}, {"analysis.findings", "chk_finding_one_job"},
			{"usage.llm_calls", "chk_llm_call_comparison_mode"}, {"invitations.invitations", "fk_invitation_reviewer_membership"},
			{"inspections.inspections", "fk_inspection_inventory_prompt_snapshot"}, {"analysis.inventory_jobs", "fk_inventory_job_prompt_snapshot"},
			{"analysis.inventory_item_results", "fk_inventory_result_inspection"}, {"analysis.inventory_evidence_pairs", "fk_inventory_pair_origin_media"},
		} {
			var count int64
			if err := admin.Raw(`SELECT count(*) FROM pg_constraint c JOIN pg_class r ON r.oid=c.conrelid JOIN pg_namespace n ON n.oid=r.relnamespace WHERE n.nspname||'.'||r.relname=? AND c.conname=?`, constraint.table, constraint.name).Scan(&count).Error; err != nil {
				t.Fatal(err)
			}
			if count != 1 {
				t.Errorf("missing constraint %s on %s", constraint.name, constraint.table)
			}
		}
	})
}

func assertVideoPlanPreparationCatalog(t *testing.T, db *gorm.DB) {
	t.Helper()
	var constraints []string
	if err := db.Raw(`SELECT conname FROM pg_constraint WHERE conrelid='analysis.video_plan_preparations'::regclass AND contype IN ('c','f') ORDER BY conname`).Scan(&constraints).Error; err != nil {
		t.Fatal(err)
	}
	wantConstraints := []string{"video_plan_preparations_role_check", "video_plan_preparations_tenant_id_plan_id_fkey", "video_plan_preparations_tenant_id_preparation_id_fkey"}
	if strings.Join(constraints, ",") != strings.Join(wantConstraints, ",") {
		t.Fatalf("video_plan_preparations constraints=%v, want %v", constraints, wantConstraints)
	}
	var createdAtNotNull bool
	if err := db.Raw(`SELECT is_nullable='NO' FROM information_schema.columns WHERE table_schema='analysis' AND table_name='video_plan_preparations' AND column_name='created_at'`).Scan(&createdAtNotNull).Error; err != nil {
		t.Fatal(err)
	}
	if !createdAtNotNull {
		t.Fatal("video_plan_preparations.created_at must be NOT NULL")
	}
	var updates int64
	if err := db.Raw(`SELECT count(*) FROM information_schema.role_table_grants WHERE table_schema='analysis' AND table_name='video_plan_preparations' AND grantee='inspection_runtime' AND privilege_type='UPDATE'`).Scan(&updates).Error; err != nil {
		t.Fatal(err)
	}
	if updates != 0 {
		t.Fatalf("inspection_runtime has UPDATE on video_plan_preparations (%d grants)", updates)
	}
}

func insertOriginVersion(db *gorm.DB, tenant, origin identity.ID, status string, inventory bool, purpose *string) (identity.ID, error) {
	var versionNumber int
	if err := db.Raw("SELECT coalesce(max(version_number),0)+1 FROM origins.origin_versions WHERE tenant_id=? AND origin_id=?", tenant, origin).Scan(&versionNumber).Error; err != nil {
		return identity.ID{}, err
	}
	versionID := identity.NewID()
	err := db.Exec(`INSERT INTO origins.origin_versions(id,tenant_id,origin_id,version_number,status,media_kind,scope_manifest_digest,idempotency_key,has_inventory,purpose) VALUES(?,?,?,?,?,'PHOTO','',?,?,?)`, versionID, tenant, origin, versionNumber, status, "it-foundation-"+identity.NewID().String(), inventory, purpose).Error
	return versionID, err
}
