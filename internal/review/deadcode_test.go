package review

import (
	"context"
	"strings"
	"testing"
)

func TestReachabilityIncludesTestRootsWithoutDuplicateVariantFalsePositives(t *testing.T) {
	root := t.TempDir()
	writeReviewFixture(t, root, "go.mod", "module example.com/roots\n\ngo 1.24\n")
	writeReviewFixture(t, root, "app.go", "package roots\n\nfunc Live() int { return 7 }\nfunc unused() int { return 9 }\n")
	writeReviewFixture(t, root, "app_test.go", "package roots\nimport \"testing\"\nfunc TestLive(t *testing.T) { if Live()!=7 { t.Fatal(\"bad\") } }\n")
	r, err := Analyze(context.Background(), Options{Dir: root})
	if err != nil {
		t.Fatal(err)
	}
	status, detail, err := reviewDeadcode(context.Background(), r)
	if err != nil || status != "fail" || !strings.Contains(detail, "unused") || strings.Contains(detail, "roots.Live") {
		t.Fatalf("incorrect reachability: %s %s %v", status, detail, err)
	}
}

func TestReachabilityWithoutRootsIsNotACleanBillOfHealth(t *testing.T) {
	root := t.TempDir()
	writeReviewFixture(t, root, "go.mod", "module example.com/library\n\ngo 1.24\n")
	writeReviewFixture(t, root, "app.go", "package library\n\nfunc Public() {}\n")
	r, err := Analyze(context.Background(), Options{Dir: root})
	if err != nil {
		t.Fatal(err)
	}
	status, _, err := reviewDeadcode(context.Background(), r)
	if err != nil || status != "not-run" {
		t.Fatalf("library without roots: %s %v", status, err)
	}
}
