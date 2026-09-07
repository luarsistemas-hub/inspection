package catalog

import "fmt"

type PolicyOverride struct {
	GPSRequired    *bool `json:"gpsRequired,omitempty"`
	GeofenceMeters *int  `json:"geofenceMeters,omitempty"`
	AllowGallery   *bool `json:"allowGallery,omitempty"`
}

type PolicySnapshot struct {
	GPSRequired    bool           `json:"gpsRequired"`
	GeofenceMeters int            `json:"geofenceMeters"`
	AllowGallery   bool           `json:"allowGallery"`
	ComparisonMode ComparisonMode `json:"comparisonMode"`
	ReferenceID    string         `json:"referenceId,omitempty"`
}

func ResolvePolicy(defaults Policy, override PolicyOverride, mode ComparisonMode, referenceID string) (PolicySnapshot, error) {
	result := PolicySnapshot{GPSRequired: defaults.GPSRequired, GeofenceMeters: defaults.GeofenceMeters, AllowGallery: defaults.AllowGallery, ComparisonMode: mode, ReferenceID: referenceID}
	if result.GeofenceMeters == 0 {
		result.GeofenceMeters = DefaultGeofence
	}
	if override.GPSRequired != nil {
		result.GPSRequired = *override.GPSRequired
	}
	if override.GeofenceMeters != nil {
		result.GeofenceMeters = *override.GeofenceMeters
	}
	if override.AllowGallery != nil {
		result.AllowGallery = *override.AllowGallery
	}
	if result.GeofenceMeters < MinGeofence || result.GeofenceMeters > MaxGeofence {
		return PolicySnapshot{}, fmt.Errorf("geofence out of range")
	}
	if !validMode(mode) {
		return PolicySnapshot{}, fmt.Errorf("conflicting comparison mode")
	}
	if mode != ChecklistOnly && referenceID == "" {
		return PolicySnapshot{}, fmt.Errorf("reference is required")
	}
	return result, nil
}
