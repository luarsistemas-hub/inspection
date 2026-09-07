package capture_integration_test

import (
	"context"
	"encoding/json"
	"testing"

	capturecore "inspection/services/inspection/internal/features/capture/core"
	mediacore "inspection/services/inspection/internal/features/media/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
)

func setRequirements(t *testing.T, f submissionFixture, requirements []capturecore.Requirement) {
	t.Helper()
	if err := f.db.Model(&database.CaptureDraft{}).Where("id=?", f.draft.ID).Update("requirements", mustJSON(requirements)).Error; err != nil {
		t.Fatal(err)
	}
}

func TestIT151IT152CaptureRejectsUnsupportedOrEmptyRequirements(t *testing.T) {
	if err := mediacore.ValidateAdmission("application/pdf", 10, 0); code(err) != apperror.InvalidInput {
		t.Fatalf("unsupported media accepted: %v", err)
	}
	if _, err := capturecore.Evaluate(nil, nil, false); code(err) != apperror.InvalidState {
		t.Fatalf("empty template accepted: %v", err)
	}
	f := newSubmissionFixture(t)
	if err := f.service.DeclareImpossibility(context.Background(), f.draft.TenantID, f.draft.ResponsibilityID, "room", " ", 0); code(err) != apperror.InvalidInput {
		t.Fatalf("blank impossibility accepted: %v", err)
	}
}

func TestIT153PerRequirementLimitKeepsAcceptedMedia(t *testing.T) {
	f := newSubmissionFixture(t)
	setRequirements(t, f, []capturecore.Requirement{{Key: "room", Required: true, MinimumMedia: 1, MaximumMedia: 1, DescriptionRequired: true}})
	one := f.addMedia(t, "READY", "", "", false)
	two := f.addMedia(t, "READY", "", "", false)
	input := capturecore.MetadataInput{TenantID: f.draft.TenantID, ResponsibilityID: f.draft.ResponsibilityID, RequirementKey: "room", Description: "room", CaptureSource: "CAMERA", WindowStartedAt: f.now}
	input.MediaID = one.ID
	if _, err := f.service.SaveMetadata(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	input.MediaID = two.ID
	if _, err := f.service.SaveMetadata(context.Background(), input); code(err) != apperror.InvalidState {
		t.Fatalf("per-requirement limit bypassed: %v", err)
	}
	var accepted int64
	if err := f.db.Model(&database.MediaObject{}).Where("responsibility_id=? AND requirement_key='room'", f.draft.ResponsibilityID).Count(&accepted).Error; err != nil || accepted != 1 {
		t.Fatalf("accepted evidence changed: %d %v", accepted, err)
	}
}

func TestIT155IT156IT157MetadataVersionAndRefreshContracts(t *testing.T) {
	f := newSubmissionFixture(t)
	setRequirements(t, f, []capturecore.Requirement{{Key: "room", Required: true, MinimumMedia: 1, MaximumMedia: 10, DescriptionRequired: true}})
	media := []database.MediaObject{f.addMedia(t, "READY", "", "", false), f.addMedia(t, "READY", "", "", false), f.addMedia(t, "READY", "", "", false)}
	base := capturecore.MetadataInput{TenantID: f.draft.TenantID, ResponsibilityID: f.draft.ResponsibilityID, RequirementKey: "room", Description: "room", CaptureSource: "CAMERA", WindowStartedAt: f.now}
	base.MediaID = media[0].ID
	if _, err := f.service.SaveMetadata(context.Background(), base); err != nil {
		t.Fatal(err)
	}
	base.MediaID = media[1].ID
	base.ExpectedVersion = 1
	if _, err := f.service.SaveMetadata(context.Background(), base); err != nil {
		t.Fatal(err)
	}
	base.MediaID = media[2].ID
	if _, err := f.service.SaveMetadata(context.Background(), base); code(err) != apperror.Conflict {
		t.Fatalf("stale metadata save accepted: %v", err)
	}
	view, err := f.service.Load(context.Background(), f.draft.TenantID, f.draft.ResponsibilityID)
	if err != nil || len(view.Answers) != 1 {
		t.Fatalf("refresh lost answer: %+v %v", view, err)
	}
	base.MediaID = media[1].ID
	if _, err := f.service.SaveMetadata(context.Background(), base); err != nil {
		t.Fatalf("same completion was not idempotent: %v", err)
	}
}

func TestIT161IT162IT163GPSUsesFixedPolicyAndFlagsOptionalFailure(t *testing.T) {
	f := newSubmissionFixture(t)
	setRequirements(t, f, []capturecore.Requirement{{Key: "room", Required: true, MinimumMedia: 1, MaximumMedia: 10, DescriptionRequired: true}})
	if err := f.db.Model(&database.CaptureDraft{}).Where("id=?", f.draft.ID).Update("policy_payload", json.RawMessage(`{"gpsRequired":true,"allowGallery":true,"assetLatitudeE6":-27590000,"assetLongitudeE6":-48550000,"geofenceMeters":100}`)).Error; err != nil {
		t.Fatal(err)
	}
	media := f.addMedia(t, "READY", "", "", false)
	lat, lon, accuracy := 91.0, -48.55, 1.0
	input := capturecore.MetadataInput{TenantID: f.draft.TenantID, ResponsibilityID: f.draft.ResponsibilityID, MediaID: media.ID, RequirementKey: "room", Description: "room", CaptureSource: "CAMERA", Latitude: &lat, Longitude: &lon, AccuracyMeters: &accuracy, WindowStartedAt: f.now}
	if _, err := f.service.SaveMetadata(context.Background(), input); code(err) != apperror.InvalidState {
		t.Fatalf("invalid required GPS accepted: %v", err)
	}
	f = newSubmissionFixture(t)
	setRequirements(t, f, []capturecore.Requirement{{Key: "room", Required: true, MinimumMedia: 1, MaximumMedia: 10, DescriptionRequired: true}})
	media = f.addMedia(t, "READY", "", "", false)
	input = capturecore.MetadataInput{TenantID: f.draft.TenantID, ResponsibilityID: f.draft.ResponsibilityID, MediaID: media.ID, RequirementKey: "room", Description: "room", CaptureSource: "CAMERA", Latitude: &lat, Longitude: &lon, AccuracyMeters: &accuracy, WindowStartedAt: f.now}
	if _, err := f.service.SaveMetadata(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	var saved database.MediaObject
	if err := f.db.First(&saved, "id=?", media.ID).Error; err != nil {
		t.Fatal(err)
	}
	var flags []string
	if err := json.Unmarshal(saved.Flags, &flags); err != nil || !containsString(flags, "GPS_MISSING_OR_INVALID") {
		t.Fatalf("optional GPS flag missing: %s %v", saved.Flags, err)
	}
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
