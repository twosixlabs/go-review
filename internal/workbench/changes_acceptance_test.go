package workbench

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/russellbrasser/go-review/internal/changes"
	"github.com/russellbrasser/go-review/internal/review"
)

func TestChangeInteractiveAcceptance(t *testing.T) {
	root := t.TempDir()
	if retained := os.Getenv("GO_REVIEW_CHANGE_FIXTURE_DIR"); retained != "" {
		if !filepath.IsAbs(retained) {
			t.Fatal("browser fixture directory must be absolute")
		}
		if err := os.Mkdir(retained, 0700); err != nil {
			t.Fatalf("create new owned browser fixture (must not already exist): %v", err)
		}
		root = retained
	}
	git := func(args ...string) string {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", root}, args...)...)
		command.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.test", "GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.test")
		data, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %s", err, data)
		}
		return strings.TrimSpace(string(data))
	}
	write := func(name, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q")
	write("go.mod", "module example.test/interactive\n\ngo 1.24\n")
	write("app.go", "package interactive\nfunc First() int {return helper()}\nfunc Second() int {return helper()}\nfunc helper() int {return 1}\n")
	write("app_test.go", "package interactive\nimport(\"os\";\"testing\")\nfunc TestMustNotExecute(t *testing.T){os.WriteFile("+strconv.Quote(filepath.Join(root, "EXECUTED"))+",[]byte(\"executed\"),0600)}\n")
	git("add", ".")
	git("commit", "-qm", "before")
	before := git("rev-parse", "HEAD")
	write("app.go", "package interactive\nfunc First() int {return helper()}\nfunc Second() int {return helper()}\nfunc helper() int {return 2}\n")
	write("notes.md", "# Supporting change\n")
	git("add", ".")
	git("commit", "-qm", "after")
	dirty := git("status", "--porcelain")
	resolved, err := changes.Resolve(context.Background(), changes.Request{Directory: root, Before: before, After: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := changes.Collect(context.Background(), resolved, t.TempDir(), review.ReviewScope{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	report, err := c.Report(c.Modules[0].ID, "after")
	if err != nil {
		t.Fatal(err)
	}
	s := New(report)
	s.comparison = c
	flow := c.Flows[0]
	logical, err := s.queryChanges(context.Background(), changeQueryInput{ComparisonID: c.ID, Operation: "side", ModuleID: c.Modules[0].ID, Side: "after", SnapshotID: c.Modules[0].After.SnapshotID, View: "logical", SymbolID: flow.After.SymbolID})
	if err != nil || !strings.Contains(string(logical), "helper") {
		t.Fatalf("revision-specific logical relationships unavailable: %s %v", logical, err)
	}
	server := httptest.NewServer(s.Handler())
	defer server.Close()
	command := exec.CommandContext(t.Context(), "node", "--test", "../webui/changes_live_ui_test.js")
	command.Env = append(os.Environ(), "GO_REVIEW_CHANGE_URL="+server.URL)
	if data, err := command.CombinedOutput(); err != nil {
		t.Fatalf("interactive fixture: %v\n%s", err, data)
	}
	changeAcceptanceChat(t, s, c)
	if _, err := os.Stat(filepath.Join(root, "EXECUTED")); !os.IsNotExist(err) {
		t.Fatalf("target tests executed: %v", err)
	}
	if git("status", "--porcelain") != dirty {
		t.Fatal("collection/UI modified target worktree")
	}
	if s.current() != report {
		t.Fatal("comparison navigation replaced ordinary selected report")
	}
}
