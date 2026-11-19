package review

import (
	"context"
	"path/filepath"
	"testing"
)

func TestLambdaEntryLocalHandlerBinding(t *testing.T) {
	root := t.TempDir()
	sdk, app := filepath.Join(root, "sdk"), filepath.Join(root, "app")
	writeReviewFixture(t, sdk, "go.mod", "module github.com/aws/aws-lambda-go\n\ngo 1.24\n")
	writeReviewFixture(t, sdk, "lambda/lambda.go", "package lambda\nfunc Start(any) {}\n")
	writeReviewFixture(t, app, "go.mod", "module example.test/app\n\ngo 1.24\nrequire github.com/aws/aws-lambda-go v0.0.0\nreplace github.com/aws/aws-lambda-go => ../sdk\n")
	writeReviewFixture(t, app, "main.go", `package main
import "github.com/aws/aws-lambda-go/lambda"
func Handle() {}
func main() { handler := Handle; lambda.Start(handler) }
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
	t.Fatalf("local handler binding was not resolved: entries=%+v bindings=%+v", r.EntryPoints.Entries, r.OperationSources.Bindings)
}

func TestLambdaEntryAmbiguousHandlerBinding(t *testing.T) {
	root := t.TempDir()
	sdk, app := filepath.Join(root, "sdk"), filepath.Join(root, "app")
	writeReviewFixture(t, sdk, "go.mod", "module github.com/aws/aws-lambda-go\n\ngo 1.24\n")
	writeReviewFixture(t, sdk, "lambda/lambda.go", "package lambda\nfunc Start(any) {}\n")
	writeReviewFixture(t, app, "go.mod", "module example.test/app\n\ngo 1.24\nrequire github.com/aws/aws-lambda-go v0.0.0\nreplace github.com/aws/aws-lambda-go => ../sdk\n")
	writeReviewFixture(t, app, "main.go", `package main
import "github.com/aws/aws-lambda-go/lambda"
func A() {}
func B() {}
func main() { handler := A; if condition() { handler = B }; lambda.Start(handler) }
func condition() bool { return true }
`)
	r, err := Analyze(context.Background(), Options{Dir: app})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range r.EntryPoints.Entries {
		if entry.Kind == "lambda" {
			if entry.Resolution != "unresolved" || entry.SymbolID != "" {
				t.Fatalf("ambiguous handler was guessed: %+v bindings=%+v", entry, r.OperationSources.Bindings)
			}
			return
		}
	}
	t.Fatal("Lambda registration missing")
}
