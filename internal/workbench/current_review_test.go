package workbench

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/russellbrasser/go-review/internal/review"
)

func TestReviewReplacementStaticAnalysisDeletesOldExecutionEvidence(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"go.mod": "module example.test/analysisreplacement\n\ngo 1.24\n",
		"app.go": "package replacement\n\nfunc F() int { return 7 }\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	report, err := review.Analyze(context.Background(), review.Options{Dir: root})
	if err != nil {
		t.Fatal(err)
	}
	old, err := review.SaveAnalysis(report)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old.Directory, "old-test-output"), []byte("obsolete"), 0600); err != nil {
		t.Fatal(err)
	}
	s := NewReviewed(old)
	body, _ := json.Marshal(map[string]string{"directory": root})
	r := httptest.NewRequest("POST", "http://127.0.0.1:8090/api/analyze", strings.NewReader(string(body)))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(s.review.Directory); err != nil {
			t.Error(err)
		}
	})
	if _, err := os.Stat(old.Directory); !os.IsNotExist(err) {
		t.Fatalf("static analysis left old execution evidence: %v", err)
	}
	if s.review.Status != "analyzed" || s.review.Directory == old.Directory {
		t.Fatal("static replacement did not publish the current snapshot")
	}
	for _, command := range s.current().Commands {
		if len(command.Args) > 0 && command.Args[0] == "test" {
			t.Fatal("static replacement executed target tests")
		}
	}
}

func TestUnitEvidenceRefreshReplacesPriorFailureAndTestCommands(t *testing.T) {
	report, err := review.Analyze(context.Background(), review.Options{Dir: "../review/testdata/module"})
	if err != nil {
		t.Fatal(err)
	}
	s := New(report)
	refresh := func(name string) {
		t.Helper()
		body, _ := json.Marshal(map[string]interface{}{"execute": true, "selections": []review.UnitTestSelection{{Package: ".", Tests: []string{name}}}})
		request := httptest.NewRequest("POST", "http://127.0.0.1:8090/api/unit-tests", strings.NewReader(string(body)))
		request.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, request)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	t.Setenv("GO_REVIEW_TEST_FAIL", "1")
	refresh("TestExplicitOnly")
	old := s.review.Directory
	t.Setenv("GO_REVIEW_TEST_FAIL", "0")
	refresh("TestPaths")
	t.Cleanup(func() {
		if err := os.RemoveAll(s.review.Directory); err != nil {
			t.Error(err)
		}
	})
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("old selected-test evidence still exists: %v", err)
	}
	current := s.current()
	tests := 0
	for _, command := range current.Commands {
		if len(command.Args) > 0 && command.Args[0] == "test" {
			tests++
		}
	}
	if tests != 1 {
		t.Fatalf("current report retained superseded test executions: %d", tests)
	}
	if s.review.Status != "completed" {
		t.Fatal("passing replacement retained prior failure status")
	}
	for _, check := range current.Assessment.Checks {
		if check.ID == "tests" && check.Status != "pass" {
			t.Fatalf("test check still describes obsolete evidence: %+v", check)
		}
	}
	for _, warning := range current.Warnings {
		if strings.HasPrefix(warning, "Selected unit evidence:") {
			t.Fatalf("obsolete test failure warning: %s", warning)
		}
	}
}
