package core

import (
	"inspection/services/inspection/internal/features/templates/catalog"
	"testing"
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
