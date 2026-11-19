package review

import (
	"context"
	"testing"
)

func TestLambdaEntryDynamicConfiguredEventsRemainVisible(t *testing.T) {
	root := t.TempDir()
	writeReviewFixture(t, root, "go.mod", "module example.test/app\n\ngo 1.24\n")
	writeReviewFixture(t, root, "app.go", "package app\n")
	writeReviewFixture(t, root, "serverless.yml", `provider: aws
functions:
  app:
    handler: bin/app
    events:
      - ${file(./events.yml):event}
`)
	r, err := Analyze(context.Background(), Options{Dir: root})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range r.EntryPoints.Entries {
		if entry.Kind == "lambda" && entry.Trigger == "unknown" && entry.Resolution == "unresolved" &&
			entry.Reason != "" && entry.TriggerDetails != "" {
			return
		}
	}
	t.Fatalf("dynamic event should remain visible without guessing its type: %+v", r.EntryPoints)
}
