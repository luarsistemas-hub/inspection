package reference_items

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/objectstore"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type signerStub struct{ keys []string }

func (*signerStub) CreateMultipart(context.Context, string, string, string) (string, error) {
	return "", nil
}
func (*signerStub) PresignPart(context.Context, string, string, string, int, time.Duration) (string, error) {
	return "", nil
}
func (*signerStub) CompleteMultipart(context.Context, string, string, string, []objectstore.Part) error {
	return nil
}
func (*signerStub) AbortMultipart(context.Context, string, string, string) error { return nil }
func (*signerStub) Head(context.Context, string, string) (objectstore.Head, error) {
	return objectstore.Head{}, nil
}
func (*signerStub) Get(context.Context, string, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}
func (*signerStub) Put(context.Context, string, string, io.Reader, int64, string) error { return nil }
func (s *signerStub) PresignGet(_ context.Context, _, key string, _ time.Duration) (string, error) {
	s.keys = append(s.keys, key)
	return "https://signed.invalid/" + key, nil
}

func TestSetupRequiresDependencies(t *testing.T) {
	if err := Setup(Dependencies{}); err == nil {
		t.Fatal("missing dependencies accepted")
	}
}

func TestReferenceItemsArePinnedAuthorizedAndDisplayOnly(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"capture", "invitations", "media"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, model := range []any{&database.CaptureDraft{}, &database.ProcessingAcceptance{}, &database.MediaObject{}, &database.MediaDerivative{}} {
		if err := db.AutoMigrate(model); err != nil && !strings.Contains(err.Error(), "no such table: main.") {
			t.Fatal(err)
		}
	}
	parent, child, missing, unrelated := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	tenant, responsibility := identity.NewID(), identity.NewID()
	requirements, _ := json.Marshal([]capturecore.Requirement{
		{Key: "origin:" + child.String(), Section: "Referência", Instructions: "Cozinha", ComparisonTarget: "FIXED_ORIGIN"},
		{Key: "origin:" + missing.String(), Section: "Referência", Instructions: "Sala", ComparisonTarget: "FIXED_ORIGIN"},
	})
	reference, _ := json.Marshal(map[string]any{"comparisonMode": "FIXED_ORIGIN", "items": []map[string]any{{"mediaId": child, "description": "Cozinha"}, {"mediaId": missing, "description": "Sala"}, {"mediaId": parent, "description": "Não solicitado"}}})
	rows := []any{
		&database.CaptureDraft{ID: identity.NewID(), TenantID: tenant, ResponsibilityID: responsibility, Kind: "INSPECTION", Status: "OPEN", Requirements: requirements, ReferencePayload: reference, PolicyPayload: json.RawMessage(`{}`)},
		&database.ProcessingAcceptance{ID: identity.NewID(), TenantID: tenant, ResponsibilityID: responsibility, PhotoProcessing: true, AIAnalysis: true, GPSUse: true},
		&database.MediaObject{ID: child, TenantID: tenant, ResponsibilityID: parent, ObjectKey: "original", ContentType: "image/jpeg", Status: "READY"},
		&database.MediaObject{ID: unrelated, TenantID: tenant, ResponsibilityID: parent, ObjectKey: "unrelated", ContentType: "image/jpeg", Status: "READY"},
		&database.MediaDerivative{ID: identity.NewID(), TenantID: tenant, MediaID: child, ObjectKey: "display/cozinha", Kind: "DISPLAY", SHA256: "hash"},
		&database.MediaDerivative{ID: identity.NewID(), TenantID: tenant, MediaID: unrelated, ObjectKey: "display/unrelated", Kind: "DISPLAY", SHA256: "hash"},
	}
	for _, row := range rows {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	signer := &signerStub{}
	bus := mediator.New()
	if err := Setup(Dependencies{Bus: bus, DB: db, Store: objectstore.Store{Bucket: "private", Client: signer}, Within: func(ctx context.Context, _ identity.ID, fn func(*gorm.DB) error) error {
		return db.WithContext(ctx).Transaction(fn)
	}}); err != nil {
		t.Fatal(err)
	}
	raw, err := bus.Ask(context.Background(), Query{TenantID: tenant, ResponsibilityID: responsibility})
	if err != nil {
		t.Fatal(err)
	}
	items := raw.([]Item)
	if len(items) != 2 || items[0].ImageURL != "https://signed.invalid/display/cozinha" || items[0].Availability != "AVAILABLE" || items[0].ImageURLExpiresAt == nil || items[1].Availability != "UNAVAILABLE" || items[1].ImageURL != "" {
		t.Fatalf("unexpected reference projection: %+v", items)
	}
	if len(signer.keys) != 1 || signer.keys[0] != "display/cozinha" {
		t.Fatalf("signed keys leaked beyond requested display: %v", signer.keys)
	}
	if err := db.Where("tenant_id=? AND responsibility_id=?", tenant, responsibility).Delete(&database.ProcessingAcceptance{}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := bus.Ask(context.Background(), Query{TenantID: tenant, ResponsibilityID: responsibility}); err == nil {
		t.Fatal("references exposed without acceptance")
	}
	if err := db.Model(&database.CaptureDraft{}).Where("tenant_id=? AND responsibility_id=?", tenant, responsibility).Update("media_kind", "VIDEO").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.ProcessingAcceptance{ID: identity.NewID(), TenantID: tenant, ResponsibilityID: responsibility, VideoProcessing: true, AIAnalysis: true, GPSUse: true}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := bus.Ask(context.Background(), Query{TenantID: tenant, ResponsibilityID: responsibility}); err != nil {
		t.Fatalf("video reference guidance denied after video consent: %v", err)
	}
}

func TestVideoMainRequirementProjectsEveryPinnedScopeItem(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"capture", "invitations", "media"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, model := range []any{&database.CaptureDraft{}, &database.ProcessingAcceptance{}, &database.VideoFrame{}} {
		if err := db.AutoMigrate(model); err != nil && !strings.Contains(err.Error(), "no such table: main.") {
			t.Fatal(err)
		}
	}
	tenantID, responsibilityID, sourceMediaID, frameID, scopeID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	requirements, _ := json.Marshal([]capturecore.Requirement{{Key: "video:main", Section: "vistoria", Label: "Vídeo principal", EvidenceKind: "VIDEO", Required: true, MinimumMedia: 1, MaximumMedia: 1}})
	reference, _ := json.Marshal(map[string]any{"comparisonMode": "FIXED_ORIGIN", "mediaKind": "VIDEO", "scope": []map[string]any{{"scopeItemId": scopeID, "frameId": frameID, "sourceMediaId": sourceMediaID, "ptsMicros": 1500000, "environmentCandidate": "kitchen", "componentLabel": "sink", "viewLabel": "front", "visibility": "VISIBLE", "ambiguous": false, "gap": false}}})
	draft := database.CaptureDraft{ID: identity.NewID(), TenantID: tenantID, ResponsibilityID: responsibilityID, Kind: "INSPECTION", MediaKind: "VIDEO", Status: "OPEN", Requirements: requirements, ReferencePayload: reference, PolicyPayload: json.RawMessage(`{}`)}
	acceptance := database.ProcessingAcceptance{ID: identity.NewID(), TenantID: tenantID, ResponsibilityID: responsibilityID, VideoProcessing: true, AIAnalysis: true, GPSUse: true}
	frame := database.VideoFrame{ID: frameID, TenantID: tenantID, SourceMediaID: sourceMediaID, PreparationID: identity.NewID(), PTSMicros: 1500000, SHA256: "frame-digest", ObjectKey: "frames/sink.jpg"}
	for _, row := range []any{&draft, &acceptance, &frame} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	items, err := (Dependencies{DB: db, Store: objectstore.Store{Bucket: "private", Client: &signerStub{}}, Within: func(ctx context.Context, _ identity.ID, fn func(*gorm.DB) error) error {
		return db.WithContext(ctx).Transaction(fn)
	}}).load(context.Background(), Query{TenantID: tenantID, ResponsibilityID: responsibilityID})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].RequirementKey != "video:main" || items[0].EvidenceID != frameID || items[0].Environment != "kitchen" || items[0].Component != "sink" || items[0].View != "front" || items[0].Availability != "AVAILABLE" {
		t.Fatalf("video reference scope was not projected from the single upload slot: %+v", items)
	}
	legacyRequirements, _ := json.Marshal([]capturecore.Requirement{{Key: "video:" + scopeID.String(), Section: "kitchen", Label: "sink", Instructions: "front", EvidenceKind: "VIDEO_FRAME", Required: true}})
	if err := db.Model(&database.CaptureDraft{}).Where("tenant_id=? AND responsibility_id=?", tenantID, responsibilityID).Update("requirements", legacyRequirements).Error; err != nil {
		t.Fatal(err)
	}
	legacyItems, err := (Dependencies{DB: db, Store: objectstore.Store{Bucket: "private", Client: &signerStub{}}, Within: func(ctx context.Context, _ identity.ID, fn func(*gorm.DB) error) error {
		return db.WithContext(ctx).Transaction(fn)
	}}).load(context.Background(), Query{TenantID: tenantID, ResponsibilityID: responsibilityID})
	if err != nil || len(legacyItems) != 1 || legacyItems[0].RequirementKey != "video:"+scopeID.String() {
		t.Fatalf("legacy video scope requirement was not tolerated: items=%+v err=%v", legacyItems, err)
	}
}

func TestVideoRecaptureProjectsOnlySelectedOriginFrames(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"capture", "invitations", "media", "analysis"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, model := range []any{&database.CaptureDraft{}, &database.ProcessingAcceptance{}, &database.VideoPlan{}, &database.VideoPlanItem{}, &database.VideoFrame{}, &database.VideoScopeItem{}} {
		if err := db.AutoMigrate(model); err != nil && !strings.Contains(err.Error(), "no such table: main.") {
			t.Fatal(err)
		}
	}
	tenantID, responsibilityID, sourceID, planID, prepID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	componentIDs, frameIDs := []identity.ID{identity.NewID(), identity.NewID()}, []identity.ID{identity.NewID(), identity.NewID()}
	selected := []map[string]any{
		{"componentId": componentIDs[0], "requirementKey": "video:" + componentIDs[0].String(), "originalMediaId": sourceID, "reason": "A pia está opaca", "originFrameIds": []identity.ID{frameIDs[0]}},
		{"componentId": componentIDs[1], "requirementKey": "video:" + componentIDs[1].String(), "originalMediaId": sourceID, "reason": "A ducha não aparece", "originFrameIds": []identity.ID{frameIDs[1]}},
	}
	reference, _ := json.Marshal(map[string]any{"comparisonMode": "FIXED_ORIGIN", "mediaKind": "VIDEO", "videoRecapture": map[string]any{"planId": planID, "generation": 1, "selected": selected}})
	requirements, _ := json.Marshal([]capturecore.Requirement{{Key: "video:supplement", EvidenceKind: "VIDEO", Required: true}})
	draft := database.CaptureDraft{ID: identity.NewID(), TenantID: tenantID, ResponsibilityID: responsibilityID, Kind: "RECAPTURE", MediaKind: "VIDEO", Status: "OPEN", Requirements: requirements, ReferencePayload: reference, PolicyPayload: json.RawMessage(`{}`)}
	acceptance := database.ProcessingAcceptance{ID: identity.NewID(), TenantID: tenantID, ResponsibilityID: responsibilityID, VideoProcessing: true, AIAnalysis: true, GPSUse: true}
	plan := database.VideoPlan{ID: planID, TenantID: tenantID, InspectionID: identity.NewID(), SubmissionID: identity.NewID(), CurrentPreparationID: prepID, Generation: 1, PlanDigest: "plan-digest", Status: "SEALED"}
	rows := []any{&draft, &acceptance, &plan}
	for index := range componentIDs {
		key := "video:" + componentIDs[index].String()
		originFrameIDs := []identity.ID{frameIDs[index]}
		currentFrameIDs := []identity.ID{}
		if index == 0 {
			// F3.7's active-plan projection selects the component's current
			// evidence; it may not be part of the item's original-frame set.
			originFrameIDs = []identity.ID{}
			currentFrameIDs = []identity.ID{frameIDs[index]}
		}
		rows = append(rows,
			&database.VideoPlanItem{ID: identity.NewID(), TenantID: tenantID, PlanID: planID, ComponentID: componentIDs[index], RequirementKey: key, Outcome: "DISPATCHED", InputDigest: "input-digest", OriginFrameIDs: mustJSON(t, originFrameIDs), CurrentFrameIDs: mustJSON(t, currentFrameIDs), CoreGroups: mustJSON(t, []any{}), CurrentGroups: mustJSON(t, []any{})},
			&database.VideoFrame{ID: frameIDs[index], TenantID: tenantID, SourceMediaID: sourceID, PreparationID: prepID, PTSMicros: int64(index+1) * 1_250_000, SHA256: "frame-digest", ObjectKey: "frames/" + frameIDs[index].String()},
			&database.VideoScopeItem{ID: identity.NewID(), TenantID: tenantID, PreparationID: prepID, FrameID: frameIDs[index], EnvironmentCandidate: []string{"kitchen", "bathroom"}[index], ComponentLabel: []string{"sink", "shower"}[index], ViewLabel: "front", Visibility: "VISIBLE", Landmarks: json.RawMessage(`[]`)},
		)
	}
	for _, row := range rows {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	items, err := (Dependencies{DB: db, Store: objectstore.Store{Bucket: "private", Client: &signerStub{}}, Within: func(ctx context.Context, _ identity.ID, fn func(*gorm.DB) error) error {
		return db.WithContext(ctx).Transaction(fn)
	}}).load(context.Background(), Query{TenantID: tenantID, ResponsibilityID: responsibilityID})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Component != "sink" || items[0].Reason != "A pia está opaca" || items[0].TimestampMS != 1250 || items[0].Availability != "AVAILABLE" || items[1].Component != "shower" || items[1].Reason != "A ducha não aparece" || items[1].TimestampMS != 2500 {
		t.Fatalf("unexpected video recapture projection: %+v", items)
	}
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestScopeDescriptionPrefersCompleteDisplayNames(t *testing.T) {
	scope := database.VideoScopeItem{EnvironmentCandidate: "outdoor_kitchen", ComponentLabel: "grill", EnvironmentDisplayName: "Área gourmet", ComponentDisplayName: "Churrasqueira"}
	if got := scopeDescription(scope); got != "Área gourmet · Churrasqueira" {
		t.Fatalf("description = %q", got)
	}
	scope.ComponentDisplayName = ""
	if got := scopeDescription(scope); got != "outdoor_kitchen · grill" {
		t.Fatalf("incomplete description = %q", got)
	}
}
