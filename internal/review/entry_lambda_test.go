package review

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestLambdaEntryDirectRegistration(t *testing.T) {
	root := t.TempDir()
	sdk := filepath.Join(root, "sdk")
	app := filepath.Join(root, "app")
	writeReviewFixture(t, sdk, "go.mod", "module github.com/aws/aws-lambda-go\n\ngo 1.24\n")
	writeReviewFixture(t, sdk, "lambda/lambda.go", "package lambda\nfunc Start(handler any) {}\nfunc StartWithOptions(handler any, options ...any) {}\n")
	writeReviewFixture(t, app, "go.mod", "module example.test/app\n\ngo 1.24\nrequire github.com/aws/aws-lambda-go v0.0.0\nreplace github.com/aws/aws-lambda-go => ../sdk\n")
	writeReviewFixture(t, app, "main.go", `package main
import runtime "github.com/aws/aws-lambda-go/lambda"
func Handle() error { return nil }
func main() { runtime.Start(Handle) }
`)
	r, err := Analyze(context.Background(), Options{Dir: app})
	if err != nil {
		t.Fatal(err)
	}
	var handler string
	for _, s := range r.Symbols {
		if s.Name == "Handle" {
			handler = s.ID
		}
	}
	for _, e := range r.EntryPoints.Entries {
		if e.Kind == "lambda" && e.SymbolID == handler && e.Resolution == "resolved" && len(e.Evidence) > 0 {
			return
		}
	}
	t.Fatalf("Lambda handler was not discovered: %+v", r.EntryPoints)
}

func TestLambdaEntryForwardingAndUnrelatedCalls(t *testing.T) {
	root := t.TempDir()
	sdk := filepath.Join(root, "sdk")
	app := filepath.Join(root, "app")
	writeReviewFixture(t, sdk, "go.mod", "module github.com/aws/aws-lambda-go\n\ngo 1.24\n")
	writeReviewFixture(t, sdk, "lambda/lambda.go", "package lambda\nfunc Start(handler any) {}\nfunc StartWithOptions(handler any, options ...any) {}\n")
	writeReviewFixture(t, app, "go.mod", "module example.test/app\n\ngo 1.24\nrequire github.com/aws/aws-lambda-go v0.0.0\nreplace github.com/aws/aws-lambda-go => ../sdk\n")
	writeReviewFixture(t, app, "main.go", `package main
import "github.com/aws/aws-lambda-go/lambda"
func Handle() error { return nil }
func register(handler any) { lambda.StartWithOptions(handler) }
func unused() { lambda.Start(Handle) }
func main() { handler := Handle; register(handler) }
`)
	r, err := Analyze(context.Background(), Options{Dir: app})
	if err != nil {
		t.Fatal(err)
	}
	var handler string
	for _, s := range r.Symbols {
		if s.Name == "Handle" {
			handler = s.ID
		}
	}
	if len(r.EntryPoints.Entries) != 1 || r.EntryPoints.Entries[0].SymbolID != handler ||
		r.EntryPoints.Entries[0].Resolution != "resolved" {
		t.Fatalf("expected forwarded handler only, got %+v", r.EntryPoints.Entries)
	}
	for _, evidence := range r.EntryPoints.Entries[0].Evidence {
		if evidence.Kind == "lambda-binding" {
			return
		}
	}
	t.Fatalf("forwarded local handler binding lacks provenance: %+v", r.EntryPoints.Entries[0].Evidence)
}

func TestLambdaEntryServerlessTriggersAndBuild(t *testing.T) {
	root := t.TempDir()
	sdk := filepath.Join(root, "sdk")
	app := filepath.Join(root, "app")
	writeReviewFixture(t, sdk, "go.mod", "module github.com/aws/aws-lambda-go\n\ngo 1.24\n")
	writeReviewFixture(t, sdk, "lambda/lambda.go", "package lambda\nfunc Start(handler any) {}\n")
	writeReviewFixture(t, app, "go.mod", "module example.test/app\n\ngo 1.24\nrequire github.com/aws/aws-lambda-go v0.0.0\nreplace github.com/aws/aws-lambda-go => ../sdk\n")
	writeReviewFixture(t, app, "cmd/orders/main.go", `package main
import "github.com/aws/aws-lambda-go/lambda"
func Handle() error { return nil }
func main() { lambda.Start(Handle) }
`)
	writeReviewFixture(t, app, "Makefile", "build:\n\tgo build -o bin/orders ./cmd/orders\n")
	writeReviewFixture(t, app, "serverless.yml", `service: orders
provider:
  name: aws
functions:
  orders:
    handler: bin/orders
    events:
      - httpApi:
          path: /orders
          method: post
      - sqs: arn:aws:sqs:us-east-1:123:orders
`)
	r, err := Analyze(context.Background(), Options{Dir: app})
	if err != nil {
		t.Fatal(err)
	}

	found := map[string]bool{}
	for _, e := range r.EntryPoints.Entries {
		if e.Kind == "lambda" && e.Resolution == "resolved" && e.SymbolID != "" {
			for _, evidence := range e.Evidence {
				if evidence.Kind == "lambda-trigger" && evidence.SourceID != "" && evidence.Line > 0 {
					if evidence.Detail == "httpApi" {
						raw, err := json.Marshal(e)
						if err != nil {
							t.Fatal(err)
						}
						if !strings.Contains(string(raw), "/orders") || !strings.Contains(string(raw), "post") {
							t.Fatalf("HTTP trigger details were not retained: %+v", e)
						}
					}
					found[evidence.Detail] = true
				}
			}
		}
	}
	if !found["httpApi"] || !found["sqs"] {
		t.Fatalf("expected two linked trigger entries, got %s", fmt.Sprint(r.EntryPoints))
	}
	for _, source := range r.EntryPoints.Sources {
		if strings.HasSuffix(source.Path, "serverless.yml") {
			return
		}
	}
	t.Fatal("deployment source was not retained")
}

func TestLambdaEntrySAMUnresolvedTrigger(t *testing.T) {
	root := t.TempDir()
	writeReviewFixture(t, root, "go.mod", "module example.test/app\n\ngo 1.24\n")
	writeReviewFixture(t, root, "app.go", "package app\n")
	writeReviewFixture(t, root, "template.yaml", `Transform: AWS::Serverless-2016-10-31
Resources:
  Orders:
    Type: AWS::Serverless::Function
    Properties:
      CodeUri: !Sub "${CodePath}"
      Handler: bootstrap
      Events:
        Receive:
          Type: SQS
          Properties:
            Queue: !GetAtt Queue.Arn
`)
	r, err := Analyze(context.Background(), Options{Dir: root})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range r.EntryPoints.Entries {
		for _, evidence := range entry.Evidence {
			if entry.Kind == "lambda" && entry.Resolution == "unresolved" && entry.Reason != "" &&
				evidence.Kind == "lambda-trigger" && evidence.Detail == "SQS" && evidence.Line > 0 {
				return
			}
		}
	}
	t.Fatalf("configuration-only trigger should remain visible and unresolved: %+v", r.EntryPoints)
}

func TestLambdaEntryInterfaceForwarding(t *testing.T) {
	root := t.TempDir()
	sdk := filepath.Join(root, "sdk")
	app := filepath.Join(root, "app")
	writeReviewFixture(t, sdk, "go.mod", "module github.com/aws/aws-lambda-go\n\ngo 1.24\n")
	writeReviewFixture(t, sdk, "lambda/lambda.go", "package lambda\nfunc Start(handler any) {}\n")
	writeReviewFixture(t, app, "go.mod", "module example.test/app\n\ngo 1.24\nrequire github.com/aws/aws-lambda-go v0.0.0\nreplace github.com/aws/aws-lambda-go => ../sdk\n")
	writeReviewFixture(t, app, "main.go", `package main
import "github.com/aws/aws-lambda-go/lambda"
type Provider interface { Run(any) }
type implementation struct{}
func (implementation) Run(h any) { lambda.Start(h) }
func run(p Provider, h any) { p.Run(h) }
func Handle() error { return nil }
func main() { run(implementation{}, Handle) }
`)
	r, err := Analyze(context.Background(), Options{Dir: app})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range r.EntryPoints.Entries {
		for _, symbol := range r.Symbols {
			if symbol.Name == "Handle" && entry.Kind == "lambda" && entry.SymbolID == symbol.ID && entry.Resolution == "resolved" {
				return
			}
		}
	}
	t.Fatalf("interface forwarding not resolved: entries=%+v operations=%+v", r.EntryPoints.Entries, r.OperationSources.Calls)
}

func TestLambdaEntrySAMAndCloudFormationEvents(t *testing.T) {
	root := t.TempDir()
	sdk := filepath.Join(root, "sdk")
	app := filepath.Join(root, "app")
	writeReviewFixture(t, sdk, "go.mod", "module github.com/aws/aws-lambda-go\n\ngo 1.24\n")
	writeReviewFixture(t, sdk, "lambda/lambda.go", "package lambda\nfunc Start(handler any) {}\n")
	writeReviewFixture(t, app, "go.mod", "module example.test/app\n\ngo 1.24\nrequire github.com/aws/aws-lambda-go v0.0.0\nreplace github.com/aws/aws-lambda-go => ../sdk\n")
	writeReviewFixture(t, app, "cmd/orders/main.go", "package main\nimport \"github.com/aws/aws-lambda-go/lambda\"\nfunc Handle() {}\nfunc main(){lambda.Start(Handle)}\n")
	writeReviewFixture(t, app, ".gitlab-ci.yml", "build:\n  script:\n    - go build -o bin/orders ./cmd/orders\n")
	writeReviewFixture(t, app, "template.yaml", `Transform: AWS::Serverless-2016-10-31
Resources:
  Orders:
    Type: AWS::Serverless::Function
    Properties:
      CodeUri: .
      Handler: bin/orders
      Events:
        Request:
          Type: HttpApi
          Properties: {Path: /orders, Method: POST}
  Subscription:
    Type: AWS::Lambda::EventSourceMapping
    Properties:
      FunctionName: !Ref Orders
      EventSourceArn: !GetAtt Queue.Arn
`)
	r, err := Analyze(context.Background(), Options{Dir: app})
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]EntryPoint{}
	for _, entry := range r.EntryPoints.Entries {
		if entry.Kind == "lambda" && entry.Trigger != "" {
			found[entry.Trigger] = entry
		}
	}
	for _, kind := range []string{"HttpApi", "EventSourceMapping"} {
		if entry, ok := found[kind]; !ok || entry.Resolution != "resolved" || entry.SymbolID == "" {
			t.Errorf("%s trigger was not linked: %+v", kind, r.EntryPoints.Entries)
		}

	}
}

func TestLambdaEntryBuildCommentsAreNotEvidence(t *testing.T) {
	root := t.TempDir()
	sdk := filepath.Join(root, "sdk")
	app := filepath.Join(root, "app")
	writeReviewFixture(t, sdk, "go.mod", "module github.com/aws/aws-lambda-go\n\ngo 1.24\n")
	writeReviewFixture(t, sdk, "lambda/lambda.go", "package lambda\nfunc Start(handler any) {}\n")
	writeReviewFixture(t, app, "go.mod", "module example.test/app\n\ngo 1.24\nrequire github.com/aws/aws-lambda-go v0.0.0\nreplace github.com/aws/aws-lambda-go => ../sdk\n")
	writeReviewFixture(t, app, "cmd/orders/main.go", "package main\nimport \"github.com/aws/aws-lambda-go/lambda\"\nfunc Handle() {}\nfunc main(){lambda.Start(Handle)}\n")
	writeReviewFixture(t, app, "Makefile", "# go build -o bin/orders ./cmd/orders\n")
	writeReviewFixture(t, app, "serverless.yml", "provider: aws\nfunctions:\n  orders:\n    handler: bin/orders\n    events:\n      - sqs: example\n")
	r, err := Analyze(context.Background(), Options{Dir: app})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range r.EntryPoints.Entries {
		if entry.Trigger == "sqs" {
			if entry.Resolution != "unresolved" || entry.SymbolID != "" {
				t.Fatalf("comment incorrectly treated as build evidence: %+v", entry)
			}
			return
		}
	}
	t.Fatal("configured trigger missing")
}

func TestLambdaEntryDistinctForwardedHandlers(t *testing.T) {
	root := t.TempDir()
	sdk := filepath.Join(root, "sdk")
	app := filepath.Join(root, "app")
	writeReviewFixture(t, sdk, "go.mod", "module github.com/aws/aws-lambda-go\n\ngo 1.24\n")
	writeReviewFixture(t, sdk, "lambda/lambda.go", "package lambda\nfunc Start(handler any) {}\n")
	writeReviewFixture(t, app, "go.mod", "module example.test/app\n\ngo 1.24\nrequire github.com/aws/aws-lambda-go v0.0.0\nreplace github.com/aws/aws-lambda-go => ../sdk\n")
	writeReviewFixture(t, app, "main.go", `package main
import "github.com/aws/aws-lambda-go/lambda"
func A() {}
func B() {}
func run(h any) { lambda.Start(h) }
func main() { if condition() { run(A) } else { run(B) } }
func condition() bool { return true }
`)
	r, err := Analyze(context.Background(), Options{Dir: app})
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	symbols := map[string]bool{}
	for _, entry := range r.EntryPoints.Entries {
		if entry.Kind == "lambda" {
			if ids[entry.ID] {
				t.Fatalf("distinct registration paths share ID %q", entry.ID)
			}
			ids[entry.ID] = true
			symbols[entry.SymbolID] = true
		}
	}
	if len(ids) != 2 || len(symbols) != 2 {
		t.Fatalf("expected both possible handlers: %+v", r.EntryPoints.Entries)
	}
}

func TestLambdaEntryCIChangedDirectoryIsUnresolved(t *testing.T) {
	root := t.TempDir()
	sdk := filepath.Join(root, "sdk")
	app := filepath.Join(root, "app")
	writeReviewFixture(t, sdk, "go.mod", "module github.com/aws/aws-lambda-go\n\ngo 1.24\n")
	writeReviewFixture(t, sdk, "lambda/lambda.go", "package lambda\nfunc Start(handler any) {}\n")
	writeReviewFixture(t, app, "go.mod", "module example.test/app\n\ngo 1.24\nrequire github.com/aws/aws-lambda-go v0.0.0\nreplace github.com/aws/aws-lambda-go => ../sdk\n")
	writeReviewFixture(t, app, "cmd/orders/main.go", "package main\nimport \"github.com/aws/aws-lambda-go/lambda\"\nfunc Handle() {}\nfunc main(){lambda.Start(Handle)}\n")
	writeReviewFixture(t, app, ".gitlab-ci.yml", "build:\n  script:\n    - cd other\n    - go build -o bin/orders ./cmd/orders\n")
	writeReviewFixture(t, app, "serverless.yml", "provider: aws\nfunctions:\n  orders:\n    handler: bin/orders\n    events:\n      - sqs: example\n")
	r, err := Analyze(context.Background(), Options{Dir: app})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range r.EntryPoints.Entries {
		if entry.Trigger == "sqs" {
			if entry.Resolution != "unresolved" {
				t.Fatalf("working directory was guessed: %+v", entry)
			}
			return
		}
	}
	t.Fatal("configured trigger missing")
}

func TestLambdaEntryHandlerForms(t *testing.T) {
	tests := []struct {
		name, declarations, registration, handler string
	}{
		{"method", "type service struct{}\nfunc (service) Handle() {}", "lambda.Start(service{}.Handle)", "Handle"},
		{"closure", "", "lambda.Start(func(){})", ""},
		{"handler-interface", "type service struct{}\nfunc (service) Invoke(context.Context, []byte) ([]byte, error) { return nil, nil }", "lambda.StartHandler(service{})", "Invoke"},
		{"sdk-handler-wrapper", "func Handle() {}", "lambda.StartHandler(lambda.NewHandler(Handle))", "Handle"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			sdk, app := filepath.Join(root, "sdk"), filepath.Join(root, "app")
			writeReviewFixture(t, sdk, "go.mod", "module github.com/aws/aws-lambda-go\n\ngo 1.24\n")
			writeReviewFixture(t, sdk, "lambda/lambda.go", "package lambda\nimport \"context\"\ntype Handler interface { Invoke(context.Context, []byte) ([]byte, error) }\nfunc Start(any) {}\nfunc StartHandler(Handler) {}\nfunc NewHandler(any) Handler { return nil }\n")
			writeReviewFixture(t, app, "go.mod", "module example.test/app\n\ngo 1.24\nrequire github.com/aws/aws-lambda-go v0.0.0\nreplace github.com/aws/aws-lambda-go => ../sdk\n")
			imports := "import \"github.com/aws/aws-lambda-go/lambda\"\n"
			if tt.name == "handler-interface" {
				imports += "import \"context\"\n"
			}
			writeReviewFixture(t, app, "main.go", "package main\n"+imports+tt.declarations+"\nfunc main(){"+tt.registration+"}\n")
			r, err := Analyze(context.Background(), Options{Dir: app})
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range r.EntryPoints.Entries {
				if entry.Kind != "lambda" || entry.Resolution != "resolved" || entry.SymbolID == "" {
					continue
				}
				for _, symbol := range r.Symbols {
					if symbol.ID == entry.SymbolID && (tt.handler == "" && symbol.Kind == "closure" || symbol.Name == tt.handler) {
						return
					}
				}
			}
			t.Fatalf("handler form not resolved: %+v callables=%+v calls=%+v", r.EntryPoints.Entries, r.OperationSources.Callables, r.OperationSources.Calls)
		})
	}
}
