package workbench

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/russellbrasser/go-review/internal/changes"
	"github.com/russellbrasser/go-review/internal/review"
)

func TestChangeEndpointsPreserveApplicationAndRejectStaleState(t *testing.T) {
	root := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.test", "GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.test")
		data, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %s", err, data)
		}
		return strings.TrimSpace(string(data))
	}
	run("init", "-q")
	if err := os.WriteFile(filepath.Join(root, "notes.md"), []byte("before\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-qm", "before")
	before := run("rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(root, "notes.md"), []byte("after\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-qm", "after")
	resolved, err := changes.Resolve(context.Background(), changes.Request{Directory: root, Before: before, After: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := changes.Collect(context.Background(), resolved, t.TempDir(), review.ReviewScope{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	report := &review.Report{Module: "independent Application", SourceSHA256: "application"}
	s := New(report)
	s.comparison = c
	handler := s.Handler()
	call := func(path string, body any, code int) map[string]any {
		t.Helper()
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest("POST", "http://127.0.0.1:8090"+path, strings.NewReader(string(data)))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != code {
			t.Fatalf("%s: %d %s", path, response.Code, response.Body.String())
		}
		var decoded map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
			t.Fatal(err)
		}
		return decoded
	}
	result := call("/api/changes/query", map[string]any{"comparisonID": c.ID, "operation": "overview"}, 200)
	if result["comparisonID"] != c.ID {
		t.Fatal("missing comparison discovery")
	}
	call("/api/changes/query", map[string]any{"comparisonID": "stale", "operation": "overview"}, 409)
	file := c.Files[0]
	source := call("/api/changes/query", map[string]any{"comparisonID": c.ID, "operation": "source", "fileID": file.ID, "side": "before"}, 200)
	if source["text"] != "before\n" {
		t.Fatalf("source was not revision bound: %v", source)
	}
	call("/api/changes/state", map[string]any{"comparisonID": c.ID, "version": 0, "itemIDs": []string{file.Items[0].ID}, "status": "reviewed"}, 400)
	call("/api/changes/state", map[string]any{"comparisonID": c.ID, "version": 0, "itemIDs": []string{file.Items[0].ID}, "status": "reviewed", "confirmed": true}, 200)
	call("/api/changes/state", map[string]any{"comparisonID": c.ID, "version": 0, "itemIDs": []string{file.Items[0].ID}, "status": "follow-up", "confirmed": true}, 409)
	if s.current() != report {
		t.Fatal("comparison mutated ordinary Application selection")
	}
	oldPreview := &changes.Resolved{ObjectDirectory: filepath.Join(t.TempDir(), "revision-input-unowned")}
	s.changePreview = oldPreview
	call("/api/changes/resolve", map[string]any{"directory": root, "before": before, "after": "HEAD"}, 500)
	if s.changePreview != oldPreview {
		t.Fatal("failed preview replacement silently installed unconfirmed inputs")
	}
}
