// Package graphqlcontract validates the canonical GraphQL contract shared by Inspection products.
package graphqlcontract

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
)

// Capability identifies the accountable journey for one root GraphQL field.
type Capability struct {
	Root     string `json:"root"`
	Owner    string `json:"owner"`
	Persona  string `json:"persona"`
	Journey  string `json:"journey"`
	Evidence string `json:"evidence"`
}

// Manifest is the deterministic product-operation inventory for one schema.
type Manifest struct {
	SchemaSHA256 string             `json:"schemaSha256"`
	Capabilities []Capability       `json:"capabilities"`
	Products     []ProductInventory `json:"products"`
}

var capabilityRow = regexp.MustCompile("(?m)^\\|\\s*\\d+\\s*\\|\\s*`([^`]+)`\\s*\\|\\s*([^|]+?)\\s*\\|\\s*[^|]*\\|\\s*(.+?)\\s*\\|\\s*$")

// ParseCapabilityMatrix turns the reviewed Markdown matrix into an executable root-field mapping.
func ParseCapabilityMatrix(markdown []byte) ([]Capability, error) {
	sections := strings.SplitN(string(markdown), "## Mutations", 2)
	if len(sections) != 2 {
		return nil, fmt.Errorf("capability matrix is missing query or mutation sections")
	}
	capabilities := append(matrixCapabilities(sections[0], "Query"), matrixCapabilities(sections[1], "Mutation")...)
	if len(capabilities) == 0 {
		return nil, fmt.Errorf("capability matrix has no operation rows")
	}
	return capabilities, nil
}

func matrixCapabilities(section, rootKind string) []Capability {
	matches := capabilityRow.FindAllStringSubmatch(section, -1)
	capabilities := make([]Capability, 0, len(matches))
	for _, match := range matches {
		owner := strings.TrimSpace(match[2])
		capabilities = append(capabilities, Capability{Root: rootKind + "." + strings.TrimSpace(match[1]), Owner: owner, Persona: personaForOwner(owner), Journey: strings.TrimSpace(match[3]), Evidence: "IT-001"})
	}
	return capabilities
}

func personaForOwner(owner string) string {
	switch {
	case strings.Contains(owner, "Capture"):
		return "external participant"
	case strings.Contains(owner, "customer"):
		return "customer viewer"
	case strings.Contains(owner, "Dashboard"):
		return "manager, employee, or internal viewer"
	case strings.Contains(owner, "Shared"):
		return "authenticated internal user"
	default:
		return "administrative user"
	}
}

// ProductInventory records each named product operation and its document digest.
type ProductInventory struct {
	Product    string      `json:"product"`
	Operations []Operation `json:"operations"`
}

// Operation is a named operation declared in a product-owned document.
type Operation struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	DocumentSHA string `json:"documentSha256"`
}

// SchemaHash returns the stable SHA-256 digest for the canonical schema bytes.
func SchemaHash(schema []byte) string {
	digest := sha256.Sum256(schema)
	return hex.EncodeToString(digest[:])
}

// ValidateCapabilities verifies one complete, documented owner mapping per root field.
func ValidateCapabilities(schema []byte, capabilities []Capability) error {
	document, err := gqlparser.LoadSchema(&ast.Source{Name: "schema.graphqls", Input: string(schema)})
	if err != nil {
		return fmt.Errorf("parse canonical schema: %w", err)
	}

	wanted := rootFields(document)
	seen := make(map[string]struct{}, len(capabilities))
	for _, capability := range capabilities {
		if capability.Root == "" || capability.Owner == "" || capability.Persona == "" || capability.Journey == "" || capability.Evidence == "" {
			return fmt.Errorf("capability %q is missing owner, persona, journey, or evidence", capability.Root)
		}
		if _, exists := wanted[capability.Root]; !exists {
			return fmt.Errorf("capability %q is not a canonical root field", capability.Root)
		}
		if _, duplicate := seen[capability.Root]; duplicate {
			return fmt.Errorf("capability %q is mapped more than once", capability.Root)
		}
		seen[capability.Root] = struct{}{}
	}

	for root := range wanted {
		if _, exists := seen[root]; !exists {
			return fmt.Errorf("canonical root field %q is unmapped", root)
		}
	}
	for _, mutation := range document.Mutation.Fields {
		argument := mutation.Arguments.ForName("input")
		if argument == nil {
			return fmt.Errorf("mutation %q must accept an input with clientMutationId", mutation.Name)
		}
		input := document.Types[argument.Type.Name()]
		if input == nil || input.Kind != ast.InputObject || input.Fields.ForName("clientMutationId") == nil || input.Fields.ForName("clientMutationId").Type.String() != "String!" {
			return fmt.Errorf("mutation %q must require input.clientMutationId: String!", mutation.Name)
		}
	}
	return nil
}

// BuildManifest parses product documents and returns a sorted, schema-bound inventory.
func BuildManifest(schema []byte, documents map[string][]byte) (Manifest, error) {
	canonical, err := gqlparser.LoadSchema(&ast.Source{Name: "schema.graphqls", Input: string(schema)})
	if err != nil {
		return Manifest{}, fmt.Errorf("parse canonical schema: %w", err)
	}
	manifest := Manifest{SchemaSHA256: SchemaHash(schema)}
	products := make([]string, 0, len(documents))
	for product := range documents {
		products = append(products, product)
	}
	sort.Strings(products)
	for _, product := range products {
		input := documents[product]
		document, queryErrors := gqlparser.LoadQuery(canonical, string(input))
		if queryErrors != nil {
			return Manifest{}, fmt.Errorf("validate %s product document: %s", product, queryErrors.Error())
		}
		inventory := ProductInventory{Product: product}
		for _, definition := range document.Operations {
			if definition.Name == "" {
				return Manifest{}, fmt.Errorf("%s product document has an unnamed operation", product)
			}
			inventory.Operations = append(inventory.Operations, Operation{Name: definition.Name, Type: string(definition.Operation), DocumentSHA: SchemaHash([]byte(definition.Position.Src.Input))})
		}
		sort.Slice(inventory.Operations, func(i, j int) bool { return inventory.Operations[i].Name < inventory.Operations[j].Name })
		manifest.Products = append(manifest.Products, inventory)
	}
	return manifest, nil
}

// ValidateManifest rejects a stale schema hash or partial product inventory.
func ValidateManifest(schema []byte, manifest Manifest, requiredProducts []string) error {
	if manifest.SchemaSHA256 != SchemaHash(schema) {
		return fmt.Errorf("schema hash mismatch: manifest=%s canonical=%s", manifest.SchemaSHA256, SchemaHash(schema))
	}
	if err := ValidateCapabilities(schema, manifest.Capabilities); err != nil {
		return err
	}
	products := make(map[string]ProductInventory, len(manifest.Products))
	for _, product := range manifest.Products {
		if product.Product == "" || len(product.Operations) == 0 {
			return fmt.Errorf("product inventory %q is empty", product.Product)
		}
		if _, duplicate := products[product.Product]; duplicate {
			return fmt.Errorf("product inventory %q is duplicated", product.Product)
		}
		products[product.Product] = product
	}
	for _, product := range requiredProducts {
		if _, exists := products[product]; !exists {
			return fmt.Errorf("product inventory %q is missing", product)
		}
	}
	return nil
}

func rootFields(document *ast.Schema) map[string]struct{} {
	fields := make(map[string]struct{})
	for _, root := range []string{"Query", "Mutation"} {
		definition := document.Types[root]
		if definition == nil {
			continue
		}
		for _, field := range definition.Fields {
			if len(field.Name) > 1 && field.Name[:2] == "__" {
				continue
			}
			fields[root+"."+field.Name] = struct{}{}
		}
	}
	return fields
}
