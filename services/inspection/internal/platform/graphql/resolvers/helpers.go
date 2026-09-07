package resolvers

import (
	"context"
	"encoding/json"
	"time"

	"inspection/libs/identity"
	assetcore "inspection/services/inspection/internal/features/assets/core"
	inspectioncore "inspection/services/inspection/internal/features/inspections/core"
	participantcore "inspection/services/inspection/internal/features/participants/core"
	projectcore "inspection/services/inspection/internal/features/projects/core"
	"inspection/services/inspection/internal/features/templates/catalog"
	templatecore "inspection/services/inspection/internal/features/templates/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	graphql1 "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/security"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func unauthenticated() error {
	return apperror.New(apperror.Unauthenticated, "", "authentication required")
}

func invalidID(field string) error {
	return apperror.New(apperror.InvalidInput, field, "invalid identifier")
}

func graphql1Error(field string) error {
	return apperror.New(apperror.InvalidInput, field, "invalid value")
}

func internalRole(meta requestctx.Metadata, allowed ...string) error {
	for _, role := range meta.Principal.Roles {
		for _, candidate := range allowed {
			if role == candidate {
				return nil
			}
		}
	}
	return apperror.New(apperror.Forbidden, "", "access denied")
}

func intValue(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func mapMembership(ctx context.Context, db *gorm.DB, row database.Membership) (*graphql1.Membership, error) {
	var rows []database.ResourceScope
	if err := (tenanttx.Runner{DB: db}).Within(ctx, row.TenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id = ? AND membership_id = ?", row.TenantID, row.ID).Order("kind, resource_id").Find(&rows).Error
	}); err != nil {
		return nil, err
	}
	scopes := make([]*graphql1.Scope, 0, len(rows))
	for _, scope := range rows {
		scopes = append(scopes, &graphql1.Scope{Kind: scope.Kind, ResourceID: scope.ResourceID.String()})
	}
	return &graphql1.Membership{ID: row.ID.String(), TenantID: row.TenantID.String(), Role: row.Role, Status: row.Status, Version: int(row.Version), Scopes: scopes}, nil
}

func pageInfo(cursor string, more bool) *graphql1.PageInfo {
	var end *string
	if cursor != "" {
		end = &cursor
	}
	return &graphql1.PageInfo{EndCursor: end, HasNextPage: more}
}

func (r *mutationResolver) mutateLegalHold(ctx context.Context, input graphql1.LegalHoldInput, active bool) (*graphql1.RetentionMutationPayload, error) {
	meta, ok := requestctx.FromContext(ctx)
	if !ok {
		return nil, unauthenticated()
	}
	if err := internalRole(meta, auth.TenantAdmin, auth.Manager); err != nil {
		return nil, err
	}
	inspectionID, err := identity.ParseID(input.InspectionID)
	if err != nil {
		return nil, invalidID("inspectionId")
	}
	now := time.Now().UTC()
	if active {
		var row database.LegalHold
		err := withTask06Tenant(ctx, r.DB, meta.TenantID, func(tx *gorm.DB) error {
			if err := lockInspectionForLegalHold(tx, meta.TenantID, inspectionID); err != nil {
				return err
			}
			if err := tx.Session(&gorm.Session{}).Where("inspection_id=? AND active=true", inspectionID).First(&row).Error; err == nil {
				return nil
			} else if err != gorm.ErrRecordNotFound {
				return err
			}
			row = database.LegalHold{ID: identity.NewID(), TenantID: meta.TenantID, InspectionID: inspectionID, Reason: input.Reason, Active: true, CreatedAt: now}
			return tx.Session(&gorm.Session{}).Create(&row).Error
		})
		if err != nil {
			return nil, err
		}
		return &graphql1.RetentionMutationPayload{Status: "HELD", UserErrors: []*graphql1.UserError{}, ClientMutationID: input.ClientMutationID}, nil
	}
	if err := withTask06Tenant(ctx, r.DB, meta.TenantID, func(tx *gorm.DB) error {
		if err := lockInspectionForLegalHold(tx, meta.TenantID, inspectionID); err != nil {
			return err
		}
		return tx.Model(&database.LegalHold{}).Where("inspection_id=? AND active", inspectionID).Updates(map[string]any{"active": false, "released_at": now}).Error
	}); err != nil {
		return nil, err
	}
	return &graphql1.RetentionMutationPayload{Status: "RELEASED", UserErrors: []*graphql1.UserError{}, ClientMutationID: input.ClientMutationID}, nil
}

func lockInspectionForLegalHold(tx *gorm.DB, tenantID, inspectionID identity.ID) error {
	var inspection database.Inspection
	return tx.Session(&gorm.Session{}).Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=?", tenantID, inspectionID).First(&inspection).Error
}

func firstRole(roles []string) string {
	if len(roles) == 0 {
		return ""
	}
	return roles[0]
}

func membershipStatus(disabled bool) string {
	if disabled {
		return "DISABLED"
	}
	return "ACTIVE"
}

func optionalID(value *identity.ID) *string {
	if value == nil {
		return nil
	}
	result := value.String()
	return &result
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func mapParticipant(view participantcore.ParticipantView) *graphql1.Participant {
	contacts := make([]*graphql1.ParticipantContact, 0, len(view.Contacts))
	for _, contact := range view.Contacts {
		contacts = append(contacts, &graphql1.ParticipantContact{ID: contact.ID.String(), Channel: contact.Channel, Value: contact.Value, Active: contact.Active, Verified: view.Verified[contact.ID]})
	}
	selected := make([]string, 0, len(view.Selected))
	for _, id := range view.Selected {
		selected = append(selected, id.String())
	}
	row := view.Participant
	return &graphql1.Participant{ID: row.ID.String(), BusinessUnitID: row.BusinessUnitID.String(), Name: row.Name, SegmentRole: row.SegmentRole, Status: row.Status, Version: int(row.Version), Contacts: contacts, SelectedContactIds: selected}
}

func mapSegment(row database.SegmentDefinition) *graphql1.SegmentDefinition {
	return &graphql1.SegmentDefinition{ID: row.ID.String(), Key: row.Key, Name: row.Name, ActiveVersionID: optionalID(row.ActiveVersionID), Version: int(row.Version)}
}

func mapSegmentVersion(row database.SegmentDefinitionVersion) *graphql1.SegmentDefinitionVersion {
	schema := map[string]any{}
	uiSchema := map[string]any{}
	_ = json.Unmarshal(row.SchemaJSON, &schema)
	_ = json.Unmarshal(row.UISchemaJSON, &uiSchema)
	return &graphql1.SegmentDefinitionVersion{ID: row.ID.String(), DefinitionID: row.DefinitionID.String(), VersionNumber: row.VersionNumber, SchemaVersion: row.SchemaVersion, Schema: schema, UISchema: uiSchema, CanonicalDigest: row.CanonicalDigest, Status: row.Status, PublishedAt: row.PublishedAt.Format(time.RFC3339Nano)}
}

func mapTemplate(row database.Template) *graphql1.Template {
	return &graphql1.Template{ID: row.ID.String(), Key: row.Key, Name: row.Name, SegmentVersionID: row.SegmentVersionID.String(), ActiveVersionID: optionalID(row.ActiveVersionID), Version: int(row.Version)}
}

func mapTemplateVersion(row database.TemplateVersion) *graphql1.TemplateVersion {
	definition := map[string]any{}
	_ = json.Unmarshal(row.DefinitionJSON, &definition)
	return &graphql1.TemplateVersion{ID: row.ID.String(), TemplateID: row.TemplateID.String(), VersionNumber: row.VersionNumber, SchemaVersion: row.SchemaVersion, Definition: definition, CanonicalDigest: row.CanonicalDigest, Status: row.Status, PublishedAt: row.PublishedAt.Format(time.RFC3339Nano)}
}

func mapTemplatePayload(view templatecore.View, mutationID string) *graphql1.TemplatePayload {
	return &graphql1.TemplatePayload{Template: mapTemplate(view.Template), Version: mapTemplateVersion(view.Version), UserErrors: []*graphql1.UserError{}, ClientMutationID: mutationID}
}

func mapAssetInput(tenantID identity.ID, input graphql1.AssetInput, idempotencyKey string) (assetcore.Input, error) {
	unitID, err := identity.ParseID(input.BusinessUnitID)
	if err != nil {
		return assetcore.Input{}, invalidID("businessUnitId")
	}
	segmentID, err := identity.ParseID(input.SegmentVersionID)
	if err != nil {
		return assetcore.Input{}, invalidID("segmentVersionId")
	}
	var templateID *identity.ID
	if input.TemplateID != nil {
		parsed, parseErr := identity.ParseID(*input.TemplateID)
		if parseErr != nil {
			return assetcore.Input{}, invalidID("templateId")
		}
		templateID = &parsed
	}
	assignments := make([]assetcore.AssignmentInput, 0, len(input.Assignments))
	for _, assignment := range input.Assignments {
		if assignment == nil {
			return assetcore.Input{}, graphql1Error("assignments")
		}
		participantID, parseErr := identity.ParseID(assignment.ParticipantID)
		if parseErr != nil {
			return assetcore.Input{}, invalidID("assignments")
		}
		assignments = append(assignments, assetcore.AssignmentInput{ParticipantID: participantID, Role: assignment.Role})
	}
	geofence := 150
	if input.GeofenceMeters != nil {
		geofence = *input.GeofenceMeters
	}
	policy := catalog.PolicyOverride{}
	raw, _ := json.Marshal(input.PolicyOverrides)
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &policy); err != nil {
			return assetcore.Input{}, graphql1Error("policyOverrides")
		}
	}
	return assetcore.Input{TenantID: tenantID, BusinessUnitID: unitID, SegmentVersionID: segmentID, TemplateID: templateID, Name: input.Name, ExternalKey: input.ExternalKey, Address: input.Address, LatitudeE6: int32Pointer(input.LatitudeE6), LongitudeE6: int32Pointer(input.LongitudeE6), GeofenceMeters: geofence, Attributes: input.Attributes, PolicyOverrides: policy, Assignments: assignments, IdempotencyKey: idempotencyKey}, nil
}

func parseOptionalID(value *string, field string) (*identity.ID, error) {
	if value == nil || *value == "" {
		return nil, nil
	}
	parsed, err := identity.ParseID(*value)
	if err != nil {
		return nil, invalidID(field)
	}
	return &parsed, nil
}

func parseInstant(value string, field string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, graphql1Error(field)
	}
	return parsed.UTC(), nil
}

func parseInstants(values []string, field string) ([]time.Time, error) {
	result := make([]time.Time, 0, len(values))
	for _, value := range values {
		parsed, err := parseInstant(value, field)
		if err != nil {
			return nil, err
		}
		result = append(result, parsed)
	}
	return result, nil
}

func int32Pointer(value *int) *int32 {
	if value == nil {
		return nil
	}
	result := int32(*value)
	return &result
}

func mapAsset(view assetcore.View) *graphql1.Asset {
	attributes, policies := map[string]any{}, map[string]any{}
	_ = json.Unmarshal(view.Attributes.AttributesJSON, &attributes)
	_ = json.Unmarshal(view.Asset.PolicyOverrides, &policies)
	assignments := make([]*graphql1.AssetAssignment, 0, len(view.Assignments))
	for _, assignment := range view.Assignments {
		assignments = append(assignments, &graphql1.AssetAssignment{ParticipantID: assignment.ParticipantID.String(), Role: assignment.Role, Active: assignment.Active})
	}
	var latitude, longitude *int
	if view.Asset.LatitudeE6 != nil {
		value := int(*view.Asset.LatitudeE6)
		latitude = &value
	}
	if view.Asset.LongitudeE6 != nil {
		value := int(*view.Asset.LongitudeE6)
		longitude = &value
	}
	return &graphql1.Asset{ID: view.Asset.ID.String(), BusinessUnitID: view.Asset.BusinessUnitID.String(), SegmentVersionID: view.Asset.SegmentVersionID.String(), TemplateID: optionalID(view.Asset.TemplateID), Name: view.Asset.Name, ExternalKey: view.Asset.ExternalKey, Address: view.Asset.Address, LatitudeE6: latitude, LongitudeE6: longitude, GeofenceMeters: view.Asset.GeofenceMeters, Attributes: attributes, PolicyOverrides: policies, Status: view.Asset.Status, Version: int(view.Asset.Version), Assignments: assignments}
}

func mapOriginVersion(row database.OriginVersion) *graphql1.OriginVersion {
	var activated *string
	if row.ActivatedAt != nil {
		value := row.ActivatedAt.Format(time.RFC3339Nano)
		activated = &value
	}
	return &graphql1.OriginVersion{ID: row.ID.String(), OriginID: row.OriginID.String(), VersionNumber: row.VersionNumber, Status: row.Status, SupersedesID: optionalID(row.SupersedesID), ActivatedAt: activated}
}

func mapSchedule(row database.Schedule) *graphql1.Schedule {
	var offsets []int
	_ = json.Unmarshal(row.ReminderOffsets, &offsets)
	return &graphql1.Schedule{ID: row.ID.String(), BusinessUnitID: row.BusinessUnitID.String(), AssetID: row.AssetID.String(), ParticipantID: row.ParticipantID.String(), TemplateID: row.TemplateID.String(), ReferenceVersionID: optionalID(row.ReferenceVersionID), Rrule: row.RRule, Timezone: row.Timezone, StartsAt: row.StartsAt.Format(time.RFC3339Nano), NextDueAt: row.NextDueAt.Format(time.RFC3339Nano), DeadlineMinutes: row.DeadlineMinutes, ReminderOffsetsMinutes: offsets, Status: row.Status, Version: int(row.Version)}
}

func mapInspection(view inspectioncore.View) *graphql1.Inspection {
	row := view.Inspection
	var reminders []string
	_ = json.Unmarshal(row.ReminderInstants, &reminders)
	return &graphql1.Inspection{ID: row.ID.String(), BusinessUnitID: row.BusinessUnitID.String(), AssetID: row.AssetID.String(), ParticipantID: row.ParticipantID.String(), TemplateID: row.TemplateID.String(), TemplateVersionID: row.TemplateVersionID.String(), AnalysisProfileVersionID: row.AnalysisProfileVersionID.String(), ProjectID: optionalID(row.ProjectID), StageID: optionalID(row.StageID), Source: row.Source, SourceReason: optionalString(row.SourceReason), StateReason: optionalString(row.StateReason), Status: row.Status, EvidenceCount: row.EvidenceCount, DueAt: row.DueAt.Format(time.RFC3339Nano), DeadlineAt: row.DeadlineAt.Format(time.RFC3339Nano), ReminderInstants: reminders, Version: int(row.Version)}
}

func mapProject(view projectcore.View) *graphql1.Project {
	stages := make([]*graphql1.ProjectStage, 0, len(view.Stages))
	for _, stage := range view.Stages {
		var planned *string
		if stage.PlannedAt != nil {
			value := stage.PlannedAt.Format(time.RFC3339Nano)
			planned = &value
		}
		stages = append(stages, &graphql1.ProjectStage{ID: stage.ID.String(), Key: stage.Key, Label: stage.Label, Kind: stage.Kind, Position: stage.Position, Status: stage.Status, PlannedAt: planned, Reason: optionalString(stage.Reason), InspectionID: optionalID(stage.InspectionID), Version: int(stage.Version)})
	}
	transitions := make([]*graphql1.StageTransition, 0, len(view.Transitions))
	for _, transition := range view.Transitions {
		transitions = append(transitions, &graphql1.StageTransition{ID: transition.ID.String(), StageID: optionalID(transition.StageID), FromState: optionalString(transition.FromState), ToState: transition.ToState, Reason: optionalString(transition.Reason), OccurredAt: transition.OccurredAt.Format(time.RFC3339Nano)})
	}
	row := view.Project
	return &graphql1.Project{ID: row.ID.String(), BusinessUnitID: row.BusinessUnitID.String(), AssetID: row.AssetID.String(), ParticipantID: row.ParticipantID.String(), TemplateID: row.TemplateID.String(), TemplateVersionID: row.TemplateVersionID.String(), ReportMode: row.ReportMode, Status: row.Status, Version: int(row.Version), Stages: stages, Transitions: transitions}
}

func mapMedia(row database.MediaObject) *graphql1.Media {
	var flags []string
	_ = json.Unmarshal(row.Flags, &flags)
	return &graphql1.Media{ID: row.ID.String(), Status: row.Status, RequirementKey: optionalString(row.RequirementKey), Description: optionalString(row.Description), CaptureSource: optionalString(row.CaptureSource), Flags: flags, ReplacesMediaID: optionalID(row.ReplacesMediaID)}
}

func (r *Resolver) externalResponsibility(ctx context.Context) (identity.ID, identity.ID, error) {
	credentials, ok := requestctx.ExternalCredentialsFromContext(ctx)
	if !ok {
		return identity.ID{}, identity.ID{}, unauthenticated()
	}
	responsibilityID, err := r.Invitations.ValidateSession(ctx, credentials.SessionToken, credentials.CSRFToken)
	if err != nil {
		return identity.ID{}, identity.ID{}, err
	}
	tenantID, parseErr := security.ParseScopedToken(credentials.SessionToken)
	if parseErr != nil {
		return identity.ID{}, identity.ID{}, unauthenticated()
	}
	return tenantID, responsibilityID, nil
}
