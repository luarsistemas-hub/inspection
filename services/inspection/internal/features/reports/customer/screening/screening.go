// Package screening is the single customer triage of report evidence, shared
// by the customer PDF, HTML and GraphQL projections: the snapshot's own
// blocks (availability and sensitive flags) and the screening decisions in
// force when the customer reads the report.
package screening

import (
	"context"
	"errors"

	"inspection/libs/identity"
	reportcore "inspection/services/inspection/internal/features/reports/core"
	customerreport "inspection/services/inspection/internal/features/reports/customer/core"
	"inspection/services/inspection/internal/platform/database"

	"gorm.io/gorm"
)

// ErrInvalidEvidence rejects a snapshot whose photo evidence ID is not an ID.
var ErrInvalidEvidence = errors.New("report evidence id is invalid")

// Redact turns one evidence item into a metadata-only placeholder:
// no image is loaded for it and nothing that addresses the frame (identifiers,
// digests, deep link) reaches the PDF. The item keeps its role, description and
// flags so the customer still sees that a visual exists but is unavailable.
//
// In report v3 an image that the issued report holds as usable is marked
// EvidenceWithheld instead of MISSING: it stays out of the PDF, but the
// classification label and the evidence notice still count it, exactly as the
// customer screen does from the same snapshot. v1/v2 keep MISSING.
func Redact(item *reportcore.Evidence, schemaVersion int) {
	if schemaVersion >= 3 && (item.Availability == "AVAILABLE" || item.Availability == reportcore.EvidenceWithheld) {
		item.Availability = reportcore.EvidenceWithheld
	} else {
		item.Availability = "MISSING"
	}
	item.EvidenceKind = ""
	item.FrameID, item.PreparationID, item.SourceMediaID = "", "", ""
	item.FrameDigest, item.DisplayDigest = "", ""
	item.PTSMicros, item.TimestampMS = nil, nil
	item.SourceAvailability = ""
}

// RedactBlocked applies the snapshot's own blocks (availability or
// sensitive flag) to the customer copy, without touching the stored snapshot.
func RedactBlocked(snapshot reportcore.Snapshot) reportcore.Snapshot {
	evidence := append([]reportcore.Evidence(nil), snapshot.Evidence...)
	for i := range evidence {
		if customerreport.EvidenceBlocked(evidence[i]) {
			Redact(&evidence[i], snapshot.SchemaVersion)
		}
	}
	snapshot.Evidence = evidence
	return snapshot
}

// ApplyDecisions applies the screening decisions in force at render
// time, with the same rule as the customer GraphQL view: a photo is shown only
// when its latest terminal decision is CLEARED, OVERRIDDEN or DISABLED, and a
// frame is hidden when its source video was blocked.
func ApplyDecisions(ctx context.Context, db *gorm.DB, tenantID identity.ID, snapshot *reportcore.Snapshot) error {
	var photoIDs, sourceIDs []identity.ID
	for _, item := range snapshot.Evidence {
		if !reportcore.EvidenceAvailable(item.Availability) {
			continue
		}
		if item.EvidenceKind == "VIDEO_FRAME" {
			if id, err := identity.ParseID(item.SourceMediaID); err == nil {
				sourceIDs = append(sourceIDs, id)
			}
			continue
		}
		id, err := identity.ParseID(item.ID)
		if err != nil {
			// Legacy v1/v2 snapshots sometimes carry presentation-only evidence
			// identifiers. They cannot have a screening row keyed by a media UUID,
			// and rejecting them would change historical customer report reads.
			if snapshot.SchemaVersion < 3 {
				continue
			}
			return ErrInvalidEvidence
		}
		photoIDs = append(photoIDs, id)
	}
	cleared := make(map[string]bool, len(photoIDs))
	if len(photoIDs) > 0 {
		var runs []database.ScreeningRun
		if err := db.WithContext(ctx).Select("id", "media_id", "status", "created_at").
			Where("tenant_id=? AND media_id IN ? AND status IN ?", tenantID, photoIDs, []string{"CLEARED", "BLOCKED", "OVERRIDDEN", "DISABLED"}).
			Order("created_at DESC, id DESC").Find(&runs).Error; err != nil {
			return err
		}
		decided := make(map[identity.ID]bool, len(runs))
		for _, run := range runs {
			if decided[run.MediaID] {
				continue
			}
			decided[run.MediaID] = true
			cleared[run.MediaID.String()] = run.Status != "BLOCKED"
		}
	}
	blockedSources := make(map[string]bool, len(sourceIDs))
	if len(sourceIDs) > 0 {
		var sources []database.VideoSource
		if err := db.WithContext(ctx).Where("tenant_id=? AND media_id IN ?", tenantID, sourceIDs).Find(&sources).Error; err != nil {
			return err
		}
		for _, source := range sources {
			if source.ScreeningStatus == "BLOCKED" {
				blockedSources[source.MediaID.String()] = true
			}
		}
	}
	for i := range snapshot.Evidence {
		item := &snapshot.Evidence[i]
		if !reportcore.EvidenceAvailable(item.Availability) {
			continue
		}
		if item.EvidenceKind == "VIDEO_FRAME" {
			if blockedSources[item.SourceMediaID] {
				Redact(item, snapshot.SchemaVersion)
			}
			continue
		}
		if !cleared[item.ID] {
			Redact(item, snapshot.SchemaVersion)
		}
	}
	return nil
}
