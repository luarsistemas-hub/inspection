package video

import (
	"errors"
	"testing"
)

func TestDeployedProfileDigestIsStableAndLookupRejectsUnknown(t *testing.T) {
	profile := DefaultProfile()
	if profile.Digest() == "" {
		t.Fatal("default profile digest is empty")
	}
	if profile.Digest() != DefaultProfile().Digest() {
		t.Fatal("default profile digest changed between lookups")
	}
	resolved, err := ProfileByDigest(profile.Digest())
	if err != nil || resolved != profile {
		t.Fatalf("profile lookup = %+v, %v; want %+v", resolved, err, profile)
	}
	if _, err := ProfileByDigest("unknown-profile"); !errors.Is(err, ErrUnknownProfile) {
		t.Fatalf("unknown profile error = %v", err)
	}
}
