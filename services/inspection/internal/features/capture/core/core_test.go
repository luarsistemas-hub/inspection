package core

import (
	"sync"
	"testing"
	"time"
)

func TestUT030CompletenessAcceptsEvidenceAndPermittedImpossibility(t *testing.T) {
	requirements := []Requirement{{Key: "photo", Required: true, MinimumMedia: 1}, {Key: "reason", Required: true, ImpossibilityAllowed: true, MinimumMedia: 1}}
	result, err := Evaluate(requirements, []Answer{{RequirementKey: "photo", ReadyMedia: 1}, {RequirementKey: "reason", Impossibility: "sem acesso"}}, false)
	if err != nil || !result.Complete || result.Satisfied != 2 || result.Total != 2 {
		t.Fatalf("complete evidence rejected: %+v %v", result, err)
	}
}

func TestUT031CompletenessNamesMissingAndBlockedRequirements(t *testing.T) {
	requirements := []Requirement{{Key: "photo", Required: true, MinimumMedia: 1}, {Key: "blocked", Required: true, MinimumMedia: 1}, {Key: "reason", Required: true, ImpossibilityAllowed: true, MinimumMedia: 1}}
	result, err := Evaluate(requirements, []Answer{{RequirementKey: "photo", ReadyMedia: 1}, {RequirementKey: "blocked", FailedMedia: 1}, {RequirementKey: "reason", Impossibility: "sem acesso"}}, false)
	if err == nil || result.Complete || len(result.Blocked) != 1 || result.Satisfied != 2 {
		t.Fatalf("unexpected completeness: %+v %v", result, err)
	}
	if result.Blocked[0] != "blocked" {
		t.Fatalf("blocked requirement not named: %+v", result)
	}
}

func TestCompletenessIgnoresOptionalRequirements(t *testing.T) {
	result, err := Evaluate([]Requirement{{Key: "required", Required: true, MinimumMedia: 1}, {Key: "optional", Required: false, MinimumMedia: 1}}, []Answer{{RequirementKey: "required", ReadyMedia: 1}}, false)
	if err != nil || !result.Complete || result.Total != 1 || result.Satisfied != 1 {
		t.Fatalf("optional requirement changed completeness: %+v %v", result, err)
	}
}

func TestUT060GPSAcceptsExactAccuracyAndTimeBoundary(t *testing.T) {
	now := time.Unix(1000, 0).UTC()
	lat, lon := -27.59, -48.55
	decision, err := EvaluateGPS(GPSPolicy{Required: true, AssetLatitude: &lat, AssetLongitude: &lon, GeofenceMeters: 150}, &GPSReading{Latitude: lat, Longitude: lon, AccuracyMeters: 50, CapturedAt: now.Add(60 * time.Second)}, now)
	if err != nil || !decision.Accepted || decision.Blocked || decision.DistanceMeters != 0 {
		t.Fatalf("boundary rejected: %+v %v", decision, err)
	}
}

func TestUT061GPSFlagsInvalidReadingsWithoutBlockingOptionalPolicy(t *testing.T) {
	now := time.Unix(1000, 0).UTC()
	decision, _ := EvaluateGPS(GPSPolicy{Required: false}, nil, now)
	if !decision.Accepted || decision.Blocked || len(decision.Flags) == 0 {
		t.Fatalf("optional GPS blocked: %+v", decision)
	}
	for _, reading := range []*GPSReading{{Latitude: 91, CapturedAt: now}, {Latitude: 0, Longitude: 0, AccuracyMeters: 51, CapturedAt: now}, {Latitude: 0, Longitude: 0, AccuracyMeters: 1, CapturedAt: now.Add(61 * time.Second)}} {
		decision, err := EvaluateGPS(GPSPolicy{Required: true}, reading, now)
		if err != nil || !decision.Blocked || decision.Accepted || len(decision.Flags) != 1 {
			t.Fatalf("invalid required GPS was not flagged: %+v %v", decision, err)
		}
	}
}

func TestUT063AtomicAdmission(t *testing.T) {
	admission := NewAdmission(10, 3)
	var wg sync.WaitGroup
	var mu sync.Mutex
	accepted := 0
	releases := make([]func(), 0, 3)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, err := admission.Reserve("tenant")
			if err == nil {
				mu.Lock()
				accepted++
				releases = append(releases, release)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if accepted != 3 {
		t.Fatalf("tenant limit overshot: %d", accepted)
	}
	for _, release := range releases {
		release()
		release()
	}
	if release, err := admission.Reserve("tenant"); err != nil {
		t.Fatalf("released capacity was not restored: %v", err)
	} else {
		release()
	}
}
