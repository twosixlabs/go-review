package review

import (
	"context"
	"testing"
)

func TestLambdaEntryDynamicSAMEventsRemainVisible(t *testing.T) {
	root := t.TempDir()
	writeReviewFixture(t, root, "go.mod", "module example.test/app\n\ngo 1.24\n")
	writeReviewFixture(t, root, "app.go", "package app\n")
	writeReviewFixture(t, root, "template.yaml", `Resources:
  Orders:
    Type: AWS::Serverless::Function
    Properties:
      CodeUri: .
      Handler: bootstrap
      Events: !If [UseEvents, {Request: {Type: HttpApi}}, {}]
`)
	r, err := Analyze(context.Background(), Options{Dir: root})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range r.EntryPoints.Entries {
		if entry.Kind == "lambda" && entry.Trigger == "unknown" && entry.Resolution == "unresolved" && entry.TriggerDetails != "" {
			return
		}
	}
	t.Fatalf("dynamic SAM events should remain visible without guessing: %+v", r.EntryPoints)
}
