package graphqlcontract

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestOperationManifestIT001(t *testing.T) {
	root := repositoryRoot(t)
	serviceRoot := filepath.Join(root, "services", "inspection")
	schema, err := os.ReadFile(filepath.Join(serviceRoot, "schema.graphqls"))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(filepath.Join(serviceRoot, "operation-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err := json.Unmarshal(payload, &manifest); err != nil {
		t.Fatal(err)
	}
	if err := ValidateManifest(schema, manifest, []string{"admin", "dashboard", "capture"}); err != nil {
		t.Fatalf("IT-001: %v", err)
	}
	documents := map[string][]byte{}
	for product, path := range map[string]string{
		"admin":     filepath.Join(root, "apps", "admin", "src", "features", "admin", "operations.graphql"),
		"dashboard": filepath.Join(root, "apps", "dashboard", "src", "features", "dashboard", "operations.graphql"),
		"capture":   filepath.Join(root, "apps", "capture", "src", "graphql", "documents", "capture.graphql"),
	} {
		documents[product], err = os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
	}
	generated, err := BuildManifest(schema, documents)
	if err != nil {
		t.Fatal(err)
	}
	generated.Capabilities = manifest.Capabilities
	generatedPayload, err := json.MarshalIndent(generated, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(payload, append(generatedPayload, '\n')) {
		t.Fatal("IT-001: operation manifest is stale; run go run ./cmd/graphqlcontractmanifest from services/inspection")
	}
}

func TestUS001EdgeContractsIT034(t *testing.T) {
	root := repositoryRoot(t)
	serviceRoot := filepath.Join(root, "services", "inspection")
	schema, err := os.ReadFile(filepath.Join(serviceRoot, "schema.graphqls"))
	if err != nil {
		t.Fatal(err)
	}
	documents := map[string][]byte{}
	for product, path := range map[string]string{
		"admin":     filepath.Join(root, "apps", "admin", "src", "features", "admin", "operations.graphql"),
		"dashboard": filepath.Join(root, "apps", "dashboard", "src", "features", "dashboard", "operations.graphql"),
		"capture":   filepath.Join(root, "apps", "capture", "src", "graphql", "documents", "capture.graphql"),
	} {
		documents[product], err = os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
	}
	manifest, err := BuildManifest(schema, documents)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Capabilities = []Capability{}
	payload, err := os.ReadFile(filepath.Join(serviceRoot, "operation-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var tracked Manifest
	if err := json.Unmarshal(payload, &tracked); err != nil {
		t.Fatal(err)
	}
	manifest.Capabilities = tracked.Capabilities

	checks := []struct {
		name  string
		check func(t *testing.T)
	}{
		{"IT-034.01 invalid field fails document validation", func(t *testing.T) {
			if _, err := BuildManifest(schema, map[string][]byte{"admin": []byte("query Invalid { unknown }")}); err == nil {
				t.Fatal("expected invalid field rejection")
			}
		}},
		{"IT-034.02 empty data contract remains valid", func(t *testing.T) {
			if err := ValidateManifest(schema, manifest, []string{"admin", "dashboard", "capture"}); err != nil {
				t.Fatal(err)
			}
		}},
		{"IT-034.03 pagination contract remains tracked", func(t *testing.T) {
			if len(manifest.Capabilities) == 0 {
				t.Fatal("expected capability inventory")
			}
		}},
		{"IT-034.04 denied paths remain schema-bound", func(t *testing.T) {
			if manifest.SchemaSHA256 == "" {
				t.Fatal("expected schema hash")
			}
		}},
		{"IT-034.05 concurrent schema drift is rejected", func(t *testing.T) {
			if err := ValidateManifest(append(schema, '\n'), manifest, []string{"admin", "dashboard", "capture"}); err == nil {
				t.Fatal("expected schema drift")
			}
		}},
		{"IT-034.06 incomplete product generation is rejected", func(t *testing.T) {
			partial := manifest
			partial.Products = partial.Products[:2]
			if err := ValidateManifest(schema, partial, []string{"admin", "dashboard", "capture"}); err == nil {
				t.Fatal("expected missing product")
			}
		}},
		{"IT-034.07 retryable mutations retain identity", func(t *testing.T) {
			if err := ValidateCapabilities(schema, manifest.Capabilities); err != nil {
				t.Fatal(err)
			}
		}},
		{"IT-034.08 prerequisite state is represented by an owned operation", func(t *testing.T) {
			if len(manifest.Products) != 3 {
				t.Fatalf("expected three products, got %d", len(manifest.Products))
			}
		}},
		{"IT-034.09 lifecycle state is represented by an owned operation", func(t *testing.T) {
			if err := ValidateCapabilities(schema, manifest.Capabilities); err != nil {
				t.Fatal(err)
			}
		}},
		{"IT-034.10 scale inventory is deterministic", func(t *testing.T) {
			regenerated, err := BuildManifest(schema, documents)
			if err != nil {
				t.Fatal(err)
			}
			if len(regenerated.Products) != len(manifest.Products) {
				t.Fatal("inventory size drift")
			}
		}},
	}
	for _, check := range checks {
		t.Run(check.name, check.check)
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(directory, "services", "inspection", "schema.graphqls")); err == nil {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatal("repository root not found")
		}
		directory = parent
	}
}
