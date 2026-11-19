package review

import "strings"

// lambdaEntries follows source-resolved calls from each main. Registration
// identity comes from the imported AWS package, never the spelling of an alias.
func lambdaEntries(r *Report) ([]EntryPoint, map[string]bool) {
	if r.OperationSources == nil {
		return nil, nil
	}
	callables := map[string]OperationCallable{}
	bySymbol := map[string]OperationCallable{}
	calls := map[string][]OperationCall{}
	hasRegistration := false
	for _, callable := range r.OperationSources.Callables {
		callables[callable.Key] = callable
		bySymbol[callable.SymbolID] = callable
	}
	for _, call := range r.OperationSources.Calls {
		calls[call.CallerKey] = append(calls[call.CallerKey], call)
		if call.TargetPackage == "github.com/aws/aws-lambda-go/lambda" {
			switch call.TargetName {
			case "Start", "StartWithOptions", "StartHandler", "StartHandlerFunc", "StartWithContext", "StartHandlerWithContext":
				hasRegistration = true
			}
		}
	}
	if !hasRegistration {
		return nil, nil
	}
	var entries []EntryPoint
	mains := map[string]bool{}
	for _, entry := range r.EntryPoints.Entries {
		if entry.Kind != "main" {
			continue
		}
		main, ok := bySymbol[entry.SymbolID]
		if !ok {
			continue
		}
		seen := map[string]bool{}
		budget := 4096
		var visit func(string, map[string]string, map[string]string, string, []EntryPointEvidence)
		visit = func(key string, values, types map[string]string, path string, chain []EntryPointEvidence) {
			if seen[key] {
				return
			}
			seen[key] = true
			defer delete(seen, key)
			for _, call := range calls[key] {
				if budget == 0 {
					return
				}
				budget--
				if call.TargetPackage == "github.com/aws/aws-lambda-go/lambda" {
					arg := -1
					switch call.TargetName {
					case "Start", "StartWithOptions", "StartHandler", "StartHandlerFunc":
						arg = 0
					case "StartWithContext", "StartHandlerWithContext":
						arg = 1
					}
					if arg < 0 {
						continue
					}
					mains[entry.SymbolID] = true
					location := call.Anchor.Location
					result := EntryPoint{
						ID:   stableID("entry", "lambda:"+entry.SymbolID+":"+path+":"+call.ID),
						Kind: "lambda", Name: "Lambda handler", Resolution: "unresolved",
						Reason:           "Lambda handler could not be resolved from the registration.",
						BoundarySymbolID: entry.SymbolID,
						Evidence:         append(append([]EntryPointEvidence{}, chain...), EntryPointEvidence{Kind: "lambda-registration", Detail: call.TargetName, Location: &location}),
					}
					for _, value := range call.Arguments {
						if value.Position != arg {
							continue
						}
						result.Name = value.Value.Expression
						handler := value.Value
						for _, inner := range calls[key] {
							if inner.TargetPackage != "github.com/aws/aws-lambda-go/lambda" ||
								(inner.TargetName != "NewHandler" && inner.TargetName != "NewHandlerWithOptions") ||
								inner.Anchor.Location != handler.Anchor.Location || len(inner.Arguments) == 0 {
								continue
							}
							handler = inner.Arguments[0].Value
							location := inner.Anchor.Location
							result.Evidence = append(result.Evidence, EntryPointEvidence{Kind: "lambda-handler-wrapper", Detail: inner.TargetName, Location: &location})
							break
						}
						targetKey, bindings := lambdaBoundValue(handler.ObjectKey, key, call.Anchor.Location, values, r.OperationSources.Bindings)
						result.Evidence = append(result.Evidence, bindings...)
						target, found := callables[targetKey]
						if !found {
							for _, candidate := range callables {
								if candidate.Kind == "closure" && candidate.Anchor.Location.FileID == handler.Anchor.Location.FileID &&
									candidate.Anchor.Location.Offset == handler.Anchor.Location.Offset {
									target, found = candidate, true
									break
								}
							}
						}
						if !found {
							receiver := handler.Type
							if forwarded := types[handler.ObjectKey]; forwarded != "" {
								receiver = forwarded
							}
							for _, candidate := range callables {
								if candidate.Name != "Invoke" || candidate.Receiver == "" ||
									(candidate.Receiver != receiver && !(strings.HasPrefix(receiver, "*") && candidate.Receiver == strings.TrimPrefix(receiver, "*"))) ||
									len(candidate.Parameters) != 2 || len(candidate.Results) != 2 ||
									candidate.Parameters[0].Value.Type != "context.Context" || candidate.Parameters[1].Value.Type != "[]byte" ||
									candidate.Results[0].Value.Type != "[]byte" || candidate.Results[1].Value.Type != "error" {
									continue
								}
								target, found = candidate, true
								break
							}
						}
						if found && target.SymbolID != "" {
							result.SymbolID, result.Resolution, result.Reason = target.SymbolID, "resolved", ""
						}
						break
					}
					entries = append(entries, result)
				}
				target, ok := callables[call.TargetKey]
				if !ok && call.Receiver != nil && types[call.Receiver.ObjectKey] != "" {
					receiver := types[call.Receiver.ObjectKey]
					for _, candidate := range callables {
						if candidate.Name != call.TargetName || candidate.Receiver == "" {
							continue
						}
						if candidate.Receiver != receiver && !(strings.HasPrefix(receiver, "*") && candidate.Receiver == strings.TrimPrefix(receiver, "*")) {
							continue
						}
						if ok {
							ok = false
							break
						}
						target, ok = candidate, true
					}
				}
				if ok {
					evidence := append([]EntryPointEvidence{}, chain...)
					next := make(map[string]string, len(values))
					for key, value := range values {
						next[key] = value
					}
					nextTypes := make(map[string]string, len(types))
					for key, value := range types {
						nextTypes[key] = value
					}
					for _, arg := range call.Arguments {
						for _, parameter := range target.Parameters {
							if parameter.Position == arg.ParameterPosition {
								value, bindings := lambdaBoundValue(arg.Value.ObjectKey, key, call.Anchor.Location, values, r.OperationSources.Bindings)
								evidence = append(evidence, bindings...)
								next[parameter.Value.ObjectKey] = value
								typ := arg.Value.Type
								if forwarded := types[arg.Value.ObjectKey]; forwarded != "" {
									typ = forwarded
								}
								nextTypes[parameter.Value.ObjectKey] = typ
							}
						}
					}
					location := call.Anchor.Location
					evidence = append(evidence, EntryPointEvidence{
						Kind: "lambda-forwarding", Detail: call.TargetName, Location: &location})
					visit(target.Key, next, nextTypes, path+":"+call.ID, evidence)
				}
			}
		}
		visit(main.Key, nil, nil, "", nil)
		if budget == 0 {
			r.EntryPoints.Limitations = append(r.EntryPoints.Limitations, "Lambda registration traversal reached the 4096 call limit for "+entry.Name+".")
		}
	}
	return entries, mains
}

// Only a single preceding assignment in the same callable supports an alias;
// multiple writes or cross-file bindings are not a control-flow proof.
func lambdaBoundValue(key, owner string, at Location, values map[string]string, bindings []OperationBinding) (string, []EntryPointEvidence) {
	seen := map[string]bool{}
	var evidence []EntryPointEvidence
	for key != "" && !seen[key] {
		seen[key] = true
		if value := values[key]; value != "" {
			key = value
			continue
		}
		var binding *OperationBinding
		for i := range bindings {
			candidate := &bindings[i]
			destination := candidate.DestinationKey
			if destination == "" && candidate.Destination != nil {
				destination = candidate.Destination.ObjectKey
			}
			if destination != key {
				continue
			}
			if binding != nil {
				return key, evidence
			}
			binding = candidate
		}
		if binding == nil || binding.OwnerKey != owner || binding.DestinationKey == "" || binding.Status != "resolved" || binding.SourceKey == "" ||
			binding.Anchor.Location.FileID != at.FileID || binding.Anchor.Location.EndOffset > at.Offset {
			break
		}
		location := binding.Anchor.Location
		evidence = append(evidence, EntryPointEvidence{Kind: "lambda-binding", Location: &location})
		key = binding.SourceKey
	}
	return key, evidence
}
