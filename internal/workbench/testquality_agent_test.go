package workbench

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func TestQualityAgentRecordsInspectedContextAroundTest(t *testing.T) {
	for _, span := range [][2]int{{1, 4}, {1, 3}, {3, 4}, {3, 3}} {
		t.Run(fmt.Sprintf("%d-%d", span[0], span[1]), func(t *testing.T) {
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
			item := a.Items[0]
			read, _ := json.Marshal(map[string]any{"snapshotID": item.Identity.SnapshotID, "fileID": "test", "startLine": 1, "endLine": 4})
			_, seen, err := options.Read(t.Context(), "review_source", read)
			if err != nil {
				t.Fatal(err)
			}
			args, _ := json.Marshal(map[string]any{
				"snapshotID": item.Identity.SnapshotID, "symbolID": item.Identity.SymbolID,
				"fileID": "test", "startLine": span[0], "endLine": span[1],
				"craftsmanship": 3, "assertionStrength": 2, "rationale": "The return value is checked.",
			})
			if err := s.recordQualityAgent(a, qualityAgentGroup{}, "record_test", args, seen); err != nil {
				t.Fatalf("inspected context prevented recording: %v", err)
			}
			saved, err := s.qualityStore.Load(a.ID)
			if err != nil {
				t.Fatal(err)
			}
			evidence := saved.Items[0].Ratings[0].Evidence
			if saved.Items[0].Status != testquality.StatusScored || len(evidence) != 1 ||
				evidence[0].StartLine != span[0] || evidence[0].EndLine != span[1] {
				t.Fatalf("source-backed judgment or its cited context was lost: %+v", saved.Items[0])
			}
		})
	}
}

func TestQualityAgentRecordingErrorsIdentifySourceProblem(t *testing.T) {
	for _, tc := range []struct {
		name, file, symbol, snapshot string
		start, end, readEnd          int
		want                         []string
	}{
		{name: "unknown test", symbol: "unknown-test", start: 3, end: 3, readEnd: 7, want: []string{"unknown-test", "assessment"}},
		{name: "wrong snapshot", snapshot: "other-snapshot", start: 3, end: 3, readEnd: 7, want: []string{"other-snapshot", "snapshot"}},
		{name: "wrong file", file: "f", start: 2, end: 2, readEnd: 7, want: []string{"f", "test", "file"}},
		{name: "unrelated lines", start: 1, end: 2, readEnd: 7, want: []string{"1-2", "3-7", "overlap"}},
		{name: "unread test body", start: 3, end: 3, readEnd: 5, want: []string{"6-7", "test", "inspect"}},
		{name: "unread citation", start: 6, end: 7, readEnd: 5, want: []string{"6-7", "test", "inspect"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := qualityFixture(t)
			source := &s.review.Modules[0].Report.TestSources[0]
			source.File.Source = "package example\nimport \"testing\"\nfunc TestF(t *testing.T) {\n if F()!=1 {\n  t.Fatal(\"wrong\")\n }\n}\n"
			source.File.SHA256 = testquality.Digest([]byte(source.File.Source))
			source.Declarations[0].Location.EndLine = 7
			a, err := s.qualityStore.Create(t.Context(), s.review, "", "")
			if err != nil {
				t.Fatal(err)
			}
			item := a.Items[0]
			seen := []testquality.Citation{
				{SnapshotID: item.Identity.SnapshotID, FileID: "test", StartLine: 1, EndLine: tc.readEnd},
				{SnapshotID: item.Identity.SnapshotID, FileID: "f", StartLine: 2, EndLine: 2},
			}
			file, symbol, snapshot := tc.file, tc.symbol, tc.snapshot
			if file == "" {
				file = "test"
			}
			if symbol == "" {
				symbol = item.Identity.SymbolID
			}
			if snapshot == "" {
				snapshot = item.Identity.SnapshotID
			}
			args, _ := json.Marshal(map[string]any{
				"snapshotID": snapshot, "symbolID": symbol,
				"fileID": file, "startLine": tc.start, "endLine": tc.end,
				"craftsmanship": 3, "assertionStrength": 2, "rationale": "The return value is checked.",
			})
			group := qualityAgentGroup{SnapshotID: item.Identity.SnapshotID, Tests: a.Items}
			primary := &qualityPrimary{server: s, assessment: a, items: a.Items}
			for _, recordErr := range []error{
				s.recordQualityAgent(a, group, "record_test", args, seen),
				primary.record(a.Items, true, "record_test", args, seen),
			} {
				if recordErr == nil {
					t.Fatal("invalid source-backed recording succeeded")
				}
				for _, detail := range tc.want {
					if !strings.Contains(recordErr.Error(), detail) {
						t.Errorf("error omitted corrective detail %q: %v", detail, recordErr)
					}
				}
			}
			saved, err := s.qualityStore.Load(a.ID)
			if err != nil || saved.Items[0].Status != testquality.StatusUnreviewed {
				t.Fatalf("rejected recording changed saved judgment: %+v %v", saved, err)
			}
		})
	}
}

func TestQualityAgentRecordsAfterReadingSourceTextPages(t *testing.T) {
	for _, tool := range []string{"review_source", "review_read_file", "review_batch", "changed_live"} {
		for _, skipPage := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/skip=%t", tool, skipPage), func(t *testing.T) {
				s := qualityFixture(t)
				source := &s.review.Modules[0].Report.TestSources[0].File
				source.Source = strings.ReplaceAll(source.Source, "wrong", "\u03c0\\twrong")
				source.SHA256 = testquality.Digest([]byte(source.Source))
				a, err := s.qualityStore.Create(t.Context(), s.review, "", "")
				if err != nil {
					t.Fatal(err)
				}
				item := a.Items[0]
				if tool == "changed_live" {
					if err := os.WriteFile(filepath.Join(s.review.Root, "f_test.go"), []byte(strings.ReplaceAll(source.Source, "wrong", "changed")), 0600); err != nil {
						t.Fatal(err)
					}
				}
				options, closeSession, err := s.qualityEvidence(t.Context(), a, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer closeSession()
				requests, offset, skipped := 0, 0, false
				recorded := false
				cfg := qualityAgentProvider(t, func(w http.ResponseWriter, _ *http.Request, request qualityAgentRequest) {
					requests++
					if requests > 1 {
						last := request.Messages[len(request.Messages)-1].Content
						if recorded {
							if strings.Contains(last, `"recorded":true`) == (skipPage || tool == "changed_live") {
								t.Errorf("paging provenance was not enforced: %s", last)
							}
							qualityAgentAnswer(w)
							return
						}
						var result struct {
							Data struct {
								TextPage struct{ NextOffset *int }
							}
							Results []struct{ Result json.RawMessage }
						}
						if err := json.Unmarshal([]byte(last), &result); err != nil {
							t.Error(err)
							http.Error(w, "bad source response", http.StatusInternalServerError)
							return
						}
						if tool == "review_batch" && len(result.Results) == 1 {
							if err := json.Unmarshal(result.Results[0].Result, &result); err != nil {
								t.Error(err)
								http.Error(w, "bad batch response", http.StatusInternalServerError)
								return
							}
						}
						if result.Data.TextPage.NextOffset == nil {
							recorded = true
							qualityFixtureRespond(w, qualityFixtureTest(item, 3, 2, "Read the entire retained test across text pages."))
							return
						}
						offset = *result.Data.TextPage.NextOffset
						if skipPage && !skipped {
							offset += 11
							skipped = true
						}
					}
					args := map[string]any{"snapshotID": item.Identity.SnapshotID, "startLine": 3, "endLine": 3, "textOffset": offset, "textLimit": 11}
					name := tool
					if tool == "review_read_file" || tool == "changed_live" {
						name = "review_read_file"
						args["path"] = "f_test.go"
						if tool == "changed_live" {
							args["source"] = "live"
						}
					} else {
						args["fileID"] = "test"
					}
					if tool == "review_batch" {
						args = map[string]any{"calls": []any{map[string]any{"tool": "review_source", "arguments": args}}}
					}
					raw, _ := json.Marshal(args)
					qualityAgentAnswer(w, qualityAgentCall(fmt.Sprint(requests), name, string(raw)))
				})
				cfg.MaxSteps = 20
				err = runQualityAgent(t.Context(), cfg, "Read and assess the selected test.", options, func(name string, args json.RawMessage, seen []testquality.Citation) error {
					return s.recordQualityAgent(a, qualityAgentGroup{Tests: a.Items}, name, args, seen)
				})
				if err != nil {
					t.Fatal(err)
				}
				saved, err := s.qualityStore.Load(a.ID)
				if err != nil {
					t.Fatal(err)
				}
				if !recorded || (saved.Items[0].Status == testquality.StatusScored) == (skipPage || tool == "changed_live") {
					t.Fatalf("text-page inspection did not determine saved status: %+v", saved.Items[0])
				}
			})
		}
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
	s.ConfigureAgent(intelligence.Config{Enabled: true, BaseURL: provider.URL, Model: "fixture", MaxSteps: 2})
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
