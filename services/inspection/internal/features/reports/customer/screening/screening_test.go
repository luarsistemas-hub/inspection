package screening

import (
	"context"
	"errors"
	"testing"

	"inspection/libs/identity"
	reportcore "inspection/services/inspection/internal/features/reports/core"
)

func TestApplyDecisionsKeepsLegacyPresentationEvidenceReadable(t *testing.T) {
	for _, schemaVersion := range []int{1, 2} {
		snapshot := reportcore.Snapshot{SchemaVersion: schemaVersion, Evidence: []reportcore.Evidence{{ID: "origin-photo", Availability: "AVAILABLE"}}}
		if err := ApplyDecisions(context.Background(), nil, identity.NewID(), &snapshot); err != nil {
			t.Fatalf("v%d legacy evidence returned %v", schemaVersion, err)
		}
	}
}

func TestApplyDecisionsRejectsInvalidV3EvidenceIdentity(t *testing.T) {
	snapshot := reportcore.Snapshot{SchemaVersion: 3, Evidence: []reportcore.Evidence{{ID: "photo", Availability: "AVAILABLE"}}}
	if err := ApplyDecisions(context.Background(), nil, identity.NewID(), &snapshot); !errors.Is(err, ErrInvalidEvidence) {
		t.Fatalf("v3 invalid evidence error=%v, want ErrInvalidEvidence", err)
	}
}
