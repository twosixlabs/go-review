package workbench

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/russellbrasser/go-review/internal/review"
)

func TestReviewEndpointRequiresExplicitExecutionAndKeepsExistingSnapshotOnError(t *testing.T) {
	s := New(&review.Report{Module: "original"})
	request := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://127.0.0.1:8090/api/review", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}

	if w := request(`{"directory":".","execute":false}`); w.Code != 400 {
		t.Fatalf("review accepted without consent: %d %s", w.Code, w.Body.String())
	}
	if w := request(`{"directory":"/does-not-exist","execute":true}`); w.Code != 422 {
		t.Fatalf("review error lost: %d %s", w.Code, w.Body.String())
	}
	if s.current().Module != "original" {
		t.Fatal("failed collection erased previous review")
	}
}

func TestRetainedReviewModuleSelectionDoesNotExecuteAgain(t *testing.T) {
	s := NewReviewed(&review.RepositoryReview{Root: "/repo", Directory: "/retained/run", Status: "attention",
		Modules: []review.ReviewedModule{
			{Directory: "/repo/a", Report: &review.Report{Root: "/repo/a", Module: "a"}},
			{Directory: "/repo/b", Report: &review.Report{Root: "/repo/b", Module: "b"}},
			{Directory: "/repo/broken", Error: "could not parse module"},
		}})
	r := httptest.NewRequest("GET", "http://127.0.0.1:8090/api/review", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "could not parse module") || !strings.Contains(w.Body.String(), "/retained/run") {
		t.Fatal("review summary lost failed modules or artifact location")
	}
	r = httptest.NewRequest("POST", "http://127.0.0.1:8090/api/review/select", strings.NewReader(`{"directory":"/repo/b"}`))
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	var report review.Report
	if err := json.Unmarshal(w.Body.Bytes(), &report); err != nil || report.Module != "b" || s.current().Module != "b" {
		t.Fatalf("selected snapshot not used by drill-downs: %s %v", w.Body.String(), err)
	}
}

func TestOneReviewRequestCollectsAndRetainsCoverageAndCalls(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for name, source := range map[string]string{
		"go.mod": "module example.com/oneclick\n\ngo 1.24\n",
		"app.go": "package oneclick\n\nfunc F() int { return 7 }\n",
		"app_test.go": `package oneclick
import ("os"; "testing"; "time")
func TestF(t *testing.T) {
    deadline := time.Now().Add(20*time.Second)
    for {
        if _, err := os.Stat("release-test"); err == nil { break }
        if time.Now().After(deadline) { t.Fatal("test release timed out") }
        time.Sleep(10*time.Millisecond)
    }
    if F()!=7 { t.Fatal("bad") }
}
`,
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	body, err := json.Marshal(map[string]interface{}{"directory": root, "execute": true})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", "http://127.0.0.1:8090/api/review", strings.NewReader(string(body)))
	request.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	server := New(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	request = request.WithContext(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		server.Handler().ServeHTTP(w, request)
	}()
	defer func() {
		cancel()
		<-done
	}()
	for {
		status := httptest.NewRecorder()
		server.Handler().ServeHTTP(status, httptest.NewRequest("GET", "http://127.0.0.1:8090/api/review", nil))
		var state struct {
			Running  bool                  `json:"running"`
			Progress review.ReviewProgress `json:"progress"`
		}
		if err := json.Unmarshal(status.Body.Bytes(), &state); err != nil {
			t.Fatal(err)
		}
		c := state.Progress.Command
		if state.Running && c != nil && c.Executable == "go" && len(c.Args) > 0 && c.Args[0] == "test" {
			if state.Progress.TotalStages != 6 || state.Progress.CompletedStages != 4 {
				t.Fatalf("HTTP progress must count completed stages, not the current test stage: %+v", state.Progress)
			}
			if c.Directory != root || c.StartedAt.IsZero() || !c.FinishedAt.IsZero() {
				t.Fatalf("incorrect live command: %+v", c)
			}
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("no live test command exposed:", ctx.Err())
		case <-done:
			t.Fatal("review finished without exposing its running test command")
		case <-time.After(10 * time.Millisecond):
		}
	}
	// Release the real target test only after another HTTP request has observed it.
	if err := os.WriteFile(filepath.Join(root, "release-test"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	report := server.current()
	if report == nil || report.Coverage == nil || report.UnitEvidence == nil || report.UnitEvidence.Runs[0].Status != "pass" || report.Review == nil {
		t.Fatal("single operation did not populate review evidence")
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(report.Review.Directory); err != nil {
			t.Error(err)
		}
	})
	if _, err := os.Stat(filepath.Join(report.Review.Directory, "review.json")); err != nil {
		t.Fatal(err)
	}
}
