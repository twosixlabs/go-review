package review

import (
	"fmt"
	"go/ast"
	"go/types"
	"path/filepath"
	"sort"
)

const operationSourcesVersion = "operation-sources/1"

type OperationSources struct {
	Version     string                `json:"version"`
	Callables   []OperationCallable   `json:"callables"`
	Calls       []OperationCall       `json:"calls"`
	Returns     []OperationReturn     `json:"returns"`
	Bindings    []OperationBinding    `json:"bindings"`
	FieldStores []OperationFieldStore `json:"fieldStores"`
}

type OperationSlot struct {
	Position int         `json:"position"`
	Value    SourceValue `json:"value"`
}

type OperationCallable struct {
	Key           string          `json:"key"`
	SymbolID      string          `json:"symbolID"`
	Package       string          `json:"package"`
	Receiver      string          `json:"receiver,omitempty"`
	ReceiverValue *SourceValue    `json:"receiverValue,omitempty"`
	Name          string          `json:"name"`
	Kind          string          `json:"kind"`
	Anchor        OperationAnchor `json:"anchor"`
	Parameters    []OperationSlot `json:"parameters"`
	Results       []OperationSlot `json:"results"`
}

type OperationArgument struct {
	Position          int         `json:"position"`
	ParameterPosition int         `json:"parameterPosition"`
	TuplePosition     *int        `json:"tuplePosition,omitempty"`
	Value             SourceValue `json:"value"`
	Status            string      `json:"status"`
	Reason            string      `json:"reason,omitempty"`
}

type OperationResultBinding struct {
	Position    int             `json:"position"`
	Destination *SourceValue    `json:"destination,omitempty"`
	Status      string          `json:"status"`
	Reason      string          `json:"reason,omitempty"`
	Anchor      OperationAnchor `json:"anchor"`
}

type OperationCall struct {
	ID            string                   `json:"id"`
	CallID        string                   `json:"callID,omitempty"`
	CallerKey     string                   `json:"callerKey"`
	TargetKey     string                   `json:"targetKey,omitempty"`
	TargetName    string                   `json:"targetName"`
	TargetPackage string                   `json:"targetPackage,omitempty"`
	TargetOrigin  string                   `json:"targetOrigin,omitempty"`
	TargetStatus  string                   `json:"targetStatus"`
	Reason        string                   `json:"reason,omitempty"`
	Anchor        OperationAnchor          `json:"anchor"`
	Receiver      *SourceValue             `json:"receiver,omitempty"`
	Arguments     []OperationArgument      `json:"arguments"`
	Results       []OperationResultBinding `json:"results"`
	ResultTypes   []string                 `json:"resultTypes,omitempty"`
}

type OperationReturn struct {
	CallableKey string          `json:"callableKey"`
	Position    int             `json:"position"`
	Value       *SourceValue    `json:"value,omitempty"`
	Anchor      OperationAnchor `json:"anchor"`
	Status      string          `json:"status"`
	Reason      string          `json:"reason,omitempty"`
}

type OperationBinding struct {
	OwnerKey       string          `json:"ownerKey"`
	Source         *SourceValue    `json:"source,omitempty"`
	Destination    *SourceValue    `json:"destination,omitempty"`
	SourceKey      string          `json:"sourceKey,omitempty"`
	DestinationKey string          `json:"destinationKey,omitempty"`
	TuplePosition  *int            `json:"tuplePosition,omitempty"`
	Kind           string          `json:"kind"`
	Anchor         OperationAnchor `json:"anchor"`
	Status         string          `json:"status"`
	Reason         string          `json:"reason,omitempty"`
}

type OperationFieldStore struct {
	OwnerKey  string          `json:"ownerKey"`
	Receiver  *SourceValue    `json:"receiver,omitempty"`
	FieldKey  string          `json:"fieldKey,omitempty"`
	FieldName string          `json:"fieldName"`
	Value     *SourceValue    `json:"value,omitempty"`
	Kind      string          `json:"kind"`
	Anchor    OperationAnchor `json:"anchor"`
	Status    string          `json:"status"`
	Reason    string          `json:"reason,omitempty"`
}

type operationSourceCollector struct {
	a             *analyzer
	sources       *OperationSources
	includedFiles map[string]bool
	sourceByFile  map[string]string
	symbols       map[string]Symbol
	callables     map[string]OperationCallable
	symbolKeys    map[string]string
	literalKeys   map[*ast.FuncLit]string
}

func collectOperationSources(a *analyzer) error {
	r := a.report
	c := &operationSourceCollector{
		a: a,
		sources: &OperationSources{
			Version: operationSourcesVersion, Callables: []OperationCallable{}, Calls: []OperationCall{},
			Returns: []OperationReturn{}, Bindings: []OperationBinding{}, FieldStores: []OperationFieldStore{},
		},
		includedFiles: map[string]bool{}, sourceByFile: map[string]string{},
		symbols: map[string]Symbol{}, callables: map[string]OperationCallable{},
		symbolKeys: map[string]string{}, literalKeys: map[*ast.FuncLit]string{},
	}
	for _, file := range r.Files {
		c.includedFiles[file.ID] = true
		c.sourceByFile[file.ID] = file.Source
	}
	for _, symbol := range r.Symbols {
		c.symbols[symbol.ID] = symbol
	}
	paths := make([]string, 0, len(a.packages))
	for path := range a.packages {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		p := a.packages[path]
		for _, file := range p.ast {
			if !c.includedFiles[a.fileIDs[a.fset.PositionFor(file.Pos(), false).Filename]] {
				continue
			}
			ast.Inspect(file, func(node ast.Node) bool {
				literal, ok := node.(*ast.FuncLit)
				if !ok {
					return true
				}
				if symbolID := a.literals[literal]; symbolID != "" {
					if _, included := c.symbols[symbolID]; included {
						callable := c.newCallable(p, literal, symbolID)
						c.callables[symbolID] = callable
						c.symbolKeys[symbolID] = callable.Key
						c.literalKeys[literal] = callable.Key
					}
				}
				return true
			})
			for _, declaration := range file.Decls {
				if fn, ok := declaration.(*ast.FuncDecl); ok {
					symbolID := a.symbolID(path, fn.Name.Name, fn.Pos())
					if _, included := c.symbols[symbolID]; !included {
						continue
					}
					callable := c.newCallable(p, fn, symbolID)
					c.callables[symbolID] = callable
					c.symbolKeys[symbolID] = callable.Key
					if object, ok := p.info.Defs[fn.Name].(*types.Func); ok {
						c.symbolKeys[operationFunctionKey(object)] = callable.Key
					}
				}
			}
		}
	}
	for _, path := range paths {
		p := a.packages[path]
		for _, file := range p.ast {
			fileID := a.fileIDs[a.fset.PositionFor(file.Pos(), false).Filename]
			if !c.includedFiles[fileID] {
				continue
			}
			for _, declaration := range file.Decls {
				switch declaration := declaration.(type) {
				case *ast.FuncDecl:
					symbolID := a.symbolID(path, declaration.Name.Name, declaration.Pos())
					callable, ok := c.callables[symbolID]
					if !ok || declaration.Body == nil {
						continue
					}
					c.sources.Callables = append(c.sources.Callables, callable)
					c.walk(p, callable.Key, declaration.Body, callable)
				case *ast.GenDecl:
					for _, spec := range declaration.Specs {
						valueSpec, ok := spec.(*ast.ValueSpec)
						if !ok || len(valueSpec.Values) == 0 {
							continue
						}
						anchor := c.anchor(a.location(valueSpec.Pos(), valueSpec.End()))
						key := stableID("initializer", fmt.Sprintf("%s:%s:%d", path, fileID, anchor.Location.Offset))
						name := "package initializer"
						callable := OperationCallable{
							Key: key, Package: path, Name: name, Kind: "initializer", Anchor: anchor,
							Parameters: []OperationSlot{}, Results: []OperationSlot{},
						}
						c.sources.Callables = append(c.sources.Callables, callable)
						c.walk(p, key, valueSpec, callable)
					}
				}
			}
		}
	}
	sort.Slice(c.sources.Callables, func(i, j int) bool { return c.sources.Callables[i].Key < c.sources.Callables[j].Key })
	sort.Slice(c.sources.Calls, func(i, j int) bool {
		x, y := c.sources.Calls[i], c.sources.Calls[j]
		if x.Anchor.Location.FileID != y.Anchor.Location.FileID {
			return x.Anchor.Location.FileID < y.Anchor.Location.FileID
		}
		return x.Anchor.Location.Offset < y.Anchor.Location.Offset
	})
	sort.Slice(c.sources.Returns, func(i, j int) bool {
		x, y := c.sources.Returns[i], c.sources.Returns[j]
		if x.Anchor.Location.FileID != y.Anchor.Location.FileID {
			return x.Anchor.Location.FileID < y.Anchor.Location.FileID
		}
		if x.Anchor.Location.Offset != y.Anchor.Location.Offset {
			return x.Anchor.Location.Offset < y.Anchor.Location.Offset
		}
		return x.Position < y.Position
	})
	sort.Slice(c.sources.Bindings, func(i, j int) bool {
		x, y := c.sources.Bindings[i], c.sources.Bindings[j]
		if x.Anchor.Location.FileID != y.Anchor.Location.FileID {
			return x.Anchor.Location.FileID < y.Anchor.Location.FileID
		}
		return x.Anchor.Location.Offset < y.Anchor.Location.Offset
	})
	sort.Slice(c.sources.FieldStores, func(i, j int) bool {
		x, y := c.sources.FieldStores[i], c.sources.FieldStores[j]
		if x.Anchor.Location.FileID != y.Anchor.Location.FileID {
			return x.Anchor.Location.FileID < y.Anchor.Location.FileID
		}
		return x.Anchor.Location.Offset < y.Anchor.Location.Offset
	})
	r.OperationSources = c.sources
	return nil
}

func (c *operationSourceCollector) newCallable(p *parsedPackage, node ast.Node, symbolID string) OperationCallable {
	symbol := c.symbols[symbolID]
	callable := OperationCallable{
		Key: "callable:" + symbolID, SymbolID: symbolID, Package: p.meta.ImportPath,
		Name: symbol.Name, Kind: symbol.Kind, Anchor: c.anchor(symbol.Location),
		Parameters: []OperationSlot{}, Results: []OperationSlot{},
	}
	var signature *types.Signature
	var receiver *ast.FieldList
	var parameters, results *ast.FieldList
	switch node := node.(type) {
	case *ast.FuncDecl:
		if object, ok := p.info.Defs[node.Name].(*types.Func); ok {
			callable.Key = operationFunctionKey(object)
			signature, _ = object.Type().(*types.Signature)
		}
		receiver, parameters, results = node.Recv, node.Type.Params, node.Type.Results
	case *ast.FuncLit:
		signature, _ = p.info.TypeOf(node).(*types.Signature)
		parameters, results = node.Type.Params, node.Type.Results
	}
	if signature != nil && signature.Recv() != nil {
		callable.Receiver = operationTypeString(signature.Recv().Type())
	}
	if receiver != nil && len(receiver.List) > 0 {
		field := receiver.List[0]
		var ident *ast.Ident
		if len(field.Names) > 0 {
			ident = field.Names[0]
		}
		value := c.slotValue(p, field, ident, signatureRecv(signature))
		callable.ReceiverValue = &value
	}
	if signature != nil {
		callable.Parameters = c.slots(p, parameters, signature.Params())
		callable.Results = c.slots(p, results, signature.Results())
	}
	return callable
}

func signatureRecv(signature *types.Signature) types.Type {
	if signature == nil || signature.Recv() == nil {
		return nil
	}
	return signature.Recv().Type()
}

func (c *operationSourceCollector) slots(p *parsedPackage, fields *ast.FieldList, tuple *types.Tuple) []OperationSlot {
	var slots []OperationSlot
	if tuple == nil {
		return slots
	}
	type slotNode struct {
		node  ast.Expr
		ident *ast.Ident
	}
	var nodes []slotNode
	if fields != nil {
		for _, field := range fields.List {
			if len(field.Names) == 0 {
				nodes = append(nodes, slotNode{node: field.Type})
				continue
			}
			for _, ident := range field.Names {
				nodes = append(nodes, slotNode{node: ident, ident: ident})
			}
		}
	}
	for position := 0; position < tuple.Len(); position++ {
		if position >= len(nodes) || nodes[position].node == nil {
			continue
		}
		var value SourceValue
		if nodes[position].ident != nil {
			value = c.sourceValue(p, nodes[position].ident, tuple.At(position).Type(), p.info.Defs[nodes[position].ident])
		} else {
			value = c.sourceValue(p, nodes[position].node, tuple.At(position).Type(), nil)
			value.Name = ""
		}
		slots = append(slots, OperationSlot{Position: position, Value: value})
	}
	return slots
}

func (c *operationSourceCollector) slotValue(p *parsedPackage, field *ast.Field, ident *ast.Ident, typ types.Type) SourceValue {
	if ident != nil {
		return c.sourceValue(p, ident, typ, p.info.Defs[ident])
	}
	return c.sourceValue(p, field.Type, typ, nil)
}

func (c *operationSourceCollector) walk(p *parsedPackage, owner string, root ast.Node, callable OperationCallable) {
	parents := []ast.Node{}
	ast.Inspect(root, func(node ast.Node) bool {
		if node == nil {
			if len(parents) > 0 {
				parents = parents[:len(parents)-1]
			}
			return true
		}
		if literal, ok := node.(*ast.FuncLit); ok && node != root {
			if key := c.literalKeys[literal]; key != "" {
				nested := c.callables[aLiteralSymbolID(c.a, literal)]
				c.sources.Callables = append(c.sources.Callables, nested)
				c.walk(p, key, literal.Body, nested)
			}
			return false
		}
		switch node := node.(type) {
		case *ast.CallExpr:
			c.collectCall(p, owner, node, parents)
		case *ast.AssignStmt:
			c.collectAssignment(p, owner, node.Lhs, node.Rhs, node, "assignment")
			c.collectFieldAssignments(p, owner, node)
		case *ast.ValueSpec:
			names := make([]ast.Expr, 0, len(node.Names))
			for _, name := range node.Names {
				names = append(names, name)
			}
			c.collectAssignment(p, owner, names, node.Values, node, "initializer")
		case *ast.ReturnStmt:
			c.collectReturn(p, callable, node)
		case *ast.CompositeLit:
			c.collectCompositeStores(p, owner, node)
		}
		parents = append(parents, node)
		return true
	})
}

func aLiteralSymbolID(a *analyzer, literal *ast.FuncLit) string { return a.literals[literal] }

func (c *operationSourceCollector) collectCall(p *parsedPackage, owner string, call *ast.CallExpr, parents []ast.Node) {
	loc := c.a.location(call.Pos(), call.End())
	callID := stableID("call", fmt.Sprintf("%s:%d:%d", loc.FileID, loc.Offset, loc.EndOffset))
	targetKey, targetName, targetStatus, reason, receiver := c.callTarget(p, call)
	arguments := c.callArguments(p, call)
	results := c.callResults(p, call, parents, loc)
	var resultTypes []string
	if typ := p.info.TypeOf(call.Fun); typ != nil {
		if signature, ok := types.Unalias(typ).Underlying().(*types.Signature); ok {
			for i := 0; i < signature.Results().Len(); i++ {
				resultTypes = append(resultTypes, operationTypeString(signature.Results().At(i).Type()))
			}
		}
	}
	var object types.Object
	switch fun := unwrap(call.Fun).(type) {
	case *ast.Ident:
		object = p.info.Uses[fun]
	case *ast.SelectorExpr:
		if selection := p.info.Selections[fun]; selection != nil {
			object = selection.Obj()
		} else {
			object = p.info.Uses[fun.Sel]
		}
	}
	targetPackage := ""
	if object != nil && object.Pkg() != nil {
		targetPackage = object.Pkg().Path()
	}
	c.sources.Calls = append(c.sources.Calls, OperationCall{
		ID: stableID("operation-call", fmt.Sprintf("%s:%s", owner, callID)), CallID: callID,
		CallerKey: owner, TargetKey: targetKey, TargetName: targetName, TargetStatus: targetStatus,
		Reason: reason, Anchor: c.anchor(loc), Receiver: receiver, Arguments: arguments, Results: results,
		TargetPackage: targetPackage, TargetOrigin: c.a.origins[targetPackage], ResultTypes: resultTypes,
	})
}

func (c *operationSourceCollector) callTarget(p *parsedPackage, call *ast.CallExpr) (key, name, status, reason string, receiver *SourceValue) {
	expression := unwrap(call.Fun)
	var object types.Object
	switch expression := expression.(type) {
	case *ast.FuncLit:
		key = c.literalKeys[expression]
		name = "function literal"
		if key != "" {
			return key, name, "local", "", nil
		}
	case *ast.Ident:
		object = p.info.Uses[expression]
	case *ast.SelectorExpr:
		if selection := p.info.Selections[expression]; selection != nil {
			object = selection.Obj()
			switch selection.Kind() {
			case types.MethodVal:
				value := c.sourceValue(p, expression.X, p.info.TypeOf(expression.X), nil)
				receiver = &value
			case types.FieldVal:
				key = operationFieldKey(selection.Recv(), selection.Obj().Name())
				value := c.sourceValue(p, expression.X, p.info.TypeOf(expression.X), nil)
				receiver = &value
				name = selection.Obj().Name()
				if key != "" {
					return key, name, "function-value", "stored function value; assignment provenance is separate evidence", receiver
				}
			}
		} else {
			object = p.info.Uses[expression.Sel]
		}
	}
	if object == nil {
		if key == "" && name == "" {
			name = c.a.text(call.Fun)
		}
		return key, name, "unresolved", "call target identity is unavailable", receiver
	}
	name = object.Name()
	switch object := object.(type) {
	case *types.Func:
		key = operationFunctionKey(object)
		if c.symbolKeys[key] != "" {
			return key, name, "local", "", receiver
		}
		if object.Pkg() != nil && c.a.packages[object.Pkg().Path()] != nil {
			return key, name, "out-of-scope", "declaration is not part of the included source snapshot", receiver
		}
		return key, name, "external", "declaration source is outside the analyzed module", receiver
	case *types.Var:
		if object.IsField() {
			if key == "" {
				key = operationFieldKey(p.info.TypeOf(call.Fun), object.Name())
			}
			if key != "" {
				return key, name, "function-value", "stored function value; assignment provenance is separate evidence", receiver
			}
		}
		return operationObjectKey(c.a, object), name, "function-value", "function value target depends on assignment provenance", receiver
	default:
		return operationObjectKey(c.a, object), name, "unresolved", "target is not a source-resolved function", receiver
	}
}

func (c *operationSourceCollector) callArguments(p *parsedPackage, call *ast.CallExpr) []OperationArgument {
	var signature *types.Signature
	if typ := p.info.TypeOf(call.Fun); typ != nil {
		signature, _ = types.Unalias(typ).Underlying().(*types.Signature)
	}
	var arguments []OperationArgument
	add := func(expressionPosition, parameterPosition int, expr ast.Expr, tuplePosition *int, valueType types.Type) {
		value := c.sourceValue(p, expr, valueType, nil)
		status, reason := "resolved", ""
		if signature == nil || parameterPosition < 0 || parameterPosition >= signature.Params().Len() {
			status, reason = "unresolved", "callee parameter position is unavailable"
		} else if operationTypeString(valueType) == "<unknown>" || operationTypeString(signature.Params().At(parameterPosition).Type()) == "<unknown>" {
			status, reason = "unresolved", "source or parameter type information is incomplete"
		}
		arguments = append(arguments, OperationArgument{
			Position: expressionPosition, ParameterPosition: parameterPosition,
			TuplePosition: tuplePosition, Value: value, Status: status, Reason: reason,
		})
	}
	for expressionPosition, expr := range call.Args {
		if len(call.Args) == 1 {
			if tuple, ok := p.info.TypeOf(expr).(*types.Tuple); ok {
				for tuplePosition := 0; tuplePosition < tuple.Len(); tuplePosition++ {
					parameterPosition := c.parameterPosition(signature, tuplePosition)
					index := tuplePosition
					add(expressionPosition, parameterPosition, expr, &index, tuple.At(tuplePosition).Type())
				}
				continue
			}
		}
		parameterPosition := c.parameterPosition(signature, expressionPosition)
		add(expressionPosition, parameterPosition, expr, nil, p.info.TypeOf(expr))
	}
	return arguments
}

func (c *operationSourceCollector) parameterPosition(signature *types.Signature, argument int) int {
	if signature == nil || signature.Params().Len() == 0 {
		return -1
	}
	count := signature.Params().Len()
	if argument < count {
		return argument
	}
	if signature.Variadic() {
		return count - 1
	}
	return -1
}

func (c *operationSourceCollector) callResults(p *parsedPackage, call *ast.CallExpr, parents []ast.Node, callLoc Location) []OperationResultBinding {
	count := 0
	if typ := p.info.TypeOf(call); typ != nil {
		if tuple, ok := typ.(*types.Tuple); ok {
			count = tuple.Len()
		} else if _, ok := typ.Underlying().(*types.Signature); !ok {
			count = 1
		}
	}
	context, destinations, anchor := c.resultContext(p, call, parents, callLoc, count)
	if count == 0 && len(destinations) > 0 {
		count = len(destinations)
	}
	if count == 0 {
		return []OperationResultBinding{}
	}
	results := make([]OperationResultBinding, 0, count)
	for position := 0; position < count; position++ {
		result := OperationResultBinding{Position: position, Status: "unresolved", Anchor: c.anchor(anchor)}
		if context == "discarded" {
			result.Status, result.Reason = "discarded", "call result is not bound by the source"
		} else if context == "returned" {
			result.Status, result.Reason = "returned", "call result contributes to the enclosing return expression"
		} else if context == "assignment" {
			if position < len(destinations) {
				if ident, ok := destinations[position].(*ast.Ident); ok && ident.Name == "_" {
					result.Status, result.Reason = "discarded", "blank identifier discards this result"
				} else {
					value := c.sourceValue(p, destinations[position], p.info.TypeOf(destinations[position]), nil)
					result.Destination = &value
					result.Status = "resolved"
				}
			} else {
				result.Reason = "assignment arity does not establish a result destination"
			}
		} else {
			result.Reason = "call result is used in a larger expression; destination is not captured"
		}
		results = append(results, result)
	}
	return results
}

func (c *operationSourceCollector) resultContext(p *parsedPackage, call *ast.CallExpr, parents []ast.Node, callLoc Location, resultCount int) (string, []ast.Expr, Location) {
	current := ast.Node(call)
	for i := len(parents) - 1; i >= 0; i-- {
		parent := parents[i]
		switch node := parent.(type) {
		case *ast.ParenExpr:
			if node.X != current {
				return "", nil, callLoc
			}
			current = node
		case *ast.AssignStmt:
			if index := expressionIndex(node.Rhs, current); index >= 0 {
				if len(node.Rhs) == 1 && resultCount > 1 {
					return "assignment", node.Lhs, c.a.location(node.Pos(), node.End())
				}
				if len(node.Lhs) == len(node.Rhs) && index < len(node.Lhs) {
					return "assignment", []ast.Expr{node.Lhs[index]}, c.a.location(node.Pos(), node.End())
				}
			}
			return "", nil, callLoc
		case *ast.ValueSpec:
			if index := expressionIndex(node.Values, current); index >= 0 {
				if len(node.Values) == 1 && resultCount > 1 {
					destinations := make([]ast.Expr, 0, len(node.Names))
					for _, ident := range node.Names {
						destinations = append(destinations, ident)
					}
					return "assignment", destinations, c.a.location(node.Pos(), node.End())
				}
				if len(node.Names) == len(node.Values) && index < len(node.Names) {
					return "assignment", []ast.Expr{node.Names[index]}, c.a.location(node.Pos(), node.End())
				}
			}
			return "", nil, callLoc
		case *ast.ReturnStmt:
			if expressionIndex(node.Results, current) >= 0 {
				return "returned", nil, c.a.location(node.Pos(), node.End())
			}
			return "", nil, callLoc
		case *ast.ExprStmt:
			if node.X == current {
				return "discarded", nil, c.a.location(node.Pos(), node.End())
			}
			return "", nil, callLoc
		default:
			return "", nil, callLoc
		}
	}
	return "", nil, callLoc
}

func expressionIndex(expressions []ast.Expr, node ast.Node) int {
	for i, expression := range expressions {
		if expression == node {
			return i
		}
	}
	return -1
}

func (c *operationSourceCollector) collectReturn(p *parsedPackage, callable OperationCallable, statement *ast.ReturnStmt) {
	if len(statement.Results) == 0 {
		for _, slot := range callable.Results {
			result := OperationReturn{CallableKey: callable.Key, Position: slot.Position, Anchor: c.anchor(c.a.location(statement.Pos(), statement.End())), Status: "unresolved"}
			if slot.Value.Name != "" {
				value := slot.Value
				result.Value, result.Status = &value, "resolved"
			} else {
				result.Reason = "bare return has no named result binding"
			}
			c.sources.Returns = append(c.sources.Returns, result)
		}
		return
	}
	for expressionPosition, expr := range statement.Results {
		if len(statement.Results) == 1 {
			if tuple, ok := p.info.TypeOf(expr).(*types.Tuple); ok {
				for position := 0; position < tuple.Len(); position++ {
					value := c.sourceValue(p, expr, tuple.At(position).Type(), nil)
					c.sources.Returns = append(c.sources.Returns, OperationReturn{
						CallableKey: callable.Key, Position: position, Value: &value,
						Anchor: c.anchor(c.a.location(statement.Pos(), statement.End())), Status: "resolved",
					})
				}
				continue
			}
		}
		position := expressionPosition
		value := c.sourceValue(p, expr, p.info.TypeOf(expr), nil)
		status, reason := "resolved", ""
		if position >= len(callable.Results) || operationTypeString(p.info.TypeOf(expr)) == "<unknown>" {
			status, reason = "unresolved", "result position or expression type information is incomplete"
		}
		c.sources.Returns = append(c.sources.Returns, OperationReturn{
			CallableKey: callable.Key, Position: position, Value: &value,
			Anchor: c.anchor(c.a.location(statement.Pos(), statement.End())), Status: status, Reason: reason,
		})
	}
}

func (c *operationSourceCollector) collectAssignment(p *parsedPackage, owner string, lhs, rhs []ast.Expr, statement ast.Node, kind string) {
	if len(lhs) == 0 || len(rhs) == 0 {
		return
	}
	anchor := c.anchor(c.a.location(statement.Pos(), statement.End()))
	if len(rhs) == 1 {
		if tuple, ok := p.info.TypeOf(rhs[0]).(*types.Tuple); ok {
			for position, destination := range lhs {
				var source *SourceValue
				status, reason := "unresolved", "tuple arity does not establish a positional assignment"
				var tuplePosition *int
				if position < tuple.Len() && len(lhs) == tuple.Len() {
					index := position
					tuplePosition = &index
					value := c.sourceValue(p, rhs[0], tuple.At(position).Type(), nil)
					source, status, reason = &value, "resolved", ""
				}
				c.addBinding(p, owner, destination, rhs[0], source, tuplePosition, kind, anchor, status, reason)
			}
			return
		}
	}
	if len(lhs) == len(rhs) {
		for position, destination := range lhs {
			source := c.sourceValue(p, rhs[position], p.info.TypeOf(rhs[position]), nil)
			c.addBinding(p, owner, destination, rhs[position], &source, nil, kind, anchor, "resolved", "")
		}
		return
	}
	for _, destination := range lhs {
		c.addBinding(p, owner, destination, nil, nil, nil, kind, anchor, "unresolved", "assignment arity does not establish a positional mapping")
	}
}

func (c *operationSourceCollector) addBinding(p *parsedPackage, owner string, destination, sourceExpr ast.Expr, source *SourceValue, tuplePosition *int, kind string, anchor OperationAnchor, status, reason string) {
	var target *SourceValue
	if ident, ok := destination.(*ast.Ident); !ok || ident.Name != "_" {
		value := c.sourceValue(p, destination, p.info.TypeOf(destination), nil)
		target = &value
	} else if status == "resolved" {
		status, reason = "discarded", "blank identifier discards this value"
	}
	binding := OperationBinding{
		OwnerKey: owner, Source: source, Destination: target, TuplePosition: tuplePosition,
		Kind: kind, Anchor: anchor, Status: status, Reason: reason,
	}
	if source != nil {
		binding.SourceKey = c.expressionKey(p, sourceExpr, owner)
	}
	binding.DestinationKey = c.destinationKey(p, destination)
	c.sources.Bindings = append(c.sources.Bindings, binding)
}

func (c *operationSourceCollector) expressionKey(p *parsedPackage, expression ast.Expr, owner string) string {
	if expression == nil {
		return ""
	}
	switch expression := expression.(type) {
	case *ast.Ident:
		return operationObjectKey(c.a, p.info.Uses[expression])
	case *ast.FuncLit:
		return c.literalKeys[expression]
	case *ast.SelectorExpr:
		if selection := p.info.Selections[expression]; selection != nil && selection.Kind() == types.FieldVal {
			return operationFieldKey(selection.Recv(), selection.Obj().Name())
		}
		return operationObjectKey(c.a, p.info.Uses[expression.Sel])
	default:
		return ""
	}
}

func (c *operationSourceCollector) destinationKey(p *parsedPackage, expression ast.Expr) string {
	switch expression := expression.(type) {
	case *ast.Ident:
		return operationObjectKey(c.a, p.info.Defs[expression])
	case *ast.SelectorExpr:
		if selection := p.info.Selections[expression]; selection != nil && selection.Kind() == types.FieldVal {
			return operationFieldKey(selection.Recv(), selection.Obj().Name())
		}
		return operationObjectKey(c.a, p.info.Uses[expression.Sel])
	default:
		return ""
	}
}

func (c *operationSourceCollector) collectFieldAssignments(p *parsedPackage, owner string, statement *ast.AssignStmt) {
	for index, destination := range statement.Lhs {
		base, kind := destination, "assignment"
		for {
			element, ok := base.(*ast.IndexExpr)
			if !ok {
				break
			}
			base, kind = element.X, "element-assignment"
		}
		selector, ok := base.(*ast.SelectorExpr)
		if !ok {
			continue
		}
		selection := p.info.Selections[selector]
		if selection == nil || selection.Kind() != types.FieldVal {
			continue
		}
		var value *SourceValue
		status, reason := "unresolved", "assignment source does not establish a field value"
		if len(statement.Rhs) == len(statement.Lhs) {
			item := c.sourceValue(p, statement.Rhs[index], p.info.TypeOf(statement.Rhs[index]), nil)
			value, status, reason = &item, "resolved", ""
		} else if len(statement.Rhs) == 1 {
			if tuple, ok := p.info.TypeOf(statement.Rhs[0]).(*types.Tuple); ok && len(statement.Lhs) == tuple.Len() {
				item := c.sourceValue(p, statement.Rhs[0], tuple.At(index).Type(), nil)
				value, status, reason = &item, "resolved", ""
			}
		}
		receiver := c.sourceValue(p, selector.X, p.info.TypeOf(selector.X), nil)
		c.sources.FieldStores = append(c.sources.FieldStores, OperationFieldStore{
			OwnerKey: owner, Receiver: &receiver, FieldKey: operationFieldKey(selection.Recv(), selection.Obj().Name()),
			FieldName: selection.Obj().Name(), Value: value, Kind: kind,
			Anchor: c.anchor(c.a.location(destination.Pos(), destination.End())), Status: status, Reason: reason,
		})
	}
}

func (c *operationSourceCollector) collectCompositeStores(p *parsedPackage, owner string, literal *ast.CompositeLit) {
	typeValue := p.info.TypeOf(literal)
	structure, ok := underlyingStruct(typeValue)
	if !ok {
		return
	}
	for index, element := range literal.Elts {
		var fieldName string
		var valueExpr ast.Expr
		var fieldPos int
		if keyed, ok := element.(*ast.KeyValueExpr); ok {
			valueExpr = keyed.Value
			fieldName = ""
			if ident, ok := keyed.Key.(*ast.Ident); ok {
				fieldName = ident.Name
			}
			for position := 0; position < structure.NumFields(); position++ {
				if structure.Field(position).Name() == fieldName {
					fieldPos = position
					break
				}
			}
		} else {
			valueExpr, _ = element.(ast.Expr)
			fieldPos = index
			if fieldPos < structure.NumFields() {
				fieldName = structure.Field(fieldPos).Name()
			}
		}
		if valueExpr == nil || fieldName == "" {
			continue
		}
		receiver := c.sourceValue(p, literal, typeValue, nil)
		value := c.sourceValue(p, valueExpr, p.info.TypeOf(valueExpr), nil)
		status, reason := "resolved", ""
		if fieldPos >= structure.NumFields() {
			status, reason = "unresolved", "composite literal field position is unavailable"
		}
		c.sources.FieldStores = append(c.sources.FieldStores, OperationFieldStore{
			OwnerKey: owner, Receiver: &receiver, FieldKey: operationFieldKey(typeValue, fieldName),
			FieldName: fieldName, Value: &value, Kind: "composite-literal",
			Anchor: c.anchor(c.a.location(element.Pos(), element.End())), Status: status, Reason: reason,
		})
	}
}

func (c *operationSourceCollector) sourceValue(p *parsedPackage, node ast.Node, override types.Type, object types.Object) SourceValue {
	loc := c.a.location(node.Pos(), node.End())
	expression := ""
	if source := c.sourceByFile[loc.FileID]; loc.Offset >= 0 && loc.EndOffset >= loc.Offset && loc.EndOffset <= len(source) {
		expression = source[loc.Offset:loc.EndOffset]
	}
	name := ""
	if ident, ok := node.(*ast.Ident); ok && ident.Name != "_" {
		name = ident.Name
	}
	if object == nil {
		switch node := node.(type) {
		case *ast.Ident:
			object = p.info.Uses[node]
			if object == nil {
				object = p.info.Defs[node]
			}
		case *ast.SelectorExpr:
			if selection := p.info.Selections[node]; selection != nil {
				object = selection.Obj()
			} else {
				object = p.info.Uses[node.Sel]
			}
		}
	}
	if override == nil {
		if expr, ok := node.(ast.Expr); ok {
			override = p.info.TypeOf(expr)
		}
	}
	objectKey := operationObjectKey(c.a, object)
	valueID := stableID("source-value", fmt.Sprintf("%s:%d:%d:%s", loc.FileID, loc.Offset, loc.EndOffset, objectKey))
	return SourceValue{
		ID: valueID, ObjectKey: objectKey, Expression: expression, Name: name, Type: operationTypeString(override),
		Anchor: c.anchor(loc),
	}
}

func (c *operationSourceCollector) anchor(location Location) OperationAnchor {
	return OperationAnchor{Module: c.a.report.Module, Location: location}
}

func operationTypeString(typ types.Type) string {
	if typ == nil {
		return "<unknown>"
	}
	if basic, ok := typ.(*types.Basic); ok && basic.Kind() == types.Invalid {
		return "<unknown>"
	}
	text := types.TypeString(typ, func(pkg *types.Package) string { return pkg.Path() })
	if text == "invalid type" {
		return "<unknown>"
	}
	return text
}

func operationFunctionKey(object *types.Func) string {
	if object == nil {
		return ""
	}
	origin := object.Origin()
	return "func:" + types.ObjectString(origin, func(pkg *types.Package) string { return pkg.Path() })
}

func operationObjectKey(a *analyzer, object types.Object) string {
	if object == nil {
		return ""
	}
	if function, ok := object.(*types.Func); ok {
		return operationFunctionKey(function)
	}
	if variable, ok := object.(*types.Var); ok && variable.IsField() {
		return "field-object:" + types.ObjectString(variable, func(pkg *types.Package) string { return pkg.Path() })
	}
	pkg := ""
	if object.Pkg() != nil {
		pkg = object.Pkg().Path()
	}
	if object.Parent() != nil && object.Pkg() != nil && object.Parent() == object.Pkg().Scope() {
		return fmt.Sprintf("object:%s:%s", pkg, object.Name())
	}
	position := a.fset.PositionFor(object.Pos(), false)
	fileID := a.fileIDs[filepath.Clean(position.Filename)]
	return fmt.Sprintf("object:%s:%s:%d:%s", pkg, fileID, position.Offset, object.Name())
}

func operationFieldKey(receiver types.Type, name string) string {
	if receiver == nil || name == "" {
		return ""
	}
	receiver = types.Unalias(receiver)
	for {
		pointer, ok := receiver.(*types.Pointer)
		if !ok {
			break
		}
		receiver = types.Unalias(pointer.Elem())
	}
	if named, ok := receiver.(*types.Named); ok {
		object := named.Origin().Obj()
		if object.Pkg() != nil {
			return fmt.Sprintf("field:%s.%s.%s", object.Pkg().Path(), object.Name(), name)
		}
		return "field:" + object.Name() + "." + name
	}
	return fmt.Sprintf("field:%s.%s", operationTypeString(receiver), name)
}

func underlyingStruct(typ types.Type) (*types.Struct, bool) {
	if typ == nil {
		return nil, false
	}
	typ = types.Unalias(typ)
	for {
		pointer, ok := typ.(*types.Pointer)
		if !ok {
			break
		}
		typ = types.Unalias(pointer.Elem())
	}
	structure, ok := typ.Underlying().(*types.Struct)
	return structure, ok
}

func (c *operationSourceCollector) stampAnchor(anchor *OperationAnchor) {
	if anchor == nil {
		return
	}
	anchor.Module = c.a.report.Module
	anchor.SourceSHA256 = c.a.report.SourceSHA256
}

func stampOperationSourceAnchors(report *Report) {
	sources := report.OperationSources
	if sources == nil {
		return
	}
	stamp := func(anchor *OperationAnchor) {
		if anchor != nil {
			anchor.Module = report.Module
			anchor.SourceSHA256 = report.SourceSHA256
		}
	}
	value := func(source *SourceValue) {
		if source != nil {
			stamp(&source.Anchor)
		}
	}
	for i := range sources.Callables {
		callable := &sources.Callables[i]
		stamp(&callable.Anchor)
		value(callable.ReceiverValue)
		for j := range callable.Parameters {
			value(&callable.Parameters[j].Value)
		}
		for j := range callable.Results {
			value(&callable.Results[j].Value)
		}
	}
	for i := range sources.Calls {
		call := &sources.Calls[i]
		stamp(&call.Anchor)
		value(call.Receiver)
		for j := range call.Arguments {
			value(&call.Arguments[j].Value)
		}
		for j := range call.Results {
			stamp(&call.Results[j].Anchor)
			value(call.Results[j].Destination)
		}
	}
	for i := range sources.Returns {
		stamp(&sources.Returns[i].Anchor)
		value(sources.Returns[i].Value)
	}
	for i := range sources.Bindings {
		binding := &sources.Bindings[i]
		stamp(&binding.Anchor)
		value(binding.Source)
		value(binding.Destination)
	}
	for i := range sources.FieldStores {
		field := &sources.FieldStores[i]
		stamp(&field.Anchor)
		value(field.Receiver)
		value(field.Value)
	}
}
