package review

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"golang.org/x/tools/go/ssa"
)

// DataFlow is bounded, intraprocedural SSA operand evidence for one snapshot symbol.
// Edges are direct definitions consumed by instructions, not transitive taint.
type DataFlow struct {
	SymbolID     string     `json:"symbolID"`
	SourceSHA256 string     `json:"sourceSHA256"`
	Nodes        []DataNode `json:"nodes"`
	Edges        []DataEdge `json:"edges"`
	Limitations  []string   `json:"limitations"`
	Collector    string     `json:"collector"`
	ToolVersion  string     `json:"toolVersion"`
	GeneratedAt  time.Time  `json:"generatedAt"`
	Commands     []Command  `json:"commands"`
}

type DataNode struct {
	ID       string    `json:"id"`
	Kind     string    `json:"kind"`
	Label    string    `json:"label"`
	Type     string    `json:"type"`
	Location *Location `json:"location,omitempty"`
	Boundary string    `json:"boundary,omitempty"`
	CalleeID string    `json:"calleeID,omitempty"`
}

type DataEdge struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Label string `json:"label"`
}

// AnalyzeDataFlow never mutates snapshot or executes target tests. It checks
// snapshot sources and manifests before and after read-only Go tooling, then
// builds package SSA from the verified embedded source. The checks are best
// effort, not a filesystem transaction; dependency export data is current-build.
func AnalyzeDataFlow(ctx context.Context, snapshot *Report, symbolID string) (*DataFlow, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if snapshot == nil {
		return nil, fmt.Errorf("data flow: snapshot is required")
	}
	var selected *Symbol
	for i := range snapshot.Symbols {
		if snapshot.Symbols[i].ID == symbolID {
			selected = &snapshot.Symbols[i]
			break
		}
	}
	if selected == nil {
		return nil, fmt.Errorf("data flow: unknown symbol %q", symbolID)
	}
	if selected.Kind != "function" && selected.Kind != "method" && selected.Kind != "closure" {
		return nil, fmt.Errorf("data flow: unsupported symbol kind %q", selected.Kind)
	}
	var source *File
	for i := range snapshot.Files {
		if snapshot.Files[i].ID == selected.FileID {
			source = &snapshot.Files[i]
			break
		}
	}
	if source == nil || source.Package != selected.Package {
		return nil, fmt.Errorf("data flow: selected source ownership is unavailable")
	}
	if strings.HasSuffix(source.Path, "_test.go") {
		return nil, fmt.Errorf("data flow: unsupported test function; test variants are not loaded")
	}
	if err := verifyDataSources(ctx, snapshot); err != nil {
		return nil, err
	}
	for _, p := range snapshot.Packages {
		if p.Path == selected.Package && len(p.TypeErrors) > 0 {
			return nil, fmt.Errorf("data flow: type checking unavailable: %s", strings.Join(p.TypeErrors, "; "))
		}
	}
	a, p, err := loadDataPackage(ctx, snapshot, source)
	if err != nil {
		return nil, err
	}
	fn, err := buildDataFunction(a, p, selected)
	if err != nil {
		return nil, err
	}
	result, err := collectDataFlow(ctx, a, fn, selected, snapshot)
	if err != nil {
		return nil, err
	}
	if err := verifyDataSources(ctx, snapshot); err != nil {
		return nil, err
	}
	result.Collector = "golang.org/x/tools/go/ssa"
	result.ToolVersion = dataToolVersion()
	result.GeneratedAt = time.Now().UTC()
	result.Commands = a.report.Commands
	return result, nil
}

func dataToolVersion() string {
	// Tests may omit dependency build metadata; retain the compiled dependency
	// pin explicitly rather than imply it was discovered from a target module.
	version := "v0.42.0 (compiled pin; build metadata unavailable)"
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, dep := range info.Deps {
			if dep.Path == "golang.org/x/tools" {
				version = dep.Version
				if dep.Replace != nil {
					version += " replaced by " + dep.Replace.Path + " " + dep.Replace.Version
				}
				break
			}
		}
	}
	return "golang.org/x/tools " + version + "; " + runtime.Version()
}

func verifyDataSources(ctx context.Context, r *Report) error {
	sources := append(append([]File{}, r.Files...), r.SupportingFiles...)
	for _, testSource := range r.TestSources {
		sources = append(sources, testSource.File)
	}
	for _, f := range sources {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := filepath.Join(r.Root, filepath.FromSlash(f.Path))
		if !inside(r.Root, name) || digest([]byte(f.Source)) != f.SHA256 {
			return fmt.Errorf("data flow: invalid snapshot source %s", f.Path)
		}
		data, err := os.ReadFile(name)
		if err != nil {
			return fmt.Errorf("data flow: source changed or unavailable %s: %w", f.Path, err)
		}
		if digest(data) != f.SHA256 {
			return fmt.Errorf("data flow: source changed since snapshot: %s", f.Path)
		}
	}
	for _, in := range r.Inputs {
		name := filepath.Join(r.Root, filepath.FromSlash(in.Path))
		if !inside(r.Root, name) {
			return fmt.Errorf("data flow: invalid manifest path %s", in.Path)
		}
		data, err := os.ReadFile(name)
		if err != nil || digest(data) != in.SHA256 {
			return fmt.Errorf("data flow: manifest changed or unavailable: %s", in.Path)
		}
	}
	return nil
}

func loadDataPackage(ctx context.Context, snapshot *Report, source *File) (*analyzer, *parsedPackage, error) {
	// Command provenance belongs to this request, not the published snapshot.
	scratch := &Report{Environment: snapshot.Environment}
	dir := filepath.Dir(filepath.Join(snapshot.Root, filepath.FromSlash(source.Path)))
	envData, err := runGo(ctx, scratch, dir, false, "env", "-json", "GOOS", "GOARCH", "CGO_ENABLED", "GOFLAGS", "GOWORK", "GOVERSION", "GOTOOLCHAIN", "GOMOD")
	if ctx.Err() != nil {
		return nil, nil, ctx.Err()
	}
	if err != nil {
		return nil, nil, fmt.Errorf("data flow: build environment unavailable: %w", err)
	}
	var environment map[string]string
	if err := json.Unmarshal(envData, &environment); err != nil {
		return nil, nil, fmt.Errorf("data flow: decode build environment: %w", err)
	}
	for _, key := range []string{"GOOS", "GOARCH", "CGO_ENABLED", "GOFLAGS", "GOWORK", "GOVERSION", "GOTOOLCHAIN", "GOMOD"} {
		if previous, ok := snapshot.Environment[key]; ok && previous != environment[key] {
			return nil, nil, fmt.Errorf("data flow: build environment changed since snapshot: %s", key)
		}
	}
	scratch.Environment = environment
	data, err := runGo(ctx, scratch, dir, false, "list", "-mod=readonly", "-deps", "-export", "-json", ".")
	if ctx.Err() != nil {
		return nil, nil, ctx.Err()
	}
	if err != nil {
		return nil, nil, fmt.Errorf("data flow: type checking/build unavailable: %w", err)
	}
	a := &analyzer{report: scratch, fset: token.NewFileSet(), packages: map[string]*parsedPackage{}, fileIDs: map[string]string{}, exports: map[string]string{}}
	var target *parsedPackage
	dec := json.NewDecoder(bytes.NewReader(data))
	for {
		var meta listedPackage
		if err := dec.Decode(&meta); err == io.EOF {
			break
		} else if err != nil {
			return nil, nil, fmt.Errorf("data flow: decode build metadata: %w", err)
		}
		if meta.Export != "" {
			a.exports[meta.ImportPath] = meta.Export
		}
		if meta.ImportPath == source.Package {
			target = &parsedPackage{meta: meta}
		}
	}
	if target == nil {
		return nil, nil, fmt.Errorf("data flow: selected package is unavailable in current build")
	}
	if len(target.meta.CgoFiles) > 0 {
		return nil, nil, fmt.Errorf("data flow: unsupported cgo package without generated bindings")
	}
	files := map[string]File{}
	for _, f := range snapshot.Files {
		if f.Package == source.Package && !strings.HasSuffix(f.Path, "_test.go") {
			files[filepath.Clean(filepath.Join(snapshot.Root, filepath.FromSlash(f.Path)))] = f
		}
	}
	for _, f := range snapshot.SupportingFiles {
		if f.Package == source.Package && !strings.HasSuffix(f.Path, "_test.go") {
			files[filepath.Clean(filepath.Join(snapshot.Root, filepath.FromSlash(f.Path)))] = f
		}
	}
	if len(files) != len(target.meta.GoFiles) {
		return nil, nil, fmt.Errorf("data flow: package source set changed since snapshot")
	}
	for _, name := range target.meta.GoFiles {
		abs := filepath.Clean(filepath.Join(target.meta.Dir, name))
		f, ok := files[abs]
		if !ok {
			return nil, nil, fmt.Errorf("data flow: package source set changed: %s", name)
		}
		parsed, err := parser.ParseFile(a.fset, abs, f.Source, 0)
		if err != nil {
			return nil, nil, fmt.Errorf("data flow: parse source: %w", err)
		}
		target.ast = append(target.ast, parsed)
		a.fileIDs[abs] = f.ID
	}
	a.packages[source.Package] = target
	a.fallback = importer.ForCompiler(a.fset, "gc", func(path string) (io.ReadCloser, error) {
		if name := a.exports[path]; name != "" {
			return os.Open(name)
		}
		return nil, fmt.Errorf("no build export data for %s", path)
	})
	// SSA needs implicit type-switch bindings, scopes and per-file language
	// versions, beyond the symbol collector's intentionally smaller type info.
	target.info = &types.Info{
		Types:        map[ast.Expr]types.TypeAndValue{},
		Defs:         map[*ast.Ident]types.Object{},
		Uses:         map[*ast.Ident]types.Object{},
		Implicits:    map[ast.Node]types.Object{},
		Selections:   map[*ast.SelectorExpr]*types.Selection{},
		Instances:    map[*ast.Ident]types.Instance{},
		Scopes:       map[ast.Node]*types.Scope{},
		FileVersions: map[*ast.File]string{},
	}
	config := types.Config{Importer: a, Sizes: types.SizesFor("gc", environment["GOARCH"])}
	if target.meta.Module != nil && target.meta.Module.GoVersion != "" {
		config.GoVersion = "go" + target.meta.Module.GoVersion
	}
	target.pkg, err = config.Check(source.Package, a.fset, target.ast, target.info)
	if err != nil {
		return nil, nil, fmt.Errorf("data flow: type checking unavailable: %w", err)
	}
	return a, target, nil
}

func buildDataFunction(a *analyzer, p *parsedPackage, selected *Symbol) (result *ssa.Function, err error) {
	// Unsupported library/build combinations must fail visibly, never publish a
	// partial graph that looks like successful analysis.
	defer func() {
		if recovered := recover(); recovered != nil {
			result = nil
			err = fmt.Errorf("data flow: SSA build unavailable for this source/toolchain: %v", recovered)
		}
	}()
	var owner *ast.FuncDecl
	for _, file := range p.ast {
		if a.fileIDs[a.fset.PositionFor(file.Pos(), false).Filename] != selected.FileID {
			continue
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if ok && selected.Offset >= a.fset.PositionFor(fn.Pos(), false).Offset && selected.EndOffset <= a.fset.PositionFor(fn.End(), false).Offset {
				owner = fn
				break
			}
		}
	}
	if owner == nil || owner.Body == nil {
		return nil, fmt.Errorf("data flow: unsupported function without a source body")
	}
	obj, ok := p.info.Defs[owner.Name].(*types.Func)
	if !ok {
		return nil, fmt.Errorf("data flow: function type information unavailable")
	}
	sig := obj.Type().(*types.Signature)
	if sig.TypeParams().Len() > 0 || sig.RecvTypeParams().Len() > 0 {
		return nil, fmt.Errorf("data flow: unsupported generic function or receiver")
	}
	prog := ssa.NewProgram(a.fset, ssa.SanityCheckFunctions)
	var dependencies func(*types.Package)
	dependencies = func(pkg *types.Package) {
		if prog.Package(pkg) != nil {
			return
		}
		for _, dep := range pkg.Imports() {
			dependencies(dep)
		}
		prog.CreatePackage(pkg, nil, nil, true)
	}
	for _, dep := range p.pkg.Imports() {
		dependencies(dep)
	}
	pkg := prog.CreatePackage(p.pkg, p.ast, p.info, true)
	pkg.Build()
	var find func(*ssa.Function) *ssa.Function
	find = func(fn *ssa.Function) *ssa.Function {
		if fn == nil {
			return nil
		}
		if syntax := fn.Syntax(); syntax != nil {
			loc := a.location(syntax.Pos(), syntax.End())
			if loc.FileID == selected.FileID && loc.Offset == selected.Offset && loc.EndOffset == selected.EndOffset {
				return fn
			}
		}
		for _, child := range fn.AnonFuncs {
			if found := find(child); found != nil {
				return found
			}
		}
		return nil
	}
	fn := find(prog.FuncValue(obj))
	if fn == nil || fn.Blocks == nil {
		return nil, fmt.Errorf("data flow: unsupported function; no matching SSA source body")
	}
	return fn, nil
}

func collectDataFlow(ctx context.Context, a *analyzer, fn *ssa.Function, symbol *Symbol, snapshot *Report) (*DataFlow, error) {
	flow := &DataFlow{SymbolID: symbol.ID, SourceSHA256: snapshot.SourceSHA256, Nodes: []DataNode{}, Edges: []DataEdge{}, Limitations: []string{
		"Within one function only. Edges are direct SSA operand definitions, not aliases or transitive taint; no source-wide or cross-package flow is claimed.",
		"Phi branch merges represent potential incoming definitions, not proof of an executable path or runtime values.",
		"Calls are opaque: arguments and the call result share an instruction, but no argument-to-return or interprocedural propagation is inferred. Dynamic call targets are unknown.",
		"Pointer aliasing and memory state are unmodeled. Loads/stores, field/index addresses, maps and channels are explicit memory boundaries; no store-to-load edges are inferred.",
		"No runtime values, target execution, or target tests. SSA may optimize away source expressions; compiler-generated instructions may have no source location.",
		"Sources/manifests are checked before and after collection, not locked. Dependency export data is current-build, not historical snapshot evidence.",
	}}
	values := map[ssa.Value]string{}
	instructions := map[ssa.Instruction]string{}
	constants := map[string]string{}
	add := func(key, kind, label, typ, boundary string, pos token.Pos) string {
		id := stableID("data", symbol.ID+":"+key)
		n := DataNode{ID: id, Kind: kind, Label: label, Type: typ, Boundary: boundary}
		if len(n.Label) > 500 {
			n.Label = n.Label[:500] + "..."
		}
		if pos.IsValid() && a.fset.File(pos) != nil {
			loc := a.location(pos, pos)
			if loc.FileID == symbol.FileID && loc.Offset >= symbol.Offset && loc.Offset < symbol.EndOffset {
				n.Location = &loc
			}
		}
		flow.Nodes = append(flow.Nodes, n)
		return id
	}
	typeName := func(v ssa.Value) string { return types.TypeString(v.Type(), nil) }
	for i, param := range fn.Params {
		values[param] = add(fmt.Sprintf("parameter:%d", i), "parameter", param.Name(), typeName(param), "", param.Pos())
	}
	for i, free := range fn.FreeVars {
		values[free] = add(fmt.Sprintf("freevar:%d", i), "freevar", free.Name(), typeName(free), "closure capture; enclosing function/memory state is not modeled", token.NoPos)
	}
	for _, block := range fn.Blocks {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for i, instr := range block.Instrs {
			if len(flow.Nodes) >= 10000 {
				return nil, fmt.Errorf("data flow: function exceeds 10000-node bound")
			}
			kind, boundary := dataInstructionKind(instr)
			typ, label := "", instr.String()
			if value, ok := instr.(ssa.Value); ok {
				typ = typeName(value)
				label = value.Name() + " = " + label
			}
			id := add(fmt.Sprintf("block:%d:instruction:%d", block.Index, i), kind, label, typ, boundary, instr.Pos())
			if call, ok := instr.(ssa.CallInstruction); ok {
				flow.Nodes[len(flow.Nodes)-1].CalleeID = dataCalleeID(a, call.Common().StaticCallee(), snapshot)
			}
			instructions[instr] = id
			if value, ok := instr.(ssa.Value); ok {
				values[value] = id
			}
		}
	}
	resolve := func(value ssa.Value) string {
		if id := values[value]; id != "" {
			return id
		}
		kind, boundary := "boundary", "cross-function definition; body/state not modeled"
		key := ""
		if c, ok := value.(*ssa.Const); ok {
			kind, boundary = "constant", ""
			constantValue := "nil"
			if c.Value != nil {
				constantValue = c.Value.String()
			}
			key = typeName(c) + ":" + constantValue
			if id := constants[key]; id != "" {
				values[value] = id
				return id
			}
		} else {
			switch value.(type) {
			case *ssa.Global:
				kind, boundary = "memory", "global address; memory state/aliasing not modeled"
			case *ssa.Function, *ssa.Builtin:
				kind, boundary = "function", "opaque function reference; no interprocedural flow"
			}
		}
		id := add(fmt.Sprintf("operand:%d", len(flow.Nodes)), kind, value.String(), typeName(value), boundary, token.NoPos)
		values[value] = id
		if kind == "constant" {
			constants[key] = id
		}
		return id
	}
	for _, block := range fn.Blocks {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for _, instr := range block.Instrs {
			for i, operand := range instr.Operands(nil) {
				if operand == nil || *operand == nil {
					continue
				}
				if len(flow.Nodes) >= 10000 || len(flow.Edges) >= 50000 {
					return nil, fmt.Errorf("data flow: function exceeds graph size bound")
				}
				flow.Edges = append(flow.Edges, DataEdge{From: resolve(*operand), To: instructions[instr], Label: dataOperandLabel(instr, i)})
			}
		}
	}
	return flow, nil
}

// Callee links identify declarations only; they never add cross-function data edges.
func dataCalleeID(a *analyzer, target *ssa.Function, snapshot *Report) string {
	if target == nil {
		return ""
	}
	if syntax := target.Syntax(); syntax != nil {
		loc := a.location(syntax.Pos(), syntax.End())
		for _, s := range snapshot.Symbols {
			if s.FileID == loc.FileID && s.Offset == loc.Offset && s.EndOffset == loc.EndOffset &&
				(s.Kind == "function" || s.Kind == "method" || s.Kind == "closure") {
				return s.ID
			}
		}
	}
	// Imported declarations have no local AST. Match the fully qualified typed
	// declaration, including receiver and signature, never an unqualified name.
	if obj := target.Object(); obj != nil && obj.Pkg() != nil {
		qualified := types.ObjectString(obj, func(p *types.Package) string { return p.Path() })
		for _, s := range snapshot.Symbols {
			if s.Package == obj.Pkg().Path() && s.Qualified == qualified &&
				(s.Kind == "function" || s.Kind == "method") {
				return s.ID
			}
		}
	}
	return ""
}

func dataInstructionKind(instr ssa.Instruction) (string, string) {
	switch n := instr.(type) {
	case *ssa.Phi:
		return "phi", ""
	case *ssa.Return:
		return "return", ""
	case ssa.CallInstruction:
		target := n.Common()
		if builtin, ok := target.Value.(*ssa.Builtin); ok {
			switch builtin.Name() {
			case "append", "copy", "delete", "clear", "close", "len", "cap":
				return "call", "opaque builtin call; memory state/aliasing not modeled"
			}
		}
		if target.IsInvoke() || target.StaticCallee() == nil {
			if _, builtin := target.Value.(*ssa.Builtin); !builtin {
				return "call", "opaque call; unknown dynamic target; no interprocedural flow"
			}
		}
		return "call", "opaque call; no interprocedural flow"
	case *ssa.RunDefers:
		return "call", "opaque deferred execution; unknown deferred targets"
	case *ssa.Alloc, *ssa.Store, *ssa.FieldAddr, *ssa.IndexAddr,
		*ssa.Lookup, *ssa.MapUpdate, *ssa.MakeMap, *ssa.MakeChan,
		*ssa.MakeSlice, *ssa.Send, *ssa.Select, *ssa.Range, *ssa.Next,
		*ssa.Slice, *ssa.Index:
		return "memory", "memory state/aliasing not modeled"
	case *ssa.UnOp:
		if n.Op == token.MUL || n.Op == token.ARROW {
			return "memory", "memory state/aliasing not modeled"
		}
	case *ssa.MakeClosure:
		return "operation", "closure creation/captures only; closure body is a separate function"
	}
	return "operation", ""
}

func dataOperandLabel(instr ssa.Instruction, index int) string {
	switch n := instr.(type) {
	case *ssa.Phi:
		if index < len(n.Block().Preds) {
			return fmt.Sprintf("incoming[%d] from block %d", index, n.Block().Preds[index].Index)
		}
	case *ssa.Return:
		return fmt.Sprintf("result[%d]", index)
	case *ssa.Store:
		if index == 0 {
			return "address"
		}
		return "value"
	case ssa.CallInstruction:
		if index == 0 {
			return "callee/receiver"
		}
		return fmt.Sprintf("argument[%d]", index-1)
	case *ssa.BinOp:
		if index == 0 {
			return "left"
		}
		return "right"
	}
	return fmt.Sprintf("operand[%d]", index)
}
