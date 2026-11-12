package testquality

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/russellbrasser/go-review/internal/review"
)

func TestQueryFindingsRetainQualifiedScopeAndFilters(t *testing.T) {
	r := testReport(t.TempDir())
	other := testReport(t.TempDir())
	other.Module = "other"
	other.Symbols[0].Package = "other/service"
	a, err := (Store{Directory: t.TempDir()}).Create(context.Background(), &review.RepositoryReview{Root: r.Root, Modules: []review.ReviewedModule{{Report: r}, {Report: other}}}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	a.Items[0].Findings = []Finding{{Title: "Return value checked", Category: "strength", Kind: "observation", Rationale: "Assertion checks behavior."}}
	raw, err := Query(context.Background(), a, QueryInput{AssessmentID: a.ID, Operation: "findings", Category: "strength", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Total int `json:"total"`
		Items []struct {
			Identity Identity `json:"identity"`
			Title    string   `json:"title"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Total != 1 || len(got.Items) != 1 || got.Items[0].Identity != a.Items[0].Identity {
		t.Fatalf("scope lost: %s", raw)
	}
	raw, err = Query(context.Background(), a, QueryInput{AssessmentID: a.ID, Operation: "findings", SnapshotID: a.Items[1].Identity.SnapshotID})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &got); err != nil || got.Total != 0 {
		t.Fatalf("cross-snapshot findings leaked: %s %v", raw, err)
	}
}
