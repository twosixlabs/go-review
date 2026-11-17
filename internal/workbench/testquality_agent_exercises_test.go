package workbench

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
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

func qualityExerciseProvider(t *testing.T, name string) (intelligence.Config, string, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	if os.Getenv("GO_REVIEW_QUALITY_LIVE") != "1" {
		t.Skip("explicit configured-local-model acceptance")
	}
	directory := os.Getenv("GO_REVIEW_QUALITY_TRACE_DIR")
	if directory == "" {
		t.Fatal("GO_REVIEW_QUALITY_TRACE_DIR is required")
	}
	directory = filepath.Join(directory, name)
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	path, err := intelligence.ConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := intelligence.LoadConfig(path)
	if err != nil || !cfg.Enabled || cfg.External() {
		t.Fatalf("configured local model required: %v", err)
	}
	trace, err := os.CreateTemp(directory, "requests-*.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { trace.Close() })
	target, err := url.Parse(cfg.BaseURL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	var mu sync.Mutex
	var requests, active atomic.Int32
	transport := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		active.Add(1)
		defer active.Add(-1)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			http.Error(w, err.Error(), 500)
			return
		}
		r.Body.Close()
		mu.Lock()
		err = json.NewEncoder(trace).Encode(map[string]any{"time": time.Now(), "request": json.RawMessage(body)})
		mu.Unlock()
		if err != nil {
			t.Error(err)
			http.Error(w, err.Error(), 500)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(transport.Close)
	cfg.BaseURL = transport.URL
	t.Logf("model=%s trace=%s", cfg.Model, trace.Name())
	return cfg, directory, &requests, &active
}

func qualityExerciseProject(t *testing.T, files map[string]string, selections []review.UnitTestSelection) *review.Report {
	t.Helper()
	root := t.TempDir()
	files["go.mod"] = "module qualitylive\n\ngo 1.24.0\n"
	for name, source := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	report, err := review.Analyze(t.Context(), review.Options{Dir: root})
	if err != nil {
		t.Fatal(err)
	}
	if err := review.CollectUnitEvidence(t.Context(), report, review.UnitEvidenceOptions{Selections: selections, ArtifactDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	return report
}

func qualityExerciseMixedProject(t *testing.T) *review.Report {
	return qualityExerciseProject(t, map[string]string{
		"README.md": `# Quality review fixture
Parse accepts exactly "true" and "false", rejecting other input.
Clamp maps integers below zero to zero, above ten to ten, and preserves other integers.
Deliver must notify exactly once with the unchanged message and return the notifier error unchanged.
`,
		"parse/parse.go": `package parse
import "errors"
var ErrInput = errors.New("invalid input")
// Parse accepts exactly "true" and "false"; all other strings return ErrInput.
func Parse(s string) (bool,error) {switch s {case "true":return true,nil;case "false":return false,nil};return false,ErrInput}
`,
		"parse/parse_test.go": `package parse
import "testing"
func check(t *testing.T, input string, want bool, wantErr error) {t.Helper();got,err:=Parse(input);if got!=want||err!=wantErr {t.Fatalf("Parse(%q)=(%v,%v), want (%v,%v)",input,got,err,want,wantErr)}}
func TestTrue(t *testing.T) {check(t,"true",true,nil)}
func TestFalse(t *testing.T) {check(t,"false",false,nil)}
func TestInvalid(t *testing.T) {for _,s:=range []string{"","TRUE","invalid"} {check(t,s,false,ErrInput)}}
func TestLoggingOnly(t *testing.T) {v,err:=Parse("true");t.Log(v,err)}
`,
		"bounds/bounds.go": `package bounds
// Clamp maps values into [0,10], preserving values already in range.
func Clamp(n int) int {if n<0{return 0};if n>10{return 10};return n}
`,
		"bounds/bounds_test.go": `package bounds
import "testing"
func TestInterior(t *testing.T) {for _,n:=range []int{1,5,9} {if got:=Clamp(n);got!=n {t.Fatalf("Clamp(%d)=%d",n,got)}}}
`,
		"notify/notify.go": `package notify
type Notifier interface {Notify(string) error}
// Deliver notifies exactly once with unchanged message and returns the notifier's error unchanged.
func Deliver(n Notifier,message string) error {return n.Notify(message)}
`,
		"notify/notify_test.go": `package notify
import ("errors";"testing")
type recorder struct {calls int;message string;err error}
func (r *recorder) Notify(message string) error {r.calls++;r.message=message;return r.err}
func TestSuccess(t *testing.T) {r:=&recorder{};if err:=Deliver(r,"hello");err!=nil||r.calls!=1||r.message!="hello" {t.Fatalf("err=%v recorder=%+v",err,r)}}
func TestError(t *testing.T) {want:=errors.New("offline");r:=&recorder{err:want};if err:=Deliver(r,"hello");err!=want||r.calls!=1||r.message!="hello" {t.Fatalf("err=%v recorder=%+v",err,r)}}
`,
	}, []review.UnitTestSelection{
		{Package: "qualitylive/parse", Tests: []string{"TestTrue", "TestFalse", "TestInvalid", "TestLoggingOnly"}},
		{Package: "qualitylive/bounds", Tests: []string{"TestInterior"}},
		{Package: "qualitylive/notify", Tests: []string{"TestSuccess", "TestError"}},
	})
}

func qualityExerciseStart(t *testing.T, report *review.Report, cfg intelligence.Config, directory string) (*Server, *qualityJob) {
	t.Helper()
	server := New(report)
	server.qualityStore = testquality.Store{Directory: filepath.Join(directory, "archive")}
	server.ConfigureAgent(cfg)
	response := qualityRequest(t, server, "/api/test-quality/start", `{"allowExecution":false}`)
	if response.Code != 202 {
		t.Fatal(response.Body.String())
	}
	var launched struct{ ID string }
	if err := json.Unmarshal(response.Body.Bytes(), &launched); err != nil {
		t.Fatal(err)
	}
	server.qualityMu.Lock()
	job := server.qualityJobs[launched.ID]
	server.qualityMu.Unlock()
	t.Cleanup(func() { job.cancel(); <-job.done })
	t.Logf("assessment=%s", launched.ID)
	return server, job
}

func TestQualityAgentLiveMixed(t *testing.T) {
	cfg, directory, _, _ := qualityExerciseProvider(t, "mixed")
	server, job := qualityExerciseStart(t, qualityExerciseMixedProject(t), cfg, directory)
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Minute)
	defer cancel()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var running map[string]json.RawMessage
running:
	for {
		select {
		case <-job.done:
			break running
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
			server.qualityMu.Lock()
			scored := 0
			for _, item := range job.data.Items {
				if item.Status == testquality.StatusScored {
					scored++
				}
			}
			id, activity, requests := job.data.ID, job.data.Activity, job.data.Review.Requests
			server.qualityMu.Unlock()
			if scored > 0 && running == nil {
				overview, err := server.queryTestQuality(t.Context(), testquality.QueryInput{AssessmentID: id, Operation: "overview"})
				if err != nil {
					t.Fatal(err)
				}
				status := qualityRequest(t, server, "/api/test-quality/status", "")
				running = map[string]json.RawMessage{"overview": overview, "quality-status": append(json.RawMessage(nil), status.Body.Bytes()...)}
				t.Logf("incremental-scored=%d requests=%d activity=%s", scored, requests, activity)
			}
		}
	}
	saved, err := server.qualityStore.Load(job.data.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Status != "completed" || saved.Suite == nil || saved.Suite.Status != testquality.StatusScored {
		t.Fatalf("mixed review did not complete: %s %s", saved.Status, saved.Error)
	}
	for _, item := range saved.Items {
		if item.Status != testquality.StatusScored || len(item.Ratings) != 2 {
			t.Fatalf("missing test advice: %s %s", item.Name, item.Reason)
		}
		for _, rating := range item.Ratings {
			if rating.Rationale == "" || len(rating.Evidence) == 0 {
				t.Fatalf("uncited or unexplained advice: %+v", rating)
			}
		}
		t.Logf("test=%s package=%s ratings=%+v findings=%+v", item.Name, item.Package, item.Ratings, item.Findings)
	}
	if running == nil {
		t.Fatal("no incremental results observed before completion")
	}
	qualityLiveBrowserData(t, server, saved, cfg, directory)
	raw, err := json.Marshal(running)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "running-data.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("completed requests=%d groups=%d", saved.Review.Requests, len(saved.Suite.Groups))
}

func TestQualityAgentLiveDelegation(t *testing.T) {
	cfg, directory, _, _ := qualityExerciseProvider(t, "delegation")
	report := qualityExerciseProject(t, map[string]string{
		"sample.go":      "package qualitylive\nfunc Value() int {return 1}\n",
		"sample_test.go": "package qualitylive\nimport \"testing\"\nfunc TestValue(t *testing.T) {if Value()!=1 {t.Fatal(\"wrong value\")}}\n",
	}, []review.UnitTestSelection{{Package: "qualitylive", Tests: []string{"TestValue"}}})
	server := New(report)
	server.qualityStore = testquality.Store{Directory: filepath.Join(directory, "archive")}
	a, err := server.qualityStore.Create(t.Context(), &review.RepositoryReview{Root: report.Root, Modules: []review.ReviewedModule{{Report: report}}}, cfg.BaseURL, cfg.Model)
	if err != nil {
		t.Fatal(err)
	}
	a.Policy = testquality.DefaultDiagnosticPolicy(false)
	a.Status, a.Phase = "running", "reviewing"
	if err := server.qualityStore.Save(a); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	p, err := newQualityPrimary(ctx, server, a, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	trace, err := os.CreateTemp(directory, "primary-tools-*.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer trace.Close()
	delegated, readReport, finished := false, false, false
	options := p.options
	originalTrace := options.Trace
	options.Trace = func(event testquality.ReviewTrace) error {
		if err := originalTrace(event); err != nil {
			return err
		}
		if err := json.NewEncoder(trace).Encode(event); err != nil {
			return err
		}
		if event.Kind == "tool" {
			switch event.Tool {
			case "delegate_review":
				delegated = delegated || event.Error == ""
			case "read_review_agent":
				readReport = readReport || event.Error == "" && strings.Contains(string(event.Result), `"status":"completed"`)
			case "finish_review":
				finished = finished || event.Error == ""
			}
		}
		return nil
	}
	scope, err := json.Marshal(map[string]any{"snapshots": p.snapshots(), "testCount": len(p.items), "policy": a.Policy})
	if err != nil {
		t.Fatal(err)
	}
	prompt := qualityPrimaryPrompt + string(scope) + `
For this bounded acceptance exercise, delegate TestValue's individual assessment to one child,
read its completed report and evaluate the result.
Inspect source yourself for the project collective judgment and finish the review. Do not execute fresh tests.`
	if err := runQualityAgent(ctx, cfg, prompt, options, func(name string, args json.RawMessage, seen []testquality.Citation) error {
		return p.record(p.items, true, name, args, seen)
	}); err != nil {
		t.Fatal(err)
	}
	p.close()
	saved, err := server.qualityStore.Load(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !delegated || !readReport || !finished || saved.Items[0].Status != testquality.StatusScored || saved.Suite == nil || saved.Suite.Status != testquality.StatusScored || len(saved.Diagnostics) != 0 {
		t.Fatalf("primary did not complete the delegated exercise: delegated=%v read-report=%v finished=%v", delegated, readReport, finished)
	}
	t.Logf("assessment=%s delegated=true read-completed-report=true project-synthesis-saved=true explicit-finish=true", saved.ID)
}

func TestQualityAgentLiveDiscovery(t *testing.T) {
	cfg, directory, requests, _ := qualityExerciseProvider(t, "discovery")
	report := qualityExerciseProject(t, map[string]string{
		"clarification/normalize.go":      "package clarification\nimport \"strings\"\n// Normalize trims leading and trailing whitespace, preserving interior whitespace.\nfunc Normalize(s string) string {return strings.TrimSpace(s)}\n",
		"clarification/normalize_test.go": "package clarification\nimport \"testing\"\nfunc TestTrim(t *testing.T) {if got:=Normalize(\"  a b  \");got!=\"a b\" {t.Fatal(got)}}\nfunc TestEmpty(t *testing.T) {if got:=Normalize(\" \\t \");got!=\"\" {t.Fatal(got)}}\n",
		"other/other.go":                  "package other\nfunc Value() int {return 1}\n",
		"other/other_test.go":             "package other\nimport \"testing\"\nfunc TestOther(t *testing.T) {if Value()!=1 {t.Fatal(\"wrong\")}}\n",
	}, []review.UnitTestSelection{{Package: "qualitylive/clarification", Tests: []string{"TestTrim", "TestEmpty"}}})
	if err := os.WriteFile(filepath.Join(report.Root, "clarification", "review-note.txt"), []byte("live-discovery-marker: cedar-42\nThis is live-only context, not retained grading evidence.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	server := New(report)
	server.qualityStore = testquality.Store{Directory: filepath.Join(directory, "archive")}
	a, err := server.qualityStore.Create(t.Context(), &review.RepositoryReview{Root: report.Root, Modules: []review.ReviewedModule{{Report: report}}}, cfg.BaseURL, cfg.Model)
	if err != nil {
		t.Fatal(err)
	}
	a.Policy = testquality.DefaultDiagnosticPolicy(false)
	a.Status, a.Phase = "running", "reviewing"
	if err := server.qualityStore.Save(a); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	p, err := newQualityPrimary(ctx, server, a, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	scope, _ := json.Marshal(map[string]any{"snapshots": p.snapshots(), "testCount": len(p.items), "policy": a.Policy})
	prompt := qualityPrimaryPrompt + string(scope) + `
For this bounded acceptance exercise, delegate a focused investigation of
package qualitylive/clarification. Ask the child to discover this package's selected test
identities, inspect its retained test source and read live clarification/review-note.txt.
The child should report the identities, findings and exact live marker. Read and evaluate
its completed report. Complete the selected tests and project synthesis using pinned evidence.
Keep the child's investigation focused on clarification. You may directly inspect
other selected tests needed to finish the project. Do not execute fresh tests.`
	if err := runQualityAgent(ctx, cfg, prompt, p.options, func(name string, args json.RawMessage, seen []testquality.Citation) error {
		return p.record(p.items, true, name, args, seen)
	}); err != nil {
		t.Fatal(err)
	}
	p.close()
	beforeRead := requests.Load()
	file, err := os.Open(filepath.Join(server.qualityStore.Directory, a.ID, "agent-events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	discovered, retainedRead, liveRead, reportedMarker := false, false, false, false
	decoder := json.NewDecoder(file)
	eventCount := 0
	for {
		var event testquality.AgentEvent
		err := decoder.Decode(&event)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		eventCount++
		if event.ParentID != "Lead" || event.Error != "" {
			continue
		}
		if event.Kind == "message" && strings.Contains(event.Message, "cedar-42") {
			reportedMarker = true
		}
		if event.Kind != "tool" {
			continue
		}
		var args struct{ Source, Path, Package string }
		if err := json.Unmarshal(event.Args, &args); err != nil {
			t.Fatal(err)
		}
		if event.Tool == "review_tests" && args.Package == "qualitylive/clarification" {
			discovered = true
		}
		if event.Tool == "review_source" || event.Tool == "review_read_file" && args.Source != "live" {
			retainedRead = true
		}
		if event.Tool == "review_read_file" && args.Source == "live" && args.Path == "clarification/review-note.txt" {
			liveRead = true
		}
	}
	saved, err := server.qualityStore.Load(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range saved.Items {
		if item.Status != testquality.StatusScored {
			t.Fatalf("selected test unfinished: %s", item.Name)
		}
	}
	if !discovered || !retainedRead || !liveRead || !reportedMarker || saved.Suite == nil || saved.Suite.Status != testquality.StatusScored || len(saved.Diagnostics) != 0 || requests.Load() != beforeRead {
		t.Fatalf("discovery incomplete: selected=%v retained=%v live=%v marker=%v suite=%v", discovered, retainedRead, liveRead, reportedMarker, saved.Suite != nil)
	}
	t.Logf("assessment=%s selected-tests=%d events=%d requests=%d child-discovery=true retained-source=true live-marker=true synthesis-saved=true", a.ID, len(saved.Items), eventCount, beforeRead)
}

func TestQualityAgentLiveDiagnostic(t *testing.T) {
	cfg, directory, _, _ := qualityExerciseProvider(t, "diagnostic")
	report := qualityExerciseProject(t, map[string]string{
		"sample.go": "package qualitylive\nfunc Value() int {return 1}\n",
		"sample_test.go": `package qualitylive
import ("os";"testing")
func TestRerun(t *testing.T) {if Value()!=1 {t.Fatal("wrong value")};if err:=os.WriteFile("rerun-marker",[]byte("ran"),0600);err!=nil {t.Fatal(err)};t.Log("live diagnostic ran")}
func TestNotSelected(t *testing.T) {if err:=os.WriteFile("unselected-marker",[]byte("ran"),0600);err!=nil {t.Fatal(err)}}
`,
	}, []review.UnitTestSelection{{Package: "qualitylive", Tests: []string{"TestRerun"}}})
	marker := filepath.Join(report.Root, "rerun-marker")
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	server := New(report)
	server.qualityStore = testquality.Store{Directory: filepath.Join(directory, "archive")}
	a, err := server.qualityStore.Create(t.Context(), &review.RepositoryReview{Root: report.Root, Modules: []review.ReviewedModule{{Report: report}}}, cfg.BaseURL, cfg.Model)
	if err != nil {
		t.Fatal(err)
	}
	a.Policy = testquality.DefaultDiagnosticPolicy(true)
	var selected testquality.Responsibility
	for _, item := range a.Items {
		if item.Name == "TestRerun" {
			selected = item
		}
	}
	if selected.Name == "" {
		t.Fatal("missing diagnostic target")
	}
	evidence := &testquality.Assessment{ID: a.ID, Version: a.Version, Root: a.Root, Reports: a.Reports, Items: append([]testquality.Responsibility(nil), a.Items...), Policy: a.Policy}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	options, closeSession, err := server.qualityAgentEvidence(ctx, a, evidence)
	if err != nil {
		t.Fatal(err)
	}
	defer closeSession()
	trajectory, err := os.CreateTemp(directory, "tools-*.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer trajectory.Close()
	options.Trace = func(event testquality.ReviewTrace) error { return json.NewEncoder(trajectory).Encode(event) }
	scope, err := json.Marshal(selected)
	if err != nil {
		t.Fatal(err)
	}
	prompt := qualityAgentPrompt + string(scope) + "\nThe user authorizes and requests exactly one rerun of TestRerun to verify its current result. Read its source, run it once with run_test_diagnostic (kind rerun, repetitions 1), inspect the returned output, and record its assessment. Do not execute other tests."
	group := qualityAgentGroup{SnapshotID: selected.Identity.SnapshotID, Module: selected.Identity.Module, Package: selected.Package, Tests: []testquality.Responsibility{selected}}
	err = runQualityAgent(ctx, cfg, prompt, options, func(name string, args json.RawMessage, seen []testquality.Citation) error {
		return server.recordQualityAgent(a, group, name, args, seen)
	})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := server.qualityStore.Load(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Diagnostics) != 1 || saved.Diagnostics[0].Status != "pass" || len(saved.Diagnostics[0].Runs) != 1 {
		t.Fatalf("authorized agent diagnostic missing: %+v", saved.Diagnostics)
	}
	raw, _ := json.Marshal(saved.Diagnostics[0])
	if !strings.Contains(string(raw), "live diagnostic ran") {
		t.Fatal("actual diagnostic command output was not retained")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("selected test did not actually rerun: %v", err)
	}
	if _, err := os.Stat(filepath.Join(report.Root, "unselected-marker")); !os.IsNotExist(err) {
		t.Fatalf("unselected test executed: %v", err)
	}
	for _, item := range saved.Items {
		if item.Identity == selected.Identity && item.Status != testquality.StatusScored {
			t.Fatal("agent did not record its diagnostic-backed assessment")
		}
	}
	t.Logf("assessment=%s diagnostic=%s output-retained=true unselected-executed=false", saved.ID, saved.Diagnostics[0].Status)
}

func TestQualityAgentLiveCancel(t *testing.T) {
	cfg, directory, requests, active := qualityExerciseProvider(t, "cancel")
	server, job := qualityExerciseStart(t, qualityExerciseMixedProject(t), cfg, directory)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	scoredBefore, unreviewedBefore := 0, 0
waiting:
	for {
		select {
		case <-job.done:
			t.Fatal("review finished before cancellation could be exercised")
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
			server.qualityMu.Lock()
			scored, unreviewed := 0, 0
			for _, item := range job.data.Items {
				switch item.Status {
				case testquality.StatusScored:
					scored++
				case testquality.StatusUnreviewed:
					unreviewed++
				}
			}
			server.qualityMu.Unlock()
			if scored > 0 && unreviewed > 0 && active.Load() > 0 {
				scoredBefore, unreviewedBefore = scored, unreviewed
				response := qualityRequest(t, server, "/api/test-quality/cancel", fmt.Sprintf(`{"assessmentID":%q}`, job.data.ID))
				if response.Code != 200 {
					t.Fatal(response.Body.String())
				}
				break waiting
			}
		}
	}
	select {
	case <-job.done:
	case <-time.After(10 * time.Second):
		t.Fatal("live job did not stop after cancellation")
	}
	for deadline := time.Now().Add(5 * time.Second); active.Load() != 0 && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
	}
	if active.Load() != 0 {
		t.Fatal("provider HTTP requests remained active after cancellation")
	}
	saved, err := server.qualityStore.Load(job.data.ID)
	if err != nil {
		t.Fatal(err)
	}
	summary, err := testquality.AggregateVersion(saved.Version, saved.Items)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Status != "cancelled" || summary.Counts.Scored < scoredBefore || summary.Counts.Unreviewed == 0 {
		t.Fatalf("cancellation lost or invented results: %s %+v", saved.Status, summary)
	}
	before := requests.Load()
	reopened := New(saved.Reports[0].Report)
	reopened.qualityStore = server.qualityStore
	reopened.ConfigureAgent(cfg)
	for _, operation := range []string{"overview", "tests", "suite", "suite-groups", "indicators"} {
		if _, err := reopened.queryTestQuality(t.Context(), testquality.QueryInput{AssessmentID: saved.ID, Operation: operation}); err != nil {
			t.Fatalf("cancelled archive %s unavailable: %v", operation, err)
		}
	}
	time.Sleep(200 * time.Millisecond)
	if requests.Load() != before {
		t.Fatal("reopening cancelled results restarted inference")
	}
	qualityLiveBrowserData(t, reopened, saved, cfg, directory)
	t.Logf("cancelled scored-before=%d unreviewed-before=%d saved-scored=%d saved-unreviewed=%d active-provider-requests=%d reopen-added-requests=%d",
		scoredBefore, unreviewedBefore, summary.Counts.Scored, summary.Counts.Unreviewed, active.Load(), requests.Load()-before)
}
