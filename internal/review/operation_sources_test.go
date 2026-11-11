package review

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type operationSourceFixture struct {
	Version     string                  `json:"version"`
	Callables   []operationCallableJSON `json:"callables"`
	Calls       []operationCallJSON     `json:"calls"`
	Returns     []operationReturnJSON   `json:"returns"`
	Bindings    []operationBindingJSON  `json:"bindings"`
	FieldStores []operationFieldJSON    `json:"fieldStores"`
}

type operationAnchorJSON struct {
	Module       string   `json:"module"`
	SourceSHA256 string   `json:"sourceSHA256"`
	Location     Location `json:"location"`
}

type operationValueJSON struct {
	ID         string              `json:"id"`
	Expression string              `json:"expression"`
	Name       string              `json:"name"`
	Type       string              `json:"type"`
	Anchor     operationAnchorJSON `json:"anchor"`
}

type operationSlotJSON struct {
	Position int                `json:"position"`
	Value    operationValueJSON `json:"value"`
}

type operationCallableJSON struct {
	Key           string              `json:"key"`
	SymbolID      string              `json:"symbolID"`
	Package       string              `json:"package"`
	Receiver      string              `json:"receiver"`
	ReceiverValue *operationValueJSON `json:"receiverValue"`
	Name          string              `json:"name"`
	Kind          string              `json:"kind"`
	Anchor        operationAnchorJSON `json:"anchor"`
	Parameters    []operationSlotJSON `json:"parameters"`
	Results       []operationSlotJSON `json:"results"`
}

type operationArgumentJSON struct {
	Position          int                `json:"position"`
	ParameterPosition int                `json:"parameterPosition"`
	TuplePosition     *int               `json:"tuplePosition,omitempty"`
	Value             operationValueJSON `json:"value"`
	Status            string             `json:"status"`
	Reason            string             `json:"reason"`
}

type operationResultJSON struct {
	Position    int                 `json:"position"`
	Destination *operationValueJSON `json:"destination"`
	Status      string              `json:"status"`
	Reason      string              `json:"reason"`
	Anchor      operationAnchorJSON `json:"anchor"`
}

type operationCallJSON struct {
	ID           string                  `json:"id"`
	CallID       string                  `json:"callID"`
	CallerKey    string                  `json:"callerKey"`
	TargetKey    string                  `json:"targetKey"`
	TargetName   string                  `json:"targetName"`
	TargetStatus string                  `json:"targetStatus"`
	Anchor       operationAnchorJSON     `json:"anchor"`
	Receiver     *operationValueJSON     `json:"receiver"`
	Arguments    []operationArgumentJSON `json:"arguments"`
	Results      []operationResultJSON   `json:"results"`
}

type operationReturnJSON struct {
	CallableKey string              `json:"callableKey"`
	Position    int                 `json:"position"`
	Value       *operationValueJSON `json:"value"`
	Anchor      operationAnchorJSON `json:"anchor"`
	Status      string              `json:"status"`
	Reason      string              `json:"reason"`
}

type operationBindingJSON struct {
	OwnerKey       string              `json:"ownerKey"`
	Source         *operationValueJSON `json:"source"`
	Destination    *operationValueJSON `json:"destination"`
	SourceKey      string              `json:"sourceKey"`
	DestinationKey string              `json:"destinationKey"`
	Kind           string              `json:"kind"`
	Anchor         operationAnchorJSON `json:"anchor"`
	Status         string              `json:"status"`
	Reason         string              `json:"reason"`
}

type operationFieldJSON struct {
	OwnerKey  string              `json:"ownerKey"`
	Receiver  operationValueJSON  `json:"receiver"`
	FieldKey  string              `json:"fieldKey"`
	FieldName string              `json:"fieldName"`
	Value     operationValueJSON  `json:"value"`
	Kind      string              `json:"kind"`
	Anchor    operationAnchorJSON `json:"anchor"`
	Status    string              `json:"status"`
	Reason    string              `json:"reason"`
}

func analyzeOperationSourceFixture(t *testing.T, files map[string]string, scope *ReviewScope) *Report {
	t.Helper()
	root := t.TempDir()
	writeReviewFixture(t, root, "go.mod", "module example.test/source\n\ngo 1.24\n")
	for path, source := range files {
		writeReviewFixture(t, root, path, source)
	}
	report, err := Analyze(context.Background(), Options{Dir: root, Scope: scope})
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func readOperationSources(t *testing.T, report *Report) operationSourceFixture {
	t.Helper()
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		OperationSources *operationSourceFixture `json:"operationSources"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if document.OperationSources == nil {
		t.Fatal("Analyze did not retain source-named operation facts")
	}
	if document.OperationSources.Version != "operation-sources/1" {
		t.Fatalf("unexpected operation-source version %q", document.OperationSources.Version)
	}
	return *document.OperationSources
}

func operationExcerpt(t *testing.T, report *Report, anchor operationAnchorJSON) string {
	t.Helper()
	for _, file := range report.Files {
		if file.ID == anchor.Location.FileID {
			source := []byte(file.Source)
			start, end := anchor.Location.Offset, anchor.Location.EndOffset
			if start < 0 || end < start || end > len(source) {
				t.Fatalf("invalid UTF-8 byte range [%d,%d) for %s", start, end, file.Path)
			}
			return string(source[start:end])
		}
	}
	t.Fatalf("anchor file %q is not in the included snapshot", anchor.Location.FileID)
	return ""
}

func callableNamed(t *testing.T, sources operationSourceFixture, name string) operationCallableJSON {
	t.Helper()
	for _, callable := range sources.Callables {
		if callable.Name == name {
			return callable
		}
	}
	t.Fatalf("callable %q was not collected", name)
	return operationCallableJSON{}
}

func callWithExcerpt(t *testing.T, report *Report, sources operationSourceFixture, excerpt string) operationCallJSON {
	t.Helper()
	for _, call := range sources.Calls {
		if operationExcerpt(t, report, call.Anchor) == excerpt {
			return call
		}
	}
	t.Fatalf("call %q was not collected", excerpt)
	return operationCallJSON{}
}

func TestOperationSourceArgumentAndReturn(t *testing.T) {
	const source = `package source
type Event struct { Body []byte }
func decode(body []byte) (event Event, err error) { return Event{Body: body}, nil }
func consume(event Event) error { return nil }
func receive(input []byte) error {
	event, err := decode(input)
	if err != nil { return err }
	return consume(event)
}
`
	report := analyzeOperationSourceFixture(t, map[string]string{"source.go": source}, nil)
	sources := readOperationSources(t, report)
	call := callWithExcerpt(t, report, sources, "decode(input)")
	decode := callableNamed(t, sources, "decode")
	if call.TargetKey != decode.Key || call.TargetStatus != "local" {
		t.Fatalf("call target identity did not resolve to decode: %+v", call)
	}
	if len(decode.Parameters) != 1 || decode.Parameters[0].Position != 0 || decode.Parameters[0].Value.Name != "body" {
		t.Fatalf("parameter position/name missing: %+v", decode.Parameters)
	}
	if len(call.Arguments) != 1 || call.Arguments[0].ParameterPosition != 0 ||
		call.Arguments[0].Status != "resolved" || call.Arguments[0].Value.Name != "input" ||
		operationExcerpt(t, report, call.Arguments[0].Value.Anchor) != "input" {
		t.Fatalf("input -> body mapping is not source-backed: %+v", call.Arguments)
	}
	if len(call.Results) != 2 {
		t.Fatalf("want two result-position mappings, got %+v", call.Results)
	}
	for position, name := range []string{"event", "err"} {
		result := call.Results[position]
		if result.Position != position || result.Destination == nil || result.Destination.Name != name ||
			result.Status != "resolved" || operationExcerpt(t, report, result.Destination.Anchor) != name {
			t.Fatalf("result position %d did not bind to %q: %+v", position, name, result)
		}
	}
	returns := map[int]operationReturnJSON{}
	for _, result := range sources.Returns {
		if result.CallableKey == decode.Key {
			returns[result.Position] = result
		}
	}
	if got := operationExcerpt(t, report, returns[0].Value.Anchor); got != "Event{Body: body}" {
		t.Fatalf("result position 0 lost its original expression: %q", got)
	}
	if got := operationExcerpt(t, report, returns[1].Value.Anchor); got != "nil" {
		t.Fatalf("result position 1 lost its original expression: %q", got)
	}
}

func TestOperationSourcePositionsAndIdentity(t *testing.T) {
	const source = `package source
type Box struct { callback func(int) int; ptr *int }
type Worker struct{}
func (Worker) Work(value int) int { return identity(value) }
func identity[T any](value T) T { return value }
func variadic(head int, rest ...int) {}
func pair() (int, string) { return 1, "é" }
func unnamed() (int, string) { return 1, "café" }
func bare() (value int) { value = 1; return }
func apply(box *Box, worker Worker, value int, callback func(int) int) {
	worker.Work(value)
	variadic(value, 1, 2)
	first, second := pair()
	_, discarded := pair()
	_ = first
	_ = second
	_ = discarded
	_ = identity(value)
	{ value := "shadow"; _ = identity(value) }
	box.callback = callback
	box.ptr = &value
	box.callback(value)
	_ = func() int { return value }()
}
var initialized = Box{callback: func(n int) int { return n }}
var genericValue = identity[int]
`
	report := analyzeOperationSourceFixture(t, map[string]string{"source.go": source}, nil)
	sources := readOperationSources(t, report)

	methodCall := callWithExcerpt(t, report, sources, "worker.Work(value)")
	if methodCall.Receiver == nil || methodCall.Receiver.Name != "worker" ||
		operationExcerpt(t, report, methodCall.Receiver.Anchor) != "worker" {
		t.Fatalf("receiver method call lost its receiver expression: %+v", methodCall)
	}
	results := callableNamed(t, sources, "unnamed").Results
	if len(results) != 2 || results[0].Position != 0 || results[1].Position != 1 ||
		results[0].Value.Name != "" || results[1].Value.Name != "" {
		t.Fatalf("unnamed result positions were not retained: %+v", results)
	}

	variadic := callWithExcerpt(t, report, sources, "variadic(value, 1, 2)")
	if len(variadic.Arguments) != 3 {
		t.Fatalf("variadic arguments missing: %+v", variadic.Arguments)
	}
	for i, wantPosition := range []int{0, 1, 1} {
		if variadic.Arguments[i].ParameterPosition != wantPosition ||
			variadic.Arguments[i].Status != "resolved" {
			t.Fatalf("variadic argument %d mapped incorrectly: %+v", i, variadic.Arguments[i])
		}
	}

	var pairCalls []operationCallJSON
	for _, call := range sources.Calls {
		if operationExcerpt(t, report, call.Anchor) == "pair()" {
			pairCalls = append(pairCalls, call)
		}
	}
	var sawTuple, sawDiscard bool
	for _, call := range pairCalls {
		if len(call.Results) != 2 {
			continue
		}
		if call.Results[0].Destination != nil && call.Results[0].Destination.Name == "first" &&
			call.Results[1].Destination != nil && call.Results[1].Destination.Name == "second" {
			sawTuple = true
		}
		if call.Results[0].Destination == nil && call.Results[0].Status == "discarded" &&
			call.Results[1].Destination != nil && call.Results[1].Destination.Name == "discarded" {
			sawDiscard = true
		}
	}
	if !sawTuple || !sawDiscard {
		t.Fatalf("multi-value destinations or discarded result were fabricated/lost: %+v", pairCalls)
	}

	var identityCalls []operationCallJSON
	apply := callableNamed(t, sources, "apply")
	for _, call := range sources.Calls {
		if call.CallerKey == apply.Key && operationExcerpt(t, report, call.Anchor) == "identity(value)" {
			identityCalls = append(identityCalls, call)
		}
	}
	if len(identityCalls) != 2 {
		t.Fatalf("want outer and shadowed generic calls, got %d", len(identityCalls))
	}
	outer, inner := identityCalls[0].Arguments[0].Value, identityCalls[1].Arguments[0].Value
	if outer.Name != "value" || inner.Name != "value" || outer.ID == inner.ID ||
		outer.Type == inner.Type || operationExcerpt(t, report, outer.Anchor) != "value" ||
		operationExcerpt(t, report, inner.Anchor) != "value" {
		t.Fatalf("same-named shadowed objects were conflated: %+v / %+v", outer, inner)
	}

	bare := callableNamed(t, sources, "bare")
	foundBare := false
	for _, result := range sources.Returns {
		if result.CallableKey == bare.Key && result.Position == 0 &&
			result.Status == "resolved" && result.Value != nil && result.Value.Name == "value" {
			foundBare = true
			if got := operationExcerpt(t, report, result.Anchor); got != "return" {
				t.Fatalf("bare return site range is not exact: %q", got)
			}
		}
	}
	if !foundBare {
		t.Fatal("bare return did not retain its named result identity")
	}

	var callbackStore, pointerStore bool
	for _, store := range sources.FieldStores {
		if store.FieldName == "callback" && store.Kind == "assignment" &&
			store.Value.Name == "callback" && store.FieldKey != "" {
			callbackStore = true
		}
		if store.FieldName == "ptr" && store.Kind == "assignment" &&
			store.Value.Expression == "&value" {
			pointerStore = true
		}
	}
	if !callbackStore || !pointerStore {
		t.Fatalf("field writes/callback wiring missing: %+v", sources.FieldStores)
	}
	var closureFound, packageInitializerFound bool
	for _, callable := range sources.Callables {
		closureFound = closureFound || callable.Kind == "closure"
		packageInitializerFound = packageInitializerFound || callable.Kind == "initializer"
	}
	if !closureFound || !packageInitializerFound {
		t.Fatalf("closure or package initializer identity missing: closures=%t initializer=%t", closureFound, packageInitializerFound)
	}

	// All ranges are token offsets over UTF-8 bytes, not rune-indexed positions.
	for _, call := range sources.Calls {
		excerpt := operationExcerpt(t, report, call.Anchor)
		if strings.Contains(excerpt, "café") && !strings.Contains(string([]byte(source)[call.Anchor.Location.Offset:call.Anchor.Location.EndOffset]), "café") {
			t.Fatalf("Unicode source range is not byte-accurate: %q", excerpt)
		}
	}
	if got := operationExcerpt(t, report, func() operationAnchorJSON {
		for _, result := range sources.Returns {
			if result.CallableKey == callableNamed(t, sources, "unnamed").Key && result.Position == 1 && result.Value != nil {
				return result.Value.Anchor
			}
		}
		t.Fatal("unnamed result expression missing")
		return operationAnchorJSON{}
	}()); got != `"café"` {
		t.Fatalf("UTF-8 return expression range is wrong: %q", got)
	}

	if len(sources.Bindings) == 0 {
		t.Fatal("source assignment and function-value bindings were not collected")
	}
}

func TestOperationSourceIncompleteTypesAndExcludedBodies(t *testing.T) {
	included := `package source
func included() { unresolved(1) }
func callsExcluded() { hidden() }
func broken(value Missing) Missing { return value }
`
	excluded := `package source
func hidden() { unresolved(2) }
`
	generated := `// Code generated by fixture. DO NOT EDIT.
package source
func generated() { unresolved(3) }
`
	root := t.TempDir()
	writeReviewFixture(t, root, "go.mod", "module example.test/source\n\ngo 1.24\n")
	writeReviewFixture(t, root, "included.go", included)
	writeReviewFixture(t, root, "excluded.go", excluded)
	writeReviewFixture(t, root, "generated.go", generated)
	scope := &ReviewScope{Root: root, ExcludePaths: []string{"excluded.go"}}
	report, err := Analyze(context.Background(), Options{Dir: root, Scope: scope})
	if err != nil {
		t.Fatal(err)
	}
	sources := readOperationSources(t, report)
	for _, callable := range sources.Callables {
		if callable.Name == "hidden" || callable.Name == "generated" {
			t.Fatalf("excluded implementation re-entered operation facts: %+v", callable)
		}
	}
	for _, call := range sources.Calls {
		excerpt := operationExcerpt(t, report, call.Anchor)
		if strings.Contains(excerpt, "unresolved(2)") || strings.Contains(excerpt, "unresolved(3)") {
			t.Fatalf("excluded body re-entered operation facts: %q", excerpt)
		}
	}
	broken := callableNamed(t, sources, "broken")
	if len(broken.Parameters) != 1 || broken.Parameters[0].Value.Type == "" ||
		(broken.Parameters[0].Value.Type != "<unknown>" && broken.Parameters[0].Value.Type != "invalid type") {
		t.Fatalf("incomplete parameter type was presented as known: %+v", broken.Parameters)
	}

	for _, anchor := range allOperationAnchors(sources) {
		if anchor.Module != report.Module || anchor.SourceSHA256 != report.SourceSHA256 {
			t.Fatalf("source anchor is not bound to the finalized snapshot: %+v report=%s/%s", anchor, report.Module, report.SourceSHA256)
		}
		_ = operationExcerpt(t, report, anchor)
	}
}

func allOperationAnchors(sources operationSourceFixture) []operationAnchorJSON {
	var anchors []operationAnchorJSON
	for _, callable := range sources.Callables {
		anchors = append(anchors, callable.Anchor)
		for _, value := range callable.Parameters {
			anchors = append(anchors, value.Value.Anchor)
		}
		for _, value := range callable.Results {
			anchors = append(anchors, value.Value.Anchor)
		}
		if callable.ReceiverValue != nil {
			anchors = append(anchors, callable.ReceiverValue.Anchor)
		}
	}
	for _, call := range sources.Calls {
		anchors = append(anchors, call.Anchor)
		if call.Receiver != nil {
			anchors = append(anchors, call.Receiver.Anchor)
		}
		for _, arg := range call.Arguments {
			anchors = append(anchors, arg.Value.Anchor)
		}
		for _, result := range call.Results {
			anchors = append(anchors, result.Anchor)
			if result.Destination != nil {
				anchors = append(anchors, result.Destination.Anchor)
			}
		}
	}
	for _, result := range sources.Returns {
		anchors = append(anchors, result.Anchor)
		if result.Value != nil {
			anchors = append(anchors, result.Value.Anchor)
		}
	}
	for _, binding := range sources.Bindings {
		anchors = append(anchors, binding.Anchor)
		if binding.Source != nil {
			anchors = append(anchors, binding.Source.Anchor)
		}
		if binding.Destination != nil {
			anchors = append(anchors, binding.Destination.Anchor)
		}
	}
	for _, store := range sources.FieldStores {
		anchors = append(anchors, store.Anchor, store.Receiver.Anchor, store.Value.Anchor)
	}
	return anchors
}
