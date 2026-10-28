package video

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
)

// Profile contains the deployed, immutable preparation choices.
type Profile struct {
	Name                string `json:"name"`
	Version             int    `json:"version"`
	SelectionVersion    int    `json:"selectionVersion"`
	MaxWidth            int    `json:"maxWidth"`
	MaxHeight           int    `json:"maxHeight"`
	JPEGQuality         int    `json:"jpegQuality"`
	MaxFramesPerSecond  int    `json:"maxFramesPerSecond"`
	AllowRetainedFrames bool   `json:"allowRetainedFrames"`
}

var ErrUnknownProfile = errors.New("video: unknown deployed profile")

var deployedProfiles = func() map[string]Profile {
	profiles := []Profile{
		{Name: "standard", Version: 1, SelectionVersion: 1, MaxWidth: 2048, MaxHeight: 2048, JPEGQuality: 85, MaxFramesPerSecond: 2, AllowRetainedFrames: false},
		{Name: "standard", Version: 2, SelectionVersion: 2, MaxWidth: 2048, MaxHeight: 2048, JPEGQuality: 85, MaxFramesPerSecond: 2, AllowRetainedFrames: false},
	}
	result := make(map[string]Profile, len(profiles))
	for _, profile := range profiles {
		result[profile.Digest()] = profile
	}
	return result
}()

// Digest returns the deterministic content digest for a deployed profile.
func (p Profile) Digest() string {
	raw, _ := json.Marshal(p)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// DefaultProfile returns the explicitly selected current preparation profile.
func DefaultProfile() Profile { return deployedProfiles[defaultProfileDigest] }

var defaultProfileDigest = Profile{Name: "standard", Version: 2, SelectionVersion: 2, MaxWidth: 2048, MaxHeight: 2048, JPEGQuality: 85, MaxFramesPerSecond: 2, AllowRetainedFrames: false}.Digest()

// ProfileByNameVersion resolves a deployed profile without relying on map order.
func ProfileByNameVersion(name string, version int) (Profile, error) {
	for _, profile := range deployedProfiles {
		if profile.Name == name && profile.Version == version {
			return profile, nil
		}
	}
	return Profile{}, ErrUnknownProfile
}

// ProfileByDigest resolves only profiles deployed by this build.
func ProfileByDigest(digest string) (Profile, error) {
	profile, ok := deployedProfiles[digest]
	if !ok {
		return Profile{}, ErrUnknownProfile
	}
	return profile, nil
}
