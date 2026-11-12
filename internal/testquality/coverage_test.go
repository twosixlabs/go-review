package testquality

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/russellbrasser/go-review/internal/review"
)

func TestEvidenceExposesScopedStatementBlocksWithoutInventingCaseAttribution(t *testing.T) {
	r := testReport(t.TempDir())
	r.Coverage = &review.Coverage{SourceVerified: false, Origin: "imported", Blocks: []review.CoverageBlock{
		{FileID: "f", StartLine: 2, EndLine: 2, Statements: 1, Count: 5},
		{FileID: "other", StartLine: 4, EndLine: 4, Statements: 3, Count: 0},
	}}
	a, err := (Store{Directory: t.TempDir()}).Create(context.Background(), &review.RepositoryReview{Root: r.Root, Modules: []review.ReviewedModule{{Report: r}}}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	data, err := Evidence(context.Background(), a, EvidenceInput{SnapshotID: a.Reports[0].SnapshotID, Operation: "coverage", Args: json.RawMessage(`{"symbolID":"F","limit":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		SourceVerified bool `json:"sourceVerified"`
		Blocks         struct {
			Items []review.CoverageBlock `json:"items"`
			Total int                    `json:"total"`
		} `json:"blocks"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.SourceVerified || got.Blocks.Total != 1 || len(got.Blocks.Items) != 1 || got.Blocks.Items[0].Count != 5 {
		t.Fatalf("evidence: %s", data)
	}
	if !strings.Contains(string(data), "individual") {
		t.Fatalf("missing attribution boundary: %s", data)
	}
}
