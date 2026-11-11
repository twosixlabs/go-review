package review

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"reflect"
	"strings"
	"testing"
)

func flowFixture(t *testing.T, source string) *Report {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "flow.go", "package fixture\n"+source, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	info := &types.Info{Defs: map[*ast.Ident]types.Object{}, Uses: map[*ast.Ident]types.Object{}, Types: map[ast.Expr]types.TypeAndValue{}, Selections: map[*ast.SelectorExpr]*types.Selection{}}
	conf := types.Config{Error: func(error) {}}
	pkg, _ := conf.Check("fixture", fset, []*ast.File{file}, info)
	r := &Report{Files: []File{{ID: "source", Source: "package fixture\n" + source}}}
	a := &analyzer{report: r, fset: fset, fileIDs: map[string]string{"flow.go": "source"}, objects: map[types.Object]string{}, literals: map[*ast.FuncLit]string{}}
	p := &parsedPackage{meta: listedPackage{ImportPath: "fixture"}, ast: []*ast.File{file}, info: info, pkg: pkg}
	a.collectSymbols(p)
	a.collectEdges(p)
	a.collectControlFlows(p)
	return r
}

func namedFlow(t *testing.T, r *Report, name string) ControlFlow {
	t.Helper()
	s := symbol(t, r, name)
	for _, f := range r.ControlFlows {
		if f.SymbolID == s.ID {
			return f
		}
	}
	t.Fatalf("no control flow for %s", name)
	return ControlFlow{}
}

func flowNode(t *testing.T, f ControlFlow, label string) FlowNode {
	t.Helper()
	for _, n := range f.Nodes {
		if n.Label == label {
			return n
		}
	}
	t.Fatalf("missing node %q: %+v", label, f.Nodes)
	return FlowNode{}
}

func flowHasEdge(f ControlFlow, from, to, label string) bool {
	for _, e := range f.Edges {
		if e.From == from && e.To == to && e.Label == label {
			return true
		}
	}
	return false
}

func TestControlFlowSimpleStatementsRetainBasicBlockOwnership(t *testing.T) {
	r := flowFixture(t, `func call() int { return 1 }
func f(ch chan int, ok bool) int {
	var x int
	x = 1
	y := x + 2
	x = call()
	z := <-ch
	fn := func() int { return 2 }
	flag := ok && x > 0
	if flag { x = 4; y = 5 }
	for i := 0; i < 2; i++ { x = 6; y = 7 }
	_, _, _ = z, fn, y
	return x
}`)
	flow := namedFlow(t, r, "f")
	data, err := json.Marshal(flow)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Nodes []struct{ Label, StatementBlock string }
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	blocks := map[string]string{}
	for _, n := range wire.Nodes {
		blocks[n.Label] = n.StatementBlock
	}
	for _, label := range []string{"x int", "x = 1", "y := x + 2", "x = 4", "y = 5", "x = 6", "y = 7"} {
		if blocks[label] == "" {
			t.Errorf("simple statement not marked: %q", label)
		}
	}
	if blocks["x int"] != blocks["y := x + 2"] {
		t.Error("straight-line statements lost block identity")
	}
	if blocks["x = 4"] == blocks["x = 6"] {
		t.Error("branch and loop body share block identity")
	}
	for _, label := range []string{"x = call()", "z := <-ch", "flag := ok && x > 0", "return x", "i++"} {
		if _, ok := blocks[label]; !ok {
			t.Fatalf("missing boundary fixture %q", label)
		}
		if blocks[label] != "" {
			t.Errorf("boundary marked simple: %q", label)
		}
		for _, n := range wire.Nodes {
			if strings.HasPrefix(n.Label, "fn :=") && n.StatementBlock != "" {
				t.Error("closure creation marked simple")
			}
		}
	}
}

func TestControlFlowIfElseExactAndStable(t *testing.T) {
	src := `func step() {}
func f(ok bool) {
	step()
	if ok {
		step()
		return
	} else {
		step()
	}
	step()
}`
	r := flowFixture(t, src)
	f := namedFlow(t, r, "f")
	cond := flowNode(t, f, "ok")
	var branches []FlowEdge
	for _, e := range f.Edges {
		if e.From == cond.ID {
			branches = append(branches, e)
		}
	}
	if len(branches) != 2 || branches[0].Label != "true" || branches[1].Label != "false" || branches[0].To == branches[1].To {
		t.Fatalf("incorrect conditional edges: %+v", branches)
	}
	ret := flowNode(t, f, "return")
	for _, e := range f.Edges {
		if e.From == ret.ID && e.Kind != "return" {
			t.Fatalf("return falls through: %+v", e)
		}
	}
	if f.Entry == "" || !reflect.DeepEqual(r.ControlFlows, flowFixture(t, src).ControlFlows) {
		t.Fatal("missing entry or unstable graph")
	}
	data, _ := json.Marshal(r)
	var round Report
	if err := json.Unmarshal(data, &round); err != nil || !reflect.DeepEqual(round.ControlFlows, r.ControlFlows) {
		t.Fatal("control-flow JSON round trip failed")
	}
}

func TestControlFlowLoopsBranchesSwitchSelectGoto(t *testing.T) {
	r := flowFixture(t, `func f(ch chan int, n int) {
outer:
	for i := 0; i < n; i++ {
		if i == 1 { continue outer }
		if i == 2 { break outer }
	}
	switch n {
	case 1, 2: n++; fallthrough
	case 3: n++
	default: n--
	}
	select {
	case n = <-ch: n++
	case ch <- n: n--
	default: goto done
	}
done:
	return
}`)
	f := namedFlow(t, r, "f")
	for _, label := range []string{"continue outer", "break outer", "fallthrough", "goto done"} {
		n := flowNode(t, f, label)
		found := false
		for _, e := range f.Edges {
			if e.From == n.ID && e.Label == strings.Fields(label)[0] {
				found = true
			}
		}
		if !found {
			t.Errorf("missing transfer edge for %q", label)
		}
	}
	for _, e := range f.Edges {
		if e.Kind == "select" && (e.Label == "true" || e.Label == "false" || strings.Contains(e.Label, "==")) {
			t.Fatalf("select misrepresented as boolean/equality: %+v", e)
		}
	}
	if len(f.Limitations) == 0 {
		t.Fatal("missing static-analysis limitations")
	}
}

func TestControlFlowSourceCallsCommentsClosuresAndPanic(t *testing.T) {
	src := `func step() {}
func f(ok bool) {
	// attached directly
	step() // trailing
	unused := func() { step() }
	_ = unused
	defer step()
	go step()
	if ok && func() bool { step(); return true }() { panic("stop") }
	step()
}
func shadow(panic func(string)) { panic("not builtin"); step() }
func empty() {}
func blocked(ch chan int) { select { case <-ch: step() } }
func forever() { for {} }`
	r := flowFixture(t, src)
	f := namedFlow(t, r, "f")
	seen := map[string]int{}
	for _, n := range f.Nodes {
		for _, id := range n.CallIDs {
			seen[id]++
			for _, c := range r.Calls {
				if c.ID == id && c.From != f.SymbolID {
					t.Fatal("nested closure body call attached to parent")
				}
			}
		}
		if n.Kind != "entry" && n.Kind != "exit" && n.Location.FileID != "source" {
			t.Fatalf("missing source linkage: %+v", n)
		}
	}
	for _, c := range r.Calls {
		if c.From == f.SymbolID && seen[c.ID] != 1 {
			t.Errorf("parent call %s attached %d times", c.Target, seen[c.ID])
		}
	}
	step := flowNode(t, f, "step()")
	if len(step.Comments) != 2 {
		t.Fatalf("direct comments=%+v", step.Comments)
	}
	for _, label := range []string{"defer step()", "go step()"} {
		n := flowNode(t, f, label)
		if n.Kind != strings.Fields(label)[0] {
			t.Errorf("missing scheduling kind: %+v", n)
		}
	}
	panicNode := flowNode(t, f, `panic("stop")`)
	for _, e := range f.Edges {
		if e.From == panicNode.ID && e.Kind != "panic" {
			t.Fatalf("builtin panic falls through: %+v", e)
		}
	}
	shadow := namedFlow(t, r, "shadow")
	if !flowHasEdge(shadow, flowNode(t, shadow, `panic("not builtin")`).ID, flowNode(t, shadow, "step()").ID, "") {
		t.Fatal("shadowed panic incorrectly terminates")
	}
	for _, name := range []string{"empty", "forever", "blocked"} {
		graph := namedFlow(t, r, name)
		if graph.Entry == "" || len(graph.Nodes) < 2 {
			t.Errorf("missing graph for %s", name)
		}
	}

}

func TestControlFlowSelectOperandsAndAssignmentOrder(t *testing.T) {
	r := flowFixture(t, `func channel() chan int { return nil }
	func value() int { return 1 }
	func index() int { return 0 }
	func f(dst []int) {
		select {
		case dst[index()] = <-channel(): return
		case channel() <- value(): return
		default: return
		}
	}`)
	f := namedFlow(t, r, "f")
	for _, c := range r.Calls {
		if c.From != f.SymbolID {
			continue
		}
		var matches []FlowNode
		for _, n := range f.Nodes {
			for _, id := range n.CallIDs {
				if id == c.ID {
					matches = append(matches, n)
				}
			}
		}
		if len(matches) != 1 {
			t.Fatalf("call %s mapped %d times", c.Target, len(matches))
		}
		want := "select_operand"
		if strings.Contains(c.Target, "index") {
			want = "communication"
		}
		if matches[0].Kind != want {
			t.Errorf("%s mapped to %s, want %s", c.Target, matches[0].Kind, want)
		}
	}
	foundDefault := false
	for _, e := range f.Edges {
		foundDefault = foundDefault || e.Label == "default" && e.Kind == "select"
	}
	if !foundDefault {
		t.Fatal("select default edge missing")
	}
}

func TestControlFlowTypeSwitchCommentsAndUnreachable(t *testing.T) {
	r := flowFixture(t, `func step() {}
	func f(v any, ok bool) {
		// conditional ownership
		if ok { return }
		switch v.(type) {
		case nil: step()
		case int, string: step()
		default: return
		}
		return
		step()
	}`)
	f := namedFlow(t, r, "f")
	if len(flowNode(t, f, "ok").Comments) != 1 {
		t.Fatal("lost comment owned by conditional statement")
	}
	for _, label := range []string{"type matches nil", "type matches int", "type matches string"} {
		flowNode(t, f, label)
	}
	var last FlowNode
	for _, n := range f.Nodes {
		if n.Label == "step()" && n.Location.Offset > last.Location.Offset {
			last = n
		}
	}
	if last.Reachable {
		t.Fatal("statement after return marked reachable")
	}
}

func TestControlFlowRangeBindingRunsInsideIteration(t *testing.T) {
	r := flowFixture(t, `func index() int { return 0 }
func values() []int { return nil }
func f(dst []int) {
	for dst[index()] = range values() { continue }
}`)
	f := namedFlow(t, r, "f")
	var binding FlowNode
	for _, n := range f.Nodes {
		for _, id := range n.CallIDs {
			for _, c := range r.Calls {
				if c.ID == id && strings.Contains(c.Target, "index") {
					binding = n
				}
			}
		}
	}
	if binding.Kind != "range_assignment" {
		t.Fatalf("range LHS was evaluated outside iteration: %+v", binding)
	}
	test := flowNode(t, f, "next range iteration")
	if !flowHasEdge(f, test.ID, binding.ID, "next") {
		t.Fatal("range assignment not controlled by iteration test")
	}
}

func TestControlFlowAnalyzeIntegration(t *testing.T) {
	r := fixture(t, Options{})
	if len(r.ControlFlows) == 0 {
		t.Fatal("Analyze omitted control-flow evidence")
	}
	for _, f := range r.ControlFlows {
		for _, limitation := range f.Limitations {
			if strings.Contains(limitation, "unavailable") {
				t.Fatalf("fixture CFG failed: %s", limitation)
			}
		}

		ids := map[string]bool{}
		for _, n := range f.Nodes {
			ids[n.ID] = true
		}
		if !ids[f.Entry] {
			t.Fatal("entry does not reference a node")
		}
		for _, e := range f.Edges {
			if !ids[e.From] || !ids[e.To] {
				t.Fatalf("edge references absent node: %+v", e)
			}
		}
	}
}

func TestControlFlowEmptySelectBlocksPermanently(t *testing.T) {
	r := flowFixture(t, `func f() { select {} }`)
	f := namedFlow(t, r, "f")
	blocked := flowNode(t, f, "select {} blocks permanently")
	if blocked.Kind != "blocked" || !blocked.Reachable {
		t.Fatalf("empty select not explicitly blocked: %+v", blocked)
	}
	for _, e := range f.Edges {
		if e.From == blocked.ID {
			t.Fatalf("empty select has outgoing edge: %+v", e)
		}
	}
	for _, n := range f.Nodes {
		if n.Kind == "exit" && n.Reachable {
			t.Fatal("empty select can reach exit")
		}
	}
}

func TestControlFlowTransferDestinations(t *testing.T) {
	r := flowFixture(t, `func after() {}
func second() {}
func f(n int) {
	for i := 0; i < n; i++ {
		if i == 1 { continue }
		break
	}
	after()
	switch n {
	case 1: fallthrough
	case 2: second()
	}
	goto done
	after()
done:
	return
}`)
	f := namedFlow(t, r, "f")
	for _, tt := range []struct{ from, to, label string }{
		{"continue", "i++", "continue"},
		{"break", "after()", "break"},
		{"fallthrough", "second()", "fallthrough"},
		{"goto done", "done:", "goto"},
	} {
		if !flowHasEdge(f, flowNode(t, f, tt.from).ID, flowNode(t, f, tt.to).ID, tt.label) {
			t.Errorf("%s does not transfer directly to %s", tt.from, tt.to)
		}
	}
}
