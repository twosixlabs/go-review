package review

import (
	"context"
	"path/filepath"
	"testing"
)

func TestLambdaEntrySameLineTriggersRemainDistinct(t *testing.T) {
	root := t.TempDir()
	writeReviewFixture(t, root, "go.mod", "module example.test/app\n\ngo 1.24\n")
	writeReviewFixture(t, root, "app.go", "package app\n")
	writeReviewFixture(t, root, "serverless.yml", `provider: aws
functions:
  app:
    handler: ${env:HANDLER}
    events: [{sqs: queue-one}, {sqs: queue-two}]
`)
	r, err := Analyze(context.Background(), Options{Dir: root})
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, entry := range r.EntryPoints.Entries {
		if entry.Trigger != "sqs" {
			continue
		}
		if ids[entry.ID] {
			t.Fatalf("distinct triggers have the same ID: %+v", r.EntryPoints.Entries)
		}
		ids[entry.ID] = true
		if entry.Resolution != "unresolved" || entry.Reason == "" {
			t.Fatalf("dynamic handler should remain unresolved: %+v", entry)
		}
	}
	if len(ids) != 2 {
		t.Fatalf("expected two distinct triggers: %+v", r.EntryPoints.Entries)
	}
}

func TestLambdaEntryConsistentBuildEvidence(t *testing.T) {
	root := t.TempDir()
	sdk, app := filepath.Join(root, "sdk"), filepath.Join(root, "app")
	writeReviewFixture(t, sdk, "go.mod", "module github.com/aws/aws-lambda-go\n\ngo 1.24\n")
	writeReviewFixture(t, sdk, "lambda/lambda.go", "package lambda\nfunc Start(any) {}\n")
	writeReviewFixture(t, app, "go.mod", "module example.test/app\n\ngo 1.24\nrequire github.com/aws/aws-lambda-go v0.0.0\nreplace github.com/aws/aws-lambda-go => ../sdk\n")
	writeReviewFixture(t, app, "cmd/orders/main.go", "package main\nimport \"github.com/aws/aws-lambda-go/lambda\"\nfunc Handle() {}\nfunc main(){lambda.Start(Handle)}\n")
	writeReviewFixture(t, app, "Makefile", "build:\n\tgo build -o bin/orders ./cmd/orders\n")
	writeReviewFixture(t, app, ".gitlab-ci.yml", "build:\n  script:\n    - go build -o bin/orders ./cmd/orders\n")
	writeReviewFixture(t, app, "serverless.yml", "provider: aws\nfunctions:\n  orders:\n    handler: bin/orders\n    events:\n      - sqs: example\n")
	r, err := Analyze(context.Background(), Options{Dir: app})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range r.EntryPoints.Entries {
		if entry.Trigger != "sqs" {
			continue
		}
		builds := 0
		for _, evidence := range entry.Evidence {
			if evidence.Kind == "lambda-build" {
				builds++
			}
		}
		if entry.Resolution != "resolved" || builds != 2 {
			t.Fatalf("consistent build evidence should link, not be ambiguous: %+v", entry)
		}
		return
	}
	t.Fatal("configured trigger missing")
}
