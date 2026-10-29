// Package core owns capture completeness and immutable provenance rules.
package core

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"inspection/services/inspection/internal/features/templates/catalog"
	"inspection/services/inspection/internal/platform/apperror"
)

const (
	MaxActivePhotos                = 200
	MaxEnvironmentPhotos           = 20
	MaxEnvironmentInspectionPhotos = catalog.MaxEnvironmentInspectionPhotos
	MaxTextRunes                   = 2000
	MaxGPSAccuracyM                = 50.0
	GPSWindow                      = 60 * time.Second
)

type Requirement struct {
	Key                   string
	Section               string
	Label                 string
	Instructions          string
	EvidenceKind          string
	Required              bool
	ImpossibilityAllowed  bool
	MinimumMedia          int
	MaximumMedia          int
	DescriptionRequired   bool
	CaptureSourcePolicy   string
	ComparisonTarget      string
	EnvironmentIdentityID string `json:"environmentIdentityId,omitempty"`
	EnvironmentTypeCode   string `json:"environmentTypeCode,omitempty"`
	EnvironmentName       string `json:"environmentName,omitempty"`
	AddedDuringCapture    bool   `json:"addedDuringCapture,omitempty"`
	Expected              bool   `json:"expected,omitempty"`
	Deleted               bool   `json:"deleted,omitempty"`
}

// VideoMainRequirement returns the canonical participant upload slot for a video capture.
func VideoMainRequirement(comparisonTarget string) Requirement {
	return Requirement{
		Key:                 "video:main",
		Section:             "vistoria",
		Label:               "Vídeo principal",
		Instructions:        "Grave um único vídeo contínuo da vistoria.",
		EvidenceKind:        "VIDEO",
		Required:            true,
		MinimumMedia:        1,
		MaximumMedia:        1,
		CaptureSourcePolicy: "CAMERA_DEFAULT",
		ComparisonTarget:    comparisonTarget,
	}
}

// VideoSupplementRequirement returns the canonical participant upload slot for a video recapture.
func VideoSupplementRequirement(comparisonTarget, instructions string) Requirement {
	requirement := VideoMainRequirement(comparisonTarget)
	requirement.Key = "video:supplement"
	requirement.Label = "Complemento em vídeo"
	requirement.Instructions = instructions
	return requirement
}

type Answer struct {
	RequirementKey string
	ReadyMedia     int
	FailedMedia    int
	Impossibility  string
}

type Completeness struct {
	Complete, RequiresAttention bool
	Satisfied, Total            int
	Missing, Blocked            []string
	PendingKeys                 []string
}

// PendingRequirementError carries the v2 environment keys through GraphQL
// error presentation without putting them in client-facing prose.
type PendingRequirementError struct {
	Keys []string
	Err  error
}

func (e PendingRequirementError) Error() string { return e.Err.Error() }
func (e PendingRequirementError) Unwrap() error { return e.Err }
func (e PendingRequirementError) PendingRequirementKeys() []string {
	return append([]string(nil), e.Keys...)
}

func Evaluate(requirements []Requirement, answers []Answer, confirmIncomplete bool) (Completeness, error) {
	if len(requirements) == 0 {
		return Completeness{}, apperror.New(apperror.InvalidState, "requirements", "capture has no applicable requirements")
	}
	byKey := make(map[string]Answer, len(answers))
	for _, answer := range answers {
		byKey[answer.RequirementKey] = answer
	}
	result := Completeness{}
	for _, requirement := range requirements {
		if requirement.Deleted {
			continue
		}
		if !requirement.Required {
			continue
		}
		result.Total++
		answer := byKey[requirement.Key]
		reason := strings.TrimSpace(answer.Impossibility)
		if answer.FailedMedia > 0 {
			result.Blocked = append(result.Blocked, requirement.Key)
			continue
		}
		if answer.ReadyMedia >= requirement.MinimumMedia || (requirement.ImpossibilityAllowed && reason != "") {
			result.Satisfied++
			continue
		}
		result.Missing = append(result.Missing, requirement.Key)
		result.PendingKeys = append(result.PendingKeys, requirement.Key)
	}
	result.Complete = result.Satisfied == result.Total && len(result.Blocked) == 0
	result.RequiresAttention = !result.Complete
	if len(result.Blocked) > 0 {
		return result, apperror.New(apperror.InvalidState, result.Blocked[0], "media checks must complete before submission")
	}
	v2ExpectedPending := false
	for _, requirement := range requirements {
		if requirement.Expected && strings.HasPrefix(requirement.Key, "env:") && slices.Contains(result.PendingKeys, requirement.Key) {
			v2ExpectedPending = true
			break
		}
	}
	if !result.Complete && (!confirmIncomplete || v2ExpectedPending) {
		field := "requirements"
		if len(result.Blocked) > 0 {
			field = result.Blocked[0]
		} else if len(result.Missing) > 0 {
			field = result.Missing[0]
		}
		base := apperror.New(apperror.InvalidState, field, "explicit incomplete submission confirmation is required")
		if v2ExpectedPending {
			return result, PendingRequirementError{Keys: append([]string(nil), result.PendingKeys...), Err: base}
		}
		return result, base
	}
	return result, nil
}

func ValidateDescription(value string, required bool) error {
	value = strings.TrimSpace(value)
	if required && value == "" {
		return apperror.New(apperror.InvalidInput, "description", "description is required")
	}
	if utf8.RuneCountInString(value) > MaxTextRunes {
		return apperror.New(apperror.InvalidInput, "description", "description is too long")
	}
	if strings.Contains(strings.ToLower(value), "<script") {
		return apperror.New(apperror.InvalidInput, "description", "description contains unsafe text")
	}
	for _, r := range value {
		if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' {
			return apperror.New(apperror.InvalidInput, "description", "description contains unsafe text")
		}
	}
	return nil
}

type GPSPolicy struct {
	Required                      bool
	AllowGallery                  bool
	AssetLatitude, AssetLongitude *float64
	GeofenceMeters                float64
}

type GPSReading struct {
	Latitude, Longitude, AccuracyMeters float64
	CapturedAt                          time.Time
}

type GPSDecision struct {
	Accepted, Blocked, OutOfGeofence bool
	DistanceMeters                   float64
	Flags                            []string
}

func EvaluateGPS(policy GPSPolicy, reading *GPSReading, windowStartedAt time.Time) (GPSDecision, error) {
	decision := GPSDecision{}
	invalid := reading == nil || math.IsNaN(reading.Latitude) || math.IsNaN(reading.Longitude) || math.IsNaN(reading.AccuracyMeters) || reading.Latitude < -90 || reading.Latitude > 90 || reading.Longitude < -180 || reading.Longitude > 180 || reading.AccuracyMeters < 0
	stale := !invalid && (reading.CapturedAt.Before(windowStartedAt) || reading.CapturedAt.After(windowStartedAt.Add(GPSWindow)))
	inaccurate := !invalid && reading.AccuracyMeters > MaxGPSAccuracyM
	if invalid || stale || inaccurate {
		if invalid {
			decision.Flags = append(decision.Flags, "GPS_MISSING_OR_INVALID")
		} else if stale {
			decision.Flags = append(decision.Flags, "GPS_OUTSIDE_GUIDED_WINDOW")
		} else {
			decision.Flags = append(decision.Flags, "GPS_LOW_ACCURACY")
		}
		decision.Blocked = policy.Required
		decision.Accepted = !policy.Required
		return decision, nil
	}
	decision.Accepted = true
	if policy.AssetLatitude != nil && policy.AssetLongitude != nil {
		decision.DistanceMeters = haversine(*policy.AssetLatitude, *policy.AssetLongitude, reading.Latitude, reading.Longitude)
		radius := policy.GeofenceMeters
		if radius <= 0 {
			radius = 150
		}
		if decision.DistanceMeters > radius {
			decision.OutOfGeofence = true
			decision.Flags = append(decision.Flags, "OUT_OF_GEOFENCE")
		}
	}
	return decision, nil
}

func haversine(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadius = 6371008.8
	toRad := math.Pi / 180
	dLat, dLon := (lat2-lat1)*toRad, (lon2-lon1)*toRad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1*toRad)*math.Cos(lat2*toRad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return earthRadius * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}

type Admission struct {
	mu                       sync.Mutex
	GlobalLimit, TenantLimit int
	global                   int
	tenants                  map[string]int
}

func NewAdmission(global, perTenant int) *Admission {
	return &Admission{GlobalLimit: global, TenantLimit: perTenant, tenants: map[string]int{}}
}

func (a *Admission) Reserve(tenant string) (func(), error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if strings.TrimSpace(tenant) == "" || a.global >= a.GlobalLimit || a.tenants[tenant] >= a.TenantLimit {
		return nil, fmt.Errorf("capture admission limit reached")
	}
	a.global++
	a.tenants[tenant]++
	var once sync.Once
	return func() { once.Do(func() { a.mu.Lock(); defer a.mu.Unlock(); a.global--; a.tenants[tenant]-- }) }, nil
}
