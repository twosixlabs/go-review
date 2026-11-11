package review

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/printer"
	"go/token"
	"go/types"
	"strings"
)

func (a *analyzer) text(n ast.Node) string {
	var b bytes.Buffer
	_ = printer.Fprint(&b, a.fset, n)
	return b.String()
}

func (a *analyzer) symbolID(pkg, name string, pos token.Pos) string {
	loc := a.location(pos, pos)
	return stableID("sym", fmt.Sprintf("%s:%s:%d:%s", pkg, loc.FileID, loc.Offset, name))
}

func (a *analyzer) collectSymbols(p *parsedPackage) {
	for _, file := range p.ast {
		ast.Inspect(file, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.FuncDecl:
				name := n.Name.Name
				obj := p.info.Defs[n.Name]
				qualified := p.meta.ImportPath + "." + name
				if obj != nil {
					qualified = types.ObjectString(obj, func(p *types.Package) string { return p.Path() })
				}
				s := Symbol{ID: a.symbolID(p.meta.ImportPath, name, n.Pos()), Package: p.meta.ImportPath, Name: name, Qualified: qualified, Kind: "function", Signature: a.text(n.Type), Location: a.location(n.Pos(), n.End())}
				if n.Recv != nil {
					s.Kind = "method"
					s.Signature = a.text(n.Recv) + " " + name + s.Signature
				}
				if n.Doc != nil {
					s.Doc = n.Doc.Text()
				}
				for _, comment := range file.Comments {
					if comment.Pos() >= n.Pos() && comment.End() <= n.End() {
						s.Comments = append(s.Comments, Comment{Text: comment.Text(), Location: a.location(comment.Pos(), comment.End())})
					}
				}
				if n.Body != nil {
					s.Cyclomatic = complexity(n.Body)
				}
				a.report.Symbols = append(a.report.Symbols, s)
				if obj != nil {
					a.objects[obj] = s.ID
				}
			case *ast.FuncLit:
				name := fmt.Sprintf("func literal at %d:%d", a.fset.Position(n.Pos()).Line, a.fset.Position(n.Pos()).Column)
				id := a.symbolID(p.meta.ImportPath, name, n.Pos())
				a.literals[n] = id
				a.report.Symbols = append(a.report.Symbols, Symbol{ID: id, Package: p.meta.ImportPath, Name: name, Qualified: p.meta.ImportPath + "." + name, Kind: "closure", Signature: a.text(n.Type), Cyclomatic: complexity(n.Body), Location: a.location(n.Pos(), n.End())})
			case *ast.GenDecl:
				for _, spec := range n.Specs {
					var ids []*ast.Ident
					var doc *ast.CommentGroup
					kind := "variable"
					switch sp := spec.(type) {
					case *ast.TypeSpec:
						ids = []*ast.Ident{sp.Name}
						doc = sp.Doc
						kind = "type"
					case *ast.ValueSpec:
						ids = sp.Names
						doc = sp.Doc
						if n.Tok == token.CONST {
							kind = "constant"
						}
					}
					if doc == nil {
						doc = n.Doc
					}
					for _, ident := range ids {
						obj := p.info.Defs[ident]
						// Only package-level declarations have package ownership.
						if obj == nil || p.pkg == nil || obj.Parent() != p.pkg.Scope() {
							continue
						}
						id := a.symbolID(p.meta.ImportPath, ident.Name, ident.Pos())
						s := Symbol{ID: id, Package: p.meta.ImportPath, Name: ident.Name, Qualified: p.meta.ImportPath + "." + ident.Name, Kind: kind, Signature: types.ObjectString(obj, func(p *types.Package) string { return p.Path() }), Location: a.location(spec.Pos(), spec.End())}
						if doc != nil {
							s.Doc = doc.Text()
						}
						a.objects[obj] = id
						a.report.Symbols = append(a.report.Symbols, s)
					}
				}
			}
			return true
		})
	}
}

func complexity(body *ast.BlockStmt) int {
	n := 1
	ast.Inspect(body, func(node ast.Node) bool {
		switch v := node.(type) {
		case *ast.FuncLit:
			return false
		case *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt:
			n++
		case *ast.CaseClause:
			if v.List != nil {
				n++
			}
		case *ast.CommClause:
			if v.Comm != nil {
				n++
			}
		case *ast.BinaryExpr:
			if v.Op == token.LAND || v.Op == token.LOR {
				n++
			}
		}
		return true
	})
	return n
}

type edgeVisitor struct {
	a           *analyzer
	p           *parsedPackage
	owner       string
	callees     map[ast.Node]bool
	invocations map[*ast.CallExpr]string
	currentCall *ast.CallExpr
	callIndices map[*ast.CallExpr]int
	order       map[string]int
}

func (v edgeVisitor) Visit(node ast.Node) ast.Visitor {
	if node == nil {
		if v.currentCall != nil {
			v.order[v.owner]++
			v.a.report.Calls[v.callIndices[v.currentCall]].EvaluationOrder = v.order[v.owner]
		}
		return nil
	}
	v.currentCall = nil
	switch n := node.(type) {
	case *ast.GoStmt:
		v.invocations[n.Call] = "goroutine"
	case *ast.DeferStmt:
		v.invocations[n.Call] = "deferred"
	case *ast.FuncDecl:
		v.owner = v.a.objects[v.p.info.Defs[n.Name]]
	case *ast.FuncLit:
		id := v.a.literals[n]
		usage := "value"
		if v.callees[n] {
			usage = "callee"
		}
		v.a.report.References = append(v.a.report.References, Reference{From: v.owner, To: id,
			Name:  fmt.Sprintf("func literal at %d:%d", v.a.fset.Position(n.Pos()).Line, v.a.fset.Position(n.Pos()).Column),
			Usage: usage, Location: v.a.location(n.Pos(), n.End())})
		v.owner = v.a.literals[n]
	case *ast.CallExpr:
		switch target := unwrap(n.Fun).(type) {
		case *ast.Ident:
			v.callees[target] = true
		case *ast.SelectorExpr:
			v.callees[target.Sel] = true
		case *ast.FuncLit:
			v.callees[target] = true
		}
		invocation := v.invocations[n]
		if invocation == "" {
			invocation = "synchronous"
		}
		v.a.call(v.p, v.owner, n, invocation)
		v.currentCall = n
		v.callIndices[n] = len(v.a.report.Calls) - 1
		v.a.collectHTTPRegistration(v.p, v.owner, n)
	case *ast.Ident:
		if id := v.a.objects[v.p.info.Uses[n]]; id != "" {
			usage := ""
			if _, ok := v.p.info.Uses[n].(*types.Func); ok {
				usage = "value"
				if v.callees[n] {
					usage = "callee"
				}
			}
			v.a.report.References = append(v.a.report.References, Reference{From: v.owner, To: id, Name: n.Name, Usage: usage, Location: v.a.location(n.Pos(), n.End())})
		}
	}
	return v
}

func (a *analyzer) collectEdges(p *parsedPackage) {
	for _, f := range p.ast {
		ast.Walk(edgeVisitor{a: a, p: p, callees: map[ast.Node]bool{}, invocations: map[*ast.CallExpr]string{},
			callIndices: map[*ast.CallExpr]int{}, order: map[string]int{}}, f)
	}
}

func unwrap(e ast.Expr) ast.Expr {
	switch n := e.(type) {
	case *ast.ParenExpr:
		return unwrap(n.X)
	case *ast.IndexExpr:
		return unwrap(n.X)
	case *ast.IndexListExpr:
		return unwrap(n.X)
	}
	return e
}

func (a *analyzer) call(p *parsedPackage, from string, n *ast.CallExpr, invocation string) {
	loc := a.location(n.Pos(), n.End())
	// Chained calls can share their start offset, but not their full range.
	c := Call{ID: stableID("call", fmt.Sprintf("%s:%d:%d", loc.FileID, loc.Offset, loc.EndOffset)), From: from, Target: a.text(n.Fun), Kind: "unresolved", Execution: "possible", Invocation: invocation, Location: loc}
	if tv, ok := p.info.Types[n.Fun]; ok && tv.IsType() {
		c.Kind = "conversion"
		c.Reason = "type conversion, not a function invocation"
	} else {
		e := unwrap(n.Fun)
		var obj types.Object
		switch e := e.(type) {
		case *ast.FuncLit:
			c.Kind = "static"
			c.To = a.literals[e]
			c.Target = fmt.Sprintf("func literal at %d:%d", a.fset.Position(e.Pos()).Line, a.fset.Position(e.Pos()).Column)
		case *ast.Ident:
			obj = p.info.Uses[e]
		case *ast.SelectorExpr:
			obj = p.info.Uses[e.Sel]
			if sel := p.info.Selections[e]; sel != nil {
				obj = sel.Obj()
			}
		}
		switch obj := obj.(type) {
		case *types.Func:
			c.Kind = "static"
			c.To = a.objects[obj.Origin()]
			c.Target = types.ObjectString(obj, func(p *types.Package) string { return p.Path() })
			if sig, ok := obj.Type().(*types.Signature); ok && sig.Recv() != nil {
				t := sig.Recv().Type()
				if ptr, ok := t.(*types.Pointer); ok {
					t = ptr.Elem()
				}
				if _, ok := t.Underlying().(*types.Interface); ok {
					c.Kind = "indirect"
					c.To = ""
					c.Reason = "interface dispatch; concrete runtime receiver unavailable"
				}
			}
			if c.To == "" && c.Kind == "static" {
				c.Reason = "external declaration; source not in selected module packages"
			}
		case *types.Builtin:
			c.Kind = "builtin"
			c.Target = obj.Name()
		case *types.Var:
			c.Kind = "indirect"
			c.Reason = "function value; runtime target unavailable"
		}
		if c.Kind == "unresolved" {
			c.Reason = "type information unavailable or dynamic call expression"
		}
	}
	c.Target = strings.TrimSpace(c.Target)
	a.report.Calls = append(a.report.Calls, c)
}
