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

func TestBuildKeepsPairedEvidenceTogether(t *testing.T) {
	currentID, originID := identity.NewID(), identity.NewID()
	images, _, err := Build(
		[]Evidence{{ID: currentID, Source: "CURRENT", PairID: "pair-1", Position: "CURRENT_1", Digest: "current", DataURL: "data:image/jpeg;base64,current"}},
		[]Evidence{{ID: originID, Source: "ORIGIN", PairID: "pair-1", Position: "ORIGIN", Digest: "origin", DataURL: "data:image/jpeg;base64,origin"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 2 || images[0].Source != "ORIGIN" || images[1].Source != "CURRENT" {
		t.Fatalf("paired evidence was not grouped: %+v", images)
	}
	for _, image := range images {
		if image.PairID != "pair-1" || image.Position == "" {
			t.Fatalf("pair lineage was lost: %+v", image)
		}
	}
}
