package webui

import (
	"regexp"
	"strings"
	"testing"
)

func TestEmbeddedInterfaceAssetsAndControls(t *testing.T) {
	data, err := assets.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(data)
	ids := make(map[string]bool)
	for _, match := range regexp.MustCompile(`\bid="([^"]+)"`).FindAllStringSubmatch(html, -1) {
		if ids[match[1]] {
			t.Errorf("duplicate control ID %q", match[1])
		}
		ids[match[1]] = true
	}
	for _, id := range []string{"application", "application-overview", "architecture-signals", "hygiene", "signal-summary", "review-map", "logical-flow", "function-flow", "data-flow", "call-map", "run-tests", "import-coverage", "show-comments", "show-unreachable", "compact-statements", "coverage-summary"} {
		if !ids[id] {
			t.Errorf("missing evidence control %q", id)
		}
	}
	if strings.Count(html, "<nav") != strings.Count(html, "</nav>") {
		t.Error("unbalanced navigation markup")
	}
	for _, match := range regexp.MustCompile(`(?:src|href)="/([^"]+)"`).FindAllStringSubmatch(html, -1) {
		if _, err := assets.ReadFile(match[1]); err != nil {
			t.Errorf("interface references missing embedded asset: %v", err)
		}
	}
}
