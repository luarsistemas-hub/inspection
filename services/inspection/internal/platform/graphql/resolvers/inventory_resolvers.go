package resolvers

import (
	"context"
	"encoding/json"
	"errors"

	"inspection/libs/identity"
	inventoryactivate "inspection/services/inspection/internal/features/inventory/activate_version"
	inventoryadditem "inspection/services/inspection/internal/features/inventory/add_item"
	inventorybaseline "inspection/services/inspection/internal/features/inventory/baseline"
	inventorybulk "inspection/services/inspection/internal/features/inventory/bulk_accept_environment"
	inventorycore "inspection/services/inspection/internal/features/inventory/core"
	inventorydecide "inspection/services/inspection/internal/features/inventory/decide_items"
	inventorydiscard "inspection/services/inspection/internal/features/inventory/discard_draft"
	inventorydraft "inspection/services/inspection/internal/features/inventory/draft"
	inventorymerge "inspection/services/inspection/internal/features/inventory/merge_items"
	inventoryremoveenv "inspection/services/inspection/internal/features/inventory/remove_environment"
	inventoryrequest "inspection/services/inspection/internal/features/inventory/request_cataloging"
	inventoryretry "inspection/services/inspection/internal/features/inventory/retry_draft"
	inventorysplit "inspection/services/inspection/internal/features/inventory/split_item"
	inventorystart "inspection/services/inspection/internal/features/inventory/start_from_inspection"
	inventorytrail "inspection/services/inspection/internal/features/inventory/trail"
	inventoryupsertenv "inspection/services/inspection/internal/features/inventory/upsert_environment"
	originlist "inspection/services/inspection/internal/features/origins/list_versions"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	graphql1 "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
)

func inventoryMutationMeta(ctx context.Context) (requestctx.Metadata, error) {
	meta, ok := requestctx.FromContext(ctx)
	if !ok {
		return requestctx.Metadata{}, unauthenticated()
	}
	return meta, nil
}

func (r *mutationResolver) decideInventoryItems(ctx context.Context, input graphql1.DecideInventoryItemsInput) (*graphql1.InventoryItemsPayload, error) {
	meta, err := inventoryMutationMeta(ctx)
	if err != nil {
		return nil, err
	}
	out := &graphql1.InventoryItemsPayload{Items: []*graphql1.InventoryItem{}, EmptyEnvironments: []string{}, UserErrors: []*graphql1.UserError{}, ClientMutationID: input.ClientMutationID}
	version, err := inventoryID(input.OriginVersionID, "originVersionId")
	if err != nil {
		out.UserErrors = append(out.UserErrors, userErrorFrom(err))
		return out, nil
	}
	asset, err := r.inventoryVersionAsset(ctx, meta.TenantID, version)
	if err != nil {
		out.UserErrors = append(out.UserErrors, userErrorFrom(err))
		return out, nil
	}
	if err = r.inventoryAuthorize(ctx, asset, "mutate"); err != nil {
		if businessInventoryError(err) {
			out.UserErrors = append(out.UserErrors, userErrorFrom(err))
			return out, nil
		}
		return nil, err
	}
	decisions := make([]inventorycore.Decision, 0, len(input.Decisions))
	for _, d := range input.Decisions {
		if d == nil {
			out.UserErrors = append(out.UserErrors, userErrorFrom(graphql1Error("decisions")))
			return out, nil
		}
		id, e := inventoryID(d.ItemID, "itemId")
		if e != nil {
			out.UserErrors = append(out.UserErrors, userErrorFrom(e))
			return out, nil
		}
		decision := inventorycore.Decision{ItemID: id, Action: d.Action, ExpectedVersion: d.ExpectedVersion}
		if d.Reason != nil {
			decision.Reason = *d.Reason
		}
		if d.ReasonText != nil {
			decision.ReasonText = *d.ReasonText
		}
		if d.Patch != nil {
			patch := &inventorycore.ItemPatch{TypeCode: d.Patch.TypeCode, Name: d.Patch.Name, Material: d.Patch.Material, Color: d.Patch.Color, Quantity: d.Patch.Quantity, Condition: d.Patch.Condition, Observation: d.Patch.Observation, Limitation: d.Patch.Limitation}
			if d.Patch.EnvironmentID != nil {
				id, e := inventoryID(*d.Patch.EnvironmentID, "environmentId")
				if e != nil {
					out.UserErrors = append(out.UserErrors, userErrorFrom(e))
					return out, nil
				}
				patch.EnvironmentID = &id
			}
			decision.Patch = patch
		}
		decisions = append(decisions, decision)
	}
	raw, err := r.Bus.Send(ctx, inventorydecide.Command{Input: inventorycore.DecideInput{TenantID: meta.TenantID, OriginVersionID: version, ActorMembershipID: meta.Principal.MembershipID, Decisions: decisions}})
	if err != nil {
		errors, top := mutationInventoryError(err, input.ClientMutationID)
		if top != nil {
			return nil, top
		}
		out.UserErrors = errors
		return out, nil
	}
	result := raw.(inventorycore.DecideResult)
	for _, row := range result.Items {
		item, e := r.inventorySingleItem(ctx, meta.TenantID, row)
		if e != nil {
			return nil, e
		}
		out.Items = append(out.Items, item)
	}
	for _, id := range result.EmptyEnvironments {
		out.EmptyEnvironments = append(out.EmptyEnvironments, id.String())
	}
	return out, nil
}

func (r *mutationResolver) bulkAcceptInventoryEnvironment(ctx context.Context, input graphql1.BulkAcceptInventoryEnvironmentInput) (*graphql1.BulkAcceptInventoryEnvironmentPayload, error) {
	meta, err := inventoryMutationMeta(ctx)
	if err != nil {
		return nil, err
	}
	out := &graphql1.BulkAcceptInventoryEnvironmentPayload{UserErrors: []*graphql1.UserError{}, ClientMutationID: input.ClientMutationID}
	v, e := inventoryID(input.OriginVersionID, "originVersionId")
	if e != nil {
		out.UserErrors = append(out.UserErrors, userErrorFrom(e))
		return out, nil
	}
	env, e := inventoryID(input.EnvironmentID, "environmentId")
	if e != nil {
		out.UserErrors = append(out.UserErrors, userErrorFrom(e))
		return out, nil
	}
	asset, e := r.inventoryVersionAsset(ctx, meta.TenantID, v)
	if e != nil {
		out.UserErrors = append(out.UserErrors, userErrorFrom(e))
		return out, nil
	}
	if e = r.inventoryAuthorize(ctx, asset, "mutate"); e != nil {
		if businessInventoryError(e) {
			out.UserErrors = append(out.UserErrors, userErrorFrom(e))
			return out, nil
		}
		return nil, e
	}
	threshold := 8500
	if input.MinimumConfidenceBps != nil {
		threshold = *input.MinimumConfidenceBps
	}
	countRaw, e := r.Bus.Send(ctx, inventorybulk.Command{TenantID: meta.TenantID, VersionID: v, EnvironmentID: env, ActorID: meta.Principal.MembershipID, Threshold: threshold})
	if e != nil {
		out.UserErrors, err = mutationInventoryError(e, input.ClientMutationID)
		if err != nil {
			return nil, err
		}
		return out, nil
	}
	out.AcceptedCount = countRaw.(int)
	return out, nil
}

func (r *mutationResolver) addInventoryItem(ctx context.Context, input graphql1.AddInventoryItemInput) (*graphql1.InventoryItemPayload, error) {
	meta, err := inventoryMutationMeta(ctx)
	if err != nil {
		return nil, err
	}
	out := &graphql1.InventoryItemPayload{UserErrors: []*graphql1.UserError{}, ClientMutationID: input.ClientMutationID}
	v, e := inventoryID(input.OriginVersionID, "originVersionId")
	if e != nil {
		out.UserErrors = append(out.UserErrors, userErrorFrom(e))
		return out, nil
	}
	env, e := inventoryID(input.EnvironmentID, "environmentId")
	if e != nil {
		out.UserErrors = append(out.UserErrors, userErrorFrom(e))
		return out, nil
	}
	asset, e := r.inventoryVersionAsset(ctx, meta.TenantID, v)
	if e != nil {
		out.UserErrors = append(out.UserErrors, userErrorFrom(e))
		return out, nil
	}
	if e = r.inventoryAuthorize(ctx, asset, "mutate"); e != nil {
		if businessInventoryError(e) {
			out.UserErrors = append(out.UserErrors, userErrorFrom(e))
			return out, nil
		}
		return nil, e
	}
	condition, observation := "NOT_ASSESSABLE", "OBSERVED"
	if input.Condition != nil {
		condition = *input.Condition
	}
	if input.Observation != nil {
		observation = *input.Observation
	}
	evidence := make([]inventorycore.EvidenceInput, 0, len(input.Evidence))
	for _, entry := range input.Evidence {
		if entry == nil {
			out.UserErrors = append(out.UserErrors, userErrorFrom(graphql1Error("evidence")))
			return out, nil
		}
		media, e := inventoryID(entry.MediaID, "evidence")
		if e != nil {
			out.UserErrors = append(out.UserErrors, userErrorFrom(e))
			return out, nil
		}
		var frame *identity.ID
		if entry.FrameID != nil {
			id, e := inventoryID(*entry.FrameID, "evidence")
			if e != nil {
				out.UserErrors = append(out.UserErrors, userErrorFrom(e))
				return out, nil
			}
			frame = &id
		}
		var region json.RawMessage
		if entry.Region != nil {
			region, _ = json.Marshal(entry.Region)
		}
		evidence = append(evidence, inventorycore.EvidenceInput{Kind: entry.Kind, MediaID: media, FrameID: frame, Region: region})
	}
	raw, e := r.Bus.Send(ctx, inventoryadditem.Command{Input: inventorycore.AddItemInput{TenantID: meta.TenantID, OriginVersionID: v, EnvironmentID: env, ActorMembershipID: meta.Principal.MembershipID, TypeCode: input.TypeCode, Name: input.Name, Material: input.Material, Color: input.Color, Quantity: input.Quantity, Condition: condition, Observation: observation, Limitation: input.Limitation, Evidence: evidence}})
	if e != nil {
		out.UserErrors, err = mutationInventoryError(e, input.ClientMutationID)
		if err != nil {
			return nil, err
		}
		return out, nil
	}
	row := raw.(database.InventoryItem)
	out.Item, e = r.inventorySingleItem(ctx, meta.TenantID, row)
	return out, e
}

func (r *mutationResolver) mergeInventoryItems(ctx context.Context, input graphql1.MergeInventoryItemsInput) (*graphql1.InventoryItemPayload, error) {
	meta, err := inventoryMutationMeta(ctx)
	if err != nil {
		return nil, err
	}
	out := &graphql1.InventoryItemPayload{UserErrors: []*graphql1.UserError{}, ClientMutationID: input.ClientMutationID}
	v, e := inventoryID(input.OriginVersionID, "originVersionId")
	if e != nil {
		out.UserErrors = append(out.UserErrors, userErrorFrom(e))
		return out, nil
	}
	asset, e := r.inventoryVersionAsset(ctx, meta.TenantID, v)
	if e != nil {
		out.UserErrors = append(out.UserErrors, userErrorFrom(e))
		return out, nil
	}
	if e = r.inventoryAuthorize(ctx, asset, "mutate"); e != nil {
		if businessInventoryError(e) {
			out.UserErrors = append(out.UserErrors, userErrorFrom(e))
			return out, nil
		}
		return nil, e
	}
	ids := make([]identity.ID, 0, len(input.ItemIds))
	for _, raw := range input.ItemIds {
		id, e := inventoryID(raw, "itemIds")
		if e != nil {
			out.UserErrors = append(out.UserErrors, userErrorFrom(e))
			return out, nil
		}
		ids = append(ids, id)
	}
	var env *identity.ID
	if input.ResultEnvironmentID != nil {
		id, e := inventoryID(*input.ResultEnvironmentID, "resultEnvironmentId")
		if e != nil {
			out.UserErrors = append(out.UserErrors, userErrorFrom(e))
			return out, nil
		}
		env = &id
	}
	raw, e := r.Bus.Send(ctx, inventorymerge.Command{Input: inventorycore.MergeItemsInput{TenantID: meta.TenantID, OriginVersionID: v, ActorMembershipID: meta.Principal.MembershipID, ItemIDs: ids, ResultEnvironmentID: env}})
	if e != nil {
		out.UserErrors, err = mutationInventoryError(e, input.ClientMutationID)
		if err != nil {
			return nil, err
		}
		return out, nil
	}
	row := raw.(database.InventoryItem)
	out.Item, e = r.inventorySingleItem(ctx, meta.TenantID, row)
	return out, e
}

func (r *mutationResolver) splitInventoryItem(ctx context.Context, input graphql1.SplitInventoryItemInput) (*graphql1.InventoryItemsPayload, error) {
	meta, err := inventoryMutationMeta(ctx)
	if err != nil {
		return nil, err
	}
	out := &graphql1.InventoryItemsPayload{Items: []*graphql1.InventoryItem{}, EmptyEnvironments: []string{}, UserErrors: []*graphql1.UserError{}, ClientMutationID: input.ClientMutationID}
	v, e := inventoryID(input.OriginVersionID, "originVersionId")
	if e != nil {
		out.UserErrors = append(out.UserErrors, userErrorFrom(e))
		return out, nil
	}
	asset, e := r.inventoryVersionAsset(ctx, meta.TenantID, v)
	if e != nil {
		out.UserErrors = append(out.UserErrors, userErrorFrom(e))
		return out, nil
	}
	if e = r.inventoryAuthorize(ctx, asset, "mutate"); e != nil {
		if businessInventoryError(e) {
			out.UserErrors = append(out.UserErrors, userErrorFrom(e))
			return out, nil
		}
		return nil, e
	}
	itemID, e := inventoryID(input.ItemID, "itemId")
	if e != nil {
		out.UserErrors = append(out.UserErrors, userErrorFrom(e))
		return out, nil
	}
	parts := make([]inventorycore.SplitPart, 0, len(input.Parts))
	for _, part := range input.Parts {
		if part == nil {
			out.UserErrors = append(out.UserErrors, userErrorFrom(graphql1Error("parts")))
			return out, nil
		}
		p := inventorycore.SplitPart{TypeCode: part.TypeCode, Name: part.Name}
		if part.Condition != nil {
			p.Condition = *part.Condition
		}
		for _, raw := range part.EvidenceIds {
			id, e := inventoryID(raw, "evidenceIds")
			if e != nil {
				out.UserErrors = append(out.UserErrors, userErrorFrom(e))
				return out, nil
			}
			p.EvidenceIDs = append(p.EvidenceIDs, id)
		}
		parts = append(parts, p)
	}
	raw, e := r.Bus.Send(ctx, inventorysplit.Command{Input: inventorycore.SplitItemInput{TenantID: meta.TenantID, OriginVersionID: v, ItemID: itemID, ActorMembershipID: meta.Principal.MembershipID, ExpectedVersion: input.ExpectedVersion, Parts: parts}})
	if e != nil {
		out.UserErrors, err = mutationInventoryError(e, input.ClientMutationID)
		if err != nil {
			return nil, err
		}
		return out, nil
	}
	rows := raw.([]database.InventoryItem)
	for _, row := range rows {
		item, e := r.inventorySingleItem(ctx, meta.TenantID, row)
		if e != nil {
			return nil, e
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}

func (r *mutationResolver) upsertInventoryEnvironment(ctx context.Context, input graphql1.UpsertInventoryEnvironmentInput) (*graphql1.InventoryEnvironmentPayload, error) {
	meta, err := inventoryMutationMeta(ctx)
	if err != nil {
		return nil, err
	}
	out := &graphql1.InventoryEnvironmentPayload{UserErrors: []*graphql1.UserError{}, ClientMutationID: input.ClientMutationID}
	v, e := inventoryID(input.OriginVersionID, "originVersionId")
	if e != nil {
		out.UserErrors = append(out.UserErrors, userErrorFrom(e))
		return out, nil
	}
	asset, e := r.inventoryVersionAsset(ctx, meta.TenantID, v)
	if e != nil {
		out.UserErrors = append(out.UserErrors, userErrorFrom(e))
		return out, nil
	}
	if e = r.inventoryAuthorize(ctx, asset, "mutate"); e != nil {
		if businessInventoryError(e) {
			out.UserErrors = append(out.UserErrors, userErrorFrom(e))
			return out, nil
		}
		return nil, e
	}
	var env identity.ID
	if input.EnvironmentID != nil {
		env, e = inventoryID(*input.EnvironmentID, "environmentId")
		if e != nil {
			out.UserErrors = append(out.UserErrors, userErrorFrom(e))
			return out, nil
		}
	}
	raw, e := r.Bus.Send(ctx, inventoryupsertenv.Command{Input: inventorycore.UpsertEnvironmentInput{TenantID: meta.TenantID, OriginVersionID: v, EnvironmentID: env, ActorMembershipID: meta.Principal.MembershipID, TypeCode: input.TypeCode, Name: input.Name, Expected: input.Expected}})
	if e != nil {
		out.UserErrors, err = mutationInventoryError(e, input.ClientMutationID)
		if err != nil {
			return nil, err
		}
		return out, nil
	}
	row := raw.(database.InventoryEnvironment)
	out.Environment = inventoryEnvironment(inventorycore.EnvironmentView{Environment: row})
	return out, nil
}

func (r *mutationResolver) removeInventoryEnvironment(ctx context.Context, input graphql1.RemoveInventoryEnvironmentInput) (*graphql1.InventoryEnvironmentPayload, error) {
	meta, err := inventoryMutationMeta(ctx)
	if err != nil {
		return nil, err
	}
	out := &graphql1.InventoryEnvironmentPayload{UserErrors: []*graphql1.UserError{}, ClientMutationID: input.ClientMutationID}
	v, e := inventoryID(input.OriginVersionID, "originVersionId")
	if e != nil {
		out.UserErrors = append(out.UserErrors, userErrorFrom(e))
		return out, nil
	}
	env, e := inventoryID(input.EnvironmentID, "environmentId")
	if e != nil {
		out.UserErrors = append(out.UserErrors, userErrorFrom(e))
		return out, nil
	}
	asset, e := r.inventoryVersionAsset(ctx, meta.TenantID, v)
	if e != nil {
		out.UserErrors = append(out.UserErrors, userErrorFrom(e))
		return out, nil
	}
	if e = r.inventoryAuthorize(ctx, asset, "mutate"); e != nil {
		if businessInventoryError(e) {
			out.UserErrors = append(out.UserErrors, userErrorFrom(e))
			return out, nil
		}
		return nil, e
	}
	_, e = r.Bus.Send(ctx, inventoryremoveenv.Command{Input: inventorycore.RemoveEnvironmentInput{TenantID: meta.TenantID, OriginVersionID: v, EnvironmentID: env, ActorMembershipID: meta.Principal.MembershipID, ConfirmItemExclusion: input.ConfirmItemExclusion}})
	if e != nil {
		out.UserErrors, err = mutationInventoryError(e, input.ClientMutationID)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (r *mutationResolver) activateBaselineVersion(ctx context.Context, input graphql1.ActivateBaselineVersionInput) (*graphql1.OriginVersionPayload, error) {
	meta, err := inventoryMutationMeta(ctx)
	if err != nil {
		return nil, err
	}
	out := &graphql1.OriginVersionPayload{UserErrors: []*graphql1.UserError{}, ClientMutationID: input.ClientMutationID}
	v, e := inventoryID(input.OriginVersionID, "originVersionId")
	if e != nil {
		out.UserErrors = append(out.UserErrors, userErrorFrom(e))
		return out, nil
	}
	asset, e := r.inventoryVersionAsset(ctx, meta.TenantID, v)
	if e != nil {
		out.UserErrors = append(out.UserErrors, userErrorFrom(e))
		return out, nil
	}
	if e = r.inventoryAuthorize(ctx, asset, "mutate"); e != nil {
		if businessInventoryError(e) {
			out.UserErrors = append(out.UserErrors, userErrorFrom(e))
			return out, nil
		}
		return nil, e
	}
	participants := make([]identity.ID, 0, len(input.CounterpartyParticipantIds))
	for _, raw := range input.CounterpartyParticipantIds {
		id, e := inventoryID(raw, "counterpartyParticipantIds")
		if e != nil {
			out.UserErrors = append(out.UserErrors, userErrorFrom(e))
			return out, nil
		}
		participants = append(participants, id)
	}
	rowRaw, e := r.Bus.Send(ctx, inventoryactivate.Command{Input: inventorycore.ActivateInput{TenantID: meta.TenantID, OriginVersionID: v, ActorMembershipID: meta.Principal.MembershipID, Purpose: input.Purpose, ExpectedOriginVersion: int64(input.ExpectedOriginVersion), CounterpartyParticipantIDs: participants}})
	if e != nil {
		out.UserErrors, err = mutationInventoryError(e, input.ClientMutationID)
		if err != nil {
			return nil, err
		}
		return out, nil
	}
	row := rowRaw.(database.OriginVersion)
	revision, err := r.inventoryOriginRevision(ctx, meta.TenantID, row.OriginID)
	if err != nil {
		return nil, err
	}
	out.Version = inventoryVersion(row, revision)
	return out, nil
}

func (r *mutationResolver) discardBaselineDraft(ctx context.Context, input graphql1.DiscardBaselineDraftInput) (*graphql1.OriginVersionPayload, error) {
	meta, err := inventoryMutationMeta(ctx)
	if err != nil {
		return nil, err
	}
	out := &graphql1.OriginVersionPayload{UserErrors: []*graphql1.UserError{}, ClientMutationID: input.ClientMutationID}
	v, e := inventoryID(input.OriginVersionID, "originVersionId")
	if e != nil {
		out.UserErrors = append(out.UserErrors, userErrorFrom(e))
		return out, nil
	}
	asset, e := r.inventoryVersionAsset(ctx, meta.TenantID, v)
	if e != nil {
		out.UserErrors = append(out.UserErrors, userErrorFrom(e))
		return out, nil
	}
	if e = r.inventoryAuthorize(ctx, asset, "mutate"); e != nil {
		if businessInventoryError(e) {
			out.UserErrors = append(out.UserErrors, userErrorFrom(e))
			return out, nil
		}
		return nil, e
	}
	rowRaw, e := r.Bus.Send(ctx, inventorydiscard.Command{TenantID: meta.TenantID, VersionID: v, ActorID: meta.Principal.MembershipID})
	if e != nil {
		out.UserErrors, err = mutationInventoryError(e, input.ClientMutationID)
		if err != nil {
			return nil, err
		}
		return out, nil
	}
	row := rowRaw.(database.OriginVersion)
	revision, err := r.inventoryOriginRevision(ctx, meta.TenantID, row.OriginID)
	if err != nil {
		return nil, err
	}
	out.Version = inventoryVersion(row, revision)
	return out, nil
}

func (r *mutationResolver) retryInventoryDraft(ctx context.Context, input graphql1.RetryInventoryDraftInput) (*graphql1.OriginVersionPayload, error) {
	meta, err := inventoryMutationMeta(ctx)
	if err != nil {
		return nil, err
	}
	out := &graphql1.OriginVersionPayload{UserErrors: []*graphql1.UserError{}, ClientMutationID: input.ClientMutationID}
	versionID, err := inventoryID(input.OriginVersionID, "originVersionId")
	if err != nil {
		out.UserErrors = append(out.UserErrors, userErrorFrom(err))
		return out, nil
	}
	assetID, err := r.inventoryVersionAsset(ctx, meta.TenantID, versionID)
	if err != nil {
		out.UserErrors = append(out.UserErrors, userErrorFrom(err))
		return out, nil
	}
	if err := r.inventoryAuthorize(ctx, assetID, "mutate"); err != nil {
		if businessInventoryError(err) {
			out.UserErrors = append(out.UserErrors, userErrorFrom(err))
			return out, nil
		}
		return nil, err
	}
	rowRaw, err := r.Bus.Send(ctx, inventoryretry.Command{TenantID: meta.TenantID, VersionID: versionID, ActorID: meta.Principal.MembershipID})
	if err != nil {
		out.UserErrors, err = mutationInventoryError(err, input.ClientMutationID)
		if err != nil {
			return nil, err
		}
		return out, nil
	}
	row := rowRaw.(database.OriginVersion)
	revision, err := r.inventoryOriginRevision(ctx, meta.TenantID, row.OriginID)
	if err != nil {
		return nil, err
	}
	out.Version = inventoryVersion(row, revision)
	return out, nil
}

func (r *mutationResolver) requestOriginCataloging(ctx context.Context, input graphql1.RequestOriginCatalogingInput) (*graphql1.OriginVersionPayload, error) {
	meta, err := inventoryMutationMeta(ctx)
	if err != nil {
		return nil, err
	}
	out := &graphql1.OriginVersionPayload{UserErrors: []*graphql1.UserError{}, ClientMutationID: input.ClientMutationID}
	asset, e := inventoryID(input.AssetID, "assetId")
	if e != nil {
		out.UserErrors = append(out.UserErrors, userErrorFrom(e))
		return out, nil
	}
	if e = r.inventoryAuthorize(ctx, asset, "mutate"); e != nil {
		if businessInventoryError(e) {
			out.UserErrors = append(out.UserErrors, userErrorFrom(e))
			return out, nil
		}
		return nil, e
	}
	rowRaw, e := r.Bus.Send(ctx, inventoryrequest.Command{TenantID: meta.TenantID, AssetID: asset, ActorID: meta.Principal.MembershipID})
	if e != nil {
		out.UserErrors, err = mutationInventoryError(e, input.ClientMutationID)
		if err != nil {
			return nil, err
		}
		return out, nil
	}
	result := rowRaw.(inventoryrequest.Result)
	row := result.Version
	revision, err := r.inventoryOriginRevision(ctx, meta.TenantID, row.OriginID)
	if err != nil {
		return nil, err
	}
	out.Version = inventoryVersion(row, revision)
	out.SourcesExpired = &result.SourcesExpired
	return out, nil
}

func (r *mutationResolver) startBaselineDraftFromInspection(ctx context.Context, input graphql1.StartBaselineDraftFromInspectionInput) (*graphql1.OriginVersionPayload, error) {
	meta, err := inventoryMutationMeta(ctx)
	if err != nil {
		return nil, err
	}
	out := &graphql1.OriginVersionPayload{UserErrors: []*graphql1.UserError{}, ClientMutationID: input.ClientMutationID}
	inspectionID, e := inventoryID(input.InspectionID, "inspectionId")
	if e != nil {
		out.UserErrors = append(out.UserErrors, userErrorFrom(e))
		return out, nil
	}
	var inspection database.Inspection
	e = (tenanttx.Runner{DB: r.DB}).Within(ctx, meta.TenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND id=?", meta.TenantID, inspectionID).First(&inspection).Error
	})
	if e != nil {
		out.UserErrors = append(out.UserErrors, userErrorFrom(apperror.New(apperror.NotFound, "inspectionId", "inspection not found")))
		return out, nil
	}
	if e = r.inventoryAuthorize(ctx, inspection.AssetID, "mutate"); e != nil {
		if businessInventoryError(e) {
			out.UserErrors = append(out.UserErrors, userErrorFrom(e))
			return out, nil
		}
		return nil, e
	}
	rowRaw, e := r.Bus.Send(ctx, inventorystart.Command{TenantID: meta.TenantID, InspectionID: inspectionID, ActorID: meta.Principal.MembershipID})
	if e != nil {
		out.UserErrors, err = mutationInventoryError(e, input.ClientMutationID)
		if err != nil {
			return nil, err
		}
		return out, nil
	}
	row := rowRaw.(database.OriginVersion)
	viewRaw, e := r.Bus.Ask(ctx, inventorydraft.Query{TenantID: meta.TenantID, VersionID: row.ID})
	if e != nil {
		return nil, e
	}
	view := viewRaw.(inventorycore.DraftView)
	revision, e := r.inventoryOriginRevision(ctx, meta.TenantID, row.OriginID)
	if e != nil {
		return nil, e
	}
	out.Version = inventoryVersion(row, revision)
	out.Warnings = view.Warnings
	return out, nil
}

func (r *queryResolver) baselineVersions(ctx context.Context, assetID string, first *int, after *string) (*graphql1.OriginVersionConnection, error) {
	meta, ok := requestctx.FromContext(ctx)
	if !ok {
		return nil, unauthenticated()
	}
	asset, err := identity.ParseID(assetID)
	if err != nil {
		return nil, invalidID("assetId")
	}
	limit := 50
	if first != nil {
		limit = *first
	}
	cursor := ""
	if after != nil {
		cursor = *after
	}
	raw, err := r.Bus.Ask(ctx, originlist.Query{TenantID: meta.TenantID, AssetID: asset, First: limit, After: cursor})
	if err != nil {
		return nil, err
	}
	result := raw.(originlist.Result)
	var origin database.Origin
	originErr := (tenanttx.Runner{DB: r.DB}).Within(ctx, meta.TenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND asset_id=?", meta.TenantID, asset).First(&origin).Error
	})
	if originErr != nil && !errors.Is(originErr, gorm.ErrRecordNotFound) {
		return nil, originErr
	}
	countsByVersion, contestByVersion, err := r.inventoryVersionSummaries(ctx, meta.TenantID, result.Versions)
	if err != nil {
		return nil, err
	}
	nodes := make([]*graphql1.OriginVersion, 0, len(result.Versions))
	for _, row := range result.Versions {
		details := result.Details[row.ID]
		node := mapOriginVersion(row, &details)
		revision := 0
		if origin.ID != (identity.ID{}) {
			revision = int(origin.Version)
		}
		node.OriginRevision = revision
		applyInventoryVersionSummary(node, countsByVersion[row.ID], contestByVersion[row.ID])
		nodes = append(nodes, node)
	}
	return &graphql1.OriginVersionConnection{Nodes: nodes, PageInfo: pageInfo(result.EndCursor, result.HasNextPage)}, nil
}

func (r *queryResolver) inventoryDraft(ctx context.Context, originVersionID string) (*graphql1.InventoryDraft, error) {
	meta, ok := requestctx.FromContext(ctx)
	if !ok {
		return nil, unauthenticated()
	}
	if !canReadInventoryDraft(meta.Principal.Roles) {
		return nil, apperror.New(apperror.Forbidden, "", "access denied")
	}
	id, e := inventoryID(originVersionID, "originVersionId")
	if e != nil {
		return nil, e
	}
	asset, e := r.inventoryVersionAsset(ctx, meta.TenantID, id)
	if e != nil {
		return nil, e
	}
	if e = r.inventoryAuthorize(ctx, asset, "draft"); e != nil {
		return nil, e
	}
	viewRaw, e := r.Bus.Ask(ctx, inventorydraft.Query{TenantID: meta.TenantID, VersionID: id})
	if e != nil {
		return nil, e
	}
	view := viewRaw.(inventorycore.DraftView)
	var origin database.Origin
	if e := (tenanttx.Runner{DB: r.DB}).Within(ctx, meta.TenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id=? AND id=?", meta.TenantID, view.Version.OriginID).First(&origin).Error
	}); e != nil {
		return nil, e
	}
	out := &graphql1.InventoryDraft{AssetID: asset.String(), Version: inventoryVersion(view.Version, origin.Version), Environments: []*graphql1.InventoryEnvironment{}, Progress: &graphql1.InventoryProgress{Decided: view.Decided, Pending: view.Pending}, ReadOnly: view.ReadOnly, Warnings: view.Warnings}
	if view.NoticeCode != "" {
		out.NoticeCode = &view.NoticeCode
	}
	for _, env := range view.Environments {
		out.Environments = append(out.Environments, inventoryEnvironment(env))
	}
	if err := hydrateInventoryEvidence(ctx, r.DB, r.Store, meta.TenantID, id, out); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *queryResolver) baselineInventory(ctx context.Context, assetID string, versionID *string) (*graphql1.BaselineInventory, error) {
	meta, ok := requestctx.FromContext(ctx)
	if !ok {
		return nil, unauthenticated()
	}
	asset, e := inventoryID(assetID, "assetId")
	if e != nil {
		return nil, e
	}
	customer := hasRole(meta, auth.CustomerViewer)
	if e = r.inventoryAuthorize(ctx, asset, "baseline"); e != nil {
		return nil, e
	}
	var version *identity.ID
	if versionID != nil {
		id, e := inventoryID(*versionID, "versionId")
		if e != nil {
			return nil, e
		}
		version = &id
	}
	if customer && version != nil {
		var origin database.Origin
		e = (tenanttx.Runner{DB: r.DB}).Within(ctx, meta.TenantID, func(tx *gorm.DB) error {
			return tx.Where("tenant_id=? AND asset_id=?", meta.TenantID, asset).First(&origin).Error
		})
		if e != nil || origin.ActiveVersionID == nil || *origin.ActiveVersionID != *version {
			return nil, apperror.New(apperror.NotFound, "versionId", "inventory version not found")
		}
	}
	viewRaw, e := r.Bus.Ask(ctx, inventorybaseline.Query{TenantID: meta.TenantID, AssetID: asset, VersionID: version, Customer: customer})
	if e != nil {
		return nil, e
	}
	view := viewRaw.(inventorycore.BaselineView)
	out := &graphql1.BaselineInventory{HasInventory: view.HasInventory, Environments: []*graphql1.InventoryEnvironment{}}
	if view.Version != nil {
		var origin database.Origin
		if e := (tenanttx.Runner{DB: r.DB}).Within(ctx, meta.TenantID, func(tx *gorm.DB) error {
			return tx.Where("tenant_id=? AND asset_id=?", meta.TenantID, asset).First(&origin).Error
		}); e != nil {
			return nil, e
		}
		if customer {
			out.Version = inventoryCustomerVersion(*view.Version, origin.Version)
		} else {
			out.Version = inventoryVersion(*view.Version, origin.Version)
		}
	}
	for _, env := range view.Environments {
		if customer {
			out.Environments = append(out.Environments, inventoryCustomerEnvironment(env))
		} else {
			out.Environments = append(out.Environments, inventoryEnvironment(env))
		}
	}
	if view.Version != nil {
		if e := hydrateEnvironmentEvidence(ctx, r.DB, r.Store, meta.TenantID, view.Version.ID, out.Environments); e != nil {
			return nil, e
		}
	}
	return out, nil
}

func (r *queryResolver) inventoryTrail(ctx context.Context, originVersionID string) (*graphql1.InventoryTrail, error) {
	meta, ok := requestctx.FromContext(ctx)
	if !ok {
		return nil, unauthenticated()
	}
	id, e := inventoryID(originVersionID, "originVersionId")
	if e != nil {
		return nil, e
	}
	asset, e := r.inventoryVersionAsset(ctx, meta.TenantID, id)
	if e != nil {
		return nil, e
	}
	if e = r.inventoryAuthorize(ctx, asset, "trail"); e != nil {
		return nil, e
	}
	viewRaw, e := r.Bus.Ask(ctx, inventorytrail.Query{TenantID: meta.TenantID, VersionID: id})
	if e != nil {
		return nil, e
	}
	view := viewRaw.(inventorycore.TrailView)
	return inventoryTrail(view.Suggestions, view.SuggestionEvidence, view.Decisions, view.CounterpartyEvents), nil
}
