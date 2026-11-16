package workbench

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/russellbrasser/go-review/internal/intelligence"
	"github.com/russellbrasser/go-review/internal/review"
	"github.com/russellbrasser/go-review/internal/testquality"
)

func TestQualityAgentRecordsResultsBeforeFinishing(t *testing.T) {
	identity := make(chan testquality.Identity, 1)
	waiting := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Tools []intelligence.Tool `json:"tools"`
		}

		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		call := calls.Add(1)
		if call == 1 {
			found := false
			for _, tool := range request.Tools {
				found = found || tool.Function.Name == "record_test"
			}
			if !found {
				http.Error(w, "agent has no record_test tool", http.StatusBadRequest)
				return
			}
		}
		var name string
		var args any
		switch call {
		case 1:
			id := <-identity
			identity <- id
			name = "review_source"
			args = map[string]any{"snapshotID": id.SnapshotID, "fileID": "test", "startLine": 3, "endLine": 3}
		case 2:
			id := <-identity
			name = "record_test"
			args = map[string]any{
				"snapshotID": id.SnapshotID, "symbolID": id.SymbolID,
				"craftsmanship": 3, "assertionStrength": 2, "rationale": "The return value is checked.",
				"fileID": "test", "startLine": 3, "endLine": 3,
			}
		default:
			close(waiting)
			<-release
			http.Error(w, "provider stopped", http.StatusServiceUnavailable)
			return
		}
		raw, _ := json.Marshal(args)
		delta := map[string]any{"tool_calls": []any{map[string]any{
			"index": 0, "id": fmt.Sprint(call), "function": map[string]string{"name": name, "arguments": string(raw)},
		}}}
		response, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta, "finish_reason": "tool_calls"}}})
		fmt.Fprintf(w, "data: %s\n\n", response)
	}))
	defer provider.Close()
	defer unblock()
	s := qualityFixture(t)
	s.ConfigureAgent(intelligence.Config{Enabled: true, BaseURL: provider.URL, Model: "fixture"})
	start := qualityRequest(t, s, "/api/test-quality/start", `{"allowExecution":false}`)
	if start.Code != http.StatusAccepted {
		t.Fatal(start.Body.String())
	}
	var response struct{ ID string }
	if err := json.Unmarshal(start.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	s.qualityMu.Lock()
	job := s.qualityJobs[response.ID]
	identity <- job.data.Items[0].Identity
	s.qualityMu.Unlock()
	defer func() {
		unblock()
		job.cancel()
		<-job.done
	}()
	select {
	case <-waiting:
	case <-job.done:
		s.qualityMu.Lock()
		message := job.data.Error
		s.qualityMu.Unlock()
		t.Fatalf("agent stopped before recording: %s", message)
	case <-time.After(5 * time.Second):
		t.Fatal("agent did not record its result")
	}
	saved, err := s.qualityStore.Load(response.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Status != "running" || saved.Items[0].Status != testquality.StatusScored ||
		!strings.Contains(saved.Items[0].Ratings[0].Rationale, "return value") {
		t.Fatalf("completed work was not persisted while agent continued: %+v", saved.Items)
	}
	if saved.Review.Requests < 2 {
		t.Fatalf("agent activity was not published: %+v", saved.Review)
	}
	unblock()
	select {
	case <-job.done:
	case <-time.After(5 * time.Second):
		t.Fatal("job did not terminate after provider error")
	}
	saved, err = s.qualityStore.Load(response.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Status != "failed" || saved.Items[0].Status != testquality.StatusScored {
		t.Fatalf("provider failure lost completed work: status=%s items=%+v", saved.Status, saved.Items)
	}
}

func TestQualityAgentRecordsIndependentProjectSynthesis(t *testing.T) {
	s := qualityFixture(t)
	a, err := s.qualityStore.Create(t.Context(), s.review, "", "")
	if err != nil {
		t.Fatal(err)
	}

	item := a.Items[0]
	seen := []testquality.Citation{item.Declaration}
	args, err := json.Marshal(map[string]any{
		"snapshotID": item.Identity.SnapshotID, "fileID": item.Declaration.FileID,
		"startLine": item.Declaration.StartLine, "endLine": item.Declaration.EndLine,
		"behaviorAdequacy": 2, "conditionAdequacy": 3, "rationale": "The behavior has focused checks.",
	})
	if err != nil {
		t.Fatal(err)
	}
	group := qualityAgentGroup{SnapshotID: item.Identity.SnapshotID, Package: item.Package, Tests: []testquality.Responsibility{item}}
	if err := s.recordQualityAgent(a, group, "record_group", args, seen); err != nil {
		t.Fatal(err)
	}
	if err := s.recordQualityAgent(a, qualityAgentGroup{Tests: a.Items}, "record_group", args, seen); err != nil {
		t.Fatal(err)
	}
	saved, err := s.qualityStore.Load(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Suite == nil || saved.Suite.Status != testquality.StatusScored || len(saved.Suite.Groups) != 1 || len(saved.Suite.Ratings) != 2 || *saved.Suite.Ratings[0].Value != 2 {
		t.Fatalf("group or project synthesis was lost: %+v", saved.Suite)
	}
	groups, err := s.queryTestQuality(t.Context(), testquality.QueryInput{AssessmentID: a.ID, Operation: "suite-groups", Limit: 1})
	if err != nil || !strings.Contains(string(groups), item.Package) || !strings.Contains(string(groups), `"scopeCount":1`) {
		t.Fatalf("saved group judgment unavailable through the review API: %s %v", groups, err)
	}
}

func TestQualityAgentRetainsMultipleFindingsForOneTest(t *testing.T) {
	s := qualityFixture(t)
	a, err := s.qualityStore.Create(t.Context(), s.review, "", "")
	if err != nil {
		t.Fatal(err)
	}
	item := a.Items[0]
	group := qualityAgentGroup{SnapshotID: item.Identity.SnapshotID, Package: item.Package, Tests: []testquality.Responsibility{item}}
	for _, title := range []string{"First concern", "Second concern"} {
		args, err := json.Marshal(map[string]any{
			"snapshotID": item.Identity.SnapshotID, "symbolID": item.Identity.SymbolID,
			"fileID": item.Declaration.FileID, "startLine": item.Declaration.StartLine, "endLine": item.Declaration.EndLine,
			"craftsmanship": 3, "assertionStrength": 2, "rationale": "The check has limitations.",
			"findingTitle": title, "findingRationale": "This deserves attention.",
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.recordQualityAgent(a, group, "record_test", args, []testquality.Citation{item.Declaration}); err != nil {
			t.Fatal(err)
		}
	}
	saved, err := s.qualityStore.Load(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Items[0].Findings) != 2 {
		t.Fatalf("later observation erased earlier finding: %+v", saved.Items[0].Findings)
	}
}

func TestQualityAgentComputesFactsOnlyOnDemand(t *testing.T) {
	s := qualityFixture(t)
	a, err := s.qualityStore.Create(t.Context(), s.review, "", "")
	if err != nil {
		t.Fatal(err)
	}
	options, closeSession, err := s.qualityEvidence(t.Context(), a, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeSession()
	if a.Analysis.ParsedSources != 0 {
		t.Fatal("opening the agent evidence tools eagerly analyzed the whole project")
	}
	args, err := json.Marshal(map[string]string{"snapshotID": a.Items[0].Identity.SnapshotID})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := options.Read(t.Context(), "quality_facts", args); err != nil {
		t.Fatal(err)
	}
	if a.Analysis.ParsedSources == 0 {
		t.Fatal("agent-requested facts were not computed")
	}
}

func TestQualityAgentHonorsExecutionOptOut(t *testing.T) {
	identity := make(chan testquality.Identity, 1)
	var calls atomic.Int32
	result := make(chan string, 1)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []intelligence.Message `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		if calls.Add(1) > 1 {
			result <- request.Messages[len(request.Messages)-1].Content
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Review paused.\"},\"finish_reason\":\"stop\"}]}\n\n")
			return
		}
		id := <-identity
		args, _ := json.Marshal(testquality.DiagnosticRequest{Kind: "rerun", Tests: []testquality.Identity{id}, Repetitions: 1, Reason: "Check a failure", Evidence: []testquality.Citation{{SnapshotID: id.SnapshotID, FileID: "test", StartLine: 3, EndLine: 3}}})
		delta := map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "diagnostic", "function": map[string]string{"name": "run_test_diagnostic", "arguments": string(args)}}}}
		response, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta, "finish_reason": "tool_calls"}}})
		fmt.Fprintf(w, "data: %s\n\n", response)
	}))
	defer provider.Close()
	s := qualityFixture(t)
	s.ConfigureAgent(intelligence.Config{Enabled: true, BaseURL: provider.URL, Model: "fixture"})
	start := qualityRequest(t, s, "/api/test-quality/start", `{"allowExecution":false}`)
	if start.Code != http.StatusAccepted {
		t.Fatal(start.Body.String())
	}
	var response struct{ ID string }
	if err := json.Unmarshal(start.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	s.qualityMu.Lock()
	job := s.qualityJobs[response.ID]
	identity <- job.data.Items[0].Identity
	s.qualityMu.Unlock()
	defer func() { job.cancel(); <-job.done }()
	select {
	case message := <-result:
		if !strings.Contains(message, "execution explicitly disabled") {
			t.Fatalf("agent did not receive authorization error: %s", message)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("agent did not receive diagnostic result")
	}
	<-job.done
	saved, err := s.qualityStore.Load(response.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Diagnostics) != 0 {
		t.Fatalf("disabled diagnostic was executed: %+v", saved.Diagnostics)
	}
}

func TestQualityAgentRunsAtMostFourGroupsConcurrently(t *testing.T) {
	s := qualityFixture(t)
	base := s.review.Modules[0]
	for i := 1; i < 5; i++ {
		module := base
		report := *base.Report
		report.Module = fmt.Sprintf("module-%d", i)
		module.Report = &report
		s.review.Modules = append(s.review.Modules, module)
	}

	started := make(chan struct{}, 5)
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	var active, maximum atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		now := active.Add(1)
		for old := maximum.Load(); now > old && !maximum.CompareAndSwap(old, now); old = maximum.Load() {
		}
		started <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
		}
		active.Add(-1)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Done.\"},\"finish_reason\":\"stop\"}]}\n\n")
	}))
	defer provider.Close()
	defer unblock()
	s.ConfigureAgent(intelligence.Config{Enabled: true, BaseURL: provider.URL, Model: "fixture"})
	start := qualityRequest(t, s, "/api/test-quality/start", `{"allowExecution":false}`)
	if start.Code != http.StatusAccepted {
		t.Fatal(start.Body.String())
	}
	var response struct{ ID string }
	if err := json.Unmarshal(start.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	s.qualityMu.Lock()
	job := s.qualityJobs[response.ID]
	s.qualityMu.Unlock()
	defer func() { unblock(); job.cancel(); <-job.done }()
	for i := 0; i < 4; i++ {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("four independent agents did not start concurrently")
		}
	}
	select {
	case <-started:
		t.Fatal("more than four agents ran concurrently")
	case <-time.After(100 * time.Millisecond):
	}
	unblock()
	select {
	case <-job.done:
	case <-time.After(5 * time.Second):
		t.Fatal("agents did not finish")
	}
	if maximum.Load() != 4 {
		t.Fatalf("maximum concurrent agents = %d, want 4", maximum.Load())
	}
}

func TestQualityAgentCanInspectDiagnosticItJustRan(t *testing.T) {
	s := qualityFixture(t)
	report := s.review.Modules[0].Report
	report.UnitEvidence = &review.UnitEvidence{SourceSHA256: report.SourceSHA256, Runs: []review.UnitTestRun{{
		Selection: review.UnitTestSelection{Package: "example"}, MatchedTests: []string{"TestF"},
	}}}
	item := make(chan testquality.Responsibility, 1)
	result := make(chan string, 1)
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []intelligence.Message `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		switch calls.Add(1) {
		case 1:
			selected := <-item
			item <- selected
			qualityFixtureRespond(w, qualityFixtureCall{"run_test_diagnostic", testquality.DiagnosticRequest{
				Kind: "rerun", Tests: []testquality.Identity{selected.Identity}, Repetitions: 1,
				Reason: "Investigate the retained test", Evidence: []testquality.Citation{selected.Declaration},
			}})
		case 2:
			selected := <-item
			qualityFixtureRespond(w, qualityFixtureCall{"quality_diagnostics", map[string]any{
				"snapshotID": selected.Identity.SnapshotID, "operation": "diagnostics",
			}})
		default:
			result <- request.Messages[len(request.Messages)-1].Content
			qualityFixtureRespond(w)
		}
	}))
	defer provider.Close()
	s.ConfigureAgent(intelligence.Config{Enabled: true, BaseURL: provider.URL, Model: "fixture"})
	start := qualityRequest(t, s, "/api/test-quality/start", `{"allowExecution":true}`)
	if start.Code != http.StatusAccepted {
		t.Fatal(start.Body.String())
	}
	var response struct{ ID string }
	if err := json.Unmarshal(start.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	s.qualityMu.Lock()
	job := s.qualityJobs[response.ID]
	item <- job.data.Items[0]
	s.qualityMu.Unlock()
	defer func() { job.cancel(); <-job.done }()
	select {
	case message := <-result:
		if !strings.Contains(message, "Investigate the retained test") {
			t.Fatalf("diagnostic reader did not return the agent's attempt: %s", message)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("agent did not receive diagnostic evidence")
	}
}
