//go:build integration

package expire_video_sources

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/integration/harness"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/objectstore"

	"gorm.io/gorm"
)

// TestInventoryOriginPurgeWithoutResponsibilityIT047 seeds inventory origin
// versions without responsibility in every lifecycle state and checks that the
// origin-version purge removes only those past their window and unprotected,
// and that scheduler discovery reports the tenant exactly while that work is due.
func TestInventoryOriginPurgeWithoutResponsibilityIT047(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	fixture, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	recent, old, beyondSuperseded := now.AddDate(0, 0, -10), now.AddDate(0, 0, -60), now.AddDate(0, 0, -400)
	type versionCase struct {
		name, status string
		at           time.Time
		purged       bool
		contested    bool
		scheduled    bool
		versionHold  bool
		sourceHold   bool
		id, itemID   identity.ID
	}
	protected := []versionCase{
		{name: "ACTIVE version protected", status: "ACTIVE", at: beyondSuperseded},
		{name: "SUPERSEDED inside the 365-day window kept", status: "SUPERSEDED", at: recent},
		{name: "SUPERSEDED past window with open contestation kept", status: "SUPERSEDED", at: beyondSuperseded, contested: true},
		{name: "SUPERSEDED past window with legal hold kept", status: "SUPERSEDED", at: beyondSuperseded, versionHold: true},
		{name: "SUPERSEDED past window pinned by ACTIVE schedule kept", status: "SUPERSEDED", at: beyondSuperseded, scheduled: true},
		{name: "SUPERSEDED past window citing a held source kept", status: "SUPERSEDED", at: beyondSuperseded, sourceHold: true},
		{name: "DISCARDED inside the 30-day window kept", status: "DISCARDED", at: recent},
	}
	due := []versionCase{
		{name: "SUPERSEDED past window purged", status: "SUPERSEDED", at: beyondSuperseded, purged: true},
		{name: "DISCARDED past window purged", status: "DISCARDED", at: old, purged: true},
		{name: "INVALIDATED past window purged", status: "INVALIDATED", at: old, purged: true},
	}
	originID := identity.NewID()
	versionNumber := 0
	digest := strings.Repeat("f", 64)
	seed := func(cases []versionCase) {
		t.Helper()
		if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
			if versionNumber == 0 {
				if err := tx.Create(&database.Origin{ID: originID, TenantID: fixture.TenantID, AssetID: fixture.AssetID, Version: 1, CreatedAt: old, UpdatedAt: old}).Error; err != nil {
					return err
				}
			}
			for i := range cases {
				c := &cases[i]
				versionNumber++
				c.id, c.itemID = identity.NewID(), identity.NewID()
				purpose := "MOVE_IN"
				at := c.at
				version := database.OriginVersion{ID: c.id, TenantID: fixture.TenantID, OriginID: originID, VersionNumber: versionNumber, HasInventory: true, Purpose: &purpose, Status: c.status, MediaKind: "PHOTO", IdempotencyKey: "it047-" + c.id.String(), CreatedAt: at.Add(-time.Hour), ActivatedAt: &at}
				switch c.status {
				case "SUPERSEDED":
					version.SupersededAt = &at
				case "INVALIDATED":
					version.InvalidatedAt = &at
				case "DISCARDED":
					version.Purpose, version.ActivatedAt, version.DiscardedAt = nil, nil, &at
				}
				if err := tx.Create(&version).Error; err != nil {
					return err
				}
				if c.status == "ACTIVE" {
					if err := tx.Model(&database.Origin{}).Where("tenant_id=? AND id=?", fixture.TenantID, originID).Update("active_version_id", c.id).Error; err != nil {
						return err
					}
				}
				environmentID := identity.NewID()
				if err := tx.Create(&database.InventoryEnvironment{ID: environmentID, TenantID: fixture.TenantID, OriginVersionID: c.id, IdentityID: identity.NewID(), TypeCode: "KITCHEN", Name: "Cozinha", Decision: "ACCEPTED", Version: 1, CreatedAt: at, UpdatedAt: at}).Error; err != nil {
					return err
				}
				if err := tx.Create(&database.InventoryItem{ID: c.itemID, TenantID: fixture.TenantID, OriginVersionID: c.id, EnvironmentID: environmentID, IdentityID: identity.NewID(), TypeCode: "SINK", Name: c.status, Condition: "GOOD", Observation: "OBSERVED", Decision: "ACCEPTED", CreatedAt: at, UpdatedAt: at}).Error; err != nil {
					return err
				}
				if c.scheduled {
					future := now.AddDate(1, 0, 0)
					if err := tx.Create(&database.Schedule{ID: identity.NewID(), TenantID: fixture.TenantID, BusinessUnitID: fixture.BusinessUnitID, AssetID: fixture.AssetID, ParticipantID: fixture.ParticipantID, TemplateID: fixture.TemplateID, ReferenceVersionID: &c.id, ComparisonMode: "INVENTORY", RRule: "FREQ=DAILY", Timezone: "UTC", StartsAt: future, NextDueAt: future, DeadlineMinutes: 60, ReminderOffsets: json.RawMessage("[]"), Status: "ACTIVE", IdempotencyKey: "it047-schedule-" + c.id.String(), CreatedAt: now, UpdatedAt: now}).Error; err != nil {
						return err
					}
				}
				evidence := database.InventoryItemEvidence{ID: identity.NewID(), TenantID: fixture.TenantID, ItemID: c.itemID, Kind: "PHOTO", MediaID: fixture.MediaID, CreatedAt: at}
				if c.sourceHold {
					mediaID, preparationID, frameID := identity.NewID(), identity.NewID(), identity.NewID()
					if err := tx.Create(&database.MediaObject{ID: mediaID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, ObjectKey: "it047/" + mediaID.String(), ContentType: "video/mp4", SHA256: digest, SizeBytes: 1, Status: "READY", MediaKind: "VIDEO", IdempotencyKey: "it047-" + mediaID.String(), AttentionItems: []byte(`[]`), Flags: []byte(`[]`), CreatedAt: at}).Error; err != nil {
						return err
					}
					if err := tx.Create(&database.VideoPreparationJob{ID: preparationID, TenantID: fixture.TenantID, MediaID: mediaID, Status: "READY", Phase: "READY", PreparationVersion: 1, Generation: 1, NextAttemptAt: at, SourceSHA256: digest, ManifestDigest: digest, PendingObjectKeys: []byte(`[]`), CreatedAt: at}).Error; err != nil {
						return err
					}
					if err := tx.Create(&database.VideoSource{ID: identity.NewID(), TenantID: fixture.TenantID, MediaID: mediaID, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 1, Width: 2, Height: 2, FrameRate: 1, ScreeningStatus: "APPROVED", RetentionAnchor: "FIXED", Status: "RETAINED", CreatedAt: at, UpdatedAt: at}).Error; err != nil {
						return err
					}
					if err := tx.Create(&database.VideoFrame{ID: frameID, TenantID: fixture.TenantID, SourceMediaID: mediaID, PreparationID: preparationID, SHA256: digest, ObjectKey: "it047/frames/" + frameID.String(), Width: 2, Height: 2, CreatedAt: at}).Error; err != nil {
						return err
					}
					if err := tx.Create(&database.LegalHold{ID: identity.NewID(), TenantID: fixture.TenantID, SourceMediaID: &mediaID, Reason: "IT-047 source", Active: true, CreatedAt: now}).Error; err != nil {
						return err
					}
					evidence.Kind, evidence.MediaID, evidence.FrameID = "VIDEO_FRAME", mediaID, &frameID
				}
				if err := tx.Create(&evidence).Error; err != nil {
					return err
				}
				if c.contested {
					if err := tx.Create(&database.CounterpartyInvitation{ID: identity.NewID(), TenantID: fixture.TenantID, OriginVersionID: c.id, ParticipantID: fixture.ParticipantID, TokenHash: []byte("it047-" + c.id.String()), Channels: []byte(`[]`), DeadlineAt: now, Response: "CONTESTED", Resolution: "OPEN", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
						return err
					}
				}
				if c.versionHold {
					if err := tx.Create(&database.LegalHold{ID: identity.NewID(), TenantID: fixture.TenantID, OriginVersionID: &c.id, Reason: "IT-047 version", Active: true, CreatedAt: now}).Error; err != nil {
						return err
					}
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	// Other discovery branches (inspection deadlines and terminal age) are
	// pinned far in the past so only retention work can select this tenant.
	discovered := func() bool {
		t.Helper()
		var tenants []identity.ID
		distantPast := now.AddDate(-10, 0, 0)
		if err := h.DB.WithContext(ctx).Raw(`SELECT * FROM platform.discover_scheduler_tenants(?,?,?)`, now, distantPast, distantPast).Scan(&tenants).Error; err != nil {
			t.Fatal(err)
		}
		for _, tenant := range tenants {
			if tenant == fixture.TenantID {
				return true
			}
		}
		return false
	}
	executor, err := Setup(Dependencies{DB: h.DB, Store: h.Store, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	seed(protected)
	if discovered() {
		t.Fatal("IT-047 scheduler discovered the tenant although only protected or in-window versions exist")
	}
	if count, err := executor.PurgeInactiveOriginBanks(ctx, fixture.TenantID, 10); err != nil || count != 0 {
		t.Fatalf("IT-047 purge without due work = %d, %v; want 0", count, err)
	}
	seed(due)
	if !discovered() {
		t.Fatal("IT-047 scheduler did not discover the tenant with due inventory purge work")
	}
	if count, err := executor.PurgeInactiveOriginBanks(ctx, fixture.TenantID, 10); err != nil || count != len(due) {
		t.Fatalf("IT-047 origin-version evidence purge = %d, %v; want %d due versions", count, err, len(due))
	}
	if discovered() {
		t.Fatal("IT-047 scheduler still discovers the tenant after the due versions were purged")
	}
	for _, c := range append(protected, due...) {
		t.Run("IT-047 "+c.name, func(t *testing.T) {
			var items, evidence, versions int64
			if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
				if err := tx.Model(&database.InventoryItem{}).Where("tenant_id=? AND origin_version_id=?", fixture.TenantID, c.id).Count(&items).Error; err != nil {
					return err
				}
				if err := tx.Model(&database.InventoryItemEvidence{}).Where("tenant_id=? AND item_id=?", fixture.TenantID, c.itemID).Count(&evidence).Error; err != nil {
					return err
				}
				return tx.Model(&database.OriginVersion{}).Where("tenant_id=? AND id=?", fixture.TenantID, c.id).Count(&versions).Error
			}); err != nil {
				t.Fatal(err)
			}
			if versions != 1 {
				t.Fatalf("version row removed (count=%d); the purge must keep the version for the audit trail", versions)
			}
			if c.purged && (items != 0 || evidence != 0) {
				t.Fatalf("items/evidence=%d/%d; want purged", items, evidence)
			}
			if !c.purged && (items != 1 || evidence != 1) {
				t.Fatalf("items/evidence=%d/%d; want kept", items, evidence)
			}
		})
	}
}

func TestInactiveOriginPurgePreservesFrameBanksReferencedByRetainedEvidence(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	fixture, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	type bank struct {
		mediaID, preparationID, versionID, responsibilityID identity.ID
		key, ownerType                                      string
	}
	banks := []bank{
		{mediaID: identity.NewID(), preparationID: identity.NewID(), versionID: identity.NewID(), responsibilityID: identity.NewID(), key: "inactive-origin-purge/report.jpg", ownerType: "REPORT_SNAPSHOT"},
		{mediaID: identity.NewID(), preparationID: identity.NewID(), versionID: identity.NewID(), responsibilityID: identity.NewID(), key: "inactive-origin-purge/plan.jpg", ownerType: "VIDEO_PLAN"},
		{mediaID: identity.NewID(), preparationID: identity.NewID(), versionID: identity.NewID(), responsibilityID: identity.NewID(), key: "inactive-origin-purge/inventory.jpg", ownerType: "INVENTORY"},
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	for i := range banks {
		banks[i].key = "inactive-origin-purge/" + banks[i].preparationID.String() + ".jpg"
		key := banks[i].key
		if err := h.Store.Client.Put(ctx, h.Store.Bucket, key, strings.NewReader("frame"), 5, "image/jpeg"); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = h.Store.Delete(context.Background(), key) })
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		originID := identity.NewID()
		if err := tx.Create(&database.Origin{ID: originID, TenantID: fixture.TenantID, AssetID: fixture.AssetID, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		for _, item := range banks {
			if err := tx.Create(&database.Responsibility{ID: item.responsibilityID, TenantID: fixture.TenantID, InspectionID: fixture.InspectionID, ParticipantID: fixture.ParticipantID, Status: "EXPIRED", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
				return err
			}
			if err := tx.Create(&database.MediaObject{ID: item.mediaID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, ObjectKey: "inactive-origin-purge/source-" + item.mediaID.String(), ContentType: "video/mp4", SHA256: strings.Repeat("b", 64), SizeBytes: 5, Status: "READY", MediaKind: "VIDEO", IdempotencyKey: "inactive-origin-media-" + item.mediaID.String(), RequirementKey: "video:main", AttentionItems: []byte(`[]`), Flags: []byte(`[]`), CaptureSource: "CAMERA", CreatedAt: now}).Error; err != nil {
				return err
			}
			if err := tx.Create(&database.VideoPreparationJob{ID: item.preparationID, TenantID: fixture.TenantID, MediaID: item.mediaID, Status: "READY", Phase: "READY", PreparationVersion: 1, Generation: 1, NextAttemptAt: now, SourceSHA256: strings.Repeat("b", 64), ManifestDigest: strings.Repeat("b", 64), PendingObjectKeys: []byte(`[]`), CreatedAt: now}).Error; err != nil {
				return err
			}
			versionNumber := 1
			if item.ownerType == "VIDEO_PLAN" {
				versionNumber = 2
			} else if item.ownerType == "INVENTORY" {
				versionNumber = 3
			}
			if err := tx.Create(&database.OriginVersion{ID: item.versionID, TenantID: fixture.TenantID, OriginID: originID, VersionNumber: versionNumber, ResponsibilityID: &item.responsibilityID, Status: "INVALIDATED", MediaKind: "VIDEO", VideoPreparationID: &item.preparationID, IdempotencyKey: "inactive-origin-ref-" + item.versionID.String(), CreatedAt: now}).Error; err != nil {
				return err
			}
			frame := database.VideoFrame{ID: identity.NewID(), TenantID: fixture.TenantID, SourceMediaID: item.mediaID, PreparationID: item.preparationID, PTSMicros: 0, SHA256: strings.Repeat("b", 64), ObjectKey: item.key, Width: 2, Height: 2, Role: "BASELINE", SizeBytes: 5, CreatedAt: now}
			if err := tx.Create(&frame).Error; err != nil {
				return err
			}
			if item.ownerType == "INVENTORY" {
				// A legacy bank and an inventory baseline share this preparation.
				// The inventory version remains inside its 365-day window, with an
				// open contestation and an inspection-level hold.
				inventoryVersionID := identity.NewID()
				started := now.AddDate(0, 0, -30)
				purpose := "MOVE_IN"
				if err := tx.Create(&database.OriginVersion{ID: inventoryVersionID, TenantID: fixture.TenantID, OriginID: originID, VersionNumber: 4, HasInventory: true, Purpose: &purpose, Status: "SUPERSEDED", MediaKind: "PHOTO", SupersededAt: &started, ActivatedAt: &started, IdempotencyKey: "inactive-origin-inventory-" + inventoryVersionID.String(), CreatedAt: started}).Error; err != nil {
					return err
				}
				environmentID, itemID := identity.NewID(), identity.NewID()
				if err := tx.Create(&database.InventoryEnvironment{ID: environmentID, TenantID: fixture.TenantID, OriginVersionID: inventoryVersionID, IdentityID: identity.NewID(), TypeCode: "KITCHEN", Name: "Kitchen", Decision: "ACCEPTED", Version: 1, CreatedAt: started, UpdatedAt: started}).Error; err != nil {
					return err
				}
				if err := tx.Create(&database.InventoryItem{ID: itemID, TenantID: fixture.TenantID, OriginVersionID: inventoryVersionID, EnvironmentID: environmentID, IdentityID: identity.NewID(), TypeCode: "SINK", Name: "Sink", Condition: "GOOD", Observation: "OBSERVED", Decision: "ACCEPTED", CreatedAt: started, UpdatedAt: started}).Error; err != nil {
					return err
				}
				if err := tx.Create(&database.InventoryItemEvidence{ID: identity.NewID(), TenantID: fixture.TenantID, ItemID: itemID, Kind: "VIDEO_FRAME", MediaID: item.mediaID, FrameID: &frame.ID, CreatedAt: started}).Error; err != nil {
					return err
				}
				if err := tx.Create(&database.CounterpartyInvitation{ID: identity.NewID(), TenantID: fixture.TenantID, OriginVersionID: inventoryVersionID, ParticipantID: fixture.ParticipantID, TokenHash: []byte("inactive-origin-contested-" + inventoryVersionID.String()), Channels: []byte(`[]`), DeadlineAt: now, Response: "CONTESTED", Resolution: "OPEN", CreatedAt: started, UpdatedAt: started}).Error; err != nil {
					return err
				}
				if err := tx.Create(&database.LegalHold{ID: identity.NewID(), TenantID: fixture.TenantID, InspectionID: &fixture.InspectionID, Reason: "inventory frame hold", Active: true, CreatedAt: now}).Error; err != nil {
					return err
				}
			}
			if item.ownerType != "INVENTORY" {
				if err := tx.Create(&database.VideoEvidenceReference{ID: identity.NewID(), TenantID: fixture.TenantID, PreparationID: item.preparationID, OwnerType: item.ownerType, OwnerID: identity.NewID(), CreatedAt: now}).Error; err != nil {
					return err
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	executor, err := Setup(Dependencies{DB: h.DB, Store: h.Store})
	if err != nil {
		t.Fatal(err)
	}
	count, err := executor.PurgeInactiveOriginBanks(ctx, fixture.TenantID, 10)
	if err != nil || count != 0 {
		t.Fatalf("PurgeInactiveOriginBanks() = %d, %v; want 0", count, err)
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		for _, item := range banks {
			var frames int64
			if err := tx.Model(&database.VideoFrame{}).Where("tenant_id=? AND preparation_id=?", fixture.TenantID, item.preparationID).Count(&frames).Error; err != nil {
				return err
			}
			if frames != 1 {
				t.Errorf("%s frame rows=%d, want 1", item.ownerType, frames)
			}
			reader, _, err := (objectstore.VideoStore{Store: h.Store}).Open(ctx, item.key)
			if err != nil {
				return err
			}
			if err := reader.Close(); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestInactiveOriginPurgeDeletesUnreferencedFrameBank(t *testing.T) {
	if os.Getenv("INSPECTION_TEST_DATABASE_URL") == "" || os.Getenv("INSPECTION_TEST_RUNTIME_DATABASE_URL") == "" {
		t.Skip("isolated PostgreSQL and runtime-role URLs are required")
	}
	ctx := context.Background()
	h, err := harness.NewFromEnv(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	fixture, err := h.SeedTask06Fixture(ctx, harness.Task06FixtureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	mediaID, preparationID, originID, versionID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	frameKey := "inactive-origin-purge/expired/" + identity.NewID().String() + ".jpg"
	if err := h.Store.Client.Put(ctx, h.Store.Bucket, frameKey, strings.NewReader("frame"), 5, "image/jpeg"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Store.Delete(context.Background(), frameKey) })
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		if err := tx.Create(&database.Origin{ID: originID, TenantID: fixture.TenantID, AssetID: fixture.AssetID, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.MediaObject{ID: mediaID, TenantID: fixture.TenantID, ResponsibilityID: fixture.ResponsibilityID, ObjectKey: "inactive-origin-purge/source/" + mediaID.String(), ContentType: "video/mp4", SHA256: strings.Repeat("c", 64), SizeBytes: 5, Status: "READY", MediaKind: "VIDEO", IdempotencyKey: "inactive-origin-expired-" + mediaID.String(), RequirementKey: "video:main", Flags: []byte(`[]`), CaptureSource: "CAMERA", CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPreparationJob{ID: preparationID, TenantID: fixture.TenantID, MediaID: mediaID, Status: "READY", Phase: "READY", PreparationVersion: 1, Generation: 1, NextAttemptAt: now, SourceSHA256: strings.Repeat("c", 64), ManifestDigest: strings.Repeat("c", 64), PendingObjectKeys: []byte(`[]`), CreatedAt: now}).Error; err != nil {
			return err
		}
		old := now.AddDate(0, 0, -400)
		return tx.Create(&database.OriginVersion{ID: versionID, TenantID: fixture.TenantID, OriginID: originID, VersionNumber: 1, Status: "SUPERSEDED", MediaKind: "VIDEO", VideoPreparationID: &preparationID, SupersededAt: &old, IdempotencyKey: "inactive-origin-expired-" + versionID.String(), CreatedAt: old}).Error
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		return tx.Create(&database.VideoFrame{ID: identity.NewID(), TenantID: fixture.TenantID, SourceMediaID: mediaID, PreparationID: preparationID, PTSMicros: 0, SHA256: strings.Repeat("c", 64), ObjectKey: frameKey, Width: 2, Height: 2, Role: "BASELINE", SizeBytes: 5, CreatedAt: now}).Error
	}); err != nil {
		t.Fatal(err)
	}
	executor, err := Setup(Dependencies{DB: h.DB, Store: h.Store, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	var tenants []identity.ID
	distantPast := now.AddDate(-10, 0, 0)
	if err := h.DB.WithContext(ctx).Raw(`SELECT * FROM platform.discover_scheduler_tenants(?,?,?)`, now, distantPast, distantPast).Scan(&tenants).Error; err != nil {
		t.Fatal(err)
	}
	discovered := false
	for _, tenantID := range tenants {
		discovered = discovered || tenantID == fixture.TenantID
	}
	if !discovered {
		t.Fatal("scheduler did not discover tenant with due legacy frame-bank purge work")
	}
	count, err := executor.PurgeInactiveOriginBanks(ctx, fixture.TenantID, 10)
	if err != nil || count != 1 {
		t.Fatalf("IT-047 unreferenced frame-bank purge = %d, %v; want one purged preparation", count, err)
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		var frames int64
		if err := tx.Model(&database.VideoFrame{}).Where("tenant_id=? AND preparation_id=?", fixture.TenantID, preparationID).Count(&frames).Error; err != nil {
			return err
		}
		if frames != 0 {
			t.Errorf("IT-047 expired preparation retained %d frames", frames)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.DB.WithContext(ctx).Raw(`SELECT * FROM platform.discover_scheduler_tenants(?,?,?)`, now, distantPast, distantPast).Scan(&tenants).Error; err != nil {
		t.Fatal(err)
	}
	for _, tenantID := range tenants {
		if tenantID == fixture.TenantID {
			t.Fatal("scheduler still discovers tenant after legacy frame-bank purge")
		}
	}
}
