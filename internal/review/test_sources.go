package review

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// collectTestSources snapshots test code separately from production files and metrics.
func collectTestSources(ctx context.Context, a *analyzer, packagePaths []string) error {
	r := a.report
	for _, path := range packagePaths {
		p := a.packages[path]
		names := append(append([]string{}, p.meta.TestGoFiles...), p.meta.XTestGoFiles...)
		sort.Strings(names)
		for _, name := range names {
			if err := ctx.Err(); err != nil {
				return err
			}
			abs := filepath.Join(p.meta.Dir, name)
			if !inside(r.Root, abs) {
				r.Warnings = append(r.Warnings, "skipped test source outside module: "+abs)
				continue
			}
			rel, err := filepath.Rel(r.Root, abs)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			data, err := os.ReadFile(abs)
			if err != nil {
				r.Warnings = append(r.Warnings, fmt.Sprintf("test source read failed: %s: %v", rel, err))
				continue
			}
			if reason := r.Scope.sourceReason(r.Root, rel, string(data)); reason != "" {
				recordExclusion(r, rel, reason)
				continue
			}

			fset := token.NewFileSet()
			file, parseErr := parser.ParseFile(fset, abs, data, parser.ParseComments|parser.AllErrors)
			packagePath := path
			if file != nil && file.Name != nil && file.Name.Name != p.meta.Name {
				packagePath += "_test"
			}
			fileID := stableID("file", packagePath+"/"+name)
			source := File{ID: fileID, Package: packagePath, Path: rel, SHA256: digest(data), Source: string(data)}
			testSource := TestSource{File: source, Declarations: []TestDeclaration{}, ExcludedRanges: []Location{}}
			if parseErr != nil {
				r.Warnings = append(r.Warnings, fmt.Sprintf("test source parse failed: %s: %v", rel, parseErr))
			}
			if file != nil {
				for _, declaration := range file.Decls {
					fn, ok := declaration.(*ast.FuncDecl)
					if !ok {
						continue
					}
					// IDs and ranges use stable token byte offsets; the file hash covers the original bytes.
					start, end := fset.Position(fn.Pos()), fset.Position(fn.End())
					location := Location{
						FileID: fileID, Line: start.Line, Column: start.Column,
						EndLine: end.Line, EndColumn: end.Column,
						Offset: start.Offset, EndOffset: end.Offset,
					}
					kind := "helper"
					entry := fn.Recv == nil && testEntry(fn.Name.Name)
					if entry {
						kind = testDeclarationKind(fn.Name.Name)
					}
					id := stableID("test-decl", fmt.Sprintf("%s:%d:%s", fileID, start.Offset, fn.Name.Name))
					testSource.Declarations = append(testSource.Declarations, TestDeclaration{
						ID: id, Name: fn.Name.Name, Kind: kind, Location: location,
					})
					if entry && r.Scope.testReason(fn.Name.Name) != "" {
						// The range covers the full declaration, including nested closures, for downstream disclosure filtering.
						testSource.ExcludedRanges = append(testSource.ExcludedRanges, location)
					}
				}
			}
			r.TestSources = append(r.TestSources, testSource)
		}
	}
	return nil
}

func testDeclarationKind(name string) string {
	for _, prefix := range []string{"Test", "Example", "Fuzz", "Benchmark"} {
		if strings.HasPrefix(name, prefix) {
			rest := strings.TrimPrefix(name, prefix)
			if rest == "" {
				return strings.ToLower(prefix[:1]) + prefix[1:]
			}
			first, _ := utf8.DecodeRuneInString(rest)
			if !unicode.IsLower(first) {
				return strings.ToLower(prefix[:1]) + prefix[1:]
			}
		}
	}
	return "helper"
}
