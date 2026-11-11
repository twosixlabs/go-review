package review

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFlowRuntimeStandardEventsDistinguishConcurrentLaunchers(t *testing.T) {
	r := analyzeOperationSourceFixture(t, map[string]string{
		"source.go": `package source
type Worker struct{}
func (Worker) Wait(ready, finish, done chan struct{}) { defer close(done); close(ready); <-finish }
type Service struct{ Worker Worker }
func (s Service) Selected(ready, finish, done chan struct{}) { go s.Worker.Wait(ready, finish, done) }
func (s Service) Alternative(ready, finish, done chan struct{}) { go s.Worker.Wait(ready, finish, done) }
`,
		"source_test.go": `package source
import ("runtime";"strings";"testing")
func TestSelected(t *testing.T) {
	ready,finish,done:=make(chan struct{}),make(chan struct{}),make(chan struct{})
	defer func(){close(finish);<-done}()
	Service{}.Selected(ready,finish,done)
	<-ready
	for i:=0;i<10000;i++ {
		buf:=make([]byte,65536);n:=runtime.Stack(buf,true)
		if strings.Contains(string(buf[:n]),"[chan receive]:\nexample.test/source.Worker.Wait") {return}
		runtime.Gosched()
	}
	t.Fatal("worker never reached its channel wait")
}
func TestUnselected(t *testing.T){t.Fatal("unselected sentinel")}
`,
	}, nil)
	if err := CollectUnitEvidence(context.Background(), r, UnitEvidenceOptions{
		ArtifactDir: t.TempDir(), RuntimeTrace: true,
		Selections: []UnitTestSelection{{Package: ".", Tests: []string{"TestSelected"}}},
	}); err != nil {
		t.Fatal(err)
	}
	run := r.UnitEvidence.Runs[0]
	if run.Runtime == nil || run.Runtime.Status != "available" {
		t.Fatalf("standard events were not interpreted: %+v", run.Runtime)
	}
	launch, block, wake := false, false, false
	for _, observation := range run.Runtime.Observations {
		if observation.Kind == "launch" {
			for _, frame := range observation.Stack {
				launch = launch || strings.Contains(frame.Function, "Service.Selected")
				if strings.Contains(frame.Function, "Service.Alternative") {
					t.Fatal("unexecuted static launcher became runtime evidence")
				}
			}
			if len(observation.ExchangeIDs) != 1 {
				t.Fatalf("launch did not distinguish its source-supported exchange: %+v", observation)
			}
		}
		block = block || observation.Kind == "block" && observation.Reason == "chan receive"
		wake = wake || observation.Kind == "wakeup"
	}
	if !launch || !block || !wake {
		t.Fatalf("missing launch/block/wakeup evidence: %+v", run.Runtime.Observations)
	}
	for _, path := range []string{run.RuntimeTracePath, run.Runtime.ParsedPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal("runtime artifact not retained:", err)
		}
	}
	if run.Runtime.SourceSHA256 != r.SourceSHA256 || run.Runtime.TraceSHA256 == "" || run.Runtime.ToolVersion == "" {
		t.Fatal("runtime provenance missing")
	}
}

func TestFlowRuntimeAdapterRejectsUnsupportedAndMalformedEvents(t *testing.T) {
	for _, data := range []string{
		"not trace event output",
		"M=1 P=0 G=2 StateTransition Time=1 Resource=Goroutine(3) Running->Waiting",
		"M=1 P=0 G=2 StateTransition Time=1 GoID=3 malformed",
		"M=1 P=0 G=2 StateTransition Time=1 GoID=3 Running->Waiting Reason=\"chan receive\"\nStack=\n\tworker @ 0x1\n",
	} {
		if _, err := parseStandardRuntime(strings.NewReader(data)); err == nil {
			t.Fatalf("unsupported/truncated text was accepted: %q", data)
		}
	}
}

func TestFlowRuntimeMissingTruncatedAndMismatchedTraceKeepArchitectureUsable(t *testing.T) {
	r, _ := flowExecutionFixture(t)
	for _, tc := range []struct{ name, data, source string }{
		{"missing", "", r.SourceSHA256},
		{"truncated", "go 1.27 trace\x00\x00\x00\x00", r.SourceSHA256},
		{"mismatch", "not read", "different-source"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := UnitTestRun{RuntimeTracePath: filepath.Join(t.TempDir(), "runtime.trace")}
			if tc.data != "" {
				if err := os.WriteFile(run.RuntimeTracePath, []byte(tc.data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := collectStandardRuntime(context.Background(), r, &run, tc.source); err == nil {
				t.Fatal("invalid runtime evidence accepted")
			}
			if run.Runtime == nil || run.Runtime.Status == "available" || run.Runtime.Error == "" || len(run.Runtime.Observations) != 0 {
				t.Fatalf("invalid evidence looks successful: %+v", run.Runtime)
			}
			if BuildSystemFlow(r) == nil {
				t.Fatal("optional trace failure lost architectural model")
			}
		})
	}
}

func TestFlowRuntimeNoStacksDoesNotInventNormalCallHistory(t *testing.T) {
	events, err := parseStandardRuntime(strings.NewReader("M=1 P=0 G=2 StateTransition Time=1 GoID=3 Waiting->Runnable Reason=\"\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	r, _ := flowExecutionFixture(t)
	if observations := runtimeObservations(r, events); len(observations) != 0 {
		t.Fatalf("stackless scheduler event manufactured production calls: %+v", observations)
	}
}

func TestFlowRuntimeParsedGoIDStackFixture(t *testing.T) {
	events, err := parseStandardRuntime(strings.NewReader(`M=1 P=0 G=37 StateTransition Time=42954036055488 GoID=38 NotExist->Runnable Reason=""
TransitionStack=
	example.test/source.Worker.Wait @ 0x100eac4e3
		C:\repo\source.go:3

Stack=
	example.test/source.Service.Selected @ 0x100eac677
		C:\repo\source.go:5

M=1 P=0 G=38 StateTransition Time=42954036056576 GoID=38 Running->Waiting Reason="chan receive"
TransitionStack=
	example.test/source.Worker.Wait @ 0x100eac837
		/repo/source.go:3
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].target != "38" || events[0].current != "37" ||
		events[0].stack[0].Function != "example.test/source.Service.Selected" ||
		events[0].stack[0].File != `C:\repo\source.go` || events[1].reason != "chan receive" {
		t.Fatalf("event/stack association lost: %+v", events)
	}
}

func TestFlowRuntimeRetainedMismatchedSourceWithdrawsObservations(t *testing.T) {
	r, _ := flowExecutionFixture(t)
	r.UnitEvidence = &UnitEvidence{SourceSHA256: r.SourceSHA256, Runs: []UnitTestRun{{
		Runtime: &RuntimeEvidence{Status: "available", SourceSHA256: "other", Observations: []RuntimeObservation{{Kind: "launch"}}},
	}}}
	PrepareSystemFlow(r)
	evidence := r.UnitEvidence.Runs[0].Runtime
	if evidence.Status != "stale" || evidence.Error == "" || len(evidence.Observations) != 0 {
		t.Fatalf("mismatched retained events were joined: %+v", evidence)
	}
}
