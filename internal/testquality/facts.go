package testquality

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/constant"
	"go/doc"
	"go/format"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/russellbrasser/go-review/internal/review"
	"github.com/russellbrasser/go-review/internal/webui"
)

type CheckFact struct {
	Kind        string   `json:"kind"`
	Expressions []string `json:"expressions"`
	Evidence    Citation `json:"evidence"`
}
type CaseFact struct {
	Label    string            `json:"label"`
	Values   map[string]string `json:"values"`
	Evidence Citation          `json:"evidence"`
}
type EvidenceFact struct {
	Category   string     `json:"category"`
	Provenance string     `json:"provenance"`
	Summary    string     `json:"summary"`
	Evidence   []Citation `json:"evidence,omitempty"`
}
type TestFacts struct {
	Identity     Identity        `json:"identity"`
	Checks       []CheckFact     `json:"checks"`
	Cases        []CaseFact      `json:"cases"`
	Helpers      []Citation      `json:"helpers"`
	Observations []EvidenceFact  `json:"observations"`
	Limitations  []string        `json:"limitations"`
	Predicates   []PredicateFact `json:"predicates,omitempty"`
}
type AnalysisMetrics struct {
	ParsedSources   int `json:"parsedSources"`
	HelpersAnalyzed int `json:"helpersAnalyzed"`
}
type factFunction struct {
	snapshot, pkg, file string
	node                *ast.FuncDecl
	tree                *ast.File
	fset                *token.FileSet
	imports             map[string]string
	output              *doc.Example
	writes              map[*ast.Object]int
}
type factIndex struct {
	functions    map[string][]*factFunction
	memo         map[*factFunction]TestFacts
	active       map[*factFunction]bool
	rows         []TestFacts
	metrics      AnalysisMetrics
	sources      map[string]review.File
	lines        map[string][]string
	queryMu      sync.Mutex
	queries      map[string]*webui.ReviewQuery
	declarations map[Identity]Responsibility
	parseLimits  map[string]string
	comments     map[string][]Citation
	predicates   map[*factFunction][]predicateTemplate
}

func functionKey(snapshot, pkg, name string) string { return snapshot + "\n" + pkg + "\n" + name }

// The pinned evidence revision is immutable. Build a source/binding index once,
// and memoize shared helpers instead of parsing/traversing them for each test.
func AnalyzeFacts(a *Assessment) ([]TestFacts, error) {
	a.factsOnce.Do(func() {
		index := &factIndex{functions: map[string][]*factFunction{}, memo: map[*factFunction]TestFacts{}, active: map[*factFunction]bool{}, sources: map[string]review.File{}, lines: map[string][]string{}, queries: map[string]*webui.ReviewQuery{}}
		index.declarations = map[Identity]Responsibility{}
		index.parseLimits = map[string]string{}
		index.comments = map[string][]Citation{}
		for _, pinned := range a.Reports {
			if pinned.Report == nil {
				a.factsError = fmt.Errorf("missing pinned report %s", pinned.SnapshotID)
				return
			}
			if err := validateSources(pinned.Report); err != nil {
				a.factsError = err
				return
			}
			for _, source := range pinned.Report.TestSources {
				for _, decl := range source.Declarations {
					id := Identity{SnapshotID: pinned.SnapshotID, Module: pinned.Report.Module, SymbolID: decl.ID}
					index.declarations[id] = Responsibility{Name: decl.Name, Declaration: Citation{SnapshotID: pinned.SnapshotID, FileID: source.File.ID, StartLine: decl.Location.Line, EndLine: decl.Location.EndLine}}
				}
			}
			for _, file := range files(pinned.Report) {
				key := pinned.SnapshotID + "\n" + file.ID
				if _, exists := index.sources[key]; exists {
					continue
				}
				index.sources[key] = file
				index.lines[key] = strings.Split(file.Source, "\n")
				if !strings.HasSuffix(file.Path, ".go") {
					continue
				}
				fset := token.NewFileSet()
				tree, err := parser.ParseFile(fset, file.Path, file.Source, parser.ParseComments)
				if err != nil {
					index.parseLimits[key] = fmt.Sprintf("Syntax limits in retained source %s: %v; completeness is unresolved.", file.Path, err)
				}
				if tree == nil {
					continue
				}
				for _, comment := range tree.Comments {
					index.comments[key] = append(index.comments[key], Citation{SnapshotID: pinned.SnapshotID, FileID: file.ID, StartLine: fset.Position(comment.Pos()).Line, EndLine: fset.Position(comment.End()).Line})
				}
				index.metrics.ParsedSources++
				imports := map[string]string{}
				for _, spec := range tree.Imports {
					path, err := strconv.Unquote(spec.Path.Value)
					if err != nil {
						a.factsError = err
						return
					}
					name := path[strings.LastIndex(path, "/")+1:]
					if spec.Name != nil {
						name = spec.Name.Name
					}
					imports[name] = path
				}
				examples := []*doc.Example{}
				if err == nil {
					examples = doc.Examples(tree)
				}
				for _, decl := range tree.Decls {
					fn, ok := decl.(*ast.FuncDecl)
					if !ok || fn.Recv != nil || fn.Name == nil {
						continue
					}
					excluded := false
					for _, testSource := range pinned.Report.TestSources {
						if testSource.File.ID != file.ID {
							continue
						}
						for _, location := range testSource.ExcludedRanges {
							if fset.Position(fn.Pos()).Line <= location.EndLine && fset.Position(fn.End()).Line >= location.Line {
								excluded = true
							}
						}
					}
					if excluded {
						continue
					}
					entry := &factFunction{snapshot: pinned.SnapshotID, pkg: file.Package, file: file.ID, node: fn, tree: tree, fset: fset, imports: imports, writes: objectWrites(fn.Body)}
					for _, example := range examples {
						if fn.Body != nil && example.Code != nil && example.Code.Pos() == fn.Body.Pos() && (example.Output != "" || example.EmptyOutput) {
							entry.output = example
						}
					}
					key := functionKey(entry.snapshot, entry.pkg, fn.Name.Name)
					index.functions[key] = append(index.functions[key], entry)
				}
			}
		}
		for _, item := range a.Items {
			if item.Status == StatusExcluded {
				continue
			}
			row := TestFacts{Identity: item.Identity, Checks: []CheckFact{}, Cases: []CaseFact{}, Helpers: []Citation{}, Observations: []EvidenceFact{}, Limitations: []string{}}
			matches := index.functions[functionKey(item.Identity.SnapshotID, item.Package, item.Name)]
			declaration, exists := index.declarations[item.Identity]
			identityMatches := exists && declaration.Name == item.Name && declaration.Declaration == item.Declaration
			if len(matches) != 1 || !identityMatches || matches[0].file != item.Declaration.FileID || matches[0].fset.Position(matches[0].node.Pos()).Line != item.Declaration.StartLine || matches[0].fset.Position(matches[0].node.End()).Line != item.Declaration.EndLine {
				row.Limitations = append(row.Limitations, "Selected test body is unavailable or ambiguous; missing checks cannot be inferred.")
			} else {
				row = index.inspect(matches[0], 0)
				row.Identity = item.Identity
			}
			if limit := index.parseLimits[item.Identity.SnapshotID+"\n"+item.Declaration.FileID]; limit != "" {
				row.Limitations = append(row.Limitations, limit)
			}
			r, err := pinned(a, item.Identity.SnapshotID)
			if err != nil {
				a.factsError = err
				return
			}
			if r.UnitEvidence == nil {
				row.Limitations = append(row.Limitations, "No retained execution; reliability is unobserved.")
			} else {
				for _, run := range r.UnitEvidence.Runs {
					keys := make([]string, 0, len(run.Outcomes))
					for key := range run.Outcomes {
						keys = append(keys, key)
					}
					sort.Strings(keys)
					for _, key := range keys {
						status := run.Outcomes[key]
						pkg, name, qualified := strings.Cut(key, "::")
						if qualified && pkg == strings.TrimSuffix(item.Package, "_test") && (name == item.Name || strings.HasPrefix(name, item.Name+"/")) {
							row.Observations = append(row.Observations, EvidenceFact{Category: "test-outcome", Provenance: "observed", Summary: key + ": " + status + " (run " + run.Status + ")"})
						}
					}
				}
				if r.SourceSHA256 != "" && r.UnitEvidence.SourceSHA256 != r.SourceSHA256 {
					row.Limitations = append(row.Limitations, "Retained execution source identity differs; outcomes are not source-verified.")
				}
			}
			index.rows = append(index.rows, row)
		}
		a.factsIndex = index
		a.Analysis = index.metrics
		a.Facts = index.rows
	})
	if a.factsError != nil {
		return nil, a.factsError
	}
	return a.factsIndex.rows, nil
}

func factText(fset *token.FileSet, node ast.Node) string {
	var out bytes.Buffer
	if err := format.Node(&out, fset, node); err != nil {
		return fmt.Sprintf("unprintable retained expression: %v", err)
	}
	return out.String()
}
func (f *factFunction) citation(node ast.Node) Citation {
	return Citation{SnapshotID: f.snapshot, FileID: f.file, StartLine: f.fset.Position(node.Pos()).Line, EndLine: f.fset.Position(node.End()).Line}
}

func constantExpression(expr ast.Expr) constant.Value {
	switch e := expr.(type) {
	case *ast.BasicLit:
		return constant.MakeFromLiteral(e.Value, e.Kind, 0)
	case *ast.Ident:
		if e.Name == "true" && e.Obj == nil {
			return constant.MakeBool(true)
		}
		if e.Name == "false" && e.Obj == nil {
			return constant.MakeBool(false)
		}
	case *ast.ParenExpr:
		return constantExpression(e.X)
	case *ast.UnaryExpr:
		value := constantExpression(e.X)
		numeric := value.Kind() == constant.Int || value.Kind() == constant.Float || value.Kind() == constant.Complex
		if e.Op == token.NOT && value.Kind() == constant.Bool || (e.Op == token.ADD || e.Op == token.SUB) && numeric {
			return constant.UnaryOp(e.Op, value, 0)
		}
	case *ast.BinaryExpr:
		a, b := constantExpression(e.X), constantExpression(e.Y)
		if a.Kind() == constant.Unknown || b.Kind() == constant.Unknown || a.Kind() != b.Kind() {
			break
		}
		switch e.Op {
		case token.EQL, token.NEQ, token.LSS, token.LEQ, token.GTR, token.GEQ:
			if (a.Kind() == constant.Bool || a.Kind() == constant.Complex) && e.Op != token.EQL && e.Op != token.NEQ {
				break
			}
			return constant.MakeBool(constant.Compare(a, e.Op, b))
		}
	}
	return constant.MakeUnknown()
}

// Recognized calls are checking sites, not proof that expected values are
// appropriate. Lexical shadowing and dynamic helpers remain explicit unknowns.
func (index *factIndex) inspect(fn *factFunction, depth int) TestFacts {
	if row, ok := index.memo[fn]; ok {
		return row
	}
	row := TestFacts{Checks: []CheckFact{}, Cases: []CaseFact{}, Helpers: []Citation{}, Observations: []EvidenceFact{}, Limitations: []string{}}
	if fn.node.Body == nil {
		row.Limitations = append(row.Limitations, "Retained helper implementation is unavailable; no absence claim is possible.")
		return row
	}
	if fn.output != nil {
		row.Checks = append(row.Checks, CheckFact{Kind: "example-output", Expressions: []string{fn.output.Output}, Evidence: fn.citation(fn.node.Body)})
	}
	if depth >= 8 || index.active[fn] {
		row.Limitations = append(row.Limitations, "Helper cycle/depth limit; assertion completeness is unresolved.")
		return row
	}
	index.active[fn] = true
	defer delete(index.active, fn)
	if depth > 0 {
		index.metrics.HelpersAnalyzed++
	}
	testingObjects := map[*ast.Object]bool{}
	writes := fn.writes
	predicateLimited := false
	addPredicates := func(facts []PredicateFact) {
		remaining := 64 - len(row.Predicates)
		if len(facts) > remaining {
			predicateLimited = true
		}
		row.Predicates = append(row.Predicates, facts[:min(remaining, len(facts))]...)
	}
	addParams := func(fields *ast.FieldList) {
		if fields == nil {
			return
		}
		for _, field := range fields.List {
			pointer, ok := field.Type.(*ast.StarExpr)
			if !ok {
				continue
			}
			selector, ok := pointer.X.(*ast.SelectorExpr)
			if !ok {
				continue
			}
			base, ok := selector.X.(*ast.Ident)
			if !ok || fn.imports[base.Name] != "testing" {
				continue
			}
			if selector.Sel.Name != "T" && selector.Sel.Name != "F" {
				continue
			}
			for _, name := range field.Names {
				if name.Obj != nil {
					testingObjects[name.Obj] = true
				}
			}
		}
	}
	addParams(fn.node.Type.Params)
	ast.Inspect(fn.node.Body, func(node ast.Node) bool {
		if lit, ok := node.(*ast.FuncLit); ok {
			addParams(lit.Type.Params)
		}
		return true
	})
	isTesting := func(expr ast.Expr) bool {
		id, ok := expr.(*ast.Ident)
		return ok && id.Obj != nil && testingObjects[id.Obj]
	}
	var ancestors []ast.Node
	ast.Inspect(fn.node.Body, func(node ast.Node) bool {
		if node == nil {
			ancestors = ancestors[:len(ancestors)-1]
			return true
		}
		ancestors = append(ancestors, node)
		if assign, ok := node.(*ast.AssignStmt); ok {
			for _, left := range assign.Lhs {
				if id, ok := left.(*ast.Ident); ok && id.Obj != nil && fn.tree.Scope.Lookup(id.Name) == id.Obj {
					row.Observations = append(row.Observations, EvidenceFact{Category: "package-state-write", Provenance: "static", Summary: factText(fn.fset, assign), Evidence: []Citation{fn.citation(assign)}})
				}
			}
		}
		if loop, ok := node.(*ast.RangeStmt); ok {
			expr := loop.X
			if name, ok := expr.(*ast.Ident); ok && name.Obj != nil {
				switch decl := name.Obj.Decl.(type) {
				case *ast.AssignStmt:
					if len(decl.Rhs) == 1 {
						expr = decl.Rhs[0]
					}
				case *ast.ValueSpec:
					if len(decl.Values) == 1 {
						expr = decl.Values[0]
					}
				}
			}
			if table, ok := expr.(*ast.CompositeLit); ok {
				for n, element := range table.Elts {
					values := map[string]string{}
					label := fmt.Sprintf("literal case %d", n+1)
					if tuple, ok := element.(*ast.CompositeLit); ok {
						for position, value := range tuple.Elts {
							key := fmt.Sprint(position)
							expression := value
							if field, ok := value.(*ast.KeyValueExpr); ok {
								key = factText(fn.fset, field.Key)
								expression = field.Value
							}
							values[key] = factText(fn.fset, expression)
							if literal, ok := expression.(*ast.BasicLit); ok && literal.Kind == token.STRING && (key == "name" || position == 0) {
								if decoded, err := strconv.Unquote(literal.Value); err == nil {
									label = decoded
								}
							}
						}
					} else {
						values["expression"] = factText(fn.fset, element)
					}
					row.Cases = append(row.Cases, CaseFact{Label: label, Values: values, Evidence: fn.citation(element)})
				}
			} else {
				row.Limitations = append(row.Limitations, "dynamic-case-data: "+factText(fn.fset, loop.X)+"; generators are not executed.")
			}
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if len(row.Predicates) < 64 {
			addPredicates(index.callPredicates(fn, call, ancestors, writes))
		} else {
			predicateLimited = true
		}
		if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
			name := selector.Sel.Name
			if isTesting(selector.X) {
				switch name {
				case "Fatal", "Fatalf", "Error", "Errorf", "Fail", "FailNow":
					check := CheckFact{Kind: "unconditional-failure", Expressions: []string{factText(fn.fset, call)}, Evidence: fn.citation(call)}
					for n := len(ancestors) - 1; n >= 0; n-- {
						condition, ok := ancestors[n].(*ast.IfStmt)
						if !ok || call.Pos() < condition.Body.Pos() || call.End() > condition.Body.End() {
							continue
						}
						check.Kind = "conditional-failure"
						check.Expressions = []string{factText(fn.fset, condition.Cond)}
						if constantExpression(condition.Cond).Kind() == constant.Bool {
							check.Kind = "constant-ineffective"
						}
						check.Evidence = fn.citation(condition)
						break
					}
					row.Checks = append(row.Checks, check)
				case "Cleanup":
					row.Observations = append(row.Observations, EvidenceFact{Category: "cleanup", Provenance: "static", Summary: factText(fn.fset, call), Evidence: []Citation{fn.citation(call)}})
				case "Run":
					if len(call.Args) > 0 {
						if label, ok := call.Args[0].(*ast.BasicLit); ok && label.Kind == token.STRING {
							text, err := strconv.Unquote(label.Value)
							if err == nil {
								row.Cases = append(row.Cases, CaseFact{Label: text, Evidence: fn.citation(call)})
							}
						}
					}
				}
				return true
			}
			base, identifier := selector.X.(*ast.Ident)
			if identifier && (base.Obj == nil || base.Obj.Kind == ast.Pkg) {
				path := fn.imports[base.Name]
				if path == "time" && name == "Sleep" {
					row.Observations = append(row.Observations, EvidenceFact{Category: "sleep", Provenance: "static", Summary: factText(fn.fset, call), Evidence: []Citation{fn.citation(call)}})
				}
				checkingCall := false
				switch name {
				case "Equal", "EqualValues", "NotEqual", "EqualError", "Error", "NoError", "ErrorIs", "ErrorAs", "True", "False", "Nil", "NotNil", "Contains", "NotContains", "Len", "Empty", "NotEmpty", "Panics", "NotPanics", "Eventually", "Never", "Same", "NotSame", "Greater", "GreaterOrEqual", "Less", "LessOrEqual", "WithinDuration", "InDelta", "InEpsilon", "ElementsMatch", "Subset", "NotSubset", "Zero", "NotZero", "Regexp", "NotRegexp", "JSONEq", "YAMLEq", "Fail", "FailNow":
					checkingCall = true
				}
				if checkingCall && (path == "github.com/stretchr/testify/assert" || path == "github.com/stretchr/testify/require") && len(call.Args) > 0 && isTesting(call.Args[0]) {
					row.Checks = append(row.Checks, CheckFact{Kind: "assertion-call", Expressions: []string{factText(fn.fset, call)}, Evidence: fn.citation(call)})
					row.Limitations = append(row.Limitations, "Assertion-call arguments require semantic review; library implementation is not inspected by this static site.")
					return true
				}
			}
			if name == "Fatal" || name == "Error" || name == "Fail" || name == "Equal" {
				row.Limitations = append(row.Limitations, "Unresolved checking receiver: "+factText(fn.fset, call))
			}
		}
		carriesTesting := false
		for _, arg := range call.Args {
			if isTesting(arg) {
				carriesTesting = true
			}
		}
		if !carriesTesting {
			return true
		}
		name, ok := call.Fun.(*ast.Ident)
		if !ok {
			row.Limitations = append(row.Limitations, "Opaque helper: "+factText(fn.fset, call))
			return true
		}
		matches := index.functions[functionKey(fn.snapshot, fn.pkg, name.Name)]
		if len(matches) != 1 || name.Obj != nil && name.Obj.Kind != ast.Fun {
			row.Limitations = append(row.Limitations, "Dynamic/unresolved helper: "+factText(fn.fset, call))
			return true
		}
		helper := matches[0]
		result := index.inspect(helper, depth+1)
		row.Helpers = append(row.Helpers, helper.citation(helper.node))
		row.Helpers = append(row.Helpers, result.Helpers...)
		row.Checks = append(row.Checks, result.Checks...)
		row.Cases = append(row.Cases, result.Cases...)
		row.Observations = append(row.Observations, result.Observations...)
		row.Limitations = append(row.Limitations, result.Limitations...)
		addPredicates(result.Predicates)
		return true
	})
	if predicateLimited {
		row.Limitations = append(row.Limitations, "Predicate fact budget exhausted; input/condition mapping is incomplete.")
	}
	index.memo[fn] = row
	return row
}
