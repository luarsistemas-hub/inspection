package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"inspection/libs/identity"
	participantget "inspection/services/inspection/internal/features/participants/get_participant"
	segmentresolve "inspection/services/inspection/internal/features/segments/resolve_definition"
	"inspection/services/inspection/internal/features/templates/catalog"
	templateresolve "inspection/services/inspection/internal/features/templates/resolve_template"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
)

type AssignmentInput struct {
	ParticipantID identity.ID
	Role          string
}
type Input struct {
	TenantID, BusinessUnitID, SegmentVersionID identity.ID
	TemplateID                                 *identity.ID
	Name, ExternalKey, Address, IdempotencyKey string
	LatitudeE6, LongitudeE6                    *int32
	GeofenceMeters                             int
	Attributes                                 map[string]any
	PolicyOverrides                            catalog.PolicyOverride
	Assignments                                []AssignmentInput
}
type View struct {
	Asset       database.Asset
	Attributes  database.AssetAttributeVersion
	Assignments []database.AssetAssignment
}
type Service struct {
	DB         *gorm.DB
	Bus        *mediator.Bus
	Authorizer auth.Authorizer
}

func (s Service) Register(ctx context.Context, in Input) (View, error) {
	if in.TenantID == (identity.ID{}) || in.BusinessUnitID == (identity.ID{}) || in.SegmentVersionID == (identity.ID{}) || in.IdempotencyKey == "" {
		return View{}, apperror.New(apperror.InvalidInput, "input", "tenant, business unit, segment, and idempotency key are required")
	}
	if _, err := s.Authorizer.Authorize(ctx, in.TenantID, []string{auth.TenantAdmin, auth.InspectionConfigAdmin, auth.Manager}, &requestctx.Scope{Kind: "BUSINESS_UNIT", ID: in.BusinessUnitID}, true); err != nil {
		return View{}, err
	}
	if s.Bus == nil {
		return View{}, fmt.Errorf("asset register: missing mediator")
	}
	if _, err := s.Bus.Ask(ctx, segmentresolve.Query{TenantID: in.TenantID, VersionID: in.SegmentVersionID}); err != nil {
		return View{}, err
	}
	status := "ACTIVE"
	name, address, key := strings.TrimSpace(in.Name), strings.TrimSpace(in.Address), strings.TrimSpace(in.ExternalKey)
	if name == "" || address == "" || key == "" {
		status = "DRAFT"
	}
	if name != "" && !catalog.ValidName(name) {
		return View{}, apperror.New(apperror.InvalidInput, "name", "name exceeds limit")
	}
	if !catalog.ValidDescription(address) {
		return View{}, apperror.New(apperror.InvalidInput, "address", "address exceeds limit")
	}
	if _, err := s.Bus.Ask(ctx, segmentresolve.ValidateAttributesQuery{TenantID: in.TenantID, VersionID: in.SegmentVersionID, Attributes: in.Attributes}); err != nil {
		_, _, safe := apperror.Public(err)
		if strings.HasPrefix(safe, "required attribute") {
			status = "DRAFT"
		} else {
			return View{}, err
		}
	}
	if err := validateLocation(in.LatitudeE6, in.LongitudeE6, &in.GeofenceMeters); err != nil {
		return View{}, apperror.New(apperror.InvalidInput, "location", err.Error())
	}
	overrides, _, err := catalog.CanonicalJSON(in.PolicyOverrides)
	if err != nil {
		return View{}, err
	}
	if string(overrides) != "{}" && in.TemplateID == nil {
		return View{}, apperror.New(apperror.InvalidState, "templateId", "template must be selected before an override")
	}
	if in.TemplateID != nil {
		templateRaw, err := s.Bus.Ask(ctx, templateresolve.Query{TenantID: in.TenantID, TemplateID: *in.TemplateID})
		if err != nil {
			return View{}, err
		}
		if templateRaw.(templateresolve.Result).Template.SegmentVersionID != in.SegmentVersionID {
			return View{}, apperror.New(apperror.InvalidInput, "templateId", "template belongs to another segment version")
		}
	}
	for _, assignment := range in.Assignments {
		if _, err := s.Bus.Ask(ctx, participantget.Query{TenantID: in.TenantID, ParticipantID: assignment.ParticipantID}); err != nil {
			return View{}, err
		}
		if strings.TrimSpace(assignment.Role) == "" {
			return View{}, apperror.New(apperror.InvalidInput, "assignments", "role is required")
		}
	}
	var out View
	err = (tenanttx.Runner{DB: s.DB}).Within(ctx, in.TenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND idempotency_key=?", in.TenantID, in.IdempotencyKey).First(&out.Asset).Error; err == nil {
			return s.load(tx, &out)
		} else if err != gorm.ErrRecordNotFound {
			return err
		}
		var count int64
		if err := tx.Model(&database.Asset{}).Where("tenant_id=?", in.TenantID).Count(&count).Error; err != nil {
			return err
		}
		if count >= catalog.MaxAssetsPerTenant {
			return apperror.New(apperror.InvalidState, "assets", "asset capacity reached")
		}
		now := time.Now().UTC()
		asset := database.Asset{ID: identity.NewID(), TenantID: in.TenantID, BusinessUnitID: in.BusinessUnitID, SegmentVersionID: in.SegmentVersionID, TemplateID: in.TemplateID, Name: name, ExternalKey: key, Address: address, LatitudeE6: in.LatitudeE6, LongitudeE6: in.LongitudeE6, GeofenceMeters: in.GeofenceMeters, PolicyOverrides: overrides, Status: status, Version: 1, IdempotencyKey: in.IdempotencyKey, CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&asset).Error; err != nil {
			return err
		}
		attrs, digest, _ := catalog.CanonicalJSON(in.Attributes)
		attribute := database.AssetAttributeVersion{ID: identity.NewID(), TenantID: in.TenantID, AssetID: asset.ID, SegmentVersionID: in.SegmentVersionID, VersionNumber: 1, AttributesJSON: attrs, CanonicalDigest: digest, CreatedAt: now}
		if err := tx.Create(&attribute).Error; err != nil {
			return err
		}
		for _, a := range in.Assignments {
			if err := tx.Create(&database.AssetAssignment{ID: identity.NewID(), TenantID: in.TenantID, AssetID: asset.ID, ParticipantID: a.ParticipantID, Role: a.Role, Active: true, CreatedAt: now}).Error; err != nil {
				return err
			}
		}
		out = View{Asset: asset, Attributes: attribute}
		return s.load(tx, &out)
	})
	return out, err
}

func (s Service) Update(ctx context.Context, assetID identity.ID, expectedVersion int64, in Input) (View, error) {
	var existing View
	err := (tenanttx.Runner{DB: s.DB}).Within(ctx, in.TenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND id=?", in.TenantID, assetID).First(&existing.Asset).Error; err != nil {
			return err
		}
		return s.load(tx, &existing)
	})
	if err != nil {
		return View{}, apperror.New(apperror.NotFound, "assetId", "asset not found")
	}
	if _, err := s.Authorizer.Authorize(ctx, in.TenantID, []string{auth.TenantAdmin, auth.InspectionConfigAdmin, auth.Manager}, &requestctx.Scope{Kind: "BUSINESS_UNIT", ID: existing.Asset.BusinessUnitID}, true); err != nil {
		return View{}, err
	}
	if existing.Asset.Status == "ARCHIVED" {
		return View{}, apperror.New(apperror.InvalidState, "assetId", "asset is archived")
	}
	in.BusinessUnitID = existing.Asset.BusinessUnitID
	in.SegmentVersionID = existing.Asset.SegmentVersionID
	if err := validateLocation(in.LatitudeE6, in.LongitudeE6, &in.GeofenceMeters); err != nil {
		return View{}, apperror.New(apperror.InvalidInput, "location", err.Error())
	}
	if !catalog.ValidName(in.Name) || !catalog.ValidDescription(in.Address) {
		return View{}, apperror.New(apperror.InvalidInput, "asset", "text limit exceeded")
	}
	if _, err := s.Bus.Ask(ctx, segmentresolve.ValidateAttributesQuery{TenantID: in.TenantID, VersionID: in.SegmentVersionID, Attributes: in.Attributes}); err != nil {
		return View{}, err
	}
	if in.TemplateID != nil {
		templateRaw, err := s.Bus.Ask(ctx, templateresolve.Query{TenantID: in.TenantID, TemplateID: *in.TemplateID})
		if err != nil {
			return View{}, err
		}
		if templateRaw.(templateresolve.Result).Template.SegmentVersionID != in.SegmentVersionID {
			return View{}, apperror.New(apperror.InvalidInput, "templateId", "template belongs to another segment version")
		}
	}
	overrides, _, _ := catalog.CanonicalJSON(in.PolicyOverrides)
	if string(overrides) != "{}" && in.TemplateID == nil {
		return View{}, apperror.New(apperror.InvalidState, "templateId", "template must be selected before an override")
	}
	attrs, digest, _ := catalog.CanonicalJSON(in.Attributes)
	existingPolicyDigest, err := jsonDigest(existing.Asset.PolicyOverrides)
	if err != nil {
		return View{}, fmt.Errorf("decode stored asset policy: %w", err)
	}
	_, overrideDigest, _ := catalog.CanonicalJSON(in.PolicyOverrides)
	if existing.Asset.Name == strings.TrimSpace(in.Name) && existing.Asset.ExternalKey == strings.TrimSpace(in.ExternalKey) && existing.Asset.Address == strings.TrimSpace(in.Address) && sameID(existing.Asset.TemplateID, in.TemplateID) && sameInt32(existing.Asset.LatitudeE6, in.LatitudeE6) && sameInt32(existing.Asset.LongitudeE6, in.LongitudeE6) && existing.Asset.GeofenceMeters == in.GeofenceMeters && existingPolicyDigest == overrideDigest && existing.Attributes.CanonicalDigest == digest {
		return existing, nil
	}
	err = (tenanttx.Runner{DB: s.DB}).Within(ctx, in.TenantID, func(tx *gorm.DB) error {
		r := tx.Model(&database.Asset{}).Where("tenant_id=? AND id=? AND version=?", in.TenantID, assetID, expectedVersion).Updates(map[string]any{"name": strings.TrimSpace(in.Name), "external_key": strings.TrimSpace(in.ExternalKey), "address": strings.TrimSpace(in.Address), "template_id": in.TemplateID, "latitude_e6": in.LatitudeE6, "longitude_e6": in.LongitudeE6, "geofence_meters": in.GeofenceMeters, "policy_overrides": overrides, "status": "ACTIVE", "version": expectedVersion + 1, "updated_at": time.Now().UTC()})
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return apperror.New(apperror.Conflict, "version", "stale asset version")
		}
		attribute := database.AssetAttributeVersion{ID: identity.NewID(), TenantID: in.TenantID, AssetID: assetID, SegmentVersionID: in.SegmentVersionID, VersionNumber: expectedVersion + 1, AttributesJSON: attrs, CanonicalDigest: digest, CreatedAt: time.Now().UTC()}
		if err := tx.Create(&attribute).Error; err != nil {
			return err
		}
		if err := tx.Where("tenant_id=? AND id=?", in.TenantID, assetID).First(&existing.Asset).Error; err != nil {
			return err
		}
		existing.Attributes = attribute
		return s.load(tx, &existing)
	})
	return existing, err
}

func (s Service) Archive(ctx context.Context, tenantID, assetID identity.ID, expectedVersion int64) error {
	var asset database.Asset
	if err := (tenanttx.Runner{DB: s.DB}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND id=?", tenantID, assetID).First(&asset).Error
	}); err != nil {
		return apperror.New(apperror.NotFound, "assetId", "asset not found")
	}
	if asset.Status == "ARCHIVED" {
		return nil
	}
	if _, err := s.Authorizer.Authorize(ctx, tenantID, []string{auth.TenantAdmin, auth.InspectionConfigAdmin, auth.Manager}, &requestctx.Scope{Kind: "BUSINESS_UNIT", ID: asset.BusinessUnitID}, true); err != nil {
		return err
	}
	return (tenanttx.Runner{DB: s.DB}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		r := tx.Model(&database.Asset{}).Where("tenant_id=? AND id=? AND version=? AND status<>'ARCHIVED'", tenantID, assetID, expectedVersion).Updates(map[string]any{"status": "ARCHIVED", "version": expectedVersion + 1, "updated_at": time.Now().UTC()})
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return apperror.New(apperror.Conflict, "version", "stale asset version")
		}
		return nil
	})
}
func sameID(a, b *identity.ID) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
func sameInt32(a, b *int32) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func (s Service) Get(ctx context.Context, tenantID, assetID identity.ID) (View, error) {
	var out View
	err := (tenanttx.Runner{DB: s.DB}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND id=?", tenantID, assetID).First(&out.Asset).Error; err != nil {
			return apperror.New(apperror.NotFound, "assetId", "asset not found")
		}
		return s.load(tx, &out)
	})
	if err != nil {
		return View{}, err
	}
	if _, err := s.Authorizer.Authorize(ctx, tenantID, []string{auth.TenantAdmin, auth.InspectionConfigAdmin, auth.Manager, auth.Employee, auth.Viewer}, &requestctx.Scope{Kind: "BUSINESS_UNIT", ID: out.Asset.BusinessUnitID}, false); err != nil {
		return View{}, err
	}
	return out, nil
}
func (s Service) List(ctx context.Context, tenantID identity.ID, businessUnitID *identity.ID, search string, first int, after string) ([]View, string, bool, error) {
	principal, err := s.Authorizer.Authorize(ctx, tenantID, []string{auth.TenantAdmin, auth.InspectionConfigAdmin, auth.Manager, auth.Employee, auth.Viewer}, nil, false)
	if err != nil {
		return nil, "", false, err
	}
	if first <= 0 {
		first = 25
	}
	if first > 100 {
		return nil, "", false, apperror.New(apperror.InvalidInput, "first", "page size exceeds 100")
	}
	allowed := map[identity.ID]struct{}{}
	if !hasRole(principal.Roles, auth.TenantAdmin) {
		for _, scope := range principal.Scopes {
			if scope.Kind == "BUSINESS_UNIT" {
				allowed[scope.ID] = struct{}{}
			}
		}
		if len(allowed) == 0 {
			return []View{}, "", false, nil
		}
	}
	var rows []database.Asset
	err = (tenanttx.Runner{DB: s.DB}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		q := tx.Where("tenant_id=?", tenantID)
		if businessUnitID != nil {
			if len(allowed) > 0 {
				if _, ok := allowed[*businessUnitID]; !ok {
					return apperror.New(apperror.Forbidden, "businessUnitId", "access denied")
				}
			}
			q = q.Where("business_unit_id=?", *businessUnitID)
		} else if len(allowed) > 0 {
			ids := make([]identity.ID, 0, len(allowed))
			for id := range allowed {
				ids = append(ids, id)
			}
			q = q.Where("business_unit_id IN ?", ids)
		}
		if search != "" {
			q = q.Where("name ILIKE ?", "%"+search+"%")
		}
		if after != "" {
			q = q.Where("id > ?", after)
		}
		return q.Order("id").Limit(first + 1).Find(&rows).Error
	})
	if err != nil {
		return nil, "", false, err
	}
	more := len(rows) > first
	if more {
		rows = rows[:first]
	}
	out := make([]View, len(rows))
	for i, row := range rows {
		out[i].Asset = row
	}
	end := ""
	if len(rows) > 0 {
		end = rows[len(rows)-1].ID.String()
	}
	return out, end, more, nil
}

func validateLocation(lat, lon *int32, radius *int) error {
	if (lat == nil) != (lon == nil) {
		return fmt.Errorf("latitude and longitude must be supplied together")
	}
	if lat != nil && (*lat < -90000000 || *lat > 90000000 || *lon < -180000000 || *lon > 180000000) {
		return fmt.Errorf("coordinates out of range")
	}
	if *radius == 0 {
		*radius = catalog.DefaultGeofence
	}
	if *radius < catalog.MinGeofence || *radius > catalog.MaxGeofence {
		return fmt.Errorf("geofence out of range")
	}
	return nil
}
func jsonDigest(value []byte) (string, error) {
	var decoded any
	if err := json.Unmarshal(value, &decoded); err != nil {
		return "", err
	}
	_, digest, err := catalog.CanonicalJSON(decoded)
	return digest, err
}
func (s Service) load(tx *gorm.DB, v *View) error {
	if err := tx.Where("tenant_id=? AND asset_id=?", v.Asset.TenantID, v.Asset.ID).Order("version_number DESC").First(&v.Attributes).Error; err != nil && err != gorm.ErrRecordNotFound {
		return err
	}
	return tx.Where("tenant_id=? AND asset_id=? AND active=true", v.Asset.TenantID, v.Asset.ID).Find(&v.Assignments).Error
}
func hasRole(roles []string, w string) bool {
	for _, r := range roles {
		if r == w {
			return true
		}
	}
	return false
}
