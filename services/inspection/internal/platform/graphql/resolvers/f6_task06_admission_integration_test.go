//go:build integration

package resolvers

import (
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/database"
)

func TestVV_IT_241JPEGUploadForVideoRecaptureSupplementIsRejected(t *testing.T) {
	fx := newPhotoUploadFixture(t, "RECAPTURE")
	if err := fx.h.AdminDB.Model(&database.CaptureDraft{}).Where("tenant_id=? AND responsibility_id=?", fx.seed.TenantID, fx.seed.ResponsibilityID).Updates(map[string]any{
		"media_kind":   "VIDEO",
		"requirements": []byte(`[{"key":"video:supplement","label":"Complemento","evidenceKind":"VIDEO","required":true,"minimumCount":1,"maximumCount":1}]`),
	}).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	attemptID := identity.NewID()
	if err := fx.h.AdminDB.Create(&database.VideoAttempt{ID: attemptID, TenantID: fx.seed.TenantID, ResponsibilityID: fx.seed.ResponsibilityID, Kind: "SUPPLEMENT", PolicyVersion: 1, MaxDurationSeconds: 900, MaxBytes: 1 << 30, AcquisitionSource: "CAMERA", Status: "ACTIVE", IdempotencyKey: "vv-it-241-attempt", AdmittedAt: now, ExpiresAt: now.Add(time.Hour), CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	before := fx.mediaCount(t)
	response := fx.upload(t, "image/jpeg", 1024, &attemptID, "vv-it-241-jpeg")
	assertPhotoUploadRejected(t, fx, before, response, "INVALID_INPUT")
}
