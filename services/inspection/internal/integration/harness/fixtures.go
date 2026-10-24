package harness

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"inspection/libs/identity"
	analysisprompt "inspection/services/inspection/internal/features/analysis/prompt"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/video"

	"gorm.io/gorm"
)

// Task06Fixture is the smallest complete tenant-scoped graph used by the
// external Task 06 cases. Tests may extend it inside WithinTenant without
// depending on another test's rows.
type Task06Fixture struct {
	TenantID, IdentityID, MembershipID, BusinessUnitID identity.ID
	ParticipantID, ContactID                           identity.ID
	SegmentVersionID, TemplateID, TemplateVersionID    identity.ID
	PromptSnapshotID, AssetID                          identity.ID
	InspectionID, ResponsibilityID, DraftID            identity.ID
	MediaID, DerivativeID, ProjectID, StageID          identity.ID
}

type Task06FixtureOptions struct {
	InspectionStatus string
	RequirementKey   string
	ComparisonMode   string
}

// VideoOriginFixture is a ready, active VIDEO origin with retained frame scope.
type VideoOriginFixture struct {
	TenantID, AssetID, ResponsibilityID, OriginID, OriginVersionID identity.ID
	MediaID, PreparationID, FrameID, ScopeItemID                   identity.ID
}

// VideoOriginFixtureOptions controls the origin activation state for tests
// which need to exercise draft activation.
type VideoOriginFixtureOptions struct {
	Active bool
}

// SeedVideoOriginFixture creates a VIDEO origin whose retained frames are
// stored in MinIO and whose scope is attached to the frame.
func (h *Harness) SeedVideoOriginFixture(ctx context.Context, options VideoOriginFixtureOptions) (VideoOriginFixture, error) {
	if h == nil || h.DB == nil || h.Store.Client == nil || h.Store.Bucket == "" {
		return VideoOriginFixture{}, fmt.Errorf("integration harness: database and object store are required")
	}
	base, err := h.SeedTask06Fixture(ctx, Task06FixtureOptions{InspectionStatus: "COMPLETED"})
	if err != nil {
		return VideoOriginFixture{}, err
	}
	f := VideoOriginFixture{TenantID: base.TenantID, AssetID: base.AssetID, ResponsibilityID: identity.NewID(), OriginID: identity.NewID(), OriginVersionID: identity.NewID(), MediaID: identity.NewID(), PreparationID: identity.NewID(), FrameID: identity.NewID(), ScopeItemID: identity.NewID()}
	now := time.Now().UTC().Truncate(time.Microsecond)
	retentionDays := 30
	expiresAt := now.Add(time.Duration(retentionDays) * 24 * time.Hour)
	frameBytes := fixturePNG()
	frameDigest := digest(frameBytes)
	frameKey := "tenant/" + f.TenantID.String() + "/video-frames/" + f.FrameID.String() + ".jpg"
	if err := h.Store.Client.Put(ctx, h.Store.Bucket, frameKey, bytes.NewReader(frameBytes), int64(len(frameBytes)), "image/png"); err != nil {
		return VideoOriginFixture{}, fmt.Errorf("seed video origin frame object: %w", err)
	}
	err = h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
		if err := tx.Create(&database.Responsibility{ID: f.ResponsibilityID, TenantID: f.TenantID, InspectionID: base.InspectionID, ParticipantID: base.ParticipantID, Status: "SUBMITTED", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.CaptureDraft{ID: identity.NewID(), TenantID: f.TenantID, ResponsibilityID: f.ResponsibilityID, Kind: "ORIGIN", MediaKind: "VIDEO", TemplateVersionID: base.TemplateVersionID, ReferencePayload: json.RawMessage(`{}`), PolicyPayload: json.RawMessage(`{}`), Requirements: json.RawMessage(`[{"key":"video:main","label":"Vídeo principal","evidenceKind":"VIDEO","required":true,"minimumCount":1,"maximumCount":1}]`), Status: "SUBMITTED", Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		status := "DRAFT"
		activated := (*time.Time)(nil)
		activeVersion := (*identity.ID)(nil)
		if options.Active {
			status = "ACTIVE"
			activated = &now
			activeVersion = &f.OriginVersionID
		}
		if err := tx.Create(&database.Origin{ID: f.OriginID, TenantID: f.TenantID, AssetID: f.AssetID, ActiveVersionID: activeVersion, Version: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.MediaObject{ID: f.MediaID, TenantID: f.TenantID, ResponsibilityID: f.ResponsibilityID, ObjectKey: "tenant/" + f.TenantID.String() + "/media/" + f.MediaID.String(), ContentType: "video/mp4", SHA256: frameDigest, SizeBytes: 1, Status: "READY", MediaKind: "VIDEO", RequirementKey: "video:main", AttentionItems: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoPreparationJob{ID: f.PreparationID, TenantID: f.TenantID, MediaID: f.MediaID, Status: "READY", Phase: "READY", PreparationVersion: 1, Generation: 1, NextAttemptAt: now, SourceSHA256: frameDigest, ManifestDigest: frameDigest, PendingObjectKeys: []byte(`[]`), FirstReadyAt: &now, CreatedAt: now}).Error; err != nil {
			return err
		}
		responsibilityID := f.ResponsibilityID
		if err := tx.Create(&database.OriginVersion{ID: f.OriginVersionID, TenantID: f.TenantID, OriginID: f.OriginID, VersionNumber: 1, ResponsibilityID: &responsibilityID, Status: status, MediaKind: "VIDEO", VideoPreparationID: &f.PreparationID, ScopeManifestDigest: frameDigest, IdempotencyKey: "video-origin-fixture-" + f.OriginVersionID.String(), CreatedAt: now, SubmittedAt: &now, ActivatedAt: activated}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoSource{ID: identity.NewID(), TenantID: f.TenantID, MediaID: f.MediaID, Container: "mp4", Codec: "h264", PixelFormat: "yuv420p", ColorTransfer: "bt709", DurationMicros: 1_000_000, Width: 1, Height: 1, FrameRate: 1, ScreeningStatus: "APPROVED", FirstFrameReadyAt: &now, RetentionDays: &retentionDays, SourceExpiryAt: &expiresAt, RetentionAnchor: "FIXED", Status: "RETAINED", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.VideoFrame{ID: f.FrameID, TenantID: f.TenantID, SourceMediaID: f.MediaID, PreparationID: f.PreparationID, PTSMicros: 0, SHA256: frameDigest, ObjectKey: frameKey, Width: 1, Height: 1, Role: "BASELINE", Quality: "GOOD", SizeBytes: int64(len(frameBytes)), CreatedAt: now}).Error; err != nil {
			return err
		}
		return tx.Create(&database.VideoScopeItem{ID: f.ScopeItemID, TenantID: f.TenantID, PreparationID: f.PreparationID, FrameID: f.FrameID, EnvironmentCandidate: "kitchen", ComponentLabel: "sink", ViewLabel: "wide", Visibility: video.VisibilityVisible, IdentityStatus: "RESOLVED", Landmarks: json.RawMessage(`[]`), CreatedAt: now}).Error
	})
	if err != nil {
		return VideoOriginFixture{}, fmt.Errorf("seed video origin fixture: %w", err)
	}
	return f, nil
}

func (o Task06FixtureOptions) normalized() Task06FixtureOptions {
	if o.InspectionStatus == "" {
		o.InspectionStatus = "SUBMITTED"
	}
	if o.RequirementKey == "" {
		o.RequirementKey = "room-1"
	}
	if o.ComparisonMode == "" {
		o.ComparisonMode = "FIXED_ORIGIN"
	}
	return o
}

// SeedTask06Fixture creates a tenant, authorized internal principal, catalog,
// capture graph, inspection and project in one transaction. It intentionally
// uses the same tenant transaction boundary as production assertions.
func (h *Harness) SeedTask06Fixture(ctx context.Context, options Task06FixtureOptions) (Task06Fixture, error) {
	if h == nil || h.DB == nil {
		return Task06Fixture{}, fmt.Errorf("integration harness: database unavailable")
	}
	if h.RuntimeDatabaseURL == "" {
		return Task06Fixture{}, fmt.Errorf("integration harness: INSPECTION_TEST_RUNTIME_DATABASE_URL is required for tenant-scoped fixtures")
	}
	options = options.normalized()
	now := time.Now().UTC().Truncate(time.Microsecond)
	f := Task06Fixture{
		TenantID: identity.NewID(), IdentityID: identity.NewID(), MembershipID: identity.NewID(), BusinessUnitID: identity.NewID(),
		ParticipantID: identity.NewID(), ContactID: identity.NewID(), SegmentVersionID: identity.NewID(), TemplateID: identity.NewID(), TemplateVersionID: identity.NewID(),
		PromptSnapshotID: identity.NewID(), AssetID: identity.NewID(), InspectionID: identity.NewID(), ResponsibilityID: identity.NewID(),
		DraftID: identity.NewID(), MediaID: identity.NewID(), DerivativeID: identity.NewID(), ProjectID: identity.NewID(), StageID: identity.NewID(),
	}
	tenant := database.Tenant{ID: f.TenantID, TenantID: f.TenantID, Name: "Task 06 tenant", Language: "pt-BR", DefaultTimezone: "America/Sao_Paulo", Status: "ACTIVE", Version: 1, CreatedAt: now, UpdatedAt: now}
	adminDB := h.AdminDB
	if adminDB == nil {
		adminDB = h.MigrationDB
	}
	if adminDB == nil {
		return Task06Fixture{}, fmt.Errorf("integration harness: administrative database unavailable")
	}
	if err := adminDB.Create(&tenant).Error; err != nil {
		return Task06Fixture{}, fmt.Errorf("seed tenant: %w", err)
	}
	err := h.WithinTenant(ctx, f.TenantID, func(tx *gorm.DB) error {
		createdAt := now
		if err := tx.Create(&database.BusinessUnit{ID: f.BusinessUnitID, TenantID: f.TenantID, Code: "TASK06", Name: "Task 06 unit", Status: "ACTIVE", Version: 1, CreatedAt: createdAt, UpdatedAt: createdAt}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.Membership{ID: f.MembershipID, TenantID: f.TenantID, IdentityID: f.IdentityID, Issuer: "task06-test", Subject: f.IdentityID.String(), Role: auth.TenantAdmin, Status: "ACTIVE", Version: 1, CreatedAt: createdAt, UpdatedAt: createdAt}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.ResourceScope{ID: identity.NewID(), TenantID: f.TenantID, MembershipID: f.MembershipID, Kind: "BUSINESS_UNIT", ResourceID: f.BusinessUnitID, CreatedAt: createdAt}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.Participant{ID: f.ParticipantID, TenantID: f.TenantID, BusinessUnitID: f.BusinessUnitID, Name: "Task 06 participant", SegmentRole: "OWNER", Status: "ACTIVE", Version: 1, IdempotencyKey: "task06-participant", CreatedAt: createdAt, UpdatedAt: createdAt}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.ParticipantContact{ID: f.ContactID, TenantID: f.TenantID, ParticipantID: f.ParticipantID, Channel: "EMAIL", Value: "task06@example.test", Normalized: "task06@example.test", Active: true, CreatedAt: createdAt, UpdatedAt: createdAt}).Error; err != nil {
			return err
		}
		verifiedAt := createdAt
		if err := tx.Create(&database.ContactVerification{ID: identity.NewID(), TenantID: f.TenantID, ContactID: f.ContactID, IdempotencyKey: "task06-verification", Status: "VERIFIED", VerifiedAt: &verifiedAt, CreatedAt: createdAt}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.ChannelSelection{ID: identity.NewID(), TenantID: f.TenantID, ParticipantID: f.ParticipantID, ContactID: f.ContactID, CreatedAt: createdAt}).Error; err != nil {
			return err
		}
		segmentSchema := json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}}}`)
		segmentDigest := digest(segmentSchema)
		segmentID := identity.NewID()
		if err := tx.Create(&database.SegmentDefinition{ID: segmentID, TenantID: f.TenantID, Key: "task06", Name: "Task 06 segment", ActiveVersionID: &f.SegmentVersionID, Version: 1, CreatedAt: createdAt, UpdatedAt: createdAt}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.SegmentDefinitionVersion{ID: f.SegmentVersionID, TenantID: f.TenantID, DefinitionID: segmentID, VersionNumber: 1, SchemaVersion: 1, SchemaJSON: segmentSchema, UISchemaJSON: segmentSchema, CanonicalDigest: segmentDigest, Status: "ACTIVE", IdempotencyKey: "task06-segment", PublishedAt: createdAt, CreatedBy: f.IdentityID}).Error; err != nil {
			return err
		}
		templateJSON := json.RawMessage(fmt.Sprintf(`{"segment":"task06","requirements":[{"key":"%s","description":"Capture current evidence","required":true}]}`, options.RequirementKey))
		if err := tx.Create(&database.Template{ID: f.TemplateID, TenantID: f.TenantID, Key: "task06-template", Name: "Task 06 template", SegmentVersionID: f.SegmentVersionID, ActiveVersionID: &f.TemplateVersionID, Version: 1, CreatedAt: createdAt, UpdatedAt: createdAt}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.TemplateVersion{ID: f.TemplateVersionID, TenantID: f.TenantID, TemplateID: f.TemplateID, VersionNumber: 1, SchemaVersion: 1, DefinitionJSON: templateJSON, CanonicalDigest: digest(templateJSON), Status: "ACTIVE", IdempotencyKey: "task06-template", PublishedAt: createdAt, CreatedBy: f.IdentityID}).Error; err != nil {
			return err
		}
		promptJSON, promptDigest, err := analysisprompt.Canonicalize(analysisprompt.DefaultDefinition())
		if err != nil {
			return err
		}
		var promptSnapshot database.AnalysisPromptSnapshot
		if err := tx.Where("analysis_type = ? AND task = ? AND canonical_digest = ?", analysisprompt.RealEstate, analysisprompt.Findings, promptDigest).
			Attrs(database.AnalysisPromptSnapshot{ID: f.PromptSnapshotID, AnalysisType: analysisprompt.RealEstate, Task: string(analysisprompt.Findings), DefinitionJSON: promptJSON, CanonicalDigest: promptDigest, CreatedAt: createdAt}).
			FirstOrCreate(&promptSnapshot).Error; err != nil {
			return err
		}
		f.PromptSnapshotID = promptSnapshot.ID
		if err := tx.Create(&database.Asset{ID: f.AssetID, TenantID: f.TenantID, BusinessUnitID: f.BusinessUnitID, SegmentVersionID: f.SegmentVersionID, TemplateID: &f.TemplateID, Name: "Task 06 asset", ExternalKey: "task06-asset", Address: "Test address", GeofenceMeters: 150, PolicyOverrides: json.RawMessage(`{}`), Status: "ACTIVE", Version: 1, IdempotencyKey: "task06-asset", CreatedAt: createdAt, UpdatedAt: createdAt}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.AssetAttributeVersion{ID: identity.NewID(), TenantID: f.TenantID, AssetID: f.AssetID, SegmentVersionID: f.SegmentVersionID, VersionNumber: 1, AttributesJSON: json.RawMessage(`{"name":"fixture"}`), CanonicalDigest: digest([]byte(`{"name":"fixture"}`)), CreatedAt: createdAt}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.AssetAssignment{ID: identity.NewID(), TenantID: f.TenantID, AssetID: f.AssetID, ParticipantID: f.ParticipantID, Role: "OWNER", Active: true, CreatedAt: createdAt}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.Project{ID: f.ProjectID, TenantID: f.TenantID, BusinessUnitID: f.BusinessUnitID, AssetID: f.AssetID, Name: "Fixture project", Status: "ACTIVE", Version: 1, IdempotencyKey: "task06-project", CreatedAt: createdAt, UpdatedAt: createdAt}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.ProjectStage{ID: f.StageID, TenantID: f.TenantID, ProjectID: f.ProjectID, Key: "stage-1", Label: "Fixture stage", Kind: "INSPECTION", Position: 1, Status: "COMPLETED", Requirements: templateJSON, EffectiveReference: json.RawMessage(`{}`), Version: 1, CreatedAt: createdAt, UpdatedAt: createdAt}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.Inspection{ID: f.InspectionID, TenantID: f.TenantID, BusinessUnitID: f.BusinessUnitID, AssetID: f.AssetID, ParticipantID: f.ParticipantID, TemplateID: f.TemplateID, TemplateVersionID: f.TemplateVersionID, AnalysisPromptSnapshotID: f.PromptSnapshotID, ProjectID: &f.ProjectID, StageID: &f.StageID, Source: "MANUAL", SourceKey: "task06-inspection", SourceReason: "integration fixture", Status: options.InspectionStatus, EvidenceCount: 1, DueAt: createdAt, DeadlineAt: createdAt.Add(24 * time.Hour), ReminderInstants: json.RawMessage(`[]`), ContextSnapshot: json.RawMessage(`{}`), Version: 1, CreatedAt: createdAt, UpdatedAt: createdAt}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.Responsibility{ID: f.ResponsibilityID, TenantID: f.TenantID, InspectionID: f.InspectionID, ParticipantID: f.ParticipantID, Status: "SUBMITTED", Version: 1, CreatedAt: createdAt, UpdatedAt: createdAt}).Error; err != nil {
			return err
		}
		referencePayload := json.RawMessage(`{}`)
		if options.ComparisonMode == "CURRENT_ONLY" {
			referencePayload = json.RawMessage(`{"comparisonMode":"CURRENT_ONLY"}`)
		}
		if err := tx.Create(&database.ReferenceSnapshot{ID: identity.NewID(), TenantID: f.TenantID, InspectionID: f.InspectionID, ComparisonMode: options.ComparisonMode, Payload: referencePayload, CreatedAt: createdAt}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.PolicySnapshot{ID: identity.NewID(), TenantID: f.TenantID, InspectionID: f.InspectionID, SchemaVersion: 1, Payload: json.RawMessage(`{}`), CanonicalDigest: digest([]byte(`{}`)), CreatedAt: createdAt}).Error; err != nil {
			return err
		}
		mediaKey := "tenant/" + f.TenantID.String() + "/media/" + f.MediaID.String()
		mediaBytes := fixturePNG()
		mediaDigest := digest(mediaBytes)
		mediaContentType := "image/png"
		derivativeKey := mediaKey + "/normalized"
		if h.Store.Client != nil {
			if err := h.Store.Client.Put(ctx, h.Store.Bucket, mediaKey, bytes.NewReader(mediaBytes), int64(len(mediaBytes)), mediaContentType); err != nil {
				return err
			}
			if err := h.Store.Client.Put(ctx, h.Store.Bucket, derivativeKey, bytes.NewReader(mediaBytes), int64(len(mediaBytes)), mediaContentType); err != nil {
				return err
			}
		}
		if err := tx.Create(&database.MediaObject{ID: f.MediaID, TenantID: f.TenantID, ResponsibilityID: f.ResponsibilityID, ObjectKey: mediaKey, ContentType: mediaContentType, SHA256: mediaDigest, SizeBytes: int64(len(mediaBytes)), Status: "READY", IdempotencyKey: "task06-media", RequirementKey: options.RequirementKey, Description: "Fixture evidence", CaptureSource: "CAMERA", AttentionItems: json.RawMessage(`[]`), Flags: json.RawMessage(`[]`), CreatedAt: createdAt}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.MediaDerivative{ID: f.DerivativeID, TenantID: f.TenantID, MediaID: f.MediaID, ObjectKey: derivativeKey, Kind: "NORMALIZED", SHA256: mediaDigest, ContentType: mediaContentType, Width: 1, Height: 1, SizeBytes: int64(len(mediaBytes)), CreatedAt: createdAt}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.CaptureDraft{ID: f.DraftID, TenantID: f.TenantID, ResponsibilityID: f.ResponsibilityID, Kind: "INSPECTION", TemplateVersionID: f.TemplateVersionID, ReferencePayload: json.RawMessage(`{}`), PolicyPayload: json.RawMessage(`{}`), Requirements: templateJSON, Status: "SUBMITTED", Version: 1, CreatedAt: createdAt, UpdatedAt: createdAt}).Error; err != nil {
			return err
		}
		mediaIDs, _ := json.Marshal([]identity.ID{f.MediaID})
		if err := tx.Create(&database.RequirementAnswer{ID: identity.NewID(), TenantID: f.TenantID, DraftID: f.DraftID, RequirementKey: options.RequirementKey, MediaIDs: mediaIDs, Flags: json.RawMessage(`[]`), Version: 1, CreatedAt: createdAt, UpdatedAt: createdAt}).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return Task06Fixture{}, fmt.Errorf("seed task06 fixture: %w", err)
	}
	return f, nil
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func fixturePNG() []byte {
	data, err := hex.DecodeString("89504e470d0a1a0a0000000d49484452000000010000000108060000001f15c4890000000d49444154789c6360000000020001e221bc330000000049454e44ae426082")
	if err != nil {
		panic(err)
	}
	return data
}
