package workbench

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/russellbrasser/go-review/internal/review"
)

func TestScopeAPIStoresRulesWithoutExecutingOrChangingSnapshot(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := t.TempDir()
	s := New(&review.Report{Root: root, Module: "original"})
	request := func(method, url, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://127.0.0.1:8090"+url, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	data, _ := json.Marshal(map[string]interface{}{"directory": root, "scope": map[string]interface{}{"excludePaths": []string{"cmd/bench-*"}, "excludeTests": []string{"^TestSmoke"}}})
	if w := request("POST", "/api/review/scope", string(data)); w.Code != 200 {
		t.Fatalf("save scope: %d %s", w.Code, w.Body.String())
	}
	if w := request("GET", "/api/review/scope?directory="+root, ""); w.Code != 200 || !strings.Contains(w.Body.String(), "cmd/bench-*") {
		t.Fatalf("load scope: %d %s", w.Code, w.Body.String())
	}
	if s.current().Module != "original" || s.reviewRunning {
		t.Fatal("saving scope executed code or silently changed existing results")
	}
	data, _ = json.Marshal(map[string]interface{}{"directory": root, "scope": map[string]interface{}{"excludePaths": []string{"../outside"}}})
	if w := request("POST", "/api/review/scope", string(data)); w.Code != 422 {
		t.Fatalf("invalid scope accepted: %d", w.Code)
	}
	if w := request("GET", "/api/review/scope?directory="+root, ""); !strings.Contains(w.Body.String(), "cmd/bench-*") {
		t.Fatal("invalid update destroyed prior scope")
	}
	if !s.collect.TryLock() {
		t.Fatal("fixture collection unexpectedly busy")
	}
	w := request("POST", "/api/review/scope", string(data))
	s.collect.Unlock()
	if w.Code != 409 {
		t.Fatalf("scope edited during collection: %d", w.Code)
	}
	r := httptest.NewRequest("POST", "http://127.0.0.1:8090/api/review/scope", strings.NewReader(string(data)))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://untrusted.invalid")
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("untrusted origin changed scope: %d", w.Code)
	}
}

func TestScopeUnitCollectionDoesNotRelabelPriorCoverage(t *testing.T) {
	dir := "../review/testdata/module"
	previousScope, err := review.LoadReviewScope(dir)
	if err != nil {
		t.Fatal(err)
	}
	previousScope.ExcludeTests = append(previousScope.ExcludeTests, "^OldPolicy$")
	report, err := review.Analyze(context.Background(), review.Options{Dir: dir, Scope: &previousScope})
	if err != nil {
		t.Fatal(err)
	}
	if err = review.ImportCoverage(report, strings.NewReader("mode: count\nexample.test/fixture/flow.go:18.2,19.6 1 4\n"), "old-policy.cover"); err != nil {
		t.Fatal(err)
	}
	h := New(report).Handler()
	r := httptest.NewRequest("POST", "http://127.0.0.1:8090/api/unit-tests", strings.NewReader(`{"execute":true,"selections":[{"package":".","tests":["TestPaths"]}]}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("trace: %d %s", w.Code, w.Body.String())
	}
	var result review.Report
	if err = json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Coverage == nil || !result.Coverage.SourceVerified || result.Coverage.Origin == "old-policy.cover" || result.Coverage.SHA256 == report.Coverage.SHA256 {
		t.Fatal("prior imported coverage was relabeled rather than replaced by the selected run")
	}
	if report.Coverage.SourceVerified {
		t.Fatal("original unverified coverage snapshot was mutated")
	}
	if len(result.Scope.ExcludeTests) != len(previousScope.ExcludeTests) {
		t.Fatal("unit collection changed snapshot policy")
	}
}
