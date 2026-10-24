package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	invitationcore "inspection/services/inspection/internal/features/invitations/core"
	"inspection/services/inspection/internal/features/templates/catalog"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestIT091IT092IT095IT097ActivationPreservesHistory(t *testing.T) {
	f := newOriginActivationFixture(t)
	first, err := f.service.ActivateCompleted(context.Background(), f.tenantID, f.firstResponsibilityID)
	if err != nil || first.Status != "ACTIVE" {
		t.Fatalf("first valid origin was not activated: %+v %v", first, err)
	}
	replayed, err := f.service.ActivateCompleted(context.Background(), f.tenantID, f.firstResponsibilityID)
	if err != nil || replayed.ID != first.ID || replayed.Status != "ACTIVE" {
		t.Fatalf("activation replay changed the active version: %+v %v", replayed, err)
	}

	secondResponsibility := identity.NewID()
	secondVersion := database.OriginVersion{ID: identity.NewID(), TenantID: f.tenantID, OriginID: f.originID, VersionNumber: 2, ResponsibilityID: identity.Ptr(secondResponsibility), SupersedesID: &first.ID, Status: "DRAFT", IdempotencyKey: "origin-second", CreatedAt: f.now}
	secondDraft := database.CaptureDraft{ID: identity.NewID(), TenantID: f.tenantID, ResponsibilityID: secondResponsibility, Kind: "ORIGIN", TemplateVersionID: identity.NewID(), ReferencePayload: json.RawMessage(`{}`), PolicyPayload: json.RawMessage(`{}`), Requirements: mustJSONOrigin([]capturecore.Requirement{{Key: "room", Required: true, MinimumMedia: 1, MaximumMedia: 1}}), Status: "SUBMITTED", Version: 2, CreatedAt: f.now, UpdatedAt: f.now}
	secondMedia := database.MediaObject{ID: identity.NewID(), TenantID: f.tenantID, ResponsibilityID: secondResponsibility, ObjectKey: "origin/second", ContentType: "image/jpeg", SHA256: strings.Repeat("b", 64), SizeBytes: 10, Status: "READY", RequirementKey: "room", Description: "second view", CaptureSource: "CAMERA", Flags: json.RawMessage(`[]`), CreatedAt: f.now}
	for _, row := range []any{&secondVersion, &secondDraft, &secondMedia} {
		if err := f.db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	second, err := f.service.ActivateCompleted(context.Background(), f.tenantID, secondResponsibility)
	if err != nil || second.Status != "ACTIVE" {
		t.Fatalf("replacement origin was not activated: %+v %v", second, err)
	}
	var prior database.OriginVersion
	if err := f.db.First(&prior, "id=?", first.ID).Error; err != nil || prior.Status != "SUPERSEDED" {
		t.Fatalf("prior origin history was rewritten incorrectly: %+v %v", prior, err)
	}
	var origin database.Origin
	if err := f.db.First(&origin, "id=?", f.originID).Error; err != nil || origin.ActiveVersionID == nil || *origin.ActiveVersionID != second.ID {
		t.Fatalf("active origin pointer is wrong: %+v %v", origin, err)
	}
	var evidence int64
	if err := f.db.Model(&database.OriginEvidence{}).Where("tenant_id=?", f.tenantID).Count(&evidence).Error; err != nil || evidence != 2 {
		t.Fatalf("origin evidence history count=%d err=%v", evidence, err)
	}
}

func TestIT091ActivationRejectsUndescribedOriginEvidence(t *testing.T) {
	f := newOriginActivationFixture(t)
	if err := f.db.Model(&database.MediaObject{}).Where("id=?", f.firstMediaID).Update("description", " ").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.ActivateCompleted(context.Background(), f.tenantID, f.firstResponsibilityID); originCode(err) != apperror.InvalidState {
		t.Fatalf("undescribed origin evidence accepted: %v", err)
	}
}

func TestActivateVideoOriginRequiresUsableScopeAndPinsItsPreparation(t *testing.T) {
	f := newOriginActivationFixture(t)
	preparationID, frameID := identity.NewID(), identity.NewID()
	mediaID := identity.NewID()
	media := database.MediaObject{ID: mediaID, TenantID: f.tenantID, ResponsibilityID: f.firstResponsibilityID, MediaKind: "VIDEO", ObjectKey: "video/source", ContentType: "video/mp4", SHA256: strings.Repeat("c", 64), SizeBytes: 10, Status: "READY", CreatedAt: f.now}
	preparation := database.VideoPreparationJob{ID: preparationID, TenantID: f.tenantID, MediaID: mediaID, Status: "READY", Phase: "READY", ManifestDigest: strings.Repeat("d", 64), CreatedAt: f.now}
	source := database.VideoSource{ID: identity.NewID(), TenantID: f.tenantID, MediaID: mediaID, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 1000000, Width: 10, Height: 10, FrameRate: 30, ScreeningStatus: "APPROVED", Status: "RETAINED", CreatedAt: f.now, UpdatedAt: f.now}
	frame := database.VideoFrame{ID: frameID, TenantID: f.tenantID, SourceMediaID: mediaID, PreparationID: preparationID, PTSMicros: 0, SHA256: strings.Repeat("e", 64), ObjectKey: "video/frame.jpg", SizeBytes: 5, CreatedAt: f.now}
	scope := database.VideoScopeItem{ID: identity.NewID(), TenantID: f.tenantID, PreparationID: preparationID, FrameID: frameID, EnvironmentCandidate: "kitchen", ComponentLabel: "sink", ViewLabel: "front", Visibility: "CLEAR", Landmarks: json.RawMessage(`[]`), CreatedAt: f.now}
	for _, row := range []any{&media, &preparation, &source, &frame, &scope} {
		if err := f.db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := f.db.Model(&database.CaptureDraft{}).Where("tenant_id=? AND responsibility_id=?", f.tenantID, f.firstResponsibilityID).Update("media_kind", "VIDEO").Error; err != nil {
		t.Fatal(err)
	}
	activated, err := f.service.ActivateCompleted(context.Background(), f.tenantID, f.firstResponsibilityID)
	if err != nil || activated.Status != "ACTIVE" || activated.MediaKind != "VIDEO" || activated.VideoPreparationID == nil || *activated.VideoPreparationID != preparationID || activated.ScopeManifestDigest != preparation.ManifestDigest {
		t.Fatalf("video origin not pinned to prepared scope: %+v %v", activated, err)
	}
	var refs int64
	if err := f.db.Model(&database.VideoEvidenceReference{}).Where("tenant_id=? AND preparation_id=? AND owner_type='ORIGIN_VERSION' AND owner_id=?", f.tenantID, preparationID, activated.ID).Count(&refs).Error; err != nil || refs != 1 {
		t.Fatalf("origin frame bank dependency count=%d err=%v", refs, err)
	}
}

func TestInvalidateVideoOriginReleasesPreparationReference(t *testing.T) {
	f := newOriginActivationFixture(t)
	preparationID, frameID, mediaID := identity.NewID(), identity.NewID(), identity.NewID()
	for _, row := range []any{
		&database.MediaObject{ID: mediaID, TenantID: f.tenantID, ResponsibilityID: f.firstResponsibilityID, MediaKind: "VIDEO", ObjectKey: "video/source", ContentType: "video/mp4", SHA256: strings.Repeat("c", 64), SizeBytes: 10, Status: "READY", CreatedAt: f.now},
		&database.VideoPreparationJob{ID: preparationID, TenantID: f.tenantID, MediaID: mediaID, Status: "READY", Phase: "READY", ManifestDigest: strings.Repeat("d", 64), CreatedAt: f.now},
		&database.VideoSource{ID: identity.NewID(), TenantID: f.tenantID, MediaID: mediaID, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 1000000, Width: 10, Height: 10, FrameRate: 30, ScreeningStatus: "APPROVED", Status: "RETAINED", CreatedAt: f.now, UpdatedAt: f.now},
		&database.VideoFrame{ID: frameID, TenantID: f.tenantID, SourceMediaID: mediaID, PreparationID: preparationID, PTSMicros: 0, SHA256: strings.Repeat("e", 64), ObjectKey: "video/frame.jpg", SizeBytes: 5, CreatedAt: f.now},
		&database.VideoScopeItem{ID: identity.NewID(), TenantID: f.tenantID, PreparationID: preparationID, FrameID: frameID, EnvironmentCandidate: "kitchen", ComponentLabel: "sink", ViewLabel: "front", Visibility: "CLEAR", Landmarks: json.RawMessage(`[]`), CreatedAt: f.now},
	} {
		if err := f.db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := f.db.Model(&database.CaptureDraft{}).Where("tenant_id=? AND responsibility_id=?", f.tenantID, f.firstResponsibilityID).Update("media_kind", "VIDEO").Error; err != nil {
		t.Fatal(err)
	}
	version, err := f.service.ActivateCompleted(context.Background(), f.tenantID, f.firstResponsibilityID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Invalidate(context.Background(), f.tenantID, version.ID); err != nil {
		t.Fatal(err)
	}
	var references int64
	if err := f.db.Model(&database.VideoEvidenceReference{}).Where("tenant_id=? AND preparation_id=? AND owner_type='ORIGIN_VERSION' AND owner_id=?", f.tenantID, preparationID, version.ID).Count(&references).Error; err != nil || references != 0 {
		t.Fatalf("invalidated origin references=%d err=%v", references, err)
	}
}

func TestActivateVideoOriginRejectsEmptyScope(t *testing.T) {
	f := newOriginActivationFixture(t)
	mediaID := identity.NewID()
	media := database.MediaObject{ID: mediaID, TenantID: f.tenantID, ResponsibilityID: f.firstResponsibilityID, MediaKind: "VIDEO", ObjectKey: "video/source", ContentType: "video/mp4", SHA256: strings.Repeat("c", 64), SizeBytes: 10, Status: "READY", CreatedAt: f.now}
	preparation := database.VideoPreparationJob{ID: identity.NewID(), TenantID: f.tenantID, MediaID: mediaID, Status: "READY", Phase: "READY", ManifestDigest: strings.Repeat("d", 64), CreatedAt: f.now}
	source := database.VideoSource{ID: identity.NewID(), TenantID: f.tenantID, MediaID: mediaID, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 1000000, Width: 10, Height: 10, FrameRate: 30, ScreeningStatus: "APPROVED", Status: "RETAINED", CreatedAt: f.now, UpdatedAt: f.now}
	for _, row := range []any{&media, &preparation, &source} {
		if err := f.db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := f.db.Model(&database.CaptureDraft{}).Where("tenant_id=? AND responsibility_id=?", f.tenantID, f.firstResponsibilityID).Update("media_kind", "VIDEO").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.ActivateCompleted(context.Background(), f.tenantID, f.firstResponsibilityID); originCode(err) != apperror.InvalidState {
		t.Fatalf("video origin with empty scope activated: %v", err)
	}
}

func TestActivateInvalidatedVersionRejectedWithoutMutation(t *testing.T) {
	f := newOriginActivationFixture(t)
	var version database.OriginVersion
	if err := f.db.Where("responsibility_id=?", f.firstResponsibilityID).First(&version).Error; err != nil {
		t.Fatal(err)
	}
	version.Status = "INVALIDATED"
	version.InvalidatedAt = &f.now
	if err := f.db.Save(&version).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&database.Origin{}).Where("id=?", f.originID).Update("active_version_id", version.ID).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.ActivateCompleted(context.Background(), f.tenantID, f.firstResponsibilityID); originCode(err) != apperror.InvalidState {
		t.Fatalf("invalidated origin activation error=%v, want INVALID_STATE", err)
	}
	var got database.OriginVersion
	if err := f.db.First(&got, "id=?", version.ID).Error; err != nil || got.Status != "INVALIDATED" || got.InvalidatedAt == nil {
		t.Fatalf("invalidated origin was mutated: %+v err=%v", got, err)
	}
	var origin database.Origin
	if err := f.db.First(&origin, "id=?", f.originID).Error; err != nil || origin.ActiveVersionID == nil || *origin.ActiveVersionID != version.ID {
		t.Fatalf("origin pointer changed after rejected activation: %+v err=%v", origin, err)
	}
}

func TestActivateVideoOriginRejectsScopeFrameWithoutDigest(t *testing.T) {
	f := newOriginActivationFixture(t)
	preparationID, frameID, mediaID := identity.NewID(), identity.NewID(), identity.NewID()
	media := database.MediaObject{ID: mediaID, TenantID: f.tenantID, ResponsibilityID: f.firstResponsibilityID, MediaKind: "VIDEO", ObjectKey: "video/source", ContentType: "video/mp4", SHA256: strings.Repeat("c", 64), SizeBytes: 10, Status: "READY", CreatedAt: f.now}
	preparation := database.VideoPreparationJob{ID: preparationID, TenantID: f.tenantID, MediaID: mediaID, Status: "READY", Phase: "READY", ManifestDigest: strings.Repeat("d", 64), CreatedAt: f.now}
	source := database.VideoSource{ID: identity.NewID(), TenantID: f.tenantID, MediaID: mediaID, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 1000000, Width: 10, Height: 10, FrameRate: 30, ScreeningStatus: "APPROVED", Status: "RETAINED", CreatedAt: f.now, UpdatedAt: f.now}
	frame := database.VideoFrame{ID: frameID, TenantID: f.tenantID, SourceMediaID: mediaID, PreparationID: preparationID, PTSMicros: 0, SHA256: "", ObjectKey: "video/frame.jpg", SizeBytes: 5, CreatedAt: f.now}
	scope := database.VideoScopeItem{ID: identity.NewID(), TenantID: f.tenantID, PreparationID: preparationID, FrameID: frameID, EnvironmentCandidate: "kitchen", ComponentLabel: "sink", ViewLabel: "front", Visibility: "CLEAR", Landmarks: json.RawMessage(`[]`), CreatedAt: f.now}
	for _, row := range []any{&media, &preparation, &source, &frame, &scope} {
		if err := f.db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := f.db.Model(&database.CaptureDraft{}).Where("tenant_id=? AND responsibility_id=?", f.tenantID, f.firstResponsibilityID).Update("media_kind", "VIDEO").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.ActivateCompleted(context.Background(), f.tenantID, f.firstResponsibilityID); originCode(err) != apperror.InvalidState {
		t.Fatalf("video origin with scope frame missing digest activated: %v", err)
	}
}

func TestInvitePersistsTemplateMediaKindOnOriginVersion(t *testing.T) {
	for _, mediaKind := range []string{"PHOTO", "VIDEO"} {
		t.Run(mediaKind, func(t *testing.T) {
			f := newOriginActivationFixture(t)
			templateID, templateVersionID := identity.NewID(), identity.NewID()
			definition := catalog.TemplateDocument{SchemaVersion: catalog.SchemaVersion, IsVideo: mediaKind == "VIDEO", SegmentVersionID: identity.NewID().String(), ParticipantRoles: []string{"TENANT"}, Requirements: []catalog.CaptureRequirement{{Key: "room", Section: "room", Label: "room", EvidenceKind: mediaKind, MinimumCount: 1, MaximumCount: 1, Required: true}}}
			definitionJSON, err := json.Marshal(definition)
			if err != nil {
				t.Fatal(err)
			}
			templateVersion := database.TemplateVersion{ID: templateVersionID, TenantID: f.tenantID, TemplateID: templateID, VersionNumber: 1, SchemaVersion: catalog.SchemaVersion, DefinitionJSON: definitionJSON, CanonicalDigest: strings.Repeat("f", 64), Status: "ACTIVE", IdempotencyKey: "template-" + mediaKind, PublishedAt: f.now, CreatedBy: identity.NewID()}
			if err := f.db.Create(&templateVersion).Error; err != nil {
				t.Fatal(err)
			}
			created, err := f.service.Invite(context.Background(), InviteInput{TenantID: f.tenantID, AssetID: identity.NewID(), TemplateVersionID: templateVersionID, Delivery: []invitationcore.DeliveryIntent{{Channel: "EMAIL", Destination: "participant@example.test"}}, Requirements: []capturecore.Requirement{{Key: "room", MinimumMedia: 1, MaximumMedia: 1}}, ExpiresAt: f.now.Add(time.Hour), IdempotencyKey: "invite-" + mediaKind})
			if err != nil {
				t.Fatal(err)
			}
			var version database.OriginVersion
			if err := f.db.First(&version, "id=?", created.VersionID).Error; err != nil || version.MediaKind != mediaKind {
				t.Fatalf("origin version media kind=%q want %q err=%v", version.MediaKind, mediaKind, err)
			}
		})
	}
}

type originActivationFixture struct {
	db                                                      *gorm.DB
	service                                                 Service
	tenantID, originID, firstResponsibilityID, firstMediaID identity.ID
	now                                                     time.Time
}

func newOriginActivationFixture(t *testing.T) originActivationFixture {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"origins", "capture", "media", "messaging", "templates", "invitations", "platform"} {
		if err := db.Exec("ATTACH DATABASE ':memory:' AS " + schema).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, model := range []any{&database.Origin{}, &database.OriginVersion{}, &database.OriginEvidence{}, &database.CaptureDraft{}, &database.MediaObject{}, &database.VideoPreparationJob{}, &database.VideoSource{}, &database.VideoFrame{}, &database.VideoScopeItem{}, &database.VideoEvidenceReference{}, &database.OutboxIntent{}, &database.TemplateVersion{}, &database.Invitation{}, &database.VideoAdmission{}} {
		if err := db.AutoMigrate(model); err != nil && !strings.Contains(err.Error(), "no such table: main.") {
			t.Fatal(err)
		}
	}
	now := time.Date(2026, time.September, 5, 12, 0, 0, 0, time.UTC)
	tenantID, originID, responsibilityID, mediaID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	origin := database.Origin{ID: originID, TenantID: tenantID, AssetID: identity.NewID(), Version: 1, CreatedAt: now, UpdatedAt: now}
	version := database.OriginVersion{ID: identity.NewID(), TenantID: tenantID, OriginID: originID, VersionNumber: 1, ResponsibilityID: identity.Ptr(responsibilityID), Status: "DRAFT", IdempotencyKey: "origin-first", CreatedAt: now}
	draft := database.CaptureDraft{ID: identity.NewID(), TenantID: tenantID, ResponsibilityID: responsibilityID, Kind: "ORIGIN", TemplateVersionID: identity.NewID(), ReferencePayload: json.RawMessage(`{}`), PolicyPayload: json.RawMessage(`{}`), Requirements: mustJSONOrigin([]capturecore.Requirement{{Key: "room", Required: true, MinimumMedia: 1, MaximumMedia: 1}}), Status: "SUBMITTED", Version: 2, CreatedAt: now, UpdatedAt: now}
	media := database.MediaObject{ID: mediaID, TenantID: tenantID, ResponsibilityID: responsibilityID, ObjectKey: "origin/first", ContentType: "image/jpeg", SHA256: strings.Repeat("a", 64), SizeBytes: 10, Status: "READY", RequirementKey: "room", Description: "first view", CaptureSource: "CAMERA", Flags: json.RawMessage(`[]`), CreatedAt: now}
	for _, row := range []any{&origin, &version, &draft, &media} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&database.VideoAdmission{ID: 1, Enabled: true, Version: 1, ChangedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	return originActivationFixture{db: db, service: Service{DB: db, Now: func() time.Time { return now }, Within: func(ctx context.Context, _ identity.ID, fn func(*gorm.DB) error) error {
		return db.WithContext(ctx).Transaction(fn)
	}}, tenantID: tenantID, originID: originID, firstResponsibilityID: responsibilityID, firstMediaID: mediaID, now: now}
}

func TestInviteVideoDisabledRejectsBeforeCreatingResponsibilityRows(t *testing.T) {
	f := newOriginActivationFixture(t)
	templateID, templateVersionID := identity.NewID(), identity.NewID()
	definitionJSON, err := json.Marshal(catalog.TemplateDocument{SchemaVersion: catalog.SchemaVersion, IsVideo: true, SegmentVersionID: identity.NewID().String(), ParticipantRoles: []string{"TENANT"}, Requirements: []catalog.CaptureRequirement{{Key: "room", Section: "room", Label: "room", EvidenceKind: "VIDEO", MinimumCount: 1, MaximumCount: 1, Required: true}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.db.Create(&database.TemplateVersion{ID: templateVersionID, TenantID: f.tenantID, TemplateID: templateID, VersionNumber: 1, SchemaVersion: catalog.SchemaVersion, DefinitionJSON: definitionJSON, CanonicalDigest: strings.Repeat("f", 64), Status: "ACTIVE", IdempotencyKey: "disabled-video-template", PublishedAt: f.now, CreatedBy: identity.NewID()}).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&database.VideoAdmission{}).Where("id=1").Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	_, err = f.service.Invite(context.Background(), InviteInput{TenantID: f.tenantID, AssetID: identity.NewID(), TemplateVersionID: templateVersionID, Delivery: []invitationcore.DeliveryIntent{{Channel: "EMAIL", Destination: "participant@example.test"}}, Requirements: []capturecore.Requirement{{Key: "room", MinimumMedia: 1, MaximumMedia: 1}}, ExpiresAt: f.now.Add(time.Hour), IdempotencyKey: "disabled-video-invite"})
	if originCode(err) != apperror.IntegrationDisabled {
		t.Fatalf("disabled VIDEO invitation error=%v, want INTEGRATION_DISABLED", err)
	}
	for _, check := range []struct {
		model any
		want  int64
	}{{&database.OriginVersion{}, 1}, {&database.CaptureDraft{}, 1}, {&database.Invitation{}, 0}, {&database.OutboxIntent{}, 0}} {
		var count int64
		if err := f.db.Model(check.model).Where("tenant_id=?", f.tenantID).Count(&count).Error; err != nil || count != check.want {
			t.Fatalf("rejected invite changed %T rows: count=%d want=%d err=%v", check.model, count, check.want, err)
		}
	}
}

func mustJSONOrigin(value any) json.RawMessage {
	payload, _ := json.Marshal(value)
	return payload
}

func originCode(err error) apperror.Code {
	value, _, _ := apperror.Public(err)
	return value
}
