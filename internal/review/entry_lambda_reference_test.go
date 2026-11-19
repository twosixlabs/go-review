package review

import (
	"context"
	"testing"
)

func TestLambdaEntryCloudFormationAttributeDoesNotImplyARN(t *testing.T) {
	root := t.TempDir()
	writeReviewFixture(t, root, "go.mod", "module example.test/app\n\ngo 1.24\n")
	writeReviewFixture(t, root, "app.go", "package app\n")
	writeReviewFixture(t, root, "template.yaml", `Resources:
  Queue:
    Type: AWS::SQS::Queue
  Mapping:
    Type: AWS::Lambda::EventSourceMapping
    Properties:
      FunctionName: !Ref Orders
      EventSourceArn: !GetAtt Queue.QueueName
`)
	r, err := Analyze(context.Background(), Options{Dir: root})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range r.EntryPoints.Entries {
		if entry.Kind == "lambda" {
			if entry.Trigger != "EventSourceMapping" {
				t.Fatalf("non-ARN attribute was classified as a queue event source: %+v", entry)
			}
			return
		}
	}
	t.Fatal("event mapping missing")
}
