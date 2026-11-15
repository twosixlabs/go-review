package testquality

import (
	"go/ast"
	"go/constant"
	"go/token"
	"strconv"
	"strings"
)

// Predicate inputs describe source-visible, potential truth values, not
// execution, branch coverage, assertion effectiveness, or intended contracts.
type PredicateFact struct {
	Function          string                 `json:"function"`
	Expression        string                 `json:"expression"`
	Evidence          Citation               `json:"evidence"`
	Alternatives      []PredicateAlternative `json:"alternatives"`
	MutuallyExclusive [][2]string            `json:"mutuallyExclusive,omitempty"`
}
type PredicateAlternative struct {
	ID         string           `json:"id"`
	Expression string           `json:"expression"`
	Evidence   Citation         `json:"evidence"`
	Inputs     []PredicateInput `json:"inputs"`
}
type PredicateInput struct {
	Value    string   `json:"value"`
	Truth    string   `json:"potentialTruth"`
	Call     Citation `json:"call"`
	Evidence Citation `json:"evidence"`
}
type predicateTemplate struct {
	expression   ast.Expr
	alternatives []ast.Expr
}
type literalInput struct {
	value    constant.Value
	evidence Citation
}

func referencedObject(expr ast.Expr) *ast.Object {
	switch n := expr.(type) {
	case *ast.Ident:
		return n.Obj
	case *ast.IndexExpr:
		return referencedObject(n.X)
	case *ast.SliceExpr:
		return referencedObject(n.X)
	case *ast.SelectorExpr:
		return referencedObject(n.X)
	}
	return nil
}

func literalAggregate(object *ast.Object) bool {
	if object == nil {
		return false
	}
	var expressions []ast.Expr
	switch declaration := object.Decl.(type) {
	case *ast.AssignStmt:
		expressions = declaration.Rhs
	case *ast.ValueSpec:
		expressions = declaration.Values
	}
	for _, expression := range expressions {
		if _, ok := expression.(*ast.CompositeLit); ok {
			return true
		}
	}
	return false
}

func objectWrites(body *ast.BlockStmt) map[*ast.Object]int {
	writes := map[*ast.Object]int{}
	if body == nil {
		return writes
	}
	add := func(expr ast.Expr) {
		if object := referencedObject(expr); object != nil {
			writes[object]++
		}
	}
	var parents []ast.Node
	ast.Inspect(body, func(node ast.Node) bool {
		if node == nil {
			parents = parents[:len(parents)-1]
			return true
		}
		var parent ast.Node
		if len(parents) > 0 {
			parent = parents[len(parents)-1]
		}
		parents = append(parents, node)
		switch n := node.(type) {
		case *ast.AssignStmt:
			for _, left := range n.Lhs {
				add(left)
			}
		case *ast.Ident:
			if !literalAggregate(n.Obj) {
				break
			}
			// Only the declaration and a direct range read preserve a literal
			// table. Every other use may expose an alias to mutable storage.
			safe := false
			switch p := parent.(type) {
			case *ast.AssignStmt:
				for _, left := range p.Lhs {
					safe = safe || left == n
				}
			case *ast.ValueSpec:
				for _, name := range p.Names {
					safe = safe || name == n
				}
			case *ast.RangeStmt:
				safe = p.X == n
			}
			if !safe {
				writes[n.Obj] += 2
			}
		case *ast.IncDecStmt:
			add(n.X)
		case *ast.RangeStmt:
			add(n.Key)
			add(n.Value)
		case *ast.UnaryExpr:
			// Taking an address makes later mutation opaque to this analysis.
			if n.Op == token.AND {
				if id, ok := n.X.(*ast.Ident); ok && id.Obj != nil {
					writes[id.Obj] += 2
				}
			}
		}
		return true
	})
	return writes
}

func predicateAlternatives(expr ast.Expr) []ast.Expr {
	if paren, ok := expr.(*ast.ParenExpr); ok {
		return predicateAlternatives(paren.X)
	}
	if binary, ok := expr.(*ast.BinaryExpr); ok && (binary.Op == token.LOR || binary.Op == token.LAND) {
		return append(predicateAlternatives(binary.X), predicateAlternatives(binary.Y)...)
	}
	return []ast.Expr{expr}
}

// Cache structural predicates on the same immutable AST index as test/helper
// facts. We deliberately do not solve path constraints or execute generators.
func (index *factIndex) predicateTemplates(fn *factFunction) []predicateTemplate {
	if index.predicates == nil {
		index.predicates = map[*factFunction][]predicateTemplate{}
	}
	if templates, ok := index.predicates[fn]; ok {
		return templates
	}
	templates := []predicateTemplate{}
	ast.Inspect(fn.node.Body, func(node ast.Node) bool {
		if _, ok := node.(*ast.FuncLit); ok {
			return false
		}
		var expression ast.Expr
		switch n := node.(type) {
		case *ast.IfStmt:
			expression = n.Cond
		case *ast.ForStmt:
			expression = n.Cond
		}
		if expression != nil && len(templates) < 32 {
			templates = append(templates, predicateTemplate{expression: expression, alternatives: predicateAlternatives(expression)})
		}
		return true
	})
	index.predicates[fn] = templates
	return templates
}

func (fn *factFunction) literalInputs(expr ast.Expr, ancestors []ast.Node, writes map[*ast.Object]int, depth int) []literalInput {
	if depth >= 4 {
		return nil
	}
	if value := constantExpression(expr); value.Kind() != constant.Unknown {
		return []literalInput{{value: value, evidence: fn.citation(expr)}}
	}
	id, ok := expr.(*ast.Ident)
	if !ok || id.Obj == nil || writes[id.Obj] > 1 {
		return nil
	}
	for _, parent := range ancestors {
		loop, ok := parent.(*ast.RangeStmt)
		if !ok {
			continue
		}
		value, ok := loop.Value.(*ast.Ident)
		if !ok || value.Obj != id.Obj {
			continue
		}
		table := loop.X
		if name, ok := table.(*ast.Ident); ok && name.Obj != nil && writes[name.Obj] <= 1 {
			if assignment, ok := name.Obj.Decl.(*ast.AssignStmt); ok && len(assignment.Rhs) == 1 {
				table = assignment.Rhs[0]
			}
		}
		literal, ok := table.(*ast.CompositeLit)
		if !ok || len(literal.Elts) > 64 {
			return nil
		}
		if _, ok := literal.Type.(*ast.ArrayType); !ok {
			return nil
		}
		inputs := []literalInput{}
		for _, element := range literal.Elts {
			resolved := fn.literalInputs(element, ancestors, writes, depth+1)
			if len(resolved) != 1 {
				return nil
			}
			inputs = append(inputs, resolved[0])
		}
		return inputs
	}
	switch declaration := id.Obj.Decl.(type) {
	case *ast.AssignStmt:
		if writes[id.Obj] != 1 || len(declaration.Lhs) != len(declaration.Rhs) {
			return nil
		}
		for position, left := range declaration.Lhs {
			if name, ok := left.(*ast.Ident); ok && name.Obj == id.Obj {
				return fn.literalInputs(declaration.Rhs[position], ancestors, writes, depth+1)
			}
		}
	case *ast.ValueSpec:
		if writes[id.Obj] != 0 || len(declaration.Values) != len(declaration.Names) || declaration.Pos() < fn.node.Body.Pos() {
			return nil
		}
		for position, name := range declaration.Names {
			if name.Obj == id.Obj {
				return fn.literalInputs(declaration.Values[position], ancestors, writes, depth+1)
			}
		}
	}
	return nil
}

func parameterPosition(fn *factFunction, expr ast.Expr) int {
	if paren, ok := expr.(*ast.ParenExpr); ok {
		return parameterPosition(fn, paren.X)
	}
	id, ok := expr.(*ast.Ident)
	if !ok || id.Obj == nil || fn.node.Type.Params == nil {
		return -1
	}
	position := 0
	for _, field := range fn.node.Type.Params.List {
		for _, name := range field.Names {
			if name.Obj == id.Obj {
				return position
			}
			position++
		}
	}
	return -1
}

// Only an unmodified integer/string/bool parameter directly compared with a
// literal is resolvable. Aliases, mutable parameters and expressions involving
// multiple parameters stay unknown rather than implying absent test coverage.
func alternativeInput(fn *factFunction, expr ast.Expr) (int, constant.Value, token.Token, bool) {
	binary, ok := expr.(*ast.BinaryExpr)
	if !ok {
		return -1, constant.MakeUnknown(), token.ILLEGAL, false
	}
	switch binary.Op {
	case token.EQL, token.NEQ, token.LSS, token.LEQ, token.GTR, token.GEQ:
	default:
		return -1, constant.MakeUnknown(), token.ILLEGAL, false
	}
	position := parameterPosition(fn, binary.X)
	value := constantExpression(binary.Y)
	reverse := false
	if position < 0 {
		position = parameterPosition(fn, binary.Y)
		value = constantExpression(binary.X)
		reverse = true
	}
	if position < 0 || value.Kind() == constant.Unknown {
		return -1, value, binary.Op, reverse
	}
	for _, field := range fn.node.Type.Params.List {
		for _, name := range field.Names {
			if position == 0 {
				if fn.writes[name.Obj] > 0 {
					return -1, value, binary.Op, reverse
				}
				kind, ok := field.Type.(*ast.Ident)
				if !ok || kind.Obj != nil {
					return -1, value, binary.Op, reverse
				}
				switch kind.Name {
				case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "uintptr", "byte", "rune", "string", "bool":
				default:
					return -1, value, binary.Op, reverse
				}
				return parameterPosition(fn, name), value, binary.Op, reverse
			}
			position--
		}
	}
	return -1, value, binary.Op, reverse
}

func potentialTruth(value, bound constant.Value, op token.Token, reverse bool) string {
	if value.Kind() == constant.Unknown || value.Kind() != bound.Kind() || value.Kind() == constant.Complex {
		return "unknown"
	}
	if value.Kind() == constant.Bool && op != token.EQL && op != token.NEQ {
		return "unknown"
	}
	if reverse {
		value, bound = bound, value
	}
	if constant.Compare(value, op, bound) {
		return "true"
	}
	return "false"
}

// Two direct integer comparisons can prove an empty intersection without
// enumerating inputs. Other predicates remain unresolved, not presumed feasible.
func exclusiveAlternatives(fn *factFunction, expressions []ast.Expr) bool {
	if len(expressions) != 2 {
		return false
	}
	position := -1
	var lower, upper constant.Value
	for _, expression := range expressions {
		param, bound, op, reverse := alternativeInput(fn, expression)
		if param < 0 || bound.Kind() != constant.Int || position >= 0 && position != param {
			return false
		}
		position = param
		if reverse {
			switch op {
			case token.LSS:
				op = token.GTR
			case token.LEQ:
				op = token.GEQ
			case token.GTR:
				op = token.LSS
			case token.GEQ:
				op = token.LEQ
			}
		}
		switch op {
		case token.LSS, token.LEQ:
			if op == token.LSS {
				bound = constant.BinaryOp(bound, token.SUB, constant.MakeInt64(1))
			}
			if upper == nil || constant.Compare(bound, token.LSS, upper) {
				upper = bound
			}
		case token.GTR, token.GEQ:
			if op == token.GTR {
				bound = constant.BinaryOp(bound, token.ADD, constant.MakeInt64(1))
			}
			if lower == nil || constant.Compare(bound, token.GTR, lower) {
				lower = bound
			}
		default:
			return false
		}
	}
	index := 0
	for _, field := range fn.node.Type.Params.List {
		for range field.Names {
			if index == position {
				kind, ok := field.Type.(*ast.Ident)
				if !ok || kind.Name == "string" || kind.Name == "bool" {
					return false
				}
			}
			index++
		}
	}
	return lower != nil && upper != nil && constant.Compare(lower, token.GTR, upper)
}

func (index *factIndex) callPredicates(caller *factFunction, call *ast.CallExpr, ancestors []ast.Node, writes map[*ast.Object]int) []PredicateFact {
	name, ok := call.Fun.(*ast.Ident)
	if !ok || name.Obj != nil && name.Obj.Kind != ast.Fun {
		return nil
	}
	matches := index.functions[functionKey(caller.snapshot, caller.pkg, name.Name)]
	if len(matches) != 1 {
		return nil
	}
	target := matches[0]
	if target.node.Body == nil || strings.HasSuffix(index.sources[target.snapshot+"\n"+target.file].Path, "_test.go") {
		return nil
	}
	facts := []PredicateFact{}
	for _, template := range index.predicateTemplates(target) {
		fact := PredicateFact{Function: name.Name, Expression: factText(target.fset, template.expression), Evidence: target.citation(template.expression)}
		for _, expr := range template.alternatives {
			alternative := PredicateAlternative{Expression: factText(target.fset, expr), Evidence: target.citation(expr)}
			identity := target.snapshot + "\n" + target.file + "\n" + strconv.Itoa(int(expr.Pos()))
			alternative.ID = Digest([]byte(identity))
			position, bound, op, reverse := alternativeInput(target, expr)
			inputs := []literalInput{}
			if position >= 0 && position < len(call.Args) {
				inputs = caller.literalInputs(call.Args[position], ancestors, writes, 0)
			}
			if len(inputs) == 0 {
				alternative.Inputs = []PredicateInput{{Value: "unresolved input", Truth: "unknown", Call: caller.citation(call), Evidence: caller.citation(call)}}
			}
			for _, input := range inputs {
				alternative.Inputs = append(alternative.Inputs, PredicateInput{Value: input.value.ExactString(), Truth: potentialTruth(input.value, bound, op, reverse), Call: caller.citation(call), Evidence: input.evidence})
			}
			fact.Alternatives = append(fact.Alternatives, alternative)
		}
		if exclusiveAlternatives(target, template.alternatives) {
			fact.MutuallyExclusive = [][2]string{{fact.Alternatives[0].ID, fact.Alternatives[1].ID}}
		}
		facts = append(facts, fact)
	}
	return facts
}
