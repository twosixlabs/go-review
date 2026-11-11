package review

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/tools/go/callgraph/rta"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

// reviewDeadcode uses the same rapid type analysis as the Go deadcode tool.
// Program and generated test mains are roots; library APIs are not assumed live.
// Reflection, plugins and other build tags remain outside this build's evidence.
func reviewDeadcode(ctx context.Context, report *Report) (string, string, error) {
	out, err := os.MkdirTemp("", "go-review-scope-rta-")
	if err != nil {
		return "", "", err
	}
	defer os.RemoveAll(out)
	selection, err := prepareExecutionScope(ctx, report, report.Root, out, []string{"./..."})
	if err != nil {
		return "", "", err
	}
	overlay := map[string][]byte{}
	for name, target := range selection.overlay {
		data, e := os.ReadFile(target)
		if e != nil {
			return "", "", e
		}
		overlay[name] = data
	}
	loaded, err := packages.Load(&packages.Config{
		Context: ctx, Dir: report.Root, Mode: packages.LoadAllSyntax, Tests: true,
		Env: append(os.Environ(), "GOTOOLCHAIN=local"), BuildFlags: []string{"-mod=readonly"},
		Overlay: overlay,
	}, selection.packages...)
	if err != nil {
		return "", "", fmt.Errorf("load reachability packages: %w", err)
	}
	var diagnostics []string
	packages.Visit(loaded, nil, func(p *packages.Package) {
		for _, e := range p.Errors {
			diagnostics = append(diagnostics, e.Error())
		}
	})
	if len(diagnostics) > 0 {
		return "", "", fmt.Errorf("reachability type checking: %s", strings.Join(diagnostics, "\n"))
	}
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	program, all := ssautil.AllPackages(loaded, ssa.InstantiateGenerics)
	program.Build()
	var roots []*ssa.Function
	for _, p := range all {
		if p != nil && p.Pkg.Name() == "main" && p.Func("main") != nil {
			roots = append(roots, p.Func("init"), p.Func("main"))
		}
	}
	if len(roots) == 0 {
		return "not-run", "No executable or test-main roots. Library API reachability cannot be established.", nil
	}
	reachable := rta.Analyze(roots, false)
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	files := map[string]bool{}
	for _, f := range report.Files {
		files[filepath.Clean(filepath.Join(report.Root, filepath.FromSlash(f.Path)))] = true
	}
	matches := map[string]bool{}
	livePositions := map[string]bool{}
	for fn := range reachable.Reachable {
		pos := program.Fset.Position(fn.Pos())
		livePositions[fmt.Sprintf("%s:%d:%d", filepath.Clean(pos.Filename), pos.Line, pos.Column)] = true
	}
	for fn := range ssautil.AllFunctions(program) {
		if _, ok := reachable.Reachable[fn]; ok || fn.Parent() != nil || fn.Syntax() == nil || fn.Synthetic != "" {
			continue
		}
		pos := program.Fset.Position(fn.Pos())
		// A source declaration may have both ordinary and test-package SSA
		// instances. Reachability in either variant keeps the declaration live.
		if livePositions[fmt.Sprintf("%s:%d:%d", filepath.Clean(pos.Filename), pos.Line, pos.Column)] {
			continue
		}
		if files[filepath.Clean(pos.Filename)] {
			rel, _ := filepath.Rel(report.Root, pos.Filename)
			matches[fmt.Sprintf("%s:%d: %s", filepath.ToSlash(rel), pos.Line, fn.String())] = true
		}
	}
	var lines []string
	for line := range matches {
		lines = append(lines, line)
	}
	sort.Strings(lines)
	status := "pass"
	if len(lines) > 0 {
		status = "fail"
	}
	return status, fmt.Sprintf("%s · RTA with executable and test roots; current build only. Unreachable is a review signal, not proof safe to delete. Reflection/plugins may not be resolved.\n%d unreachable production declarations\n%s",
		dataToolVersion(), len(lines), strings.Join(lines, "\n")), nil
}
