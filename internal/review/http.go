package review

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/types"
)

func (a *analyzer) collectHTTPRegistration(p *parsedPackage, owner string, call *ast.CallExpr) {
	var fn *types.Func
	var receiver string
	arg := 0
	switch expr := unwrap(call.Fun).(type) {
	case *ast.SelectorExpr:
		fn, _ = p.info.Uses[expr.Sel].(*types.Func)
		if selection := p.info.Selections[expr]; selection != nil {
			fn, _ = selection.Obj().(*types.Func)
			receiver = a.text(expr.X)
			if selection.Kind() == types.MethodExpr {
				arg = 1
				if len(call.Args) > 0 {
					receiver = a.text(call.Args[0])
				}
			}
		}
	case *ast.Ident:
		fn, _ = p.info.Uses[expr].(*types.Func)
	}
	if fn == nil || fn.Pkg() == nil || fn.Pkg().Path() != "net/http" || (fn.Name() != "HandleFunc" && fn.Name() != "Handle") {
		return
	}
	signature, ok := fn.Type().(*types.Signature)
	if !ok {
		return
	}
	if signature.Recv() != nil {
		t := signature.Recv().Type()
		if ptr, ok := t.(*types.Pointer); ok {
			t = ptr.Elem()
		}
		named, ok := t.(*types.Named)
		if !ok || named.Obj().Pkg() == nil || named.Obj().Pkg().Path() != "net/http" || named.Obj().Name() != "ServeMux" {
			return
		}
	}
	if len(call.Args) != arg+2 {
		return
	}
	pattern, handler := call.Args[arg], call.Args[arg+1]
	location := a.location(call.Pos(), call.End())
	endpoint := HTTPEndpoint{
		ID:             stableID("http", fmt.Sprintf("%s:%d", location.FileID, location.Offset)),
		RegistrationID: owner, API: "net/http." + fn.Name(), Receiver: receiver,
		PatternExpression: a.text(pattern), Handler: a.text(handler), Location: location,
	}
	if value := p.info.Types[pattern].Value; value != nil && value.Kind() == constant.String {
		endpoint.PatternKnown = true
		endpoint.Pattern = constant.StringVal(value)
	}
	endpoint.HandlerID = a.httpHandler(p, handler, fn.Name() == "HandleFunc")
	if endpoint.HandlerID == "" {
		endpoint.Reason = "No verified local handler implementation; function values, middleware results, interfaces and external handlers are not followed."
	}
	a.report.HTTPEndpoints = append(a.report.HTTPEndpoints, endpoint)
}

func (a *analyzer) httpHandler(p *parsedPackage, expr ast.Expr, function bool) string {
	expr = unwrap(expr)
	// HandlerFunc is a conversion with known dispatch, not arbitrary middleware.
	if call, ok := expr.(*ast.CallExpr); ok && len(call.Args) == 1 {
		if tv, ok := p.info.Types[call.Fun]; ok && tv.IsType() {
			if named, ok := tv.Type.(*types.Named); ok && named.Obj().Pkg() != nil &&
				named.Obj().Pkg().Path() == "net/http" && named.Obj().Name() == "HandlerFunc" {
				return a.httpHandler(p, call.Args[0], true)
			}
		}
	}
	if function {
		var obj types.Object
		switch expr := expr.(type) {
		case *ast.FuncLit:
			return a.literals[expr]
		case *ast.Ident:
			obj = p.info.Uses[expr]
		case *ast.SelectorExpr:
			obj = p.info.Uses[expr.Sel]
		}
		if fn, ok := obj.(*types.Func); ok {
			sig, ok := fn.Type().(*types.Signature)
			if ok && sig.Recv() != nil {
				t := sig.Recv().Type()
				if ptr, ok := t.(*types.Pointer); ok {
					t = ptr.Elem()
				}
				if _, ok := t.Underlying().(*types.Interface); ok {
					return ""
				}
			}
			return a.objects[fn.Origin()]
		}
		return ""
	}
	t := p.info.TypeOf(expr)
	if t == nil {
		return ""
	}
	if _, ok := t.Underlying().(*types.Interface); ok {
		return ""
	}
	methods := types.NewMethodSet(t)
	for i := 0; i < methods.Len(); i++ {
		if fn, ok := methods.At(i).Obj().(*types.Func); ok && fn.Name() == "ServeHTTP" {
			return a.objects[fn.Origin()]
		}
	}
	return ""
}
