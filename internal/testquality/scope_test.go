package testquality

import (
	"context"
	"testing"

	"github.com/russellbrasser/go-review/internal/review"
)

func TestStoreAccountsForBodylessAndUnavailableDeclarations(t *testing.T) {
	r := testReport(t.TempDir())
	r.Files[0].Source = "package example\nfunc F() int\n"
	r.Files[0].SHA256 = Digest([]byte(r.Files[0].Source))
	r.Symbols = append(r.Symbols,
		review.Symbol{ID: "missing", Name: "Missing", Kind: "method", Package: "example", Location: review.Location{FileID: "missing", Line: 4}},
		review.Symbol{ID: "closure", Name: "F$1", Kind: "closure", Package: "example", Location: review.Location{FileID: "f", Line: 2}})
	a, err := (Store{Directory: t.TempDir()}).Create(context.Background(), &review.RepositoryReview{Root: r.Root, Modules: []review.ReviewedModule{{Report: r}}}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Items) != 2 {
		t.Fatalf("population: %+v", a.Items)
	}
	for _, item := range a.Items {
		if item.Status != StatusInsufficient || item.Reason == "" {
			t.Fatalf("unavailable declaration not accounted for: %+v", item)
		}
	}
}
