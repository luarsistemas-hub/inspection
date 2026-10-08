package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/assets/address"
	segmentresolve "inspection/services/inspection/internal/features/segments/resolve_definition"
	"inspection/services/inspection/internal/features/templates/catalog"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestAssetLocationAndPolicyContractsIT051IT052IT061ToIT070(t *testing.T) {
	radius := 0
	if err := validateLocation(nil, nil, &radius); err != nil || radius != catalog.DefaultGeofence {
		t.Fatalf("default geofence: %d %v", radius, err)
	}
	lat, lon := int32(90000000), int32(180000000)
	radius = catalog.MaxGeofence
	if err := validateLocation(&lat, &lon, &radius); err != nil {
		t.Fatal(err)
	}
	badLat := int32(90000001)
	if validateLocation(&badLat, &lon, &radius) == nil {
		t.Fatal("bad latitude accepted")
	}
	if validateLocation(&lat, nil, &radius) == nil {
		t.Fatal("partial coordinates accepted")
	}
	radius = catalog.MinGeofence - 1
	if validateLocation(nil, nil, &radius) == nil {
		t.Fatal("small geofence accepted")
	}
}

func TestOnboardingAddressKeyUsesNormalizedIdentityFields(t *testing.T) {
	first, err := address.Validate(address.Details{
		PostalCode: "88058-000", Street: "  Rua   das Flores ", Number: "12a", Complement: " Ap  2 ", City: "Florianópolis", State: "sc", Reference: "Portão azul",
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := address.Validate(address.Details{
		PostalCode: "88058000", Street: "rua das flores", Number: "12A", Complement: "ap 2", City: "FLORIANÓPOLIS", State: "SC", Reference: "Outra referência",
	})
	if err != nil {
		t.Fatal(err)
	}
	if onboardingAddressKey(first) != onboardingAddressKey(second) {
		t.Fatal("normalized address and reference-only change should keep the same lock key")
	}
	second.Complement = "ap 3"
	if onboardingAddressKey(first) == onboardingAddressKey(second) {
		t.Fatal("different complement should produce a different lock key")
	}
}

func TestSameOnboardingAddressUsesConfiguredAddressComponents(t *testing.T) {
	asset := database.Asset{
		AddressPostalCode: "88058000", AddressStreet: "Rua das Flores", AddressNumber: "12A",
		AddressComplement: "Ap 2", AddressCity: "Florianópolis", AddressState: "SC",
	}
	details := address.Details{PostalCode: "88058-000", Street: " rua  das flores ", Number: "12a", Complement: "ap 2", City: "FLORIANÓPOLIS", State: "sc", District: "Centro", Reference: "Portão azul"}
	if !sameOnboardingAddress(asset, details) {
		t.Fatal("case and whitespace differences plus ignored fields should still match")
	}
	details.Number = "13"
	if sameOnboardingAddress(asset, details) {
		t.Fatal("different number should not match")
	}
	details.Number = "12A"
	details.Complement = "ap 3"
	if sameOnboardingAddress(asset, details) {
		t.Fatal("different complement should not match")
	}
}

func TestReuseAddressUpdatesSameAssetAndPreservesItsUnitAndAssignments(t *testing.T) {
	db := openAssetReuseDB(t)
	tenantID, originalUnit, onboardingUnit := identity.NewID(), identity.NewID(), identity.NewID()
	assetID, segmentID, participantID := identity.NewID(), identity.NewID(), identity.NewID()
	now := time.Now().UTC()
	asset := database.Asset{
		ID: assetID, TenantID: tenantID, BusinessUnitID: originalUnit, SegmentVersionID: segmentID,
		Name: "Imóvel antigo", ExternalKey: "legacy-key", Address: "Rua das Flores, 12A, Ap 2",
		AddressCountryCode: "BR", AddressPostalCode: "88058000", AddressStreet: "Rua das Flores", AddressNumber: "12A",
		AddressComplement: "Ap 2", AddressCity: "Florianópolis", AddressState: "SC", AddressStatus: "COMPLETE",
		Status: "ACTIVE", Version: 1, IdempotencyKey: "existing", PolicyOverrides: json.RawMessage(`{}`), CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&asset).Error; err != nil {
		t.Fatal(err)
	}
	attrs, digest, err := catalog.CanonicalJSON(map[string]any{"propertyType": "HOUSE", "purpose": "RENTAL"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.AssetAttributeVersion{ID: identity.NewID(), TenantID: tenantID, AssetID: assetID, SegmentVersionID: segmentID, VersionNumber: 1, AttributesJSON: attrs, CanonicalDigest: digest, CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	assignment := database.AssetAssignment{ID: identity.NewID(), TenantID: tenantID, AssetID: assetID, ParticipantID: participantID, Role: "PROPERTY_OWNER", Active: true, CreatedAt: now}
	if err := db.Create(&assignment).Error; err != nil {
		t.Fatal(err)
	}

	bus := mediator.New()
	if err := bus.RegisterQuery(segmentresolve.ValidateAttributesQuery{}, func(_ context.Context, raw any) (any, error) {
		query := raw.(segmentresolve.ValidateAttributesQuery)
		if query.VersionID != segmentID {
			return nil, apperror.New(apperror.InvalidInput, "attributes", "incompatible attributes")
		}
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	service := Service{DB: db, Bus: bus}
	details := address.Details{PostalCode: "88058-000", Street: " rua  das flores ", Number: "12a", Complement: "ap 2", City: "FLORIANÓPOLIS", State: "sc", Reference: "Nova referência"}
	validated, err := address.Validate(details)
	if err != nil {
		t.Fatal(err)
	}
	input := Input{
		TenantID: tenantID, BusinessUnitID: onboardingUnit, Name: "Rua das Flores 12A", AddressDetails: &details,
		Attributes: map[string]any{"propertyType": "APARTMENT", "purpose": "SALE"},
	}
	var view View
	var found bool
	err = db.Transaction(func(tx *gorm.DB) error {
		var err error
		view, found, err = service.reuseAddress(tx, input, validated, input.Name)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if !found || view.Asset.ID != assetID || view.Asset.BusinessUnitID != originalUnit || view.Asset.Version != 2 {
		t.Fatalf("existing asset identity or ownership changed: found=%t asset=%+v", found, view.Asset)
	}
	if len(view.Assignments) != 1 || view.Assignments[0].ID != assignment.ID {
		t.Fatalf("existing assignments were not preserved: %+v", view.Assignments)
	}
	if view.Asset.AddressReference != "Nova referência" {
		t.Fatalf("address details were not refreshed: %+v", view.Asset)
	}
	if string(view.Attributes.AttributesJSON) != `{"propertyType":"APARTMENT","purpose":"SALE"}` {
		t.Fatalf("compatible attributes were not updated: %s", view.Attributes.AttributesJSON)
	}
	var count int64
	if err := db.Model(&database.Asset{}).Where("tenant_id = ?", tenantID).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("reuse created another asset: count=%d err=%v", count, err)
	}
}

func TestReuseAddressPreservesIncompatibleAttributesAndDoesNotMatchDifferentAddress(t *testing.T) {
	db := openAssetReuseDB(t)
	tenantID, otherTenant, unitID, segmentID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	now := time.Now().UTC()
	assets := []database.Asset{
		{ID: identity.NewID(), TenantID: tenantID, BusinessUnitID: unitID, SegmentVersionID: segmentID, Name: "Original", ExternalKey: "same-tenant", Address: "Rua Um, 10, Bloco A", AddressCountryCode: "BR", AddressPostalCode: "01001000", AddressStreet: "Rua Um", AddressNumber: "10", AddressComplement: "Bloco A", AddressCity: "São Paulo", AddressState: "SP", AddressStatus: "COMPLETE", Status: "ACTIVE", Version: 1, IdempotencyKey: "tenant-asset", PolicyOverrides: json.RawMessage(`{}`), CreatedAt: now, UpdatedAt: now},
		{ID: identity.NewID(), TenantID: otherTenant, BusinessUnitID: unitID, SegmentVersionID: segmentID, Name: "Other tenant", ExternalKey: "other-tenant", Address: "Rua Um, 10, Bloco A", AddressCountryCode: "BR", AddressPostalCode: "01001000", AddressStreet: "Rua Um", AddressNumber: "10", AddressComplement: "Bloco A", AddressCity: "São Paulo", AddressState: "SP", AddressStatus: "COMPLETE", Status: "ACTIVE", Version: 1, IdempotencyKey: "other-tenant-asset", PolicyOverrides: json.RawMessage(`{}`), CreatedAt: now, UpdatedAt: now},
	}
	for i := range assets {
		if err := db.Create(&assets[i]).Error; err != nil {
			t.Fatal(err)
		}
		attrs, digest, err := catalog.CanonicalJSON(map[string]any{"legacyType": "custom"})
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&database.AssetAttributeVersion{ID: identity.NewID(), TenantID: assets[i].TenantID, AssetID: assets[i].ID, SegmentVersionID: segmentID, VersionNumber: 1, AttributesJSON: attrs, CanonicalDigest: digest, CreatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
	}
	bus := mediator.New()
	if err := bus.RegisterQuery(segmentresolve.ValidateAttributesQuery{}, func(context.Context, any) (any, error) {
		return nil, apperror.New(apperror.InvalidInput, "attributes", "incompatible attributes")
	}); err != nil {
		t.Fatal(err)
	}
	service := Service{DB: db, Bus: bus}
	details := address.Details{PostalCode: "01001-000", Street: "Rua Um", Number: "10", Complement: "Bloco A", City: "São Paulo", State: "SP"}
	validated, err := address.Validate(details)
	if err != nil {
		t.Fatal(err)
	}
	input := Input{TenantID: tenantID, BusinessUnitID: identity.NewID(), Name: "Updated", Attributes: map[string]any{"propertyType": "HOUSE", "purpose": "RENTAL"}}
	var view View
	var found bool
	err = db.Transaction(func(tx *gorm.DB) error {
		var err error
		view, found, err = service.reuseAddress(tx, input, validated, input.Name)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if !found || view.Asset.ID != assets[0].ID || view.Asset.BusinessUnitID != unitID {
		t.Fatalf("same-tenant asset in another unit was not reused: found=%t asset=%+v", found, view.Asset)
	}
	if string(view.Attributes.AttributesJSON) != `{"legacyType":"custom"}` {
		t.Fatalf("incompatible attributes were overwritten: %s", view.Attributes.AttributesJSON)
	}

	different := validated
	different.Number = "11"
	var missing bool
	err = db.Transaction(func(tx *gorm.DB) error {
		_, missing, err = service.reuseAddress(tx, input, different, input.Name)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if missing {
		t.Fatal("different number matched an existing asset")
	}
	input.TenantID = identity.NewID()
	err = db.Transaction(func(tx *gorm.DB) error {
		_, missing, err = service.reuseAddress(tx, input, validated, input.Name)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if missing {
		t.Fatal("asset from another tenant matched")
	}
	input.TenantID = otherTenant
	err = db.Transaction(func(tx *gorm.DB) error {
		view, missing, err = service.reuseAddress(tx, input, validated, input.Name)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if !missing || view.Asset.ID != assets[1].ID {
		t.Fatalf("tenant-scoped search failed to select its own asset: found=%t asset=%s", missing, view.Asset.ID)
	}
}

func openAssetReuseDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+identity.NewID().String()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ATTACH DATABASE ':memory:' AS assets").Error; err != nil {
		t.Fatal(err)
	}
	statements := []string{
		`CREATE TABLE assets.assets (id blob PRIMARY KEY, tenant_id blob, business_unit_id blob, segment_version_id blob, template_id blob, name text, external_key text, address text, address_country_code text, address_postal_code text, address_street text, address_number text, address_without_number boolean, address_complement text, address_district text, address_city text, address_state text, address_municipality_code text, address_reference text, address_status text, legacy_address text, latitude_e6 integer, longitude_e6 integer, geofence_meters integer, policy_overrides blob, status text, version integer, idempotency_key text, created_at datetime, updated_at datetime)`,
		`CREATE TABLE assets.asset_attribute_versions (id blob PRIMARY KEY, tenant_id blob, asset_id blob, segment_version_id blob, version_number integer, attributes_json blob, canonical_digest text, created_at datetime)`,
		`CREATE TABLE assets.asset_assignments (id blob PRIMARY KEY, tenant_id blob, asset_id blob, participant_id blob, role text, active boolean, created_at datetime)`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func TestReuseAddressKeepsCuratedNameAndOptionalFieldsWhenSubmissionOmitsThem(t *testing.T) {
	db := openAssetReuseDB(t)
	tenantID, unitID, segmentID, assetID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	now := time.Now().UTC()
	asset := database.Asset{
		ID: assetID, TenantID: tenantID, BusinessUnitID: unitID, SegmentVersionID: segmentID,
		Name: "Apto 302 - Ed. Atlântico", ExternalKey: "curated", Address: "Rua das Flores, 12A, Centro, Florianópolis/SC, Portão azul",
		AddressCountryCode: "BR", AddressPostalCode: "88058000", AddressStreet: "Rua das Flores", AddressNumber: "12A",
		AddressDistrict: "Centro", AddressCity: "Florianópolis", AddressState: "SC", AddressMunicipalityCode: "4205407",
		AddressReference: "Portão azul", AddressStatus: "COMPLETE", Status: "ACTIVE", Version: 1,
		IdempotencyKey: "curated", PolicyOverrides: json.RawMessage(`{}`), CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&asset).Error; err != nil {
		t.Fatal(err)
	}

	bus := mediator.New()
	if err := bus.RegisterQuery(segmentresolve.ValidateAttributesQuery{}, func(context.Context, any) (any, error) {
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	service := Service{DB: db, Bus: bus}
	details := address.Details{PostalCode: "88058000", Street: "Rua das Flores", Number: "12A", City: "Florianópolis", State: "SC"}
	validated, err := address.Validate(details)
	if err != nil {
		t.Fatal(err)
	}
	input := Input{
		TenantID: tenantID, BusinessUnitID: identity.NewID(), Name: "Rua das Flores 12A", AddressDetails: &details,
		Attributes: map[string]any{"propertyType": "HOUSE", "purpose": "SALE"},
	}
	var view View
	var found bool
	err = db.Transaction(func(tx *gorm.DB) error {
		var err error
		view, found, err = service.reuseAddress(tx, input, validated, input.Name)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("matching address must reuse the existing asset")
	}
	if view.Asset.Name != "Apto 302 - Ed. Atlântico" {
		t.Fatalf("curated asset name was overwritten: %q", view.Asset.Name)
	}
	if view.Asset.AddressReference != "Portão azul" || view.Asset.AddressDistrict != "Centro" || view.Asset.AddressMunicipalityCode != "4205407" {
		t.Fatalf("optional address fields were erased by a submission without them: %+v", view.Asset)
	}
	if !strings.Contains(view.Asset.Address, "Centro") {
		t.Fatalf("formatted address must reflect the preserved district: %q", view.Asset.Address)
	}
}
