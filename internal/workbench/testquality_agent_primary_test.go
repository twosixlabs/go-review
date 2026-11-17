package workbench

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/russellbrasser/go-review/internal/intelligence"
	"github.com/russellbrasser/go-review/internal/review"
	"github.com/russellbrasser/go-review/internal/testquality"
)

func TestQualityAgentDelegatedDiscoveryPreservesRecordingMode(t *testing.T) {
	for _, mode := range []string{"research", "assigned-tests"} {
		t.Run(mode, func(t *testing.T) {
			s := qualityFixture(t)
			source := s.review.Modules[0].Report.TestSources[0].File
			if err := os.WriteFile(filepath.Join(s.review.Root, source.Path), []byte(source.Source), 0600); err != nil {
				t.Fatal(err)
			}
			a, err := s.qualityStore.Create(t.Context(), s.review, "", "")
			if err != nil {
				t.Fatal(err)
			}
			requests := 0
			cfg := qualityAgentProvider(t, func(w http.ResponseWriter, _ *http.Request, request qualityAgentRequest) {
				requests++
				call := func(name string, args map[string]any) {
					args["snapshotID"] = a.Items[0].Identity.SnapshotID
					qualityFixtureRespond(w, qualityFixtureCall{name, args})
				}
				last := request.Messages[len(request.Messages)-1].Content
				switch requests {
				case 1:
					if !strings.Contains(request.Messages[0].Content, `"mode":"`+mode+`"`) {
						t.Errorf("child mode is not explicit: %s", request.Messages[0].Content)
					}
					call("review_tests", map[string]any{"package": "example"})
				case 2:
					if !strings.Contains(last, `"symbolID":"TestF"`) {
						t.Errorf("child could not discover selected test: %s", last)
					}
					call("review_list_files", map[string]any{"path": ".", "source": "retained"})
				case 3:
					if !strings.Contains(last, "f_test.go") {
						t.Errorf("child inventory hid test source: %s", last)
					}
					call("review_read_file", map[string]any{"path": "f_test.go", "source": "live", "startLine": 3, "endLine": 3})
				case 4:
					if !strings.Contains(last, `"snapshotMatch":true`) {
						t.Errorf("live provenance missing: %s", last)
					}
					qualityFixtureRespond(w, qualityFixtureTest(a.Items[0], 3, 3, "Checks the returned value."))
				default:
					if strings.Contains(last, "recording scope does not contain any assigned tests") != (mode == "research") {
						t.Errorf("child recording mode not enforced: %s", last)
					}
					qualityFixtureRespond(w)
				}
			})
			p, err := newQualityPrimary(t.Context(), s, a, cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer p.close()
			input := map[string]any{"instructions": "Inspect tests in package example; do not broaden the investigation."}
			if mode == "assigned-tests" {
				input["tests"] = []testquality.Identity{a.Items[0].Identity}
			}
			raw, _ := json.Marshal(input)
			if _, _, err := p.call(t.Context(), "delegate_review", raw); err != nil {
				t.Fatal(err)
			}
			result, _, err := p.call(t.Context(), "read_review_agent", json.RawMessage(`{"agentID":"agent-1","wait":true}`))
			if err != nil || !strings.Contains(string(result), `"status":"completed"`) {
				t.Fatalf("focused child failed: %s %v", result, err)
			}
			p.close()
			if (a.Items[0].Status == testquality.StatusScored) != (mode == "assigned-tests") || a.Suite != nil {
				t.Fatalf("child recording authority changed: %+v", qualityCompact(a))
			}
		})
	}
}

func TestQualityAgentPrimaryOwnsReviewAndRepairsRejectedCompletion(t *testing.T) {
	s := qualityFixture(t)
	var requests int
	cfg := qualityAgentProvider(t, func(w http.ResponseWriter, _ *http.Request, request qualityAgentRequest) {
		requests++
		switch requests {
		case 1:
			for _, name := range []string{"delegate_review", "read_review_agent", "review_progress", "finish_review"} {
				found := false
				for _, tool := range request.Tools {
					found = found || tool.Function.Name == name
				}
				if !found {
					t.Errorf("primary cannot own the investigation: missing %s", name)
				}
			}
			qualityAgentAnswer(w, qualityAgentCall("early", "finish_review", `{}`))
		case 2:
			last := request.Messages[len(request.Messages)-1].Content
			if !strings.Contains(last, "test") || !strings.Contains(last, "project") {
				t.Errorf("primary did not receive complete missing requirements: %s", last)
			}
			qualityAgentAnswer(w, qualityAgentCall("discover", "review_progress", `{"operation":"tests"}`))
		case 3:
			var page struct{ Items []testquality.Responsibility }
			if err := json.Unmarshal([]byte(request.Messages[len(request.Messages)-1].Content), &page); err != nil || len(page.Items) != 1 {
				t.Errorf("cannot discover selected tests: %+v %v", page, err)
				http.Error(w, "missing tests", 500)
				return
			}
			item := page.Items[0]
			read := qualityFixtureRead(item.Declaration)
			test := qualityFixtureTest(item, 3, 2, "Checks the result.")
			group := qualityFixtureGroup(item.Declaration, "Independent project judgment.")
			group.args.(map[string]any)["scope"] = "project"
			qualityFixtureRespond(w, read, test, group)
		default:
			for _, message := range request.Messages {
				if message.Role == "tool" && strings.Contains(message.Content, `"error"`) && !strings.Contains(message.Content, "remaining") {
					// The first finish rejection is expected; later writes must succeed.
					if !strings.Contains(message.Content, "project") {
						t.Errorf("unexpected tool error: %s", message.Content)
					}
				}
			}
			qualityAgentAnswer(w, qualityAgentCall("finish", "finish_review", `{}`))
		}
	})
	cfg.MaxSteps = 6
	a, err := s.qualityStore.Create(t.Context(), s.review, "", "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	job := &qualityJob{data: a, cancel: cancel, done: make(chan struct{})}
	s.runAgentQuality(ctx, job, nil, cfg)
	saved, err := s.qualityStore.Load(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 4 || saved.Status != "completed" || saved.Items[0].Status != testquality.StatusScored || saved.Suite == nil || saved.Suite.Status != testquality.StatusScored {
		t.Fatalf("primary did not complete its own review: requests=%d assessment=%+v", requests, qualityCompact(saved))
	}
	events, err := s.queryTestQuality(t.Context(), testquality.QueryInput{AssessmentID: a.ID, Operation: "events", Limit: 100})
	if err != nil || !strings.Contains(string(events), "finish_review") || !strings.Contains(string(events), "remaining requirements") {
		t.Fatalf("completion rejection was not observable: %s %v", events, err)
	}
}

func TestQualityAgentPrimaryOwnsChildConcurrencyAndCancellation(t *testing.T) {
	started := make(chan struct{}, 4)
	stopped := make(chan struct{}, 4)
	var active atomic.Int32
	cfg := qualityAgentProvider(t, func(_ http.ResponseWriter, r *http.Request, request qualityAgentRequest) {
		active.Add(1)
		defer func() { active.Add(-1); stopped <- struct{}{} }()
		for _, tool := range request.Tools {
			if tool.Function.Name == "delegate_review" || tool.Function.Name == "finish_review" {
				t.Error("child was given primary orchestration authority")
			}
			var assignment struct {
				Tests     []testquality.Responsibility `json:"tests"`
				Snapshots []map[string]string          `json:"snapshots"`
			}
			text := request.Messages[0].Content
			if err := json.Unmarshal([]byte(text[strings.Index(text, "{"):]), &assignment); err != nil || len(assignment.Snapshots) != 1 || len(assignment.Tests) != 0 {
				t.Errorf("research-only child cannot discover pinned evidence: %+v %v", assignment, err)
			}
		}
		started <- struct{}{}
		<-r.Context().Done()
	})
	s := qualityFixture(t)
	a, err := s.qualityStore.Create(t.Context(), s.review, "", "")
	if err != nil {
		t.Fatal(err)
	}
	p, err := newQualityPrimary(t.Context(), s, a, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	for i := 0; i < 3; i++ {
		if _, _, err := p.call(t.Context(), "delegate_review", json.RawMessage(`{"instructions":"Inspect the test helper; report observations."}`)); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("primary-chosen children did not start")
		}
	}
	if _, _, err := p.call(t.Context(), "delegate_review", json.RawMessage(`{"instructions":"Must wait for capacity."}`)); err == nil {
		t.Fatal("allowed more than four total agents")
	}
	if err := p.finish(); err == nil || !strings.Contains(err.Error(), "active") {
		t.Fatalf("finish ignored active children: %v", err)
	}
	if _, _, err := p.call(t.Context(), "cancel_review_agent", json.RawMessage(`{"agentID":"agent-1"}`)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := p.call(t.Context(), "delegate_review", json.RawMessage(`{"instructions":"Investigate a different hypothesis."}`)); err != nil {
		t.Fatalf("cancelled child retained its execution slot: %v", err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("replacement investigation did not start")
	}
	p.close()
	for i := 0; i < 4; i++ {
		select {
		case <-stopped:
		case <-time.After(5 * time.Second):
			t.Fatal("child HTTP request did not observe cancellation")
		}
	}
	if active.Load() != 0 {
		t.Fatalf("primary shutdown left %d active child requests", active.Load())
	}
}

func TestQualityAgentPrimaryDelegatesAcrossModulesWithoutSharingCitationAuthority(t *testing.T) {
	s := qualityFixture(t)
	base := s.review.Modules[0].Report
	worker := *base
	worker.Root, worker.Module = t.TempDir(), "worker"
	worker.TestSources = append([]review.TestSource(nil), base.TestSources...)
	s.review.Modules = append(s.review.Modules, review.ReviewedModule{Report: &worker})
	spare := "package spare\nimport \"testing\"\nfunc TestSpare(t *testing.T) {}\n"
	base.TestSources = append(base.TestSources, review.TestSource{
		File: review.File{ID: "spare", Package: "spare", Path: "spare/spare_test.go", Source: spare, SHA256: testquality.Digest([]byte(spare))},
		Declarations: []review.TestDeclaration{{ID: "TestSpare", Name: "TestSpare", Kind: "test",
			Location: review.Location{FileID: "spare", Line: 3, EndLine: 3}}},
	})
	a, err := s.qualityStore.Create(t.Context(), s.review, "", "")
	if err != nil {
		t.Fatal(err)
	}
	var assigned []testquality.Responsibility
	var outside testquality.Responsibility
	for _, item := range a.Items {
		if item.Identity.Module == "example" && item.Name == "TestF" {
			outside = item
		} else {
			assigned = append(assigned, item)
		}
	}
	if len(assigned) != 2 || assigned[0].Identity.Module == assigned[1].Identity.Module || assigned[0].Package == assigned[1].Package {
		t.Fatal("fixture must span modules and packages")
	}
	cfg := qualityAgentProvider(t, func(w http.ResponseWriter, _ *http.Request, request qualityAgentRequest) {
		if len(request.Messages) == 1 {
			var scope struct{ Tests []testquality.Responsibility }
			text := request.Messages[0].Content
			if err := json.Unmarshal([]byte(text[strings.Index(text, "{"):]), &scope); err != nil || len(scope.Tests) != 2 {
				t.Errorf("primary's cross-module assignment was not delivered: %+v %v", scope, err)
			}
			qualityFixtureRespond(w,
				qualityFixtureRead(assigned[0].Declaration), qualityFixtureRead(assigned[1].Declaration), qualityFixtureRead(outside.Declaration),
				qualityFixtureTest(outside, 2, 1, "Unassigned test must not be recorded."),
				qualityFixtureGroup(assigned[0].Declaration, "Child must not own the project."),
				qualityFixtureTest(assigned[0], 3, 2, "Assigned test inspected."),
				qualityFixtureTest(assigned[1], 2, 3, "Second module inspected."))
			return
		}
		rejections := 0
		for _, message := range request.Messages {
			if message.Role == "tool" && strings.Contains(message.Content, `"error"`) {
				rejections++
			}
		}
		if rejections != 2 {
			t.Errorf("child scope violations did not return feedback: rejections=%d", rejections)
		}
		qualityAgentAnswer(w)
	})
	p, err := newQualityPrimary(t.Context(), s, a, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	args, _ := json.Marshal(map[string]any{"instructions": "Compare these related tests across modules.", "tests": []testquality.Identity{assigned[0].Identity, assigned[1].Identity}})
	if _, _, err := p.call(t.Context(), "delegate_review", args); err != nil {
		t.Fatal(err)
	}
	result, citations, err := p.call(t.Context(), "read_review_agent", json.RawMessage(`{"agentID":"agent-1","wait":true}`))
	if err != nil || !strings.Contains(string(result), `"status":"completed"`) || !strings.Contains(string(result), "Finished.") || len(citations) != 0 {
		t.Fatalf("child report or citation isolation lost: %s %v", result, err)
	}
	saved, err := s.qualityStore.Load(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	summary, err := testquality.AggregateVersion(saved.Version, saved.Items)
	if err != nil || summary.Counts.Scored != 2 || summary.Counts.Unreviewed != 1 || saved.Suite != nil {
		t.Fatalf("child escaped assigned recording scope: %+v %+v %v", summary, saved.Suite, err)
	}
	group, _ := json.Marshal(qualityFixtureGroup(assigned[0].Declaration, "Primary's independent judgment.").args)
	if err := p.record(p.items, true, "record_group", group, citations); err == nil {
		t.Fatal("child report authorized an uninspected primary citation")
	}
	read, _ := json.Marshal(qualityFixtureRead(assigned[0].Declaration).args)
	_, seen, err := p.call(t.Context(), "review_source", read)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.record(p.items, true, "record_group", group, seen); err != nil {
		t.Fatalf("primary could not synthesize after its own inspection: %v", err)
	}
}

func TestQualityAgentPrimaryCanRecoverFromChildFailure(t *testing.T) {
	cfg := qualityAgentProvider(t, func(w http.ResponseWriter, _ *http.Request, _ qualityAgentRequest) {
		http.Error(w, "child investigation unavailable", 503)
	})
	s := qualityFixture(t)
	a, err := s.qualityStore.Create(t.Context(), s.review, "", "")
	if err != nil {
		t.Fatal(err)
	}
	p, err := newQualityPrimary(t.Context(), s, a, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	if _, _, err := p.call(t.Context(), "delegate_review", json.RawMessage(`{"instructions":"Inspect test behavior."}`)); err != nil {
		t.Fatal(err)
	}
	result, citations, err := p.call(t.Context(), "read_review_agent", json.RawMessage(`{"agentID":"agent-1","wait":true}`))
	if err != nil || !strings.Contains(string(result), "503") || len(citations) != 0 || p.ctx.Err() != nil {
		t.Fatalf("child failure cancelled or hid evidence from primary: %s %v", result, err)
	}
	item := a.Items[0]
	args, _ := json.Marshal(qualityFixtureRead(item.Declaration).args)
	_, seen, err := p.options.Read(t.Context(), "review_source", args)
	if err != nil {
		t.Fatal(err)
	}
	test, _ := json.Marshal(qualityFixtureTest(item, 3, 2, "Primary investigated after child failure.").args)
	if err := p.record(a.Items, true, "record_test", test, seen); err != nil {
		t.Fatal(err)
	}
	group := qualityFixtureGroup(item.Declaration, "Primary completed collective review.")
	group.args.(map[string]any)["scope"] = "project"
	raw, _ := json.Marshal(group.args)
	if err := p.record(a.Items, true, "record_group", raw, seen); err != nil {
		t.Fatal(err)
	}
	if err := p.finish(); err != nil {
		t.Fatalf("failed child permanently prevented primary recovery: %v", err)
	}
}

func TestQualityAgentPrimaryRejectsUnownedDelegationScope(t *testing.T) {
	s := qualityFixture(t)
	a, err := s.qualityStore.Create(t.Context(), s.review, "", "")
	if err != nil {
		t.Fatal(err)
	}
	p, err := newQualityPrimary(t.Context(), s, a, intelligence.Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	for _, args := range []string{
		`{"instructions":""}`,
		`{"instructions":"Inspect","tests":[{"snapshotID":"foreign","module":"example","symbolID":"TestF"}]}`,
	} {
		if _, _, err := p.call(t.Context(), "delegate_review", json.RawMessage(args)); err == nil {
			t.Fatalf("invalid delegation accepted: %s", args)
		}
	}
}
