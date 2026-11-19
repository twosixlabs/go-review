package review

import (
	"context"
	"testing"
)

func TestLambdaEntrySharedConfigurationSourceIsRetainedOnce(t *testing.T) {
	root := t.TempDir()
	writeReviewFixture(t, root, "go.mod", "module example.test/app\n\ngo 1.24\n")
	writeReviewFixture(t, root, "app.go", "package app\n")
	writeReviewFixture(t, root, "template.yaml", `apiVersion: wasmcloud.com/v1alpha1
kind: Application
provider: aws
functions:
  app:
    handler: bin/app
    events: [{sqs: queue}]
`)
	r, err := Analyze(context.Background(), Options{Dir: root})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, source := range r.EntryPoints.Sources {
		if seen[source.ID] {
			t.Fatalf("shared configuration source was retained twice: %+v", r.EntryPoints.Sources)
		}
		seen[source.ID] = true
	}
	if len(seen) == 0 {
		t.Fatal("configuration source was not retained")
	}
}
