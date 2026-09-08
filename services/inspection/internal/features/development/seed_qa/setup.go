// Package seed_qa creates a small, disposable local dataset for browser QA.
package seed_qa

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"inspection/libs/identity"
	capturecore "inspection/services/inspection/internal/features/capture/core"
	invitationcore "inspection/services/inspection/internal/features/invitations/core"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/security"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const sliceName = "development/seed_qa"

// ErrLocalOnboardingMissing tells the local seed command that it may bootstrap
// the first Keycloak identity before retrying the QA scenario.
var ErrLocalOnboardingMissing = errors.New("local admin onboarding missing")

// Setup adds one fresh end-to-end QA scenario to the first active local admin
// onboarding. Catalog rows are stable across runs; capture scenarios are not.
func Setup(ctx context.Context, db *gorm.DB, issuer, captureBaseURL string) (string, error) {
	if db == nil || strings.TrimSpace(issuer) == "" || strings.TrimSpace(captureBaseURL) == "" {
		return "", fmt.Errorf("slice %s: missing dependency", sliceName)
	}

	var membership database.Membership
	if err := db.WithContext(ctx).
		Where("issuer = ? AND role = ? AND status = ?", issuer, "TENANT_ADMIN", "ACTIVE").
		Order("created_at ASC").First(&membership).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return "", fmt.Errorf("%w: slice %s: local admin onboarding not found", ErrLocalOnboardingMissing, sliceName)
		}
		return "", fmt.Errorf("slice %s: find onboarding: %w", sliceName, err)
	}

	var tenant database.Tenant
	if err := db.WithContext(ctx).Where("id = ? AND status = ?", membership.TenantID, "ACTIVE").First(&tenant).Error; err != nil {
		return "", fmt.Errorf("slice %s: find active tenant: %w", sliceName, err)
	}
	var unit database.BusinessUnit
	if err := db.WithContext(ctx).Where("tenant_id = ? AND status = ?", tenant.ID, "ACTIVE").Order("created_at ASC").First(&unit).Error; err != nil {
		return "", fmt.Errorf("slice %s: find active business unit: %w", sliceName, err)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	ids := stableIDs(tenant.ID)
	requirements := []capturecore.Requirement{{
		Key:                  "fachada-geral",
		Section:              "Área externa",
		Label:                "Fachada do imóvel",
		Instructions:         "Registre a fachada inteira, com boa iluminação.",
		EvidenceKind:         "PHOTO",
		Required:             true,
		ImpossibilityAllowed: true,
		MinimumMedia:         1,
		MaximumMedia:         3,
		DescriptionRequired:  true,
		CaptureSourcePolicy:  "CAMERA_OR_GALLERY",
		ComparisonTarget:     "FIXED_ORIGIN",
	}}
	requirementJSON, err := json.Marshal(requirements)
	if err != nil {
		return "", fmt.Errorf("slice %s: encode requirements: %w", sliceName, err)
	}
	segmentJSON := json.RawMessage(`{"type":"object","properties":{"propertyType":{"type":"string"}}}`)
	templateJSON := json.RawMessage(requirementJSON)
	profileJSON := json.RawMessage(`{"normalMaximum":0.25,"attentionMaximum":0.60}`)

	token, err := security.NewScopedToken(tenant.ID)
	if err != nil {
		return "", fmt.Errorf("slice %s: create invitation token: %w", sliceName, err)
	}
	tokenHash := security.HashToken(token)
	scenarioID := identity.NewID()
	inspectionID, responsibilityID := identity.NewID(), identity.NewID()
	projectID, stageID, draftID, invitationID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	deadline := now.Add(24 * time.Hour)
	deliveryIntents, _ := json.Marshal([]invitationcore.DeliveryIntent{{Channel: "EMAIL", Destination: "qa.inspection@example.test"}})

	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		stableRows := []any{
			&database.Participant{ID: ids.participant, TenantID: tenant.ID, BusinessUnitID: unit.ID, Name: "Participante QA", SegmentRole: "OWNER", Status: "ACTIVE", Version: 1, IdempotencyKey: "qa-seed-participant", CreatedAt: now, UpdatedAt: now},
			&database.ParticipantContact{ID: ids.contact, TenantID: tenant.ID, ParticipantID: ids.participant, Channel: "EMAIL", Value: "qa.inspection@example.test", Normalized: "qa.inspection@example.test", Active: true, CreatedAt: now, UpdatedAt: now},
			&database.ContactVerification{ID: ids.verification, TenantID: tenant.ID, ContactID: ids.contact, IdempotencyKey: "qa-seed-verification", Status: "VERIFIED", VerifiedAt: &now, CreatedAt: now},
			&database.ChannelSelection{ID: ids.channelSelection, TenantID: tenant.ID, ParticipantID: ids.participant, ContactID: ids.contact, CreatedAt: now},
			&database.SegmentDefinition{ID: ids.segment, TenantID: tenant.ID, Key: "qa-property", Name: "Imóvel QA", ActiveVersionID: &ids.segmentVersion, Version: 1, CreatedAt: now, UpdatedAt: now},
			&database.SegmentDefinitionVersion{ID: ids.segmentVersion, TenantID: tenant.ID, DefinitionID: ids.segment, VersionNumber: 1, SchemaVersion: 1, SchemaJSON: segmentJSON, UISchemaJSON: json.RawMessage(`{}`), CanonicalDigest: digest(segmentJSON), Status: "ACTIVE", IdempotencyKey: "qa-seed-segment", PublishedAt: now, CreatedBy: membership.IdentityID},
			&database.Template{ID: ids.template, TenantID: tenant.ID, Key: "qa-basic-inspection", Name: "Inspeção básica QA", SegmentVersionID: ids.segmentVersion, ActiveVersionID: &ids.templateVersion, Version: 1, CreatedAt: now, UpdatedAt: now},
			&database.TemplateVersion{ID: ids.templateVersion, TenantID: tenant.ID, TemplateID: ids.template, VersionNumber: 1, SchemaVersion: 1, DefinitionJSON: templateJSON, CanonicalDigest: digest(templateJSON), Status: "ACTIVE", IdempotencyKey: "qa-seed-template", PublishedAt: now, CreatedBy: membership.IdentityID},
			&database.AnalysisProfile{ID: ids.profile, TenantID: tenant.ID, Key: "qa-default", ActiveVersionID: ids.profileVersion, Version: 1, CreatedAt: now, UpdatedAt: now},
			&database.AnalysisProfileVersion{ID: ids.profileVersion, TenantID: tenant.ID, ProfileID: ids.profile, Key: "qa-default", VersionNumber: 1, SchemaVersion: 1, DefinitionJSON: profileJSON, CanonicalDigest: digest(profileJSON), Status: "ACTIVE", IdempotencyKey: "qa-seed-profile", PublishedAt: now, CreatedBy: membership.IdentityID},
			&database.Asset{ID: ids.asset, TenantID: tenant.ID, BusinessUnitID: unit.ID, SegmentVersionID: ids.segmentVersion, TemplateID: &ids.template, Name: "Imóvel QA", ExternalKey: "QA-001", Address: "Rua de Teste, 100", GeofenceMeters: 150, PolicyOverrides: json.RawMessage(`{"allowGallery":true}`), Status: "ACTIVE", Version: 1, IdempotencyKey: "qa-seed-asset", CreatedAt: now, UpdatedAt: now},
			&database.AssetAttributeVersion{ID: ids.assetAttributes, TenantID: tenant.ID, AssetID: ids.asset, SegmentVersionID: ids.segmentVersion, VersionNumber: 1, AttributesJSON: json.RawMessage(`{"propertyType":"residential"}`), CanonicalDigest: digest([]byte(`{"propertyType":"residential"}`)), CreatedAt: now},
			&database.AssetAssignment{ID: ids.assetAssignment, TenantID: tenant.ID, AssetID: ids.asset, ParticipantID: ids.participant, Role: "OWNER", Active: true, CreatedAt: now},
		}
		for _, row := range stableRows {
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(row).Error; err != nil {
				return err
			}
		}

		project := database.Project{ID: projectID, TenantID: tenant.ID, BusinessUnitID: unit.ID, AssetID: ids.asset, ParticipantID: ids.participant, TemplateID: ids.template, TemplateVersionID: ids.templateVersion, ReportMode: "CURRENT", Status: "ACTIVE", Version: 1, IdempotencyKey: "qa-seed-project-" + scenarioID.String(), CreatedAt: now, UpdatedAt: now}
		stage := database.ProjectStage{ID: stageID, TenantID: tenant.ID, ProjectID: projectID, Key: "inspection", Label: "Inspeção inicial", Kind: "INSPECTION", Position: 1, Status: "AVAILABLE", Requirements: requirementJSON, EffectiveReference: json.RawMessage(`{}`), InspectionID: &inspectionID, Version: 1, IdempotencyKey: "qa-seed-stage-" + scenarioID.String(), CreatedAt: now, UpdatedAt: now}
		inspection := database.Inspection{ID: inspectionID, TenantID: tenant.ID, BusinessUnitID: unit.ID, AssetID: ids.asset, ParticipantID: ids.participant, TemplateID: ids.template, TemplateVersionID: ids.templateVersion, AnalysisProfileVersionID: ids.profileVersion, ProjectID: &projectID, StageID: &stageID, Source: "MANUAL", SourceKey: "qa-seed-" + scenarioID.String(), SourceReason: "Browser QA scenario", Status: "INVITED", DueAt: now, DeadlineAt: deadline, ReminderInstants: json.RawMessage(`[]`), ContextSnapshot: json.RawMessage(`{"source":"qa-seed"}`), Version: 1, CreatedAt: now, UpdatedAt: now}
		rows := []any{
			&project,
			&stage,
			&inspection,
			&database.Responsibility{ID: responsibilityID, TenantID: tenant.ID, InspectionID: inspectionID, ParticipantID: ids.participant, Status: "PENDING", Version: 1, CreatedAt: now, UpdatedAt: now},
			&database.ReferenceSnapshot{ID: identity.NewID(), TenantID: tenant.ID, InspectionID: inspectionID, ComparisonMode: "FIXED_ORIGIN", Payload: json.RawMessage(`{}`), CreatedAt: now},
			&database.PolicySnapshot{ID: identity.NewID(), TenantID: tenant.ID, InspectionID: inspectionID, SchemaVersion: 1, Payload: json.RawMessage(`{"gpsRequired":false,"allowGallery":true,"geofenceMeters":150}`), CanonicalDigest: digest([]byte(`{"gpsRequired":false,"allowGallery":true,"geofenceMeters":150}`)), CreatedAt: now},
			&database.CaptureDraft{ID: draftID, TenantID: tenant.ID, ResponsibilityID: responsibilityID, Kind: "INSPECTION", TemplateVersionID: ids.templateVersion, ReferencePayload: json.RawMessage(`{}`), PolicyPayload: json.RawMessage(`{"gpsRequired":false,"allowGallery":true,"geofenceMeters":150}`), Requirements: requirementJSON, Status: "OPEN", Version: 1, CreatedAt: now, UpdatedAt: now},
			&database.Invitation{ID: invitationID, TenantID: tenant.ID, ResponsibilityID: responsibilityID, TokenHash: tokenHash[:], DeliveryIntents: deliveryIntents, Status: "ACTIVE", ExpiresAt: deadline, CreatedAt: now},
			&database.DashboardInspection{ID: identity.NewID(), TenantID: tenant.ID, InspectionID: inspectionID, ProjectID: &projectID, AssetID: ids.asset, Classification: "ATTENTION", Status: "INVITED", Sequence: now.UnixNano(), UpdatedAt: now},
			&database.AuditEvent{ID: identity.NewID(), TenantID: tenant.ID, ActorID: membership.IdentityID, Action: "QA_SCENARIO_SEEDED", TargetType: "INSPECTION", TargetID: inspectionID.String(), Outcome: "SUCCESS", Reason: "Local browser QA", CorrelationID: "qa-seed-" + scenarioID.String(), OccurredAt: now},
			&database.RecipientNotification{ID: identity.NewID(), TenantID: tenant.ID, RecipientMembershipID: membership.ID, EventID: identity.NewID(), Kind: "INSPECTION_INVITED", Title: "Inspeção QA disponível", Body: "Uma inspeção de demonstração está pronta para captura.", ResourceKind: "INSPECTION", ResourceID: &inspectionID, CreatedAt: now},
		}
		for _, row := range rows {
			if err := tx.Create(row).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("slice %s: persist scenario: %w", sliceName, err)
	}

	return strings.TrimRight(captureBaseURL, "/") + "/capture/" + token, nil
}

type qaIDs struct {
	participant, contact, verification, channelSelection identity.ID
	segment, segmentVersion, template, templateVersion   identity.ID
	profile, profileVersion, asset, assetAttributes      identity.ID
	assetAssignment                                      identity.ID
}

func stableIDs(tenantID identity.ID) qaIDs {
	id := func(name string) identity.ID {
		return identity.NewDeterministicID("inspection/qa-seed/"+tenantID.String(), name)
	}
	return qaIDs{
		participant: id("participant"), contact: id("contact"), verification: id("verification"), channelSelection: id("channel-selection"),
		segment: id("segment"), segmentVersion: id("segment-version"), template: id("template"), templateVersion: id("template-version"),
		profile: id("profile"), profileVersion: id("profile-version"), asset: id("asset"), assetAttributes: id("asset-attributes"), assetAssignment: id("asset-assignment"),
	}
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
