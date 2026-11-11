package review

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"regexp"
	"sort"
	"strings"
)

// Assessment is a versioned set of source-bound signals, not an architecture
// certification. Checks that execute target code are never implied by analysis.
type Assessment struct {
	Version      string              `json:"version"`
	SourceSHA256 string              `json:"sourceSHA256"`
	Rules        []SignalRule        `json:"rules"`
	Findings     []SignalFinding     `json:"findings"`
	Packages     []PackageAssessment `json:"packages"`
	Checks       []HygieneCheck      `json:"checks"`
}

type SignalRule struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

type SignalFinding struct {
	ID       string   `json:"id"`
	RuleID   string   `json:"ruleID"`
	Package  string   `json:"package"`
	SymbolID string   `json:"symbolID,omitempty"`
	Severity string   `json:"severity"`
	Summary  string   `json:"summary"`
	Evidence string   `json:"evidence"`
	Location Location `json:"location"`
}

type PackageAssessment struct {
	Package   string `json:"package"`
	Level     string `json:"level"`
	Files     int    `json:"files"`
	Functions int    `json:"functions"`
	Owners    int    `json:"owners"`
	Reason    string `json:"reason,omitempty"`
}

type HygieneCheck struct {
	ID        string     `json:"id"`
	Title     string     `json:"title"`
	Status    string     `json:"status"`
	Detail    string     `json:"detail"`
	Locations []Location `json:"locations"`
}

var ownershipPlumbing = regexp.MustCompile(`^(clone[A-Z]\w*|copy[A-Z]\w*|Err\w+Nil|With[A-Z]\w*Key)$`)

// Assess inspects only snapshot production source. Existing complexity, coverage
// and function size never feed this independent architecture indicator.
func Assess(r *Report) *Assessment {
	result := &Assessment{
		Version: "architecture-signals/1", SourceSHA256: r.SourceSHA256,
		Rules: []SignalRule{
			{"ownership-plumbing", "Ownership plumbing", "Calls named cloneX/copyX/WithXKey and references to ErrXNil need an ownership/invariant justification. Name-based heuristic, not a defect; one finding per owner."},
			{"forwarding", "Forwarding layer", "A function only returns another call with the same named arguments. Check that the boundary hides meaningful complexity. One wrapper may be entirely appropriate."},
			{"wide-interface", "Wide interface", "10–19 explicitly declared interface methods: medium; 20 or more: high. Embedded methods are not expanded. Check consumer needs before splitting."},
		},
		Findings: []SignalFinding{}, Packages: []PackageAssessment{},
		Checks: []HygieneCheck{
			{ID: "gofmt", Title: "Go formatting", Status: "not-run", Detail: "go/format.Source compared with collected production source.", Locations: []Location{}},
			{ID: "syntax", Title: "Go syntax", Status: "not-run", Detail: "go/parser on collected production source.", Locations: []Location{}},
			{ID: "types", Title: "Type resolution", Status: "not-run", Detail: "Existing go/types analysis; not a substitute for go vet.", Locations: []Location{}},
			{ID: "repository-hygiene", Title: "Repository hygiene script", Status: "not-run", Detail: "Repository policy scripts are not executed by static analysis.", Locations: []Location{}},
			{ID: "vet", Title: "Go vet", Status: "not-run", Detail: "No go vet execution captured by this assessment.", Locations: []Location{}},
			{ID: "deadcode", Title: "Unreachable code", Status: "not-run", Detail: "No deadcode execution captured; a static call graph does not establish unreachability.", Locations: []Location{}},
			{ID: "tests", Title: "Selected unit tests", Status: "not-run", Detail: "No explicitly selected unit-test execution captured. Imported coverage does not establish a selected test outcome.", Locations: []Location{}},
		},
	}
	scopes := map[string]*PackageAssessment{}
	for _, p := range r.Packages {
		scopes[p.Path] = &PackageAssessment{Package: p.Path}
	}
	seen := map[string]bool{}
	nonCalls := map[string]bool{}
	for _, call := range r.Calls {
		if call.Kind == "conversion" || call.Kind == "builtin" {
			nonCalls[fmt.Sprintf("%s:%d", call.FileID, call.Offset)] = true
		}
	}
	for _, file := range r.Files {
		if strings.HasSuffix(file.Path, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		tree, parseErr := parser.ParseFile(fset, file.Path, file.Source, parser.ParseComments|parser.AllErrors)
		if tree != nil && ast.IsGenerated(tree) {
			continue
		}
		scope := scopes[file.Package]
		if scope == nil {
			continue
		}
		scope.Files++
		for i := 0; i < 2; i++ {
			if result.Checks[i].Status == "not-run" {
				result.Checks[i].Status = "pass"
			}
		}
		location := func(start, end token.Pos) Location {
			s, e := fset.Position(start), fset.Position(end)
			return Location{FileID: file.ID, Line: s.Line, Column: s.Column, EndLine: e.Line, EndColumn: e.Column, Offset: s.Offset, EndOffset: e.Offset}
		}
		fileLoc := Location{FileID: file.ID, Line: 1, Column: 1, EndLine: 1, EndColumn: 1}
		formatted, formatErr := format.Source([]byte(file.Source))
		if formatErr != nil {
			result.Checks[0].Status = "error"
			result.Checks[0].Detail += "\n" + file.Path + ": " + formatErr.Error()
			result.Checks[0].Locations = append(result.Checks[0].Locations, fileLoc)
		} else if !bytes.Equal(formatted, []byte(file.Source)) {
			if result.Checks[0].Status != "error" {
				result.Checks[0].Status = "fail"
			}
			result.Checks[0].Locations = append(result.Checks[0].Locations, fileLoc)
		}
		if parseErr != nil {
			scope.Reason = "Source could not be fully parsed; findings may be incomplete."
			result.Checks[1].Status = "error"
			result.Checks[1].Detail += "\n" + file.Path + ": " + parseErr.Error()
			result.Checks[1].Locations = append(result.Checks[1].Locations, fileLoc)
			continue
		}
		owner := func(n ast.Node) string {
			loc := location(n.Pos(), n.End())
			var best *Symbol
			for i := range r.Symbols {
				s := &r.Symbols[i]
				if s.Location.FileID == file.ID && s.Location.Offset <= loc.Offset && s.Location.EndOffset >= loc.EndOffset &&
					(best == nil || s.Location.EndOffset-s.Location.Offset < best.Location.EndOffset-best.Location.Offset) {
					best = s
				}
			}
			if best != nil {
				return best.ID
			}
			return stableID("owner", fmt.Sprintf("%s:%d", file.ID, loc.Offset))
		}
		add := func(rule, severity, summary, evidence, ownerID string, n ast.Node) {
			loc := location(n.Pos(), n.End())
			key := rule + ":" + ownerID
			if seen[key] {
				return
			}
			seen[key] = true
			result.Findings = append(result.Findings, SignalFinding{
				ID: stableID("finding", key), RuleID: rule, Package: file.Package,
				SymbolID: ownerID, Severity: severity, Summary: summary, Evidence: evidence, Location: loc,
			})
		}
		inspectOwnership := func(body *ast.BlockStmt, name, id string) {
			ast.Inspect(body, func(child ast.Node) bool {
				if _, nested := child.(*ast.FuncLit); nested {
					return false
				}
				match := ""
				switch v := child.(type) {
				case *ast.CallExpr:
					switch target := v.Fun.(type) {
					case *ast.Ident:
						match = target.Name
					case *ast.SelectorExpr:
						match = target.Sel.Name
					}
				case *ast.Ident:
					if strings.HasPrefix(v.Name, "Err") && strings.HasSuffix(v.Name, "Nil") {
						match = v.Name
					}
				}
				if ownershipPlumbing.MatchString(match) {
					add("ownership-plumbing", "low", "Review ownership/invariant plumbing in "+name, match+": verify the ownership boundary or invalid state this protects. This name match does not establish redundant copying or an impossible state.", id, child)
				}
				return true
			})
		}
		ast.Inspect(tree, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.FuncDecl:
				if node.Body != nil {
					scope.Functions++
					scope.Owners++
					id := owner(node)
					if invocation := forwarding(node); invocation != nil {
						loc := location(invocation.Pos(), invocation.End())
						if !nonCalls[fmt.Sprintf("%s:%d", file.ID, loc.Offset)] {
							add("forwarding", "low", node.Name.Name+" only forwards its arguments", "Single return of a call; arguments match named parameters in order. Verify that this layer enforces a real boundary.", id, node)
						}
					}
					inspectOwnership(node.Body, node.Name.Name, id)
				}
			case *ast.FuncLit:
				scope.Functions++
				scope.Owners++
				inspectOwnership(node.Body, fmt.Sprintf("closure at line %d", fset.Position(node.Pos()).Line), owner(node))
			case *ast.TypeSpec:
				if iface, ok := node.Type.(*ast.InterfaceType); ok {
					scope.Owners++
					count := 0
					for _, method := range iface.Methods.List {
						count += len(method.Names)
					}
					if count >= 10 {
						severity := "medium"
						if count >= 20 {
							severity = "high"
						}
						add("wide-interface", severity, node.Name.Name+" exposes a wide interface", fmt.Sprintf("%d explicitly declared methods. Review whether consumers require this entire contract.", count), owner(node), node)
					}
				}
			}
			return true
		})
	}
	for _, p := range r.Packages {
		scope := scopes[p.Path]
		if scope.Files > 0 && result.Checks[2].Status == "not-run" {
			result.Checks[2].Status = "pass"
		}
		if scope.Files > 0 && len(p.TypeErrors) > 0 {
			result.Checks[2].Status = "error"
			result.Checks[2].Detail += "\n" + p.Path + ": " + strings.Join(p.TypeErrors, "\n")
		}
		var findings []SignalFinding
		for _, finding := range result.Findings {
			if finding.Package == p.Path {
				findings = append(findings, finding)
			}
		}
		scope.Level = "not-assessed"
		if scope.Files > 0 && scope.Reason == "" {
			scope.Level = concernLevel(scope.Owners, findings)
			if scope.Owners == 0 {
				scope.Reason = "No function or interface declarations supported by current rules."
			}
		}
		if scope.Files == 0 {
			scope.Reason = "No eligible production source; generated and test files are excluded."
		}
		result.Packages = append(result.Packages, *scope)
	}
	sort.Slice(result.Findings, func(i, j int) bool {
		a, b := result.Findings[i], result.Findings[j]
		if a.Package != b.Package {
			return a.Package < b.Package
		}
		if a.Location.FileID != b.Location.FileID {
			return a.Location.FileID < b.Location.FileID
		}
		if a.Location.Offset != b.Location.Offset {
			return a.Location.Offset < b.Location.Offset
		}
		return a.RuleID < b.RuleID
	})
	return result
}

func forwarding(fn *ast.FuncDecl) *ast.CallExpr {
	if fn.Body == nil || len(fn.Body.List) != 1 {
		return nil
	}
	ret, ok := fn.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(ret.Results) != 1 {
		return nil
	}
	call, ok := ret.Results[0].(*ast.CallExpr)
	if !ok {
		return nil
	}
	if name, ok := call.Fun.(*ast.Ident); ok && name.Name == fn.Name.Name {
		return nil
	}
	var params []string
	for _, field := range fn.Type.Params.List {
		if len(field.Names) == 0 {
			return nil
		}
		for _, name := range field.Names {
			params = append(params, name.Name)
		}
	}
	if len(params) != len(call.Args) {
		return nil
	}
	for i, arg := range call.Args {
		name, ok := arg.(*ast.Ident)
		if !ok || name.Name != params[i] {
			return nil
		}
	}
	return call
}

// Recurrence counts distinct owners, not findings: multiple rules on one
// owner cannot inflate the affected-scope ratio. Complexity is not an input.
func concernLevel(owners int, findings []SignalFinding) string {
	if owners == 0 {
		return "not-assessed"
	}
	level := "clear"
	affected, significant := map[string]bool{}, map[string]bool{}
	for _, f := range findings {
		if f.Severity == "high" {
			return "high-concern"
		}
		if level == "clear" {
			level = "watch"
		}
		if f.SymbolID != "" {
			affected[f.SymbolID] = true
		}
		if f.Severity == "medium" {
			level = "concern"
			if f.SymbolID != "" {
				significant[f.SymbolID] = true
			}
		}
	}
	if len(significant) >= 3 && len(significant)*5 >= owners {
		return "high-concern"
	}
	if level == "watch" && len(affected) >= 3 && len(affected)*5 >= owners {
		return "concern"
	}
	return level
}
