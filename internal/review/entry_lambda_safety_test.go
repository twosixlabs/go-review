package review

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLambdaEntrySymlinkConfigurationIsNotRead(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	writeReviewFixture(t, root, "go.mod", "module example.test/app\n\ngo 1.24\n")
	writeReviewFixture(t, root, "app.go", "package app\n")
	writeReviewFixture(t, outside, "serverless.yml", "provider: aws\nfunctions:\n  x:\n    handler: bin/x\n    events: [{sqs: queue}]\n")
	if err := os.Symlink(filepath.Join(outside, "serverless.yml"), filepath.Join(root, "serverless.yml")); err != nil {
		t.Fatal(err)
	}
	r, err := Analyze(context.Background(), Options{Dir: root})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range r.EntryPoints.Entries {
		if entry.Kind == "lambda" {
			t.Fatalf("followed excluded symlink: %+v", entry)
		}
	}
	for _, limitation := range r.EntryPoints.Limitations {
		if strings.Contains(limitation, "symlink") && strings.Contains(limitation, "serverless.yml") {
			return
		}
	}
	t.Fatalf("skipped configuration symlink not disclosed: %+v", r.EntryPoints.Limitations)
}
