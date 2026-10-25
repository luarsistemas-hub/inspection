package core

import (
	"context"
	"testing"
	"time"

	"inspection/libs/identity"
	inventorycore "inspection/services/inspection/internal/features/inventory/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

func newViewFixture(t *testing.T, deadline time.Time) (*counterpartyFixture, SessionOwner) {
	t.Helper()
	f := newCounterpartyFixture(t, 1)
	within := func(_ context.Context, _ identity.ID, fn func(*gorm.DB) error) error { return fn(f.db) }
	f.service.Inventory = inventorycore.Service{DB: f.db, Within: within}
	row := seedResponseInvitation(t, f.db, f.tenantID, f.versionID, f.participants[0], deadline)
	return f, SessionOwner{TenantID: f.tenantID, InvitationID: row.ID}
}

func TestUnit_UT294_ViewAfterDeadlineIsReadOnly(t *testing.T) {
	f, owner := newViewFixture(t, time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC))
	*f.clock = time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC).Add(-time.Millisecond)
	view, err := f.service.View(context.Background(), owner)
	if err != nil || view.ReadOnly {
		t.Fatalf("UT-294 view before deadline readOnly=%v err=%v", view.ReadOnly, err)
	}
	*f.clock = f.clock.Add(time.Millisecond)
	view, err = f.service.View(context.Background(), owner)
	if err != nil || !view.ReadOnly || view.Invitation.Response != "PENDING" || !view.Invitation.DeadlineAt.Equal(*f.clock) {
		t.Fatalf("UT-294 view at deadline=%+v err=%v, want readOnly with deadline", view.Invitation, err)
	}
	_, err = f.service.Respond(context.Background(), owner, "ACKNOWLEDGED", "", "late")
	assertAppError(t, err, apperror.DeadlinePassed, "deadlineAt")
}

func TestUnit_UT294_ViewAfterResponseIsReadOnly(t *testing.T) {
	f, owner := newViewFixture(t, time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC))
	if _, err := f.service.Respond(context.Background(), owner, "ACKNOWLEDGED", "", "ack"); err != nil {
		t.Fatal(err)
	}
	view, err := f.service.View(context.Background(), owner)
	if err != nil || !view.ReadOnly || view.Invitation.Response != "ACKNOWLEDGED" || view.Invitation.RespondedAt == nil {
		t.Fatalf("view after acknowledgment=%+v err=%v", view.Invitation, err)
	}
}

func TestUnit_UT295_ViewReportsNewerVersion(t *testing.T) {
	f, owner := newViewFixture(t, time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC))
	view, err := f.service.View(context.Background(), owner)
	if err != nil || view.Newer {
		t.Fatalf("UT-295 active version newer=%v err=%v", view.Newer, err)
	}
	var origin database.Origin
	if err := f.db.Where("tenant_id=? AND asset_id=?", f.tenantID, f.assetID).First(&origin).Error; err != nil {
		t.Fatal(err)
	}
	newerVersionID := identity.NewID()
	if err := f.db.Model(&database.OriginVersion{}).Where("id=?", f.versionID).Update("status", "SUPERSEDED").Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Exec(`INSERT INTO origins.origin_versions(id,tenant_id,origin_id,status,has_inventory,purpose) VALUES(?,?,?,?,?,?)`, newerVersionID, f.tenantID, origin.ID, "ACTIVE", true, "MOVE_IN").Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Exec("UPDATE origins.origins SET active_version_id=? WHERE id=?", newerVersionID, origin.ID).Error; err != nil {
		t.Fatal(err)
	}
	view, err = f.service.View(context.Background(), owner)
	if err != nil || !view.Newer || view.Inventory.Version == nil || view.Inventory.Version.ID != f.versionID {
		t.Fatalf("UT-295 superseded view newer=%v inventory=%+v err=%v, want the contested version with newerVersionExists", view.Newer, view.Inventory.Version, err)
	}
}

func TestUnit_UT305_ViewShowsDeclinedResolutionAndReason(t *testing.T) {
	f, owner := newViewFixture(t, time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC))
	if _, err := f.service.Respond(context.Background(), owner, "CONTESTED", "O sofá está rasgado.", "contest"); err != nil {
		t.Fatal(err)
	}
	view, err := f.service.View(context.Background(), owner)
	if err != nil || view.Invitation.Resolution != "OPEN" || view.Invitation.ResolutionReason != nil {
		t.Fatalf("UT-305 open contestation view=%+v err=%v", view.Invitation, err)
	}
	if _, err := f.service.Resolve(context.Background(), f.tenantID, owner.InvitationID, identity.NewID(), "DECLINE", "Registro fotográfico confirma o estado."); err != nil {
		t.Fatal(err)
	}
	view, err = f.service.View(context.Background(), owner)
	if err != nil || view.Invitation.Response != "CONTESTED" || view.Invitation.Resolution != "DECLINED" || view.Invitation.ResolutionReason == nil || *view.Invitation.ResolutionReason != "Registro fotográfico confirma o estado." || view.Invitation.AdjustmentVersionID != nil || view.AdjustmentStatus != "" {
		t.Fatalf("UT-305 declined view=%+v adjustment=%q err=%v", view.Invitation, view.AdjustmentStatus, err)
	}
}

func TestUnit_ViewUnknownInvitationIsNotFound(t *testing.T) {
	f, _ := newViewFixture(t, time.Now().UTC())
	_, err := f.service.View(context.Background(), SessionOwner{TenantID: f.tenantID, InvitationID: identity.NewID()})
	assertAppError(t, err, apperror.NotFound, "counterpartyInvitationId")
}
