package testquality

import (
	"math"
	"strings"
	"testing"
)

func rating(d Dimension, value int) Rating {
	return Rating{Dimension: d, State: RatingAssessed, Value: &value, Rationale: "supported by retained evidence", Evidence: []Citation{{SnapshotID: "snapshot", FileID: "tests", StartLine: 1, EndLine: 1}}}
}

func reviewed(symbol string, ratings ...Rating) Responsibility {
	return Responsibility{Identity: Identity{SnapshotID: "snapshot", Module: "module", SymbolID: symbol}, Status: StatusScored, Ratings: ratings}
}

func TestAggregateScoresAndScope(t *testing.T) {
	tests := []struct {
		name        string
		items       []Responsibility
		wantScore   *float64
		wantCounts  Counts
		wantPartial bool
	}{
		{
			name: "independent individual dimensions",
			items: []Responsibility{reviewed("one",
				rating(AssertionStrength, 4), rating(Craftsmanship, 2))},
			wantScore: ptr(75), wantCounts: Counts{Scored: 1},
		},
		{
			name: "equal test weights and unknown scope",
			items: []Responsibility{
				reviewed("one", rating(AssertionStrength, 4), rating(Craftsmanship, 4)),
				reviewed("two", rating(AssertionStrength, 2), rating(Craftsmanship, 2)),
				{Identity: Identity{SnapshotID: "snapshot", Module: "module", SymbolID: "three"}, Status: StatusUnreviewed},
			},
			wantScore: ptr(75), wantCounts: Counts{Scored: 2, Unreviewed: 1}, wantPartial: true,
		},
		{
			name: "no score remains unknown",
			items: []Responsibility{
				{Identity: Identity{SnapshotID: "snapshot", Module: "module", SymbolID: "one"}, Status: StatusInsufficient},
				{Identity: Identity{SnapshotID: "snapshot", Module: "module", SymbolID: "two"}, Status: StatusExcluded},
			},
			wantCounts: Counts{Insufficient: 1, Excluded: 1}, wantPartial: true,
		},
		{
			name: "inapplicable dimension excluded",
			items: []Responsibility{reviewed("one", rating(AssertionStrength, 4),
				Rating{Dimension: Craftsmanship, State: RatingNotApplicable, Rationale: "not applicable"})},
			wantScore: ptr(100), wantCounts: Counts{Scored: 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := AggregateVersion(EvidenceRubricVersion, tt.items)
			if err != nil {
				t.Fatal(err)
			}
			if got.Counts != tt.wantCounts || got.Partial != tt.wantPartial {
				t.Fatalf("got counts %+v partial %v, want %+v partial %v", got.Counts, got.Partial, tt.wantCounts, tt.wantPartial)
			}
			if (got.Score == nil) != (tt.wantScore == nil) {
				t.Fatalf("score = %v, want %v", got.Score, tt.wantScore)
			}
			if got.Score != nil && math.Abs(*got.Score-*tt.wantScore) > 1e-9 {
				t.Fatalf("score = %v, want %v", *got.Score, *tt.wantScore)
			}
		})
	}
}

func ptr(v float64) *float64 { return &v }

func TestAggregateRejectsInvalidEvidence(t *testing.T) {
	valid := func() Responsibility {
		return reviewed("one", rating(AssertionStrength, 3), rating(Craftsmanship, 3))
	}
	tests := []struct {
		name string
		make func() []Responsibility
		want string
	}{
		{"duplicate identity", func() []Responsibility { return []Responsibility{valid(), valid()} }, "duplicate"},
		{"rating outside range", func() []Responsibility {
			r := valid()
			r.Ratings[0] = rating(AssertionStrength, 5)
			return []Responsibility{r}
		}, "0-4"},
		{"missing dimension", func() []Responsibility { r := valid(); r.Ratings = r.Ratings[:1]; return []Responsibility{r} }, "dimension"},
		{"duplicate dimension", func() []Responsibility { r := valid(); r.Ratings[1] = r.Ratings[0]; return []Responsibility{r} }, "duplicate"},
		{"unknown applicable rating", func() []Responsibility {
			r := valid()
			r.Ratings[0] = Rating{Dimension: AssertionStrength, State: RatingUnknown, Rationale: "Unresolved assertion support"}
			return []Responsibility{r}
		}, "unknown"},
		{"unknown status", func() []Responsibility { r := valid(); r.Status = "mystery"; return []Responsibility{r} }, "status"},
		{"missing identity", func() []Responsibility { r := valid(); r.Identity.SnapshotID = ""; return []Responsibility{r} }, "identity"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := AggregateVersion(EvidenceRubricVersion, tt.make())
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got error %v, want containing %q", err, tt.want)
			}
		})
	}
}
