//go:build integration

package harness

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	capturecore "inspection/services/inspection/internal/features/capture/core"
	invitationcore "inspection/services/inspection/internal/features/invitations/core"
	recapturecore "inspection/services/inspection/internal/features/recapture/core"
	reportcore "inspection/services/inspection/internal/features/reports/core"
	"inspection/services/inspection/internal/features/reports/publication"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

// TestVV_IT_353PhotoJourneyPublishesWithReusedEvidence covers PHOTO triage
// (media.verified.v1 consumer from VERIFIED to READY), analysis and the real
// inspection.classified.v1 report handler via runPhotoFlowWithClassified,
// followed by permitted recapture and publication.
func TestVV_IT_353PhotoJourneyPublishesWithReusedEvidence(t *testing.T) {
	h, fixture, classifiedEvent := runPhotoFlowWithClassified(t)
	ctx := context.Background()
	if classifiedEvent.Type != "inspection.classified.v1" {
		t.Fatalf("runPhotoFlow returned event %q, want inspection.classified.v1", classifiedEvent.Type)
	}
	var snapshot database.ReportSnapshot
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND inspection_id=?", fixture.TenantID, fixture.InspectionID).Order("version_number DESC").Take(&snapshot).Error
	}); err != nil {
		t.Fatal(err)
	}
	created, err := loadOutboxEnvelope(ctx, h, fixture.TenantID, "report.snapshot_created.v1")
	if err != nil {
		t.Fatalf("load report.snapshot_created.v1: %v", err)
	}
	var createdPayload struct {
		SnapshotID string `json:"snapshotId"`
	}
	if err := json.Unmarshal(created.Payload, &createdPayload); err != nil || created.CausationID != classifiedEvent.ID.String() || createdPayload.SnapshotID != snapshot.ID.String() {
		t.Fatalf("snapshot %s was not produced from classified event %s: causation=%s payload=%s err=%v", snapshot.ID, classifiedEvent.ID, created.CausationID, created.Payload, err)
	}
	var media database.MediaObject
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND id=?", fixture.TenantID, fixture.MediaID).Take(&media).Error
	}); err != nil {
		t.Fatal(err)
	}
	if media.RequirementKey == "" {
		t.Fatal("PHOTO fixture has no requirement key for recapture")
	}
	var inspection database.Inspection
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND id=?", fixture.TenantID, fixture.InspectionID).Take(&inspection).Error
	}); err != nil {
		t.Fatal(err)
	}
	var classified reportcore.Snapshot
	if err := json.Unmarshal(snapshot.CanonicalJSON, &classified); err != nil {
		t.Fatalf("decode classified PHOTO snapshot: %v", err)
	}
	if media.MediaKind != "PHOTO" || inspection.MediaKind != "PHOTO" || inspection.Status != "COMPLETED" || classified.Classification == "" {
		t.Fatalf("classification did not complete through PHOTO analysis: mediaKind=%s inspection=%s/%s classification=%s", media.MediaKind, inspection.MediaKind, inspection.Status, classified.Classification)
	}
	if len(classified.Evidence) != 1 || classified.Evidence[0].ID != fixture.MediaID.String() || classified.Evidence[0].RequirementKey != media.RequirementKey {
		t.Fatalf("handler snapshot evidence=%+v, want media %s and requirement %q", classified.Evidence, fixture.MediaID, media.RequirementKey)
	}
	// PHOTO evidence keeps the existing fields (vistoria-video TechSpec): the
	// handler does not set EvidenceKind/SourceMediaID for photos and marks the
	// item AVAILABLE from the DISPLAY derivative created during triage.
	var display database.MediaDerivative
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND media_id=? AND kind='DISPLAY'", fixture.TenantID, fixture.MediaID).Take(&display).Error
	}); err != nil {
		t.Fatalf("load DISPLAY derivative: %v", err)
	}
	if classified.Evidence[0].Availability != "AVAILABLE" || classified.Evidence[0].DisplayDigest != display.SHA256 || classified.Evidence[0].Role != "CURRENT" {
		t.Fatalf("handler snapshot evidence=%+v, want CURRENT AVAILABLE with DISPLAY digest %s", classified.Evidence[0], display.SHA256)
	}

	request, err := (recapturecore.Service{DB: h.DB}).Request(ctx, recapturecore.RequestInput{
		TenantID: fixture.TenantID, InspectionID: fixture.InspectionID,
		Items:      []recapturecore.Item{{RequirementKey: media.RequirementKey, OriginalMediaID: &fixture.MediaID, Reason: "Foto precisa de mais nitidez"}},
		Delivery:   []invitationcore.DeliveryIntent{{Channel: "EMAIL", Destination: "photo@example.test", Verified: true}},
		DeadlineAt: time.Now().UTC().Add(24 * time.Hour), IdempotencyKey: "vv-it-353-recapture",
	})
	if err != nil {
		t.Fatalf("request permitted PHOTO recapture: %v", err)
	}
	var recaptureDraft database.CaptureDraft
	if err := h.AdminDB.Where("tenant_id=? AND responsibility_id=? AND kind='RECAPTURE'", fixture.TenantID, request.ResponsibilityID).Take(&recaptureDraft).Error; err != nil {
		t.Fatal(err)
	}
	var requirements []capturecore.Requirement
	if err := json.Unmarshal(recaptureDraft.Requirements, &requirements); err != nil {
		t.Fatal(err)
	}
	if recaptureDraft.MediaKind != "PHOTO" || len(requirements) != 1 || requirements[0].Key != media.RequirementKey || requirements[0].EvidenceKind != "PHOTO" {
		t.Fatalf("recapture did not preserve PHOTO requirement: kind=%s requirements=%+v", recaptureDraft.MediaKind, requirements)
	}

	publicationResult, err := (publication.Service{DB: h.DB}).Publish(ctx, publication.PublishInput{
		TenantID: fixture.TenantID, InspectionID: fixture.InspectionID, SnapshotID: snapshot.ID,
		ActorID: fixture.IdentityID, ClientMutationID: "vv-it-353-publish", Final: true,
	})
	if err != nil || publicationResult.Status != "PUBLISHED" {
		t.Fatalf("publish PHOTO snapshot: status=%s err=%v", publicationResult.Status, err)
	}
	var persistedSnapshot database.ReportSnapshot
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND id=?", fixture.TenantID, snapshot.ID).Take(&persistedSnapshot).Error
	}); err != nil {
		t.Fatalf("reload published snapshot: %v", err)
	}
	var after reportcore.Snapshot
	if err := json.Unmarshal(persistedSnapshot.CanonicalJSON, &after); err != nil {
		t.Fatal(err)
	}
	if len(after.Evidence) != 1 || after.Evidence[0].ID != fixture.MediaID.String() || after.Evidence[0].RequirementKey != media.RequirementKey {
		t.Fatalf("publication did not preserve captured PHOTO evidence: %+v", after.Evidence)
	}
	var originalAfter database.MediaObject
	if err := h.WithinTenant(ctx, fixture.TenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND id=?", fixture.TenantID, fixture.MediaID).Take(&originalAfter).Error
	}); err != nil {
		t.Fatal(err)
	}
	if originalAfter.ID != media.ID || originalAfter.ObjectKey != media.ObjectKey || originalAfter.SHA256 != media.SHA256 {
		t.Fatalf("recapture replaced original PHOTO media: before=%+v after=%+v", media, originalAfter)
	}
	var persisted database.ReportPublication
	if err := h.AdminDB.Where("tenant_id=? AND snapshot_id=?", fixture.TenantID, snapshot.ID).Take(&persisted).Error; err != nil || persisted.Status != "PUBLISHED" {
		t.Fatalf("publication not persisted: status=%s err=%v", persisted.Status, err)
	}
}
