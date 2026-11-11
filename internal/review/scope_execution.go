package review

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

type executionScope struct {
	packages []string
	overlay  map[string]string
}

func (s ReviewScope) testReason(name string) string {
	for _, p := range s.ExcludeTests {
		re := regexp.MustCompile(p) // Scope validation precedes all collection.
		if re.MatchString(name) {
			return "Excluded test: " + p
		}
	}
	return ""
}

func testEntry(name string) bool {
	// Match Go test discovery: Testhelper is an ordinary helper, not a Test.
	for _, prefix := range []string{"Test", "Example", "Fuzz", "Benchmark"} {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		if len(name) == len(prefix) {
			return true
		}
		next, _ := utf8.DecodeRuneInString(name[len(prefix):])
		return !unicode.IsLower(next)
	}
	return false
}

func recordExclusion(r *Report, name, reason string) {
	for _, e := range r.Exclusions {
		if e.Path == name && e.Reason == reason {
			return
		}
	}
	r.Exclusions = append(r.Exclusions, ScopeExclusion{Path: name, Reason: reason})
}

// Scope overlays change only the isolated build: excluded test files become
// empty packages, and excluded test entry points are renamed so the generated
// test main cannot invoke them. Required helpers/init/dependencies may still run.
func prepareExecutionScope(ctx context.Context, r *Report, dir, out string, patterns []string) (executionScope, error) {
	result := executionScope{overlay: map[string]string{}}
	args := append([]string{"list", "-mod=readonly", "-json"}, patterns...)
	data, err := runGo(ctx, r, dir, false, args...)
	if err != nil {
		return result, fmt.Errorf("list scoped test targets: %w", err)
	}
	allowed := map[string]bool{}
	for _, p := range r.Packages {
		allowed[p.Path] = true
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	for decoder.More() {
		var p struct {
			ImportPath, Dir           string
			TestGoFiles, XTestGoFiles []string
		}
		if err = decoder.Decode(&p); err != nil {
			return result, err
		}
		if !inside(r.Root, p.Dir) {
			return result, fmt.Errorf("test target outside module: %s", p.ImportPath)
		}
		if !allowed[p.ImportPath] {
			continue
		}
		result.packages = append(result.packages, p.ImportPath)
		names := append(p.TestGoFiles, p.XTestGoFiles...)
		for _, name := range names {
			abs := filepath.Join(p.Dir, name)
			source, e := os.ReadFile(abs)
			if e != nil {
				return result, e
			}
			original := source
			rel, e := filepath.Rel(r.Root, abs)
			if e != nil {
				return result, e
			}
			rel = filepath.ToSlash(rel)
			fs := token.NewFileSet()
			tree, e := parser.ParseFile(fs, abs, source, parser.ParseComments)
			if e != nil {
				return result, fmt.Errorf("parse scoped tests: %w", e)
			}
			reason := r.Scope.sourceReason(r.Root, rel, string(source))
			if reason != "" {
				recordExclusion(r, rel, reason)
				source = []byte("package " + tree.Name.Name + "\n")
			} else {
				// Rewrite backwards to retain exact offsets in the original file.
				for i := len(tree.Decls) - 1; i >= 0; i-- {
					fn, ok := tree.Decls[i].(*ast.FuncDecl)
					if !ok || fn.Recv != nil || !testEntry(fn.Name.Name) {
						continue
					}
					why := r.Scope.testReason(fn.Name.Name)
					if why == "" {
						continue
					}
					if fn.Name.Name == "TestMain" {
						return result, fmt.Errorf("cannot safely exclude TestMain by name; exclude its test file explicitly or remove this test-name rule")
					}
					recordExclusion(r, rel+"::"+fn.Name.Name, why)
					offset := fs.Position(fn.Name.Pos()).Offset
					source = append(append(append([]byte{}, source[:offset]...), []byte("goReviewExcluded_")...), source[offset:]...)
				}
			}
			if bytes.Equal(source, original) {
				continue
			}
			if err = os.MkdirAll(out, 0700); err != nil {
				return result, err
			}
			target := filepath.Join(out, digest([]byte(abs))+".go")
			if err = os.WriteFile(target, source, 0600); err != nil {
				return result, err
			}
			result.overlay[abs] = target
		}
	}
	slices.Sort(result.packages)
	if len(result.packages) == 0 {
		return result, fmt.Errorf("no test packages in review scope")
	}
	return result, nil
}

func writeScopeOverlay(out string, overlay map[string]string) (string, error) {
	if len(overlay) == 0 {
		return "", nil
	}
	if err := os.MkdirAll(out, 0700); err != nil {
		return "", err
	}
	data, err := json.Marshal(struct{ Replace map[string]string }{overlay})
	if err != nil {
		return "", err
	}
	name := filepath.Join(out, "scope-overlay.json")
	return name, os.WriteFile(name, data, 0600)
}

func executionArgs(overlay string) []string {
	if overlay == "" {
		return nil
	}
	return []string{"-overlay=" + overlay}
}
