package workbench

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/russellbrasser/go-review/internal/intelligence"
	"github.com/russellbrasser/go-review/internal/review"
	"github.com/russellbrasser/go-review/internal/testquality"
)

// Exercise delivery of advisory results through the configured provider and MCP.
// Retained trajectories support human judgment of usefulness, not score goldens.
func TestQualityLive(t *testing.T) {
	if os.Getenv("GO_REVIEW_QUALITY_LIVE") != "1" {
		t.Skip("explicit configured-local-model acceptance")
	}
	path, err := intelligence.ConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := intelligence.LoadConfig(path)
	if err != nil || !cfg.Enabled || cfg.External() {
		t.Fatalf("configured local model required: %v", err)
	}
	directory := os.Getenv("GO_REVIEW_QUALITY_TRACE_DIR")
	if directory == "" {
		t.Fatal("GO_REVIEW_QUALITY_TRACE_DIR is required to retain model trajectories")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	contract := `package qualityfixture
import "errors"
var ErrRange=errors.New("range")
// Contract: 1 and 2 are accepted; values below 1 or above 2 are rejected.
func Classify(n int)(string,error) {if n<1||n>2 {return "",ErrRange};return "accepted",nil}
`
	normal := `for _,n:=range []int{1,2} {got,err:=Classify(n);if got!="accepted"||err!=nil {t.Fatalf("got %q,%v",got,err)}}`
	below := `got,err:=Classify(0);if got!=""||err!=ErrRange {t.Fatal(got,err)}`
	above := `got,err=Classify(3);if got!=""||err!=ErrRange {t.Fatal(got,err)}`
	interaction := `
type recordingNotifier struct { calls int; message string; err error }
func (n *recordingNotifier) Notify(message string) error { n.calls++; n.message=message; return n.err }
func TestDelivery(t *testing.T) {
	for _,want:=range []error{nil,ErrRange} {
		sink:=&recordingNotifier{err:want}
		got:=Deliver(sink,"payload")
		if got!=want || sink.calls!=1 || sink.message!="payload" { t.Fatal(got,sink.calls,sink.message) }
	}
}`
	for _, fixture := range []struct{ name, body string }{
		{"combined", "func TestContract(t *testing.T) {" + normal + ";" + below + ";" + above + "}"},
		{"split", "func TestValid(t *testing.T) {" + normal + "}\nfunc TestRejected(t *testing.T) {" + below + ";" + above + "}"},
		{"job", "func TestValid(t *testing.T) {" + normal + "}\nfunc TestRejected(t *testing.T) {" + below + ";" + above + "}"},
		{"missing-behavior", "func TestContract(t *testing.T) {" + normal + "}"},
		{"missing-condition", "func TestContract(t *testing.T) {" + normal + ";" + below + "}"},
		{"logging-only", `func TestContract(t *testing.T) {for _,n:=range []int{1,2,0,3} {v,err:=Classify(n);t.Log(v,err)}}`},
		{"contracted-interaction", interaction},
	} {
		if !t.Run(fixture.name, func(t *testing.T) {
			source := "package qualityfixture;import \"testing\"\n" + fixture.body + "\n"
			formatted, err := format.Source([]byte(source))
			if err != nil {
				t.Fatal(err)
			}
			source = string(formatted)
			productionSource := contract
			if fixture.name == "contracted-interaction" {
				productionSource += `
type Notifier interface { Notify(string) error }
// Contract: Deliver calls Notify once with the unchanged message and returns its error unchanged.
func Deliver(n Notifier,message string) error { return n.Notify(message) }
`
			}
			formattedContract, err := format.Source([]byte(productionSource))
			if err != nil {
				t.Fatal(err)
			}
			production := string(formattedContract)
			fset := token.NewFileSet()
			tree, err := parser.ParseFile(fset, "contract_test.go", source, parser.ParseComments)
			if err != nil {
				t.Fatal(err)
			}
			tests := review.TestSource{File: review.File{ID: "tests", Path: "contract_test.go", Package: "qualityfixture", Source: source, SHA256: testquality.Digest([]byte(source))}}
			for _, decl := range tree.Decls {
				if fn, ok := decl.(*ast.FuncDecl); ok && strings.HasPrefix(fn.Name.Name, "Test") {
					tests.Declarations = append(tests.Declarations, review.TestDeclaration{ID: fn.Name.Name, Name: fn.Name.Name, Kind: "test", Location: review.Location{FileID: "tests", Line: fset.Position(fn.Pos()).Line, EndLine: fset.Position(fn.End()).Line}})
				}
			}
			report := &review.Report{Root: t.TempDir(), Module: "qualityfixture", Files: []review.File{{ID: "contract", Path: "contract.go", Package: "qualityfixture", Source: production, SHA256: testquality.Digest([]byte(production))}}, TestSources: []review.TestSource{tests}}
			a, err := (testquality.Store{Directory: t.TempDir()}).Create(t.Context(), &review.RepositoryReview{Root: report.Root, Modules: []review.ReviewedModule{{Report: report}}}, "", "")
			if err != nil {
				t.Fatal(err)
			}
			file, err := os.CreateTemp(directory, fixture.name+"-*.jsonl")
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			t.Logf("trajectory=%s", file.Name())
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
			defer cancel()
			if fixture.name == "job" {
				server := New(report)
				server.qualityStore = testquality.Store{Directory: filepath.Join(directory, "job-archive")}
				server.ConfigureAgent(cfg)
				start := qualityRequest(t, server, "/api/test-quality/start", `{"allowExecution":false}`)
				if start.Code != 202 {
					t.Fatal(start.Body.String())
				}
				var launched struct{ ID string }
				if err := json.Unmarshal(start.Body.Bytes(), &launched); err != nil {
					t.Fatal(err)
				}
				server.qualityMu.Lock()
				job := server.qualityJobs[launched.ID]
				server.qualityMu.Unlock()
				defer func() {
					job.cancel()
					<-job.done
				}()
				select {
				case <-job.done:
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				saved, err := server.qualityStore.Load(launched.ID)
				if err != nil {
					t.Fatal(err)
				}
				if saved.Suite == nil || len(saved.Suite.Ratings) != 2 || len(saved.Items) != 2 || len(saved.Facts) != 2 || saved.Policy.AllowExecution {
					t.Fatalf("production job lost its evidence, dimensions or policy: %s %s", saved.Status, saved.Error)
				}
				if saved.Status != "completed" && saved.Status != "partial" || saved.Error != "" {
					t.Fatalf("production job failed advisory delivery: %s %s", saved.Status, saved.Error)
				}
				if err := json.NewEncoder(file).Encode(map[string]any{"kind": "job-outcome", "assessment": saved}); err != nil {
					t.Fatal(err)
				}
				qualityLiveBrowserData(t, server, saved, cfg, directory)
				t.Logf("production-job=%s status=%s model=%s metrics=%+v", saved.ID, saved.Status, cfg.Model, saved.Review)
				return
			}

			options, closeSession, err := New(report).qualityEvidence(ctx, a, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer closeSession()
			encoder := json.NewEncoder(file)
			options.Trace = func(event testquality.ReviewTrace) error {
				if len(event.Args) > 0 && !json.Valid(event.Args) {
					event.Args, _ = json.Marshal(string(event.Args))
				}
				return encoder.Encode(event)
			}
			items, err := testquality.AssessBatch(ctx, a, a.Items, cfg, options)
			if err != nil {
				t.Fatal(err)
			}
			a.Items = items
			for _, item := range items {
				if item.Status != testquality.StatusScored && item.Status != testquality.StatusInsufficient || len(item.Ratings) != 2 {
					t.Fatalf("individual protocol/evidence failure: %s %s", item.Name, item.Reason)
				}
				for _, rating := range item.Ratings {
					if strings.TrimSpace(rating.Rationale) == "" || rating.State == testquality.RatingAssessed && len(rating.Evidence) == 0 {
						t.Fatalf("individual advice lacks explanation or evidence: %+v", rating)
					}
				}
			}
			suite, err := testquality.AssessSuite(ctx, a, cfg, options)
			if err != nil {
				t.Fatalf("collective protocol/evidence failure: %v; candidate=%+v", err, suite)
			}
			raw, _ := json.Marshal(suite)
			if err := encoder.Encode(map[string]any{"kind": "outcome", "individual": items, "suite": suite, "metrics": a.Review}); err != nil {
				t.Fatal(err)
			}
			t.Logf("model=%s metrics=%+v suite=%s", cfg.Model, a.Review, raw)
			if len(suite.Ratings) != 2 {
				t.Fatalf("collective dimensions unavailable: %s", raw)
			}
			for _, rating := range suite.Ratings {
				if strings.TrimSpace(rating.Rationale) == "" || rating.State == testquality.RatingAssessed && len(rating.Evidence) == 0 {
					t.Fatalf("collective advice lacks explanation or evidence: %s", raw)
				}
			}
		}) {
			break
		}
	}
}

// Reopen a verified live archive for browser checks without another model call
// or target-test execution. All displayed responses come from production readers.
func TestQualityLiveBrowserArchive(t *testing.T) {
	if os.Getenv("GO_REVIEW_QUALITY_BROWSER_ARCHIVE") != "1" {
		t.Skip("explicit read-only live-archive browser preparation")
	}
	directory := os.Getenv("GO_REVIEW_QUALITY_TRACE_DIR")
	if directory == "" {
		t.Fatal("GO_REVIEW_QUALITY_TRACE_DIR is required")
	}
	store := testquality.Store{Directory: filepath.Join(directory, "job-archive")}
	summaries, err := store.ListSummaries("")
	if err != nil {
		t.Fatal(err)
	}
	if len(summaries) != 1 {
		t.Fatalf("one owned live job required, found %d", len(summaries))
	}
	saved, err := store.Load(summaries[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Suite == nil || saved.Error != "" {
		t.Fatalf("live job did not deliver usable advice: %s %s", saved.Status, saved.Error)
	}
	server := New(saved.Reports[0].Report)
	server.qualityStore = store
	qualityLiveBrowserData(t, server, saved, intelligence.Config{Enabled: true, BaseURL: saved.Provider, Model: saved.Model}, directory)
}

func qualityLiveBrowserData(t *testing.T, server *Server, saved *testquality.Assessment, cfg intelligence.Config, directory string) {
	t.Helper()
	browser := map[string]any{
		"snapshot":       saved.Reports[0].Report,
		"quality-status": map[string]any{"assessments": []any{qualityCompact(saved)}},
		"chat-status":    map[string]any{"enabled": cfg.Enabled, "external": cfg.External(), "model": cfg.Model, "baseURL": cfg.BaseURL},
	}
	for _, operation := range []string{"overview", "indicators", "suite", "suite-groups", "expectations", "tests", "facts", "modules", "diagnostics", "suite-findings"} {
		data, err := server.queryTestQuality(t.Context(), testquality.QueryInput{AssessmentID: saved.ID, Operation: operation})
		if err != nil {
			t.Fatalf("saved %s read failed: %v", operation, err)
		}
		browser[operation] = data
	}
	var expectations []testquality.ExpectedBehavior
	if saved.Suite != nil {
		expectations = saved.Suite.Expectations
	}
	for _, expectation := range expectations {
		for _, operation := range []string{"expectation", "conditions"} {
			data, err := server.queryTestQuality(t.Context(), testquality.QueryInput{
				AssessmentID: saved.ID, Operation: operation, ExpectationID: expectation.ID,
			})
			if err != nil {
				t.Fatalf("saved %s %s read failed: %v", operation, expectation.ID, err)
			}
			browser[operation+"-"+expectation.ID] = data
		}
	}
	for _, item := range saved.Items {
		data, err := server.queryTestQuality(t.Context(), testquality.QueryInput{AssessmentID: saved.ID, Operation: "test", SnapshotID: item.Identity.SnapshotID, SymbolID: item.Identity.SymbolID})
		if err != nil {
			t.Fatal(err)
		}
		browser["test-"+item.Identity.SymbolID] = data
	}
	report := saved.Reports[0]
	files := append([]review.File{}, report.Report.Files...)
	for _, test := range report.Report.TestSources {
		files = append(files, test.File)
	}
	for _, file := range files {
		data, err := server.queryTestQuality(t.Context(), testquality.QueryInput{AssessmentID: saved.ID, Operation: "source", SnapshotID: report.SnapshotID, FileID: file.ID, StartLine: 1, EndLine: len(strings.Split(file.Source, "\n"))})
		if err != nil {
			t.Fatal(err)
		}
		browser["source-"+file.ID] = data
	}
	data, err := json.Marshal(browser)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "browser-data.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}
