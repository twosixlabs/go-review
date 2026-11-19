package review

import (
	"context"
	"strings"
	"testing"
)

func TestLambdaEntryConfigurationLimitIsReported(t *testing.T) {
	root := t.TempDir()
	writeReviewFixture(t, root, "go.mod", "module example.test/app\n\ngo 1.24\n")
	writeReviewFixture(t, root, "app.go", "package app\n")
	config := "provider: aws\nfunctions:\n  app:\n    handler: bin/app\n    events:\n" + strings.Repeat("      - sqs: example\n", 4097)
	writeReviewFixture(t, root, "serverless.yml", config)
	r, err := Analyze(context.Background(), Options{Dir: root})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.EntryPoints.Entries) > 4096 {
		t.Fatalf("configuration output is not bounded: %d entries", len(r.EntryPoints.Entries))
	}
	for _, limitation := range r.EntryPoints.Limitations {
		if strings.Contains(limitation, "trigger") && strings.Contains(limitation, "limit") {
			return
		}
	}
	t.Fatalf("truncation was not reported: %+v", r.EntryPoints.Limitations)
}
