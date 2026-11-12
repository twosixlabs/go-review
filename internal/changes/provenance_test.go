package changes

import (
	"testing"

	"github.com/russellbrasser/go-review/internal/review"
)

func TestChangeSideIdentityIncludesActualAnalysisEnvironment(t *testing.T) {
	r := &review.Report{Module: "example.test/env", Environment: map[string]string{"GOVERSION": "go1.24.0", "GOMOD": "/owned/after/go.mod"}}
	first, err := retainedSideIdentity("after", "commit", "module", r, review.ReviewScope{}, map[string]string{"GOTOOLCHAIN": "local"}, "/owned")
	if err != nil {
		t.Fatal(err)
	}
	r.Environment["GOVERSION"] = "go1.25.0"
	second, err := retainedSideIdentity("after", "commit", "module", r, review.ReviewScope{}, map[string]string{"GOTOOLCHAIN": "local"}, "/owned")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("different actual analysis toolchains inherited the same evidence/progress identity")
	}
}
