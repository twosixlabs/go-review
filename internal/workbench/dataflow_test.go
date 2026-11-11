package workbench

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/russellbrasser/go-review/internal/review"
)

func TestDataFlowAPIRequiresMatchingSnapshot(t *testing.T) {
	report, err := review.Analyze(context.Background(), review.Options{Dir: "../review/testdata/module"})
	if err != nil {
		t.Fatal(err)
	}
	var id string
	for _, s := range report.Symbols {
		if s.Name == "Flow" {
			id = s.ID
		}
	}
	if id == "" {
		t.Fatal("fixture function missing")
	}
	report.Root = "/unavailable/retained-checkout"
	commandCount := len(report.Commands)
	h := New(report).Handler()
	for _, tc := range []struct {
		body string
		code int
	}{
		{`{}`, 400},
		{`{"symbolID":"` + id + `","sourceSHA256":"old"}`, 409},
		{`{"symbolID":"missing","sourceSHA256":"` + report.SourceSHA256 + `"}`, 422},
		{`{"symbolID":"` + id + `","sourceSHA256":"` + report.SourceSHA256 + `"}`, 200},
	} {
		r := httptest.NewRequest("POST", "http://127.0.0.1:8090/api/dataflow", strings.NewReader(tc.body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.code {
			t.Fatalf("want %d got %d: %s", tc.code, w.Code, w.Body.String())
		}
		if tc.code == 200 {
			var data struct {
				SymbolID     string            `json:"symbolID"`
				SourceSHA256 string            `json:"sourceSHA256"`
				Nodes        []json.RawMessage `json:"nodes"`
				Edges        []json.RawMessage `json:"edges"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
				t.Fatal(err)
			}
			if data.SymbolID != id || data.SourceSHA256 != report.SourceSHA256 || len(data.Nodes) == 0 || len(data.Edges) == 0 {
				t.Fatal("missing source-attributed definition/use graph")
			}
		}
	}
	if report.Trace != nil {
		t.Fatal("data-flow analysis executed tests")
	}
	if len(report.Commands) != commandCount {
		t.Fatal("retained graph read ran Go tooling")
	}
	report.DataFlows = nil
	r := httptest.NewRequest("POST", "http://127.0.0.1:8090/api/dataflow", strings.NewReader(`{"symbolID":"`+id+`","sourceSHA256":"`+report.SourceSHA256+`"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 422 || !strings.Contains(w.Body.String(), "not collected") {
		t.Fatalf("legacy absence was not explicit: %d %s", w.Code, w.Body.String())
	}
}
