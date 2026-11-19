package review

import (
	"context"
	"testing"
)

func TestLambdaEntryUnrelatedBuildDoesNotChangeInventory(t *testing.T) {
	root := t.TempDir()
	writeReviewFixture(t, root, "go.mod", "module example.test/app\n\ngo 1.24\n")
	writeReviewFixture(t, root, "main.go", "package main\nfunc main() {}\n")
	writeReviewFixture(t, root, "Makefile", "build:\n\tgo build -o bin/app .\n")
	r, err := Analyze(context.Background(), Options{Dir: root})
	if err != nil {
		t.Fatal(err)
	}
	if r.EntryPoints.Kind != "cli" || len(r.EntryPoints.Sources) != 0 {
		t.Fatalf("ordinary build leaked into Lambda inventory: %+v", r.EntryPoints)
	}
	for _, input := range r.Inputs {
		if input.Path == "Makefile" {
			t.Fatalf("ordinary build changed the entry evidence manifest: %+v", input)
		}
	}
}
