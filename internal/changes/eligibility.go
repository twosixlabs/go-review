package changes

import (
	"path/filepath"
	"strings"

	"github.com/russellbrasser/go-review/internal/review"
)

func contentEligibility(object Object, content string) string {
	switch {
	case object.Path == "":
		return "Absent on this revision"
	case object.Mode == "120000":
		return "Symlink target retained as a blob; never followed"
	case content != "text":
		return content + ": inspect object identities and available retained content"
	case !strings.HasSuffix(object.Path, ".go"):
		return "Supporting artifact; not a Go declaration"
	default:
		return "No analyzed Go module owns this source"
	}
}

func (c *Comparison) moduleEligibility(module Module, before, after *review.Report) {
	for i := range c.Files {
		file := &c.Files[i]
		for _, side := range []string{"before", "after"} {
			object, report, result, destination := file.Before, before, module.Before, &file.BeforeEligibility
			if side == "after" {
				object, report, result, destination = file.After, after, module.After, &file.AfterEligibility
			}
			rel, err := filepath.Rel(module.Path, object.Path)
			if object.Path == "" || err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || !strings.HasSuffix(object.Path, ".go") {
				continue
			}
			if report == nil {
				if !result.Absent {
					*destination = "Side analysis unavailable: " + result.Error
				}
				continue
			}
			*destination = "Not selected in the retained Go build or unavailable to analysis"
			for _, exclusion := range report.Exclusions {
				if filepath.ToSlash(exclusion.Path) == filepath.ToSlash(rel) {
					*destination = exclusion.Reason
					break
				}
			}
			for _, source := range report.Files {
				if filepath.ToSlash(source.Path) == filepath.ToSlash(rel) {
					*destination = "Analyzed Go source"
				}
			}
			for _, source := range report.TestSources {
				if filepath.ToSlash(source.File.Path) == filepath.ToSlash(rel) {
					*destination = "Retained test source; static evidence only, not executed"
				}
			}
		}
	}
}
