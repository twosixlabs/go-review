package workbench

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/russellbrasser/go-review/internal/intelligence"
	"github.com/russellbrasser/go-review/internal/review"
	"github.com/russellbrasser/go-review/internal/testquality"
)

func TestQualityDiagnosticJobRetainsMultiModuleEvidenceAndExecutionOptOut(t *testing.T) {
	for _, allow := range []bool{true, false} {
		t.Run(fmt.Sprintf("allow-%t", allow), func(t *testing.T) {
			run := &review.RepositoryReview{Root: t.TempDir()}
			var markers, unreviewed []string
			for _, module := range []string{"first", "second"} {
				root := t.TempDir()
				marker := filepath.Join(t.TempDir(), "rerun")
				excluded := filepath.Join(t.TempDir(), "unreviewed")
				markers = append(markers, marker)
				unreviewed = append(unreviewed, excluded)
				files := map[string]string{
					"go.mod": "module " + module + "\ngo 1.24\n",
					"f.go":   "package example\nfunc F() int {return 1}\n",
					"f_test.go": fmt.Sprintf(`package example
import ("testing"; "time"; "os")
func TestAllowed(t *testing.T) {
 time.Sleep(time.Millisecond)
 if F()!=1 {t.Fatal("wrong")}
 if err:=os.WriteFile(%q,[]byte("ran"),0600);err!=nil {t.Fatal(err)}
}
func TestUnreviewed(t *testing.T) {if err:=os.WriteFile(%q,[]byte("ran"),0600);err!=nil {t.Fatal(err)}}
`, marker, excluded),
				}
				for name, text := range files {
					if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0600); err != nil {
						t.Fatal(err)
					}
				}
				report, err := review.Analyze(t.Context(), review.Options{Dir: root})
				if err != nil {
					t.Fatal(err)
				}
				if err := review.CollectUnitEvidence(t.Context(), report, review.UnitEvidenceOptions{
					Selections: []review.UnitTestSelection{{Package: module, Tests: []string{"TestAllowed"}}}, ArtifactDir: t.TempDir(),
				}); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(marker); err != nil {
					t.Fatal(err)
				}
				run.Modules = append(run.Modules, review.ReviewedModule{Directory: root, Report: report})
			}
			before, err := json.Marshal(run)
			if err != nil {
				t.Fatal(err)
			}
			s := New(run.Modules[0].Report)
			s.review = run
			s.qualityStore = testquality.Store{Directory: t.TempDir()}
			start := qualityRequest(t, s, "/api/test-quality/start", fmt.Sprintf(`{"allowExecution":%t}`, allow))
			if start.Code != 202 {
				t.Fatal(start.Body.String())
			}
			var result struct{ ID string }
			if err := json.Unmarshal(start.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			s.qualityMu.Lock()
			job := s.qualityJobs[result.ID]
			s.qualityMu.Unlock()
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				if err := s.ShutdownTestQuality(ctx); err != nil {
					t.Error(err)
				}
			}()
			select {
			case <-job.done:
			case <-time.After(30 * time.Second):
				t.Fatal("owned diagnostic job did not finish")
			}
			a, err := s.qualityStore.Load(result.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(a.Facts) != 4 || a.Policy.AllowExecution != allow || a.Status != "partial" {
				t.Fatalf("facts-only assessment lost evidence or policy: status=%s facts=%d error=%s", a.Status, len(a.Facts), a.Error)
			}
			if allow && len(a.Diagnostics) != 2 || !allow && len(a.Diagnostics) != 0 {
				t.Fatalf("wrong diagnostic authorization: allowed=%t records=%d reason=%s", allow, len(a.Diagnostics), a.DiagnosticReason)
			}
			for _, record := range a.Diagnostics {
				if !record.SourceVerified || len(record.Runs) != 1 || len(record.Runs[0].Attempts) != 2 || len(record.Artifacts) == 0 {
					t.Fatalf("real diagnostic provenance was lost: %+v", record)
				}
			}
			for _, marker := range markers {
				_, err := os.Stat(marker)
				if allow && err != nil || !allow && !os.IsNotExist(err) {
					t.Fatalf("execution opt-out was not absolute: allowed=%t error=%v", allow, err)
				}
			}
			for _, marker := range unreviewed {
				if _, err := os.Stat(marker); !os.IsNotExist(err) {
					t.Fatal("unreviewed code was executed")
				}
			}
			after, err := json.Marshal(run)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("diagnostic job mutated its original multi-module review")
			}
		})
	}
}

// Exercise the real start/job/checkpoint/read paths; only local inference is
// replaced. Overall synthesis is paused so earlier advice must already be saved.
func TestQualityLargeJobPublishesCollectiveGroupsBeforeOverallSynthesis(t *testing.T) {
	var source strings.Builder
	source.WriteString("package example; import \"testing\"\n")
	for i := 0; i < 128; i++ {
		fmt.Fprintf(&source, "func TestCase%d(t *testing.T) {if F()!=1 {t.Fatal(\"wrong\")}}\n", i)
	}
	fset := token.NewFileSet()
	tree, err := parser.ParseFile(fset, "f_test.go", source.String(), 0)
	if err != nil {
		t.Fatal(err)
	}
	tests := review.TestSource{File: review.File{ID: "tests", Path: "f_test.go", Package: "example", Source: source.String(), SHA256: testquality.Digest([]byte(source.String()))}}
	for _, decl := range tree.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			tests.Declarations = append(tests.Declarations, review.TestDeclaration{ID: fn.Name.Name, Name: fn.Name.Name, Kind: "test",
				Location: review.Location{FileID: "tests", Line: fset.Position(fn.Pos()).Line, EndLine: fset.Position(fn.End()).Line}})
		}
	}
	production := "package example\nfunc F() int {return 1}\n"
	report := &review.Report{Root: t.TempDir(), Module: "example",
		Files:       []review.File{{ID: "production", Path: "f.go", Package: "example", Source: production, SHA256: testquality.Digest([]byte(production))}},
		TestSources: []review.TestSource{tests}}
	ready, release := make(chan struct{}), make(chan struct{})
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []intelligence.Message
			Format   struct {
				Type   string
				Schema struct{ Name string } `json:"json_schema"`
			} `json:"response_format"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		var initial struct {
			Tests  []struct{ Test testquality.Responsibility }
			Groups []json.RawMessage
		}
		if err := json.Unmarshal([]byte(request.Messages[1].Content), &initial); err != nil {
			t.Error(err)
			return
		}
		if len(initial.Groups) > 0 && request.Format.Type == "" {
			close(ready)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		content := "Evidence ready."
		if request.Format.Type != "" {
			var receipt struct {
				References map[string]testquality.Citation
			}
			if err := json.Unmarshal([]byte(request.Messages[len(request.Messages)-1].Content), &receipt); err != nil {
				t.Error(err)
				return
			}
			var contract testquality.Citation
			for _, c := range receipt.References {
				if c.FileID == "production" {
					contract = c
					break
				}
			}
			if contract.FileID == "" {
				t.Error("called production evidence missing")
				return
			}
			ratings := func(dimensions []testquality.Dimension, evidence []testquality.Citation) []testquality.Rating {
				value := 3
				out := []testquality.Rating{}
				for _, dimension := range dimensions {
					out = append(out, testquality.Rating{Dimension: dimension, State: testquality.RatingAssessed,
						Value: &value, Rationale: "Source-backed advisory finding", Evidence: evidence})
				}
				return out
			}
			proposal := map[string]any{}
			if request.Format.Schema.Name == "submit_batch" {
				items := []any{}
				for _, test := range initial.Tests {
					items = append(items, map[string]any{"identity": test.Test.Identity, "ratings": ratings(
						[]testquality.Dimension{testquality.Craftsmanship, testquality.AssertionStrength},
						[]testquality.Citation{test.Test.Declaration, contract}), "findings": []any{}})
				}
				proposal["items"] = items
			} else {
				proposal["ratings"] = ratings([]testquality.Dimension{testquality.BehaviorAdequacy, testquality.ConditionAdequacy}, []testquality.Citation{contract})
				proposal["findings"] = []any{}
				expectations := []testquality.ExpectedBehavior{}
				if len(initial.Groups) == 0 {
					expectations = append(expectations, testquality.ExpectedBehavior{ID: "result", Description: "Returns one", Basis: "implementation-derived",
						State: "supported", Rationale: "Related tests assert the result.", Evidence: []testquality.Citation{contract},
						Tests: []testquality.Identity{}, Conditions: []testquality.ExpectedCondition{}})
				}
				proposal["expectations"] = expectations
			}
			raw, _ := json.Marshal(proposal)
			converted, err := qualityProposalLabels(raw, request.Messages[len(request.Messages)-1].Content)
			if err != nil {
				t.Error(err)
				return
			}
			content = string(converted)
		}
		raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": content}, "finish_reason": "stop"}}})
		fmt.Fprintf(w, "data: %s\n\n", raw)
	}))
	defer provider.Close()
	s := New(report)
	s.qualityStore = testquality.Store{Directory: t.TempDir()}
	s.ConfigureAgent(intelligence.Config{Enabled: true, BaseURL: provider.URL, Model: "fixture", MaxSteps: 2, ContextBytes: 65536})
	launch := qualityRequest(t, s, "/api/test-quality/start", `{"allowExecution":false}`)
	if launch.Code != 202 {
		t.Fatal(launch.Body.String())
	}
	var result struct{ ID string }
	if err := json.Unmarshal(launch.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	s.qualityMu.Lock()
	job := s.qualityJobs[result.ID]
	s.qualityMu.Unlock()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
		<-job.done
	}()
	select {
	case <-ready:
	case <-time.After(20 * time.Second):
		t.Fatal("large job never reached overall synthesis")
	}
	checkpoint, err := s.qualityStore.Load(result.ID)
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.Suite == nil || len(checkpoint.Suite.Expectations) < 2 || checkpoint.Suite.Status != testquality.StatusInsufficient {
		if checkpoint.Suite == nil {
			t.Fatal("completed collective groups were not persisted before overall synthesis")
		}
		t.Fatalf("completed collective groups were not retained: status=%s reason=%s", checkpoint.Suite.Status, checkpoint.Suite.Reason)
	}
	data, err := s.queryTestQuality(t.Context(), testquality.QueryInput{AssessmentID: result.ID, Operation: "suite"})
	if err != nil || !strings.Contains(string(data), `"scopeCount":128`) {
		t.Fatalf("active readers lost collective scope: %s %v", data, err)
	}
	close(release)
	select {
	case <-job.done:
	case <-time.After(20 * time.Second):
		t.Fatal("large job did not finish after synthesis")
	}
	finished, err := s.qualityStore.Load(result.ID)
	if err != nil {
		t.Fatal(err)
	}
	if finished.Status != "completed" || len(finished.Items) != 128 || finished.Suite == nil || len(finished.Suite.Scope) != 128 {
		t.Fatalf("large assessment did not finish with retained individual/collective results: status=%s error=%s", finished.Status, finished.Error)
	}
}

func TestQualityFactsOnlyJobAndExecutionOptOut(t *testing.T) {
	for _, optOut := range []bool{false, true} {
		t.Run(map[bool]string{false: "default", true: "opt-out"}[optOut], func(t *testing.T) {
			source := "package example; import \"testing\"\nfunc TestF(t *testing.T) {if 1!=1 {t.Fatal(\"wrong\")}}\n"
			r := &review.Report{Root: t.TempDir(), Module: "example", TestSources: []review.TestSource{{File: review.File{ID: "tests", Path: "sample_test.go", Package: "example", Source: source, SHA256: testquality.Digest([]byte(source))}, Declarations: []review.TestDeclaration{{ID: "test", Name: "TestF", Kind: "test", Location: review.Location{FileID: "tests", Line: 2, EndLine: 2}}}}}}
			s := New(r)
			s.qualityStore = testquality.Store{Directory: t.TempDir()}
			body := []byte(`{}`)
			if optOut {
				body = []byte(`{"allowExecution":false}`)
			}
			request := httptest.NewRequest("POST", "http://127.0.0.1/api/test-quality/start", bytes.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			s.Handler().ServeHTTP(response, request)
			if response.Code != 202 {
				t.Fatalf("AI-disabled baseline was blocked: %d %s", response.Code, response.Body)
			}
			var result struct{ ID string }
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			s.qualityMu.Lock()
			job := s.qualityJobs[result.ID]
			s.qualityMu.Unlock()
			<-job.done
			a, err := s.qualityStore.Load(result.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(a.Facts) != 1 || a.Version != testquality.EvidenceRubricVersion {
				t.Fatalf("missing real v3 facts: %+v", a)
			}
			if a.Policy.AllowExecution == optOut {
				t.Fatalf("wrong effective execution authorization: %+v", a.Policy)
			}
			if len(a.Diagnostics) != 0 {
				t.Fatal("unreviewed source silently became an execution pool")
			}
		})
	}
}
