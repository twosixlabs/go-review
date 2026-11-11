package review

import "strings"

// flowWiredTargets follows concrete argument types into named parameters and
// field stores. It is intentionally not an alias analysis: a uniquely supported
// constructor/field candidate is inferred, and ambiguous contracts stay open.
func flowWiredTargets(r *Report, callables map[string]OperationCallable, owners map[string]string) map[string]string {
	concrete := map[string]bool{}
	for _, c := range callables {
		if c.Receiver != "" {
			concrete[strings.TrimPrefix(c.Receiver, "*")] = true
		}
	}
	values := map[string]map[string]bool{}
	add := func(key, typ string) bool {
		typ = strings.TrimPrefix(typ, "*")
		if key == "" || !concrete[typ] {
			return false
		}
		if values[key] == nil {
			values[key] = map[string]bool{}
		}
		if values[key][typ] {
			return false
		}
		values[key][typ] = true
		return true
	}
	// This finite, monotone propagation handles a constructor forwarding a
	// parameter to another constructor without guessing unobserved values.
	for changed := true; changed; {
		changed = false
		for _, call := range r.OperationSources.Calls {
			target, ok := callables[call.TargetKey]
			if !ok {
				continue
			}
			for _, arg := range call.Arguments {
				for _, parameter := range target.Parameters {
					if parameter.Position != arg.ParameterPosition {
						continue
					}
					if add(parameter.Value.ObjectKey, arg.Value.Type) {
						changed = true
					}
					for typ := range values[arg.Value.ObjectKey] {
						if add(parameter.Value.ObjectKey, typ) {
							changed = true
						}
					}
				}
			}
		}
		for _, binding := range r.OperationSources.Bindings {
			if binding.Source != nil && add(binding.DestinationKey, binding.Source.Type) {
				changed = true
			}
			for typ := range values[binding.SourceKey] {
				if add(binding.DestinationKey, typ) {
					changed = true
				}
			}
		}
	}
	fields := map[string]map[string]bool{}
	for _, store := range r.OperationSources.FieldStores {
		if store.Receiver == nil || store.Value == nil || store.Kind == "element-assignment" {
			continue
		}
		key := strings.TrimPrefix(store.Receiver.Type, "*") + "." + store.FieldName
		if fields[key] == nil {
			fields[key] = map[string]bool{}
		}
		typ := strings.TrimPrefix(store.Value.Type, "*")
		if concrete[typ] {
			fields[key][typ] = true
		}
		for typ := range values[store.Value.ObjectKey] {
			fields[key][typ] = true
		}
	}
	result := map[string]string{}
	for _, call := range r.OperationSources.Calls {
		if call.Receiver == nil || owners[call.TargetKey] != "" {
			continue
		}
		owner := callables[call.CallerKey]
		if owner.Receiver == "" {
			continue
		}
		_, field := flowTypeName(call.Receiver.Expression)
		candidates := fields[strings.TrimPrefix(owner.Receiver, "*")+"."+field]
		targets := map[string]bool{}
		for typ := range candidates {
			for _, c := range callables {
				if strings.TrimPrefix(c.Receiver, "*") == typ && c.Name == call.TargetName {
					targets[owners[c.Key]] = true
				}
			}
		}
		if len(targets) == 1 {
			for target := range targets {
				if target != "" {
					result[call.ID] = target
				}
			}
		}
	}
	return result
}

func flowCallbackTargets(r *Report, callables map[string]OperationCallable, owners map[string]string) map[string]string {
	functions := map[string]map[string]bool{}
	for key := range callables {
		functions[key] = map[string]bool{key: true}
	}
	copyTargets := func(destination, source string) bool {
		if destination == "" || len(functions[source]) == 0 {
			return false
		}
		if functions[destination] == nil {
			functions[destination] = map[string]bool{}
		}
		changed := false
		for target := range functions[source] {
			if !functions[destination][target] {
				functions[destination][target] = true
				changed = true
			}
		}
		return changed
	}
	for changed := true; changed; {
		changed = false
		for _, binding := range r.OperationSources.Bindings {
			if copyTargets(binding.DestinationKey, binding.SourceKey) {
				changed = true
			}
		}
		for _, call := range r.OperationSources.Calls {
			target, ok := callables[call.TargetKey]
			if !ok {
				continue
			}
			for _, arg := range call.Arguments {
				for _, parameter := range target.Parameters {
					if parameter.Position == arg.ParameterPosition && copyTargets(parameter.Value.ObjectKey, arg.Value.ObjectKey) {
						changed = true
					}
				}
			}
		}
	}
	result := map[string]string{}
	for _, call := range r.OperationSources.Calls {
		if call.TargetStatus != "function-value" {
			continue
		}
		candidates := functions[call.TargetKey]
		// Different callbacks within one component are still different targets:
		// only a unique callable supports a concrete static binding claim.
		if len(candidates) != 1 {
			continue
		}
		for key := range candidates {
			if owner := owners[key]; owner != "" {
				result[call.ID] = owner
			}
		}
	}
	return result
}
