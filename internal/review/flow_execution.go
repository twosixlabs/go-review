package review

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

type UnitTestSelection struct {
	Package string   `json:"package"`
	Tests   []string `json:"tests"`
}

// ErrWasmUnitHarness distinguishes an unsupported test target from a failed test.
var ErrWasmUnitHarness = errors.New("selected packages depend on retained Wasm imports and require a component-aware unit-test harness, which is not configured; native Go test execution is unavailable for these selections")

func unitTestSupportError(r *Report, selections []UnitTestSelection) error {
	byPackage := map[string][]File{}
	for _, files := range [][]File{r.Files, r.SupportingFiles} {
		for _, file := range files {
			byPackage[file.Package] = append(byPackage[file.Package], file)
		}
	}
	for _, source := range r.TestSources {
		byPackage[source.File.Package] = append(byPackage[source.File.Package], source.File)
	}
	var pending []string
	for _, selection := range selections {
		pending = append(pending, selection.Package, selection.Package+"_test")
	}
	seen := map[string]bool{}
	for len(pending) > 0 {
		pkg := pending[0]
		pending = pending[1:]
		if seen[pkg] {
			continue
		}
		seen[pkg] = true
		for _, file := range byPackage[pkg] {
			tree, err := parser.ParseFile(token.NewFileSet(), file.Path, file.Source, parser.ParseComments|parser.AllErrors)
			if err != nil {
				return fmt.Errorf("inspect retained source for Wasm test imports %s: %w", file.Path, err)
			}
			for _, group := range tree.Comments {
				for _, comment := range group.List {
					fields := strings.Fields(comment.Text)
					if len(fields) > 1 && fields[0] == "//go:wasmimport" {
						return ErrWasmUnitHarness
					}
				}
			}
			for _, spec := range tree.Imports {
				imported, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					return fmt.Errorf("inspect retained import in %s: %w", file.Path, err)
				}
				pending = append(pending, imported)
			}
		}
	}
	return nil
}

type UnitEvidenceOptions struct {
	Selections   []UnitTestSelection
	AllTests     bool
	ArtifactDir  string
	RuntimeTrace bool
}

type UnitEvidence struct {
	SourceSHA256 string        `json:"sourceSHA256"`
	AllTests     bool          `json:"allTests,omitempty"`
	Directory    string        `json:"directory"`
	Runs         []UnitTestRun `json:"runs"`
	Limitations  []string      `json:"limitations"`
}

type UnitTestRun struct {
	Selection        UnitTestSelection `json:"selection"`
	Status           string            `json:"status"`
	Error            string            `json:"error,omitempty"`
	MatchedTests     []string          `json:"matchedTests"`
	Outcomes         map[string]string `json:"outcomes"`
	EventsPath       string            `json:"eventsPath"`
	CoveragePath     string            `json:"coveragePath"`
	RuntimeTracePath string            `json:"runtimeTracePath,omitempty"`
	Runtime          *RuntimeEvidence  `json:"runtime,omitempty"`
	Commands         []Command         `json:"commands"`
}

// CollectUnitEvidence runs exact selections or, when AllTests is authorized,
// normal Go tests in every included package (without benchmarks or fuzz campaigns).
// An all-tests run retains empty selection names and actual matched outcomes.
// Coverage is selection-level evidence; failed runs never acquire passing-test attribution.
// The caller owns a mutable snapshot and persistent, out-of-checkout artifacts.
func CollectUnitEvidence(ctx context.Context, r *Report, opts UnitEvidenceOptions) error {
	if opts.AllTests && len(opts.Selections) > 0 {
		return fmt.Errorf("all-tests execution and explicit selections are mutually exclusive")
	}
	if !opts.AllTests && len(opts.Selections) == 0 {
		return fmt.Errorf("explicit unit-test selections are required")
	}
	var selections []UnitTestSelection
	var err error
	if opts.AllTests {
		for _, p := range r.Packages {
			selections = append(selections, UnitTestSelection{Package: p.Path, Tests: []string{}})
		}
		if len(selections) == 0 {
			return fmt.Errorf("no test packages in review scope")
		}
		sort.Slice(selections, func(i, j int) bool { return selections[i].Package < selections[j].Package })
	} else {
		selections, err = validateUnitSelections(r, opts.Selections)
	}
	if err != nil {
		return err
	}
	if err := unitTestSupportError(r, selections); err != nil {
		return err
	}
	if err = verifyDataSources(ctx, r); err != nil {
		return err
	}
	out, err := artifactDirectory(opts.ArtifactDir)
	if err != nil {
		return err
	}
	if inside(r.Root, out) {
		return fmt.Errorf("unit-test artifacts must be outside the target checkout: %s", out)
	}
	r.Coverage = nil
	r.UnitEvidence = &UnitEvidence{SourceSHA256: r.SourceSHA256, AllTests: opts.AllTests, Directory: out, Runs: []UnitTestRun{}, Limitations: []string{
		"Go tests execute package initialization, dependencies and any TestMain; test code is trusted, not sandboxed.",
		"Coverage establishes exercised source paths for this selection, not an ordered end-to-end scenario or proof that a covered call returned.",
		"Failed selections retain partial aggregate coverage; counters cannot be attributed separately to their passing tests.",
	}}
	packages := make([]string, 0, len(r.Packages))
	for _, p := range r.Packages {
		packages = append(packages, p.Path)
	}
	sort.Strings(packages)
	var profiles []*Coverage
	var failures []error
	for index, selection := range selections {
		if ctx.Err() != nil {
			failures = append(failures, ctx.Err())
			break
		}
		run := UnitTestRun{Selection: selection, Status: "error", MatchedTests: []string{}, Outcomes: map[string]string{}, Commands: []Command{}}
		start := len(r.Commands)
		dir := filepath.Join(out, fmt.Sprintf("selection-%d", index+1))
		e := os.MkdirAll(dir, 0700)
		if e == nil {
			run.EventsPath = filepath.Join(dir, "test-events.jsonl")
			run.CoveragePath = filepath.Join(dir, "coverage.out")
			var scoped executionScope
			scoped, e = prepareExecutionScope(ctx, r, r.Root, filepath.Join(dir, "scope"), []string{selection.Package})
			if e == nil {
				var overlay string
				overlay, e = writeScopeOverlay(filepath.Join(dir, "scope"), scoped.overlay)
				if e == nil {
					names := make([]string, len(selection.Tests))
					for i, name := range selection.Tests {
						names[i] = regexp.QuoteMeta(name)
					}
					args := []string{"test", "-mod=readonly", "-count=1", "-json", "-vet=off", "-covermode=atomic", "-coverpkg=" + strings.Join(packages, ","), "-coverprofile=" + run.CoveragePath}
					if !opts.AllTests {
						args = append(args, "-run=^("+strings.Join(names, "|")+")$")
					}
					if opts.RuntimeTrace {
						run.RuntimeTracePath = filepath.Join(dir, "runtime.trace")
						args = append(args, "-trace="+run.RuntimeTracePath)
					}
					args = append(args, executionArgs(overlay)...)
					args = append(args, selection.Package)
					var data []byte
					data, e = runGo(ctx, r, r.Root, true, args...)
					e = errors.Join(e, os.WriteFile(run.EventsPath, data, 0600))
					var outcomeErr error
					run.Outcomes, outcomeErr = unitTestOutcomes(data)
					e = errors.Join(e, outcomeErr)
					for key := range run.Outcomes {
						pkg, name, ok := strings.Cut(key, "::")
						if !ok || pkg != selection.Package || name == "" || strings.Contains(name, "/") {
							continue
						}
						run.MatchedTests = append(run.MatchedTests, name)
						if !opts.AllTests && !slices.Contains(selection.Tests, name) {
							e = errors.Join(e, fmt.Errorf("unselected test executed: %s", key))
						}
						if opts.AllTests && run.Outcomes[key] == "incomplete" {
							e = errors.Join(e, fmt.Errorf("test did not complete: %s", key))
						}
					}
					sort.Strings(run.MatchedTests)
					for _, name := range selection.Tests {
						if !slices.Contains(run.MatchedTests, name) {
							e = errors.Join(e, fmt.Errorf("selected test did not run: %s::%s", selection.Package, name))
						} else if outcome := run.Outcomes[selection.Package+"::"+name]; outcome != "pass" && outcome != "fail" {
							e = errors.Join(e, fmt.Errorf("selected test did not complete as exercised evidence: %s::%s (%s)", selection.Package, name, outcome))
						}
					}
					profileData, profileErr := os.ReadFile(run.CoveragePath)
					if profileErr == nil {
						scratch := &Report{Root: r.Root, Module: r.Module, Files: r.Files, Exclusions: r.Exclusions}
						profileErr = ImportCoverage(scratch, bytes.NewReader(profileData), run.CoveragePath)
						r.Warnings = append(r.Warnings, scratch.Warnings...)
						if profileErr == nil {
							profiles = append(profiles, scratch.Coverage)
						}
					}
					if profileErr != nil {
						e = errors.Join(e, fmt.Errorf("selection coverage unavailable: %w", profileErr))
					}
				}
			}
		}
		if opts.RuntimeTrace && run.RuntimeTracePath != "" {
			e = errors.Join(e, collectStandardRuntime(ctx, r, &run, r.SourceSHA256))
		}
		run.Commands = append(run.Commands, r.Commands[start:]...)
		run.Status = "pass"
		if e != nil {
			run.Status, run.Error = "partial", e.Error()
			if ctx.Err() != nil {
				run.Status = "canceled"
			}
			failures = append(failures, fmt.Errorf("selection %s: %w", selection.Package, e))
		}
		r.UnitEvidence.Runs = append(r.UnitEvidence.Runs, run)
	}
	// Cancellation must not skip the read-only freshness check for completed
	// profiles. Nothing from changed sources is joined into the architecture.
	if err = verifyDataSources(context.WithoutCancel(ctx), r); err != nil {
		r.Coverage = nil
		failures = append(failures, err)
		for i := range r.UnitEvidence.Runs {
			r.UnitEvidence.Runs[i].Status = "stale"
			if runtime := r.UnitEvidence.Runs[i].Runtime; runtime != nil {
				runtime.Status, runtime.Error, runtime.Observations = "stale", err.Error(), []RuntimeObservation{}
			}
			previous := r.UnitEvidence.Runs[i].Error
			r.UnitEvidence.Runs[i].Error = err.Error()
			if previous != "" {
				r.UnitEvidence.Runs[i].Error = previous + "\n" + err.Error()
			}
		}
	} else if len(profiles) > 0 {
		err = mergeUnitCoverage(r, profiles, filepath.Join(out, "coverage.out"))
		if err != nil {
			failures = append(failures, err)
		}
	}
	r.SystemFlow = BuildSystemFlow(r)
	return errors.Join(failures...)
}

func validateUnitSelections(r *Report, requested []UnitTestSelection) ([]UnitTestSelection, error) {
	var result []UnitTestSelection
	for _, selection := range requested {
		if err := validateUnitSelectionSyntax(selection); err != nil {
			return nil, err
		}
		pkg := ""
		for _, p := range r.Packages {
			candidate := strings.TrimPrefix(p.Path, r.Module)
			if p.Path == selection.Package || selection.Package == "."+candidate {
				pkg = p.Path
				break
			}
		}
		if pkg == "" {
			return nil, fmt.Errorf("unit-test package is not in the snapshot: %s", selection.Package)
		}
		names := append([]string{}, selection.Tests...)
		slices.Sort(names)
		names = slices.Compact(names)
		for _, name := range names {
			found := false
			for _, source := range r.TestSources {
				if source.File.Package != pkg && source.File.Package != pkg+"_test" {
					continue
				}
				for _, decl := range source.Declarations {
					if decl.Name == name && decl.Kind == "test" {
						found = true
					}
				}
			}
			if !found {
				return nil, fmt.Errorf("selected unit test is absent from snapshot: %s::%s", pkg, name)
			}
			if why := r.Scope.testReason(name); why != "" {
				return nil, fmt.Errorf("selected unit test %s: %s", name, why)
			}
		}
		result = append(result, UnitTestSelection{Package: pkg, Tests: names})
	}
	return result, nil
}

func validateUnitSelectionSyntax(selection UnitTestSelection) error {
	if selection.Package == "" || strings.HasPrefix(selection.Package, "-") || strings.HasPrefix(selection.Package, "/") || strings.Contains(selection.Package, "...") || strings.TrimSpace(selection.Package) != selection.Package || len(strings.Fields(selection.Package)) != 1 {
		return fmt.Errorf("an exact included package is required: %q", selection.Package)
	}
	if len(selection.Tests) == 0 {
		return fmt.Errorf("explicit top-level unit-test names required for %s", selection.Package)
	}
	for _, name := range selection.Tests {
		if name == "TestMain" || !strings.HasPrefix(name, "Test") || !testEntry(name) || !token.IsIdentifier(name) {
			return fmt.Errorf("exact top-level unit-test name required: %q", name)
		}
	}
	return nil
}

func unitTestOutcomes(data []byte) (map[string]string, error) {
	out := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 4096), 8<<20)
	for sc.Scan() {
		var event struct{ Action, Package, Test string }
		if err := json.Unmarshal(sc.Bytes(), &event); err != nil {
			return out, fmt.Errorf("decode selected test output: %w", err)
		}
		key := event.Package + "::" + event.Test
		switch event.Action {
		case "run":
			out[key] = "incomplete"
		case "pass", "fail", "skip":
			out[key] = event.Action
		}
	}
	return out, sc.Err()
}

func mergeUnitCoverage(r *Report, profiles []*Coverage, path string) error {
	blocks := map[string]CoverageBlock{}
	for _, profile := range profiles {
		for _, block := range profile.Blocks {
			key := fmt.Sprintf("%s:%d.%d,%d.%d %d", block.Path, block.StartLine, block.StartColumn, block.EndLine, block.EndColumn, block.Statements)
			total := blocks[key]
			block.Count += total.Count
			blocks[key] = block
		}
	}
	keys := make([]string, 0, len(blocks))
	for key := range blocks {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var text strings.Builder
	text.WriteString("mode: atomic\n")
	for _, key := range keys {
		fmt.Fprintf(&text, "%s %d\n", key, blocks[key].Count)
	}
	if err := os.WriteFile(path, []byte(text.String()), 0600); err != nil {
		return err
	}
	if err := ImportCoverage(r, strings.NewReader(text.String()), path); err != nil {
		return err
	}
	r.Coverage.SourceVerified = true
	return nil
}
