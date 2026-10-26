package resolvers

import (
	"context"
	"strings"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/objectstore"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func counterpartyMediaDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	// Attached in-memory schemas belong to one connection.
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.Exec("ATTACH DATABASE ':memory:' AS media").Error; err != nil {
		t.Fatal(err)
	}
	for _, model := range []any{&database.MediaDerivative{}, &database.VideoFrame{}, &database.VideoSource{}, &database.VideoPreparationJob{}} {
		// AutoMigrate cannot create indexes inside attached schemas.
		if err := db.AutoMigrate(model); err != nil && !strings.Contains(err.Error(), "no such table: main.") {
			t.Fatalf("migrate %T: %v", model, err)
		}
	}
	return db
}

func TestSignCounterpartyEvidenceSignsOnlyReadyEvidenceOfTheVersion(t *testing.T) {
	db := counterpartyMediaDB(t)
	tenant, otherTenant := identity.NewID(), identity.NewID()
	photo, foreignPhoto, video, otherVideo := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	ready, pending := identity.NewID(), identity.NewID()
	readyFrame, pendingFrame, otherSourceFrame := identity.NewID(), identity.NewID(), identity.NewID()
	rows := []any{
		&database.MediaDerivative{ID: identity.NewID(), TenantID: tenant, MediaID: photo, ObjectKey: "tenant/photo-display.jpg", Kind: "DISPLAY", SHA256: "a"},
		&database.MediaDerivative{ID: identity.NewID(), TenantID: otherTenant, MediaID: foreignPhoto, ObjectKey: "tenant/foreign-display.jpg", Kind: "DISPLAY", SHA256: "b"},
		&database.VideoSource{ID: identity.NewID(), TenantID: tenant, MediaID: video, ScreeningStatus: "APPROVED"},
		&database.VideoSource{ID: identity.NewID(), TenantID: tenant, MediaID: otherVideo, ScreeningStatus: "APPROVED"},
		&database.VideoPreparationJob{ID: ready, TenantID: tenant, MediaID: video, Status: "READY"},
		&database.VideoPreparationJob{ID: pending, TenantID: tenant, MediaID: video, Status: "PROCESSING"},
		&database.VideoFrame{ID: readyFrame, TenantID: tenant, SourceMediaID: video, PreparationID: ready, PTSMicros: 2_500_000, ObjectKey: "tenant/frame-ready.jpg", SHA256: "c"},
		&database.VideoFrame{ID: pendingFrame, TenantID: tenant, SourceMediaID: video, PreparationID: pending, PTSMicros: 1, ObjectKey: "tenant/frame-pending.jpg", SHA256: "d"},
		&database.VideoFrame{ID: otherSourceFrame, TenantID: tenant, SourceMediaID: otherVideo, PreparationID: ready, PTSMicros: 2, ObjectKey: "tenant/frame-other.jpg", SHA256: "e"},
	}
	for _, row := range rows {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	evidence := []database.InventoryItemEvidence{
		{ID: identity.NewID(), Kind: "PHOTO", MediaID: photo},
		{ID: identity.NewID(), Kind: "PHOTO", MediaID: foreignPhoto},
		{ID: identity.NewID(), Kind: "VIDEO_FRAME", MediaID: video, FrameID: &readyFrame},
		{ID: identity.NewID(), Kind: "VIDEO_FRAME", MediaID: video, FrameID: &pendingFrame},
		{ID: identity.NewID(), Kind: "VIDEO_FRAME", MediaID: video, FrameID: &otherSourceFrame},
	}
	media := signCounterpartyEvidence(context.Background(), db, objectstore.Store{Bucket: "private", Client: reportMediaClient{}}, tenant, evidence)
	if got := media[evidence[0].ID]; got.url != "https://signed.invalid/photo-display.jpg" || got.timestampMs != nil {
		t.Fatalf("photo = %+v", got)
	}
	if got := media[evidence[2].ID]; got.url != "https://signed.invalid/frame-ready.jpg" || got.timestampMs == nil || *got.timestampMs != 2500 {
		t.Fatalf("ready frame = %+v", got)
	}
	for _, index := range []int{1, 3, 4} {
		if got, ok := media[evidence[index].ID]; ok {
			t.Fatalf("evidence %d must not be signed: %+v", index, got)
		}
	}
}

func TestSignCounterpartyEvidenceWithoutStoreLeavesEvidenceUnsigned(t *testing.T) {
	media := signCounterpartyEvidence(context.Background(), counterpartyMediaDB(t), objectstore.Store{}, identity.NewID(), []database.InventoryItemEvidence{{ID: identity.NewID(), Kind: "PHOTO", MediaID: identity.NewID()}})
	if len(media) != 0 {
		t.Fatalf("media = %+v, want no URLs without an object store", media)
	}
}
