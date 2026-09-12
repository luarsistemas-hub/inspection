package comparative

import (
	"inspection/libs/identity"
	"testing"
)

func TestBuildIncludesLineageAndStableDigest(t *testing.T) {
	a, b := identity.NewID(), identity.NewID()
	current := []Evidence{{ID: a, Source: "CURRENT", Digest: "a", DataURL: "data:image/jpeg;base64,A"}}
	origin := []Evidence{{ID: b, Source: "ORIGIN", Digest: "b", DataURL: "data:image/jpeg;base64,B"}}
	ones, digest, err := Build(current, origin)
	if err != nil || len(ones) != 2 || digest == "" {
		t.Fatalf("build = %+v %q %v", ones, digest, err)
	}
	if ones[0].Source != "CURRENT" || ones[1].Source != "ORIGIN" {
		t.Fatalf("lineage order lost: %+v", ones)
	}
	_, same, err := Build(current, origin)
	if err != nil || same != digest {
		t.Fatalf("digest changed: %q %q %v", digest, same, err)
	}
}
