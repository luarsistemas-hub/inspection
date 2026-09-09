// Command graphqlcontractmanifest writes the deterministic product operation inventory.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"inspection/services/inspection/internal/platform/graphqlcontract"
)

func main() {
	schemaPath := flag.String("schema", "schema.graphqls", "canonical GraphQL schema")
	outputPath := flag.String("out", "operation-manifest.json", "manifest output path")
	products := flag.String("products", "admin=../../apps/admin/src/features/admin/operations.graphql,dashboard=../../apps/dashboard/src/features/dashboard/operations.graphql,capture=../../apps/capture/src/graphql/documents/capture.graphql", "comma-separated product=document path entries")
	matrixPath := flag.String("matrix", "../../.compozy/tasks/graphql-frontend-capability-parity/_capability_matrix.md", "capability matrix source")
	flag.Parse()

	schema, err := os.ReadFile(*schemaPath)
	if err != nil {
		fail(err)
	}
	documents := make(map[string][]byte)
	for _, entry := range strings.Split(*products, ",") {
		product, path, ok := strings.Cut(entry, "=")
		if !ok || product == "" || path == "" {
			fail(fmt.Errorf("invalid product document %q", entry))
		}
		payload, err := os.ReadFile(path)
		if err != nil {
			fail(err)
		}
		documents[product] = payload
	}
	manifest, err := graphqlcontract.BuildManifest(schema, documents)
	if err != nil {
		fail(err)
	}
	matrix, err := os.ReadFile(*matrixPath)
	if err != nil {
		fail(err)
	}
	manifest.Capabilities, err = graphqlcontract.ParseCapabilityMatrix(matrix)
	if err != nil {
		fail(err)
	}
	if err := graphqlcontract.ValidateManifest(schema, manifest, []string{"admin", "dashboard", "capture"}); err != nil {
		fail(err)
	}
	payload, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		fail(err)
	}
	if err := os.WriteFile(*outputPath, append(payload, '\n'), 0o644); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
