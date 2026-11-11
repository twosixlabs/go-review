package review

// observedEdges is retained only to normalize historical snapshots on load.
// New behavior evidence uses standard Go coverage and runtime traces.
func observedEdges(events []TraceEvent) []ObservedEdge {
	stacks := map[string][]string{}
	result := []ObservedEdge{}
	index := map[string]int{}
	for _, e := range events {
		key := e.Process + ":" + e.Goroutine
		stack := stacks[key]
		if e.Action == "enter" {
			if len(stack) > 0 {
				from := stack[len(stack)-1]
				edgeKey := key + ":" + from + ":" + e.SymbolID + ":" + e.Test
				if i, ok := index[edgeKey]; ok {
					result[i].Count++
				} else {
					index[edgeKey] = len(result)
					result = append(result, ObservedEdge{From: from, To: e.SymbolID, Process: e.Process, Goroutine: e.Goroutine, Test: e.Test, Count: 1})
				}
			}
			stacks[key] = append(stack, e.SymbolID)
		} else if e.Action == "exit" {
			for i := len(stack) - 1; i >= 0; i-- {
				if stack[i] == e.SymbolID {
					stacks[key] = stack[:i]
					break
				}
			}
		}
	}
	return result
}
