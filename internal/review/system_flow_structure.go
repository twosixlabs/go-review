package review

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
)

// In executable modules, imports reachable only from tests are supporting
// evidence, not application components. Unwired declarations remain visible;
// libraries without a main root are not assigned an invented production root.
func flowTestOnlyPackages(r *Report) (map[string]bool, []string) {
	packages := map[string]Package{}
	var roots []string
	for _, p := range r.Packages {
		packages[p.Path] = p
		if p.Name == "main" {
			roots = append(roots, p.Path)
		}
	}
	testOnly := map[string]bool{}
	if len(roots) == 0 {
		return testOnly, nil
	}
	reachable := func(roots []string) map[string]bool {
		seen := map[string]bool{}
		for i := 0; i < len(roots); i++ {
			p, found := packages[roots[i]]
			if !found || seen[p.Path] {
				continue
			}
			seen[p.Path] = true
			roots = append(roots, p.Imports...)
		}
		return seen
	}
	production := reachable(roots)
	var testRoots, limitations []string
	for _, source := range r.TestSources {
		tree, err := parser.ParseFile(token.NewFileSet(), source.File.Path, source.File.Source, parser.ImportsOnly)
		if err != nil {
			limitations = append(limitations, fmt.Sprintf("Test-import classification for %s is partial: %v", source.File.Path, err))
		}
		if tree == nil {
			continue
		}
		for _, spec := range tree.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				limitations = append(limitations, fmt.Sprintf("Test import %s: %v", spec.Path.Value, err))
				continue
			}
			testRoots = append(testRoots, path)
		}
	}
	for path := range reachable(testRoots) {
		if !production[path] {
			testOnly[path] = true
		}
	}
	if len(testOnly) > 0 {
		limitations = append(limitations, "Packages reachable only from test imports, not executable imports, remain source evidence but are omitted from production architecture.")
	}
	return testOnly, limitations
}

func flowHelperOwners(r *Report, callables map[string]OperationCallable, owners map[string]string) {
	helpers := map[string]bool{}
	incoming := map[string][]string{}
	for key, c := range callables {
		if c.Receiver == "" && !ast.IsExported(c.Name) && c.Name != "init" && c.Name != "main" {
			helpers[key] = true
		}
	}
	for _, call := range r.OperationSources.Calls {
		if owners[call.CallerKey] != "" {
			incoming[call.TargetKey] = append(incoming[call.TargetKey], call.CallerKey)
		}
	}
	// Follow callers through helper cycles to their architectural owners.
	// A helper shared by distinct owners stays in its package container.
	for key := range helpers {
		seen := map[string]bool{}
		candidates := map[string]bool{}
		var visit func(string)
		visit = func(current string) {
			if seen[current] {
				return
			}
			seen[current] = true
			for _, caller := range incoming[current] {
				if helpers[caller] {
					visit(caller)
				} else if owners[caller] != "" {
					candidates[owners[caller]] = true
				}
			}
		}
		visit(key)
		if len(candidates) == 1 {
			for owner := range candidates {
				owners[key] = owner
			}
		}
	}
}

// flowMemoryStores recognizes receiver-owned map state only when a method
// writes it. Maps used as request/response payloads are not storage evidence.
func flowMemoryStores(r *Report, callables map[string]OperationCallable) (map[string]bool, []string) {
	stores := map[string]bool{}
	var limitations []string
	for _, file := range r.Files {
		fset := token.NewFileSet()
		tree, err := parser.ParseFile(fset, file.Path, file.Source, 0)
		if err != nil {
			limitations = append(limitations, fmt.Sprintf("Storage ownership analysis for %s is partial: %v", file.Path, err))
		}
		if tree == nil {
			continue
		}
		maps := map[string]map[string]bool{}
		for _, decl := range tree.Decls {
			group, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range group.Specs {
				typ, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				fields, ok := typ.Type.(*ast.StructType)
				if !ok {
					continue
				}
				for _, field := range fields.Fields.List {
					if _, ok := field.Type.(*ast.MapType); !ok {
						continue
					}
					if maps[typ.Name.Name] == nil {
						maps[typ.Name.Name] = map[string]bool{}
					}
					for _, name := range field.Names {
						maps[typ.Name.Name][name.Name] = true
					}
				}
			}
		}
		for _, decl := range tree.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || fn.Body == nil || len(fn.Recv.List[0].Names) == 0 {
				continue
			}
			var receiver string
			for _, c := range callables {
				if c.Anchor.Location.FileID == file.ID && c.Anchor.Location.Offset == fset.Position(fn.Pos()).Offset {
					receiver = strings.TrimPrefix(c.Receiver, "*")
					break
				}
			}
			_, name := flowTypeName(receiver)
			if len(maps[name]) == 0 {
				continue
			}
			receiverIdent := fn.Recv.List[0].Names[0]
			mutates := func(expr ast.Expr) bool {
				selector, ok := expr.(*ast.SelectorExpr)
				if !ok || !maps[name][selector.Sel.Name] {
					return false
				}
				ident, ok := selector.X.(*ast.Ident)
				return ok && ident.Obj != nil && ident.Obj == receiverIdent.Obj
			}
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				switch node := node.(type) {
				case *ast.AssignStmt:
					for _, left := range node.Lhs {
						if index, ok := left.(*ast.IndexExpr); ok && mutates(index.X) {
							stores[receiver] = true
						}
					}
				case *ast.CallExpr:
					if id, ok := node.Fun.(*ast.Ident); ok && id.Name == "delete" && id.Obj == nil && len(node.Args) == 2 && mutates(node.Args[0]) {
						stores[receiver] = true
					}
				}
				return true
			})
		}
	}
	return stores, limitations
}
