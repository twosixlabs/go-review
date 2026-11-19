package review

import (
	"context"
	"path/filepath"
	"testing"
)

func TestLambdaEntryAWSStartVariants(t *testing.T) {
	root := t.TempDir()
	sdk, app := filepath.Join(root, "sdk"), filepath.Join(root, "app")
	writeReviewFixture(t, sdk, "go.mod", "module github.com/aws/aws-lambda-go\n\ngo 1.24\n")
	writeReviewFixture(t, sdk, "lambda/lambda.go", `package lambda
import "context"
func StartWithContext(context.Context, any) {}
func StartHandlerFunc[T any](func(T) error) {}
`)
	writeReviewFixture(t, app, "go.mod", "module example.test/app\n\ngo 1.24\nrequire github.com/aws/aws-lambda-go v0.0.0\nreplace github.com/aws/aws-lambda-go => ../sdk\n")
	writeReviewFixture(t, app, "main.go", `package main
import (
  "context"
  runtime "github.com/aws/aws-lambda-go/lambda"
)
func First() {}
func Second(int) error { return nil }
func main() {
  runtime.StartWithContext(context.Background(), First)
  runtime.StartHandlerFunc[int](Second)
}
`)
	r, err := Analyze(context.Background(), Options{Dir: app})
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, entry := range r.EntryPoints.Entries {
		for _, symbol := range r.Symbols {
			if entry.Kind == "lambda" && entry.Resolution == "resolved" && entry.SymbolID == symbol.ID {
				found[symbol.Name] = true
			}
		}
	}
	if !found["First"] || !found["Second"] {
		t.Fatalf("Start variants not resolved: entries=%+v calls=%+v", r.EntryPoints.Entries, r.OperationSources.Calls)
	}
}

func TestLambdaEntryUnrelatedStartIsNotLambda(t *testing.T) {
	root := t.TempDir()
	writeReviewFixture(t, root, "go.mod", "module example.test/app\n\ngo 1.24\n")
	writeReviewFixture(t, root, "runtime/runtime.go", "package runtime\nfunc Start(any) {}\n")
	writeReviewFixture(t, root, "main.go", `package main
import "example.test/app/runtime"
func Handle() {}
func main() { runtime.Start(Handle) }
`)
	r, err := Analyze(context.Background(), Options{Dir: root})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range r.EntryPoints.Entries {
		if entry.Kind == "lambda" {
			t.Fatalf("unrelated Start was classified as Lambda: %+v", entry)
		}
	}
}
