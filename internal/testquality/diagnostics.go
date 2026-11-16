package testquality

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/russellbrasser/go-review/internal/review"
)

type DiagnosticPolicy struct {
	AllowExecution bool `json:"allowExecution"`
	MaxCommands    int  `json:"maxCommands"`
	MaxRepetitions int  `json:"maxRepetitions"`
}
type DiagnosticRequest struct {
	Kind        string     `json:"kind"`
	Tests       []Identity `json:"tests"`
	Repetitions int        `json:"repetitions"`
	Reason      string     `json:"reason"`
	Evidence    []Citation `json:"evidence"`
}
type DiagnosticRecord struct {
	Request        DiagnosticRequest    `json:"request"`
	Status         string               `json:"status"`
	Error          string               `json:"error,omitempty"`
	SourceVerified bool                 `json:"sourceVerified"`
	Runs           []review.UnitTestRun `json:"runs,omitempty"`
	Artifacts      []Artifact           `json:"artifacts,omitempty"`
}

type diagnosticSummary struct {
	DiagnosticIndex int               `json:"diagnosticIndex"`
	Request         DiagnosticRequest `json:"request"`
	Status          string            `json:"status"`
	Error           string            `json:"error"`
	SourceVerified  bool              `json:"sourceVerified"`
	RunCount        int               `json:"runCount"`
	ArtifactCount   int               `json:"artifactCount"`
}

func summarizeDiagnostic(index int, record DiagnosticRecord) diagnosticSummary {
	return diagnosticSummary{index, record.Request, record.Status, record.Error, record.SourceVerified, len(record.Runs), len(record.Artifacts)}
}

func DefaultDiagnosticPolicy(allow bool) DiagnosticPolicy {
	return DiagnosticPolicy{AllowExecution: allow, MaxCommands: 8, MaxRepetitions: 3}
}

// Membership comes only from source-verified retained executions, never from
// the source catalog. TestMain is an implicit package harness, not a target.
func diagnosticPool(a *Assessment) map[Identity]Responsibility {
	pool := map[Identity]Responsibility{}
	for _, item := range a.Items {
		if item.Status == StatusExcluded || item.Name == "TestMain" {
			continue
		}
		r, err := pinned(a, item.Identity.SnapshotID)
		if err != nil || r.UnitEvidence == nil || r.SourceSHA256 == "" || r.UnitEvidence.SourceSHA256 != r.SourceSHA256 {
			continue
		}
		pkg := strings.TrimSuffix(item.Package, "_test")
		for _, run := range r.UnitEvidence.Runs {
			if run.Selection.Package != pkg {
				continue
			}
			for _, name := range run.MatchedTests {
				if name == item.Name {
					pool[item.Identity] = item
				}
			}
		}
	}
	return pool
}

func ValidateDiagnostics(a *Assessment, policy DiagnosticPolicy, requests []DiagnosticRequest) error {
	if len(requests) > 0 && !policy.AllowExecution {
		return fmt.Errorf("test execution explicitly disabled")
	}
	if policy.MaxCommands < 1 || policy.MaxCommands > 8 || policy.MaxRepetitions < 1 || policy.MaxRepetitions > 3 {
		return fmt.Errorf("invalid diagnostic bounds")
	}
	pool := diagnosticPool(a)
	commands := 0
	signatures := map[string]bool{}
	for _, request := range requests {
		switch request.Kind {
		case "rerun", "repeat", "shuffle", "race":
		default:
			return fmt.Errorf("unsupported diagnostic kind %s", request.Kind)
		}
		if request.Reason == "" || len(request.Evidence) == 0 || len(request.Tests) == 0 {
			return fmt.Errorf("diagnostic requires scoped tests, reason and evidence")
		}
		if request.Repetitions < 1 || request.Repetitions > policy.MaxRepetitions {
			return fmt.Errorf("diagnostic repetition budget exceeded")
		}
		targets := append([]Identity{}, request.Tests...)
		sort.Slice(targets, func(i, j int) bool {
			x, y := targets[i], targets[j]
			return x.SnapshotID+"\n"+x.Module+"\n"+x.SymbolID < y.SnapshotID+"\n"+y.Module+"\n"+y.SymbolID
		})
		raw, err := json.Marshal(struct {
			Kind        string
			Repetitions int
			Tests       []Identity
		}{request.Kind, request.Repetitions, targets})
		if err != nil {
			return err
		}
		signature := string(raw)
		if signatures[signature] {
			return fmt.Errorf("redundant diagnostic request; use the explicit repetition count")
		}
		signatures[signature] = true
		groups := map[string]int{}
		groupCommands := map[string]int{}
		seen := map[Identity]bool{}
		for _, id := range request.Tests {
			item, ok := pool[id]
			if !ok || seen[id] {
				return fmt.Errorf("diagnostic target is unknown, excluded, duplicate or not in the source-verified reviewed pool")
			}
			seen[id] = true
			group := id.SnapshotID + "\n" + strings.TrimSuffix(item.Package, "_test")
			groups[group]++
			groupCommands[group] = max(1, groupCommands[group])
			if item.TestKind == "example" {
				groupCommands[group] = request.Repetitions
			}
		}
		if request.Kind == "shuffle" {
			for _, count := range groups {
				if count < 2 {
					return fmt.Errorf("isolated top-level shuffle cannot test inter-test order dependence")
				}
			}
		}
		for _, count := range groupCommands {
			commands += count
		}
		if commands > policy.MaxCommands {
			return fmt.Errorf("diagnostic command budget exceeded")
		}
		for _, c := range request.Evidence {
			r, err := pinned(a, c.SnapshotID)
			if err != nil {
				return err
			}
			valid := false
			for _, file := range files(r) {
				if file.ID == c.FileID && c.StartLine > 0 && c.EndLine >= c.StartLine && c.EndLine <= len(strings.Split(file.Source, "\n")) {
					valid = true
				}
			}
			if !valid {
				return fmt.Errorf("diagnostic evidence is outside pinned source")
			}
		}
	}
	return nil
}

// Static risks select a small nonredundant subset. A sleep site is a reason to
// gather observations, not an assertion that a test is flaky.
func PlanDiagnostics(a *Assessment, policy DiagnosticPolicy) ([]DiagnosticRequest, string, error) {
	if !policy.AllowExecution {
		return nil, "Fresh test execution disabled by the user.", nil
	}
	facts, err := AnalyzeFacts(a)
	if err != nil {
		return nil, "", err
	}
	pool := diagnosticPool(a)
	if len(pool) == 0 {
		return nil, "Fresh diagnostics unavailable: no source-verified evaluated test membership.", nil
	}
	requests := []DiagnosticRequest{}
	for _, fact := range facts {
		item, ok := pool[fact.Identity]
		if !ok {
			continue
		}
		for _, observation := range fact.Observations {
			if observation.Category != "sleep" && observation.Category != "package-state-write" {
				continue
			}
			request := DiagnosticRequest{Kind: "repeat", Tests: []Identity{item.Identity}, Repetitions: 2, Reason: "Gather limited repeat evidence for observed static " + observation.Category + "; passing attempts do not prove reliability.", Evidence: observation.Evidence}
			cost := 0
			for _, planned := range requests {
				if pool[planned.Tests[0]].TestKind == "example" {
					cost += planned.Repetitions
				} else {
					cost++
				}
			}
			newCost := 1
			if item.TestKind == "example" {
				newCost = request.Repetitions
			}
			if cost+newCost > policy.MaxCommands {
				return requests, fmt.Sprintf("Fresh diagnostic plan limited to %d commands; additional candidate tests were not selected.", policy.MaxCommands), nil
			}
			if err := ValidateDiagnostics(a, policy, append(requests, request)); err != nil {
				return nil, "", err
			}
			requests = append(requests, request)
			break
		}
	}
	if len(requests) == 0 {
		return nil, "No nonredundant diagnostic indicated by the retained static risk evidence.", nil
	}
	return requests, "System-selected bounded diagnostic subset; not the full suite.", nil
}

// The collector owns mutable result fields; immutable source/analysis tables
// are shared. Artifact/error records survive partial failures without replacing
// original coverage/outcomes.
func RunDiagnostic(ctx context.Context, a *Assessment, policy DiagnosticPolicy, request DiagnosticRequest, directory string) (DiagnosticRecord, error) {
	record := DiagnosticRecord{Request: request, Status: "failed"}
	if err := ValidateDiagnostics(a, policy, []DiagnosticRequest{request}); err != nil {
		record.Error = err.Error()
		return record, err
	}
	pool := diagnosticPool(a)
	groups := map[string]map[string][]string{}
	for _, id := range request.Tests {
		item := pool[id]
		if groups[id.SnapshotID] == nil {
			groups[id.SnapshotID] = map[string][]string{}
		}
		pkg := strings.TrimSuffix(item.Package, "_test")
		groups[id.SnapshotID][pkg] = append(groups[id.SnapshotID][pkg], item.Name)
	}
	snapshots := []string{}
	for snapshot := range groups {
		snapshots = append(snapshots, snapshot)
	}
	sort.Strings(snapshots)
	sourceVerified := true
	for _, snapshot := range snapshots {
		r, err := pinned(a, snapshot)
		if err != nil {
			return record, err
		}
		scratch := *r
		scratch.UnitEvidence, scratch.Coverage, scratch.Commands = nil, nil, nil
		// ImportCoverage updates flow annotations before the collector rebuilds
		// its projection, so the retained flow must not be shared.
		scratch.SystemFlow = nil
		scratch.Warnings = append([]string{}, r.Warnings...)
		selections := []review.UnitTestSelection{}
		for pkg, names := range groups[snapshot] {
			sort.Strings(names)
			selections = append(selections, review.UnitTestSelection{Package: pkg, Tests: names})
		}
		sort.Slice(selections, func(i, j int) bool { return selections[i].Package < selections[j].Package })
		options := review.UnitEvidenceOptions{Selections: selections, ArtifactDir: filepath.Join(directory, snapshot), Repetitions: request.Repetitions, Race: request.Kind == "race"}
		if request.Kind == "shuffle" {
			options.ShuffleSeed = strconv.FormatInt(time.Now().UnixNano(), 10)
		}
		err = review.CollectUnitEvidence(ctx, &scratch, options)
		verified := scratch.UnitEvidence != nil && scratch.UnitEvidence.SourceSHA256 == r.SourceSHA256
		if scratch.UnitEvidence != nil {
			record.Runs = append(record.Runs, scratch.UnitEvidence.Runs...)
			for _, run := range scratch.UnitEvidence.Runs {
				verified = verified && run.Status != "stale"
			}
		}
		sourceVerified = sourceVerified && verified
		record.SourceVerified = sourceVerified
		if err != nil {
			record.Status, record.Error = "partial", err.Error()
		}
		walkErr := filepath.WalkDir(options.ArtifactDir, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				if os.IsNotExist(walkErr) {
					return nil
				}
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("diagnostic artifact symlink")
			}
			bytes, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			record.Artifacts = append(record.Artifacts, Artifact{Path: path, SHA256: Digest(bytes)})
			return nil
		})
		if walkErr != nil {
			return record, walkErr
		}
		if err != nil {
			return record, err
		}
	}
	record.Status = "pass"
	return record, nil
}
