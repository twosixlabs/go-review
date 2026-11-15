package testquality

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/russellbrasser/go-review/internal/review"
)

func BenchmarkQualityCheckpoint(b *testing.B) {
	source := largeFactsAssessment()
	store := Store{Directory: b.TempDir()}
	a, err := store.Create(b.Context(), &review.RepositoryReview{
		Root: b.TempDir(), Modules: []review.ReviewedModule{{Report: source.Reports[0].Report}},
	}, "", "")
	if err != nil {
		b.Fatal(err)
	}
	if _, err := AnalyzeFacts(a); err != nil {
		b.Fatal(err)
	}
	if err := store.Save(a); err != nil {
		b.Fatal(err)
	}
	before := a.Persistence
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.Items[0].Status = StatusInsufficient
		a.Items[0].Reason = fmt.Sprintf("Observed checkpoint %d", i)
		if err := store.Save(a); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(a.Persistence.Bytes-before.Bytes)/float64(b.N), "bytes/checkpoint")
	b.ReportMetric(float64(a.Persistence.Writes-before.Writes)/float64(b.N), "writes/checkpoint")
}

func TestQualityEvidenceCheckpointsReuseFactsAndReports(t *testing.T) {
	source := factsAssessment("package example; import \"testing\"\nfunc TestF(t *testing.T) {if F()!=1 {t.Fatal(\"wrong\")}}\n")
	store := Store{Directory: t.TempDir()}
	a, err := store.Create(t.Context(), &review.RepositoryReview{Root: t.TempDir(), Modules: []review.ReviewedModule{{Report: source.Reports[0].Report}}}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AnalyzeFacts(a); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(a); err != nil {
		t.Fatal(err)
	}
	writes := a.Persistence.Writes
	report, facts := a.archive.Reports[0], *a.archive.Facts
	if err := store.Save(a); err != nil {
		t.Fatal(err)
	}
	if a.Persistence.Writes != writes+1 {
		t.Fatal("unchanged evidence/results were rewritten")
	}
	a.Items[0].Status, a.Items[0].Reason = StatusInsufficient, "Semantic judgment unavailable"
	writes = a.Persistence.Writes
	if err := store.Save(a); err != nil {
		t.Fatal(err)
	}
	if a.Persistence.Writes != writes+2 {
		t.Fatal("changed result did not publish as one independent record")
	}
	if a.archive.Reports[0] != report || *a.archive.Facts != facts {
		t.Fatal("immutable evidence references changed")
	}
	loaded, err := store.Load(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Items[0].Reason != a.Items[0].Reason || len(loaded.Facts) != 1 {
		t.Fatal("checkpoint failed rehydration")
	}
	list, err := store.ListSummaries(a.Root)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Reports != nil || list[0].Facts != nil || list[0].SavedSummary.Counts.Insufficient != 1 {
		t.Fatal("status listing loaded whole evidence or lost progress")
	}
	if !contained(filepath.Join(store.Directory, a.ID), filepath.Join(store.Directory, a.ID, facts.Path)) {
		t.Fatal("immutable record escaped archive")
	}
}
