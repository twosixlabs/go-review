package review

import (
	"context"
	"strings"
	"testing"
)

func TestScopeUnsafeSelectionsRemainExplicitErrors(t *testing.T) {
	for _, scenario := range []struct {
		name    string
		scope   ReviewScope
		files   map[string]string
		want    string
		execute bool
	}{
		{name: "TestMain cannot be safely renamed", scope: ReviewScope{ExcludeTests: []string{"^TestMain$"}}, files: map[string]string{
			"app_test.go": "package app\nimport \"testing\"\nfunc TestMain(m *testing.M) {}\n",
		}, want: "cannot safely exclude TestMain"},
		{name: "all packages excluded", scope: ReviewScope{ExcludePaths: []string{"app.go"}}, want: "no test packages"},
		{name: "required helper file explicitly excluded", scope: ReviewScope{ExcludePaths: []string{"helper_test.go"}}, files: map[string]string{
			"app_test.go":    "package app\nimport \"testing\"\nfunc TestNeedsHelper(t *testing.T) {helper()}\n",
			"helper_test.go": "package app\nfunc helper() {}\n",
		}, execute: true, want: "undefined: helper"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			root := t.TempDir()
			writeReviewFixture(t, root, "go.mod", "module example.com/unsafe\n\ngo 1.24\n")
			writeReviewFixture(t, root, "app.go", "package app\nfunc App() {}\n")
			for name, text := range scenario.files {
				writeReviewFixture(t, root, name, text)
			}
			scenario.scope.Root = root
			r, err := Analyze(context.Background(), Options{Dir: root, Scope: &scenario.scope})
			if err != nil {
				t.Fatal(err)
			}
			out := t.TempDir()
			if scenario.execute {
				err = CollectUnitEvidence(context.Background(), r, UnitEvidenceOptions{ArtifactDir: out, Selections: []UnitTestSelection{{Package: r.Module, Tests: []string{"TestNeedsHelper"}}}})
				command := r.Commands[len(r.Commands)-1]
				if err == nil && (command.Executable != "go" || command.ExitCode == 0) {
					t.Fatal("unhonorable scope reported a passing test run")
				}
				if err == nil || r.UnitEvidence == nil || r.UnitEvidence.Runs[0].Error == "" {
					t.Fatalf("failed execution was not disclosed: %v", err)
				}
				var diagnostics strings.Builder
				for _, c := range r.Commands {
					diagnostics.WriteString(c.Stderr)
					diagnostics.WriteString(c.Stdout)
				}
				if !strings.Contains(diagnostics.String(), scenario.want) {
					t.Fatalf("missing explicit compile failure: %s (%v)", diagnostics.String(), err)
				}
			} else {
				_, err = prepareExecutionScope(context.Background(), r, r.Root, out, []string{"./..."})
				if err == nil || !strings.Contains(err.Error(), scenario.want) {
					t.Fatalf("unsafe scope not rejected explicitly: %v", err)
				}
			}
		})
	}
}
