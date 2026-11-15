package retrieval

import (
	"context"
	"strings"
	"testing"

	"github.com/russellbrasser/go-review/internal/review"
)

func sampleReport() *review.Report {
	source := "package sample\nfunc RejectRange(n int) bool { return n < 1 || n > 2 }\nfunc Helper(n int) bool { return RejectRange(n) }\n"
	tests := "package sample\nfunc TestAllowed() { Helper(1) }\nfunc TestExcluded() { println(\"EXCLUDED_SECRET_MARKER\") }\n"
	return &review.Report{Module: "sample", Files: []review.File{{ID: "code", Path: "code.go", Package: "sample", Source: source}},
		Symbols: []review.Symbol{
			{ID: "reject", Name: "RejectRange", Package: "sample", Kind: "function", Location: review.Location{FileID: "code", Line: 2, EndLine: 2}},
			{ID: "helper", Name: "Helper", Package: "sample", Kind: "function", Location: review.Location{FileID: "code", Line: 3, EndLine: 3}},
		},
		TestSources: []review.TestSource{{File: review.File{ID: "tests", Path: "code_test.go", Package: "sample", Source: tests},
			Declarations: []review.TestDeclaration{{ID: "allowed", Name: "TestAllowed", Kind: "test", Location: review.Location{FileID: "tests", Line: 2, EndLine: 2}},
				{ID: "excluded", Name: "TestExcluded", Kind: "test", Location: review.Location{FileID: "tests", Line: 3, EndLine: 3}}},
			ExcludedRanges: []review.Location{{FileID: "tests", Line: 3, EndLine: 3}}}},
		SupportingFiles: []review.File{{ID: "support", Path: "hidden.go", Source: "package sample\nfunc Hidden(){println(\"HIDDEN_SUPPORT_MARKER\")}"}},
		Documents:       []review.Document{{ID: "contract", Path: "README.md", Text: "Reject values outside the accepted interval."}},
		Calls:           []review.Call{{From: "helper", To: "reject"}}}
}

func TestChunksRetainedScopeAndRepresentation(t *testing.T) {
	r := sampleReport()
	chunks, err := Chunks(context.Background(), r, 512)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, c := range chunks {
		found[c.EntityID] = true
		if strings.Contains(c.Text, "EXCLUDED_SECRET_MARKER") || strings.Contains(c.Text, "HIDDEN_SUPPORT_MARKER") {
			t.Fatalf("excluded source disclosed %s", c.ID)
		}
		if len(c.Text) > 512 || c.StartLine < 1 || c.EndLine < c.StartLine {
			t.Fatalf("invalid chunk %+v", c)
		}
		if c.EntityID == "reject" && !strings.Contains(c.Text, "Reject Range") {
			t.Fatalf("identifier not textualized %s", c.Text)
		}
	}
	if !found["reject"] || !found["helper"] || !found["allowed"] || !found["contract"] || found["excluded"] {
		t.Fatalf("membership %v", found)
	}
	if !strings.Contains(r.TestSources[0].File.Source, "EXCLUDED_SECRET_MARKER") {
		t.Fatal("mutated retained source")
	}
}

func TestChunksLongSourceKeepsOriginalRanges(t *testing.T) {
	r := sampleReport()
	r.Files[0].Source = "package sample\nfunc RejectRange(n int) bool {\n" + strings.Repeat("\tprintln(n)\n", 120) + "return true\n}\n"
	chunks, err := Chunks(context.Background(), r, 256)
	if err != nil {
		t.Fatal(err)
	}
	maxLine := 0
	for _, c := range chunks {
		if c.FileID == "code" {
			maxLine = max(maxLine, c.EndLine)
		}
		if len(c.Text) > 256 {
			t.Fatalf("oversized input %d", len(c.Text))
		}
	}
	if maxLine != 124 {
		t.Fatalf("lost source tail; last line=%d", maxLine)
	}
}

func TestRankHybridAndDimensionSafety(t *testing.T) {
	chunks := []Chunk{
		{ID: "exact", EntityID: "exact", Text: "RejectRange validation"},
		{ID: "semantic", EntityID: "semantic", Text: "a differently named function"},
	}
	vectors := [][]float32{{0, 1}, {1, 0}}
	result, err := Rank(chunks, vectors, "RejectRange", []float32{1, 0}, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Hits) != 2 || result.Hits[0].Chunk.ID != "exact" || result.Hits[0].LexicalRank != 1 {
		t.Fatalf("exact match suppressed %+v", result)
	}
	result, err = Rank(chunks, vectors, "outside accepted interval", []float32{1, 0}, 0, 1)
	if err != nil || result.Hits[0].Chunk.ID != "semantic" || result.NextOffset == nil {
		t.Fatalf("semantic/paging %+v %v", result, err)
	}
	if _, err = Rank(chunks, vectors, "x", []float32{1, 0, 0}, 0, 10); err == nil {
		t.Fatal("mixed embedding spaces")
	}
}
