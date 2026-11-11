package review

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFlowExecutionRepositoryDefaultsToAllTestsWithCoverageAndVerification(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "unselected-ran")
	writeReviewFixture(t, root, "go.mod", "module example.test/noimplicit\n\ngo 1.24\n")
	writeReviewFixture(t, root, "source.go", "package noimplicit\nfunc Run(){}\n")
	writeReviewFixture(t, root, "source_test.go", "package noimplicit\nimport (\"os\";\"testing\")\nfunc TestUnselected(t *testing.T){os.WriteFile(\"unselected-ran\",nil,0600)}\n")
	writeReviewFixture(t, root, "scripts/verify-repository.sh", "#!/bin/sh\ntouch verification-ran\n")
	static, err := Analyze(context.Background(), Options{Dir: root})
	if err != nil {
		t.Fatal(err)
	}
	if static.UnitEvidence != nil {
		t.Fatal("Analyze must remain non-executing")
	}
	if _, err = os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("Analyze executed tests")
	}
	if _, err = os.Stat(filepath.Join(root, "verification-ran")); !os.IsNotExist(err) {
		t.Fatal("Analyze executed repository verification")
	}
	run, err := ReviewRepository(context.Background(), root, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(marker); err != nil {
		t.Fatalf("default review did not execute included test: %v", err)
	}
	r := run.Modules[0].Report
	if r.UnitEvidence == nil || len(r.UnitEvidence.Runs) != 1 || r.UnitEvidence.Runs[0].Outcomes["example.test/noimplicit::TestUnselected"] != "pass" {
		t.Fatal("default review must retain actual test outcomes")
	}
	if r.Coverage == nil || !r.Coverage.SourceVerified {
		t.Fatal("default review must retain verified coverage")
	}
	if _, err = os.Stat(filepath.Join(root, "verification-ran")); err != nil {
		t.Fatalf("default review did not execute verification: %v", err)
	}
}

func TestFlowExecutionDefaultAllHonorsScopeAndNormalGoTestEntries(t *testing.T) {
	root := t.TempDir()
	writeReviewFixture(t, root, "go.mod", "module example.test/all\n\ngo 1.24\n")
	writeReviewFixture(t, root, "source.go", "package all\nfunc Value() int {return 1}\n")
	writeReviewFixture(t, root, "source_test.go", `package all
import ("testing";"fmt")
func TestOne(t *testing.T){if Value()!=1{t.Fatal("bad")}}
func TestSkip(t *testing.T){t.Skip("optional")}
func TestExcluded(t *testing.T){t.Fatal("excluded name")}
func ExampleValue(){fmt.Println(Value())
// Output: 1
}
func FuzzValue(f *testing.F){f.Add(1);f.Fuzz(func(t *testing.T, n int){Value()})}
func BenchmarkValue(b *testing.B){b.Fatal("benchmarks must not run")}
`)
	writeReviewFixture(t, root, "external_test.go", "package all_test\nimport \"testing\"\nfunc TestExternal(t *testing.T){}\n")
	writeReviewFixture(t, root, "excluded_test.go", "package all\nimport \"testing\"\nfunc TestExcludedFile(t *testing.T){t.Fatal(\"excluded file\")}\n")
	writeReviewFixture(t, root, "excluded/source.go", "package excluded\nfunc Value(){}\n")
	writeReviewFixture(t, root, "excluded/source_test.go", "package excluded\nimport \"testing\"\nfunc TestExcludedPackage(t *testing.T){t.Fatal(\"excluded package\")}\n")
	writeReviewFixture(t, root, "untested/source.go", "package untested\nfunc Value(){}\n")
	saveFixtureReviewScope(t, root, ReviewScope{ExcludePaths: []string{"excluded_test.go", "excluded"}, ExcludeTests: []string{"^TestExcluded$"}})
	run, err := ReviewRepository(context.Background(), root, t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	r := run.Modules[0].Report
	if r.UnitEvidence == nil || len(r.UnitEvidence.Runs) != 2 {
		t.Fatalf("default review did not execute included packages: %+v", run.Steps)
	}
	first := r.UnitEvidence.Runs[0]
	if first.Status != "pass" {
		t.Fatalf("normal skips must not invalidate an otherwise passing all-tests run: %+v", first)
	}
	for _, name := range []string{"TestOne", "TestExternal", "ExampleValue", "FuzzValue"} {
		if first.Outcomes["example.test/all::"+name] != "pass" {
			t.Errorf("missing included Go test entry %s: %v", name, first.Outcomes)
		}
	}
	if first.Outcomes["example.test/all::TestSkip"] != "skip" {
		t.Fatal("skipped outcome must remain explicit")
	}
	for key := range first.Outcomes {
		if strings.Contains(key, "Excluded") || strings.Contains(key, "Benchmark") {
			t.Errorf("out-of-scope test ran: %s", key)
		}
	}
	if r.UnitEvidence.Runs[1].Status != "pass" || r.Coverage == nil || r.Coverage.CoveredStatements == 0 || !r.Coverage.SourceVerified {
		t.Fatal("all-test review must retain cross-package coverage even for packages without tests")
	}
}

func TestFlowExecutionPartialSelectionDoesNotPreventLaterModuleReview(t *testing.T) {
	for _, allTests := range []bool{false, true} {
		t.Run(map[bool]string{false: "explicit selections", true: "default all tests"}[allTests], func(t *testing.T) {
			root := t.TempDir()
			writeReviewFixture(t, root, "a/go.mod", "module example.test/partial\n\ngo 1.24\n")
			writeReviewFixture(t, root, "a/source.go", "package partial\nfunc Run(){}\n")
			writeReviewFixture(t, root, "a/source_test.go", "package partial\nimport \"testing\"\nfunc TestGood(t *testing.T){Run()}\nfunc TestBad(t *testing.T){Run();t.Fatal(\"selected failure\")}\n")
			writeReviewFixture(t, root, "z/go.mod", "module example.test/later\n\ngo 1.24\n")
			writeReviewFixture(t, root, "z/source.go", "package later\nfunc Run(){}\n")
			writeReviewFixture(t, root, "z/source_test.go", "package later\nimport \"testing\"\nfunc TestLater(t *testing.T){Run()}\n")
			scope := ReviewScope{UnitSelections: []UnitTestSelection{
				{Package: "example.test/partial", Tests: []string{"TestGood", "TestBad"}},
				{Package: "example.test/later", Tests: []string{"TestLater"}},
			}}
			if allTests {
				scope.UnitSelections = nil
			}
			saveFixtureReviewScope(t, root, scope)
			run, err := ReviewRepository(context.Background(), root, t.TempDir(), nil)
			if err != nil {
				t.Fatal(err)
			}
			first, later := run.Modules[0].Report, run.Modules[1].Report
			if first.UnitEvidence == nil || first.UnitEvidence.Runs[0].Status != "partial" || first.UnitEvidence.Runs[0].Error == "" {
				t.Fatal("failed selection was not retained honestly")
			}
			if later.UnitEvidence == nil || later.UnitEvidence.Runs[0].Status != "pass" {
				t.Fatal("failed selection prevented later module evidence")
			}
			if first.Coverage == nil || run.Status != "attention" {
				t.Fatal("partial coverage or review failure was lost")
			}
		})
	}
}
