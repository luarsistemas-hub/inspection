package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/assets/address"
	participantget "inspection/services/inspection/internal/features/participants/get_participant"
	segmentresolve "inspection/services/inspection/internal/features/segments/resolve_definition"
	"inspection/services/inspection/internal/features/templates/catalog"
	templateresolve "inspection/services/inspection/internal/features/templates/resolve_template"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/pagination"
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
	AddressDetails                             *address.Details
	ReuseExistingAddress                       bool
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
	name, legacyAddress, key := strings.TrimSpace(in.Name), strings.TrimSpace(in.Address), strings.TrimSpace(in.ExternalKey)
	addressStatus := "INCOMPLETE"
	var details address.Details
	formattedAddress := legacyAddress
	if legacyAddress != "" {
		addressStatus = "LEGACY"
	}
	if in.AddressDetails != nil {
		var err error
		details, err = address.Validate(*in.AddressDetails)
		if err != nil {
			return View{}, apperror.New(apperror.InvalidInput, "addressDetails", err.Error())
		}
		addressStatus = "COMPLETE"
		formattedAddress = address.Format(details)
		if legacyAddress != "" && legacyAddress != formattedAddress {
			return View{}, apperror.New(apperror.InvalidInput, "address", "address must match the formatted address details")
		}
	}
	if name == "" || formattedAddress == "" || key == "" {
		status = "DRAFT"
	}
	if name != "" && !catalog.ValidName(name) {
		return View{}, apperror.New(apperror.InvalidInput, "name", "name exceeds limit")
	}
	if !catalog.ValidDescription(formattedAddress) {
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
		if in.ReuseExistingAddress && in.AddressDetails != nil {
			if err := lockAddress(tx, in.TenantID, details); err != nil {
				return err
			}
			reused, found, err := s.reuseAddress(tx, in, details, name)
			if err != nil {
				return err
			}
			if found {
				out = reused
				return nil
			}
		}
		var count int64
		if err := tx.Model(&database.Asset{}).Where("tenant_id=?", in.TenantID).Count(&count).Error; err != nil {
			return err
		}
		if count >= catalog.MaxAssetsPerTenant {
			return apperror.New(apperror.InvalidState, "assets", "asset capacity reached")
		}
		now := time.Now().UTC()
		asset := database.Asset{ID: identity.NewID(), TenantID: in.TenantID, BusinessUnitID: in.BusinessUnitID, SegmentVersionID: in.SegmentVersionID, TemplateID: in.TemplateID, Name: name, ExternalKey: key, Address: formattedAddress, AddressCountryCode: "BR", AddressPostalCode: details.PostalCode, AddressStreet: details.Street, AddressNumber: details.Number, AddressWithoutNumber: details.WithoutNumber, AddressComplement: details.Complement, AddressDistrict: details.District, AddressCity: details.City, AddressState: details.State, AddressMunicipalityCode: details.MunicipalityCode, AddressReference: details.Reference, AddressStatus: addressStatus, LegacyAddress: legacyAddress, LatitudeE6: in.LatitudeE6, LongitudeE6: in.LongitudeE6, GeofenceMeters: in.GeofenceMeters, PolicyOverrides: overrides, Status: status, Version: 1, IdempotencyKey: in.IdempotencyKey, CreatedAt: now, UpdatedAt: now}
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

// lockAddress serializes onboarding find-or-create operations for a normalized
// address across API instances. Hash collisions only cause extra serialization.
func lockAddress(tx *gorm.DB, tenantID identity.ID, details address.Details) error {
	if tx.Dialector.Name() != "postgres" {
		return nil
	}
	key := fmt.Sprintf("%s:%s", tenantID, onboardingAddressKey(details))
	return tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", key).Error
}

func onboardingAddressKey(details address.Details) string {
	details = address.Normalize(details)
	parts, _ := json.Marshal([]string{
		details.PostalCode,
		strings.ToLower(details.Street),
		strings.ToLower(details.Number),
		fmt.Sprint(details.WithoutNumber),
		strings.ToLower(details.Complement),
		strings.ToLower(details.City),
		strings.ToUpper(details.State),
	})
	digest := sha256.Sum256(parts)
	return hex.EncodeToString(digest[:])
}

func (s Service) reuseAddress(tx *gorm.DB, in Input, details address.Details, name string) (View, bool, error) {
	var candidates []database.Asset
	if err := tx.Where("tenant_id = ? AND address_status = 'COMPLETE' AND status = 'ACTIVE' AND address_postal_code = ?", in.TenantID, details.PostalCode).
		Order("created_at ASC, id ASC").Find(&candidates).Error; err != nil {
		return View{}, false, err
	}
	var existing View
	for _, candidate := range candidates {
		if sameOnboardingAddress(candidate, details) {
			existing.Asset = candidate
			break
		}
	}
	if existing.Asset.ID == (identity.ID{}) {
		return View{}, false, nil
	}
	if err := s.load(tx, &existing); err != nil {
		return View{}, false, err
	}

	attributes := in.Attributes
	updateAttributes := true
	if _, err := s.Bus.Ask(tx.Statement.Context, segmentresolve.ValidateAttributesQuery{
		TenantID: in.TenantID, VersionID: existing.Asset.SegmentVersionID, Attributes: attributes,
	}); err != nil {
		if !apperror.Is(err, apperror.InvalidInput) {
			return View{}, false, err
		}
		updateAttributes = false
	}
	var attrs []byte
	var digest string
	if updateAttributes {
		var err error
		attrs, digest, err = catalog.CanonicalJSON(attributes)
		if err != nil {
			return View{}, false, err
		}
	}

	// Curated values survive a new onboarding: a renamed asset keeps its name and
	// optional fields are only replaced when the new submission provides them.
	merged := details
	if merged.District == "" {
		merged.District = existing.Asset.AddressDistrict
	}
	if merged.MunicipalityCode == "" {
		merged.MunicipalityCode = existing.Asset.AddressMunicipalityCode
	}
	if merged.Reference == "" {
		merged.Reference = existing.Asset.AddressReference
	}
	formattedAddress := address.Format(merged)

	now := time.Now().UTC()
	updates := map[string]any{}
	if strings.TrimSpace(existing.Asset.Name) == "" && name != "" {
		updates["name"] = name
	}
	addressUpdates := map[string]any{
		"address": formattedAddress, "address_country_code": "BR", "address_postal_code": merged.PostalCode,
		"address_street": merged.Street, "address_number": merged.Number,
		"address_without_number": merged.WithoutNumber, "address_complement": merged.Complement,
		"address_city": merged.City, "address_state": merged.State,
		"address_district": merged.District, "address_municipality_code": merged.MunicipalityCode,
		"address_reference": merged.Reference, "address_status": "COMPLETE",
	}
	for column, value := range addressUpdates {
		if !assetColumnEquals(existing.Asset, column, value) {
			updates[column] = value
		}
	}
	attributesChanged := updateAttributes && existing.Attributes.CanonicalDigest != digest
	if len(updates) == 0 && !attributesChanged {
		return existing, true, nil
	}
	updates["version"] = existing.Asset.Version + 1
	updates["updated_at"] = now
	result := tx.Model(&database.Asset{}).Where("tenant_id = ? AND id = ? AND version = ?",
		in.TenantID, existing.Asset.ID, existing.Asset.Version).Updates(updates)
	if result.Error != nil {
		return View{}, false, result.Error
	}
	if result.RowsAffected != 1 {
		return View{}, false, apperror.New(apperror.Conflict, "version", "stale asset version")
	}
	if attributesChanged {
		attribute := database.AssetAttributeVersion{
			ID: identity.NewID(), TenantID: in.TenantID, AssetID: existing.Asset.ID,
			SegmentVersionID: existing.Asset.SegmentVersionID, VersionNumber: existing.Asset.Version + 1,
			AttributesJSON: attrs, CanonicalDigest: digest, CreatedAt: now,
		}
		if err := tx.Create(&attribute).Error; err != nil {
			return View{}, false, err
		}
		existing.Attributes = attribute
	}
	if err := tx.Where("tenant_id = ? AND id = ?", in.TenantID, existing.Asset.ID).First(&existing.Asset).Error; err != nil {
		return View{}, false, err
	}
	if err := s.load(tx, &existing); err != nil {
		return View{}, false, err
	}
	return existing, true, nil
}

func sameOnboardingAddress(asset database.Asset, details address.Details) bool {
	details = address.Normalize(details)
	existing := address.Normalize(address.Details{
		PostalCode: asset.AddressPostalCode, Street: asset.AddressStreet, Number: asset.AddressNumber,
		WithoutNumber: asset.AddressWithoutNumber, Complement: asset.AddressComplement,
		City: asset.AddressCity, State: asset.AddressState,
	})
	return existing.PostalCode == details.PostalCode &&
		strings.EqualFold(existing.Street, details.Street) &&
		strings.EqualFold(existing.Number, details.Number) &&
		existing.WithoutNumber == details.WithoutNumber &&
		strings.EqualFold(existing.Complement, details.Complement) &&
		strings.EqualFold(existing.City, details.City) &&
		strings.EqualFold(existing.State, details.State)
}

func assetColumnEquals(asset database.Asset, column string, value any) bool {
	switch column {
	case "address":
		return asset.Address == value.(string)
	case "address_country_code":
		return asset.AddressCountryCode == value.(string)
	case "address_postal_code":
		return asset.AddressPostalCode == value.(string)
	case "address_street":
		return asset.AddressStreet == value.(string)
	case "address_number":
		return asset.AddressNumber == value.(string)
	case "address_without_number":
		return asset.AddressWithoutNumber == value.(bool)
	case "address_complement":
		return asset.AddressComplement == value.(string)
	case "address_city":
		return asset.AddressCity == value.(string)
	case "address_state":
		return asset.AddressState == value.(string)
	case "address_district":
		return asset.AddressDistrict == value.(string)
	case "address_municipality_code":
		return asset.AddressMunicipalityCode == value.(string)
	case "address_reference":
		return asset.AddressReference == value.(string)
	default:
		return false
	}
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
	if !catalog.ValidName(in.Name) {
		return View{}, apperror.New(apperror.InvalidInput, "asset", "text limit exceeded")
	}
	addressUpdate := map[string]any{}
	if in.AddressDetails != nil {
		details, err := address.Validate(*in.AddressDetails)
		if err != nil {
			return View{}, apperror.New(apperror.InvalidInput, "addressDetails", err.Error())
		}
		formatted := address.Format(details)
		if in.Address != "" && strings.TrimSpace(in.Address) != formatted {
			return View{}, apperror.New(apperror.InvalidInput, "address", "address must match the formatted address details")
		}
		addressUpdate = map[string]any{"address": formatted, "address_country_code": "BR", "address_postal_code": details.PostalCode, "address_street": details.Street, "address_number": details.Number, "address_without_number": details.WithoutNumber, "address_complement": details.Complement, "address_district": details.District, "address_city": details.City, "address_state": details.State, "address_municipality_code": details.MunicipalityCode, "address_reference": details.Reference, "address_status": "COMPLETE"}
	} else if existing.Asset.AddressStatus == "COMPLETE" {
		// Keep structured components authoritative for older text-only clients.
	} else if strings.TrimSpace(in.Address) != existing.Asset.Address {
		addressUpdate = map[string]any{"address": strings.TrimSpace(in.Address), "legacy_address": strings.TrimSpace(in.Address), "address_status": "LEGACY"}
	}
	if !catalog.ValidDescription(in.Address) {
		return View{}, apperror.New(apperror.InvalidInput, "address", "address exceeds limit")
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
	if existing.Asset.Name == strings.TrimSpace(in.Name) && existing.Asset.ExternalKey == strings.TrimSpace(in.ExternalKey) && len(addressUpdate) == 0 && sameID(existing.Asset.TemplateID, in.TemplateID) && sameInt32(existing.Asset.LatitudeE6, in.LatitudeE6) && sameInt32(existing.Asset.LongitudeE6, in.LongitudeE6) && existing.Asset.GeofenceMeters == in.GeofenceMeters && existingPolicyDigest == overrideDigest && existing.Attributes.CanonicalDigest == digest {
		return existing, nil
	}
	err = (tenanttx.Runner{DB: s.DB}).Within(ctx, in.TenantID, func(tx *gorm.DB) error {
		updates := map[string]any{"name": strings.TrimSpace(in.Name), "external_key": strings.TrimSpace(in.ExternalKey), "template_id": in.TemplateID, "latitude_e6": in.LatitudeE6, "longitude_e6": in.LongitudeE6, "geofence_meters": in.GeofenceMeters, "policy_overrides": overrides, "status": "ACTIVE", "version": expectedVersion + 1, "updated_at": time.Now().UTC()}
		for key, value := range addressUpdate {
			updates[key] = value
		}
		r := tx.Model(&database.Asset{}).Where("tenant_id=? AND id=? AND version=?", in.TenantID, assetID, expectedVersion).Updates(updates)
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
			term := "%" + strings.TrimSpace(search) + "%"
			q = q.Where("(name ILIKE ? OR external_key ILIKE ? OR address ILIKE ? OR address_postal_code ILIKE ? OR address_city ILIKE ? OR address_status ILIKE ?)", term, term, term, term, term, term)
		}
		if after != "" {
			at, id, err := pagination.After(after)
			if err != nil {
				return err
			}
			q = q.Where("(created_at, id) > (?, ?)", at, id)
		}
		return q.Order("created_at, id").Limit(first + 1).Find(&rows).Error
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
	if err := (tenanttx.Runner{DB: s.DB}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		for i := range out {
			if err := s.load(tx, &out[i]); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return nil, "", false, err
	}
	end := ""
	if len(rows) > 0 {
		last := rows[len(rows)-1]
		end = pagination.Encode(last.CreatedAt, last.ID)
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
