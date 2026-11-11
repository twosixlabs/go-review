package review

type SnapshotRef struct {
	RunID        string `json:"runID"`
	ModuleID     string `json:"moduleID"`
	SourceSHA256 string `json:"sourceSHA256"`
}

type Citation struct {
	Snapshot  SnapshotRef `json:"snapshot"`
	SourceID  string      `json:"sourceID"`
	StartLine int         `json:"startLine"`
	EndLine   int         `json:"endLine"`
}
