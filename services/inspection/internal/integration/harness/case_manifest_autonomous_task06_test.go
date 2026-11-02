package harness

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// aipCaseToken matches the case identifiers a test name may carry after the
// AIP_ prefix: "IT_241", "UT036", and ranges such as "IT_567ToIT576".
var aipCaseToken = regexp.MustCompile(`(UT|IT)_?(\d{3})(?:To(?:UT|IT)?_?(\d{3}))?`)

// aipLocation is where a labelled Test function lives.
type aipLocation struct {
	// Directory is relative to services/inspection and slash separated.
	Directory string
	// File is the base name of the _test.go file.
	File string
	// Integration reports whether the file carries the integration build tag,
	// that is, runs against real adapters instead of in-memory stubs.
	Integration bool
}

// aipLabelledTests maps each case identifier to the locations holding a Test
// function labelled with it.
func aipLabelledTests(t *testing.T) map[string][]aipLocation {
	t.Helper()
	root := filepath.Join("..", "..", "..")
	found := map[string][]aipLocation{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if name := entry.Name(); name == "node_modules" || name == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution|parser.ParseComments)
		if err != nil {
			return err
		}
		directory := filepath.ToSlash(filepath.Dir(path))
		location := aipLocation{Directory: directory, File: filepath.Base(path), Integration: hasIntegrationTag(file)}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Recv != nil || !strings.HasPrefix(function.Name.Name, "TestAIP_") {
				continue
			}
			for _, match := range aipCaseToken.FindAllStringSubmatch(strings.TrimPrefix(function.Name.Name, "TestAIP_"), -1) {
				from, _ := strconv.Atoi(match[2])
				to := from
				if match[3] != "" {
					to, _ = strconv.Atoi(match[3])
				}
				for number := from; number <= to; number++ {
					id := match[1] + "-" + leftPad(number)
					found[id] = append(found[id], location)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan tests: %v", err)
	}
	return found
}

// hasIntegrationTag reports whether the file starts with a go:build constraint
// that mentions the integration tag.
func hasIntegrationTag(file *ast.File) bool {
	for _, group := range file.Comments {
		if group.Pos() >= file.Package {
			break
		}
		for _, comment := range group.List {
			if strings.HasPrefix(comment.Text, "//go:build") && strings.Contains(comment.Text, "integration") {
				return true
			}
		}
	}
	return false
}

func leftPad(number int) string {
	text := strconv.Itoa(number)
	for len(text) < 3 {
		text = "0" + text
	}
	return text
}

// TestAutonomousTask06ManifestAccountsForEveryAssignedCase fails fast when the
// manifest drifts from the task, or when a case claimed as covered has no test
// carrying its identifier in the area that defines it.
func TestAutonomousTask06ManifestAccountsForEveryAssignedCase(t *testing.T) {
	cases := AutonomousTask06Cases()
	if len(cases) != 148 {
		t.Fatalf("manifest lists %d cases, task_06.md assigns 148", len(cases))
	}
	seen := map[string]bool{}
	units, integrations := 0, 0
	for _, item := range cases {
		if seen[item.ID] {
			t.Fatalf("case %s is listed twice", item.ID)
		}
		seen[item.ID] = true
		switch item.Kind {
		case "unit":
			units++
		case "integration":
			integrations++
		default:
			t.Fatalf("case %s has unknown kind %q", item.ID, item.Kind)
		}
	}
	if units != 14 || integrations != 134 {
		t.Fatalf("manifest has %d unit and %d integration cases, want 14 and 134", units, integrations)
	}
	if task, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "..", ".compozy", "tasks", "autonomous-inspection-platform", "task_06.md")); err == nil {
		for _, item := range cases {
			if !strings.Contains(string(task), item.ID) {
				t.Errorf("case %s is in the manifest but not assigned by task_06.md", item.ID)
			}
		}
		for _, assigned := range regexp.MustCompile(`\b(?:UT|IT)-\d{3}\b`).FindAllString(string(task), -1) {
			if !seen[assigned] {
				t.Errorf("case %s is assigned by task_06.md but missing from the manifest", assigned)
			}
		}
	}

	labelled := aipLabelledTests(t)
	var covered, pending, partial, obsolete int
	for _, item := range cases {
		switch item.Status {
		case AutonomousCovered:
			covered++
			if item.Where == "" {
				t.Errorf("covered case %s does not name the area of its test", item.ID)
				continue
			}
			ok := false
			for _, location := range labelled[item.ID] {
				if !strings.Contains(location.Directory+"/", item.Where) && !strings.HasSuffix(location.Directory, item.Where) {
					continue
				}
				if item.File != "" && (location.File != item.File || !location.Integration) {
					continue
				}
				ok = true
				break
			}
			if !ok {
				found := make([]string, 0, len(labelled[item.ID]))
				for _, location := range labelled[item.ID] {
					found = append(found, location.Directory+"/"+location.File)
				}
				sort.Strings(found)
				t.Errorf("case %s is marked covered but no TestAIP_ function for it exists under %q file %q with the integration tag (found in %v)", item.ID, item.Where, item.File, found)
			}
			if item.File != "" && strings.TrimSpace(item.Criterion) == "" {
				t.Errorf("covered case %s is pinned to %s but states no criterion", item.ID, item.File)
			}
		case AutonomousPending, AutonomousPartial, AutonomousObsolete:
			switch item.Status {
			case AutonomousPending:
				pending++
			case AutonomousPartial:
				partial++
			default:
				obsolete++
			}
			if strings.TrimSpace(item.Note) == "" {
				t.Errorf("%s case %s needs a note explaining why and who owns it", item.Status, item.ID)
			}
			if item.Status != AutonomousPartial && len(labelled[item.ID]) > 0 {
				t.Logf("NOTE: case %s is %s in the manifest but TestAIP_ functions carry its label in %v; confirm they prove it or update the manifest", item.ID, item.Status, labelled[item.ID])
			}
		default:
			t.Errorf("case %s has unknown status %q", item.ID, item.Status)
		}
	}
	t.Logf("assigned=%d covered=%d partial=%d obsolete=%d pending=%d", len(cases), covered, partial, obsolete, pending)
}

// TestAutonomousTask06ManifestIsComplete is the completion gate of task 06: it
// only runs when INSPECTION_REQUIRE_COMPLETE_MANIFEST=true and fails while any
// case is still pending or only partially covered.
func TestAutonomousTask06ManifestIsComplete(t *testing.T) {
	if os.Getenv("INSPECTION_REQUIRE_COMPLETE_MANIFEST") != "true" {
		t.Skip("INSPECTION_REQUIRE_COMPLETE_MANIFEST is not true; the task stays pending while cases are open")
	}
	for _, item := range PendingAutonomousTask06Cases() {
		t.Errorf("case %s is still %s: %s", item.ID, item.Status, item.Note)
	}
}
