package core

import (
	"sort"

	"inspection/libs/identity"

	"gorm.io/gorm"
)

// IncludedItemDecisions lists the inventory item decisions that keep an item in
// an activated baseline. Rejected and pending items never own a source.
const IncludedItemDecisions = "('ACCEPTED','EDITED','ADDED')"

// referencingVersionsSQL selects every origin version that owns the source
// @media: legacy origin evidence, included inventory item evidence and legacy
// video versions through their frame-bank preparation.
var referencingVersionsSQL = ReferencingVersionsFor("@tenant", "@media")

// ReferencingVersionsFor renders the single source-ownership subquery for the
// given tenant and media SQL expressions (placeholders or outer-query columns),
// so batched readers can correlate it per source without diverging from the
// rules used by retention transitions, holds and expiry claims.
func ReferencingVersionsFor(tenantExpr, mediaExpr string) string {
	return `SELECT e.origin_version_id FROM origins.origin_evidence e WHERE e.tenant_id=` + tenantExpr + ` AND e.media_id=` + mediaExpr + `
	UNION SELECT i.origin_version_id FROM origins.inventory_item_evidence e
	JOIN origins.inventory_items i ON i.tenant_id=e.tenant_id AND i.id=e.item_id
	JOIN origins.inventory_environments env ON env.tenant_id=i.tenant_id AND env.id=i.environment_id
	WHERE e.tenant_id=` + tenantExpr + ` AND e.media_id=` + mediaExpr + ` AND i.decision IN ` + IncludedItemDecisions + ` AND i.merged_into_id IS NULL AND env.decision<>'REJECTED'
	UNION SELECT v2.id FROM origins.origin_versions v2
	JOIN media.video_preparation_jobs p ON p.tenant_id=v2.tenant_id AND p.id=v2.video_preparation_id
	WHERE v2.tenant_id=` + tenantExpr + ` AND p.media_id=` + mediaExpr
}

func sourceArgs(tenantID, mediaID identity.ID) map[string]any {
	return map[string]any{"tenant": tenantID, "media": mediaID}
}

// VersionSourceMediaIDs returns, in a stable order, every video source media
// referenced by the given versions through any evidence path. It is broader
// than referencingVersionsSQL on purpose: a transition must revisit sources
// whose only reference was just rejected or superseded.
func VersionSourceMediaIDs(tx *gorm.DB, tenantID identity.ID, versionIDs []identity.ID) ([]identity.ID, error) {
	versions := make([]identity.ID, 0, len(versionIDs))
	for _, id := range versionIDs {
		if id != (identity.ID{}) {
			versions = append(versions, id)
		}
	}
	if len(versions) == 0 {
		return nil, nil
	}
	var mediaIDs []identity.ID
	if err := tx.Raw(`SELECT s.media_id FROM media.video_sources s WHERE s.tenant_id=@tenant AND s.media_id IN (
		SELECT e.media_id FROM origins.origin_evidence e WHERE e.tenant_id=@tenant AND e.origin_version_id IN @versions
		UNION SELECT e.media_id FROM origins.inventory_item_evidence e
		JOIN origins.inventory_items i ON i.tenant_id=e.tenant_id AND i.id=e.item_id
		JOIN origins.inventory_environments env ON env.tenant_id=i.tenant_id AND env.id=i.environment_id
		WHERE i.tenant_id=@tenant AND i.origin_version_id IN @versions AND env.decision<>'REJECTED'
		UNION SELECT p.media_id FROM origins.origin_versions v
		JOIN media.video_preparation_jobs p ON p.tenant_id=v.tenant_id AND p.id=v.video_preparation_id
		WHERE v.tenant_id=@tenant AND v.id IN @versions
	)`, map[string]any{"tenant": tenantID, "versions": versions}).Scan(&mediaIDs).Error; err != nil {
		return nil, err
	}
	sort.Slice(mediaIDs, func(i, j int) bool { return mediaIDs[i].String() < mediaIDs[j].String() })
	return mediaIDs, nil
}
