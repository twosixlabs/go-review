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

func TestChangeResolveFetchesForkAndPinsMergeBaseWithoutChangingCheckout(t *testing.T) {
	root, fork := t.TempDir(), t.TempDir()
	run := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.test", "GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.test")
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	run(root, "init", "-q")
	if err := os.WriteFile(filepath.Join(root, "common.txt"), []byte("base\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run(root, "add", ".")
	run(root, "commit", "-qm", "base")
	base := run(root, "rev-parse", "HEAD")
	run(fork, "clone", "-q", root, ".")
	run(fork, "checkout", "-qb", "feature")
	if err := os.WriteFile(filepath.Join(fork, "feature.txt"), []byte("source\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run(fork, "add", ".")
	run(fork, "commit", "-qm", "feature")
	head := run(fork, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(root, "target.txt"), []byte("target\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run(root, "add", ".")
	run(root, "commit", "-qm", "target")
	target := run(root, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(root, "dirty.txt"), []byte("untracked\n"), 0600); err != nil {
		t.Fatal(err)
	}
	status := run(root, "status", "--porcelain")
	run(root, "remote", "add", "fork", fork)
	relativeFork, err := filepath.Rel(root, fork)
	if err != nil {
		t.Fatal(err)
	}
	run(root, "remote", "add", "relative-fork", relativeFork)
	s := New(nil)
	t.Cleanup(func() {
		if err := s.ShutdownChanges(context.Background()); err != nil {
			t.Error(err)
		}
	})
	resolve := func(remote, revision string, mergeBase bool, want int) changes.Resolved {
		t.Helper()
		body, err := json.Marshal(map[string]any{"directory": root, "before": "HEAD", "after": revision, "remote": remote, "mergeBase": mergeBase})
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest("POST", "http://127.0.0.1:8090/api/changes/resolve", strings.NewReader(string(body)))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		s.Handler().ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("resolve %q %q: %d %s", remote, revision, response.Code, response.Body.String())
		}
		if strings.Contains(remote, "secret@") || strings.Contains(remote, "user:secret@") {
			if !strings.Contains(response.Body.String(), "do not embed credentials") || strings.Contains(response.Body.String(), "secret") {
				t.Fatalf("credential was not rejected safely: %s", response.Body.String())
			}
		}
		var result changes.Resolved
		if want == 200 {
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
		}
		return result
	}
	for _, input := range []struct{ remote, revision string }{{fork, "feature"}, {"fork", "feature"}, {"fork", head}, {"relative-fork", "feature"}} {
		result := resolve(input.remote, input.revision, true, 200)
		if result.Before.Commit != base || result.After.Commit != head || result.Target == nil || result.Target.Commit != target {
			t.Fatalf("incorrect pinned revisions: %+v", result)
		}
	}
	statusResponse := httptest.NewRecorder()
	s.Handler().ServeHTTP(statusResponse, httptest.NewRequest("GET", "http://127.0.0.1:8090/api/changes", nil))
	var statusBody struct {
		Preview changes.Resolved `json:"preview"`
	}
	if err := json.Unmarshal(statusResponse.Body.Bytes(), &statusBody); err != nil {
		t.Fatal(err)
	}
	if statusResponse.Code != 200 || statusBody.Preview.Target == nil || statusBody.Preview.Target.Commit != target {
		t.Fatalf("reopened preview lost target: %d %s", statusResponse.Code, statusResponse.Body.String())
	}
	result := resolve("fork", "feature", false, 200)
	if result.Before.Commit != target || result.After.Commit != head || result.Target != nil {
		t.Fatalf("direct comparison unexpectedly changed revisions: %+v", result)
	}
	preview := s.changePreview
	comparison, err := changes.Collect(context.Background(), *preview, t.TempDir(), review.ReviewScope{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, file := range comparison.Files {
		if file.After.Path == "feature.txt" && file.Status == "added" {
			found = true
		}
	}
	if !found {
		t.Fatalf("fetched fork content missing from comparison: %+v", comparison.Files)
	}
	resolve("fork", "missing-ref", true, 422)
	resolve("https://user:secret@example.invalid/repository", "feature", true, 422)
	resolve("https://secret@example.invalid/repository", "feature", true, 422)
	if s.changePreview != preview {
		t.Fatal("failed resolution replaced the last usable preview")
	}
	if run(root, "status", "--porcelain") != status || run(root, "rev-parse", "HEAD") != target {
		t.Fatal("resolving remote revisions changed the checkout")
	}
}
