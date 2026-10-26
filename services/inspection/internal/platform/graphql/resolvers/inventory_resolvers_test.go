package resolvers

import (
	"context"
	"testing"
	"time"

	"inspection/libs/identity"
	getanalysisprompt "inspection/services/inspection/internal/features/analysis/get_prompt"
	updateanalysisprompt "inspection/services/inspection/internal/features/analysis/update_prompt"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	graphql1 "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"
)

func TestUnit_Issue006_CounterpartyTrailKeepsResponseAndResolutionEvents(t *testing.T) {
	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	responded, resolved := created.Add(time.Hour), created.Add(2*time.Hour)
	statement, reason := "visible statement", "accepted reason"
	row := database.CounterpartyInvitation{ID: identity.NewID(), Response: "CONTESTED", Resolution: "ACCEPTED", Statement: &statement, ResolutionReason: &reason, CreatedAt: created, RespondedAt: &responded, ResolvedAt: &resolved}
	trail := inventoryTrail(nil, nil, nil, []database.CounterpartyInvitation{row})
	if len(trail.CounterpartyEvents) != 3 {
		t.Fatalf("issue-006 counterparty event count=%d, want invitation, response, resolution", len(trail.CounterpartyEvents))
	}
	want := []struct {
		action string
		at     time.Time
	}{{"INVITATION_CREATED", created}, {"RESPONDED", responded}, {"RESOLVED", resolved}}
	for i, expected := range want {
		entry := trail.CounterpartyEvents[i]
		if entry.Action != expected.action || entry.OccurredAt != expected.at.Format(time.RFC3339Nano) {
			t.Fatalf("issue-006 event[%d]=%s at %s, want %s at %s", i, entry.Action, entry.OccurredAt, expected.action, expected.at.Format(time.RFC3339Nano))
		}
	}
	if trail.CounterpartyEvents[1].Statement == nil || *trail.CounterpartyEvents[1].Statement != statement || trail.CounterpartyEvents[2].ResolutionReason == nil || *trail.CounterpartyEvents[2].ResolutionReason != reason {
		t.Fatal("issue-006 response statement or resolution reason missing from its distinct audit event")
	}
}

func TestResolver_InventoryMutationErrors(t *testing.T) {
	t.Run("UT-320 conflict field mapping", func(t *testing.T) {
		userErrors, err := mutationInventoryError(apperror.New(apperror.Conflict, "expectedVersion", "item changed"), "review-1")
		if err != nil || len(userErrors) != 1 || userErrors[0].Field == nil || *userErrors[0].Field != "expectedVersion" || userErrors[0].Code != string(apperror.Conflict) {
			t.Fatalf("unexpected user error mapping: %#v, %v", userErrors, err)
		}
	})
	t.Run("UT-321 malformed activation id", func(t *testing.T) {
		ctx := inventoryResolverContext()
		resolver := mutationResolver{&Resolver{}}
		payload, err := resolver.ActivateBaselineVersion(ctx, graphql1.ActivateBaselineVersionInput{OriginVersionID: "bad-id", ClientMutationID: "activate-1"})
		if err != nil || payload == nil || len(payload.UserErrors) != 1 || payload.UserErrors[0].Field == nil || *payload.UserErrors[0].Field != "originVersionId" || payload.ClientMutationID != "activate-1" {
			t.Fatalf("unexpected activation validation response: %#v, %v", payload, err)
		}
	})
	t.Run("UT-322 mutation echoes clientMutationId on validation error", func(t *testing.T) {
		ctx := inventoryResolverContext()
		resolver := mutationResolver{&Resolver{}}
		cases := []struct {
			name string
			call func() (string, int, error)
		}{
			{"decideInventoryItems", func() (string, int, error) {
				p, e := resolver.DecideInventoryItems(ctx, graphql1.DecideInventoryItemsInput{OriginVersionID: "bad-id", ClientMutationID: "echo"})
				if p == nil {
					return "", 0, e
				}
				return p.ClientMutationID, len(p.UserErrors), e
			}},
			{"bulkAcceptInventoryEnvironment", func() (string, int, error) {
				p, e := resolver.BulkAcceptInventoryEnvironment(ctx, graphql1.BulkAcceptInventoryEnvironmentInput{OriginVersionID: "bad-id", EnvironmentID: "bad-id", ClientMutationID: "echo"})
				if p == nil {
					return "", 0, e
				}
				return p.ClientMutationID, len(p.UserErrors), e
			}},
			{"addInventoryItem", func() (string, int, error) {
				p, e := resolver.AddInventoryItem(ctx, graphql1.AddInventoryItemInput{OriginVersionID: "bad-id", ClientMutationID: "echo"})
				if p == nil {
					return "", 0, e
				}
				return p.ClientMutationID, len(p.UserErrors), e
			}},
			{"mergeInventoryItems", func() (string, int, error) {
				p, e := resolver.MergeInventoryItems(ctx, graphql1.MergeInventoryItemsInput{OriginVersionID: "bad-id", ClientMutationID: "echo"})
				if p == nil {
					return "", 0, e
				}
				return p.ClientMutationID, len(p.UserErrors), e
			}},
			{"splitInventoryItem", func() (string, int, error) {
				p, e := resolver.SplitInventoryItem(ctx, graphql1.SplitInventoryItemInput{OriginVersionID: "bad-id", ClientMutationID: "echo"})
				if p == nil {
					return "", 0, e
				}
				return p.ClientMutationID, len(p.UserErrors), e
			}},
			{"upsertInventoryEnvironment", func() (string, int, error) {
				p, e := resolver.UpsertInventoryEnvironment(ctx, graphql1.UpsertInventoryEnvironmentInput{OriginVersionID: "bad-id", ClientMutationID: "echo"})
				if p == nil {
					return "", 0, e
				}
				return p.ClientMutationID, len(p.UserErrors), e
			}},
			{"removeInventoryEnvironment", func() (string, int, error) {
				p, e := resolver.RemoveInventoryEnvironment(ctx, graphql1.RemoveInventoryEnvironmentInput{OriginVersionID: "bad-id", ClientMutationID: "echo"})
				if p == nil {
					return "", 0, e
				}
				return p.ClientMutationID, len(p.UserErrors), e
			}},
			{"activateBaselineVersion", func() (string, int, error) {
				p, e := resolver.ActivateBaselineVersion(ctx, graphql1.ActivateBaselineVersionInput{OriginVersionID: "bad-id", ClientMutationID: "echo"})
				if p == nil {
					return "", 0, e
				}
				return p.ClientMutationID, len(p.UserErrors), e
			}},
			{"discardBaselineDraft", func() (string, int, error) {
				p, e := resolver.DiscardBaselineDraft(ctx, graphql1.DiscardBaselineDraftInput{OriginVersionID: "bad-id", ClientMutationID: "echo"})
				if p == nil {
					return "", 0, e
				}
				return p.ClientMutationID, len(p.UserErrors), e
			}},
			{"requestOriginCataloging", func() (string, int, error) {
				p, e := resolver.RequestOriginCataloging(ctx, graphql1.RequestOriginCatalogingInput{AssetID: "bad-id", ClientMutationID: "echo"})
				if p == nil {
					return "", 0, e
				}
				return p.ClientMutationID, len(p.UserErrors), e
			}},
			{"startBaselineDraftFromInspection", func() (string, int, error) {
				p, e := resolver.StartBaselineDraftFromInspection(ctx, graphql1.StartBaselineDraftFromInspectionInput{InspectionID: "bad-id", ClientMutationID: "echo"})
				if p == nil {
					return "", 0, e
				}
				return p.ClientMutationID, len(p.UserErrors), e
			}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				clientID, errorCount, err := tc.call()
				if err != nil || clientID != "echo" || errorCount != 1 {
					t.Fatalf("mutation did not echo client mutation id: id=%q errors=%d err=%v", clientID, errorCount, err)
				}
			})
		}
	})
}

func TestResolver_AnalysisPromptTaskRouting(t *testing.T) {
	task := graphql1.AnalysisPromptTaskInventoryCompare
	ctx := inventoryResolverContext()
	bus := mediator.New()
	var gotCommandTask, gotQueryTask string
	if err := bus.RegisterCommand(updateanalysisprompt.Command{}, func(_ context.Context, raw any) (any, error) {
		gotCommandTask = raw.(updateanalysisprompt.Command).Task
		return database.AnalysisPrompt{AnalysisType: "REAL_ESTATE", Task: gotCommandTask}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := bus.RegisterQuery(getanalysisprompt.Query{}, func(_ context.Context, raw any) (any, error) {
		gotQueryTask = raw.(getanalysisprompt.Query).Task
		return database.AnalysisPrompt{AnalysisType: "REAL_ESTATE", Task: gotQueryTask}, nil
	}); err != nil {
		t.Fatal(err)
	}
	resolver := mutationResolver{&Resolver{Bus: bus}}
	if _, err := resolver.UpdateAnalysisPrompt(ctx, graphql1.UpdateAnalysisPromptInput{AnalysisType: graphql1.AnalysisTypeRealEstate, Task: &task}); err != nil {
		t.Fatal(err)
	}
	if gotCommandTask != string(task) {
		t.Fatalf("update command task = %q, want %q", gotCommandTask, task)
	}
	queryResolver := queryResolver{&Resolver{Bus: bus}}
	if _, err := queryResolver.AnalysisPrompt(ctx, graphql1.AnalysisTypeRealEstate, &task); err != nil {
		t.Fatal(err)
	}
	if gotQueryTask != string(task) {
		t.Fatalf("query task = %q, want %q", gotQueryTask, task)
	}
}

func TestResolver_UT082InventoryDraftForbiddenRoles(t *testing.T) {
	for _, role := range []string{auth.CustomerViewer, auth.Viewer} {
		t.Run(role, func(t *testing.T) {
			ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: identity.NewID(), Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: identity.NewID(), Roles: []string{role}}})
			meta, _ := requestctx.FromContext(ctx)
			meta.Principal.TenantID = meta.TenantID
			ctx = requestctx.WithMetadata(context.Background(), meta)
			_, err := (&queryResolver{&Resolver{}}).InventoryDraft(ctx, identity.NewID().String())
			code, _, _ := apperror.Public(err)
			if code != apperror.Forbidden {
				t.Fatalf("InventoryDraft for %s returned %s, want Forbidden (%v)", role, code, err)
			}
		})
	}
}

func TestResolver_UT085BaselineVersionFieldsAndContestationSummary(t *testing.T) {
	activatedAt := time.Date(2026, 10, 24, 10, 30, 0, 0, time.UTC)
	activatedBy := identity.NewID()
	purpose := "MOVE_IN"
	row := database.OriginVersion{ID: identity.NewID(), OriginID: identity.NewID(), VersionNumber: 4, Status: "ACTIVE", Purpose: &purpose, ActivatedAt: &activatedAt, ActivatedByMembershipID: &activatedBy}
	version := inventoryVersion(row, 5)
	applyInventoryVersionSummary(version, &graphql1.InventoryCounts{Included: 3, Rejected: 1, NotReviewed: 2}, &graphql1.ContestationSummary{Pending: 1, Acknowledged: 2, Contested: 3, NoResponse: 4})
	if version.VersionNumber != 4 || version.OriginRevision != 5 || version.Purpose == nil || *version.Purpose != purpose || version.Status != "ACTIVE" || version.ActivatedAt == nil || *version.ActivatedAt != activatedAt.Format(time.RFC3339Nano) || version.ActivatedBy == nil || *version.ActivatedBy != activatedBy.String() {
		t.Fatalf("baseline version fields are incomplete: %+v", version)
	}
	if version.Counts.Included != 3 || version.Counts.Rejected != 1 || version.Counts.NotReviewed != 2 || version.ContestationSummary.Pending != 1 || version.ContestationSummary.Acknowledged != 2 || version.ContestationSummary.Contested != 3 || version.ContestationSummary.NoResponse != 4 {
		t.Fatalf("baseline counts or contestation summary are incomplete: %+v", version)
	}
}

func TestResolver_UT088RequestOriginCatalogingRequiresAssetScope(t *testing.T) {
	assetID := identity.NewID()
	if principalHasInventoryAssetScope(nil, assetID) || principalHasInventoryAssetScope([]requestctx.Scope{{Kind: "BUSINESS_UNIT", ID: assetID}}, assetID) {
		t.Fatal("employee without an explicit asset grant was treated as in scope")
	}
	if !principalHasInventoryAssetScope([]requestctx.Scope{{Kind: "ASSET", ID: assetID}}, assetID) {
		t.Fatal("explicit asset scope was not recognized")
	}
}

func inventoryResolverContext() context.Context {
	tenantID, identityID := identity.NewID(), identity.NewID()
	return requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenantID, Principal: requestctx.Principal{TenantID: tenantID, IdentityID: identityID, MembershipID: identity.NewID()}, CorrelationID: "inventory-test"})
}

func TestResolver_InventoryItemPatchDistinguishesUnknownFromUnchanged(t *testing.T) {
	empty, clear := "", true
	patch, err := inventoryItemPatch(graphql1.InventoryItemPatchInput{Material: &empty, Color: &empty, ClearQuantity: &clear})
	if err != nil {
		t.Fatal(err)
	}
	if patch.Material == nil || *patch.Material != "" || patch.Color == nil || *patch.Color != "" || !patch.ClearQuantity || patch.Quantity != nil {
		t.Fatalf("cleared attributes = %+v, want explicit unknown values", patch)
	}
	unchanged, err := inventoryItemPatch(graphql1.InventoryItemPatchInput{})
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Material != nil || unchanged.Color != nil || unchanged.ClearQuantity {
		t.Fatalf("omitted attributes = %+v, want unchanged", unchanged)
	}
	bad := "bad-id"
	if _, err := inventoryItemPatch(graphql1.InventoryItemPatchInput{EnvironmentID: &bad}); err == nil {
		t.Fatal("malformed environment id was accepted")
	}
}
