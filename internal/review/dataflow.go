package review

import (
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
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
	Commands     []Command  `json:"commands,omitempty"`
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

// AnalyzeDataFlow reads initial-review evidence only. Legacy snapshots require
// a new explicit review; browsing never consults or executes the live checkout.
func AnalyzeDataFlow(ctx context.Context, snapshot *Report, symbolID string) (*DataFlow, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if snapshot == nil {
		return nil, fmt.Errorf("data flow: snapshot is required")
	}
	found := false
	for _, symbol := range snapshot.Symbols {
		if symbol.ID == symbolID {
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("data flow: unknown symbol %q", symbolID)
	}
	if snapshot.DataFlows == nil {
		return nil, fmt.Errorf("data flow: not collected in this review; run a new explicit review")
	}
	if snapshot.DataFlows.SourceSHA256 != snapshot.SourceSHA256 {
		return nil, fmt.Errorf("data flow: retained evidence does not match snapshot")
	}
	for _, result := range snapshot.DataFlows.Results {
		if result.SymbolID != symbolID {
			continue
		}
		if result.Status != "available" {
			return nil, fmt.Errorf("data flow: %s: %s", result.Status, result.Error)
		}
		if result.Graph == nil || result.Graph.SymbolID != symbolID || result.Graph.SourceSHA256 != snapshot.SourceSHA256 {
			return nil, fmt.Errorf("data flow: invalid retained graph identity")
		}
		graph := *result.Graph
		graph.Commands = snapshot.DataFlows.Commands
		return &graph, nil
	}
	return nil, fmt.Errorf("data flow: unsupported symbol; no retained function result")
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

func buildDataFunction(a *analyzer, p *parsedPackage, selected *Symbol, prog *ssa.Program) (result *ssa.Function, err error) {
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
		"Collected during initial review from the same parsed/type-checked package sources. Dependency export data is from that review's build environment; source collection is not a filesystem transaction.",
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
