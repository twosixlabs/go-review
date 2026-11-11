package review

import "testing"

func TestPartialRecordingFiltersFailedIncompleteAndAmbiguousLanes(t *testing.T) {
	symbols := []Symbol{
		{ID: "good", Name: "TestGood", Package: "app", Kind: "test", Location: Location{FileID: "tests"}},
		{ID: "bad", Name: "TestBad", Package: "app", Kind: "test", Location: Location{FileID: "tests"}},
		{ID: "call", Name: "Work", Package: "app", Kind: "function", Location: Location{FileID: "prod"}},
	}
	files := []File{{ID: "tests", Path: "app_test.go"}, {ID: "prod", Path: "work.go"}}
	events := []TraceEvent{
		{Process: "p", Goroutine: "1", Sequence: 1, SymbolID: "good", Action: "enter"},
		{Process: "p", Goroutine: "1", Sequence: 2, SymbolID: "call", Action: "enter"},
		{Process: "p", Goroutine: "1", Sequence: 3, SymbolID: "call", Action: "exit"},
		{Process: "p", Goroutine: "1", Sequence: 4, SymbolID: "good", Action: "exit"},
		{Process: "p", Goroutine: "2", Sequence: 5, SymbolID: "bad", Action: "enter"},
		{Process: "p", Goroutine: "2", Sequence: 6, SymbolID: "call", Action: "enter"},
		{Process: "p", Goroutine: "3", Sequence: 7, SymbolID: "call", Action: "enter"},
		{Process: "p", Goroutine: "3", Sequence: 8, SymbolID: "call", Action: "exit"},
	}
	for _, scenario := range []struct{ name, outcome string }{
		{"failed", "fail"}, {"timed out without terminal outcome", ""}, {"skipped", "skip"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			trace := &TraceEvidence{Events: append([]TraceEvent{}, events...), BaselineOutcomes: map[string]string{"app::TestGood": "pass", "app::TestBad": "pass"}, InstrumentedOutcomes: map[string]string{"app::TestGood": "pass"}, InstrumentedExitCode: 1}
			if scenario.outcome != "" {
				trace.InstrumentedOutcomes["app::TestBad"] = scenario.outcome
			}
			filterRecordedEvents(trace, symbols, files)
			if len(trace.Events) != 2 {
				t.Fatalf("expected only complete passing test, got %+v", trace.Events)
			}
			if !trace.Partial || trace.DroppedEvents != 6 || len(trace.ExcludedTests) != 1 {
				t.Fatalf("missing partial/exclusion diagnostics: %+v", trace)
			}
			for i, e := range trace.Events {
				if e.Sequence != uint64(i+2) || e.Test != "app::TestGood" {
					t.Fatal("retained ordering changed")
				}
			}
		})
	}
	t.Run("baseline failure discards otherwise passing recorded test", func(t *testing.T) {
		trace := &TraceEvidence{Events: events[:4], BaselineExitCode: 1, BaselineOutcomes: map[string]string{"app::TestGood": "fail"}, InstrumentedOutcomes: map[string]string{"app::TestGood": "pass"}}
		filterRecordedEvents(trace, symbols, files)
		if len(trace.Events) != 0 || !trace.Partial {
			t.Fatal("baseline failure observations retained")
		}
	})
	t.Run("successful recording preserves unattributed goroutines", func(t *testing.T) {
		trace := &TraceEvidence{Events: events[6:], OutcomesMatch: true, BaselineOutcomes: map[string]string{"app::TestGood": "pass"}, InstrumentedOutcomes: map[string]string{"app::TestGood": "pass"}}
		filterRecordedEvents(trace, symbols, files)
		if len(trace.Events) != 2 || trace.Partial {
			t.Fatal("healthy goroutine evidence lost")
		}
	})
	t.Run("failed subtest excludes enclosing root", func(t *testing.T) {
		trace := &TraceEvidence{Events: events[:4], BaselineExitCode: 1, InstrumentedExitCode: 1, OutcomesMatch: true, BaselineOutcomes: map[string]string{"app::TestGood": "fail", "app::TestGood/child": "fail"}, InstrumentedOutcomes: map[string]string{"app::TestGood": "fail", "app::TestGood/child": "fail"}}
		filterRecordedEvents(trace, symbols, files)
		if len(trace.Events) != 0 || len(trace.ExcludedTests) != 1 {
			t.Fatal("failed subtest retained as parent observations")
		}
	})
	t.Run("skipped subtest cannot contribute parent observations", func(t *testing.T) {
		trace := &TraceEvidence{Events: events[:4], OutcomesMatch: true, BaselineOutcomes: map[string]string{"app::TestGood": "pass", "app::TestGood/child": "skip"}, InstrumentedOutcomes: map[string]string{"app::TestGood": "pass", "app::TestGood/child": "skip"}}
		filterRecordedEvents(trace, symbols, files)
		if len(trace.Events) != 0 || !trace.Partial {
			t.Fatal("skipped subtest observations retained through parent frame")
		}
	})
}

func TestPartialRecordingTracksStartedTestsWithoutTerminalOutcome(t *testing.T) {
	outcomes, err := unitTestOutcomes([]byte("{\"Action\":\"run\",\"Package\":\"app\",\"Test\":\"TestTimedOut\"}\n{\"Action\":\"run\",\"Package\":\"app\",\"Test\":\"TestGood\"}\n{\"Action\":\"pass\",\"Package\":\"app\",\"Test\":\"TestGood\"}\n{\"Action\":\"fail\",\"Package\":\"app\"}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if outcomes["app::TestTimedOut"] != "incomplete" || outcomes["app::TestGood"] != "pass" || outcomes["app::"] != "fail" {
		t.Fatalf("timeout lost from outcome diagnostics: %+v", outcomes)
	}
}
