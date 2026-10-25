package video

import (
	"errors"
	"testing"
)

func TestUT150ProfilesKeepV1DigestAndDefaultV2IsDeterministic(t *testing.T) {
	profile := DefaultProfile()
	if profile.Digest() == "" || profile.Version != 2 || profile.SelectionVersion != 2 {
		t.Fatalf("default profile=%+v; want standard v2", profile)
	}
	for i := 0; i < 100; i++ {
		if profile.Digest() != DefaultProfile().Digest() {
			t.Fatal("default profile digest changed between lookups")
		}
	}
	resolved, err := ProfileByDigest(profile.Digest())
	if err != nil || resolved != profile {
		t.Fatalf("profile lookup = %+v, %v; want %+v", resolved, err, profile)
	}
	if _, err := ProfileByDigest("unknown-profile"); !errors.Is(err, ErrUnknownProfile) {
		t.Fatalf("unknown profile error = %v", err)
	}
	v1 := Profile{Name: "standard", Version: 1, SelectionVersion: 1, MaxWidth: 2048, MaxHeight: 2048, JPEGQuality: 85, MaxFramesPerSecond: 2, AllowRetainedFrames: false}
	legacy, err := ProfileByDigest(v1.Digest())
	if err != nil || legacy != v1 || legacy.Digest() == profile.Digest() {
		t.Fatalf("legacy profile lookup=%+v err=%v; want original v1 digest", legacy, err)
	}
	resolved, err = ProfileByNameVersion("standard", 2)
	if err != nil || resolved != profile {
		t.Fatalf("name/version lookup=%+v err=%v", resolved, err)
	}
}
