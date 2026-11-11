package review

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestScopeVerificationDefaultsAndSavedOptOut(t *testing.T) {
	root := t.TempDir()
	scope, err := LoadReviewScope(root)
	if err != nil {
		t.Fatal(err)
	}
	if !scope.VerifyRepository {
		t.Fatal("new review scope must include repository verification")
	}
	saveFixtureReviewScope(t, root, ReviewScope{VerifyRepository: false})
	saved, err := LoadReviewScope(root)
	if err != nil {
		t.Fatal(err)
	}
	if saved.VerifyRepository {
		t.Fatal("saved verification opt-out was lost")
	}
	name, err := scopeConfigPath(saved.Root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(`{"excludePaths":[],"excludeTests":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	legacy, err := LoadReviewScope(root)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.VerifyRepository {
		t.Fatal("older saved scope silently enabled verification")
	}
}

func TestRepositoryVerificationDefaultExecutionAndExclusions(t *testing.T) {
	for _, mode := range []string{"default", "opt-out", "excluded"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			writeReviewFixture(t, root, "go.mod", "module example.test/verification\n\ngo 1.24\n")
			writeReviewFixture(t, root, "source.go", "package verification\nfunc Value() int { return 1 }\n")
			writeReviewFixture(t, root, "scripts/verify-repository.sh", "#!/bin/sh\nprintf 'verification output\\n'\ntouch verification-ran\nexit 3\n")
			switch mode {
			case "opt-out":
				saveFixtureReviewScope(t, root, ReviewScope{VerifyRepository: false})
			case "excluded":
				saveFixtureReviewScope(t, root, ReviewScope{VerifyRepository: true, ExcludePaths: []string{"scripts/verify-repository.sh"}})
			}
			run, err := ReviewRepository(context.Background(), root, t.TempDir(), nil)
			if err != nil {
				t.Fatal(err)
			}
			_, markerErr := os.Stat(filepath.Join(root, "verification-ran"))
			for _, step := range run.Steps {
				if step.ID != "repository-hygiene" {
					continue
				}
				if mode == "default" {
					if markerErr != nil || step.Status != "fail" || len(step.Commands) != 1 ||
						step.Commands[0].ExitCode != 3 || step.Commands[0].Stdout != "verification output\n" {
						t.Fatalf("default verification lost execution or diagnostics: marker=%v step=%+v", markerErr, step)
					}
				} else if !os.IsNotExist(markerErr) || step.Status != "not-run" || len(step.Commands) != 0 {
					t.Fatalf("verification ignored %s: marker=%v step=%+v", mode, markerErr, step)
				}
				return
			}
			t.Fatal("repository verification stage missing")
		})
	}
}
