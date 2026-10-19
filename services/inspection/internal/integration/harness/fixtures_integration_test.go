//go:build integration

package harness

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"testing"

	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

func TestTask06FixtureSeedsCompleteGraph(t *testing.T) {
	ctx := context.Background()
	h, err := NewFromEnv(ctx)
	if err != nil {
		t.Skipf("integration dependencies unavailable: %v", err)
	}
	defer h.Close()

	fixture, err := h.SeedTask06Fixture(ctx, Task06FixtureOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		var inspection database.Inspection
		if err := tx.First(&inspection, "id = ?", fixture.InspectionID).Error; err != nil {
			return err
		}
		if inspection.Status != "SUBMITTED" || inspection.ProjectID == nil || *inspection.ProjectID != fixture.ProjectID {
			t.Fatalf("unexpected inspection fixture: %+v", inspection)
		}
		var answer database.RequirementAnswer
		if err := tx.First(&answer, "draft_id = ?", fixture.DraftID).Error; err != nil {
			return err
		}
		if answer.RequirementKey != "room-1" {
			t.Fatalf("unexpected requirement answer: %+v", answer)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSeedVideoOriginFixturePersistsReadyActiveFrameScope(t *testing.T) {
	ctx := context.Background()
	h, err := NewFromEnv(ctx)
	if err != nil {
		t.Skipf("integration dependencies unavailable: %v", err)
	}
	defer h.Close()
	f, err := h.SeedVideoOriginFixture(ctx, VideoOriginFixtureOptions{Active: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
		var origin database.Origin
		if err := tx.First(&origin, "id = ?", f.OriginID).Error; err != nil {
			return err
		}
		if origin.ActiveVersionID == nil || *origin.ActiveVersionID != f.OriginVersionID {
			t.Fatalf("origin not active: %+v", origin)
		}
		var version database.OriginVersion
		if err := tx.First(&version, "id = ?", f.OriginVersionID).Error; err != nil {
			return err
		}
		if version.Status != "ACTIVE" || version.MediaKind != "VIDEO" || version.VideoPreparationID == nil || *version.VideoPreparationID != f.PreparationID {
			t.Fatalf("unexpected origin version: %+v", version)
		}
		var preparation database.VideoPreparationJob
		if err := tx.First(&preparation, "id = ?", f.PreparationID).Error; err != nil {
			return err
		}
		if preparation.Status != "READY" || preparation.ManifestDigest == "" {
			t.Fatalf("unexpected preparation: %+v", preparation)
		}
		var frame database.VideoFrame
		if err := tx.First(&frame, "id = ?", f.FrameID).Error; err != nil {
			return err
		}
		var scope int64
		if err := tx.Model(&database.VideoScopeItem{}).Where("tenant_id=? AND preparation_id=? AND frame_id=?", f.TenantID, f.PreparationID, f.FrameID).Count(&scope).Error; err != nil {
			return err
		}
		if scope != 1 {
			t.Fatalf("scope rows=%d, want 1", scope)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var frame database.VideoFrame
	if err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error { return tx.First(&frame, "id = ?", f.FrameID).Error }); err != nil {
		t.Fatal(err)
	}
	object, err := h.Store.Client.Get(ctx, h.Store.Bucket, frame.ObjectKey)
	if err != nil {
		t.Fatal(err)
	}
	defer object.Close()
	data, err := io.ReadAll(object)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != frame.SHA256 {
		t.Fatalf("MinIO frame digest mismatch: %s", frame.ObjectKey)
	}
}
