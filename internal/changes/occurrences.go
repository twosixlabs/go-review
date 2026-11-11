package changes

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"

	"github.com/russellbrasser/go-review/internal/review"
)

func enclosingSymbol(r *review.Report, child review.Symbol) *review.Symbol {
	var parent *review.Symbol
	for i := range r.Symbols {
		s := &r.Symbols[i]
		if s.ID == child.ID || s.FileID != child.FileID || s.Offset > child.Offset || s.EndOffset < child.EndOffset {
			continue
		}
		if parent == nil || s.EndOffset-s.Offset < parent.EndOffset-parent.Offset {
			parent = s
		}
	}
	return parent
}

func syntaxText(fset *token.FileSet, node any) string {
	var text bytes.Buffer
	if err := printer.Fprint(&text, fset, node); err != nil {
		return ""
	}
	return text.String()
}

func closureContext(r *review.Report, s review.Symbol) string {
	for _, file := range r.Files {
		if file.ID != s.FileID {
			continue
		}
		fset := token.NewFileSet()
		tree, err := parser.ParseFile(fset, file.Path, file.Source, 0)
		if err != nil {
			return ""
		}
		stack := []ast.Node{}
		context := ""
		ast.Inspect(tree, func(node ast.Node) bool {
			if node == nil {
				stack = stack[:len(stack)-1]
				return false
			}
			if literal, ok := node.(*ast.FuncLit); ok && fset.Position(literal.Pos()).Offset == s.Offset {
				for i := len(stack) - 1; i >= 0; i-- {
					switch parent := stack[i].(type) {
					case *ast.AssignStmt:
						if len(parent.Lhs) == 1 {
							context = "assignment:" + syntaxText(fset, parent.Lhs[0]) + ":" + syntaxText(fset, literal.Type)
						}
						return false
					case *ast.CallExpr:
						copyCall := *parent
						copyCall.Args = append([]ast.Expr{}, parent.Args...)
						for j, arg := range copyCall.Args {
							if arg == literal {
								copyCall.Args[j] = ast.NewIdent("__callback")
							}
						}
						context = "argument:" + syntaxText(fset, &copyCall) + ":" + syntaxText(fset, literal.Type)
						return false
					case *ast.FuncLit, *ast.FuncDecl:
						return false
					}
				}
				return false
			}
			stack = append(stack, node)
			return true
		})
		return context
	}
	return ""
}

func closureCandidates(r *review.Report, matches map[string]review.Symbol, after bool) map[string][]review.Symbol {
	out := map[string][]review.Symbol{}
	if r == nil {
		return out
	}
	for _, s := range r.Symbols {
		if s.Kind != "closure" {
			continue
		}
		parent := enclosingSymbol(r, s)
		if parent == nil {
			continue
		}
		counterpart, matched := matches[parent.ID]
		if !matched {
			continue
		}
		context := closureContext(r, s)
		if context == "" {
			continue
		}
		owner := parent.ID
		if after {
			owner = counterpart.ID
		}
		key := identity(owner, context)
		out[key] = append(out[key], s)
	}
	return out
}

func (c *Comparison) matchClosures(module Module, before, after *review.Report, bmatch, amatch map[string]review.Symbol) {
	bs, as := closureCandidates(before, bmatch, false), closureCandidates(after, amatch, true)
	for key, values := range bs {
		if len(values) != 1 || len(as[key]) != 1 {
			continue
		}
		b, a := values[0], as[key][0]
		bmatch[b.ID] = a
		amatch[a.ID] = b
		bl, al := b.Location, a.Location
		c.Correspondences = append(c.Correspondences, Correspondence{Kind: "declaration", ModuleID: module.ID, BeforeID: b.ID, AfterID: a.ID, BeforeLocation: &bl, AfterLocation: &al, Reason: "Unique callback assignment/argument and signature within a matched enclosing declaration; not source offsets or traversal order"})
	}
}

func callCandidates(r *review.Report, owners map[string]review.Symbol, after bool) map[string][]review.Call {
	out := map[string][]review.Call{}
	if r == nil {
		return out
	}
	for _, call := range r.Calls {
		counterpart, ok := owners[call.From]
		if !ok {
			continue
		}
		owner := call.From
		if after {
			owner = counterpart.ID
		}
		for _, file := range r.Files {
			if file.ID != call.FileID || call.Offset < 0 || call.EndOffset > len(file.Source) {
				continue
			}
			expr, err := parser.ParseExpr(file.Source[call.Offset:call.EndOffset])
			if err != nil {
				continue
			}
			key := identity(owner, call.Kind, call.Execution, call.Invocation, syntaxText(token.NewFileSet(), expr))
			out[key] = append(out[key], call)
		}
	}
	return out
}

func (c *Comparison) matchCalls(module Module, before, after *review.Report, bmatch, amatch map[string]review.Symbol) {
	bs, as := callCandidates(before, bmatch, false), callCandidates(after, amatch, true)
	for key, values := range bs {
		if len(values) != 1 || len(as[key]) != 1 {
			continue
		}
		b, a := values[0], as[key][0]
		if (b.To == "") != (a.To == "") || b.To != "" && bmatch[b.To].ID != a.To {
			continue
		}
		bl, al := b.Location, a.Location
		c.Correspondences = append(c.Correspondences, Correspondence{Kind: "call", ModuleID: module.ID, BeforeID: b.ID, AfterID: a.ID, BeforeLocation: &bl, AfterLocation: &al, Reason: "Unique identical source call expression and invocation kind under a matched owner/target. Repeated indistinguishable expressions remain unmatched."})
	}
}
