package review

import (
	"context"
	"testing"
)

func TestLambdaEntryCloudFormationResourcesRemainVisible(t *testing.T) {
	root := t.TempDir()
	writeReviewFixture(t, root, "go.mod", "module example.test/app\n\ngo 1.24\n")
	writeReviewFixture(t, root, "app.go", "package app\n")
	writeReviewFixture(t, root, "template.yaml", `Resources:
  Function:
    Type: AWS::Lambda::Function
    Properties:
      Code: {S3Bucket: artifacts, S3Key: function.zip}
      Handler: bootstrap
      Runtime: provided.al2023
  Queue:
    Type: AWS::SQS::Queue
  Mapping:
    Type: AWS::Lambda::EventSourceMapping
    Properties:
      FunctionName: !Ref Function
      EventSourceArn: !GetAtt Queue.Arn
  Subscription:
    Type: AWS::SNS::Subscription
    Properties:
      Protocol: lambda
      Endpoint: !GetAtt Function.Arn
      TopicArn: !Ref Topic
`)
	r, err := Analyze(context.Background(), Options{Dir: root})
	if err != nil {
		t.Fatal(err)
	}
	if r.EntryPoints.Kind != "lambda" {
		t.Fatalf("configuration-only Lambda was not classified: %+v", r.EntryPoints)
	}
	found := map[string]EntryPoint{}
	for _, entry := range r.EntryPoints.Entries {
		if entry.Kind == "lambda" && entry.Trigger != "" {
			found[entry.Trigger] = entry
		}
	}
	for _, kind := range []string{"SQS", "SNS"} {
		entry, ok := found[kind]
		if !ok || entry.Resolution != "unresolved" || entry.Reason == "" || entry.TriggerDetails == "" {
			t.Errorf("%s configuration must remain visible and unresolved: %+v", kind, r.EntryPoints.Entries)
		}
	}
}
