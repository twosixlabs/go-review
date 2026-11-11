package review

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Keep transient Go modules outside editor watchers: discovery can otherwise
// race cleanup and try to run go env from an already removed directory.
func workspace(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

func fixture(t *testing.T, opts Options) *Report {
	t.Helper()
	opts.Dir = "testdata/module"
	if opts.DocumentationDir == "" {
		opts.DocumentationDir = opts.Dir
	}
	r, err := Analyze(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestWorkspaceStaysOutsideWatchedRepository(t *testing.T) {
	dir := workspace(t)
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.VolumeName(root) != filepath.VolumeName(dir) {
		return
	}
	relative, err := filepath.Rel(root, dir)
	if err != nil {
		t.Fatal(err)
	}
	if relative == "." || filepath.IsLocal(relative) {
		t.Fatalf("temporary Go module is inside the watched repository: %s", dir)
	}
}

func symbol(t *testing.T, r *Report, name string) Symbol {
	t.Helper()
	for _, s := range r.Symbols {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("missing symbol %q", name)
	return Symbol{}
}

func TestAnalysisStaticEvidenceAndStableIDs(t *testing.T) {
	dir := workspace(t)
	marker := filepath.Join(dir, "test-ran")
	t.Setenv("GO_REVIEW_TEST_MARKER", marker)
	r := fixture(t, Options{})
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("analysis executed target tests")
	}
	if len(r.Packages) != 2 || len(r.Files) != 2 {
		t.Fatalf("packages=%d files=%d", len(r.Packages), len(r.Files))
	}
	flow := symbol(t, r, "Flow")
	// Entry + 2 if + && + || + for + 2 nondefault switch cases + select case.
	if flow.Cyclomatic != 9 {
		t.Fatalf("cyclomatic=%d, want 9", flow.Cyclomatic)
	}
	if !strings.Contains(flow.Doc, "seven decisions") {
		t.Fatal("lost Go documentation")
	}
	if len(flow.Comments) != 1 || !strings.Contains(flow.Comments[0].Text, `alert("source")`) {
		t.Fatal("lost inline source comment evidence")
	}
	help := symbol(t, r, "Help")
	static, indirect, closure, generic, reference := false, 0, false, false, false
	for _, c := range r.Calls {
		if c.From == flow.ID && c.To == help.ID && c.Kind == "static" {
			static = true
		}
		if c.From == flow.ID && c.Kind == "indirect" {
			indirect++
		}
		if c.From == flow.ID && strings.Contains(c.Target, "func literal") && c.Kind == "static" {
			closure = true
		}
		if c.From == symbol(t, r, "UseGeneric").ID && c.To == symbol(t, r, "Generic").ID {
			generic = true
		}
	}
	for _, ref := range r.References {
		if ref.To == help.ID && ref.From == symbol(t, r, "AliasCall").ID {
			reference = true
		}
	}
	if !static || indirect != 2 || !closure || !generic || !reference {
		t.Fatalf("static=%v indirect=%d closure=%v generic=%v reference=%v", static, indirect, closure, generic, reference)
	}
	if len(r.Documents) != 1 || !strings.Contains(r.Documents[0].Text, "Fixture documentation") {
		t.Fatal("missing markdown")
	}
	if len(r.Packages[0].Imports) == 0 {
		t.Fatal("missing package edge")
	}
	if len(r.Commands) < 2 || r.SourceSHA256 == "" || r.Files[0].SHA256 == "" {
		t.Fatal("missing provenance")
	}
	for _, c := range r.Commands {
		if strings.Contains(strings.Join(c.Args, " "), "test ") {
			t.Fatal("unexpected test command")
		}
	}
	r2 := fixture(t, Options{})
	if r.SourceSHA256 != r2.SourceSHA256 {
		t.Fatal("unstable source digest")
	}
	for i := range r.Symbols {
		if r.Symbols[i].ID != r2.Symbols[i].ID {
			t.Fatal("unstable IDs")
		}
	}
	var round Report
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &round); err != nil || len(round.Calls) != len(r.Calls) {
		t.Fatal("invalid persistent JSON")
	}
}

func TestCoverageExactBlocksAndValidation(t *testing.T) {
	r := fixture(t, Options{})
	profile := "mode: count\nexample.test/fixture/flow.go:16.1,18.3 2 0\nexample.test/fixture/flow.go:18.3,18.21 1 4\n"
	if err := ImportCoverage(r, strings.NewReader(profile), "fixture.cover"); err != nil {
		t.Fatal(err)
	}

	if len(r.Coverage.Blocks) != 2 {
		t.Fatal("missing blocks")
	}
	b := r.Coverage.Blocks[1]
	if b.StartLine != 18 || b.StartColumn != 3 || b.EndColumn != 21 || b.Count != 4 || b.FileID == "" {
		t.Fatalf("incorrect exact block: %+v", b)
	}
	if r.Coverage.Statements != 3 || r.Coverage.CoveredStatements != 1 {
		t.Fatalf("incorrect statement totals: %+v", r.Coverage)
	}
	for _, text := range []string{"mode: wrong\n", "mode: count\nbroken\n", "mode: set\nx:0.1,1.2 1 1\n", "mode: set\nx:1.1,1.2 1 -1\n", "mode: set\nx:1.3,1.2 1 1\n", "mode: count\nexample.test/fixture/flow.go:999.1,1000.2 1 1\n"} {
		if err := ImportCoverage(r, strings.NewReader(text), "bad"); err == nil {
			t.Fatalf("accepted malformed coverage %q", text)
		}
	}
	if err := ImportCoverage(r, strings.NewReader("mode: set\nexternal.test/other/file.go:1.1,2.1 1 1\n"), "external"); err != nil {
		t.Fatal(err)
	}
	if r.Coverage.Blocks[0].FileID != "" || len(r.Warnings) == 0 {
		t.Fatal("unmatched coverage silently attached")
	}
}

func TestExplicitDocumentationRoot(t *testing.T) {
	dir := workspace(t)
	if err := os.WriteFile(filepath.Join(dir, "architecture.md"), []byte("# External architecture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	r := fixture(t, Options{DocumentationDir: dir})
	if r.DocumentationRoot != dir || len(r.Documents) != 1 || r.Documents[0].Path != "architecture.md" {
		t.Fatal("explicit documentation root not respected")
	}
	if _, err := Analyze(context.Background(), Options{Dir: "testdata/module", DocumentationDir: filepath.Join(dir, "missing")}); err == nil {
		t.Fatal("invalid documentation root silently accepted")
	}
}

func TestExplicitTestsPersistCommandEvidence(t *testing.T) {
	dir := workspace(t)
	marker := filepath.Join(dir, "marker")
	t.Setenv("GO_REVIEW_TEST_MARKER", marker)
	r := fixture(t, Options{UnitSelections: []UnitTestSelection{{Package: ".", Tests: []string{"TestExplicitOnly"}}}, ArtifactDir: dir})
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("explicit tests did not run: %v", err)
	}
	if r.Coverage == nil || len(r.Coverage.Blocks) == 0 {
		t.Fatal("no real test coverage")
	}
	found := false
	for _, c := range r.Commands {
		if len(c.Args) > 0 && c.Args[0] == "test" {
			found = true
			if c.ExitCode != 0 || !strings.Contains(c.Stdout, `"Action":"pass"`) || c.StdoutSHA256 == "" {
				t.Fatalf("bad test artifact: %+v", c)
			}
		}
	}
	if !found {
		t.Fatal("missing executed command")
	}
	if _, err := os.Stat(filepath.Join(dir, "coverage.out")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "selection-1", "test-events.jsonl")); err != nil {
		t.Fatal(err)
	}
}

func TestFailingTestsRemainVisible(t *testing.T) {
	t.Setenv("GO_REVIEW_TEST_FAIL", "1")
	r := fixture(t, Options{UnitSelections: []UnitTestSelection{{Package: ".", Tests: []string{"TestExplicitOnly"}}}, ArtifactDir: workspace(t)})
	found := false
	for _, c := range r.Commands {
		if len(c.Args) > 0 && c.Args[0] == "test" && c.ExitCode != 0 {
			found = true
		}
	}
	if !found || len(r.Warnings) == 0 {
		t.Fatal("test failure lost")
	}
}
