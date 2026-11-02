package resolvers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/database"
	graph "inspection/services/inspection/internal/platform/graphql"
	"inspection/services/inspection/internal/platform/mediator"
	"inspection/services/inspection/internal/platform/requestctx"

	"github.com/99designs/gqlgen/graphql/handler"
)

func TestTFE_IT_181DashboardReadAuthorizationMatrix(t *testing.T) {
	tenant := identity.NewID()
	cases := []struct {
		name  string
		role  string
		aud   string
		ent   []string
		allow bool
	}{
		{"manager dashboard", auth.Manager, "inspection-dashboard", []string{auth.DashboardProduct}, true},
		{"viewer dashboard", auth.Viewer, "inspection-dashboard", []string{auth.DashboardProduct}, true},
		{"customer viewer", auth.CustomerViewer, "inspection-dashboard", []string{auth.DashboardProduct}, false},
		{"wrong audience", auth.Manager, "inspection-admin", []string{auth.DashboardProduct}, false},
		{"revoked entitlement", auth.Manager, "inspection-dashboard", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			meta := requestctx.Metadata{TenantID: tenant, Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: tenant, Roles: []string{tc.role}, Audience: tc.aud, Product: auth.DashboardProduct, ProductEntitlements: tc.ent}}
			ctx := requestctx.WithMetadata(context.Background(), meta)
			err := authorizeDashboardRead(ctx, auth.Authorizer{}, meta)
			if (err == nil) != tc.allow {
				t.Fatalf("authorization err=%v allow=%v", err, tc.allow)
			}
			if err != nil {
				code, _, _ := apperror.Public(err)
				if code != apperror.Forbidden {
					t.Fatalf("denial code=%s, want FORBIDDEN", code)
				}
			}
		})
	}
}

func TestTFE_IT_430DashboardGraphQLScopesTenantAndTimeline(t *testing.T) {
	db := task06ResolverDB(t)
	tenantID, grantedAsset, otherAsset, projectID := identity.NewID(), identity.NewID(), identity.NewID(), identity.NewID()
	unitID := identity.NewID()
	now := time.Now().UTC().Truncate(time.Microsecond)
	type projection struct {
		inspection, asset identity.ID
		class             string
		seq               int64
	}
	items := []projection{{identity.NewID(), grantedAsset, "CRITICAL", 4}, {identity.NewID(), grantedAsset, "ATTENTION", 3}, {identity.NewID(), otherAsset, "CRITICAL", 2}}
	for _, item := range items {
		if err := db.Exec("INSERT INTO inspections.inspections (id,tenant_id,business_unit_id,asset_id,project_id) VALUES (?,?,?,?,?)", item.inspection, tenantID, unitID, item.asset, projectID).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&database.DashboardInspection{ID: identity.NewID(), TenantID: tenantID, InspectionID: item.inspection, ProjectID: &projectID, AssetID: item.asset, Classification: item.class, Status: "COMPLETED", Sequence: item.seq, UpdatedAt: now.Add(time.Duration(item.seq) * time.Minute)}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Exec("INSERT INTO projects.projects (id,tenant_id,business_unit_id,asset_id,name,status) VALUES (?,?,?,?,?,?)", projectID, tenantID, unitID, otherAsset, "Projeto QA", "ACTIVE").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO projects.project_stages (id,tenant_id,project_id,label,status,position) VALUES (?,?,?,?,?,?)", identity.NewID(), tenantID, projectID, "Etapa", "ACTIVE", 1).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO tenancy.tenants (id,tenant_id,name,status,language,default_timezone,version) VALUES (?,?,?,?,?,?,?)", tenantID, tenantID, "Cliente QA", "ACTIVE", "pt-BR", "America/Sao_Paulo", 7).Error; err != nil {
		t.Fatal(err)
	}
	server := handler.NewDefaultServer(graph.NewExecutableSchema(graph.Config{Resolvers: &Resolver{DB: db, Bus: mediator.New()}}))
	server.SetErrorPresenter(graph.PresentError)
	ctx := func(role string, scopes ...requestctx.Scope) context.Context {
		return requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: tenantID, Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: tenantID, Roles: []string{role}, Audience: "inspection-dashboard", Product: auth.DashboardProduct, ProductEntitlements: []string{auth.DashboardProduct}, Scopes: scopes}})
	}
	manager := ctx(auth.Manager, requestctx.Scope{Kind: "ASSET", ID: grantedAsset})
	query := `query { dashboardSummary { total critical attention normal } triageInspections(first:1) { nodes { inspectionId classification } pageInfo { hasNextPage endCursor } } }`
	response := executeTask06GraphQL(t, server, query, manager)
	if !strings.Contains(response, `"total":2`) || !strings.Contains(response, `"critical":1`) || !strings.Contains(response, `"attention":1`) || !strings.Contains(response, `"hasNextPage":true`) {
		t.Fatalf("manager dashboard did not filter the aggregate/page to the grant: %s", response)
	}
	var decoded struct {
		Data struct {
			Triage struct {
				Nodes []struct {
					InspectionID   string `json:"inspectionId"`
					Classification string `json:"classification"`
				}
				Page struct {
					EndCursor string `json:"endCursor"`
					HasNext   bool   `json:"hasNextPage"`
				} `json:"pageInfo"`
			} `json:"triageInspections"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(response), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Data.Triage.Nodes) != 1 || decoded.Data.Triage.Nodes[0].InspectionID != items[0].inspection.String() || !decoded.Data.Triage.Page.HasNext || decoded.Data.Triage.Page.EndCursor == "" {
		t.Fatalf("unexpected first scoped page: %+v", decoded.Data.Triage)
	}
	queryNext := fmt.Sprintf(`query { triageInspections(first:1,after:%q) { nodes { inspectionId } pageInfo { hasNextPage } } }`, decoded.Data.Triage.Page.EndCursor)
	response = executeTask06GraphQL(t, server, queryNext, manager)
	if !strings.Contains(response, items[1].inspection.String()) || strings.Contains(response, items[2].inspection.String()) || !strings.Contains(response, `"hasNextPage":false`) {
		t.Fatalf("cursor crossed the grant boundary: %s", response)
	}
	response = executeTask06GraphQL(t, server, query, ctx(auth.Manager))
	if !strings.Contains(response, `"total":0`) || !strings.Contains(response, `"nodes":[]`) {
		t.Fatalf("missing grants disclosed dashboard rows: %s", response)
	}
	response = executeTask06GraphQL(t, server, query, ctx(auth.TenantAdmin))
	if !strings.Contains(response, `"total":3`) || !strings.Contains(response, `"hasNextPage":true`) {
		t.Fatalf("tenant admin did not see full tenant: %s", response)
	}
	response = executeTask06GraphQL(t, server, `query { projectTimeline(projectId:"`+projectID.String()+`") { projectId } }`, ctx(auth.Manager, requestctx.Scope{Kind: "ASSET", ID: grantedAsset}))
	if !strings.Contains(response, "NOT_FOUND") || !strings.Contains(response, "Projeto não encontrado.") {
		t.Fatalf("out-of-scope timeline did not return safe NOT_FOUND: %s", response)
	}
	response = executeTask06GraphQL(t, server, `query { tenant { id name status language defaultTimezone version } }`, ctx(auth.CustomerViewer))
	if !strings.Contains(response, `"id":"`+tenantID.String()+`"`) || !strings.Contains(response, `"name":"Cliente QA"`) || !strings.Contains(response, `"status":"ACTIVE"`) || !strings.Contains(response, `"language":""`) || !strings.Contains(response, `"defaultTimezone":""`) || !strings.Contains(response, `"version":0`) {
		t.Fatalf("customer viewer received fields outside the safe tenant projection: %s", response)
	}
}

func TestTFE_IT_222TenantAcceptsDashboardViewerAndDelegatedAdmin(t *testing.T) {
	tenantID := identity.NewID()
	tests := []struct {
		name, audience, product, role string
		entitlements                  []string
		allow                         bool
	}{
		{"dashboard viewer", "inspection-dashboard", auth.DashboardProduct, auth.Viewer, []string{auth.DashboardProduct}, true},
		{"delegated admin", "inspection-admin", auth.AdminProduct, auth.AccessAdmin, []string{auth.AdminProduct}, true},
		{"customer gate tenant", "inspection-dashboard", auth.DashboardProduct, auth.CustomerViewer, []string{auth.DashboardProduct}, true},
		{"revoked admin denied", "inspection-admin", auth.AdminProduct, auth.AccessAdmin, nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			meta := requestctx.Metadata{TenantID: tenantID, Principal: requestctx.Principal{IdentityID: identity.NewID(), TenantID: tenantID, Audience: tc.audience, Product: tc.product, Roles: []string{tc.role}, ProductEntitlements: tc.entitlements}}
			err := authorizeTenantRead(requestctx.WithMetadata(context.Background(), meta), auth.Authorizer{}, meta)
			if (err == nil) != tc.allow {
				t.Fatalf("tenant authorization err=%v allow=%v", err, tc.allow)
			}
		})
	}
}
