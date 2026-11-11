package review

import "sort"

// ApplyFlowCoverage infers an arm from counters whose entry source belongs
// exclusively to that arm's CFG region. Shared successors are never witnesses.
// The decision is removed during reachability so a later loop iteration cannot
// turn both arms into one region. Ambiguous source mappings remain unknown.
func ApplyFlowCoverage(r *Report, flow *SystemFlow) {
	flow.Alternatives = []FlowAlternative{}
	owners := map[string]string{}
	exchangeCalls := map[string]bool{}
	for _, exchange := range flow.Exchanges {
		for _, id := range exchange.CallIDs {
			exchangeCalls[id] = true
		}
	}
	writes := map[string][]Location{}
	if r.OperationSources != nil {
		for _, store := range r.OperationSources.FieldStores {
			for _, op := range flow.Operations {
				if op.ID == store.OwnerKey {
					writes[op.SymbolID] = append(writes[op.SymbolID], store.Anchor.Location)
					break
				}
			}
		}
	}
	for _, op := range flow.Operations {
		owners[op.SymbolID] = op.ComponentID
	}
	for _, cfg := range r.ControlFlows {
		if owners[cfg.SymbolID] == "" {
			continue
		}
		nodes := map[string]FlowNode{}
		next := map[string][]FlowEdge{}
		for _, node := range cfg.Nodes {
			nodes[node.ID] = node
		}
		for _, edge := range cfg.Edges {
			next[edge.From] = append(next[edge.From], edge)
		}
		for _, decision := range cfg.Nodes {
			arms := next[decision.ID]
			if len(arms) < 2 {
				continue
			}
			regions := make([]map[string]bool, len(arms))
			for i, arm := range arms {
				regions[i] = flowReachable(arm.To, decision.ID, next)
			}
			// A case body may also be reached by fallthrough (or a label).
			// Hits there establish body execution, not this decision's outcome.
			bypass := flowReachable(cfg.Entry, decision.ID, next)
			relevant := false
			alternatives := []FlowAlternative{}
			for i, arm := range arms {
				a := FlowAlternative{
					ID:       stableID("alternative", cfg.SymbolID+":"+decision.ID+":"+arm.To+":"+arm.Label),
					SymbolID: cfg.SymbolID, ComponentID: owners[cfg.SymbolID],
					Decision: decision.Label, Label: arm.Label, Location: decision.Location,
					Execution: "unknown", Reason: "No matching coverage witness for this alternative.",
					Witnesses: []CoverageBlock{}, Members: []Location{},
				}
				for _, node := range cfg.Nodes {
					if !regions[i][node.ID] || node.Kind == "exit" || node.Kind == "entry" {
						continue
					}
					exclusive := true
					for j, region := range regions {
						if j != i && region[node.ID] {
							exclusive = false
							break
						}
					}
					if !exclusive {
						continue
					}
					a.Members = append(a.Members, node.Location)
					switch node.Kind {
					case "return", "panic", "go", "defer", "send", "communication":
						relevant = true
					}
					for _, id := range node.CallIDs {
						relevant = relevant || exchangeCalls[id]
					}
					for _, write := range writes[cfg.SymbolID] {
						if write.FileID == node.Location.FileID && flowLocationContains(node.Location, write.Line, write.Column) {
							relevant = true
						}
					}
					if bypass[node.ID] {
						a.Reason = "This source path also has an entry bypassing the decision; coverage cannot attribute the selected arm."
						continue
					}
					if r.Coverage == nil {
						continue
					}
					for _, block := range r.Coverage.Blocks {
						if block.FileID != node.Location.FileID || block.Statements == 0 {
							continue
						}
						// Counter entry is the witness: merely overlapping an
						// arm with a larger profile range cannot establish it.
						if !flowLocationContains(node.Location, block.StartLine, block.StartColumn) {
							continue
						}
						a.Witnesses = appendUniqueFlowWitness(a.Witnesses, block)
						if a.Execution != "exercised" {
							a.Execution = "not-observed"
							a.Reason = "Arm-specific coverage counters were not hit in this selection."
						}
						if block.Count > 0 {
							a.Execution = "exercised"
							a.Reason = "An executed counter entry lies on this arm's exclusive source path."
						}
					}
				}
				alternatives = append(alternatives, a)
			}
			if relevant {
				flow.Alternatives = append(flow.Alternatives, alternatives...)
			}
		}
	}
	// A covered nested arm is not a free-standing scenario. Preserve source/CFG
	// prerequisites without enumerating every combination or imposing chronology.
	for i := range flow.Alternatives {
		a := &flow.Alternatives[i]
		for _, parent := range flow.Alternatives {
			if parent.SymbolID != a.SymbolID || parent.ID == a.ID {
				continue
			}
			for _, member := range parent.Members {
				if member == a.Location {
					a.Requires = append(a.Requires, parent.ID)
					break
				}
			}
		}
		sort.Strings(a.Requires)
	}
}

func flowReachable(start, stop string, next map[string][]FlowEdge) map[string]bool {
	seen := map[string]bool{}
	queue := []string{start}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if id == stop || seen[id] {
			continue
		}
		seen[id] = true
		for _, edge := range next[id] {
			queue = append(queue, edge.To)
		}
	}
	return seen
}

func flowLocationContains(loc Location, line, column int) bool {
	if loc.Line == 0 || loc.Line > line || loc.EndLine < line {
		return false
	}
	if loc.Line == line && column < loc.Column {
		return false
	}
	if loc.EndLine == line && column >= loc.EndColumn {
		return false
	}
	return true
}

func appendUniqueFlowWitness(blocks []CoverageBlock, block CoverageBlock) []CoverageBlock {
	for _, existing := range blocks {
		if existing == block {
			return blocks
		}
	}
	return append(blocks, block)
}
