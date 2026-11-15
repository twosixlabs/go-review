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
		return review.File{ID: id, Package: strings.Split(strings.TrimPrefix(text, "package "), "\n")[0], Path: path, Source: text, SHA256: testquality.Digest([]byte(text))}
	}
	assertions := "package example\nimport \"testing\"\nfunc TestF(t *testing.T) { if F()!=1 { t.Fatal(\"wrong\") } }\n"
	s.review.Modules[0].Report.TestSources[0].File = makeFile("test", "f_test.go", assertions)
	source := "package worker\nfunc Publish(value string) bool { if value == \"\" { return false }; return true }\nfunc Broken() {}\n"
	tests := "package worker\nimport \"testing\"\nfunc TestPublish(t *testing.T) { mock := func(string){}; mock(\"ok\"); _ = Publish(\"ok\") }\nfunc TestBroken(t *testing.T) { Broken() }\n"
	worker := &review.Report{Root: t.TempDir(), Module: "worker", SourceSHA256: "worker-source",
		Files: []review.File{makeFile("f", "publish.go", source)}, TestSources: []review.TestSource{{File: makeFile("test", "publish_test.go", tests),
			Declarations: []review.TestDeclaration{
				{ID: "TestPublish", Name: "TestPublish", Kind: "test", Location: review.Location{FileID: "test", Line: 3, EndLine: 3}},
				{ID: "TestBroken", Name: "TestBroken", Kind: "test", Location: review.Location{FileID: "test", Line: 4, EndLine: 4}},
			}}},
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
			Tools    []intelligence.Tool    `json:"tools"`
			Format   struct {
				Type   string `json:"type"`
				Schema struct {
					Name string `json:"name"`
				} `json:"json_schema"`
			} `json:"response_format"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		var initial struct {
			Tests []struct {
				Test   testquality.Responsibility `json:"test"`
				Source json.RawMessage            `json:"source"`
			} `json:"tests"`
		}
		if err := json.Unmarshal([]byte(request.Messages[1].Content), &initial); err != nil {
			t.Error(err)
		}
		item := initial.Tests[0].Test
		if item.Name != "TestF" && item.Name != "TestPublish" {
			t.Error("production functions must not become assessment targets")
		}
		read, _ := json.Marshal(map[string]any{"snapshotID": item.Identity.SnapshotID, "fileID": "f", "startLine": 2, "endLine": 2})
		name, args := "review_source", read
		if request.Format.Type == "json_schema" || request.Messages[len(request.Messages)-1].Role == "tool" {
			levels := []int{4, 2}
			title, rationale, suggestion := "Meaningful result assertion", "The test checks the return value.", "Review relevant additional cases."
			if item.Identity.Module == "worker" {
				levels = []int{0, 1}
				title = "Invocation without behavior assertion"
				rationale = "The mock is called and Publish's result is ignored; the empty input is not represented."
				suggestion = "Assert accepted and empty-input outcomes."
				if !strings.Contains(string(initial.Tests[0].Source), "mock") {
					t.Error("mock/test source not investigated")
				}
			}
			citations := []testquality.Citation{{SnapshotID: item.Identity.SnapshotID, FileID: "f", StartLine: 2, EndLine: 2}, {SnapshotID: item.Identity.SnapshotID, FileID: "test", StartLine: 3, EndLine: 3}}
			ratings := []testquality.Rating{}
			for i, d := range []testquality.Dimension{testquality.AssertionStrength, testquality.Craftsmanship} {
				ratings = append(ratings, testquality.Rating{Dimension: d, State: testquality.RatingAssessed, Value: &levels[i], Rationale: rationale, Evidence: citations})
			}
			item.Status, item.Ratings, item.Findings = testquality.StatusScored, ratings, []testquality.Finding{{Category: "assertion-strength", Kind: "inference", Title: title, Rationale: rationale, Consequence: "Coverage alone cannot establish meaningful checks.", Suggestion: suggestion, Evidence: citations}}
			results := []testquality.Responsibility{item}
			for _, selected := range initial.Tests[1:] {
				partial := selected.Test
				partial.Status, partial.Reason = testquality.StatusInsufficient, "fixture model failure: unresolved independent batch sibling"
				partial.Ratings = []testquality.Rating{
					{Dimension: testquality.Craftsmanship, State: testquality.RatingUnknown, Rationale: partial.Reason},
					{Dimension: testquality.AssertionStrength, State: testquality.RatingUnknown, Rationale: partial.Reason},
				}
				results = append(results, partial)
			}
			name = "submit_batch"
			args, _ = json.Marshal(map[string]any{"items": results})
		}
		collective := request.Format.Schema.Name == "submit_suite"
		for _, tool := range request.Tools {
			collective = collective || tool.Function.Name == "submit_suite"
		}
		if collective {
			name = "submit_suite"
			unknownRatings := []any{}
			for _, dimension := range []testquality.Dimension{testquality.BehaviorAdequacy, testquality.ConditionAdequacy} {
				unknownRatings = append(unknownRatings, map[string]any{"dimension": dimension, "state": testquality.RatingUnknown, "rationale": "Partial fixture suite has unresolved test evidence.", "evidence": []testquality.Citation{}})
			}
			args, _ = json.Marshal(map[string]any{"ratings": unknownRatings, "expectations": []testquality.ExpectedBehavior{}, "findings": []testquality.Finding{}})
		}
		delta := map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "evidence", "function": map[string]string{"name": name, "arguments": string(args)}}}}
		finish := "tool_calls"
		if request.Format.Type == "json_schema" {
			if collective {
				var proposal map[string]any
				if err := json.Unmarshal(args, &proposal); err != nil {
					t.Error(err)
				}
				delete(proposal, "status")
				delete(proposal, "reason")
				args, _ = json.Marshal(proposal)
			}
			converted, err := qualityProposalLabels(args, request.Messages[len(request.Messages)-1].Content)
			if err != nil {
				t.Error(err)
				return
			}
			args = converted
			delta, finish = map[string]any{"content": string(args)}, "stop"
		}
		response, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta, "finish_reason": finish}}})
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
	summary, err := testquality.AggregateVersion(a.Version, a.Items)
	if err != nil {
		t.Fatal(err)
	}
	want := 25 * ((4+2)/2.0 + (0+1)/2.0) / 2
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
