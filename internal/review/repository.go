package review

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type ReviewProgress struct {
	Phase           string    `json:"phase"`
	Module          string    `json:"module,omitempty"`
	Directory       string    `json:"directory"`
	Root            string    `json:"root"`
	StartedAt       time.Time `json:"startedAt"`
	PhaseStartedAt  time.Time `json:"phaseStartedAt"`
	Command         *Command  `json:"command,omitempty"`
	CompletedStages int       `json:"completedStages"`
	TotalStages     int       `json:"totalStages"`
}

type commandProgressKey struct{}

type ReviewStep struct {
	ID         string    `json:"id"`
	Module     string    `json:"module,omitempty"`
	Title      string    `json:"title"`
	Status     string    `json:"status"`
	Detail     string    `json:"detail"`
	StartedAt  time.Time `json:"startedAt"`
	FinishedAt time.Time `json:"finishedAt"`
	Commands   []Command `json:"commands,omitempty"`
	Inputs     []Input   `json:"inputs,omitempty"`
}

type ReviewedModule struct {
	Directory string  `json:"directory"`
	Error     string  `json:"error,omitempty"`
	Report    *Report `json:"report,omitempty"`
}

type RepositoryReview struct {
	Root       string           `json:"root"`
	Directory  string           `json:"directory"`
	Status     string           `json:"status"`
	StartedAt  time.Time        `json:"startedAt"`
	FinishedAt time.Time        `json:"finishedAt"`
	Steps      []ReviewStep     `json:"steps"`
	Modules    []ReviewedModule `json:"modules"`
	Scope      ReviewScope      `json:"scope"`
	Exclusions []ScopeExclusion `json:"exclusions"`
}

type ReviewReference struct {
	Root        string    `json:"root"`
	Directory   string    `json:"directory"`
	Status      string    `json:"status"`
	StartedAt   time.Time `json:"startedAt"`
	FinishedAt  time.Time `json:"finishedAt"`
	ModuleCount int       `json:"moduleCount"`
}

// ReviewRepository is the execution boundary: callers must obtain explicit
// authorization before invoking it. Artifacts live outside the target checkout;
// repository tests/scripts remain arbitrary code, not sandboxed by this tool.
// Check failures are evidence, not fatal collection errors.
func ReviewRepository(ctx context.Context, dir, storage string, progress func(ReviewProgress)) (*RepositoryReview, error) {
	root, err := resolveReviewRoot(dir)
	if err != nil {
		return nil, err
	}
	scope, err := LoadReviewScope(root)
	if err != nil {
		return nil, err
	}
	modules, err := discoverReviewModules(ctx, root)
	if err != nil {
		return nil, err
	}
	if len(modules) == 0 {
		return nil, fmt.Errorf("no Go modules found under %s (fixtures and dependency directories excluded)", root)
	}
	var excludedModules []ScopeExclusion
	includedModules := modules[:0]
	for _, module := range modules {
		rel, _ := filepath.Rel(root, module)
		if why := scope.pathReason(filepath.ToSlash(rel)); why != "" {
			excludedModules = append(excludedModules, ScopeExclusion{Path: filepath.ToSlash(rel), Reason: why})
		} else {
			includedModules = append(includedModules, module)
		}
	}
	modules = includedModules
	if len(modules) == 0 {
		return nil, fmt.Errorf("no Go modules in review scope")
	}
	if storage == "" {
		cache, e := os.UserCacheDir()
		if e != nil {
			return nil, fmt.Errorf("locate review storage: %w", e)
		}
		storage = filepath.Join(cache, "go-review", "runs")
	}
	if err = os.MkdirAll(storage, 0700); err != nil {
		return nil, fmt.Errorf("create review storage: %w", err)
	}
	out, err := os.MkdirTemp(storage, time.Now().UTC().Format("20060102T150405-"))
	if err != nil {
		return nil, err
	}
	run := &RepositoryReview{Root: root, Directory: out, Status: "completed", StartedAt: time.Now().UTC(), Steps: []ReviewStep{}, Modules: []ReviewedModule{}, Scope: scope, Exclusions: excludedModules}
	moduleStages := []struct{ id, title string }{
		{"vet", "Go vet"},
		{"deadcode", "Unreachable code (RTA)"},
		{"tests", "Go tests and architectural coverage"},
		{"freshness", "Source snapshot consistency"},
	}
	// Each module has source analysis plus these execution/freshness stages.
	// A terminal failure or explicit skip is processed work, not a passing check.
	totalStages := len(modules)*(1+len(moduleStages)) + 1
	var current ReviewProgress
	emit := func() {
		if progress != nil {
			progress(current)
		}
	}
	ctx = context.WithValue(ctx, commandProgressKey{}, func(command *Command) {
		current.Command = command
		emit()
	})
	step := func(id, title, module string, action func(*ReviewStep)) ReviewStep {
		s := ReviewStep{ID: id, Title: title, Module: module, Status: "pass", StartedAt: time.Now().UTC()}
		current = ReviewProgress{Phase: title, Module: module, Directory: out, Root: root, StartedAt: run.StartedAt, PhaseStartedAt: s.StartedAt, CompletedStages: len(run.Steps), TotalStages: totalStages}
		emit()
		if ctx.Err() != nil {
			s.Status, s.Detail = "error", ctx.Err().Error()
		} else {
			action(&s)
		}
		s.FinishedAt = time.Now().UTC()
		run.Steps = append(run.Steps, s)
		current.CompletedStages = len(run.Steps)
		emit()
		if s.Status != "pass" {
			run.Status = "attention"
		}
		return s
	}
	// Analyze before executing the repository hook so edits made by the hook
	// cannot silently acquire the original snapshot's freshness claim.
	for _, module := range modules {
		m := ReviewedModule{Directory: module}
		step("analysis", "Source, architecture and formatting", module, func(s *ReviewStep) {
			m.Report, err = Analyze(ctx, Options{Dir: module, DocumentationDir: root, Scope: &scope})
			if err != nil {
				m.Error = err.Error()
				s.Status, s.Detail = "error", m.Error
			} else {
				for _, c := range m.Report.Assessment.Checks[:3] {
					if c.Status == "error" || c.Status == "fail" && s.Status != "error" {
						s.Status = c.Status
					}
				}
				s.Detail = fmt.Sprintf("%d packages · %d architecture signals · source %s", len(m.Report.Packages), len(m.Report.Assessment.Findings), m.Report.SourceSHA256)
			}

		})
		if m.Report == nil && m.Error == "" {
			m.Error = ctx.Err().Error()
		}
		run.Modules = append(run.Modules, m)
	}
	var selectionProblems []string
	for _, selection := range scope.UnitSelections {
		matched := false
		for _, module := range run.Modules {
			if module.Report == nil {
				continue
			}
			for _, p := range module.Report.Packages {
				if p.Path != selection.Package {
					continue
				}
				matched = true
				if _, err := validateUnitSelections(module.Report, []UnitTestSelection{selection}); err != nil {
					selectionProblems = append(selectionProblems, err.Error())
				}
			}
		}
		if !matched {
			selectionProblems = append(selectionProblems, "Saved unit-test package is absent from analyzed scope: "+selection.Package)
		}
	}
	hookReport := &Report{}
	hook := step("repository-hygiene", "Repository verification", "", func(s *ReviewStep) {
		if !scope.VerifyRepository {
			s.Status, s.Detail = "not-run", "Repository scripts require separate authorization in review scope; they are not run to collect behavior evidence."
			return
		}
		if why := scope.pathReason("scripts/verify-repository.sh"); why != "" {
			s.Status, s.Detail = "not-run", why
			run.Exclusions = append(run.Exclusions, ScopeExclusion{Path: "scripts/verify-repository.sh", Reason: why})
			return
		}
		script := filepath.Join(root, "scripts", "verify-repository.sh")
		info, e := os.Lstat(script)
		if os.IsNotExist(e) {
			s.Status, s.Detail = "not-run", "No scripts/verify-repository.sh found. Built-in Go checks still run."
			return
		}
		if e != nil || !info.Mode().IsRegular() {
			s.Status, s.Detail = "error", fmt.Sprintf("verification entry point must be a regular file, not a symlink: %s (%v)", script, e)
			return
		}
		realScript, e := filepath.EvalSymlinks(script)
		if e != nil || !inside(root, realScript) {
			s.Status, s.Detail = "error", fmt.Sprintf("verification entry point resolves outside repository or is unavailable: %s (%v)", script, e)
			return
		}
		data, e := os.ReadFile(script)
		if e != nil {
			s.Status, s.Detail = "error", fmt.Sprintf("read verification entry point: %v", e)
			return
		}
		s.Inputs = []Input{{Path: "scripts/verify-repository.sh", SHA256: digest(data)}}
		_, e = runCommand(ctx, hookReport, root, true, "bash", script)
		s.Commands = hookReport.Commands
		commandStep(s, e)
		s.Detail = "Repository script is authoritative; its commands are not restricted by direct-review exclusions.\n" + s.Detail
	})
	for i := range run.Modules {
		m := &run.Modules[i]
		r := m.Report
		if r == nil {
			for _, stage := range moduleStages {
				step(stage.id, stage.title, m.Directory, func(s *ReviewStep) {
					s.Status, s.Detail = "not-run", "Source analysis unavailable: "+m.Error
				})
			}
			continue
		}
		moduleOut := filepath.Join(out, fmt.Sprintf("module-%d", i+1))
		if err = os.MkdirAll(moduleOut, 0700); err != nil {
			return run, fmt.Errorf("create module artifacts: %w", err)
		}
		applyReviewCheck(r, hook)
		r.Commands = append(r.Commands, hook.Commands...)
		for index, action := range []func(*ReviewStep){
			func(s *ReviewStep) {
				scopedVet(ctx, r, filepath.Join(moduleOut, "scope-vet"), s)
			},
			func(s *ReviewStep) {
				var e error
				s.Status, s.Detail, e = reviewDeadcode(ctx, r)
				if e != nil {
					s.Status, s.Detail = "error", e.Error()
				}
			},
			func(s *ReviewStep) {
				if len(selectionProblems) > 0 {
					s.Status, s.Detail = "error", strings.Join(selectionProblems, "\n")
					return
				}
				selections := []UnitTestSelection{}
				for _, selection := range scope.UnitSelections {
					for _, p := range r.Packages {
						if selection.Package == p.Path {
							selections = append(selections, selection)
							break
						}
					}
				}
				allTests := len(scope.UnitSelections) == 0
				if len(selections) == 0 && !allTests {
					s.Status, s.Detail = "not-run", "No explicit unit-test selections saved for this module. Source-derived alternatives remain visible without execution evidence."
					return
				}
				start := len(r.Commands)
				e := CollectUnitEvidence(ctx, r, UnitEvidenceOptions{Selections: selections, AllTests: allTests, ArtifactDir: moduleOut})
				s.Commands = r.Commands[start:]
				if errors.Is(e, ErrWasmUnitHarness) {
					s.Status, s.Detail = "not-run", e.Error()
					return
				}
				commandStep(s, e)
				if s.Status == "pass" && r.Coverage == nil {
					s.Status, s.Detail = "error", "Tests produced no usable coverage profile."
				}
			},
		} {
			stage := moduleStages[index]
			s := step(stage.id, stage.title, m.Directory, action)
			applyReviewCheck(r, s)
		}
		stage := moduleStages[len(moduleStages)-1]
		step(stage.id, stage.title, m.Directory, func(s *ReviewStep) {
			if e := verifyDataSources(ctx, r); e != nil {
				s.Status, s.Detail = "error", e.Error()
				r.Coverage, r.Trace = nil, nil
				r.SystemFlow = BuildSystemFlow(r)
				for j := range r.Assessment.Checks {
					c := &r.Assessment.Checks[j]
					if c.ID != "gofmt" && c.ID != "syntax" && c.ID != "types" {
						c.Status, c.Detail = "error", "Source changed during review: "+e.Error()
					}
				}
				r.Warnings = append(r.Warnings, s.Detail)
			}
		})
		for _, c := range r.Assessment.Checks {
			if c.Status != "pass" {
				run.Status = "attention"
			}
		}
	}
	if ctx.Err() != nil {
		run.Status = "canceled"
	}
	run.FinishedAt = time.Now().UTC()
	for _, m := range run.Modules {
		if m.Report != nil {
			m.Report.Review = &ReviewReference{Root: run.Root, Directory: out, Status: run.Status, StartedAt: run.StartedAt, FinishedAt: run.FinishedAt, ModuleCount: len(run.Modules)}
		}
	}
	data, err := json.Marshal(run)
	if err != nil {
		return run, fmt.Errorf("encode review: %w", err)
	}
	if err = os.WriteFile(filepath.Join(out, "review.json"), data, 0600); err != nil {
		return run, fmt.Errorf("retain review: %w", err)
	}
	return run, nil
}

func applyReviewCheck(r *Report, step ReviewStep) {
	detail := step.Detail
	for _, c := range step.Commands {
		detail += fmt.Sprintf("\n%s %s\nexit %d\n%s\n%s", c.Executable, strings.Join(c.Args, " "), c.ExitCode, c.Stdout, c.Stderr)
	}
	for i := range r.Assessment.Checks {
		if r.Assessment.Checks[i].ID == step.ID {
			r.Assessment.Checks[i].Title = step.Title
			r.Assessment.Checks[i].Status, r.Assessment.Checks[i].Detail = step.Status, detail
			return
		}
	}
	r.Assessment.Checks = append(r.Assessment.Checks, HygieneCheck{ID: step.ID, Title: step.Title, Status: step.Status, Detail: detail, Locations: []Location{}})
}

func commandStep(step *ReviewStep, err error) {
	if err != nil {
		step.Status, step.Detail = "error", err.Error()
	}
	for _, c := range step.Commands {
		if c.ExitCode != 0 {
			step.Status = "fail"
			if c.ExitCode < 0 {
				step.Status = "error"
			}
		}
	}
}

func discoverReviewModules(ctx context.Context, root string) ([]string, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("review target must be a directory: %s", root)
	}
	var modules []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() && path != root {
			switch d.Name() {
			case "vendor", "node_modules", "testdata", "testwork", "go-review-report", "dist", "build", "target":
				return filepath.SkipDir
			}
			if strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
		}
		if d.Name() == "go.mod" && d.Type().IsRegular() {
			modules = append(modules, filepath.Dir(path))
		}
		return nil
	})
	sort.Strings(modules)
	return modules, err
}
