package changes

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestChangeProviderFetchPinsMergeBaseWithoutHostWrites(t *testing.T) {
	root, base, head := revisionFixture(t)
	if err := os.Chmod(filepath.Join(root, "mode.sh"), 0755); err != nil {
		t.Fatal(err)
	}
	gitFixture(t, root, "checkout", "-qb", "target", base)
	fixtureFile(t, root, "target.txt", "target-only commit\n")
	gitFixture(t, root, "add", ".")
	gitFixture(t, root, "commit", "-qm", "target diverged")
	target := gitFixture(t, root, "rev-parse", "HEAD")
	status := gitFixture(t, root, "status", "--porcelain")
	host := "provider.example.test"
	remote := "https://" + host + "/team/project.git"
	gitFixture(t, root, "remote", "add", "origin", remote)
	tools := t.TempDir()
	log := filepath.Join(tools, "calls")
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	fixtureFile(t, tools, "git", "#!/bin/sh\nfor arg do\n if [ \"$arg\" = fetch ]; then\n  exec \"$CHANGE_REAL_GIT\" -c \"url.file://$CHANGE_GIT_ROOT/.insteadOf=$CHANGE_PROVIDER_REMOTE\" \"$@\"\n fi\ndone\nexec \"$CHANGE_REAL_GIT\" \"$@\"\n")
	fixtureFile(t, tools, "gh", "#!/bin/sh\nprintf '%s\\n' \"$@\" >> \"$CHANGE_PROVIDER_CALLS\"\nprintf '%s\\n' \"$CHANGE_GITHUB_METADATA\"\n")
	fixtureFile(t, tools, "glab", "#!/bin/sh\nprintf '%s\\n' \"$@\" >> \"$CHANGE_PROVIDER_CALLS\"\ncase \"$*\" in\n */merge_requests/*) printf '%s\\n' \"$CHANGE_GITLAB_METADATA\" ;;\n *) printf '%s\\n' \"$CHANGE_GITLAB_BRANCH\" ;;\nesac\n")
	for _, name := range []string{"gh", "glab", "git"} {
		if err := os.Chmod(filepath.Join(tools, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CHANGE_PROVIDER_CALLS", log)
	t.Setenv("CHANGE_REAL_GIT", realGit)
	t.Setenv("CHANGE_GIT_ROOT", filepath.ToSlash(root))
	t.Setenv("CHANGE_PROVIDER_REMOTE", remote)
	t.Setenv("CHANGE_GITHUB_METADATA", fmt.Sprintf(`{"baseRefOid":%q,"headRefOid":%q,"baseRefName":"target"}`, target, head))
	t.Setenv("CHANGE_GITLAB_METADATA", fmt.Sprintf(`{"sha":%q,"target_branch":"target","target_project_id":7}`, head))
	t.Setenv("CHANGE_GITLAB_BRANCH", fmt.Sprintf(`{"commit":{"id":%q}}`, target))
	// Rewrite the read-only fetch to our local fixture; no provider/network is contacted.
	for _, provider := range []string{
		"https://" + host + "/team/project/pull/42",
		"https://" + host + "/team/project/-/merge_requests/42",
	} {
		storage := t.TempDir()
		resolved, err := ResolveProvider(context.Background(), Request{Directory: root, Provider: provider}, storage)
		if err != nil {
			t.Fatal(err)
		}
		if resolved.Before.Commit != base || resolved.After.Commit != head || resolved.Target == nil || resolved.Target.Commit != target {
			t.Fatalf("provider changed actual comparison to target tip or synthetic merge: %+v", resolved)
		}
		if gitFixture(t, root, "status", "--porcelain") != status || gitFixture(t, root, "rev-parse", "HEAD") != target {
			t.Fatal("provider resolution modified checkout")
		}
		if err := ReleaseInputs(resolved); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(resolved.ObjectDirectory); !os.IsNotExist(err) {
			t.Fatalf("owned provider input not released: %v", err)
		}
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"approve", "comment", "merge\n", "status", "review\n"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("provider action leaked beyond revision reads: %s", data)
		}
	}
	if _, err := ResolveProvider(context.Background(), Request{Directory: root, Provider: "https://" + host + "/foreign/project/pull/42"}, t.TempDir()); err == nil {
		t.Fatal("accepted foreign provider repository")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ResolveProvider(ctx, Request{Directory: root, Provider: "https://" + host + "/team/project/pull/42"}, t.TempDir()); err == nil {
		t.Fatal("ignored provider resolution cancellation")
	}
}
