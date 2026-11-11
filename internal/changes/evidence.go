package changes

import (
	"encoding/json"
	"runtime"
	"strings"

	"github.com/russellbrasser/go-review/internal/review"
)

// Ordinary source hashes include the scope's materialized root. Comparison
// identity binds retained content and policy, not a random owned directory.
func retainedSideIdentity(side, commit, moduleID string, r *review.Report, scope review.ReviewScope, environment map[string]string, directory string) (string, error) {
	sources := map[string]string{}
	for _, file := range r.Files {
		sources[file.Path] = file.SHA256
	}
	for _, source := range r.TestSources {
		sources[source.File.Path] = source.File.SHA256
	}
	for _, doc := range r.Documents {
		sources[doc.Path] = doc.SHA256
	}
	for _, input := range r.Inputs {
		sources[input.Path] = input.SHA256
	}
	errors := map[string][]string{}
	for _, pkg := range r.Packages {
		if len(pkg.TypeErrors) > 0 {
			errors[pkg.Path] = pkg.TypeErrors
		}
	}
	data, err := json.Marshal(struct {
		Side, Commit, ModuleID, Module, Policy, Tool string
		Scope                                        review.ReviewScope
		Environment, ActualEnvironment, Sources      map[string]string
		Warnings, Limitations                        []string
		Errors                                       map[string][]string
	}{side, commit, moduleID, r.Module, Policy, runtime.Version(), scope, environment, r.Environment, sources, r.Warnings, r.Limitations, errors})
	if err != nil {
		return "", err
	}
	return identity(strings.ReplaceAll(string(data), directory, "<comparison>")), nil
}
