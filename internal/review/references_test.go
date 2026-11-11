package review

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestCallableReferencesDistinguishInvocationFromFunctionValues(t *testing.T) {
	r := flowFixture(t, `func use(fn any) {}
func f() {
	use(use)
	go use(nil)
	defer use(nil)
	value := use
	_ = value
	_ = func() {}
	(func() {})()
}`)
	owner := symbol(t, r, "f")
	data, err := json.Marshal(r.References)
	if err != nil {
		t.Fatal(err)
	}

	var refs []struct{ From, To, Name, Usage string }
	if err := json.Unmarshal(data, &refs); err != nil {
		t.Fatal(err)
	}
	var callee, values, literalCallee, literalValue int
	for _, ref := range refs {
		if ref.From != owner.ID {
			continue
		}
		target := ""
		for _, s := range r.Symbols {
			if s.ID == ref.To {
				target = s.Kind
			}
		}
		if ref.Name == "use" {
			switch ref.Usage {
			case "callee":
				callee++
			case "value":
				values++
			default:
				t.Errorf("unknown callable usage %q", ref.Usage)
			}
		} else if target == "closure" {
			switch ref.Usage {
			case "callee":
				literalCallee++
			case "value":
				literalValue++
			}
		}
	}
	if callee != 3 || values != 2 || literalCallee != 1 || literalValue != 1 {
		t.Fatalf("got invoked=%d values=%d literal calls=%d literal values=%d; want 3,2,1,1", callee, values, literalCallee, literalValue)
	}
}

func TestCallInvocationSeparatesDeferAndGoFromArgumentEvaluation(t *testing.T) {
	r := flowFixture(t, `func argument() int { return 1 }
func work(int) {}
func f() {
	work(argument())
	defer work(argument())
	go work(argument())
}`)
	data, err := json.Marshal(r.Calls)
	if err != nil {
		t.Fatal(err)
	}
	var calls []struct{ Target, Invocation string }
	if err := json.Unmarshal(data, &calls); err != nil {
		t.Fatal(err)
	}
	var modes, arguments []string
	for _, call := range calls {
		if strings.Contains(call.Target, ".work(") {
			modes = append(modes, call.Invocation)
		}
		if strings.Contains(call.Target, ".argument(") {
			arguments = append(arguments, call.Invocation)
		}
	}
	if !reflect.DeepEqual(modes, []string{"synchronous", "deferred", "goroutine"}) {
		t.Fatalf("work modes: %v", modes)
	}
	if !reflect.DeepEqual(arguments, []string{"synchronous", "synchronous", "synchronous"}) {
		t.Fatalf("argument evaluation must remain synchronous: %v", arguments)
	}
}

func TestCallEvaluationOrderUsesArgumentsBeforeInvocationAndFlagsShortCircuit(t *testing.T) {
	r := flowFixture(t, `func argument() int { return 1 }
func work(int) bool { return true }
func f() {
	work(argument())
	defer work(argument())
	go work(argument())
	if work(1) && work(2) { work(3) }
}`)
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}

	var wire struct {
		Calls []struct {
			Target          string
			EvaluationOrder int
		}
		ControlFlows []struct {
			SymbolID string
			Nodes    []struct {
				Label          string
				CallOrderKnown bool
			}
		}
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	var work, argument []int
	for _, call := range wire.Calls {
		if strings.Contains(call.Target, ".work(") {
			work = append(work, call.EvaluationOrder)
		}
		if strings.Contains(call.Target, ".argument(") {
			argument = append(argument, call.EvaluationOrder)
		}
	}
	if !reflect.DeepEqual(argument, []int{1, 3, 5}) || !reflect.DeepEqual(work, []int{2, 4, 6, 7, 8, 9}) {
		t.Fatalf("evaluation order: argument=%v work=%v", argument, work)
	}
	var ordinary, conditional bool
	for _, flow := range wire.ControlFlows {
		if flow.SymbolID != symbol(t, r, "f").ID {
			continue
		}
		for _, node := range flow.Nodes {
			if node.Label == "work(argument())" {
				ordinary = node.CallOrderKnown
			}
			if node.Label == "work(1) && work(2)" {
				conditional = !node.CallOrderKnown
			}
		}
	}
	if !ordinary || !conditional {
		t.Fatalf("ordinary=%v conditional boundary=%v", ordinary, conditional)
	}
}

func TestChainedCallsHaveDistinctSourceIdentitiesAndEvaluationOrder(t *testing.T) {
	r := flowFixture(t, `type writer struct{}
func makeWriter() *writer { return nil }
func (w *writer) header() *writer { return w }
func (w *writer) write() {}
func f() { makeWriter().header().write() }`)
	owner := symbol(t, r, "f").ID
	ids := map[string]bool{}
	orders := map[int]string{}
	for _, call := range r.Calls {
		if call.From != owner {
			continue
		}
		if ids[call.ID] {
			t.Fatalf("chained calls share ID %s at offset %d", call.ID, call.Offset)
		}
		ids[call.ID] = true
		orders[call.EvaluationOrder] = call.Target
	}
	if len(ids) != 3 {
		t.Fatalf("want three distinct call sites, got %d", len(ids))
	}
	for order, name := range []string{"makeWriter", "header", "write"} {
		if !strings.Contains(orders[order+1], "."+name+"(") {
			t.Fatalf("call %d: want %s, got %s", order+1, name, orders[order+1])
		}
	}
}
