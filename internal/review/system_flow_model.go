package review

// SystemFlow describes source-supported architectural exchanges, not a runtime
// sequence. Members and sources retain the implementation behind each grouping.
type SystemFlow struct {
	Version      string            `json:"version"`
	SourceSHA256 string            `json:"sourceSHA256"`
	Components   []FlowComponent   `json:"components"`
	Operations   []FlowOperation   `json:"operations"`
	Exchanges    []FlowExchange    `json:"exchanges"`
	Alternatives []FlowAlternative `json:"alternatives"`
	Limitations  []string          `json:"limitations"`
}

// FlowAlternative is an inferred path alternative, not a measured branch
// counter or proof that all covered alternatives occurred in one invocation.
type FlowAlternative struct {
	ID          string          `json:"id"`
	SymbolID    string          `json:"symbolID"`
	ComponentID string          `json:"componentID,omitempty"`
	Decision    string          `json:"decision"`
	Label       string          `json:"label"`
	Location    Location        `json:"location"`
	Execution   string          `json:"execution"`
	Reason      string          `json:"reason,omitempty"`
	Witnesses   []CoverageBlock `json:"witnesses"`
	Members     []Location      `json:"members"`
	Requires    []string        `json:"requires,omitempty"`
}

type FlowComponent struct {
	ID      string     `json:"id"`
	Name    string     `json:"name"`
	Kind    string     `json:"kind"`
	Package string     `json:"package,omitempty"`
	Doc     string     `json:"doc,omitempty"`
	Members []string   `json:"members"`
	Sources []Location `json:"sources"`
}

type FlowOperation struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	ComponentID string   `json:"componentID"`
	SymbolID    string   `json:"symbolID,omitempty"`
	Location    Location `json:"location"`
}

type FlowInformation struct {
	Name      string `json:"name,omitempty"`
	Type      string `json:"type"`
	Direction string `json:"direction"`
}

type FlowExchange struct {
	ID          string            `json:"id"`
	From        string            `json:"from"`
	To          string            `json:"to"`
	Kind        string            `json:"kind"`
	Resolution  string            `json:"resolution"`
	Reason      string            `json:"reason,omitempty"`
	Information []FlowInformation `json:"information"`
	Sources     []Location        `json:"sources"`
	CallIDs     []string          `json:"callIDs"`
}
