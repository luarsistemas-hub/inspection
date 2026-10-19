//go:build integration

package harness

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	capturecore "inspection/services/inspection/internal/features/capture/core"
	invitationcore "inspection/services/inspection/internal/features/invitations/core"
	recapturecore "inspection/services/inspection/internal/features/recapture/core"
	"inspection/services/inspection/internal/platform/database"
)

func TestVV_IT_365PhotoRecaptureAfterCompletionKeepsPhotoRequirementsAndLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	h, err := NewFromEnv(ctx)
	if err != nil {
		t.Skipf("integration dependencies unavailable: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })
	fixture, err := h.SeedTask06Fixture(ctx, Task06FixtureOptions{InspectionStatus: "COMPLETED"})
	if err != nil {
		t.Fatal(err)
	}
	original := []capturecore.Requirement{{Key: "room-1", Section: "Sala", Label: "Parede da sala", Instructions: "Fotografe a parede inteira", EvidenceKind: "PHOTO", Required: true, MinimumMedia: 1, MaximumMedia: 2, CaptureSourcePolicy: "CAMERA_ONLY", ComparisonTarget: "CHECKLIST_ONLY"}}
	requirements, _ := json.Marshal(original)
	if err := h.AdminDB.Model(&database.CaptureDraft{}).Where("tenant_id=? AND id=?", fixture.TenantID, fixture.DraftID).Updates(map[string]any{"requirements": requirements, "media_kind": "PHOTO", "status": "SUBMITTED"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.AdminDB.Model(&database.MediaObject{}).Where("tenant_id=? AND id=?", fixture.TenantID, fixture.MediaID).Updates(map[string]any{"status": "READY", "requirement_key": "room-1"}).Error; err != nil {
		t.Fatal(err)
	}
	result, err := (recapturecore.Service{DB: h.DB}).Request(ctx, recapturecore.RequestInput{TenantID: fixture.TenantID, InspectionID: fixture.InspectionID, Items: []recapturecore.Item{{RequirementKey: "room-1", OriginalMediaID: &fixture.MediaID, Reason: "Foto desfocada"}}, Delivery: []invitationcore.DeliveryIntent{{Channel: "EMAIL", Destination: "photo@example.test", Verified: true}}, DeadlineAt: time.Now().UTC().Add(24 * time.Hour), IdempotencyKey: "vv-it-365"})
	if err != nil {
		t.Fatalf("permitted photo recapture after completion: %v", err)
	}
	var request database.RecaptureRequest
	if err := h.AdminDB.Where("tenant_id=? AND id=?", fixture.TenantID, result.RequestID).Take(&request).Error; err != nil {
		t.Fatal(err)
	}
	var draft database.CaptureDraft
	if err := h.AdminDB.Where("tenant_id=? AND responsibility_id=? AND kind='RECAPTURE'", fixture.TenantID, result.ResponsibilityID).Take(&draft).Error; err != nil {
		t.Fatal(err)
	}
	var focused []capturecore.Requirement
	if err := json.Unmarshal(draft.Requirements, &focused); err != nil {
		t.Fatal(err)
	}
	if request.Status != "REQUESTED" || draft.MediaKind != "PHOTO" || draft.Status != "OPEN" || len(focused) != 1 {
		t.Fatalf("photo recapture request=%s draft media=%s status=%s requirements=%d; want REQUESTED, PHOTO, OPEN, 1", request.Status, draft.MediaKind, draft.Status, len(focused))
	}
	got, want := focused[0], original[0]
	if got.Key != want.Key || got.EvidenceKind != "PHOTO" || got.Label != want.Label || got.Section != want.Section || got.Required != want.Required || got.MinimumMedia != want.MinimumMedia || got.MaximumMedia != want.MaximumMedia || got.CaptureSourcePolicy != want.CaptureSourcePolicy || !strings.HasPrefix(got.Instructions, want.Instructions) || !strings.Contains(got.Instructions, "Foto desfocada") {
		t.Fatalf("photo requirement was not preserved: got %+v want %+v plus the recapture reason", got, want)
	}
	if strings.Contains(string(draft.ReferencePayload), "videoRecapture") || strings.Contains(string(draft.Requirements), "video:") {
		t.Fatalf("photo recapture received a video projection: %s %s", draft.ReferencePayload, draft.Requirements)
	}
	var invitations, recaptureRequirements int64
	if err := h.AdminDB.Model(&database.Invitation{}).Where("tenant_id=? AND responsibility_id=?", fixture.TenantID, result.ResponsibilityID).Count(&invitations).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.AdminDB.Model(&database.RecaptureRequirement{}).Where("tenant_id=? AND request_id=? AND requirement_key='room-1'", fixture.TenantID, result.RequestID).Count(&recaptureRequirements).Error; err != nil {
		t.Fatal(err)
	}
	if invitations != 1 || recaptureRequirements != 1 {
		t.Fatalf("photo recapture lifecycle invitations=%d requirements=%d; want 1 and 1", invitations, recaptureRequirements)
	}
	var originalDraft database.CaptureDraft
	if err := h.AdminDB.Where("tenant_id=? AND id=?", fixture.TenantID, fixture.DraftID).Take(&originalDraft).Error; err != nil {
		t.Fatal(err)
	}
	var stillOriginal []capturecore.Requirement
	if err := json.Unmarshal(originalDraft.Requirements, &stillOriginal); err != nil {
		t.Fatal(err)
	}
	if originalDraft.Status != "SUBMITTED" || len(stillOriginal) != 1 || stillOriginal[0].Instructions != original[0].Instructions || stillOriginal[0].EvidenceKind != "PHOTO" {
		t.Fatalf("original photo capture changed: status=%s requirements=%+v", originalDraft.Status, stillOriginal)
	}
}
