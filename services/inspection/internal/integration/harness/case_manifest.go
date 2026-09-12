package harness

// ContractCase identifies one Task 06 case assigned by the canonical test
// catalog. The external suite uses this manifest to fail fast when its case
// corpus is incomplete; the manifest intentionally contains no pass-through
// assertions or synthetic replacements for those cases.
type ContractCase struct {
	ID   string
	Kind string
}

// Task06AssignedCases returns the immutable list of cases assigned to Task 06.
// A fresh slice is returned so a caller cannot mutate the process-wide contract.
func Task06AssignedCases() []ContractCase {
	ids := []struct{ id, kind string }{
		{"IT-047", "integration"}, {"IT-048", "integration"}, {"IT-049", "integration"}, {"IT-050", "integration"},
		{"E2E-009", "end-to-end"}, {"E2E-012", "end-to-end"}, {"E2E-013", "end-to-end"}, {"E2E-014", "end-to-end"}, {"E2E-015", "end-to-end"},
	}
	cases := make([]ContractCase, 0, len(ids))
	for _, item := range ids {
		cases = append(cases, ContractCase{ID: item.id, Kind: item.kind})
	}
	return cases
}
