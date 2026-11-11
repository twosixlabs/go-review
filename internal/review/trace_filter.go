package review

import (
	"fmt"
	"sort"
	"strings"
)

// PrepareRecordedObservations filters legacy custom-trace fields when reopening
// historical snapshots. Live collection uses UnitEvidence, not this recorder
// schema. The policy marker preserves omission counts across repeated opens.
func PrepareRecordedObservations(report *Report) {
	if report == nil || report.Trace == nil || report.Trace.ObservationPolicy == "passing-tests-v1" {
		return
	}
	trace := report.Trace
	filterRecordedEvents(trace, report.Symbols, report.Files)
	trace.ObservationPolicy = "passing-tests-v1"
	trace.Edges = observedEdges(trace.Events)
	if trace.Partial {
		report.Warnings = append(report.Warnings, fmt.Sprintf("Partial recording: %d events omitted; %d tests excluded. Only observations attributed to tests passing in both runs are retained; ambiguous goroutine observations are omitted. Raw execution output remains available.", trace.DroppedEvents, len(trace.ExcludedTests)))
	}
	trace.Limitations = append(trace.Limitations, "Failed, skipped or incomplete tests are excluded from retained observations. A failed or skipped subtest excludes its enclosing top-level test; in partial recordings, goroutine calls without a passing enclosing test frame are omitted. Raw event files remain diagnostic artifacts, not reviewed observations.")
}

// Filter before deriving graphs: a failed run must not erase unrelated passing
// tests, nor publish failed/incomplete test calls as usable review observations.
// Attribution is limited to enclosing test frames on the same goroutine.
func filterRecordedEvents(trace *TraceEvidence, symbols []Symbol, files []File) {
	tests := map[string]string{}
	testSourceSymbols := map[string]bool{}
	testSourceFiles := map[string]bool{}
	for _, file := range files {
		if strings.HasSuffix(file.Path, "_test.go") {
			testSourceFiles[file.ID] = true
		}
	}
	for _, s := range symbols {
		if testSourceFiles[s.FileID] {
			testSourceSymbols[s.ID] = true
		}
		if s.Kind == "test" {
			pkg := s.Package
			key := pkg + "::" + s.Name
			if _, ok := trace.BaselineOutcomes[key]; !ok {
				if _, ok = trace.InstrumentedOutcomes[key]; !ok {
					pkg = strings.TrimSuffix(pkg, "_test")
				}
			}
			tests[s.ID] = pkg + "::" + s.Name
		}
	}
	excluded := map[string]string{}
	check := func(key string) bool {
		if _, ok := excluded[key]; ok {
			return false
		}
		base, recorded := trace.BaselineOutcomes[key], trace.InstrumentedOutcomes[key]
		if base == "pass" && recorded == "pass" {
			return true
		}
		if base == "" {
			base = "incomplete or not reported"
		}
		if recorded == "" {
			recorded = "incomplete or not reported"
		}
		excluded[key] = fmt.Sprintf("baseline: %s; recorded: %s", base, recorded)
		return false
	}
	keys := map[string]bool{}
	for _, outcomes := range []map[string]string{trace.BaselineOutcomes, trace.InstrumentedOutcomes} {
		for key := range outcomes {
			keys[key] = true
		}
	}
	ordered := make([]string, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)
	for _, key := range ordered {
		pkg, name, ok := strings.Cut(key, "::")
		// A failed subtest invalidates its enclosing top-level test. We
		// cannot distinguish sequential subtest bodies from its other calls.
		if ok && name != "" {
			parent, _, subtest := strings.Cut(name, "/")
			parentKey := pkg + "::" + parent
			check(parentKey)
			if subtest && (trace.BaselineOutcomes[key] != "pass" || trace.InstrumentedOutcomes[key] != "pass") {
				excluded[parentKey] = "Subtest " + key + " did not pass in both runs; enclosing test observations omitted"
			}
		}
	}
	for _, event := range trace.Events {
		if key := tests[event.SymbolID]; key != "" {
			check(key)
		}
	}
	healthy := trace.OutcomesMatch && trace.BaselineExitCode == 0 && trace.InstrumentedExitCode == 0 && len(excluded) == 0
	type frame struct{ symbol, owner string }
	stacks := map[string][]frame{}
	retained := make([]TraceEvent, 0, len(trace.Events))
	for _, event := range trace.Events {
		lane := event.Process + ":" + event.Goroutine
		stack := stacks[lane]
		owner := ""
		if len(stack) > 0 {
			owner = stack[len(stack)-1].owner
		}
		if event.Action == "enter" {
			if owner == "" {
				owner = tests[event.SymbolID]
			}
			stacks[lane] = append(stack, frame{event.SymbolID, owner})
		} else if event.Action == "exit" {
			for i := len(stack) - 1; i >= 0; i-- {
				if stack[i].symbol == event.SymbolID {
					owner = stack[i].owner
					stacks[lane] = stack[:i]
					break
				}
			}
		}
		if owner != "" {
			if check(owner) && !testSourceSymbols[event.SymbolID] {
				event.Test = owner
				retained = append(retained, event)
			}
		} else if healthy && !testSourceSymbols[event.SymbolID] {
			retained = append(retained, event)
		}
	}
	trace.DroppedEvents = len(trace.Events) - len(retained)
	trace.Events = retained
	trace.Partial = !healthy || trace.DroppedEvents > 0
	for name, reason := range excluded {
		trace.ExcludedTests = append(trace.ExcludedTests, RecordingExclusion{Test: name, Reason: reason})
	}
	sort.Slice(trace.ExcludedTests, func(i, j int) bool { return trace.ExcludedTests[i].Test < trace.ExcludedTests[j].Test })
}
