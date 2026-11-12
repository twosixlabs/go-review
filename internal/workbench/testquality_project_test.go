package workbench

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/russellbrasser/go-review/internal/intelligence"
	"github.com/russellbrasser/go-review/internal/review"
	"github.com/russellbrasser/go-review/internal/testquality"
)

func TestQualityProjectAssessmentKeepsPartialMultiModuleJudgmentsAndAggregateCoverage(t *testing.T) {
	s := qualityFixture(t)
	makeFile := func(id, path, text string) review.File {
		return review.File{ID: id, Path: path, Source: text, SHA256: testquality.Digest([]byte(text))}
	}
	assertions := "package example\nimport \"testing\"\nfunc TestF(t *testing.T) { if F()!=1 { t.Fatal(\"wrong\") } }\n"
	s.review.Modules[0].Report.TestSources = []review.TestSource{{File: makeFile("test", "f_test.go", assertions)}}
	source := "package worker\nfunc Publish(value string) bool { if value == \"\" { return false }; return true }\nfunc Broken() {}\n"
	tests := "package worker\nimport \"testing\"\nfunc TestPublish(t *testing.T) { mock := func(string){}; mock(\"ok\"); _ = Publish(\"ok\") }\n"
	worker := &review.Report{Root: t.TempDir(), Module: "worker", SourceSHA256: "worker-source",
		Files: []review.File{makeFile("f", "publish.go", source)}, TestSources: []review.TestSource{{File: makeFile("test", "publish_test.go", tests)}},
		Symbols: []review.Symbol{
			{ID: "F", Name: "Publish", Kind: "function", Package: "worker", Location: review.Location{FileID: "f", Line: 2, EndLine: 2}},
			{ID: "broken", Name: "Broken", Kind: "function", Package: "worker", Location: review.Location{FileID: "f", Line: 3, EndLine: 3}}},
		Coverage: &review.Coverage{SourceVerified: true, Mode: "set", Blocks: []review.CoverageBlock{{FileID: "f", StartLine: 2, EndLine: 2, Statements: 3, Count: 1}}},
		// This profile covers a run with multiple tests, not named-case paths.
		UnitEvidence: &review.UnitEvidence{SourceSHA256: "worker-source", AllTests: true, Runs: []review.UnitTestRun{{Status: "pass", MatchedTests: []string{"TestPublish", "TestOther"}, Outcomes: map[string]string{"TestPublish": "pass", "TestOther": "pass"}}}}}
	s.review.Modules = append(s.review.Modules, review.ReviewedModule{Report: worker})
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []intelligence.Message `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		var initial struct {
			Responsibility    testquality.Responsibility `json:"responsibility"`
			StatementCoverage json.RawMessage            `json:"statementCoverage"`
		}
		if err := json.Unmarshal([]byte(request.Messages[1].Content), &initial); err != nil {
			t.Error(err)
		}
		item := initial.Responsibility
		if item.Name == "Broken" {
			http.Error(w, "fixture model failure", http.StatusServiceUnavailable)
			return
		}
		if item.Identity.Module == "worker" && !strings.Contains(string(initial.StatementCoverage), "individual") {
			t.Error("aggregate attribution limitation absent")
		}
		read, _ := json.Marshal(testquality.EvidenceInput{SnapshotID: item.Identity.SnapshotID, Operation: "source", Args: json.RawMessage(`{"fileID":"test","startLine":1,"endLine":3}`)})
		name, args := "read_evidence", read
		if request.Messages[len(request.Messages)-1].Role == "tool" {
			levels := []int{4, 2, 2}
			title, rationale, suggestion := "Meaningful result assertion", "The test checks the return value.", "Review relevant additional cases."
			if item.Identity.Module == "worker" {
				levels = []int{0, 1, 1}
				title = "Invocation without behavior assertion"
				rationale = "The mock is called and Publish's result is ignored; the empty input is not represented."
				suggestion = "Assert accepted and empty-input outcomes."
				if !strings.Contains(request.Messages[len(request.Messages)-1].Content, "mock") {
					t.Error("mock/test source not investigated")
				}
			}
			citations := []testquality.Citation{{SnapshotID: item.Identity.SnapshotID, FileID: "f", StartLine: 2, EndLine: 2}, {SnapshotID: item.Identity.SnapshotID, FileID: "test", StartLine: 3, EndLine: 3}}
			ratings := []testquality.Rating{}
			for i, d := range []testquality.Dimension{testquality.AssertionStrength, testquality.RepresentativeCases, testquality.BehaviorPaths} {
				ratings = append(ratings, testquality.Rating{Dimension: d, State: testquality.RatingAssessed, Value: &levels[i], Rationale: rationale, Evidence: citations})
			}
			name = "submit_assessment"
			args, _ = json.Marshal(map[string]any{"status": "scored", "ratings": ratings, "findings": []testquality.Finding{{Category: "assertion-strength", Kind: "inference", Title: title, Rationale: rationale, Consequence: "Coverage alone cannot establish meaningful checks.", Suggestion: suggestion, Evidence: citations}}})
		}
		response, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "evidence", "function": map[string]string{"name": name, "arguments": string(args)}}}}, "finish_reason": "tool_calls"}}})
		fmt.Fprintf(w, "data: %s\n\n", response)
	}))
	defer model.Close()
	s.ConfigureAgent(intelligence.Config{Enabled: true, BaseURL: model.URL, Model: "project-fixture", MaxSteps: 3})
	w := qualityRequest(t, s, "/api/test-quality/start", `{}`)
	if w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	var launch struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &launch); err != nil {
		t.Fatal(err)
	}
	s.qualityMu.Lock()
	job := s.qualityJobs[launch.ID]
	s.qualityMu.Unlock()
	select {
	case <-job.done:
	case <-time.After(5 * time.Second):
		t.Fatal("assessment unfinished")
	}
	// Replacing current evidence must not change archived judgments or test source.
	s.mu.Lock()
	s.review.Modules = nil
	s.report = nil
	s.mu.Unlock()
	a, err := s.qualityStore.Load(launch.ID)
	if err != nil {
		t.Fatal(err)
	}
	summary, err := testquality.Aggregate(a.Items)
	if err != nil {
		t.Fatal(err)
	}
	want := 25 * ((4+2+2)/3.0 + (0+1+1)/3.0) / 2
	if a.Status != "partial" || summary.Counts.Scored != 2 || summary.Counts.Insufficient != 1 || summary.Score == nil || math.Abs(*summary.Score-want) > 1e-8 {
		t.Fatalf("project result: %+v %+v", qualityCompact(a), summary)
	}
	if !strings.Contains(a.Items[2].Reason, "fixture model failure") {
		t.Fatalf("provider failure hidden: %+v", a.Items[2])
	}
	archive, err := s.queryTestQuality(context.Background(), testquality.QueryInput{AssessmentID: a.ID, Operation: "source", SnapshotID: a.Items[1].Identity.SnapshotID, FileID: "test", StartLine: 3, EndLine: 3})
	if err != nil || !strings.Contains(string(archive), "mock") {
		t.Fatalf("saved evidence unavailable: %s %v", archive, err)
	}
}
