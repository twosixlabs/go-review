package review

import (
	"encoding/json"
	"strings"
	"testing"
)

type systemFlowFixture struct {
	Components []struct {
		ID, Name, Kind, Package string
		Members                 []string
	} `json:"components"`
	Operations []struct {
		ID, Name, ComponentID string
	} `json:"operations"`
	Exchanges []struct {
		ID, From, To, Kind, Resolution string
		Information                    []struct{ Name, Type, Direction string }
		Sources                        []Location
	} `json:"exchanges"`
}

func readSystemFlow(t *testing.T, report *Report) systemFlowFixture {
	t.Helper()
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		SystemFlow *systemFlowFixture `json:"systemFlow"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.SystemFlow == nil {
		t.Fatal("analysis has no architectural system flow")
	}
	return *doc.SystemFlow
}

func TestSystemFlowDescribesComponentsAndInformationWithoutExecution(t *testing.T) {
	report := analyzeOperationSourceFixture(t, map[string]string{
		"service/service.go": `package service
type Request struct { Name string }
type Receipt struct { ID string }
type Record struct { Name string }
type Repository interface { Save(Record) (Receipt, error) }
type Service struct { repository Repository }
func New(r Repository) *Service { return &Service{repository:r} }
func (s *Service) Create(input Request) (Receipt,error) {
	if input.Name == "" { return Receipt{}, nil }
	record := Record{Name: input.Name}
	return s.repository.Save(record)
}
`,
		"storage/storage.go": `package storage
import "example.test/source/service"
type Memory struct { records map[string]service.Record }
func New() *Memory { return &Memory{records: map[string]service.Record{}} }
func (m *Memory) Save(record service.Record) (service.Receipt,error) {
	m.records[record.Name] = record
	return service.Receipt{ID:record.Name},nil
}
`,
		"api/api.go": `package api
import (
	"net/http"
	"example.test/source/service"
	"example.test/source/storage"
)
type API struct { service *service.Service }
func New() http.Handler {
	api := &API{service: service.New(storage.New())}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /records", api.create)
	return mux
}
func (a *API) create(w http.ResponseWriter,r *http.Request) {
	receipt, err := a.service.Create(service.Request{Name:r.URL.Query().Get("name")})
	if err != nil { http.Error(w,err.Error(),500); return }
	_, _ = w.Write([]byte(receipt.ID))
}
`,
		"service/service_test.go": `package service
import "testing"
type fakeRepository struct{}
func (fakeRepository) Save(Record)(Receipt,error) { return Receipt{},nil }
func TestUnselected(t *testing.T) { t.Fatal("must not execute") }
`,
	}, nil)
	if report.Trace != nil || report.Coverage != nil {
		t.Fatal("static flow requires execution")
	}
	flow := readSystemFlow(t, report)
	names := map[string]string{}
	for _, c := range flow.Components {
		names[c.ID] = c.Name
		if c.Name == "Memory" && c.Kind != "memory-store" {
			t.Errorf("receiver-owned map writes are not described as an in-memory store: %+v", c)
		}
		if strings.Contains(c.Name, "fake") || strings.Contains(c.Name, "TestUnselected") {
			t.Fatalf("test implementation became a component: %+v", c)
		}
	}
	for _, want := range []string{"API", "Service", "Memory"} {
		found := false
		for _, name := range names {
			found = found || name == want
		}
		if !found {
			t.Errorf("missing major component %s: %+v", want, names)
		}
	}
	wired := false
	for _, e := range flow.Exchanges {
		if names[e.From] == "Service" && names[e.To] == "Memory" && e.Resolution == "inferred" {
			wired = true
		}
	}
	if !wired {
		t.Error("constructor-to-field wiring did not connect Service to Memory")
	}
	foundRequest, foundRecord, foundResponse := false, false, false
	for _, e := range flow.Exchanges {
		if len(e.Sources) == 0 {
			t.Errorf("exchange lacks source: %+v", e)
		}
		for _, info := range e.Information {
			if names[e.From] == "API" && names[e.To] == "Service" && strings.HasSuffix(info.Type, ".Request") && info.Direction == "input" {
				foundRequest = true
			}
			if info.Name == "record" || strings.HasSuffix(info.Type, ".Record") {
				foundRecord = true
			}
			if strings.HasSuffix(info.Type, ".Receipt") && info.Direction == "output" {
				foundResponse = true
			}
		}
	}
	if !foundRequest || !foundRecord || !foundResponse {
		t.Fatalf("missing typed request/record/response exchange: request=%t record=%t response=%t flow=%+v", foundRequest, foundRecord, foundResponse, flow)
	}
}

func TestSystemFlowRetainsUnresolvedContractsAndTerminatesCycles(t *testing.T) {
	report := analyzeOperationSourceFixture(t, map[string]string{
		"source.go": `package source
type Event struct { Body string }
type Sink interface { Send(Event) error }
type Worker struct { sink Sink }
func (w *Worker) Run(e Event) error { helper(); return w.sink.Send(e) }
func helper() { cycle() }
func cycle() { helper() }
`,
	}, nil)
	flow := readSystemFlow(t, report)
	contract := false
	workerMembers := 0
	for _, c := range flow.Components {
		contract = contract || c.Kind == "contract"
		if c.Name == "Worker" {
			workerMembers = len(c.Members)
		}
	}
	if workerMembers != 3 {
		t.Errorf("recursive private helpers are not collapsed into Worker: %d members", workerMembers)
	}
	if !contract {
		t.Fatalf("unresolved production contract disappeared: %+v", flow)
	}
	for _, e := range flow.Exchanges {
		if e.From == "" || e.To == "" {
			t.Fatalf("dangling component exchange: %+v", e)
		}
	}
	errorContract := false
	for _, e := range flow.Exchanges {
		for _, info := range e.Information {
			errorContract = errorContract || info.Type == "error" && info.Direction == "output"
		}
	}
	if !errorContract {
		t.Error("unassigned interface return contract was lost")
	}
	first, _ := json.Marshal(BuildSystemFlow(report))
	second, _ := json.Marshal(BuildSystemFlow(report))
	if string(first) != string(second) {
		t.Fatal("architectural identities are nondeterministic")
	}
}

func TestSystemFlowGroupsExternalCapabilitiesAndRetainsUnknownIngress(t *testing.T) {
	r := analyzeOperationSourceFixture(t, map[string]string{
		"source.go": `package source
	import ("fmt"; "net/http")
	func Serve(w http.ResponseWriter,r *http.Request) {
		fmt.Print("request")
		fmt.Println(r.Method)
	}

	`}, nil)
	r.HTTPEndpoints = append(r.HTTPEndpoints, HTTPEndpoint{
		ID: "dynamic-registration", Pattern: "/dynamic", PatternKnown: true,
		Handler: "selectedHandler", Reason: "handler assigned at runtime", Location: r.Symbols[0].Location,
	})
	r.SystemFlow = BuildSystemFlow(r)
	fmtBoundaries, unknownIngress := 0, false
	for _, c := range r.SystemFlow.Components {
		if c.Kind == "external" && c.Package == "fmt" {
			fmtBoundaries++
		}
	}
	for _, e := range r.SystemFlow.Exchanges {
		if e.Kind == "ingress" && e.Resolution == "unresolved" {
			unknownIngress = true
		}
	}
	if fmtBoundaries != 1 {
		t.Errorf("external package became per-function boundaries: %+v", r.SystemFlow.Components)
	}
	if !unknownIngress {
		t.Error("unresolved endpoint disappeared from the system boundary")
	}
}

func TestSystemFlowPreservesAmbiguousWiringAndResolvesNamedCallbacks(t *testing.T) {
	r := analyzeOperationSourceFixture(t, map[string]string{"source.go": `package source
type Sink interface { Send(string) }
type A struct{}
func (*A) Send(string) {}
type B struct{}
func (*B) Send(string) {}
type Worker struct {sink Sink}
func New(s Sink) *Worker {return &Worker{sink:s}}
func (w *Worker) Run(s string) {w.sink.Send(s)}
type Caller struct{}
func (*Caller) Dispatch(callback func(string)) {callback("event")}
func Handle(s string) {}
func Wire() {
	New(&A{}).Run("a")
	New(&B{}).Run("b")
	(&Caller{}).Dispatch(Handle)
}
`}, nil)
	names := map[string]string{}
	for _, c := range r.SystemFlow.Components {
		names[c.ID] = c.Name
	}
	ambiguous, callback := false, false
	for _, e := range r.SystemFlow.Exchanges {
		if names[e.From] == "Worker" {
			ambiguous = ambiguous || names[e.To] == "Sink" && e.Resolution == "unresolved"
			if names[e.To] == "A" || names[e.To] == "B" {
				t.Error("ambiguous constructor instances invented a unique implementation")
			}
		}
		if names[e.From] == "Caller" && names[e.To] == "source" && e.Resolution == "inferred" {
			callback = true
		}
	}
	if !ambiguous {
		t.Error("ambiguous wiring lost its contract boundary")
	}
	if !callback {
		t.Error("named production callback binding did not connect its owner")
	}
}

func TestSystemFlowDistinguishesStateFromPayloadAndDurableBoundary(t *testing.T) {
	r := analyzeOperationSourceFixture(t, map[string]string{"source.go": `package source
import "database/sql"
type Payload struct {Fields map[string]string}
func (p Payload) Size() int {return len(p.Fields)}
type DiskRepository struct {db *sql.DB}
func (r *DiskRepository) Save(body string) error {
	_,err:=r.db.Exec("insert into records(body) values (?)",body)
	return err
}
`}, nil)
	durable := false
	for _, c := range r.SystemFlow.Components {
		if c.Name == "Payload" && strings.Contains(c.Kind, "store") {
			t.Error("a read-only payload map became a data store")
		}
		if c.Name == "DB" && c.Package == "database/sql" && c.Kind == "durable-store" {
			durable = true
		}
	}
	if !durable {
		t.Error("typed SQL boundary is not distinguished from memory state")
	}
}
