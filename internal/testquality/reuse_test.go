package testquality

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/russellbrasser/go-review/internal/review"
)

func TestStoreMergesPartialResultsAndPreservesOriginalProvenance(t *testing.T) {
	r := testReport(t.TempDir())
	for _, name := range []string{"TestOther", "TestRemaining"} {
		r.TestSources[0].Declarations = append(r.TestSources[0].Declarations,
			review.TestDeclaration{ID: name, Name: name, Kind: "test", Location: review.Location{FileID: "t", Line: 3, EndLine: 3}})
	}
	other := testReport(t.TempDir())
	other.Module = "dependency"
	s := Store{Directory: t.TempDir()}
	run := &review.RepositoryReview{Root: r.Root, Modules: []review.ReviewedModule{{Report: r}, {Report: other}}}
	create := func() *Assessment {
		t.Helper()
		r.GeneratedAt = time.Now()
		other.GeneratedAt = r.GeneratedAt
		a, err := s.Create(t.Context(), run, "local", "fixture")
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	score := func(a *Assessment, index, value int) {
		t.Helper()
		item := &a.Items[index]
		item.Status = StatusScored
		item.Ratings = nil
		for _, dimension := range []Dimension{Craftsmanship, AssertionStrength} {
			item.Ratings = append(item.Ratings, Rating{Dimension: dimension, State: RatingAssessed, Value: &value,
				Rationale: "Compared with the dependency.", Evidence: []Citation{item.Declaration,
					{SnapshotID: a.Reports[1].SnapshotID, FileID: "f", StartLine: 2, EndLine: 2}}})
		}
		if err := s.Save(a); err != nil {
			t.Fatal(err)
		}
	}
	old := create()
	score(old, 0, 2)
	newer := create()
	// Model an independent partial review: only its second test was saved.
	newer.Items[0].Status, newer.Items[0].Ratings, newer.Items[0].ReusedFrom = StatusUnreviewed, nil, nil
	score(newer, 1, 4)
	merged := create()
	if merged.Items[0].Status != StatusScored || *merged.Items[0].Ratings[0].Value != 2 ||
		merged.Items[1].Status != StatusScored || *merged.Items[1].Ratings[0].Value != 4 ||
		merged.Items[2].Status != StatusUnreviewed {
		t.Fatalf("partial results were not merged without inventing unfinished work: %+v", merged.Items)
	}
	if merged.Items[0].ReusedFrom.AssessmentID != old.ID || merged.Items[1].ReusedFrom.AssessmentID != newer.ID ||
		merged.Items[0].Ratings[0].Evidence[1].SnapshotID != merged.Reports[1].SnapshotID {
		t.Fatal("merge lost origin or cross-module citation provenance")
	}
	chained := create()
	if chained.Items[0].ReusedFrom.AssessmentID != old.ID || chained.Items[1].ReusedFrom.AssessmentID != newer.ID {
		t.Fatal("reuse chain attributed original work to an importing assessment")
	}
	chained.Items[0].Ratings[0].Evidence[0].StartLine = 99
	if old.Items[0].Ratings[0].Evidence[0].StartLine != 3 {
		t.Fatal("reused evidence aliases the original judgment")
	}
}

func TestStoreReusesSupportedResultsOnlyForMatchingInputs(t *testing.T) {
	for _, change := range []string{"none", "production", "helper", "contract", "dependency", "supporting", "other-module"} {
		t.Run(change, func(t *testing.T) {
			r := testReport(t.TempDir())
			r.Documents = []review.Document{{ID: "doc", Path: "README.md", Text: "F returns one.", SHA256: Digest([]byte("F returns one."))}}
			s := Store{Directory: t.TempDir()}
			run := &review.RepositoryReview{Root: r.Root, Modules: []review.ReviewedModule{{Report: r}}}
			create := func() *Assessment {
				t.Helper()
				a, err := s.Create(context.Background(), run, "local", "fixture")
				if err != nil {
					t.Fatal(err)
				}

				return a
			}
			old := create()
			value := 3
			old.Items[0].Status = StatusScored
			for _, dimension := range []Dimension{Craftsmanship, AssertionStrength} {
				old.Items[0].Ratings = append(old.Items[0].Ratings, Rating{Dimension: dimension, State: RatingAssessed, Value: &value,
					Rationale: "Checks the return value.", Evidence: []Citation{old.Items[0].Declaration}})
			}
			old.Status, old.Error = "failed", "provider failed after saving this result"
			old.Suite = &SuiteAssessment{Status: StatusInsufficient, Reason: "Collective review unfinished."}
			old.Policy = DefaultDiagnosticPolicy(true)
			old.Diagnostics = []DiagnosticRecord{{Status: "failed", Error: "retained diagnostic failure"}}
			if err := s.Save(old); err != nil {
				t.Fatal(err)
			}
			r.GeneratedAt = time.Now()
			switch change {
			case "production":
				r.Files[0].Source += "// Changed implementation context.\n"
				r.Files[0].SHA256 = Digest([]byte(r.Files[0].Source))
			case "helper":
				r.TestSources[0].File.Source += "func helper() {}\n"
				r.TestSources[0].File.SHA256 = Digest([]byte(r.TestSources[0].File.Source))
			case "contract":
				r.Documents[0].Text = "F must reject invalid input."
				r.Documents[0].SHA256 = Digest([]byte(r.Documents[0].Text))
			case "dependency":
				r.SourceSHA256 = "changed dependency inputs"
			case "supporting":
				r.SupportingFiles = append(r.SupportingFiles, review.File{ID: "support", Path: "support.go", Source: "package example\n", SHA256: Digest([]byte("package example\n"))})
			case "other-module":
				other := testReport(t.TempDir())
				other.Module = "other"
				run.Modules = append(run.Modules, review.ReviewedModule{Report: other})
			}
			next := create()
			if change != "none" {
				if next.Items[0].Status != StatusUnreviewed {
					t.Fatal("changed associated evidence reused an old judgment")
				}
				return
			}
			if next.Items[0].Status != StatusScored || next.Suite != nil || len(next.Diagnostics) != 0 || next.Policy.AllowExecution {
				t.Fatalf("supported partial-run work was not reused independently of synthesis/execution: %+v", next.Items[0])
			}
			if next.Items[0].Identity.SnapshotID == old.Items[0].Identity.SnapshotID ||
				next.Items[0].Ratings[0].Evidence[0].SnapshotID != next.Items[0].Identity.SnapshotID {
				t.Fatal("reused evidence did not bind to the new pinned snapshot")
			}
			raw, err := Query(t.Context(), next, QueryInput{AssessmentID: next.ID, Operation: "test",
				SnapshotID: next.Items[0].Identity.SnapshotID, SymbolID: next.Items[0].Identity.SymbolID})
			if err != nil {
				t.Fatal(err)
			}
			var data struct {
				ReusedFrom struct{ AssessmentID, Model string }
			}
			if err := json.Unmarshal(raw, &data); err != nil || data.ReusedFrom.AssessmentID != old.ID || data.ReusedFrom.Model != "fixture" {
				t.Fatalf("reuse provenance missing from persisted/queryable result: %s %v", raw, err)
			}
			loaded, err := s.Load(next.ID)
			if err != nil || loaded.Items[0].Status != StatusScored {
				t.Fatalf("reused results were not durably saved: %v", err)
			}
		})
	}
}
