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
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

// Task06Fixture is the smallest complete tenant-scoped graph used by the
// external Task 06 cases. Tests may extend it inside WithinTenant without
// depending on another test's rows.
type Task06Fixture struct {
	TenantID, IdentityID, MembershipID, BusinessUnitID identity.ID
	ParticipantID, ContactID                           identity.ID
	SegmentVersionID, TemplateID, TemplateVersionID    identity.ID
	ProfileID, ProfileVersionID, AssetID               identity.ID
	InspectionID, ResponsibilityID, DraftID            identity.ID
	MediaID, DerivativeID, ProjectID, StageID          identity.ID
}

type Task06FixtureOptions struct {
	ReportMode       string
	InspectionStatus string
	RequirementKey   string
}

func (o Task06FixtureOptions) normalized() Task06FixtureOptions {
	if o.ReportMode == "" {
		o.ReportMode = "HISTORICAL"
	}
	if o.InspectionStatus == "" {
		o.InspectionStatus = "SUBMITTED"
	}
	if o.RequirementKey == "" {
		o.RequirementKey = "room-1"
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
		ProfileID: identity.NewID(), ProfileVersionID: identity.NewID(), AssetID: identity.NewID(), InspectionID: identity.NewID(), ResponsibilityID: identity.NewID(),
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
		profileJSON := json.RawMessage(`{"threshold":"deterministic-v1"}`)
		if err := tx.Create(&database.AnalysisProfile{ID: f.ProfileID, TenantID: f.TenantID, Key: "task06-profile", ActiveVersionID: f.ProfileVersionID, Version: 1, CreatedAt: createdAt, UpdatedAt: createdAt}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.AnalysisProfileVersion{ID: f.ProfileVersionID, TenantID: f.TenantID, ProfileID: f.ProfileID, Key: "task06-profile", VersionNumber: 1, SchemaVersion: 1, DefinitionJSON: profileJSON, CanonicalDigest: digest(profileJSON), Status: "ACTIVE", IdempotencyKey: "task06-profile", PublishedAt: createdAt, CreatedBy: f.IdentityID}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.Asset{ID: f.AssetID, TenantID: f.TenantID, BusinessUnitID: f.BusinessUnitID, SegmentVersionID: f.SegmentVersionID, TemplateID: &f.TemplateID, Name: "Task 06 asset", ExternalKey: "task06-asset", Address: "Test address", GeofenceMeters: 150, PolicyOverrides: json.RawMessage(`{}`), Status: "ACTIVE", Version: 1, IdempotencyKey: "task06-asset", CreatedAt: createdAt, UpdatedAt: createdAt}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.AssetAttributeVersion{ID: identity.NewID(), TenantID: f.TenantID, AssetID: f.AssetID, SegmentVersionID: f.SegmentVersionID, VersionNumber: 1, AttributesJSON: json.RawMessage(`{"name":"fixture"}`), CanonicalDigest: digest([]byte(`{"name":"fixture"}`)), CreatedAt: createdAt}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.AssetAssignment{ID: identity.NewID(), TenantID: f.TenantID, AssetID: f.AssetID, ParticipantID: f.ParticipantID, Role: "OWNER", Active: true, CreatedAt: createdAt}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.Project{ID: f.ProjectID, TenantID: f.TenantID, BusinessUnitID: f.BusinessUnitID, AssetID: f.AssetID, ParticipantID: f.ParticipantID, TemplateID: f.TemplateID, TemplateVersionID: f.TemplateVersionID, ReportMode: options.ReportMode, Status: "ACTIVE", Version: 1, IdempotencyKey: "task06-project", CreatedAt: createdAt, UpdatedAt: createdAt}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.ProjectStage{ID: f.StageID, TenantID: f.TenantID, ProjectID: f.ProjectID, Key: "stage-1", Label: "Fixture stage", Kind: "INSPECTION", Position: 1, Status: "COMPLETED", Requirements: templateJSON, EffectiveReference: json.RawMessage(`{}`), Version: 1, CreatedAt: createdAt, UpdatedAt: createdAt}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.Inspection{ID: f.InspectionID, TenantID: f.TenantID, BusinessUnitID: f.BusinessUnitID, AssetID: f.AssetID, ParticipantID: f.ParticipantID, TemplateID: f.TemplateID, TemplateVersionID: f.TemplateVersionID, AnalysisProfileVersionID: f.ProfileVersionID, ProjectID: &f.ProjectID, StageID: &f.StageID, Source: "MANUAL", SourceKey: "task06-inspection", SourceReason: "integration fixture", Status: options.InspectionStatus, EvidenceCount: 1, DueAt: createdAt, DeadlineAt: createdAt.Add(24 * time.Hour), ReminderInstants: json.RawMessage(`[]`), ContextSnapshot: json.RawMessage(`{}`), Version: 1, CreatedAt: createdAt, UpdatedAt: createdAt}).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.ProjectStage{}).Where("tenant_id=? AND id=?", f.TenantID, f.StageID).Update("inspection_id", f.InspectionID).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.Responsibility{ID: f.ResponsibilityID, TenantID: f.TenantID, InspectionID: f.InspectionID, ParticipantID: f.ParticipantID, Status: "SUBMITTED", Version: 1, CreatedAt: createdAt, UpdatedAt: createdAt}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.ReferenceSnapshot{ID: identity.NewID(), TenantID: f.TenantID, InspectionID: f.InspectionID, ComparisonMode: "FIXED_ORIGIN", Payload: json.RawMessage(`{}`), CreatedAt: createdAt}).Error; err != nil {
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
		if err := tx.Create(&database.MediaObject{ID: f.MediaID, TenantID: f.TenantID, ResponsibilityID: f.ResponsibilityID, ObjectKey: mediaKey, ContentType: mediaContentType, SHA256: mediaDigest, SizeBytes: int64(len(mediaBytes)), Status: "READY", IdempotencyKey: "task06-media", RequirementKey: options.RequirementKey, Description: "Fixture evidence", CaptureSource: "CAMERA", Flags: json.RawMessage(`[]`), CreatedAt: createdAt}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.MediaDerivative{ID: f.DerivativeID, TenantID: f.TenantID, MediaID: f.MediaID, ObjectKey: derivativeKey, Kind: "NORMALIZED", SHA256: mediaDigest, CreatedAt: createdAt}).Error; err != nil {
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
