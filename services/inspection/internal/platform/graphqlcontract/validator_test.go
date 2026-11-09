package graphqlcontract

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testSchema = `type Query { ready: String! } type Mutation { save(input: SaveInput!): SavePayload! } input SaveInput { clientMutationId: String! } type SavePayload { clientMutationId: String! }`

func TestValidateCapabilitiesUT001ToUT004(t *testing.T) {
	capabilities := []Capability{
		{Root: "Query.ready", Owner: "Admin", Persona: "administrator", Journey: "readiness", Evidence: "UT-001"},
		{Root: "Mutation.save", Owner: "Admin", Persona: "administrator", Journey: "save", Evidence: "UT-004"},
	}
	if err := ValidateCapabilities([]byte(testSchema), capabilities); err != nil {
		t.Fatalf("UT-001: %v", err)
	}
	if err := ValidateCapabilities([]byte(testSchema), capabilities[:1]); err == nil {
		t.Fatal("UT-002: expected unmapped field diagnostic")
	}
	manifest, err := BuildManifest([]byte(testSchema), map[string][]byte{"admin": []byte("query Ready { ready }")})
	if err != nil {
		t.Fatal(err)
	}
	manifest.Capabilities = capabilities
	if err := ValidateManifest([]byte(testSchema), manifest, []string{"admin"}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateManifest([]byte("type Query { changed: String! }"), manifest, []string{"admin"}); err == nil {
		t.Fatal("UT-003: expected schema hash drift diagnostic")
	}
}

func TestValidateCapabilitiesRequiresMutationIdentity(t *testing.T) {
	bad := []byte(`type Query { ready: String! } type Mutation { save(input: SaveInput!): SavePayload! } input SaveInput { value: String! } type SavePayload { clientMutationId: String! }`)
	err := ValidateCapabilities(bad, []Capability{{Root: "Query.ready", Owner: "Admin", Persona: "administrator", Journey: "readiness", Evidence: "UT-001"}, {Root: "Mutation.save", Owner: "Admin", Persona: "administrator", Journey: "save", Evidence: "UT-004"}})
	if err == nil {
		t.Fatal("UT-004: expected client mutation identity diagnostic")
	}
}

func TestCapabilityMatrixUsesSpecificCustomerEvidenceAndPolicyPersona(t *testing.T) {
	matrix := []byte("# Matrix\n## Queries\n| # | Canonical query | Owner | Current state | Required product outcome |\n| 1 | `customerPortfolio` | Dashboard customer | Implemented | Scoped portfolio. |\n| 2 | `baselinePolicy` | Policy Admin | Implemented | Admin-only policy. |\n## Mutations\n| # | Canonical mutation | Owner | Current state | Required product outcome |\n| 3 | `save` | Admin | Implemented | Save with retries. Evidence: UT-001, IT-002 |\n")
	capabilities, err := ParseCapabilityMatrix(matrix)
	if err != nil {
		t.Fatal(err)
	}
	if capabilities[0].Evidence != "IT-054" {
		t.Fatalf("customerPortfolio evidence=%q, want IT-054", capabilities[0].Evidence)
	}
	if capabilities[1].Persona != "TENANT_ADMIN or INSPECTION_CONFIG_ADMIN" {
		t.Fatalf("baselinePolicy persona=%q", capabilities[1].Persona)
	}
	if capabilities[2].Evidence != "UT-001, IT-002" || capabilities[2].Journey != "Save with retries." {
		t.Fatalf("explicit mutation evidence=%q journey=%q", capabilities[2].Evidence, capabilities[2].Journey)
	}
}

func TestTask04ReportOperationPersonasArePublicAndStaffOnly(t *testing.T) {
	matrix := []byte("# Matrix\n## Queries\n| # | Canonical query | Owner | Current state | Required product outcome |\n| 1 | `verifyReport` | Anonymous public | Implemented | Public verification. Evidence: IT-016, IT-044 |\n## Mutations\n| # | Canonical mutation | Owner | Current state | Required product outcome |\n| 2 | `reissueReport` | Dashboard (manager or authorized employee) | Implemented | Staff correction. Evidence: IT-059, IT-060 |\n")
	capabilities, err := ParseCapabilityMatrix(matrix)
	if err != nil {
		t.Fatal(err)
	}
	if capabilities[0].Persona != "anonymous public visitor" {
		t.Fatalf("verifyReport persona=%q, want anonymous public visitor", capabilities[0].Persona)
	}
	if capabilities[1].Persona != "manager or authorized employee with report correction permission" {
		t.Fatalf("reissueReport persona=%q, internal viewer must be excluded", capabilities[1].Persona)
	}
}

func TestUS001EdgeContractsUT133(t *testing.T) {
	capabilities := []Capability{
		{Root: "Query.ready", Owner: "Admin", Persona: "administrator", Journey: "first-use, pagination, denial, prerequisite, archived-state, and scale behavior", Evidence: "IT-001"},
		{Root: "Mutation.save", Owner: "Admin", Persona: "administrator", Journey: "idempotent replay", Evidence: "IT-001"},
	}
	validDocument := []byte("query Ready { ready }")
	manifest, err := BuildManifest([]byte(testSchema), map[string][]byte{"admin": validDocument})
	if err != nil {
		t.Fatal(err)
	}
	manifest.Capabilities = capabilities

	tests := []struct {
		name  string
		check func(t *testing.T)
	}{
		{"UT-133.01 invalid schema field is named", func(t *testing.T) {
			broken := append([]Capability{}, capabilities...)
			broken[0].Root = "Query.unknown"
			err := ValidateCapabilities([]byte(testSchema), broken)
			if err == nil || !strings.Contains(err.Error(), "Query.unknown") {
				t.Fatalf("expected named unmapped field, got %v", err)
			}
		}},
		{"UT-133.02 empty data retains a first-use journey", func(t *testing.T) {
			if err := ValidateCapabilities([]byte(testSchema), capabilities); err != nil {
				t.Fatal(err)
			}
		}},
		{"UT-133.03 limits remain documented", func(t *testing.T) {
			if !strings.Contains(capabilities[0].Journey, "pagination") {
				t.Fatal("pagination behavior is undocumented")
			}
		}},
		{"UT-133.04 denial remains documented", func(t *testing.T) {
			if !strings.Contains(capabilities[0].Journey, "denial") {
				t.Fatal("denial behavior is undocumented")
			}
		}},
		{"UT-133.05 schema drift rejects concurrent clients", func(t *testing.T) {
			if err := ValidateManifest([]byte("type Query { changed: String! }"), manifest, []string{"admin"}); err == nil {
				t.Fatal("expected schema drift rejection")
			}
		}},
		{"UT-133.06 partial generation rejects missing product", func(t *testing.T) {
			if err := ValidateManifest([]byte(testSchema), manifest, []string{"admin", "capture"}); err == nil {
				t.Fatal("expected missing product rejection")
			}
		}},
		{"UT-133.07 retry requires mutation identity", func(t *testing.T) {
			if err := ValidateCapabilities([]byte(testSchema), capabilities); err != nil {
				t.Fatal(err)
			}
		}},
		{"UT-133.08 prerequisite state remains documented", func(t *testing.T) {
			if !strings.Contains(capabilities[0].Journey, "prerequisite") {
				t.Fatal("prerequisite behavior is undocumented")
			}
		}},
		{"UT-133.09 archived state remains documented", func(t *testing.T) {
			if !strings.Contains(capabilities[0].Journey, "archived-state") {
				t.Fatal("archived-state behavior is undocumented")
			}
		}},
		{"UT-133.10 scale behavior remains documented", func(t *testing.T) {
			if !strings.Contains(capabilities[0].Journey, "scale") {
				t.Fatal("scale behavior is undocumented")
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, test.check)
	}
}

func TestIT027DashboardOperationManifestDigestMatchesSource(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", "..", "..", "..", ".."))
	operations, err := os.ReadFile(filepath.Join(root, "apps", "dashboard", "src", "features", "dashboard", "operations.graphql"))
	if err != nil {
		t.Fatal(err)
	}
	manifestBytes, err := os.ReadFile(filepath.Join(root, "services", "inspection", "operation-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Products []struct {
			Product    string `json:"product"`
			Operations []struct {
				DocumentSHA256 string `json:"documentSha256"`
			} `json:"operations"`
		} `json:"products"`
	}
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatal(err)
	}
	var recorded []string
	for _, product := range manifest.Products {
		if product.Product == "dashboard" {
			for _, operation := range product.Operations {
				recorded = append(recorded, operation.DocumentSHA256)
			}
		}
	}
	actualBytes := sha256.Sum256(operations)
	actual := hex.EncodeToString(actualBytes[:])
	if len(recorded) == 0 {
		t.Fatal("operation manifest has no Dashboard operations")
	}
	for _, digest := range recorded {
		if digest != actual {
			t.Fatalf("manifest digest %s does not match operations.graphql sha256 %s", digest, actual)
		}
	}
}

func TestIT027AllFrontendManifestsAndGeneratedSchemasMatchCanonicalContract(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", "..", "..", "..", ".."))
	manifestBytes, err := os.ReadFile(filepath.Join(root, "services", "inspection", "operation-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Products []struct {
			Product    string `json:"product"`
			Operations []struct {
				DocumentSHA256 string `json:"documentSha256"`
			} `json:"operations"`
		} `json:"products"`
	}
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatal(err)
	}
	operationFiles := map[string]string{"admin": "apps/admin/src/features/admin/operations.graphql", "dashboard": "apps/dashboard/src/features/dashboard/operations.graphql", "capture": "apps/capture/src/graphql/documents/capture.graphql", "onboarding": "apps/onboarding/src/features/onboarding/operations.graphql"}
	canonicalSchema, err := os.ReadFile(filepath.Join(root, "services", "inspection", "schema.graphqls"))
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Products) != len(operationFiles) {
		t.Fatalf("manifest has %d products, expected %d", len(manifest.Products), len(operationFiles))
	}
	for _, product := range manifest.Products {
		operationPath, ok := operationFiles[product.Product]
		if !ok {
			t.Fatalf("unexpected manifest product %q", product.Product)
		}
		operations, err := os.ReadFile(filepath.Join(root, operationPath))
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(operations)
		actual := hex.EncodeToString(digest[:])
		if len(product.Operations) == 0 {
			t.Fatalf("%s has no manifest operations", product.Product)
		}
		for _, operation := range product.Operations {
			if operation.DocumentSHA256 != actual {
				t.Fatalf("%s manifest digest %s differs from operations digest %s", product.Product, operation.DocumentSHA256, actual)
			}
		}
		generatedSchema, err := os.ReadFile(filepath.Join(root, "apps", product.Product, "src", "graphql", "schema.ts"))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(canonicalSchema, []byte("reports(first: Int = 25, after: String, search: String, classification: String, inspectionId: ID)")) || !bytes.Contains(generatedSchema, []byte("export type QueryReportsArgs = {")) || !bytes.Contains(generatedSchema, []byte("inspectionId: InputMaybe<Scalars['ID']['input']>")) || !bytes.Contains(generatedSchema, []byte("export type ReportEvidence = {")) || !bytes.Contains(generatedSchema, []byte("reference: Maybe<Scalars['String']['output']>")) {
			t.Fatalf("%s generated schema is stale against canonical reports/ReportEvidence fields", product.Product)
		}
	}
}
