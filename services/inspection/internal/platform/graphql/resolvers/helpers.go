package resolvers

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"inspection/libs/identity"
	assetaddress "inspection/services/inspection/internal/features/assets/address"
	assetcore "inspection/services/inspection/internal/features/assets/core"
	inspectioncore "inspection/services/inspection/internal/features/inspections/core"
	originlist "inspection/services/inspection/internal/features/origins/list_versions"
	originpromote "inspection/services/inspection/internal/features/origins/promote_inspection"
	participantcore "inspection/services/inspection/internal/features/participants/core"
	projectcore "inspection/services/inspection/internal/features/projects/core"
	retentioncore "inspection/services/inspection/internal/features/retention/core"
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

func parsePlannedStages(inputs []*graphql1.PlannedProjectStageInput) ([]projectcore.PlannedStageInput, error) {
	stages := make([]projectcore.PlannedStageInput, 0, len(inputs))
	for _, input := range inputs {
		if input == nil {
			return nil, apperror.New(apperror.InvalidInput, "stages", "stage cannot be empty")
		}
		stage := projectcore.PlannedStageInput{Key: input.Key, Label: input.Label}
		if input.PlannedAt != nil {
			planned, err := parseInstant(*input.PlannedAt, "stages.plannedAt")
			if err != nil {
				return nil, err
			}
			stage.PlannedAt = &planned
		}
		stages = append(stages, stage)
	}
	return stages, nil
}

func invalidID(field string) error {
	return apperror.New(apperror.InvalidInput, field, "invalid identifier")
}

func graphql1Error(field string) error {
	return apperror.New(apperror.InvalidInput, field, "invalid value")
}

func internalRole(meta requestctx.Metadata, allowed ...string) error {
	for _, role := range meta.Principal.Roles {
		if role == auth.TenantAdmin {
			return nil
		}
		for _, candidate := range allowed {
			if role == candidate {
				return nil
			}
		}
	}
	return apperror.New(apperror.Forbidden, "", "access denied")
}

func hasRole(meta requestctx.Metadata, wanted string) bool {
	for _, role := range meta.Principal.Roles {
		if role == wanted {
			return true
		}
	}
	return false
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

func intPointer(value int) *int { return &value }

func userErrorFrom(err error) *graphql1.UserError {
	code, field, message := apperror.Public(err)
	var fieldValue *string
	if field != "" {
		fieldValue = &field
	}
	return &graphql1.UserError{Code: string(code), Field: fieldValue, Message: message}
}

func maskEmail(value string) string {
	at := strings.LastIndex(value, "@")
	if at <= 0 {
		return "***"
	}
	local, domain := value[:at], value[at+1:]
	if len(local) == 1 {
		return "* @" + domain
	}
	return local[:1] + "***@" + domain
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
	return &graphql1.Membership{ID: row.ID.String(), TenantID: row.TenantID.String(), Name: row.Name, Email: row.Email, Role: row.Role, Status: row.Status, InvitationStatus: row.InvitationStatus, Version: int(row.Version), Scopes: scopes}, nil
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
	scope, err := parseRetentionScope(input.InspectionID, input.SourceMediaID, input.OriginVersionID)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	if active {
		var row database.LegalHold
		err := withTask06Tenant(ctx, r.DB, meta.TenantID, func(tx *gorm.DB) error {
			if err := lockRetentionScope(tx, meta.TenantID, scope); err != nil {
				return err
			}
			query := retentionScopeQuery(tx.Session(&gorm.Session{NewDB: true}).Where("tenant_id=?", meta.TenantID), scope)
			if err := query.Where("active=true").First(&row).Error; err == nil {
				return nil
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			row = database.LegalHold{ID: identity.NewID(), TenantID: meta.TenantID, InspectionID: scope.inspectionID, SourceMediaID: scope.sourceMediaID, OriginVersionID: scope.originVersionID, Reason: input.Reason, Active: true, CreatedAt: now}
			return tx.Session(&gorm.Session{NewDB: true}).Create(&row).Error
		})
		if err != nil {
			return nil, err
		}
		return &graphql1.RetentionMutationPayload{Status: "HELD", UserErrors: []*graphql1.UserError{}, ClientMutationID: input.ClientMutationID}, nil
	}
	if err := withTask06Tenant(ctx, r.DB, meta.TenantID, func(tx *gorm.DB) error {
		if err := lockRetentionScope(tx, meta.TenantID, scope); err != nil {
			return err
		}
		return retentionScopeQuery(tx.Session(&gorm.Session{NewDB: true}).Model(&database.LegalHold{}).Where("tenant_id=?", meta.TenantID), scope).Where("active = ?", true).Updates(map[string]any{"active": false, "released_at": now}).Error
	}); err != nil {
		return nil, err
	}
	return &graphql1.RetentionMutationPayload{Status: "RELEASED", UserErrors: []*graphql1.UserError{}, ClientMutationID: input.ClientMutationID}, nil
}

type retentionScope struct {
	inspectionID, sourceMediaID, originVersionID *identity.ID
}

func parseRetentionScope(inspection, source, origin *string) (retentionScope, error) {
	values := []*string{inspection, source, origin}
	fields := []string{"inspectionId", "sourceMediaId", "originVersionId"}
	count := 0
	var scope retentionScope
	for index, value := range values {
		if value == nil || strings.TrimSpace(*value) == "" {
			continue
		}
		count++
		id, err := identity.ParseID(strings.TrimSpace(*value))
		if err != nil {
			return retentionScope{}, invalidID(fields[index])
		}
		parsed := id
		switch index {
		case 0:
			scope.inspectionID = &parsed
		case 1:
			scope.sourceMediaID = &parsed
		case 2:
			scope.originVersionID = &parsed
		}
	}
	if count != 1 {
		return retentionScope{}, apperror.New(apperror.InvalidInput, "scope", "exactly one retention scope is required")
	}
	return scope, nil
}

func retentionScopeQuery(tx *gorm.DB, scope retentionScope) *gorm.DB {
	switch {
	case scope.inspectionID != nil:
		return tx.Where("inspection_id=? AND source_media_id IS NULL AND origin_version_id IS NULL", *scope.inspectionID)
	case scope.sourceMediaID != nil:
		return tx.Where("source_media_id=? AND inspection_id IS NULL AND origin_version_id IS NULL", *scope.sourceMediaID)
	default:
		return tx.Where("origin_version_id=? AND inspection_id IS NULL AND source_media_id IS NULL", *scope.originVersionID)
	}
}

func lockRetentionScope(tx *gorm.DB, tenantID identity.ID, scope retentionScope) error {
	switch {
	case scope.inspectionID != nil:
		return lockInspectionForLegalHold(tx, tenantID, *scope.inspectionID)
	case scope.sourceMediaID != nil:
		var source database.VideoSource
		if err := tx.Session(&gorm.Session{}).Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND media_id=?", tenantID, *scope.sourceMediaID).Take(&source).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			return apperror.New(apperror.NotFound, "sourceMediaId", "video source not found")
		}
		if source.Status == "DELETING" {
			return apperror.New(apperror.Conflict, "sourceMediaId", "source deletion is already in progress")
		}
		return nil
	default:
		var version database.OriginVersion
		if err := tx.Session(&gorm.Session{}).Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=?", tenantID, *scope.originVersionID).Take(&version).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			return apperror.New(apperror.NotFound, "originVersionId", "origin version not found")
		}
		return nil
	}
}

func rejectKnownDeletionBlock(tx *gorm.DB, tenantID identity.ID, scope retentionScope) error {
	switch {
	case scope.sourceMediaID != nil:
		held, err := retentioncore.EffectiveSourceHeld(tx, tenantID, *scope.sourceMediaID)
		if err != nil {
			return err
		}
		if held {
			return apperror.New(apperror.InvalidState, "sourceMediaId", "legal hold prevents source deletion")
		}
	case scope.originVersionID != nil:
		var version database.OriginVersion
		if err := tx.Session(&gorm.Session{}).Where("tenant_id=? AND id=?", tenantID, *scope.originVersionID).Take(&version).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			return apperror.New(apperror.NotFound, "originVersionId", "origin version not found")
		}
		var holds int64
		if err := tx.Model(&database.LegalHold{}).Where("tenant_id=? AND origin_version_id=? AND active=true", tenantID, *scope.originVersionID).Count(&holds).Error; err != nil {
			return err
		}
		if holds > 0 || version.Status == "ACTIVE" {
			return apperror.New(apperror.InvalidState, "originVersionId", "active hold or origin use prevents deletion")
		}
	case scope.inspectionID != nil:
		var holds int64
		if err := tx.Model(&database.LegalHold{}).Where("tenant_id=? AND inspection_id=? AND active=true", tenantID, *scope.inspectionID).Count(&holds).Error; err != nil {
			return err
		}
		if holds > 0 {
			return apperror.New(apperror.InvalidState, "inspectionId", "legal hold prevents inspection purge")
		}
		var livePlanFrames int64
		if err := tx.Session(&gorm.Session{NewDB: true}).Table("media.video_evidence_references AS refs").
			Joins("JOIN analysis.video_plans AS plans ON plans.tenant_id=refs.tenant_id AND plans.id=refs.owner_id").
			Where("refs.tenant_id=? AND refs.owner_type='VIDEO_PLAN' AND plans.tenant_id=? AND plans.inspection_id=?", tenantID, tenantID, *scope.inspectionID).
			Count(&livePlanFrames).Error; err != nil {
			return err
		}
		if livePlanFrames > 0 {
			return apperror.New(apperror.InvalidState, "inspectionId", "live video frame dependencies prevent inspection purge")
		}
	}
	return nil
}

func lockInspectionForLegalHold(tx *gorm.DB, tenantID, inspectionID identity.ID) error {
	var inspection database.Inspection
	err := tx.Session(&gorm.Session{}).Clauses(clause.Locking{Strength: "UPDATE"}).Where("tenant_id=? AND id=?", tenantID, inspectionID).First(&inspection).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return apperror.New(apperror.NotFound, "inspectionId", "inspection not found")
	}
	return err
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
	var doc catalog.TemplateDocument
	_ = json.Unmarshal(row.DefinitionJSON, &doc)
	return &graphql1.TemplateVersion{ID: row.ID.String(), TemplateID: row.TemplateID.String(), VersionNumber: row.VersionNumber, SchemaVersion: row.SchemaVersion, Definition: definition, IsVideo: doc.IsVideo, MediaKind: doc.MediaKind(), CanonicalDigest: row.CanonicalDigest, Status: row.Status, PublishedAt: row.PublishedAt.Format(time.RFC3339Nano)}
}

func mapVideoPolicy(row database.VideoPolicy) *graphql1.VideoPolicy {
	return &graphql1.VideoPolicy{MaxDurationMinutes: row.MaxDurationMinutes, OriginRetentionDays: row.OriginRetentionDays, CurrentRetentionDays: row.CurrentRetentionDays, Version: int(row.Version)}
}

func mapVideoAdmission(row database.VideoAdmission) *graphql1.VideoAdmission {
	var reason *string
	if row.Reason != "" {
		value := row.Reason
		reason = &value
	}
	return &graphql1.VideoAdmission{Enabled: row.Enabled, Version: int(row.Version), ChangedAt: row.ChangedAt.UTC().Format(time.RFC3339Nano), Reason: reason}
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
	var details *assetaddress.Details
	if input.AddressDetails != nil {
		value := assetaddress.Details{PostalCode: input.AddressDetails.PostalCode, Street: input.AddressDetails.Street, WithoutNumber: input.AddressDetails.WithoutNumber, City: input.AddressDetails.City, State: input.AddressDetails.State}
		if input.AddressDetails.Number != nil {
			value.Number = *input.AddressDetails.Number
		}
		if input.AddressDetails.Complement != nil {
			value.Complement = *input.AddressDetails.Complement
		}
		if input.AddressDetails.District != nil {
			value.District = *input.AddressDetails.District
		}
		if input.AddressDetails.MunicipalityCode != nil {
			value.MunicipalityCode = *input.AddressDetails.MunicipalityCode
		}
		if input.AddressDetails.Reference != nil {
			value.Reference = *input.AddressDetails.Reference
		}
		details = &value
	}
	return assetcore.Input{TenantID: tenantID, BusinessUnitID: unitID, SegmentVersionID: segmentID, TemplateID: templateID, Name: input.Name, ExternalKey: input.ExternalKey, Address: stringValue(input.Address), AddressDetails: details, LatitudeE6: int32Pointer(input.LatitudeE6), LongitudeE6: int32Pointer(input.LongitudeE6), GeofenceMeters: geofence, Attributes: input.Attributes, PolicyOverrides: policy, Assignments: assignments, IdempotencyKey: idempotencyKey}, nil
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

func parseOptionalWindow(from, to *string) (time.Time, time.Time, error) {
	var start, end time.Time
	if from != nil && *from != "" {
		parsed, err := time.Parse(time.RFC3339, *from)
		if err != nil {
			return time.Time{}, time.Time{}, graphql1Error("from")
		}
		start = parsed.UTC()
	}
	if to != nil && *to != "" {
		parsed, err := time.Parse(time.RFC3339, *to)
		if err != nil {
			return time.Time{}, time.Time{}, graphql1Error("to")
		}
		end = parsed.UTC()
	}
	return start, end, nil
}

func parseInstant(value string, field string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, graphql1Error(field)
	}
	return parsed.UTC(), nil
}

func parseScheduleStart(value, timezone string) (time.Time, error) {
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed.UTC(), nil
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return time.Time{}, graphql1Error("timezone")
	}
	for _, layout := range []string{"2006-01-02T15:04", "2006-01-02T15:04:05"} {
		parsed, parseErr := time.ParseInLocation(layout, value, location)
		if parseErr == nil && parsed.In(location).Format(layout) == value {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, graphql1Error("startsAt")
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
	asset := &graphql1.Asset{ID: view.Asset.ID.String(), BusinessUnitID: view.Asset.BusinessUnitID.String(), SegmentVersionID: view.Asset.SegmentVersionID.String(), TemplateID: optionalID(view.Asset.TemplateID), Name: view.Asset.Name, ExternalKey: view.Asset.ExternalKey, Address: view.Asset.Address, AddressStatus: view.Asset.AddressStatus, LatitudeE6: latitude, LongitudeE6: longitude, GeofenceMeters: view.Asset.GeofenceMeters, Attributes: attributes, PolicyOverrides: policies, Status: view.Asset.Status, Version: int(view.Asset.Version), Assignments: assignments}
	if view.Asset.AddressStatus == "COMPLETE" {
		asset.AddressDetails = &graphql1.PostalAddress{CountryCode: "BR", PostalCode: view.Asset.AddressPostalCode, Street: view.Asset.AddressStreet, Number: view.Asset.AddressNumber, WithoutNumber: view.Asset.AddressWithoutNumber, Complement: view.Asset.AddressComplement, District: view.Asset.AddressDistrict, City: view.Asset.AddressCity, State: view.Asset.AddressState, MunicipalityCode: view.Asset.AddressMunicipalityCode, Reference: view.Asset.AddressReference}
	}
	return asset
}

func mapOriginVersion(row database.OriginVersion, details *originlist.Details) *graphql1.OriginVersion {
	var activated *string
	if row.ActivatedAt != nil {
		value := row.ActivatedAt.Format(time.RFC3339Nano)
		activated = &value
	}
	mediaKind := row.MediaKind
	if mediaKind == "" {
		mediaKind = "PHOTO"
	}
	var digest *string
	if row.ScopeManifestDigest != "" {
		value := row.ScopeManifestDigest
		digest = &value
	}
	var preparationStatus, sourceAvailability *string
	captureSubmitted := false
	selectable := row.Status == "ACTIVE"
	if details != nil {
		preparationStatus, sourceAvailability, captureSubmitted, selectable = details.PreparationStatus, details.SourceAvailability, details.CaptureSubmitted, details.Selectable
	}
	return &graphql1.OriginVersion{ID: row.ID.String(), OriginID: row.OriginID.String(), VersionNumber: row.VersionNumber, Status: row.Status, MediaKind: mediaKind, VideoPreparationID: optionalID(row.VideoPreparationID), PreparationStatus: preparationStatus, CaptureSubmitted: captureSubmitted, Selectable: selectable, SourceAvailability: sourceAvailability, LimitedCoverage: mediaKind == "VIDEO" && row.VideoPlanID != nil, ScopeManifestDigest: digest, SupersedesID: optionalID(row.SupersedesID), ActivatedAt: activated}
}

func (r *Resolver) projectOriginVersion(ctx context.Context, tenantID identity.ID, row database.OriginVersion) (*graphql1.OriginVersion, error) {
	raw, err := r.Bus.Ask(ctx, originlist.VersionDetailsQuery{TenantID: tenantID, VersionID: row.ID})
	if err != nil {
		return nil, err
	}
	result := raw.(originlist.Result)
	details := result.Details[row.ID]
	return mapOriginVersion(row, &details), nil
}

func mapOriginPromotion(result originpromote.Result) *graphql1.OriginPromotion {
	row := result.Promotion
	media := make([]*graphql1.OriginPromotionMedia, 0, len(result.Eligible))
	for _, item := range result.Eligible {
		media = append(media, &graphql1.OriginPromotionMedia{ID: item.ID.String(), Description: item.Description})
	}
	var failure *string
	var selected []identity.ID
	_ = json.Unmarshal(row.SelectedMediaIDs, &selected)
	selectedIDs := make([]string, 0, len(selected))
	for _, id := range selected {
		selectedIDs = append(selectedIDs, id.String())
	}
	if row.FailureReason != "" {
		value := row.FailureReason
		failure = &value
	}
	return &graphql1.OriginPromotion{InspectionID: row.InspectionID.String(), Status: row.Status, FailureReason: failure, OriginVersionID: optionalID(row.OriginVersionID), SelectedMediaIds: selectedIDs, EligibleMedia: media}
}

func (r *queryResolver) publishedSnapshot(ctx context.Context, tenantID identity.ID, inspectionID string, version *int) (database.ReportSnapshot, error) {
	id, err := identity.ParseID(inspectionID)
	if err != nil {
		return database.ReportSnapshot{}, invalidID("inspectionId")
	}
	var row database.ReportSnapshot
	err = withTask06Tenant(ctx, r.DB, tenantID, func(tx *gorm.DB) error {
		query := tx.Session(&gorm.Session{NewDB: true}).Joins("JOIN reports.report_publications p ON p.snapshot_id=reports.report_snapshots.id AND p.tenant_id=reports.report_snapshots.tenant_id").Where("reports.report_snapshots.tenant_id=? AND reports.report_snapshots.inspection_id=? AND p.status='PUBLISHED'", tenantID, id).Order("reports.report_snapshots.version_number DESC")
		if version != nil {
			query = query.Where("reports.report_snapshots.version_number=?", *version)
		}
		return query.First(&row).Error
	})
	return row, err
}

func mapSchedule(row database.Schedule) *graphql1.Schedule {
	var offsets []int
	_ = json.Unmarshal(row.ReminderOffsets, &offsets)
	templateVersionID := ""
	if row.TemplateVersionID != nil {
		templateVersionID = row.TemplateVersionID.String()
	}
	return &graphql1.Schedule{ID: row.ID.String(), BusinessUnitID: row.BusinessUnitID.String(), AssetID: row.AssetID.String(), ParticipantID: row.ParticipantID.String(), TemplateID: row.TemplateID.String(), TemplateVersionID: templateVersionID, ProjectID: optionalID(row.ProjectID), StageID: optionalID(row.StageID), ReferenceVersionID: optionalID(row.ReferenceVersionID), Rrule: row.RRule, Timezone: row.Timezone, StartsAt: row.StartsAt.Format(time.RFC3339Nano), NextDueAt: row.NextDueAt.Format(time.RFC3339Nano), DeadlineMinutes: row.DeadlineMinutes, ReminderOffsetsMinutes: offsets, Status: row.Status, Version: int(row.Version)}
}

func mapInspection(view inspectioncore.View) *graphql1.Inspection {
	row := view.Inspection
	var reminders []string
	_ = json.Unmarshal(row.ReminderInstants, &reminders)
	mode := view.Reference.ComparisonMode
	var referenceVersionID, referenceMediaKind, referenceVersionStatus *string
	var referenceVersionNumber *int
	var referenceIsActive *bool
	var promotedOriginVersionID, promotedOriginMediaKind, promotionFailure *string
	var promotedOriginVersionNumber *int
	var promotedOriginIsActive *bool
	if view.Reference.ID != (identity.ID{}) {
		if view.Reference.ReferenceVersionID != nil {
			id := view.Reference.ReferenceVersionID.String()
			referenceVersionID = &id
		}
		if view.ReferenceVersion != nil {
			referenceMediaKind = &view.ReferenceVersion.MediaKind
			referenceVersionStatus = &view.ReferenceVersion.Status
			number := view.ReferenceVersion.VersionNumber
			referenceVersionNumber = &number
			active := view.ReferenceIsActive
			referenceIsActive = &active
		}
	}
	if view.OriginPromotionFailure != "" {
		promotionFailure = &view.OriginPromotionFailure
	}
	if view.PromotedOriginVersion != nil {
		id := view.PromotedOriginVersion.ID.String()
		promotedOriginVersionID = &id
		promotedOriginMediaKind = &view.PromotedOriginVersion.MediaKind
		number := view.PromotedOriginVersion.VersionNumber
		promotedOriginVersionNumber = &number
		active := view.PromotedOriginIsActive
		promotedOriginIsActive = &active
	}
	return &graphql1.Inspection{ID: row.ID.String(), BusinessUnitID: row.BusinessUnitID.String(), AssetID: row.AssetID.String(), ParticipantID: row.ParticipantID.String(), TemplateID: row.TemplateID.String(), TemplateVersionID: row.TemplateVersionID.String(), AnalysisPromptSnapshotID: row.AnalysisPromptSnapshotID.String(), ProjectID: optionalID(row.ProjectID), StageID: optionalID(row.StageID), Source: row.Source, SourceReason: optionalString(row.SourceReason), StateReason: optionalString(row.StateReason), Status: row.Status, MediaKind: row.MediaKind, ComparisonMode: optionalString(mode), ReferenceVersionID: referenceVersionID, ReferenceVersionNumber: referenceVersionNumber, ReferenceMediaKind: referenceMediaKind, ReferenceVersionStatus: referenceVersionStatus, ReferenceIsActive: referenceIsActive, OriginPromotionStatus: optionalString(view.OriginPromotionStatus), OriginPromotionFailure: promotionFailure, PromotedOriginVersionID: promotedOriginVersionID, PromotedOriginVersionNumber: promotedOriginVersionNumber, PromotedOriginMediaKind: promotedOriginMediaKind, PromotedOriginIsActive: promotedOriginIsActive, EvidenceCount: row.EvidenceCount, DueAt: row.DueAt.Format(time.RFC3339Nano), DeadlineAt: row.DeadlineAt.Format(time.RFC3339Nano), ReminderInstants: reminders, Version: int(row.Version), AssetName: view.AssetName, AssetAddress: view.AssetAddress, AssetExternalKey: view.AssetExternalKey, ParticipantName: view.ParticipantName}
}

func mapAnalysisPrompt(row database.AnalysisPrompt) *graphql1.AnalysisPrompt {
	var definition struct {
		SystemPrompt         string `json:"systemPrompt"`
		ModelAlias           string `json:"modelAlias"`
		MinimumConfidenceBPS int    `json:"minimumConfidenceBps"`
	}
	_ = json.Unmarshal(row.DefinitionJSON, &definition)
	return &graphql1.AnalysisPrompt{AnalysisType: graphql1.AnalysisType(row.AnalysisType), SystemPrompt: definition.SystemPrompt, ModelAlias: definition.ModelAlias, MinimumConfidenceBps: definition.MinimumConfidenceBPS, CanonicalDigest: row.CanonicalDigest, Revision: int(row.Revision), UpdatedAt: row.UpdatedAt.Format(time.RFC3339Nano)}
}

func mapProject(view projectcore.View) *graphql1.Project {
	stages := make([]*graphql1.ProjectStage, 0, len(view.Stages))
	for _, stage := range view.Stages {
		var planned *string
		if stage.PlannedAt != nil {
			value := stage.PlannedAt.Format(time.RFC3339Nano)
			planned = &value
		}
		inspectionIDs := make([]string, 0, len(stage.InspectionIDs))
		for _, id := range stage.InspectionIDs {
			inspectionIDs = append(inspectionIDs, id.String())
		}
		stages = append(stages, &graphql1.ProjectStage{ID: stage.ID.String(), Key: stage.Key, Label: stage.Label, Kind: stage.Kind, Position: stage.Position, Status: stage.Status, PlannedAt: planned, Reason: optionalString(stage.Reason), InspectionIds: inspectionIDs, Version: int(stage.Version)})
	}
	transitions := make([]*graphql1.StageTransition, 0, len(view.Transitions))
	for _, transition := range view.Transitions {
		transitions = append(transitions, &graphql1.StageTransition{ID: transition.ID.String(), StageID: optionalID(transition.StageID), FromState: optionalString(transition.FromState), ToState: transition.ToState, Reason: optionalString(transition.Reason), OccurredAt: transition.OccurredAt.Format(time.RFC3339Nano)})
	}
	row := view.Project
	return &graphql1.Project{ID: row.ID.String(), Name: row.Name, OrderedStages: row.OrderedStages, BusinessUnitID: row.BusinessUnitID.String(), AssetID: row.AssetID.String(), Status: row.Status, Version: int(row.Version), Stages: stages, Transitions: transitions}
}

func mapMedia(row database.MediaObject) *graphql1.Media {
	var flags []string
	_ = json.Unmarshal(row.Flags, &flags)
	mediaKind := row.MediaKind
	if mediaKind == "" {
		mediaKind = "PHOTO"
	}
	var preparationStatus *string
	if mediaKind == "VIDEO" && (row.Status == "RECEIVED" || row.Status == "COMPLETING") {
		value := "PENDING_VALIDATION"
		preparationStatus = &value
	}
	return &graphql1.Media{ID: row.ID.String(), Status: row.Status, MediaKind: mediaKind, PreparationStatus: preparationStatus, RequirementKey: optionalString(row.RequirementKey), Description: optionalString(row.Description), CaptureSource: optionalString(row.CaptureSource), Flags: flags, ReplacesMediaID: optionalID(row.ReplacesMediaID)}
}

func (r *Resolver) externalResponsibility(ctx context.Context) (identity.ID, identity.ID, error) {
	credentials, ok := requestctx.ExternalCredentialsFromContext(ctx)
	if !ok {
		return identity.ID{}, identity.ID{}, externalOperationAuthError(ctx)
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

func externalOperationAuthError(ctx context.Context) error {
	if meta, ok := requestctx.FromContext(ctx); ok && meta.Principal.IdentityID != (identity.ID{}) {
		return apperror.New(apperror.Forbidden, "", "access denied")
	}
	return unauthenticated()
}

func (r *Resolver) externalCaptureReadResponsibility(ctx context.Context) (identity.ID, identity.ID, error) {
	credentials, ok := requestctx.ExternalCredentialsFromContext(ctx)
	if !ok {
		return identity.ID{}, identity.ID{}, unauthenticated()
	}
	responsibilityID, err := r.Invitations.ValidateCaptureRead(ctx, credentials.SessionToken, credentials.CSRFToken)
	if err != nil {
		return identity.ID{}, identity.ID{}, err
	}
	tenantID, err := security.ParseScopedToken(credentials.SessionToken)
	if err != nil {
		return identity.ID{}, identity.ID{}, unauthenticated()
	}
	return tenantID, responsibilityID, nil
}
