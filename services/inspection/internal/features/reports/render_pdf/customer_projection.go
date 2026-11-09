package render_pdf

import (
	"context"
	"errors"

	"inspection/libs/identity"
	reportcore "inspection/services/inspection/internal/features/reports/core"
	"inspection/services/inspection/internal/features/reports/customer/screening"

	"gorm.io/gorm"
)

// redactForCustomer is the shared customer placeholder of one evidence item
// (screening.Redact).
func redactForCustomer(item *reportcore.Evidence, schemaVersion int) {
	screening.Redact(item, schemaVersion)
}

// redactBlockedEvidence applies the snapshot's own blocks to the customer copy
// (screening.RedactBlocked).
func redactBlockedEvidence(snapshot reportcore.Snapshot) reportcore.Snapshot {
	return screening.RedactBlocked(snapshot)
}

// redactScreenedEvidence applies the screening decisions in force at render
// time with the same rule as the customer GraphQL view
// (screening.ApplyDecisions).
func redactScreenedEvidence(ctx context.Context, db *gorm.DB, tenantID identity.ID, snapshot *reportcore.Snapshot) error {
	err := screening.ApplyDecisions(ctx, db, tenantID, snapshot)
	if errors.Is(err, screening.ErrInvalidEvidence) {
		return errPermanentPDF
	}
	return err
}
