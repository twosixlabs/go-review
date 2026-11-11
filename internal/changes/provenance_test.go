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

func TestChangeProviderOriginNormalizationPreservesCustomPorts(t *testing.T) {
	for _, raw := range []string{"https://provider.test:8443/team/project.git", "ssh://git@provider.test:8443/team/project.git"} {
		if got := remoteIdentity(raw); got != "provider.test:8443/team/project" {
			t.Fatalf("custom-port origin lost identity: %q", got)
		}
	}
	p, err := parseProvider("https://PROVIDER.test:443/team/project/pull/42")
	if err != nil || p.Host != "provider.test" {
		t.Fatalf("default HTTPS port not normalized: %+v %v", p, err)
	}
}
