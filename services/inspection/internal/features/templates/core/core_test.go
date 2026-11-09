package core

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"inspection/libs/identity"
	"inspection/services/inspection/internal/features/templates/catalog"
	"inspection/services/inspection/internal/platform/apperror"
	"inspection/services/inspection/internal/platform/auth"
	"inspection/services/inspection/internal/platform/requestctx"
)

func TestDescriptiveCompileErrorsArePortuguese(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*catalog.TemplateDocument)
		want   string
	}{
		{"duplicate component in room", func(doc *catalog.TemplateDocument) {
			doc.ExpectedRooms[0].Components = append(doc.ExpectedRooms[0].Components, doc.ExpectedRooms[0].Components[0])
		}, "A chave do componente está duplicada."},
		{"invalid profile", func(doc *catalog.TemplateDocument) { doc.AssetProfile = "UNKNOWN" }, "O perfil do imóvel informado não é compatível."},
		{"component limit", func(doc *catalog.TemplateDocument) {
			for i := 0; i < catalog.MaxExpectedComponents+1; i++ {
				doc.ExpectedRooms[0].Components = append(doc.ExpectedRooms[0].Components, catalog.ExpectedComponent{Key: fmt.Sprintf("item-%d", i), TypeCode: "FLOOR", Label: "Piso"})
			}
		}, "O cômodo excede o limite de componentes esperados."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := catalog.TemplateDocument{SchemaVersion: catalog.DescriptiveSchemaVersion, CaptureLayout: catalog.EnvironmentsLayout, SegmentVersionID: "segment", AnalysisType: "REAL_ESTATE", ParticipantRoles: []string{"TENANT_PARTICIPANT"}, DefaultComparisonMode: catalog.ChecklistOnly, AssetProfile: "PROPERTY_COMPONENTS", ExpectedRooms: []catalog.ExpectedRoom{{Key: "kitchen", TypeCode: "KITCHEN", Name: "Cozinha", Components: []catalog.ExpectedComponent{{Key: "floor", TypeCode: "FLOOR", Label: "Piso"}}}}}
			tc.mutate(&doc)
			payload, err := json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			_, compileErr := catalog.Compile(payload, compileTestRefs{})
			if compileErr == nil {
				t.Fatal("invalid descriptive template compiled")
			}
			userErr := apperror.New(apperror.InvalidInput, "definition", descriptiveCompileError(compileErr))
			code, field, message := apperror.Public(userErr)
			if code != apperror.InvalidInput || field != "definition" || message != tc.want {
				t.Fatalf("compile error=%T %v; final userError=(%s,%q,%q), want (%s,%q,%q)", compileErr, compileErr, code, field, message, apperror.InvalidInput, "definition", tc.want)
			}
		})
	}
}

type compileTestRefs struct{}

func (compileTestRefs) SegmentExists(value string) bool      { return value == "segment" }
func (compileTestRefs) AnalysisTypeExists(value string) bool { return value == "REAL_ESTATE" }

func TestUT005TemplateMutationIsTenantScoped(t *testing.T) {
	callerTenant, targetTenant := identity.NewID(), identity.NewID()
	principal := requestctx.Principal{IdentityID: identity.NewID(), TenantID: callerTenant, Roles: []string{auth.TenantAdmin}, ProductEntitlements: auth.ProductsForRole(auth.TenantAdmin)}
	ctx := requestctx.WithMetadata(context.Background(), requestctx.Metadata{TenantID: callerTenant, Principal: principal})
	_, err := (Service{Authorizer: auth.Authorizer{}}).Publish(ctx, targetTenant, "other-tenant", "Other tenant", "ut005", []byte(`{}`))
	if !apperror.Is(err, apperror.NotFound) {
		t.Fatalf("UT-005 cross-tenant template mutation error=%v, want resource not found", err)
	}
}
