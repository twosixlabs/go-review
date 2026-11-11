package review

import (
	"path"
	"sort"
	"strings"
)

// PrepareSystemFlow rebuilds the architectural projection without executing
// target code. Retained execution evidence must match the source snapshot.
func PrepareSystemFlow(r *Report) {
	if r == nil {
		return
	}
	if r.UnitEvidence != nil && r.UnitEvidence.SourceSHA256 != r.SourceSHA256 {
		r.Coverage = nil
		message := "Selected unit evidence belongs to a different source snapshot; its coverage is not joined to this architecture."
		for i := range r.UnitEvidence.Runs {
			r.UnitEvidence.Runs[i].Status = "stale"
			if runtime := r.UnitEvidence.Runs[i].Runtime; runtime != nil {
				runtime.Status, runtime.Error, runtime.Observations = "stale", message, []RuntimeObservation{}
			}
		}
		found := false
		for _, warning := range r.Warnings {
			found = found || warning == message
		}
		if !found {
			r.Warnings = append(r.Warnings, message)
		}
	}
	if r.UnitEvidence != nil {
		for i := range r.UnitEvidence.Runs {
			if runtime := r.UnitEvidence.Runs[i].Runtime; runtime != nil && runtime.SourceSHA256 != r.SourceSHA256 {
				runtime.Status, runtime.Error, runtime.Observations = "stale", "Runtime source identity does not match this snapshot.", []RuntimeObservation{}
			}
		}
	}
	r.SystemFlow = BuildSystemFlow(r)
}

// BuildSystemFlow groups production implementations by receiver ownership and
// contracts. It deliberately does not infer a runtime sequence from call order.
func BuildSystemFlow(r *Report) *SystemFlow {
	flow := &SystemFlow{
		Version: "system-flow/1", SourceSHA256: r.SourceSHA256,
		Components: []FlowComponent{}, Operations: []FlowOperation{}, Exchanges: []FlowExchange{},
		Limitations: []string{
			"Component grouping is inferred from source ownership and contracts, not business-role certification.",
			"Typed exchanges describe information contracts, not proof of object identity or complete data lineage.",
			"Unresolved interface and callback implementations remain explicit contract boundaries.",
		},
	}
	if r.OperationSources == nil {
		flow.Limitations = append(flow.Limitations, "Source-named operation facts are unavailable in this snapshot.")
		return flow
	}
	testOnly, productionLimitations := flowTestOnlyPackages(r)
	flow.Limitations = append(flow.Limitations, productionLimitations...)
	files := map[string]File{}
	symbols := map[string]Symbol{}
	for _, file := range r.Files {
		files[file.ID] = file
	}
	for _, symbol := range r.Symbols {
		if file, ok := files[symbol.FileID]; ok && !strings.HasSuffix(file.Path, "_test.go") && !testOnly[file.Package] {
			symbols[symbol.ID] = symbol
		}
	}
	components := map[string]*FlowComponent{}
	ensure := func(key, name, kind, pkg string, loc Location) string {
		id := stableID("component", key)
		if components[id] == nil {
			components[id] = &FlowComponent{ID: id, Name: name, Kind: kind, Package: pkg, Members: []string{}, Sources: []Location{}}
		}
		if loc.FileID != "" {
			c := components[id]
			found := false
			for _, existing := range c.Sources {
				found = found || existing == loc
			}
			if !found {
				c.Sources = append(c.Sources, loc)
			}
		}
		return id
	}
	types := map[string]Symbol{}
	for _, s := range symbols {
		if s.Kind == "type" {
			types[s.Package+"."+s.Name] = s
		}
	}
	byKey := map[string]OperationCallable{}
	owners := map[string]string{}
	for _, c := range r.OperationSources.Callables {
		if _, ok := symbols[c.SymbolID]; !ok {
			continue
		}
		byKey[c.Key] = c
		if c.Receiver == "" {
			continue
		}
		typ := strings.TrimPrefix(c.Receiver, "*")
		_, name := flowTypeName(typ)
		id := ensure(typ, name, "component", c.Package, c.Anchor.Location)
		owners[c.Key] = id
		if s, ok := types[typ]; ok {
			components[id].Doc = s.Doc
		}
	}
	// Constructors returning an owned receiver type belong to that component,
	// not to a separate constructor/helper component.
	for _, c := range r.OperationSources.Callables {
		if _, ok := byKey[c.Key]; !ok || owners[c.Key] != "" {
			continue
		}
		if strings.HasPrefix(c.Name, "New") && len(c.Results) > 0 {
			typ := strings.TrimPrefix(c.Results[0].Value.Type, "*")
			if components[stableID("component", typ)] != nil {
				owners[c.Key] = stableID("component", typ)
			}
		}
		if owners[c.Key] == "" {
			owners[c.Key] = ensure("package:"+c.Package, path.Base(c.Package), "module", c.Package, c.Anchor.Location)
		}
	}
	flowHelperOwners(r, byKey, owners)
	memoryStores, storageLimitations := flowMemoryStores(r, byKey)
	flow.Limitations = append(flow.Limitations, storageLimitations...)
	for typ := range memoryStores {
		if c := components[stableID("component", typ)]; c != nil {
			c.Kind = "memory-store"
		}
	}
	for _, c := range r.OperationSources.Callables {
		id := owners[c.Key]
		if id == "" {
			continue
		}
		components[id].Members = append(components[id].Members, c.SymbolID)
		flow.Operations = append(flow.Operations, FlowOperation{
			ID: c.Key, Name: c.Name, ComponentID: id, SymbolID: c.SymbolID, Location: c.Anchor.Location,
		})
	}
	calls := map[string]Call{}
	for _, call := range r.Calls {
		calls[call.ID] = call
	}
	wiredTargets := flowWiredTargets(r, byKey, owners)
	for id, target := range flowCallbackTargets(r, byKey, owners) {
		wiredTargets[id] = target
	}
	exchanges := map[string]*FlowExchange{}
	for _, call := range r.OperationSources.Calls {
		from := owners[call.CallerKey]
		if from == "" {
			continue
		}
		ordinary, hasCall := calls[call.CallID]
		if hasCall && (ordinary.Kind == "builtin" || ordinary.Kind == "conversion") {
			continue
		}
		to, resolution, reason := owners[call.TargetKey], "resolved", ""
		if to == "" && wiredTargets[call.ID] != "" {
			to, resolution, reason = wiredTargets[call.ID], "inferred", "Production arguments, bindings and field stores identify this target; this is static wiring, not runtime dispatch proof."
		}
		if to == "" {
			// A declared receiver contract is useful even when its implementation
			// cannot be established. Test doubles never supply production identity.
			if call.Receiver != nil && call.Receiver.Type != "<unknown>" {
				typ := strings.TrimPrefix(call.Receiver.Type, "*")
				pkg, name := flowTypeName(typ)
				boundaryKind := "contract"
				if typ == "database/sql.DB" {
					boundaryKind = "durable-store"
				}
				to = ensure("contract:"+typ, name, boundaryKind, pkg, call.Anchor.Location)
			} else if call.TargetStatus == "external" && call.TargetKey != "" {
				key, name := call.TargetKey, call.TargetName
				if call.TargetPackage != "" {
					key, name = call.TargetPackage, path.Base(call.TargetPackage)
				}
				to = ensure("external:"+key, name, "external", call.TargetPackage, call.Anchor.Location)
			} else if call.TargetStatus == "function-value" || call.TargetStatus == "out-of-scope" {
				to = ensure("contract:"+call.TargetKey, call.TargetName, "contract", "", call.Anchor.Location)
			} else {
				to = ensure("unknown:"+call.CallerKey+":"+call.TargetName, call.TargetName, "contract", "", call.Anchor.Location)
			}
			resolution, reason = "unresolved", call.Reason
		}
		if from == to {
			continue
		}
		kind := "exchange"
		if hasCall && ordinary.Invocation == "goroutine" {
			kind = "asynchronous"
		}
		if hasCall && ordinary.Invocation == "deferred" {
			kind = "deferred"
		}
		information := []FlowInformation{}
		for _, arg := range call.Arguments {
			name := arg.Value.Name
			if name == "" {
				name = arg.Value.Expression
			}
			information = append(information, FlowInformation{Name: name, Type: arg.Value.Type, Direction: "input"})
		}
		if target, ok := byKey[call.TargetKey]; ok {
			for _, result := range target.Results {
				information = append(information, FlowInformation{Name: result.Value.Name, Type: result.Value.Type, Direction: "output"})
			}
		} else if len(call.ResultTypes) > 0 {
			for position, typ := range call.ResultTypes {
				name := ""
				for _, result := range call.Results {
					if result.Position == position && result.Destination != nil {
						name = result.Destination.Name
						break
					}
				}
				information = append(information, FlowInformation{Name: name, Type: typ, Direction: "output"})
			}
		} else {
			for _, result := range call.Results {
				if result.Destination != nil {
					information = append(information, FlowInformation{Name: result.Destination.Name, Type: result.Destination.Type, Direction: "output"})
				}
			}
		}
		// Different contracts between the same components remain distinct.
		key := from + "|" + to + "|" + kind + "|" + call.TargetKey
		if exchanges[key] == nil {
			exchanges[key] = &FlowExchange{ID: stableID("exchange", key), From: from, To: to, Kind: kind, Resolution: resolution, Reason: reason,
				Information: information, Sources: []Location{}, CallIDs: []string{}}
		}
		e := exchanges[key]
		e.Sources = append(e.Sources, call.Anchor.Location)
		e.CallIDs = append(e.CallIDs, call.CallID)
	}
	ingressSeen := map[string]bool{}
	for _, endpoint := range r.HTTPEndpoints {
		if file, found := files[endpoint.Location.FileID]; found && testOnly[file.Package] {
			continue
		}
		if ingressSeen[endpoint.ID] {
			continue
		}
		ingressSeen[endpoint.ID] = true
		target := ""
		resolution, reason := "resolved", ""
		for _, op := range flow.Operations {
			if op.SymbolID == endpoint.HandlerID {
				target = op.ComponentID
				break
			}
		}
		if target == "" {
			target = ensure("handler:"+endpoint.Handler, endpoint.Handler, "contract", "", endpoint.Location)
			resolution, reason = "unresolved", endpoint.Reason
			if reason == "" {
				reason = "The HTTP registration is known; its production handler implementation is unresolved."
			}
		}
		id := ensure("ingress:http", "HTTP clients", "actor", "", endpoint.Location)
		flow.Exchanges = append(flow.Exchanges, FlowExchange{ID: stableID("ingress", endpoint.ID), From: id, To: target, Kind: "ingress", Resolution: resolution, Reason: reason,
			Information: []FlowInformation{{Name: endpoint.Pattern, Type: "HTTP request", Direction: "input"}, {Type: "HTTP response", Direction: "output"}}, Sources: []Location{endpoint.Location}, CallIDs: []string{}})
	}
	for _, c := range components {
		if (c.Kind == "component" || c.Kind == "module") && len(c.Members) == 0 {
			continue
		}
		sort.Strings(c.Members)
		flow.Components = append(flow.Components, *c)
	}
	for _, e := range exchanges {
		flow.Exchanges = append(flow.Exchanges, *e)
	}
	sort.Slice(flow.Components, func(i, j int) bool { return flow.Components[i].ID < flow.Components[j].ID })
	sort.Slice(flow.Operations, func(i, j int) bool { return flow.Operations[i].ID < flow.Operations[j].ID })
	sort.Slice(flow.Exchanges, func(i, j int) bool { return flow.Exchanges[i].ID < flow.Exchanges[j].ID })
	ApplyFlowCoverage(r, flow)
	return flow
}

func flowTypeName(typ string) (string, string) {
	if i := strings.LastIndex(typ, "."); i >= 0 {
		return typ[:i], typ[i+1:]
	}
	return "", typ
}
