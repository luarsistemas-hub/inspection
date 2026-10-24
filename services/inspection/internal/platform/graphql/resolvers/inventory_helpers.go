package resolvers

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"inspection/libs/identity"
	inventorycore "inspection/services/inspection/internal/features/inventory/core"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	graphql1 "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
)

func inventoryID(raw, field string) (identity.ID, error) {
	id, err := identity.ParseID(raw)
	if err != nil {
		return identity.ID{}, invalidID(field)
	}
	return id, nil
}

func (r *Resolver) inventoryAuthorize(ctx context.Context, assetID identity.ID, access string) error {
	meta, ok := requestctx.FromContext(ctx)
	if !ok {
		return unauthenticated()
	}
	var asset database.Asset
	if err := (tenanttx.Runner{DB: r.DB}).Within(ctx, meta.TenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND id=?", meta.TenantID, assetID).First(&asset).Error
	}); err != nil {
		return apperror.New(apperror.NotFound, "assetId", "asset not found")
	}
	roles := []string{auth.Employee, auth.Manager, auth.TenantAdmin, auth.Viewer, auth.CustomerViewer}
	switch access {
	case "mutate":
		roles = []string{auth.Employee, auth.Manager, auth.TenantAdmin}
	case "draft":
		roles = []string{auth.Employee, auth.Manager, auth.TenantAdmin}
	case "trail":
		roles = []string{auth.Auditor, auth.Manager, auth.TenantAdmin}
	}
	product := auth.DashboardProduct
	if access == "trail" {
		product = auth.AdminProduct
	}
	principal, err := r.Authorizer.Authorize(ctx, auth.AuthorizationRequest{TenantID: meta.TenantID, Product: product, Roles: roles, Mutate: access == "mutate"})
	if err != nil {
		return err
	}
	if containsInventoryRole(principal.Roles, auth.TenantAdmin) {
		return nil
	}
	if r.inventoryAssetScopeAllowed(ctx, meta.TenantID, principal, asset) {
		return nil
	}
	return apperror.New(apperror.Forbidden, "", "access denied")
}

func containsInventoryRole(roles []string, role string) bool {
	for _, candidate := range roles {
		if candidate == role {
			return true
		}
	}
	return false
}

// inventoryAssetScopeAllowed keeps BU/asset grants effective for archived assets,
// so inventory reads and activation can return the domain's archived state.
func (r *Resolver) inventoryAssetScopeAllowed(ctx context.Context, tenant identity.ID, principal requestctx.Principal, asset database.Asset) bool {
	if principalHasInventoryAssetScope(principal.Scopes, asset.ID) {
		return true
	}
	var allowed bool
	_ = (tenanttx.Runner{DB: r.DB}).Within(ctx, tenant, func(tx *gorm.DB) error {
		var grants []database.ResourceScope
		if err := tx.Where("tenant_id=? AND membership_id=?", tenant, principal.MembershipID).Find(&grants).Error; err != nil {
			return err
		}
		for _, grant := range grants {
			if (grant.Kind == "ASSET" && grant.ResourceID == asset.ID) || (grant.Kind == "BUSINESS_UNIT" && grant.ResourceID == asset.BusinessUnitID) {
				allowed = true
				break
			}
		}
		return nil
	})
	return allowed
}

func principalHasInventoryAssetScope(scopes []requestctx.Scope, assetID identity.ID) bool {
	for _, scope := range scopes {
		if scope.Kind == "ASSET" && scope.ID == assetID {
			return true
		}
	}
	return false
}

func canReadInventoryDraft(roles []string) bool {
	for _, role := range roles {
		if role == auth.Employee || role == auth.Manager || role == auth.TenantAdmin {
			return true
		}
	}
	return false
}

func (r *Resolver) inventoryVersionAsset(ctx context.Context, tenant, versionID identity.ID) (identity.ID, error) {
	var assetID identity.ID
	err := (tenanttx.Runner{DB: r.DB}).Within(ctx, tenant, func(tx *gorm.DB) error {
		var version database.OriginVersion
		if err := tx.Where("tenant_id=? AND id=? AND has_inventory", tenant, versionID).First(&version).Error; err != nil {
			return apperror.New(apperror.NotFound, "originVersionId", "inventory version not found")
		}
		var origin database.Origin
		if err := tx.Where("tenant_id=? AND id=?", tenant, version.OriginID).First(&origin).Error; err != nil {
			return apperror.New(apperror.NotFound, "originVersionId", "inventory version not found")
		}
		assetID = origin.AssetID
		return nil
	})
	return assetID, err
}

func inventoryUserError(err error, clientMutationID string) *graphql1.UserError {
	_ = clientMutationID
	return userErrorFrom(err)
}
func businessInventoryError(err error) bool {
	code, _, _ := apperror.Public(err)
	return code != apperror.Internal && code != apperror.DependencyUnavailable && code != apperror.Unauthenticated
}
func inventoryVersion(row database.OriginVersion, revision int64) *graphql1.OriginVersion {
	out := &graphql1.OriginVersion{ID: row.ID.String(), OriginID: row.OriginID.String(), VersionNumber: row.VersionNumber, Status: row.Status, MediaKind: row.MediaKind, Selectable: row.Status == "ACTIVE" || row.Status == "SUPERSEDED", CaptureSubmitted: row.SubmittedAt != nil, Purpose: row.Purpose, Source: &row.Source, HasInventory: row.HasInventory, DraftState: row.DraftState}
	out.OriginRevision = int(revision)
	if row.VideoPreparationID != nil {
		x := row.VideoPreparationID.String()
		out.VideoPreparationID = &x
	}
	if row.SupersedesID != nil {
		x := row.SupersedesID.String()
		out.SupersedesID = &x
	}
	if row.ActivatedAt != nil {
		x := row.ActivatedAt.UTC().Format(time.RFC3339Nano)
		out.ActivatedAt = &x
	}
	if row.ActivatedByMembershipID != nil {
		x := row.ActivatedByMembershipID.String()
		out.ActivatedBy = &x
	}
	return out
}

func (r *Resolver) inventoryOriginRevision(ctx context.Context, tenant, origin identity.ID) (int64, error) {
	var revision int64
	err := (tenanttx.Runner{DB: r.DB}).Within(ctx, tenant, func(tx *gorm.DB) error {
		var row database.Origin
		if err := tx.Select("version").Where("tenant_id=? AND id=?", tenant, origin).First(&row).Error; err != nil {
			return err
		}
		revision = row.Version
		return nil
	})
	return revision, err
}

func inventoryCustomerVersion(row database.OriginVersion, revision int64) *graphql1.OriginVersion {
	return &graphql1.OriginVersion{ID: row.ID.String(), OriginID: row.OriginID.String(), VersionNumber: row.VersionNumber, OriginRevision: int(revision), Status: row.Status, MediaKind: row.MediaKind, CaptureSubmitted: row.SubmittedAt != nil, Selectable: row.Status == "ACTIVE" || row.Status == "SUPERSEDED", Purpose: row.Purpose, HasInventory: row.HasInventory}
}

func applyInventoryVersionSummary(node *graphql1.OriginVersion, counts *graphql1.InventoryCounts, contestation *graphql1.ContestationSummary) {
	if node == nil {
		return
	}
	node.Counts = counts
	node.ContestationSummary = contestation
}

func (r *Resolver) inventoryVersionSummary(ctx context.Context, tenant, version identity.ID) (*graphql1.InventoryCounts, *graphql1.ContestationSummary, error) {
	counts := &graphql1.InventoryCounts{}
	contest := &graphql1.ContestationSummary{}
	err := (tenanttx.Runner{DB: r.DB}).Within(ctx, tenant, func(tx *gorm.DB) error {
		var included, rejected, pending int64
		if err := tx.Model(&database.InventoryItem{}).Where("tenant_id=? AND origin_version_id=? AND decision IN ('ACCEPTED','EDITED','ADDED') AND merged_into_id IS NULL", tenant, version).Count(&included).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.InventoryItem{}).Where("tenant_id=? AND origin_version_id=? AND decision='REJECTED' AND merged_into_id IS NULL", tenant, version).Count(&rejected).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.InventoryItem{}).Where("tenant_id=? AND origin_version_id=? AND decision='PENDING' AND merged_into_id IS NULL", tenant, version).Count(&pending).Error; err != nil {
			return err
		}
		counts.Included, counts.Rejected, counts.NotReviewed = int(included), int(rejected), int(pending)
		var rows []struct {
			Status string `gorm:"column:response"`
			Count  int64
		}
		if err := tx.Model(&database.CounterpartyInvitation{}).Select("response, count(*) AS count").Where("tenant_id=? AND origin_version_id=?", tenant, version).Group("response").Scan(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			switch row.Status {
			case "PENDING":
				contest.Pending = int(row.Count)
			case "ACKNOWLEDGED":
				contest.Acknowledged = int(row.Count)
			case "CONTESTED":
				contest.Contested = int(row.Count)
			case "NO_RESPONSE":
				contest.NoResponse = int(row.Count)
			}
		}
		return nil
	})
	return counts, contest, err
}

func (r *Resolver) inventoryVersionSummaries(ctx context.Context, tenant identity.ID, versions []database.OriginVersion) (map[identity.ID]*graphql1.InventoryCounts, map[identity.ID]*graphql1.ContestationSummary, error) {
	counts := make(map[identity.ID]*graphql1.InventoryCounts, len(versions))
	contests := make(map[identity.ID]*graphql1.ContestationSummary, len(versions))
	ids := make([]identity.ID, 0, len(versions))
	for _, version := range versions {
		ids = append(ids, version.ID)
		counts[version.ID] = &graphql1.InventoryCounts{}
		contests[version.ID] = &graphql1.ContestationSummary{}
	}
	if len(ids) == 0 {
		return counts, contests, nil
	}
	err := (tenanttx.Runner{DB: r.DB}).Within(ctx, tenant, func(tx *gorm.DB) error {
		var rows []struct {
			VersionID identity.ID `gorm:"column:origin_version_id"`
			Decision  string      `gorm:"column:decision"`
			Count     int64
		}
		if err := tx.Model(&database.InventoryItem{}).Joins("JOIN origins.inventory_environments e ON e.tenant_id=inventory_items.tenant_id AND e.id=inventory_items.environment_id AND e.origin_version_id=inventory_items.origin_version_id AND e.decision<>'REJECTED'").Select("inventory_items.origin_version_id, inventory_items.decision, count(*) AS count").Where("inventory_items.tenant_id=? AND inventory_items.origin_version_id IN ? AND inventory_items.merged_into_id IS NULL", tenant, ids).Group("inventory_items.origin_version_id, inventory_items.decision").Scan(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			target := counts[row.VersionID]
			switch row.Decision {
			case "ACCEPTED", "EDITED", "ADDED":
				target.Included += int(row.Count)
			case "REJECTED":
				target.Rejected += int(row.Count)
			case "PENDING":
				target.NotReviewed += int(row.Count)
			}
		}
		var events []struct {
			VersionID identity.ID `gorm:"column:origin_version_id"`
			Response  string
			Count     int64
		}
		if err := tx.Model(&database.CounterpartyInvitation{}).Select("origin_version_id,response,count(*) AS count").Where("tenant_id=? AND origin_version_id IN ?", tenant, ids).Group("origin_version_id,response").Scan(&events).Error; err != nil {
			return err
		}
		for _, row := range events {
			target := contests[row.VersionID]
			switch row.Response {
			case "PENDING":
				target.Pending += int(row.Count)
			case "ACKNOWLEDGED":
				target.Acknowledged += int(row.Count)
			case "CONTESTED":
				target.Contested += int(row.Count)
			case "NO_RESPONSE":
				target.NoResponse += int(row.Count)
			}
		}
		return nil
	})
	return counts, contests, err
}
func inventoryItem(view inventorycore.ItemView) *graphql1.InventoryItem {
	row := view.Item
	out := &graphql1.InventoryItem{ID: row.ID.String(), IdentityID: row.IdentityID.String(), EnvironmentID: row.EnvironmentID.String(), TypeCode: row.TypeCode, Name: row.Name, Material: row.Material, Color: row.Color, Quantity: row.Quantity, Condition: row.Condition, Observation: row.Observation, Limitation: row.Limitation, ConfidenceBps: row.ConfidenceBps, Origin: row.Origin, Declared: row.Declared, Decision: row.Decision, RejectionReason: row.RejectionReason, RejectionText: row.RejectionText, Version: row.Version, Evidence: []*graphql1.InventoryEvidence{}}
	if len(row.Suggested) > 0 {
		var value map[string]any
		_ = json.Unmarshal(row.Suggested, &value)
		out.Suggested = value
	}
	for _, e := range view.Evidence {
		var frame *string
		if e.Evidence.FrameID != nil {
			x := e.Evidence.FrameID.String()
			frame = &x
		}
		var region map[string]any
		if len(e.Evidence.Region) > 0 {
			_ = json.Unmarshal(e.Evidence.Region, &region)
		}
		out.Evidence = append(out.Evidence, &graphql1.InventoryEvidence{ID: e.Evidence.ID.String(), Kind: e.Evidence.Kind, MediaID: e.Evidence.MediaID.String(), FrameID: frame, Region: region, Position: e.Evidence.Position, Availability: e.Availability})
	}
	return out
}

func (r *Resolver) inventorySingleItem(ctx context.Context, tenant identity.ID, row database.InventoryItem) (*graphql1.InventoryItem, error) {
	var evidence []database.InventoryItemEvidence
	availability := map[identity.ID]string{}
	err := (tenanttx.Runner{DB: r.DB}).Within(ctx, tenant, func(tx *gorm.DB) error {
		if err := tx.Where("tenant_id=? AND item_id=?", tenant, row.ID).Order("position,id").Find(&evidence).Error; err != nil {
			return err
		}
		var err error
		availability, err = inventorycore.LoadEvidenceAvailability(tx, tenant, evidence)
		return err
	})
	if err != nil {
		return nil, err
	}
	view := inventorycore.ItemView{Item: row}
	for _, e := range evidence {
		view.Evidence = append(view.Evidence, inventorycore.EvidenceView{Evidence: e, Availability: availability[e.ID]})
	}
	return inventoryItem(view), nil
}

func inventoryCustomerEnvironment(view inventorycore.EnvironmentView) *graphql1.InventoryEnvironment {
	row := view.Environment
	out := &graphql1.InventoryEnvironment{ID: "", IdentityID: "", TypeCode: row.TypeCode, Name: row.Name, Items: []*graphql1.InventoryItem{}}
	for _, item := range view.Items {
		out.Items = append(out.Items, inventoryCustomerItem(item))
	}
	return out
}

func inventoryCustomerItem(view inventorycore.ItemView) *graphql1.InventoryItem {
	row := view.Item
	out := &graphql1.InventoryItem{TypeCode: row.TypeCode, Name: row.Name, Material: row.Material, Color: row.Color, Quantity: row.Quantity, Condition: row.Condition, Observation: row.Observation, Limitation: row.Limitation, Evidence: []*graphql1.InventoryEvidence{}}
	for _, evidence := range view.Evidence {
		if evidence.Availability != "AVAILABLE" {
			continue
		}
		var frame *string
		if evidence.Evidence.FrameID != nil {
			value := evidence.Evidence.FrameID.String()
			frame = &value
		}
		out.Evidence = append(out.Evidence, &graphql1.InventoryEvidence{Kind: evidence.Evidence.Kind, MediaID: evidence.Evidence.MediaID.String(), FrameID: frame, Position: evidence.Evidence.Position, Availability: evidence.Availability})
	}
	return out
}
func inventoryEnvironment(view inventorycore.EnvironmentView) *graphql1.InventoryEnvironment {
	row := view.Environment
	out := &graphql1.InventoryEnvironment{ID: row.ID.String(), IdentityID: row.IdentityID.String(), TypeCode: row.TypeCode, Name: row.Name, Expected: row.Expected, AddedDuringCapture: row.AddedDuringCapture, RequirementKey: row.RequirementKey, Decision: row.Decision, Position: row.Position, Items: []*graphql1.InventoryItem{}}
	for _, it := range view.Items {
		out.Items = append(out.Items, inventoryItem(it))
	}
	return out
}
func inventoryJSON(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return m
}
func inventoryTrail(suggestions []database.InventoryItem, suggestionEvidence map[identity.ID][]identity.ID, rows []database.InventoryDecision, counterparty []database.CounterpartyInvitation) *graphql1.InventoryTrail {
	out := &graphql1.InventoryTrail{Suggestions: []*graphql1.InventoryTrailEntry{}, Decisions: []*graphql1.InventoryTrailEntry{}, CounterpartyEvents: []*graphql1.InventoryTrailEntry{}}
	for _, row := range suggestions {
		after := inventoryJSON(row.Suggested)
		evidence := make([]string, 0, len(suggestionEvidence[row.ID]))
		for _, id := range suggestionEvidence[row.ID] {
			evidence = append(evidence, id.String())
		}
		if after == nil {
			after = map[string]any{}
		}
		after["evidenceIds"] = evidence
		out.Suggestions = append(out.Suggestions, &graphql1.InventoryTrailEntry{ID: row.ID.String(), TargetType: "ITEM", TargetID: row.ID.String(), Action: "AI_SUGGESTED", After: after, OccurredAt: row.CreatedAt.UTC().Format(time.RFC3339Nano)})
	}
	for _, row := range rows {
		var actor *string
		if row.ActorMembershipID != nil {
			x := row.ActorMembershipID.String()
			actor = &x
		}
		out.Decisions = append(out.Decisions, &graphql1.InventoryTrailEntry{ID: row.ID.String(), TargetType: row.TargetType, TargetID: row.TargetID.String(), Action: row.Action, Before: inventoryJSON(row.Before), After: inventoryJSON(row.After), Reason: row.Reason, ActorMembershipID: actor, OccurredAt: row.OccurredAt.UTC().Format(time.RFC3339Nano)})
	}
	for _, row := range counterparty {
		timestamp := row.CreatedAt
		action := "INVITATION_CREATED"
		after := map[string]any{"response": row.Response, "resolution": row.Resolution}
		if row.RespondedAt != nil {
			timestamp = *row.RespondedAt
			action = "RESPONDED"
		}
		if row.ResolvedAt != nil {
			timestamp = *row.ResolvedAt
			action = "RESOLVED"
		}
		reason := row.ResolutionReason
		if reason == nil {
			reason = row.Statement
		}
		var actor *string
		if row.ResolvedBy != nil {
			x := row.ResolvedBy.String()
			actor = &x
		}
		out.CounterpartyEvents = append(out.CounterpartyEvents, &graphql1.InventoryTrailEntry{ID: row.ID.String(), TargetType: "COUNTERPARTY", TargetID: row.ID.String(), Action: action, After: after, Reason: reason, ActorMembershipID: actor, OccurredAt: timestamp.UTC().Format(time.RFC3339Nano)})
	}
	return out
}
func mutationInventoryError(err error, clientID string) ([]*graphql1.UserError, error) {
	if businessInventoryError(err) {
		return []*graphql1.UserError{inventoryUserError(err, clientID)}, nil
	}
	return nil, err
}
func checkInventoryVersion(ctx context.Context, db *gorm.DB, tenant, id identity.ID) (database.OriginVersion, error) {
	var row database.OriginVersion
	err := (tenanttx.Runner{DB: db}).Within(ctx, tenant, func(tx *gorm.DB) error { return tx.Where("tenant_id=? AND id=?", tenant, id).First(&row).Error })
	return row, err
}
func isRecordNotFound(err error) bool { return errors.Is(err, gorm.ErrRecordNotFound) }
