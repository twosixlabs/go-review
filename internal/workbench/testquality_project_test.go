package workbench

import (
	"context"
	"encoding/json"
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
		request, items, _ := qualityFixtureDecode(t, r)
		qualityFixtureCheckTools(t, request)
		if len(items) == 0 {
			qualityFixtureRespond(w, qualityFixtureCall{"review_progress", map[string]string{"operation": "tests"}})
			return
		}
		var selected []testquality.Responsibility
		for _, item := range items {
			switch item.Name {
			case "TestF", "TestPublish":
				selected = append(selected, item)
			case "TestBroken":
			default:
				t.Errorf("production function became an assessment target: %s", item.Name)
			}
		}
		if len(selected) != 2 {
			http.Error(w, "missing expected test", http.StatusBadRequest)
			return
		}
		var calls []qualityFixtureCall
		switch {
		case qualityFixtureCallCount(request, "review_source") == 0:
			for _, item := range selected {
				production := testquality.Citation{SnapshotID: item.Identity.SnapshotID, FileID: "f", StartLine: 2, EndLine: 2}
				calls = append(calls, qualityFixtureRead(item.Declaration), qualityFixtureRead(production))
			}
		case qualityFixtureCallCount(request, "record_test") == 0:
			for _, item := range selected {
				craft, assertion := 2, 4
				rationale := "The test checks the return value."
				if item.Identity.Module == "worker" {
					craft, assertion = 1, 0
					rationale = "The mock is called and Publish's result is ignored; the empty input is not represented."
					found := false
					for _, message := range request.Messages {
						found = found || message.Role == "tool" && strings.Contains(message.Content, "mock")
					}
					if !found {
						t.Error("mock/test source not investigated")
					}
				}
				production := testquality.Citation{SnapshotID: item.Identity.SnapshotID, FileID: "f", StartLine: 2, EndLine: 2}
				group := qualityFixtureGroup(production, rationale)
				group.args.(map[string]any)["scope"], group.args.(map[string]any)["package"] = "package", item.Package
				calls = append(calls, qualityFixtureTest(item, craft, assertion, rationale), group)
			}
		default:
			http.Error(w, "fixture model failure", http.StatusServiceUnavailable)
			return
		}
		qualityFixtureRespond(w, calls...)
	}))
	defer model.Close()
	s.ConfigureAgent(intelligence.Config{Enabled: true, BaseURL: model.URL, Model: "project-fixture", MaxSteps: 4})
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
	if a.Status != "failed" || summary.Counts.Scored != 2 || summary.Counts.Unreviewed != 1 || summary.Score == nil || math.Abs(*summary.Score-want) > 1e-8 {
		t.Fatalf("project result: %+v %+v", qualityCompact(a), summary)
	}
	var workerSnapshot string
	for _, item := range a.Items {
		if item.Name == "TestBroken" && item.Status != testquality.StatusUnreviewed {
			t.Fatalf("unfinished test was graded after provider failure: %+v", item)
		}
		if item.Name == "TestPublish" {
			workerSnapshot = item.Identity.SnapshotID
		}
	}
	if !strings.Contains(a.Error, "503") || a.Suite == nil || len(a.Suite.Groups) != 2 {
		t.Fatalf("provider failure or completed group judgments lost: error=%s suite=%+v", a.Error, a.Suite)
	}
	archive, err := s.queryTestQuality(context.Background(), testquality.QueryInput{AssessmentID: a.ID, Operation: "source", SnapshotID: workerSnapshot, FileID: "test", StartLine: 3, EndLine: 3})
	if err != nil || !strings.Contains(string(archive), "mock") {
		t.Fatalf("saved evidence unavailable: %s %v", archive, err)
	}
}
