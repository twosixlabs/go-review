package review

type OperationAnchor struct {
	Module       string   `json:"module"`
	SourceSHA256 string   `json:"sourceSHA256"`
	Location     Location `json:"location"`
}

type SourceValue struct {
	ID         string          `json:"id"`
	ObjectKey  string          `json:"objectKey,omitempty"`
	Expression string          `json:"expression"`
	Name       string          `json:"name,omitempty"`
	Type       string          `json:"type"`
	Anchor     OperationAnchor `json:"anchor"`
}

type DataHandoff struct {
	ID       string            `json:"id"`
	Kind     string            `json:"kind"`
	From     SourceValue       `json:"from"`
	To       SourceValue       `json:"to"`
	Evidence []OperationAnchor `json:"evidence"`
	Status   string            `json:"status"`
	Reason   string            `json:"reason,omitempty"`
}
