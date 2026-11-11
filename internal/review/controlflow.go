package review

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"sort"
	"strings"

	"golang.org/x/tools/go/cfg"
)

// ControlFlow is intraprocedural syntax evidence, not an execution trace.
type ControlFlow struct {
	SymbolID    string     `json:"symbolID"`
	Entry       string     `json:"entry"`
	Nodes       []FlowNode `json:"nodes"`
	Edges       []FlowEdge `json:"edges"`
	Limitations []string   `json:"limitations"`
}

type FlowNode struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	// Label is a bounded source summary; Location retains the complete source range.
	Label     string    `json:"label"`
	Location  Location  `json:"location"`
	CallIDs   []string  `json:"callIDs"`
	Comments  []Comment `json:"comments"`
	Reachable bool      `json:"reachable"`
	// StatementBlock marks display-groupable statements in the same original CFG block.
	StatementBlock string `json:"statementBlock,omitempty"`
	// CallOrderKnown permits displaying calls in Go evaluation order within
	// this step. Short-circuit expressions stay conditional, not a flat list.
	CallOrderKnown bool `json:"callOrderKnown"`
}

type FlowEdge struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Label string `json:"label"`
	Kind  string `json:"kind"`
}

func (a *analyzer) collectControlFlows(p *parsedPackage) {
	for _, file := range p.ast {
		ast.Inspect(file, func(n ast.Node) bool {
			var body *ast.BlockStmt
			var id string
			switch n := n.(type) {
			case *ast.FuncDecl:
				body = n.Body
				id = a.symbolID(p.meta.ImportPath, n.Name.Name, n.Pos())
			case *ast.FuncLit:
				body, id = n.Body, a.literals[n]
			}
			if body != nil {
				a.report.ControlFlows = append(a.report.ControlFlows, a.controlFlow(p, file, body, id))
			}
			return true
		})
	}
}

type flowBuilder struct {
	a             *analyzer
	p             *parsedPackage
	body          *ast.BlockStmt
	flow          ControlFlow
	markers       map[ast.Node]ast.Node
	comm          map[ast.Stmt]*ast.CommClause
	first         map[*cfg.Block]string
	last          map[*cfg.Block]string
	astNode       map[string]ast.Node
	commentOwner  map[ast.Node]ast.Node
	typeClauses   map[*ast.CaseClause]bool
	typeCounts    map[*ast.CaseClause]int
	rangeBindings map[ast.Node]bool
}

func (a *analyzer) controlFlow(p *parsedPackage, file *ast.File, body *ast.BlockStmt, id string) (result ControlFlow) {
	b := &flowBuilder{
		a: a, p: p, body: body,
		flow: ControlFlow{SymbolID: id, Nodes: []FlowNode{}, Edges: []FlowEdge{}, Limitations: []string{
			"Static intraprocedural control flow; reachability means a syntactic path from entry, not proof a path can execute.",
			"&& and || short-circuit paths and expression evaluation order are not expanded. Calls annotate containing steps, not an execution sequence.",
			"Only type-resolved builtin panic expression statements terminate here. Other non-returning calls, panics in expressions, deferred execution, recovery and goroutine scheduling are not modeled.",
			"Select alternatives represent nondeterministic readiness, not priority or boolean tests. Channel operands and send values evaluate before selection; selected receive assignments occur afterward.",
			"Range iteration and type-switch matching are abstract tests; no data-flow, values or interprocedural paths are inferred.",
		}},
		markers: map[ast.Node]ast.Node{}, comm: map[ast.Stmt]*ast.CommClause{},
		first: map[*cfg.Block]string{}, last: map[*cfg.Block]string{}, astNode: map[string]ast.Node{},
		commentOwner: map[ast.Node]ast.Node{}, typeClauses: map[*ast.CaseClause]bool{}, typeCounts: map[*ast.CaseClause]int{},
		rangeBindings: map[ast.Node]bool{},
	}
	// Malformed source can reach the CFG library after parser/type diagnostics.
	// Preserve an inspectable entry and report the unsupported graph, not a crash.
	defer func() {
		if err := recover(); err != nil {
			result = ControlFlow{SymbolID: id, Nodes: []FlowNode{}, Edges: []FlowEdge{}, Limitations: append(b.flow.Limitations, fmt.Sprintf("Control flow unavailable for malformed or unsupported syntax: %v", err))}
			entry := FlowNode{ID: stableID("flow", id+":entry"), Kind: "entry", Label: "entry", Location: a.location(body.Lbrace, body.Lbrace+1), Reachable: true, CallIDs: []string{}, Comments: []Comment{}}
			result.Entry, result.Nodes = entry.ID, append(result.Nodes, entry)
		}
	}()
	cloned := b.cloneStmt(body).(*ast.BlockStmt)
	graph := cfg.New(cloned, func(call *ast.CallExpr) bool { return !b.builtinPanic(call) })
	entry := b.add("entry", "entry", body.Lbrace, body.Lbrace+1, true, nil)
	b.flow.Entry = entry
	for _, block := range graph.Blocks {
		b.blockNodes(block)
	}
	exit := b.add("exit", "exit", body.Rbrace, body.Rbrace+1, false, nil)
	b.edge(entry, b.first[graph.Blocks[0]], "", "entry")
	for _, block := range graph.Blocks {
		from := b.last[block]
		if len(block.Succs) == 0 {
			node := b.node(from)
			if node.Kind == "return" || node.Kind == "implicit_return" || node.Kind == "panic" {
				kind := "return"
				if node.Kind == "panic" {
					kind = "panic"
				}
				b.edge(from, exit, kind, kind)
				if block.Live {
					b.node(exit).Reachable = true
				}
			}
		}
		for i, succ := range block.Succs {
			label, kind := b.transfer(block, succ, i)
			b.edge(from, b.first[succ], label, kind)
		}
	}
	b.linkEvidence(file)
	return b.flow
}

func (b *flowBuilder) builtinPanic(call *ast.CallExpr) bool {
	ident, ok := unwrap(call.Fun).(*ast.Ident)
	if !ok {
		return false
	}
	obj, ok := b.p.info.Uses[ident].(*types.Builtin)
	return ok && obj.Name() == "panic"
}

// Branches are omitted by cfg. Inserting a neutral marker before each branch
// retains its source ownership without changing the library's transfer targets.
// Clone statement containers so the original AST remains untouched.
func (b *flowBuilder) cloneStmt(s ast.Stmt) ast.Stmt {
	if s == nil {
		return nil
	}
	list := func(stmts []ast.Stmt) []ast.Stmt {
		out := make([]ast.Stmt, len(stmts))
		for i, stmt := range stmts {
			out[i] = b.cloneStmt(stmt)
		}
		return out
	}
	switch s := s.(type) {
	case *ast.BlockStmt:
		n := *s
		n.List = list(s.List)
		return &n
	case *ast.BranchStmt:
		marker := &ast.EmptyStmt{Semicolon: s.Pos(), Implicit: true}
		b.markers[marker] = s
		return &ast.BlockStmt{Lbrace: s.Pos(), List: []ast.Stmt{marker, s}, Rbrace: s.End()}
	case *ast.IfStmt:
		n := *s
		b.commentOwner[s.Cond] = s
		n.Body, n.Else = b.cloneStmt(s.Body).(*ast.BlockStmt), b.cloneStmt(s.Else)
		return &n
	case *ast.ForStmt:
		n := *s
		if s.Cond != nil {
			b.commentOwner[s.Cond] = s
		}
		n.Body = b.cloneStmt(s.Body).(*ast.BlockStmt)
		return &n
	case *ast.RangeStmt:
		n := *s
		b.commentOwner[s.X] = s
		b.rangeBindings[s.Key] = true
		b.rangeBindings[s.Value] = true
		n.Body = b.cloneStmt(s.Body).(*ast.BlockStmt)
		return &n
	case *ast.LabeledStmt:
		n := *s
		b.commentOwner[s.Label] = s
		n.Stmt = b.cloneStmt(s.Stmt)
		return &n
	case *ast.SwitchStmt:
		n := *s
		if s.Tag != nil {
			b.commentOwner[s.Tag] = s
		}
		n.Body = b.cloneStmt(s.Body).(*ast.BlockStmt)
		return &n
	case *ast.TypeSwitchStmt:
		n := *s
		b.commentOwner[s.Assign] = s
		n.Body = b.cloneStmt(s.Body).(*ast.BlockStmt)
		for _, clause := range n.Body.List {
			b.typeClauses[clause.(*ast.CaseClause)] = true
		}
		return &n
	case *ast.SelectStmt:
		n := *s
		n.Body = b.cloneStmt(s.Body).(*ast.BlockStmt)
		marker := &ast.EmptyStmt{Semicolon: s.Select, Implicit: true}
		b.markers[marker] = s
		return &ast.BlockStmt{Lbrace: s.Pos(), List: []ast.Stmt{marker, &n}, Rbrace: s.End()}
	case *ast.CaseClause:
		n := *s
		n.Body = list(s.Body)
		return &n
	case *ast.CommClause:
		n := *s
		n.Body = list(s.Body)
		if n.Comm != nil {
			b.comm[n.Comm] = &n
		} else {
			marker := &ast.EmptyStmt{Semicolon: s.Case, Implicit: true}
			b.markers[marker] = s
			n.Body = append([]ast.Stmt{marker}, n.Body...)
		}
		return &n
	}
	return s
}

func (b *flowBuilder) add(kind, label string, pos, end token.Pos, live bool, source ast.Node) string {
	id := stableID("flow", fmt.Sprintf("%s:%d", b.flow.SymbolID, len(b.flow.Nodes)))
	runes := []rune(strings.TrimSpace(label))
	if len(runes) > 240 {
		label = string(runes[:237]) + "..."
	} else {
		label = string(runes)
	}
	n := FlowNode{ID: id, Kind: kind, Label: label, Location: b.a.location(pos, end), Reachable: live, CallIDs: []string{}, Comments: []Comment{}}
	b.flow.Nodes = append(b.flow.Nodes, n)
	if source != nil {
		b.astNode[id] = source
	}
	return id
}

func (b *flowBuilder) node(id string) *FlowNode {
	for i := range b.flow.Nodes {
		if b.flow.Nodes[i].ID == id {
			return &b.flow.Nodes[i]
		}
	}
	panic("unknown flow node")
}

func (b *flowBuilder) edge(from, to, label, kind string) {
	b.flow.Edges = append(b.flow.Edges, FlowEdge{From: from, To: to, Label: label, Kind: kind})
}

func (b *flowBuilder) blockNodes(block *cfg.Block) {
	var ids []string
	addAST := func(n ast.Node, kind, label string) {
		id := b.add(kind, label, n.Pos(), n.End(), block.Live, n)
		ids = append(ids, id)
		if kind == "statement" && simpleFlowStatement(n) {
			b.node(id).StatementBlock = fmt.Sprintf("block:%d", block.Index)
		}
	}
	if s, ok := block.Stmt.(*ast.LabeledStmt); ok && block.Kind == cfg.KindLabel {
		ids = append(ids, b.add("label", s.Label.Name+":", s.Label.Pos(), s.Colon+1, block.Live, s.Label))
	}
	// cfg evaluates entire select communications before choosing a case. Replace
	// those with the channel/send operands mandated by Go, then retain the
	// communication itself only in the selected case body.
	if cc, ok := block.Stmt.(*ast.CommClause); ok && block.Kind == cfg.KindSelectCaseBody {
		addAST(cc.Comm, "communication", b.a.text(cc.Comm))
	}
	if s, ok := block.Stmt.(*ast.RangeStmt); ok && block.Kind == cfg.KindRangeBody {
		for _, lhs := range []ast.Expr{s.Key, s.Value} {
			if lhs != nil {
				addAST(lhs, "range_assignment", "range bind: "+b.a.text(lhs))
			}
		}
	}
	for _, original := range block.Nodes {
		n := original
		if b.rangeBindings[n] {
			continue
		}
		if marker := b.markers[n]; marker != nil {
			n = marker
		}
		if stmt, ok := n.(ast.Stmt); ok && b.comm[stmt] != nil {
			for _, operand := range selectOperands(stmt) {
				addAST(operand, "select_operand", b.a.text(operand))
			}
			continue
		}
		if cc, ok := block.Stmt.(*ast.CommClause); ok && block.Kind == cfg.KindSelectCaseBody {
			if assign, ok := cc.Comm.(*ast.AssignStmt); ok && len(assign.Lhs) > 0 && n == assign.Lhs[0] {
				continue
			}
		}
		kind, label := flowKind(n), b.a.text(n)
		switch n := n.(type) {
		case *ast.SelectStmt:
			kind, label := "select", "select"
			if len(n.Body.List) == 0 {
				kind, label = "blocked", "select {} blocks permanently"
			}
			ids = append(ids, b.add(kind, label, n.Select, n.Select+6, block.Live, n))
			continue
		case *ast.CommClause:
			ids = append(ids, b.add("select_default", "default", n.Case, n.Colon+1, block.Live, nil))
			continue
		case *ast.ReturnStmt:
			if n.Pos() == b.body.Rbrace {
				ids = append(ids, b.add("implicit_return", "implicit return", b.body.Rbrace, b.body.Rbrace+1, block.Live, nil))
				continue
			}
		case *ast.ExprStmt:
			if call, ok := n.X.(*ast.CallExpr); ok && b.builtinPanic(call) {
				kind = "panic"
			}
		}
		addAST(n, kind, label)
	}
	if len(block.Succs) == 2 {
		succ := block.Succs[0]
		if cc, ok := succ.Stmt.(*ast.CommClause); ok && succ.Kind == cfg.KindSelectCaseBody {
			ids = append(ids, b.add("select_case", "select case: "+b.a.text(cc.Comm), cc.Pos(), cc.Colon+1, block.Live, nil))
		} else if cc, ok := succ.Stmt.(*ast.CaseClause); ok && succ.Kind == cfg.KindSwitchCaseBody {
			// Type case tests have no AST node in cfg; value cases already do.
			if b.typeClauses[cc] {
				expr := cc.List[b.typeCounts[cc]]
				ids = append(ids, b.add("type_case", "type matches "+b.a.text(expr), expr.Pos(), expr.End(), block.Live, nil))
				b.typeCounts[cc]++
			}
		} else if block.Kind == cfg.KindRangeLoop {
			s := block.Stmt.(*ast.RangeStmt)
			ids = append(ids, b.add("range", "next range iteration", s.For, s.For+5, block.Live, nil))
		}
	}
	if len(ids) == 0 {
		pos := b.body.Lbrace
		label, kind := block.Kind.String(), "block"
		if block.Stmt != nil {
			pos = block.Stmt.Pos()
		}
		if block.Kind == cfg.KindSelectAfterCase && len(block.Succs) == 0 {
			label, kind = "block until a communication is ready", "blocked"
		}
		ids = append(ids, b.add(kind, label, pos, pos, block.Live, nil))
	}
	for i := 1; i < len(ids); i++ {
		if b.node(ids[i]).Kind == "select_default" {
			b.edge(ids[i-1], ids[i], "default", "select")
		} else {
			b.edge(ids[i-1], ids[i], "", "sequence")
		}
	}
	b.first[block], b.last[block] = ids[0], ids[len(ids)-1]
}

func simpleFlowStatement(n ast.Node) bool {
	switch s := n.(type) {
	case *ast.AssignStmt:
		for _, lhs := range s.Lhs {
			if _, ok := lhs.(*ast.Ident); !ok {
				return false
			}
		}
	case *ast.DeclStmt:
		decl, ok := s.Decl.(*ast.GenDecl)
		if !ok || (decl.Tok != token.VAR && decl.Tok != token.CONST) {
			return false
		}
	case *ast.ValueSpec:
		// x/tools/go/cfg emits each var declaration spec as a separate step.
	default:
		return false
	}
	simple := true
	ast.Inspect(n, func(node ast.Node) bool {
		switch v := node.(type) {
		case *ast.CallExpr, *ast.FuncLit:
			simple = false
		case *ast.UnaryExpr:
			simple = simple && v.Op != token.ARROW
		case *ast.BinaryExpr:
			simple = simple && v.Op != token.LAND && v.Op != token.LOR
		}
		return simple
	})
	return simple
}

func selectOperands(stmt ast.Stmt) []ast.Expr {
	switch s := stmt.(type) {
	case *ast.SendStmt:
		return []ast.Expr{s.Chan, s.Value}
	case *ast.ExprStmt:
		if recv, ok := s.X.(*ast.UnaryExpr); ok && recv.Op == token.ARROW {
			return []ast.Expr{recv.X}
		}
	case *ast.AssignStmt:
		if len(s.Rhs) == 1 {
			if recv, ok := s.Rhs[0].(*ast.UnaryExpr); ok && recv.Op == token.ARROW {
				return []ast.Expr{recv.X}
			}
		}
	}
	return nil
}

func flowKind(n ast.Node) string {
	switch n := n.(type) {
	case *ast.ReturnStmt:
		return "return"
	case *ast.BranchStmt:
		return n.Tok.String()
	case *ast.GoStmt:
		return "go"
	case *ast.DeferStmt:
		return "defer"
	case ast.Expr:
		return "expression"
	default:
		return "statement"
	}
}

func (b *flowBuilder) transfer(block, succ *cfg.Block, index int) (string, string) {
	n := b.node(b.last[block])
	switch n.Kind {
	case "break", "continue", "goto", "fallthrough":
		return n.Kind, n.Kind
	}
	if len(block.Succs) == 2 {
		switch succ0 := block.Succs[0]; succ0.Kind {
		case cfg.KindSelectCaseBody:
			if index == 0 {
				return "ready: " + b.a.text(succ0.Stmt.(*ast.CommClause).Comm), "select"
			}
			if b.node(b.first[succ]).Kind == "select_default" {
				return "default", "select"
			}
			return "other alternative / wait", "select"
		case cfg.KindSwitchCaseBody:
			if index == 0 {
				return "match", "case"
			}
			return "no match", "case"
		case cfg.KindRangeBody:
			if index == 0 {
				return "next", "loop"
			}
			return "done", "loop"
		default:
			if index == 0 {
				return "true", "branch"
			}
			return "false", "branch"
		}
	}
	if succ.Kind == cfg.KindForLoop || succ.Kind == cfg.KindRangeLoop || succ.Kind == cfg.KindForPost {
		return "loop", "loop"
	}
	if loop, ok := succ.Stmt.(*ast.ForStmt); ok && succ.Kind == cfg.KindForBody && loop.Cond == nil {
		return "loop", "loop"
	}
	if b.node(b.first[succ]).Kind == "select_default" {
		return "default", "select"
	}
	if cc, ok := succ.Stmt.(*ast.CaseClause); ok && cc.List == nil {
		return "default", "case"
	}
	return "", "sequence"
}

func (b *flowBuilder) linkEvidence(file *ast.File) {
	// Containment plus owner identity excludes closure bodies. Each call links
	// to one smallest containing executable step, never a guessed call order.
	for _, call := range b.a.report.Calls {
		if call.From != b.flow.SymbolID {
			continue
		}
		best := -1
		for i := range b.flow.Nodes {
			n := &b.flow.Nodes[i]
			source := b.astNode[n.ID]
			if source == nil || n.Location.FileID != call.FileID || n.Location.Offset > call.Offset || n.Location.EndOffset < call.EndOffset {
				continue
			}
			if n.Kind == "communication" {
				// Channels and send values were already evaluated before
				// selection; only receive assignment LHS executes here.
				assign, ok := source.(*ast.AssignStmt)
				lhsCall := false
				if ok {
					for _, lhs := range assign.Lhs {
						loc := b.a.location(lhs.Pos(), lhs.End())
						lhsCall = lhsCall || (loc.Offset <= call.Offset && loc.EndOffset >= call.EndOffset)
					}
				}
				if !lhsCall {
					continue
				}
			}
			if best < 0 || n.Location.EndOffset-n.Location.Offset < b.flow.Nodes[best].Location.EndOffset-b.flow.Nodes[best].Location.Offset {
				best = i
			}
		}
		if best >= 0 {
			b.flow.Nodes[best].CallIDs = append(b.flow.Nodes[best].CallIDs, call.ID)
		}
	}
	commentMap := ast.NewCommentMap(b.a.fset, b.body, file.Comments)
	for i := range b.flow.Nodes {
		node := &b.flow.Nodes[i]
		owner := b.astNode[node.ID]
		if owner != nil && len(node.CallIDs) > 0 {
			node.CallOrderKnown = true
			ast.Inspect(owner, func(n ast.Node) bool {
				if _, ok := n.(*ast.FuncLit); ok {
					return false
				}
				if expr, ok := n.(*ast.BinaryExpr); ok && (expr.Op == token.LAND || expr.Op == token.LOR) {
					node.CallOrderKnown = false
				}
				return true
			})
		}
		if control := b.commentOwner[owner]; control != nil {
			owner = control
		}
		for _, group := range commentMap[owner] {
			if group.Pos() < b.body.Pos() || group.End() > b.body.End() {
				continue
			}
			node.Comments = append(node.Comments, Comment{Text: group.Text(), Location: b.a.location(group.Pos(), group.End())})
		}
		sort.Slice(node.CallIDs, func(i, j int) bool { return node.CallIDs[i] < node.CallIDs[j] })
	}
}
