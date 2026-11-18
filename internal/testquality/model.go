// Package testquality records AI assessments separately from measured evidence.
package testquality

import (
	"sync"
	"time"

	"github.com/russellbrasser/go-review/internal/review"
)

const EvidenceRubricVersion = "test-quality/3"

// Identity qualifies a test declaration across retained module snapshots.
// SymbolID is the retained TestDeclaration ID, not a production function ID.
type Identity struct {
	SnapshotID string `json:"snapshotID"`
	Module     string `json:"module"`
	SymbolID   string `json:"symbolID"`
}

type Status string

const (
	StatusScored       Status = "scored"
	StatusInsufficient Status = "insufficient-evidence"
	StatusExcluded     Status = "excluded"
	StatusUnreviewed   Status = "unreviewed"
)

type Dimension string

const (
	AssertionStrength Dimension = "assertion-strength"
	Craftsmanship     Dimension = "test-craftsmanship"
	BehaviorAdequacy  Dimension = "behavior-adequacy"
	ConditionAdequacy Dimension = "conditions-usage-adequacy"
)

type RatingState string

const (
	RatingAssessed      RatingState = "assessed"
	RatingUnknown       RatingState = "unknown"
	RatingNotApplicable RatingState = "not-applicable"
)

type Rating struct {
	Dimension Dimension   `json:"dimension"`
	State     RatingState `json:"state"`
	Value     *int        `json:"value,omitempty"`
	Rationale string      `json:"rationale,omitempty"`
	Evidence  []Citation  `json:"evidence,omitempty"`
}

type Responsibility struct {
	Identity    Identity          `json:"identity"`
	Status      Status            `json:"status"`
	Ratings     []Rating          `json:"ratings,omitempty"`
	Reason      string            `json:"reason,omitempty"`
	Name        string            `json:"name"`
	Package     string            `json:"package"`
	Findings    []Finding         `json:"findings,omitempty"`
	Declaration Citation          `json:"declaration,omitempty"`
	TestKind    string            `json:"testKind,omitempty"`
	ReusedFrom  *ReusedAssessment `json:"reusedFrom,omitempty"`
}

type Citation struct {
	SnapshotID string `json:"snapshotID"`
	FileID     string `json:"fileID"`
	StartLine  int    `json:"startLine"`
	EndLine    int    `json:"endLine"`
}

type Finding struct {
	Category    string     `json:"category"`
	Kind        string     `json:"kind"`
	Title       string     `json:"title"`
	Rationale   string     `json:"rationale"`
	Consequence string     `json:"consequence"`
	Suggestion  string     `json:"suggestion"`
	Evidence    []Citation `json:"evidence"`
}

type PinnedReport struct {
	SnapshotID string         `json:"snapshotID"`
	SHA256     string         `json:"sha256"`
	Report     *review.Report `json:"report"`
}

type Artifact struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type Assessment struct {
	ID               string             `json:"id"`
	Version          string             `json:"version"`
	Root             string             `json:"root"`
	ReviewID         string             `json:"reviewID"`
	Status           string             `json:"status"`
	Error            string             `json:"error,omitempty"`
	Provider         string             `json:"provider"`
	Model            string             `json:"model"`
	CreatedAt        time.Time          `json:"createdAt"`
	UpdatedAt        time.Time          `json:"updatedAt"`
	Items            []Responsibility   `json:"items"`
	Reports          []PinnedReport     `json:"reports"`
	Unavailable      []string           `json:"unavailable,omitempty"`
	Artifacts        []Artifact         `json:"artifacts,omitempty"`
	Facts            []TestFacts        `json:"facts,omitempty"`
	Analysis         AnalysisMetrics    `json:"analysis,omitempty"`
	Suite            *SuiteAssessment   `json:"suite,omitempty"`
	Review           ReviewMetrics      `json:"review,omitempty"`
	Diagnostics      []DiagnosticRecord `json:"diagnostics,omitempty"`
	Policy           DiagnosticPolicy   `json:"policy"`
	DiagnosticReason string             `json:"diagnosticReason,omitempty"`
	Phase            string             `json:"phase,omitempty"`
	Activity         string             `json:"activity,omitempty"`
	PhaseMillis      map[string]int64   `json:"phaseMillis,omitempty"`
	Persistence      PersistenceMetrics `json:"persistence"`
	SavedSummary     *Summary           `json:"summary,omitempty"`
	factsOnce        sync.Once
	factsIndex       *factIndex
	factsError       error
	archive          *qualityArchive
	savedItems       []Responsibility
}

type PersistenceMetrics struct {
	Writes int   `json:"writes"`
	Bytes  int64 `json:"bytes"`
}

type Counts struct {
	Scored       int `json:"scored"`
	Insufficient int `json:"insufficient"`
	Excluded     int `json:"excluded"`
	Unreviewed   int `json:"unreviewed"`
}

type Summary struct {
	Score   *float64 `json:"score"`
	Counts  Counts   `json:"counts"`
	Partial bool     `json:"partial"`
}
