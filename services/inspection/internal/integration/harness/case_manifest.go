package harness

import "fmt"

// ContractCase identifies one Task 06 case assigned by the canonical test
// catalog. The external suite uses this manifest to fail fast when its case
// corpus is incomplete; the manifest intentionally contains no pass-through
// assertions or synthetic replacements for those cases.
type ContractCase struct {
	ID   string
	Kind string
}

// Task06AssignedCases returns the immutable list of the 148 cases assigned to
// Task 06 (14 unit and 134 integration cases). A fresh slice is returned so a
// caller cannot mutate the process-wide contract.
func Task06AssignedCases() []ContractCase {
	cases := make([]ContractCase, 0, 148)
	for _, id := range []string{"UT-011", "UT-012", "UT-015", "UT-016", "UT-034", "UT-035", "UT-036", "UT-037", "UT-044", "UT-045", "UT-048", "UT-049", "UT-066", "UT-067"} {
		cases = append(cases, ContractCase{ID: id, Kind: "unit"})
	}
	for _, bounds := range [][2]int{{241, 310}, {321, 340}, {371, 375}, {390, 390}, {423, 430}, {433, 438}, {527, 534}, {561, 562}, {567, 576}, {583, 586}} {
		for number := bounds[0]; number <= bounds[1]; number++ {
			cases = append(cases, ContractCase{ID: fmt.Sprintf("IT-%03d", number), Kind: "integration"})
		}
	}
	return cases
}
