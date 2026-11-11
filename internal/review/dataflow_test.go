package review

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func dataFixture(t *testing.T, source string) *Report {
	t.Helper()
	dir := workspace(t)
	for name, text := range map[string]string{
		"go.mod":       "module fixture\n\ngo 1.24.0\n",
		"flow.go":      "package fixture\n" + source,
		"flow_test.go": "package fixture\nimport \"testing\"\nfunc TestNeverRun(t *testing.T) { panic(\"target tests must not run\") }\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	r, err := Analyze(context.Background(), Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func dataNamed(t *testing.T, r *Report, name string) *DataFlow {
	t.Helper()
	f, err := AnalyzeDataFlow(context.Background(), r, symbol(t, r, name).ID)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func dataKind(f *DataFlow, kind string) []DataNode {
	var nodes []DataNode
	for _, n := range f.Nodes {
		if n.Kind == kind {
			nodes = append(nodes, n)
		}
	}
	return nodes
}

func dataEdge(f *DataFlow, from, to string) bool {
	for _, e := range f.Edges {
		if e.From == from && e.To == to {
			return true
		}
	}
	return false
}

func TestDataFlowArithmeticExactStableAndImmutable(t *testing.T) {
	r := dataFixture(t, `func arithmetic(x int) int { return (x + 2) * 2 }`)
	before, _ := json.Marshal(r)
	f := dataNamed(t, r, "arithmetic")
	if f.SourceSHA256 != r.SourceSHA256 || f.SymbolID != symbol(t, r, "arithmetic").ID {
		t.Fatal("lost snapshot ownership")
	}

	if len(f.Nodes) != 5 || len(f.Edges) != 5 || len(dataKind(f, "constant")) != 1 {
		t.Fatalf("want parameter, shared constant, two operations, return: %+v", f)
	}
	p, c, ops, ret := dataKind(f, "parameter"), dataKind(f, "constant"), dataKind(f, "operation"), dataKind(f, "return")
	if p[0].Label != "x" || !strings.HasPrefix(ops[0].Label, "t0 = ") || !strings.HasPrefix(ops[1].Label, "t1 = ") {
		t.Fatalf("SSA definitions must be legible: %+v", f.Nodes)
	}
	for _, pair := range [][2]string{{p[0].ID, ops[0].ID}, {c[0].ID, ops[0].ID}, {ops[0].ID, ops[1].ID}, {c[0].ID, ops[1].ID}, {ops[1].ID, ret[0].ID}} {
		if !dataEdge(f, pair[0], pair[1]) {
			t.Fatalf("missing direct edge %v", pair)
		}
	}
	for _, n := range f.Nodes {
		if n.Type == "" && n.Kind != "return" {
			t.Fatalf("missing type: %+v", n)
		}
		if n.Location != nil {
			loc := n.Location
			if loc.FileID != r.Files[0].ID || loc.Offset < 0 || loc.EndOffset > len(r.Files[0].Source) || loc.Line < 1 {
				t.Fatalf("invalid source mapping: %+v", loc)
			}
		}
	}
	second := dataNamed(t, r, "arithmetic")
	if !reflect.DeepEqual(f.Nodes, second.Nodes) || !reflect.DeepEqual(f.Edges, second.Edges) {
		t.Fatal("unstable graph")
	}
	after, _ := json.Marshal(r)
	if string(before) != string(after) {
		t.Fatal("published snapshot mutated")
	}
	payload, _ := json.Marshal(f)
	var schema map[string]json.RawMessage
	_ = json.Unmarshal(payload, &schema)
	if schema["sourceSHA256"] == nil || schema["limitations"] == nil || schema["collector"] == nil || schema["toolVersion"] == nil || schema["generatedAt"] == nil || schema["commands"] == nil {
		t.Fatalf("unexpected JSON schema: %s", payload)
	}
	if !strings.Contains(f.ToolVersion, "v0.42.0") || f.Collector != "golang.org/x/tools/go/ssa" || f.GeneratedAt.IsZero() || len(f.Commands) == 0 {
		t.Fatalf("missing collector provenance: %+v", f)
	}
	for _, command := range f.Commands {
		if command.ExitCode != 0 || command.Executable != "go" || command.StartedAt.IsZero() || command.FinishedAt.IsZero() || command.StdoutSHA256 == "" || strings.Contains(strings.Join(command.Args, " "), "test") {
			t.Fatalf("invalid actual analysis command: %+v", command)
		}
	}
}

func TestDataFlowAfterRecordingIncludesOnlyProductionPackageSources(t *testing.T) {
	r := dataFixture(t, `func arithmetic(x int) int { return x + 2 }`)
	path := "flow_test.go"
	data, err := os.ReadFile(filepath.Join(r.Root, path))
	if err != nil {
		t.Fatal(err)
	}
	r.Files = append(r.Files, File{ID: stableID("file", "fixture/"+path), Package: "fixture", Path: path, SHA256: digest(data), Source: string(data)})
	f := dataNamed(t, r, "arithmetic")
	if len(f.Nodes) == 0 {
		t.Fatal("production SSA unavailable after test sources added")
	}
}

func TestDataFlowCallTargetsUseSnapshotSymbols(t *testing.T) {
	r := dataFixture(t, `
import "fmt"
type item int
func (item) Next(x int) int { return x + 1 }
func target(x int) int { return x + 2 }
func direct(x int) int { return target(x) }
func method(v item, x int) int { return v.Next(x) }
func recursive(x int) int { if x > 0 { return recursive(x-1) }; return x }
func closure(x int) int { fn := func(y int) int { return y + x }; return fn(x) }
func deferred(x int) { defer target(x); go target(x) }
func dynamic(fn func(int) int, x int) int { return fn(x) }
type next interface { Next(int) int }
func invoke(v next, x int) int { return v.Next(x) }
func external(x int) string { return fmt.Sprint(x) }
func builtin(x []int) int { return len(x) }
`)
	var closureID string
	for _, s := range r.Symbols {
		if s.Kind == "closure" {
			closureID = s.ID
		}
	}
	if closureID == "" {
		t.Fatal("missing closure fixture")
	}
	for _, tc := range []struct {
		name, target string
		count        int
	}{
		{"direct", symbol(t, r, "target").ID, 1},
		{"method", symbol(t, r, "Next").ID, 1},
		{"recursive", symbol(t, r, "recursive").ID, 1},
		{"closure", closureID, 1},
		{"deferred", symbol(t, r, "target").ID, 2},
		{"dynamic", "", 1}, {"invoke", "", 1}, {"external", "", 1}, {"builtin", "", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			flow := dataNamed(t, r, tc.name)
			data, err := json.Marshal(flow)
			if err != nil {
				t.Fatal(err)
			}
			var wire struct {
				Nodes []struct{ Kind, CalleeID, Boundary string }
			}
			if err := json.Unmarshal(data, &wire); err != nil {
				t.Fatal(err)
			}
			count := 0
			for _, n := range wire.Nodes {
				if n.Kind != "call" || strings.Contains(n.Boundary, "unknown deferred targets") {
					continue
				}
				count++
				if n.CalleeID != tc.target {
					t.Errorf("calleeID = %q, want %q", n.CalleeID, tc.target)
				}
				if n.Boundary == "" {
					t.Error("navigation must not remove opaque-flow boundary")
				}
			}
			if count != tc.count {
				t.Fatalf("calls=%d, want %d", count, tc.count)
			}
		})
	}
}

func TestDataFlowCallTargetInAnotherLocalPackage(t *testing.T) {
	r := dataFixture(t, `func unused() {}`)
	if err := os.Mkdir(filepath.Join(r.Root, "helper"), 0700); err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{
		"helper/helper.go": "package helper\ntype Item int\nfunc (Item) Next(x int) int { return x+1 }\nfunc Next(x int) int { return x+2 }\n",
		"flow.go":          "package fixture\nimport \"fixture/helper\"\nfunc caller(x int) int { return helper.Next(x) + helper.Item(0).Next(x) }\n",
	} {
		if err := os.WriteFile(filepath.Join(r.Root, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	var err error
	r, err = Analyze(context.Background(), Options{Dir: r.Root})
	if err != nil {
		t.Fatal(err)
	}
	flow := dataNamed(t, r, "caller")
	want := map[string]bool{}
	for _, s := range r.Symbols {
		if s.Package == "fixture/helper" && s.Name == "Next" {
			want[s.ID] = false
		}
	}
	if len(want) != 2 {
		t.Fatal("missing package-level and receiver declarations")
	}
	for _, n := range flow.Nodes {
		if n.Kind != "call" {
			continue
		}
		if _, ok := want[n.CalleeID]; !ok {
			t.Fatalf("call points outside expected declarations: %+v", n)
		}
		want[n.CalleeID] = true
	}
	for id, seen := range want {
		if !seen {
			t.Errorf("callee not linked: %s", id)
		}
	}
}

func TestDataFlowPhiLoopCallsAndMemory(t *testing.T) {
	r := dataFixture(t, `
func join(x int, yes bool) int { if yes { x = x + 1 }; return x }
func loop(n int) int { x := 0; for x < n { x++ }; return x }
func callee(x int) int { return 7 }
func caller(x int, fn func(int) int) int { a := callee(x); return fn(a) }
func memory(p *int, x int) int { *p = x; return *p }
`)
	join := dataNamed(t, r, "join")
	phis := dataKind(join, "phi")
	if len(phis) != 1 {
		t.Fatalf("missing branch merge: %+v", join)
	}
	count := 0
	for _, edge := range join.Edges {
		if edge.To == phis[0].ID {
			count++
			if !strings.Contains(edge.Label, "block") {
				t.Fatalf("phi lacks predecessor: %+v", edge)
			}
		}
	}
	if count != 2 {
		t.Fatal("phi lost incoming definitions")
	}
	loop := dataNamed(t, r, "loop")
	cycle := false
	for _, phi := range dataKind(loop, "phi") {
		for _, op := range dataKind(loop, "operation") {
			cycle = cycle || (dataEdge(loop, phi.ID, op.ID) && dataEdge(loop, op.ID, phi.ID))
		}
	}
	if !cycle {
		t.Fatalf("loop recurrence lost: %+v", loop)
	}
	calls := dataNamed(t, r, "caller")
	cs := dataKind(calls, "call")
	if len(cs) != 2 || !dataEdge(calls, cs[0].ID, cs[1].ID) || !strings.Contains(cs[1].Boundary, "unknown") {
		t.Fatalf("opaque call result / dynamic target missing: %+v", calls)
	}
	for _, c := range cs {
		if c.Boundary == "" {
			t.Fatal("call lacks boundary")
		}
	}
	mem := dataNamed(t, r, "memory")
	ms := dataKind(mem, "memory")
	if len(ms) != 2 || dataEdge(mem, ms[0].ID, ms[1].ID) || ms[0].Boundary == "" || ms[1].Boundary == "" {
		t.Fatalf("store/load must not imply memory-state/alias flow: %+v", mem)
	}
	for _, f := range []*DataFlow{join, loop, calls, mem} {
		nodes := map[string]bool{}
		for _, n := range f.Nodes {
			if nodes[n.ID] {
				t.Fatal("duplicate node ID")
			}
			nodes[n.ID] = true
		}
		for _, e := range f.Edges {
			if !nodes[e.From] || !nodes[e.To] {
				t.Fatalf("dangling edge: %+v", e)
			}
		}
	}
}

func TestDataFlowClosureAndMethodSourceSelection(t *testing.T) {
	r := dataFixture(t, `
type A int
type B int
func (a A) Same(x int) int { return x + 1 }
func (b B) Same(x int) int { return x * 2 }
func outer(x int) func() int { return func() int { return x + 3 } }
`)
	methods := 0
	for _, s := range r.Symbols {
		if s.Kind != "method" && s.Kind != "closure" {
			continue
		}
		f, err := AnalyzeDataFlow(context.Background(), r, s.ID)
		if err != nil {
			t.Fatal(err)
		}
		if s.Kind == "method" {
			methods++
			if len(dataKind(f, "parameter")) != 2 {
				t.Fatalf("wrong method or lost receiver: %+v", f)
			}
			want := " + "
			if strings.HasPrefix(r.Files[0].Source[s.Offset:s.EndOffset], "func (b B)") {
				want = " * "
			}
			if ops := dataKind(f, "operation"); len(ops) != 1 || !strings.Contains(ops[0].Label, want) {
				t.Fatalf("duplicate method name selected wrong body; signature=%q source=%q: %+v", s.Signature, r.Files[0].Source[s.Offset:s.EndOffset], f)
			}
		} else {
			free := dataKind(f, "freevar")
			if len(free) != 1 || free[0].Label != "x" || free[0].Boundary == "" {
				t.Fatalf("closure capture must be a named boundary free variable: %+v", f)
			}
		}
		for _, n := range f.Nodes {
			if n.Location != nil && (n.Location.Offset < s.Offset || n.Location.Offset >= s.EndOffset) {
				t.Fatalf("cross-function location: %+v", n)
			}
		}
	}
	if methods != 2 {
		t.Fatal("fixture did not include both methods")
	}
	if len(dataKind(dataNamed(t, r, "outer"), "freevar")) != 0 {
		t.Fatal("outer graph includes closure body")
	}
}

func TestDataFlowRejectsStaleUnsupportedAndTypeErrors(t *testing.T) {
	r := dataFixture(t, "func f(x int) int { return x + 1 }\n")
	file := filepath.Join(r.Root, r.Files[0].Path)
	if err := os.WriteFile(file, []byte(r.Files[0].Source+"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	if _, err := AnalyzeDataFlow(context.Background(), r, symbol(t, r, "f").ID); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("stale source accepted: %v", err)
	}
	broken := dataFixture(t, "func f() int { return missing }\n")
	if _, err := AnalyzeDataFlow(context.Background(), broken, symbol(t, broken, "f").ID); err == nil {
		t.Fatal("type errors silently accepted")
	}
	generic := dataFixture(t, "func f[T any](x T) T { return x }\n")
	if _, err := AnalyzeDataFlow(context.Background(), generic, symbol(t, generic, "f").ID); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("generic unsupported status not explicit: %v", err)
	}
	if _, err := AnalyzeDataFlow(context.Background(), generic, "missing"); err == nil {
		t.Fatal("unknown symbol accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := AnalyzeDataFlow(ctx, generic, symbol(t, generic, "f").ID); err != context.Canceled {
		t.Fatalf("cancellation lost: %v", err)
	}
}

func TestDataFlowImportsMemoryBoundariesAndSideEffects(t *testing.T) {
	r := dataFixture(t, `
	import "strings"
	var global int
	func readGlobal() int { return global }
	func calls(x string) int { return strings.Count(x, "a") }
	func memory(m map[int]int, ch chan int, p *[2]int, x int) {
		m[x] = x
		ch <- m[x]
		p[x] = <-ch
		delete(m, x)
	}
	func launch(fn func(int), x int) { defer fn(x); go fn(x) }
	`)
	for _, name := range []string{"readGlobal", "calls", "memory", "launch"} {
		f := dataNamed(t, r, name)
		for _, n := range f.Nodes {
			if n.Kind == "memory" && n.Boundary == "" {
				t.Fatalf("memory operation lacks boundary: %+v", n)
			}
		}
		if name == "calls" && len(dataKind(f, "call")) != 1 {
			t.Fatalf("imported function call unavailable: %+v", f)
		}
		if name == "memory" {
			for _, call := range dataKind(f, "call") {
				if strings.Contains(call.Label, "delete") && !strings.Contains(call.Boundary, "memory") {
					t.Fatalf("map delete must retain memory boundary: %+v", call)
				}
			}
		}
		if name == "launch" && len(dataKind(f, "call")) != 3 {
			t.Fatalf("non-value defer/go/RunDefers lost: %+v", f)
		}
		if name == "readGlobal" {
			memory := dataKind(f, "memory")
			if len(memory) != 2 || !dataEdge(f, memory[1].ID, memory[0].ID) || memory[1].Location != nil {
				t.Fatalf("global address must be explicit external boundary: %+v", f)
			}
		}
	}
}

func TestDataFlowDoesNotMergeAbbreviatedConstants(t *testing.T) {
	r := dataFixture(t, `func f() (string, string, string) {
return "same-prefix-long-enough-A", "same-prefix-long-enough-B", "same-prefix-long-enough-A"
}`)
	f := dataNamed(t, r, "f")
	if len(dataKind(f, "constant")) != 2 || len(f.Edges) != 3 || f.Edges[0].From == f.Edges[1].From || f.Edges[0].From != f.Edges[2].From {
		t.Fatalf("abbreviated SSA labels must not merge distinct values: %+v", f)
	}
}

func TestDataFlowNilConstant(t *testing.T) {
	r := dataFixture(t, "func f() *int { return nil }\n")
	f := dataNamed(t, r, "f")
	constants := dataKind(f, "constant")
	if len(constants) != 1 || !strings.HasPrefix(constants[0].Label, "nil") {
		t.Fatalf("nil constant must be represented explicitly: %+v", f)
	}
}

func TestDataFlowTypeSwitchImplicitVariables(t *testing.T) {
	r := dataFixture(t, `
func unrelated() int { return 7 }
func choose(input any) int {
	switch value := input.(type) {
	case int:
		return value + 1
	case string, []byte:
		return len(value.(string))
	default:
		return 0
	}
}
`)
	dataNamed(t, r, "unrelated")
	f := dataNamed(t, r, "choose")
	if len(dataKind(f, "parameter")) != 1 || len(dataKind(f, "return")) < 2 {
		t.Fatalf("type switch graph is incomplete: %+v", f)
	}
}

func TestDataFlowRejectsChangedBuildAndSourceSet(t *testing.T) {
	r := dataFixture(t, "func f() {}\n")
	r.Environment["GOARCH"] = "not-a-real-architecture"
	if _, err := AnalyzeDataFlow(context.Background(), r, symbol(t, r, "f").ID); err == nil || !strings.Contains(err.Error(), "environment") {
		t.Fatalf("changed build environment accepted: %v", err)
	}

	r = dataFixture(t, "func f() {}\n")
	if err := os.WriteFile(filepath.Join(r.Root, "new.go"), []byte("package fixture\nfunc added() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := AnalyzeDataFlow(context.Background(), r, symbol(t, r, "f").ID); err == nil || !strings.Contains(err.Error(), "source set changed") {
		t.Fatalf("new package source accepted: %v", err)
	}
	r = dataFixture(t, "func f() {}\n")
	if err := os.WriteFile(filepath.Join(r.Root, "go.mod"), []byte("module changed\n\ngo 1.24.0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := AnalyzeDataFlow(context.Background(), r, symbol(t, r, "f").ID); err == nil || !strings.Contains(err.Error(), "manifest changed") {
		t.Fatalf("changed manifest accepted: %v", err)
	}
	r = dataFixture(t, "func f() {}\n")
	r.Files[0].Path = "flow_test.go"
	if _, err := AnalyzeDataFlow(context.Background(), r, symbol(t, r, "f").ID); err == nil || !strings.Contains(err.Error(), "unsupported test") {
		t.Fatalf("test variant accepted: %v", err)
	}
}
