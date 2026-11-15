package retrieval

import (
	"context"
	"testing"

	"github.com/russellbrasser/go-review/internal/review"
)

func TestEnginePinnedBindingsAvoidRepeatedReportEncoding(t *testing.T) {
	e := engineFixture(t, t.TempDir(), "one", 2, new(int))
	r := sampleReport()
	if err := e.Build(context.Background(), "ready", r); err != nil {
		t.Fatal(err)
	}
	if err := e.BindSnapshot("pinned", r); err != nil {
		t.Fatal(err)
	}
	allocations := testing.AllocsPerRun(10, func() {
		if err := e.BindSnapshot("pinned", r); err != nil {
			t.Fatal(err)
		}
	})
	if allocations > 2 {
		t.Fatalf("hot pinned binding re-encodes retained report: %g allocations", allocations)
	}
	t.Logf("hot pinned binding: %g allocations", allocations)
}

func TestEngineTestHelperExpansionIsLabeledCandidate(t *testing.T) {
	e := engineFixture(t, t.TempDir(), "one", 2, new(int))
	if err := e.Build(context.Background(), "scope", sampleReport()); err != nil {
		t.Fatal(err)
	}
	result, err := e.Search(context.Background(), "scope", "", "allowed", 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, hit := range result.Hits {
		if hit.Chunk.EntityID == "helper" {
			for _, reason := range hit.Reasons {
				if reason == "same-package call-name candidate (not resolved)" {
					return
				}
			}
		}
	}
	t.Fatalf("test/helper relation not recovered with honest provenance: %+v", result.Hits)
}

func TestChunksOuterFunctionKeepsItsIdentity(t *testing.T) {
	r := &review.Report{Files: []review.File{{ID: "f", Path: "f.go", Source: "package sample\nfunc Outer() {\n _ = func() {}\n}\n"}},
		Symbols: []review.Symbol{
			{ID: "closure", Name: "anonymous", Kind: "closure", Location: review.Location{FileID: "f", Line: 3, EndLine: 3}},
			{ID: "outer", Name: "Outer", Kind: "function", Location: review.Location{FileID: "f", Line: 2, EndLine: 4}},
		}}
	chunks, err := Chunks(context.Background(), r, 512)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 1 || chunks[0].EntityID != "outer" {
		t.Fatalf("closure replaced owner: %+v", chunks)
	}
}
