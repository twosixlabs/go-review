// Package testquality records AI assessments separately from measured evidence.
package testquality

import (
	"time"

	"github.com/russellbrasser/go-review/internal/review"
)

const RubricVersion = "test-quality/1"

// Identity qualifies a production symbol across retained module snapshots.
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
	AssertionStrength   Dimension = "assertion-strength"
	RepresentativeCases Dimension = "representative-cases"
	BehaviorPaths       Dimension = "behavior-paths"
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
	Identity Identity  `json:"identity"`
	Status   Status    `json:"status"`
	Ratings  []Rating  `json:"ratings,omitempty"`
	Reason   string    `json:"reason,omitempty"`
	Name     string    `json:"name"`
	Package  string    `json:"package"`
	Findings []Finding `json:"findings,omitempty"`
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
	ID          string           `json:"id"`
	Version     string           `json:"version"`
	Root        string           `json:"root"`
	ReviewID    string           `json:"reviewID"`
	Status      string           `json:"status"`
	Error       string           `json:"error,omitempty"`
	Provider    string           `json:"provider"`
	Model       string           `json:"model"`
	CreatedAt   time.Time        `json:"createdAt"`
	UpdatedAt   time.Time        `json:"updatedAt"`
	Items       []Responsibility `json:"items"`
	Reports     []PinnedReport   `json:"reports"`
	Unavailable []string         `json:"unavailable,omitempty"`
	Artifacts   []Artifact       `json:"artifacts,omitempty"`
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
