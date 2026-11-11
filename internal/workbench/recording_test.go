package workbench

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/russellbrasser/go-review/internal/review"
)

func TestPartialRecordingReopenedResultsFilterWithoutExecution(t *testing.T) {
	run := &review.RepositoryReview{Modules: []review.ReviewedModule{{Report: &review.Report{
		Symbols: []review.Symbol{{ID: "good", Name: "TestGood", Package: "app", Kind: "test"}, {ID: "bad", Name: "TestBad", Package: "app", Kind: "test"}},
		Trace: &review.TraceEvidence{
			Events: []review.TraceEvent{
				{Process: "p", Goroutine: "1", SymbolID: "good", Action: "enter", Sequence: 1},
				{Process: "p", Goroutine: "1", SymbolID: "good", Action: "exit", Sequence: 2},
				{Process: "p", Goroutine: "1", SymbolID: "bad", Action: "enter", Sequence: 3},
				{Process: "p", Goroutine: "1", SymbolID: "bad", Action: "exit", Sequence: 4},
			},
			BaselineOutcomes:     map[string]string{"app::TestGood": "pass", "app::TestBad": "pass"},
			InstrumentedOutcomes: map[string]string{"app::TestGood": "pass", "app::TestBad": "fail"},
			InstrumentedExitCode: 1,
		},
	}}}}
	for i := 0; i < 2; i++ {
		server := NewReviewed(run)
		w := httptest.NewRecorder()
		server.Handler().ServeHTTP(w, httptest.NewRequest("GET", "http://127.0.0.1:8090/api/snapshot", nil))
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		var result review.Report
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if len(result.Trace.Events) != 2 || !result.Trace.Partial || result.Trace.DroppedEvents != 2 || len(result.Trace.ExcludedTests) != 1 {
			t.Fatalf("reopened observations not filtered idempotently: %+v", result.Trace)
		}
		if server.reviewRunning || len(result.Commands) != 0 {
			t.Fatal("browsing retained results executed collection")
		}
	}
}
