//go:build integration

package resolvers

import (
	"context"
	"strings"
	"testing"

	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	"inspection/services/inspection/internal/platform/requestctx"
)

// AIBFIX-M4: retention policy roles follow ADR-003 of
// graphql-frontend-capability-parity. The tenant administrator and the
// governance administrator read and change the policy in the Admin product,
// the auditor only reads it, and every other role or product is refused.
func TestAIBFIX_M4RetentionPolicyRoleMatrix(t *testing.T) {
	fx := newAIPPGFixture(t)
	tenant := fx.a.TenantID
	save := func(days int) string {
		return `mutation { configureRetentionPolicy(input:{evidenceDays:` + itoa(days) + `,operationalDays:200,securityDays:7,clientMutationId:"aibfix-m4"}) { policy { version evidenceDays } } }`
	}
	read := `query { retentionPolicies { nodes { version } } }`
	dashboardAdmin := dashboardActor(tenant, auth.TenantAdmin)
	for name, tc := range map[string]struct {
		actor          context.Context
		mayRead, write bool
	}{
		"tenant administrator":              {adminProductActor(tenant, auth.TenantAdmin), true, true},
		"governance administrator":          {adminProductActor(tenant, auth.GovernanceAdmin), true, true},
		"auditor":                           {adminProductActor(tenant, auth.Auditor), true, false},
		"access administrator":              {adminProductActor(tenant, auth.AccessAdmin), false, false},
		"organization administrator":        {adminProductActor(tenant, auth.OrganizationAdmin), false, false},
		"inspection config admin":           {adminProductActor(tenant, auth.InspectionConfigAdmin), false, false},
		"manager":                           {dashboardActor(tenant, auth.Manager), false, false},
		"tenant admin on Dashboard":         {dashboardAdmin, false, false},
		"viewer":                            {dashboardActor(tenant, auth.Viewer), false, false},
		"admin product without entitlement": {aipPGContext(tenant, []string{auth.TenantAdmin}, "inspection-admin", auth.AdminProduct, []string{auth.DashboardProduct}), false, false},
	} {
		t.Run(name, func(t *testing.T) {
			if tc.mayRead {
				if outcome, raw := fx.run(t, tc.actor, read); len(outcome.Errors) != 0 {
					t.Fatalf("read denied: %s", raw)
				}
			} else {
				fx.rejected(t, tc.actor, read, "FORBIDDEN", "")
			}
			before := len(fx.policyRows(t, tenant))
			if tc.write {
				if outcome, raw := fx.run(t, tc.actor, save(1000+before)); len(outcome.Errors) != 0 {
					t.Fatalf("write denied: %s", raw)
				}
				if got := len(fx.policyRows(t, tenant)); got != before+1 {
					t.Fatalf("policy versions = %d, want %d", got, before+1)
				}
			} else {
				fx.rejected(t, tc.actor, save(1000+before), "FORBIDDEN", "")
				if got := len(fx.policyRows(t, tenant)); got != before {
					t.Fatalf("a denied role wrote a policy: %d -> %d", before, got)
				}
			}
		})
	}
}

// AIBFIX-M4: every policy change is audited with its actor, version and values
// in the same transaction; saving the values of the latest version again is an
// idempotent no-op with neither a new version nor a second audit event.
func TestAIBFIX_M4PolicyChangeIsAuditedAndIdenticalSaveIsANoOp(t *testing.T) {
	fx := newAIPPGFixture(t)
	tenant := fx.a.TenantID
	actor := adminProductActor(tenant, auth.GovernanceAdmin)
	meta, _ := requestctx.FromContext(actor)
	mutation := func(days int) string {
		return `mutation { configureRetentionPolicy(input:{evidenceDays:` + itoa(days) + `,operationalDays:300,securityDays:15,clientMutationId:"aibfix-m4-audit"}) { policy { version } } }`
	}
	audits := func() []database.AuditEvent {
		var rows []database.AuditEvent
		if err := fx.h.AdminDB.Where("tenant_id=? AND action='retention.policy_configured'", tenant).Order("occurred_at ASC").Find(&rows).Error; err != nil {
			t.Fatal(err)
		}
		return rows
	}
	if outcome, raw := fx.run(t, actor, mutation(900)); len(outcome.Errors) != 0 {
		t.Fatal(raw)
	}
	rows := audits()
	if len(rows) != 1 || rows[0].ActorID != meta.Principal.IdentityID || rows[0].TargetType != "retention_policy" {
		t.Fatalf("audit after the first save = %+v, want one event of the actor", rows)
	}
	for _, want := range []string{"version=1", "evidenceDays=900", "operationalDays=300", "securityDays=15"} {
		if !strings.Contains(rows[0].Reason, want) {
			t.Fatalf("audit reason %q does not carry %q", rows[0].Reason, want)
		}
	}
	// The identical save changes nothing.
	if outcome, raw := fx.run(t, actor, mutation(900)); len(outcome.Errors) != 0 || !strings.Contains(raw, `"version":1`) {
		t.Fatalf("identical save did not return the existing version: %s", raw)
	}
	if len(fx.policyRows(t, tenant)) != 1 || len(audits()) != 1 {
		t.Fatalf("identical save created a version or an audit event: policies=%d audits=%d", len(fx.policyRows(t, tenant)), len(audits()))
	}
	// A changed value is version 2 with its own audit event.
	if outcome, raw := fx.run(t, actor, mutation(901)); len(outcome.Errors) != 0 || !strings.Contains(raw, `"version":2`) {
		t.Fatalf("changed save did not create version 2: %s", raw)
	}
	if rows = audits(); len(rows) != 2 || !strings.Contains(rows[1].Reason, "version=2") || !strings.Contains(rows[1].Reason, "evidenceDays=901") {
		t.Fatalf("audit after the second save = %+v", rows)
	}
	// An invalid save is rejected without a version or an audit event.
	fx.rejected(t, actor, mutation(0), "INVALID_INPUT", "evidenceDays")
	if len(fx.policyRows(t, tenant)) != 2 || len(audits()) != 2 {
		t.Fatal("a rejected save left a version or an audit event")
	}
}
