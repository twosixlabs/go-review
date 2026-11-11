package changes

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitFixture(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.test",
		"GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.test")
	data, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, data)
	}
	return strings.TrimSpace(string(data))
}

func fixtureFile(t *testing.T, root, name, text string) {
	t.Helper()
	file := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}

func revisionFixture(t *testing.T) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	gitFixture(t, root, "init", "-q")
	fixtureFile(t, root, "main.go", "package main\nfunc main() { helper() }\nfunc helper() {}\n")
	fixtureFile(t, root, "old name.txt", "move unchanged\n")
	fixtureFile(t, root, "removed.txt", "gone\n")
	fixtureFile(t, root, "mode.sh", "#!/bin/sh\nexit 0\n")
	fixtureFile(t, root, "binary", "a\x00b")
	gitFixture(t, root, "add", ".")
	gitFixture(t, root, "commit", "-qm", "before")
	before := gitFixture(t, root, "rev-parse", "HEAD")
	gitFixture(t, root, "mv", "old name.txt", "new\nname.txt")
	gitFixture(t, root, "rm", "-q", "removed.txt")
	fixtureFile(t, root, "main.go", "package main\nfunc main() { helper() }\nfunc helper() { panic(\"changed\") }\n")
	fixtureFile(t, root, "binary", "a\x00c")
	fixtureFile(t, root, "added.txt", "new\n")
	gitFixture(t, root, "update-index", "--chmod=+x", "mode.sh")
	gitFixture(t, root, "add", "main.go", "binary", "added.txt")
	gitFixture(t, root, "commit", "-qm", "after")
	after := gitFixture(t, root, "rev-parse", "HEAD")
	return root, before, after
}

func TestChangeInventoryAccountsForTextRenameBinaryAndMode(t *testing.T) {
	root, before, after := revisionFixture(t)
	request, err := Resolve(context.Background(), Request{Directory: root, Before: before, After: after})
	if err != nil {
		t.Fatal(err)
	}
	inventory, err := Inventory(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory) != 6 {
		t.Fatalf("lost tree changes: %+v", inventory)
	}
	found := map[string]FileChange{}
	for _, file := range inventory {
		found[file.After.Path] = file
		if file.ID == "" || len(file.Items) == 0 {
			t.Fatalf("unaccounted file: %+v", file)
		}
	}
	if file := found["new\nname.txt"]; file.Status != "renamed" || file.Before.Path != "old name.txt" {
		t.Fatalf("NUL-framed rename lost: %+v", file)
	}
	if file := found["binary"]; file.Content != "binary" || len(file.Items) != 1 {
		t.Fatalf("binary change lost: %+v", file)
	}
	if file := found["mode.sh"]; file.Before.Mode == file.After.Mode || len(file.Items) != 1 {
		t.Fatalf("mode-only change lost: %+v", file)
	}
	if file := found["main.go"]; len(file.Items) != 1 || file.Items[0].After.Start != 3 || file.Items[0].Before.Start != 3 {
		t.Fatalf("changed source range lost: %+v", file)
	}
}

func TestChangeResolutionPinsCommitsAndPreservesDirtyCheckout(t *testing.T) {
	root, before, after := revisionFixture(t)
	fixtureFile(t, root, "main.go", "dirty checkout\n")
	status := gitFixture(t, root, "status", "--porcelain")
	resolved, err := Resolve(context.Background(), Request{Directory: root, Before: before, After: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	gitFixture(t, root, "branch", "moving", before)
	if resolved.After.Commit != after || resolved.Before.Commit != before {
		t.Fatal("references were not pinned")
	}
	if _, err := Inventory(context.Background(), resolved); err != nil {
		t.Fatal(err)
	}
	if got := gitFixture(t, root, "status", "--porcelain"); got != status {
		t.Fatalf("inventory modified checkout: %q -> %q", status, got)
	}
	for _, ref := range []string{"missing-commit", "--help"} {
		if _, err := Resolve(context.Background(), Request{Directory: root, Before: ref, After: after}); err == nil {
			t.Fatalf("accepted invalid ref %q", ref)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Inventory(ctx, resolved); err == nil {
		t.Fatal("ignored cancellation")
	}
}

func TestChangeMaterializationDoesNotFollowSymlinksOrRunHooks(t *testing.T) {
	root, before, _ := revisionFixture(t)
	if err := os.Symlink("/outside/checkout", filepath.Join(root, "external")); err != nil {
		t.Fatal(err)
	}
	fixtureFile(t, root, "scripts/verify-repository.sh", "#!/bin/sh\ntouch should-not-run\n")
	fixtureFile(t, root, ".gitattributes", "retained.go export-ignore\nrevision.txt export-subst\n")
	fixtureFile(t, root, "retained.go", "package retained\n")
	fixtureFile(t, root, "revision.txt", "$Format:%H$\n")
	gitFixture(t, root, "add", ".")
	gitFixture(t, root, "commit", "-qm", "symlink")
	resolved, err := Resolve(context.Background(), Request{Directory: root, Before: before, After: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "after")
	limits, err := Materialize(context.Background(), resolved.Repository, resolved.After.Commit, target)
	if err != nil {
		t.Fatal(err)
	}
	if len(limits) == 0 {
		t.Fatal("omitted symlink was not disclosed")
	}
	if _, err := os.Lstat(filepath.Join(target, "external")); !os.IsNotExist(err) {
		t.Fatal("materialized a host-facing symlink")
	}
	if _, err := os.Stat(filepath.Join(target, "should-not-run")); !os.IsNotExist(err) {
		t.Fatal("materialization executed repository code")
	}
	content, err := os.ReadFile(filepath.Join(target, "scripts/verify-repository.sh"))
	if err != nil || !strings.Contains(string(content), "should-not-run") {
		t.Fatal("materialization lost regular source")
	}
	if _, err := os.Stat(filepath.Join(target, "retained.go")); err != nil {
		t.Fatal("export-ignore must not remove committed review input")
	}
	revision, err := os.ReadFile(filepath.Join(target, "revision.txt"))
	if err != nil || string(revision) != "$Format:%H$\n" {
		t.Fatal("export-subst altered committed input")
	}
}
