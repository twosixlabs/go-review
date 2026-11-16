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
	exampleFinished := make(chan struct{})
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request, scope, ok := qualityFixtureDecode(t, r)
		if !ok || len(scope.Tests) == 0 {
			http.Error(w, "invalid worker scope", http.StatusBadRequest)
			return
		}
		qualityFixtureCheckTools(t, request)
		var item testquality.Responsibility
		for _, entry := range scope.Tests {
			selected := scope.item(entry)
			switch selected.Name {
			case "TestF", "TestPublish":
				item = selected
			case "TestBroken":
			default:
				t.Errorf("production function became an assessment target: %s", selected.Name)
			}
		}
		if item.Name == "" {
			http.Error(w, "missing expected test", http.StatusBadRequest)
			return
		}
		production := testquality.Citation{SnapshotID: item.Identity.SnapshotID, FileID: "f", StartLine: 2, EndLine: 2}
		switch len(request.Messages) {
		case 1:
			qualityFixtureRespond(w, qualityFixtureRead(item.Declaration), qualityFixtureRead(production))
		case 4:
			craft, assertion := 2, 4
			rationale := "The test checks the return value."
			if item.Identity.Module == "worker" {
				craft, assertion = 1, 0
				rationale = "The mock is called and Publish's result is ignored; the empty input is not represented."
				if !strings.Contains(request.Messages[2].Content, "mock") {
					t.Error("mock/test source not investigated")
				}
			}
			qualityFixtureRespond(w, qualityFixtureTest(item, craft, assertion, rationale), qualityFixtureGroup(production, rationale))
		default:
			if item.Identity.Module == "worker" {
				select {
				case <-exampleFinished:
					http.Error(w, "fixture model failure", http.StatusServiceUnavailable)
				case <-r.Context().Done():
				}
				return
			}
			close(exampleFinished)
			qualityFixtureRespond(w)
		}
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
