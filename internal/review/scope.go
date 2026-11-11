package review

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// ReviewScope is rooted at the checkout, not a module. Exclusions affect direct
// review commands; the repository verification script remains authoritative.
type ReviewScope struct {
	Root             string              `json:"root"`
	ExcludePaths     []string            `json:"excludePaths"`
	ExcludeTests     []string            `json:"excludeTests"`
	UnitSelections   []UnitTestSelection `json:"unitSelections,omitempty"`
	VerifyRepository bool                `json:"verifyRepository"`
}

type ScopeExclusion struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

func scopeConfigPath(root string) (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate review scope storage: %w", err)
	}
	return filepath.Join(dir, "go-review", "scopes", digest([]byte(root))+".json"), nil
}

func LoadReviewScope(dir string) (ReviewScope, error) {
	root, err := resolveReviewRoot(dir)
	if err != nil {
		return ReviewScope{}, err
	}
	s := ReviewScope{Root: root, ExcludePaths: []string{}, ExcludeTests: []string{}}
	name, err := scopeConfigPath(root)
	if err != nil {
		return s, err
	}
	data, err := os.ReadFile(name)
	if os.IsNotExist(err) {
		s.VerifyRepository = true
		return s, nil
	}
	if err != nil {
		return s, fmt.Errorf("read saved review scope: %w", err)
	}
	if err = json.Unmarshal(data, &s); err != nil {
		return s, fmt.Errorf("parse saved review scope: %w", err)
	}
	s.Root = root
	return s, s.validate()
}

func SaveReviewScope(dir string, s ReviewScope) error {
	root, err := resolveReviewRoot(dir)
	if err != nil {
		return err
	}
	s.Root = root
	if err = s.validate(); err != nil {
		return err
	}
	name, err := scopeConfigPath(root)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(name), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(name), ".scope-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), name); err != nil {
		return fmt.Errorf("save review scope: %w", err)
	}
	return nil
}

func (s ReviewScope) validate() error {
	for _, selection := range s.UnitSelections {
		if selection.Package == "." || strings.HasPrefix(selection.Package, "./") {
			return fmt.Errorf("saved unit selections require a full import path: %q", selection.Package)
		}
		if err := validateUnitSelectionSyntax(selection); err != nil {
			return err
		}
	}
	for _, p := range s.ExcludePaths {
		if p == "" || p == "." || strings.HasPrefix(p, "/") || strings.Contains(p, `\`) {
			return fmt.Errorf("scope path must be repository-relative: %q", p)
		}
		for _, part := range strings.Split(p, "/") {
			if part == ".." {
				return fmt.Errorf("scope path cannot leave repository: %q", p)
			}
		}
		if _, err := path.Match(p, ""); err != nil {
			return fmt.Errorf("invalid scope path %q: %w", p, err)
		}
	}
	for _, p := range s.ExcludeTests {
		if p == "" {
			return fmt.Errorf("test exclusion must not be empty")
		}
		if strings.Contains(p, "/") {
			return fmt.Errorf("test exclusions select top-level names; exclude the parent test or test file instead of subtests: %q", p)
		}
		if _, err := regexp.Compile(p); err != nil {
			return fmt.Errorf("invalid test exclusion %q: %w", p, err)
		}
	}
	return nil
}

// ** matches any number of path segments; a matching directory excludes its
// descendants. Other segments use Go path.Match syntax.
func scopeGlob(pattern, name string) bool {
	p, n := strings.Split(strings.TrimSuffix(pattern, "/"), "/"), strings.Split(name, "/")
	var match func(int, int) bool
	match = func(i, j int) bool {
		if i == len(p) {
			return true
		}
		if p[i] == "**" {
			return match(i+1, j) || (j < len(n) && match(i, j+1))
		}
		if j == len(n) {
			return false
		}
		ok, _ := path.Match(p[i], n[j])
		return ok && match(i+1, j+1)
	}
	return match(0, 0)
}

func (s ReviewScope) pathReason(name string) string {
	for _, p := range s.ExcludePaths {
		if scopeGlob(p, name) {
			return "Excluded path: " + p
		}
	}
	return ""
}

func (s ReviewScope) relative(module, name string) string {
	rel, err := filepath.Rel(s.Root, filepath.Join(module, filepath.FromSlash(name)))
	if err != nil {
		return name
	}
	return filepath.ToSlash(rel)
}

func generatedSource(source string) bool {
	tree, _ := parser.ParseFile(token.NewFileSet(), "source.go", source, parser.ParseComments)
	if tree != nil && ast.IsGenerated(tree) {
		return true
	}
	// wit-bindgen does not use Go's exact "Code generated ... DO NOT EDIT."
	// convention. Restrict its variant to the leading generated header.
	header, _, _ := strings.Cut(source, "\npackage ")
	return strings.HasPrefix(header, "// Generated by `wit-bindgen`") && strings.Contains(header, "DO NOT EDIT!")
}

func (s ReviewScope) sourceReason(module, name, source string) string {
	if reason := s.pathReason(s.relative(module, name)); reason != "" {
		return reason
	}
	if generatedSource(source) {
		return "Generated source"
	}
	return ""
}

// Vet still type-checks retained test helpers. Suppress only diagnostics located
// inside an explicitly excluded entry point, not unrelated findings in its file.
func (s ReviewScope) sourcePositionReason(module, name, source string, line int) string {
	if reason := s.sourceReason(module, name, source); reason != "" {
		return reason
	}
	if !strings.HasSuffix(name, "_test.go") || len(s.ExcludeTests) == 0 {
		return ""
	}
	fs := token.NewFileSet()
	tree, err := parser.ParseFile(fs, name, source, 0)
	if err != nil {
		return ""
	}
	for _, decl := range tree.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && testEntry(fn.Name.Name) && fs.Position(fn.Pos()).Line <= line && fs.Position(fn.End()).Line >= line {
			return s.testReason(fn.Name.Name)
		}
	}
	return ""
}

func resolveScope(opts Options) (ReviewScope, error) {
	if opts.Scope != nil {
		s := *opts.Scope
		root, err := filepath.Abs(s.Root)
		if err != nil {
			return s, err
		}
		root, err = filepath.EvalSymlinks(root)
		if err != nil {
			return s, fmt.Errorf("resolve scope root: %w", err)
		}
		s.Root = root
		return s, s.validate()
	}
	return LoadReviewScope(opts.Dir)
}

func applyScope(r *Report) {
	excluded := map[string]bool{}
	for _, f := range r.Files {
		if reason := r.Scope.sourceReason(r.Root, f.Path, f.Source); reason != "" {
			excluded[f.ID] = true
			r.Exclusions = append(r.Exclusions, ScopeExclusion{Path: f.Path, Reason: reason})
			r.SupportingFiles = append(r.SupportingFiles, f)
			r.Inputs = append(r.Inputs, Input{Path: f.Path, SHA256: f.SHA256})
		}
	}
	removed := map[string]bool{}
	testFiles := map[string]bool{}
	for _, f := range r.Files {
		testFiles[f.ID] = strings.HasSuffix(f.Path, "_test.go")
	}
	for _, source := range r.TestSources {
		testFiles[source.File.ID] = true
	}
	var removedRanges []Location
	for _, source := range r.TestSources {
		for _, declaration := range source.Declarations {
			if declaration.Kind != "helper" && testEntry(declaration.Name) && r.Scope.testReason(declaration.Name) != "" {
				removedRanges = append(removedRanges, declaration.Location)
			}
		}
	}
	for _, s := range r.Symbols {
		if testFiles[s.FileID] && s.Kind == "test" && testEntry(s.Name) && r.Scope.testReason(s.Name) != "" {
			removedRanges = append(removedRanges, s.Location)
		}
	}
	r.Symbols = slices.DeleteFunc(r.Symbols, func(s Symbol) bool {
		for _, loc := range removedRanges {
			if s.FileID == loc.FileID && s.Offset >= loc.Offset && s.EndOffset <= loc.EndOffset {
				removed[s.ID] = true
				return true
			}
		}
		if excluded[s.FileID] || testFiles[s.FileID] && s.Kind == "test" && testEntry(s.Name) && r.Scope.testReason(s.Name) != "" {
			removed[s.ID] = true
			return true
		}
		return false
	})
	r.Files = slices.DeleteFunc(r.Files, func(f File) bool { return excluded[f.ID] })
	r.Calls = slices.DeleteFunc(r.Calls, func(c Call) bool { return excluded[c.FileID] || removed[c.From] })
	for i := range r.Calls {
		if removed[r.Calls[i].To] {
			r.Calls[i].To = ""
			r.Calls[i].Reason = "Target outside review scope"
		}
	}
	r.ControlFlows = slices.DeleteFunc(r.ControlFlows, func(f ControlFlow) bool { return removed[f.SymbolID] })
	r.References = slices.DeleteFunc(r.References, func(f Reference) bool { return excluded[f.FileID] || removed[f.To] })
	r.HTTPEndpoints = slices.DeleteFunc(r.HTTPEndpoints, func(e HTTPEndpoint) bool { return excluded[e.FileID] })
	for i := range r.HTTPEndpoints {
		if removed[r.HTTPEndpoints[i].HandlerID] {
			r.HTTPEndpoints[i].HandlerID = ""
			r.HTTPEndpoints[i].Reason = "Handler outside review scope"
		}
	}
	included := map[string]bool{}
	for _, f := range r.Files {
		included[f.Package] = true
	}
	r.Packages = slices.DeleteFunc(r.Packages, func(p Package) bool { return !included[p.Path] })
	for i := range r.Packages {
		r.Packages[i].FileIDs = slices.DeleteFunc(r.Packages[i].FileIDs, func(id string) bool { return excluded[id] })
	}
	slices.SortFunc(r.Exclusions, func(a, b ScopeExclusion) int { return strings.Compare(a.Path, b.Path) })
}
