// Package review collects local, inspectable Go evidence without AI or target execution.
package review

import "time"

const SchemaVersion = "1"

type Options struct {
	Dir              string
	DocumentationDir string
	CoverageProfile  string
	UnitSelections   []UnitTestSelection
	ArtifactDir      string
	Scope            *ReviewScope
}

// Report is a snapshot: IDs depend on logical source locations, not wall-clock time.
type Report struct {
	SchemaVersion     string               `json:"schemaVersion"`
	GeneratedAt       time.Time            `json:"generatedAt"`
	Root              string               `json:"root"`
	DocumentationRoot string               `json:"documentationRoot"`
	Module            string               `json:"module"`
	ToolVersion       string               `json:"toolVersion"`
	Environment       map[string]string    `json:"environment"`
	SourceSHA256      string               `json:"sourceSHA256"`
	Packages          []Package            `json:"packages"`
	Files             []File               `json:"files"`
	Symbols           []Symbol             `json:"symbols"`
	Calls             []Call               `json:"calls"`
	ControlFlows      []ControlFlow        `json:"controlFlows"`
	References        []Reference          `json:"references"`
	HTTPEndpoints     []HTTPEndpoint       `json:"httpEndpoints"`
	EntryPoints       *EntryPointInventory `json:"entryPoints,omitempty"`
	Documents         []Document           `json:"documents"`
	TestSources       []TestSource         `json:"testSources"`
	Inputs            []Input              `json:"inputs"`
	Commands          []Command            `json:"commands"`
	Coverage          *Coverage            `json:"coverage,omitempty"`
	Warnings          []string             `json:"warnings"`
	Limitations       []string             `json:"limitations"`
	Revision          Revision             `json:"revision"`
	Trace             *TraceEvidence       `json:"trace,omitempty"`
	Assessment        *Assessment          `json:"assessment,omitempty"`
	Review            *ReviewReference     `json:"review,omitempty"`
	Scope             ReviewScope          `json:"scope"`
	Exclusions        []ScopeExclusion     `json:"exclusions"`
	OperationSources  *OperationSources    `json:"operationSources,omitempty"`
	SystemFlow        *SystemFlow          `json:"systemFlow,omitempty"`
	UnitEvidence      *UnitEvidence        `json:"unitEvidence,omitempty"`
	// SupportingFiles retain excluded source for source-bound typing/SSA, never
	// for review metrics or normal graph membership.
	SupportingFiles []File `json:"supportingFiles,omitempty"`
}

type Revision struct {
	Commit    string `json:"commit,omitempty"`
	Dirty     bool   `json:"dirty"`
	Available bool   `json:"available"`
}

type TraceEvidence struct {
	ObservationPolicy    string               `json:"observationPolicy,omitempty"`
	Partial              bool                 `json:"partial,omitempty"`
	DroppedEvents        int                  `json:"droppedEvents,omitempty"`
	ExcludedTests        []RecordingExclusion `json:"excludedTests,omitempty"`
	Events               []TraceEvent         `json:"events"`
	Edges                []ObservedEdge       `json:"edges"`
	BaselineExitCode     int                  `json:"baselineExitCode"`
	InstrumentedExitCode int                  `json:"instrumentedExitCode"`
	OutcomesMatch        bool                 `json:"outcomesMatch"`
	BaselineOutcomes     map[string]string    `json:"baselineOutcomes"`
	InstrumentedOutcomes map[string]string    `json:"instrumentedOutcomes"`
	Packages             []string             `json:"packages"`
	Limitations          []string             `json:"limitations"`
}

type RecordingExclusion struct {
	Test   string `json:"test"`
	Reason string `json:"reason"`
}

type TraceEvent struct {
	Process   string `json:"process"`
	Sequence  uint64 `json:"sequence"`
	ElapsedNS int64  `json:"elapsedNS"`
	Goroutine string `json:"goroutine"`
	SymbolID  string `json:"symbolID"`
	Action    string `json:"action"`
	// Invocation identifies one instrumented function entry within this process.
	Invocation string `json:"invocation,omitempty"`
	// Test is attached after attribution filtering, not inferred from a function name.
	Test string `json:"test,omitempty"`
	// Site refers to the original source snapshot; statement and decision events carry it.
	Site *Location `json:"site,omitempty"`
	// Outcome records an evaluated branch or selected switch/select arm.
	Outcome string `json:"outcome,omitempty"`
	// CallSite links a value observation to the production call expression.
	CallSite *Location    `json:"callSite,omitempty"`
	Values   []TraceValue `json:"values,omitempty"`
}

type TraceValue struct {
	Role      string `json:"role"`
	Name      string `json:"name,omitempty"`
	Position  int    `json:"position,omitempty"`
	Type      string `json:"type"`
	Value     any    `json:"value"`
	Truncated bool   `json:"truncated,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// ObservedEdge is a stack adjacency in one process/goroutine, not an inferred
// static edge. Uninstrumented frames may lie between From and To.
type ObservedEdge struct {
	From      string `json:"from"`
	To        string `json:"to"`
	Process   string `json:"process"`
	Goroutine string `json:"goroutine"`
	Test      string `json:"test,omitempty"`
	Count     int    `json:"count"`
}

type Package struct {
	ID         string   `json:"id"`
	Path       string   `json:"path"`
	Name       string   `json:"name"`
	Doc        string   `json:"doc"`
	Imports    []string `json:"imports"`
	FileIDs    []string `json:"fileIDs"`
	TypeErrors []string `json:"typeErrors"`
}

// Source columns and offsets are UTF-8 bytes, matching go/token and Go cover profiles.
type Location struct {
	FileID    string `json:"fileID"`
	Line      int    `json:"line"`
	Column    int    `json:"column"`
	EndLine   int    `json:"endLine"`
	EndColumn int    `json:"endColumn"`
	Offset    int    `json:"offset"`
	EndOffset int    `json:"endOffset"`
}

type File struct {
	ID      string `json:"id"`
	Package string `json:"package"`
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
	Source  string `json:"source"`
}

type Symbol struct {
	ID         string    `json:"id"`
	Package    string    `json:"package"`
	Name       string    `json:"name"`
	Qualified  string    `json:"qualified"`
	Kind       string    `json:"kind"`
	Doc        string    `json:"doc"`
	Signature  string    `json:"signature"`
	Cyclomatic int       `json:"cyclomatic,omitempty"`
	Comments   []Comment `json:"comments,omitempty"`
	Location   `json:"location"`
}

type Comment struct {
	Text     string `json:"text"`
	Location `json:"location"`
}

type Call struct {
	ID        string `json:"id"`
	From      string `json:"from"`
	To        string `json:"to,omitempty"`
	Target    string `json:"target"`
	Kind      string `json:"kind"`
	Reason    string `json:"reason,omitempty"`
	Execution string `json:"execution"`
	// Invocation describes the outer call only; go/defer arguments still
	// evaluate synchronously at the containing statement.
	Invocation string `json:"invocation,omitempty"`
	// EvaluationOrder is postorder among calls in the owning function.
	// Conditional expression paths still require the containing CFG evidence.
	EvaluationOrder int `json:"evaluationOrder,omitempty"`
	Location        `json:"location"`
}

type Reference struct {
	From string `json:"from,omitempty"`
	To   string `json:"to"`
	Name string `json:"name"`
	// Usage distinguishes a syntactic call target from a callable used as a value.
	// Empty means unclassified, not an inferred callback.
	Usage    string `json:"usage,omitempty"`
	Location `json:"location"`
}

// HTTPEndpoint records a registration site, not proof the route is reachable
// from a running server. Unknown patterns and handler targets retain source.
type HTTPEndpoint struct {
	ID                string `json:"id"`
	RegistrationID    string `json:"registrationID,omitempty"`
	API               string `json:"api"`
	Receiver          string `json:"receiver,omitempty"`
	Pattern           string `json:"pattern"`
	PatternKnown      bool   `json:"patternKnown"`
	PatternExpression string `json:"patternExpression"`
	Handler           string `json:"handler"`
	HandlerID         string `json:"handlerID,omitempty"`
	Reason            string `json:"reason,omitempty"`
	Location          `json:"location"`
}

type Document struct {
	ID     string `json:"id"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Text   string `json:"text"`
}

type TestSource struct {
	File           File              `json:"file"`
	Declarations   []TestDeclaration `json:"declarations"`
	ExcludedRanges []Location        `json:"excludedRanges"`
}

type TestDeclaration struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Kind     string   `json:"kind"`
	Location Location `json:"location"`
}

type Input struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type Command struct {
	Executable   string    `json:"executable"`
	Args         []string  `json:"args"`
	Directory    string    `json:"directory"`
	StartedAt    time.Time `json:"startedAt"`
	FinishedAt   time.Time `json:"finishedAt"`
	ExitCode     int       `json:"exitCode"`
	StdoutSHA256 string    `json:"stdoutSHA256"`
	Stdout       string    `json:"stdout,omitempty"`
	Stderr       string    `json:"stderr,omitempty"`
}

type Coverage struct {
	Mode              string          `json:"mode"`
	Origin            string          `json:"origin"`
	SHA256            string          `json:"sha256"`
	SourceVerified    bool            `json:"sourceVerified"`
	Statements        int64           `json:"statements"`
	CoveredStatements int64           `json:"coveredStatements"`
	Blocks            []CoverageBlock `json:"blocks"`
}

type CoverageBlock struct {
	FileID      string `json:"fileID,omitempty"`
	Path        string `json:"path"`
	StartLine   int    `json:"startLine"`
	StartColumn int    `json:"startColumn"`
	EndLine     int    `json:"endLine"`
	EndColumn   int    `json:"endColumn"`
	Statements  int64  `json:"statements"`
	Count       int64  `json:"count"`
}
