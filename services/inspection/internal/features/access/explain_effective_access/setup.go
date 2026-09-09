// Package explain_effective_access exposes tenant-safe explanations of the
// same role and scope decisions enforced by the authorization boundary.
package explain_effective_access

import (
	"context"
	"fmt"
	"sort"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"
	"inspection/services/inspection/internal/platform/tenanttx"

	"gorm.io/gorm"
)

const sliceName = "access/explain_effective_access"

// Scope is a protected resource whose effective access is being explained.
type Scope struct {
	Kind string
	ID   identity.ID
}

// Query explains one membership and, optionally, compares it to another
// membership in the same tenant.
type Query struct {
	TenantID, MembershipID, ComparisonMembershipID identity.ID
	Targets                                        []Scope
}

// Decision is an explainable authorization result. It intentionally contains
// no resource data, so it cannot disclose a resource outside the tenant.
type Decision struct {
	Target      Scope
	Allowed     bool
	Source      string
	Grant       *Scope
	Inheritance string
	Validity    string
}

// EffectiveAccess is the membership state and decisions used by the Admin
// effective-access review.
type EffectiveAccess struct {
	MembershipID identity.ID
	Role         string
	Status       string
	Version      int64
	Decisions    []Decision
}

// Difference identifies a target where the compared membership has a
// materially different decision.
type Difference struct {
	Target            Scope
	PrimaryAllowed    bool
	ComparisonAllowed bool
	PrimarySource     string
	ComparisonSource  string
}

// Result returns the requested explanation and an optional comparison.
type Result struct {
	Effective   EffectiveAccess
	Comparison  *EffectiveAccess
	Differences []Difference
}

// Dependencies are the concrete boundaries needed by this vertical slice.
type Dependencies struct {
	DB         *gorm.DB
	Bus        *mediator.Bus
	Authorizer auth.Authorizer
	Scopes     auth.ScopeResolver
}

// Setup registers the effective-access query with the application mediator.
func Setup(deps Dependencies) error {
	if deps.DB == nil || deps.Bus == nil || deps.Scopes == nil {
		return fmt.Errorf("slice %s: missing dependency", sliceName)
	}
	return deps.Bus.RegisterQuery(Query{}, func(ctx context.Context, raw any) (any, error) {
		return handle(ctx, deps, raw.(Query))
	})
}

func handle(ctx context.Context, deps Dependencies, query Query) (Result, error) {
	if query.TenantID == (identity.ID{}) || query.MembershipID == (identity.ID{}) {
		return Result{}, apperror.New(apperror.InvalidInput, "membershipId", "tenant and membership are required")
	}
	if len(query.Targets) == 0 {
		return Result{}, apperror.New(apperror.InvalidInput, "targets", "at least one target is required")
	}
	if _, err := deps.Authorizer.Authorize(ctx, auth.AuthorizationRequest{
		TenantID: query.TenantID,
		Product:  auth.AdminProduct,
		Roles:    []string{auth.TenantAdmin, auth.AccessAdmin, auth.Auditor},
	}); err != nil {
		return Result{}, err
	}
	if err := validateTargets(query.Targets); err != nil {
		return Result{}, err
	}

	primary, err := loadMembership(ctx, deps.DB, query.TenantID, query.MembershipID)
	if err != nil {
		return Result{}, err
	}
	result := Result{Effective: explain(ctx, deps.Scopes, query.TenantID, primary, query.Targets)}
	if query.ComparisonMembershipID == (identity.ID{}) {
		return result, nil
	}
	if query.ComparisonMembershipID == query.MembershipID {
		comparison := result.Effective
		result.Comparison = &comparison
		return result, nil
	}
	comparisonMembership, err := loadMembership(ctx, deps.DB, query.TenantID, query.ComparisonMembershipID)
	if err != nil {
		return Result{}, err
	}
	comparison := explain(ctx, deps.Scopes, query.TenantID, comparisonMembership, query.Targets)
	result.Comparison = &comparison
	for index, decision := range result.Effective.Decisions {
		other := comparison.Decisions[index]
		if decision.Allowed != other.Allowed || decision.Source != other.Source {
			result.Differences = append(result.Differences, Difference{Target: decision.Target, PrimaryAllowed: decision.Allowed, ComparisonAllowed: other.Allowed, PrimarySource: decision.Source, ComparisonSource: other.Source})
		}
	}
	return result, nil
}

func validateTargets(targets []Scope) error {
	seen := make(map[Scope]struct{}, len(targets))
	for _, target := range targets {
		if target.ID == (identity.ID{}) || !knownScope(target.Kind) {
			return apperror.New(apperror.InvalidInput, "targets", "unknown resource scope")
		}
		if _, exists := seen[target]; exists {
			return apperror.New(apperror.InvalidInput, "targets", "duplicate resource scope")
		}
		seen[target] = struct{}{}
	}
	return nil
}

func loadMembership(ctx context.Context, db *gorm.DB, tenantID, membershipID identity.ID) (database.Membership, error) {
	var membership database.Membership
	err := (tenanttx.Runner{DB: db}).Within(ctx, tenantID, func(tx *gorm.DB) error {
		return tx.Where("tenant_id = ? AND id = ?", tenantID, membershipID).First(&membership).Error
	})
	if err == gorm.ErrRecordNotFound {
		return database.Membership{}, apperror.New(apperror.NotFound, "membershipId", "membership not found")
	}
	if err != nil {
		return database.Membership{}, err
	}
	return membership, nil
}

func explain(ctx context.Context, scopes auth.ScopeResolver, tenantID identity.ID, membership database.Membership, targets []Scope) EffectiveAccess {
	ordered := append([]Scope(nil), targets...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Kind == ordered[j].Kind {
			return ordered[i].ID.String() < ordered[j].ID.String()
		}
		return ordered[i].Kind < ordered[j].Kind
	})
	result := EffectiveAccess{MembershipID: membership.ID, Role: membership.Role, Status: membership.Status, Version: membership.Version, Decisions: make([]Decision, 0, len(ordered))}
	for _, target := range ordered {
		decision := Decision{Target: target, Source: "DENIED", Inheritance: "NONE", Validity: membership.Status}
		if membership.Status != "ACTIVE" {
			result.Decisions = append(result.Decisions, decision)
			continue
		}
		if membership.Role == auth.TenantAdmin {
			decision.Allowed, decision.Source, decision.Inheritance = true, "ROLE", "TENANT"
			result.Decisions = append(result.Decisions, decision)
			continue
		}
		scopeDecision, err := scopes.Resolve(ctx, tenantID, membership.ID, requestctx.Scope{Kind: target.Kind, ID: target.ID})
		if err == nil && scopeDecision.Allowed {
			grant := Scope{Kind: scopeDecision.DirectGrant.Kind, ID: scopeDecision.DirectGrant.ID}
			decision.Allowed, decision.Source, decision.Grant = true, "SCOPE", &grant
			if grant == target {
				decision.Inheritance = "DIRECT"
			} else {
				decision.Inheritance = "INHERITED"
			}
		}
		result.Decisions = append(result.Decisions, decision)
	}
	return result
}

func knownScope(kind string) bool {
	switch kind {
	case "BUSINESS_UNIT", "ASSET", "PROJECT", "INSPECTION":
		return true
	default:
		return false
	}
}
