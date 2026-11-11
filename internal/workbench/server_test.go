package workbench

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/russellbrasser/go-review/internal/review"
)

func TestInteractiveWorkbench(t *testing.T) {
	s := New(nil)
	h := s.Handler()
	request := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "http://127.0.0.1:8090"+path, strings.NewReader(body))
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	for _, path := range []string{"/", "/app.js", "/style.css"} {
		w := request("GET", path, "")
		if w.Code != 200 {
			t.Fatalf("%s: %d", path, w.Code)
		}
		if w.Header().Get("Content-Security-Policy") == "" {
			t.Fatal("missing content policy")
		}
	}
	if w := request("GET", "/api/snapshot", ""); w.Code != 404 {
		t.Fatal("missing snapshot not reported")
	}
	if w := request("POST", "/api/analyze", `{"directory":"/does-not-exist"}`); w.Code != 422 {
		t.Fatalf("failure status %d: %s", w.Code, w.Body.String())
	}
	w := request("POST", "/api/analyze", `{"directory":"../review/testdata/module"}`)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var report review.Report
	if err := json.Unmarshal(w.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Packages) != 2 || len(report.Calls) == 0 {
		t.Fatal("no graph evidence")
	}
	if !strings.Contains(w.Body.String(), `\u003cscript\u003e`) {
		t.Fatal("source JSON not safely encoded")
	}
	if w = request("GET", "/api/snapshot", ""); w.Code != 200 {
		t.Fatal("snapshot not retained")
	}
	r := httptest.NewRequest("POST", "http://127.0.0.1:8090/api/analyze", strings.NewReader(`{"directory":"."}`))
	r.Header.Set("Origin", "http://evil.example")
	r.Header.Set("Content-Type", "application/json")
	cross := httptest.NewRecorder()
	h.ServeHTTP(cross, r)
	if cross.Code != 403 {
		t.Fatal("cross origin request accepted")
	}
	r = httptest.NewRequest("GET", "http://rebound.example:8090/api/snapshot", nil)
	cross = httptest.NewRecorder()
	h.ServeHTTP(cross, r)
	if cross.Code != 403 {
		t.Fatal("non-loopback Host accepted")
	}
}

func TestActualProjectSnapshot(t *testing.T) {
	r, err := review.Analyze(context.Background(), review.Options{Dir: "../.."})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Packages) < 3 || len(r.Symbols) < 10 {
		t.Fatal("actual project graph missing")
	}
	for _, cmd := range r.Commands {
		if len(cmd.Args) > 0 && cmd.Args[0] == "test" {
			t.Fatal("analysis executed target tests")
		}
	}
}
