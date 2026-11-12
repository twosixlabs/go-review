package testquality

import (
	"fmt"
	"strings"
)

// Aggregate weights scored functions equally. Unknown evidence is not a zero
// rating and therefore never participates in the score's denominator.
func Aggregate(items []Responsibility) (Summary, error) {
	var summary Summary
	var sum float64
	seen := make(map[Identity]bool, len(items))
	for _, item := range items {
		id := item.Identity
		if strings.TrimSpace(id.SnapshotID) == "" || strings.TrimSpace(id.Module) == "" || strings.TrimSpace(id.SymbolID) == "" {
			return Summary{}, fmt.Errorf("responsibility identity requires snapshot, module and symbol")
		}
		if seen[id] {
			return Summary{}, fmt.Errorf("duplicate responsibility identity %+v", id)
		}
		seen[id] = true
		switch item.Status {
		case StatusScored:
			score, err := scoreRatings(item.Ratings)
			if err != nil {
				return Summary{}, fmt.Errorf("score responsibility %+v: %w", id, err)
			}
			sum += score
			summary.Counts.Scored++
		case StatusInsufficient:
			summary.Counts.Insufficient++
		case StatusExcluded:
			summary.Counts.Excluded++
		case StatusUnreviewed:
			summary.Counts.Unreviewed++
		default:
			return Summary{}, fmt.Errorf("invalid responsibility status %q", item.Status)
		}
	}
	if summary.Counts.Scored > 0 {
		score := sum / float64(summary.Counts.Scored)
		summary.Score = &score
	}
	summary.Partial = summary.Counts.Insufficient > 0 || summary.Counts.Unreviewed > 0
	return summary, nil
}

func scoreRatings(ratings []Rating) (float64, error) {
	seen := make(map[Dimension]bool, len(ratings))
	total, applicable := 0, 0
	for _, rating := range ratings {
		switch rating.Dimension {
		case AssertionStrength, RepresentativeCases, BehaviorPaths:
		default:
			return 0, fmt.Errorf("unknown dimension %q", rating.Dimension)
		}
		if seen[rating.Dimension] {
			return 0, fmt.Errorf("duplicate dimension %q", rating.Dimension)
		}
		seen[rating.Dimension] = true
		switch rating.State {
		case RatingAssessed:
			if rating.Value == nil || *rating.Value < 0 || *rating.Value > 4 {
				return 0, fmt.Errorf("dimension %q rating must be in range 0–4", rating.Dimension)
			}
			if strings.TrimSpace(rating.Rationale) == "" {
				return 0, fmt.Errorf("dimension %q requires rationale", rating.Dimension)
			}
			total += *rating.Value
			applicable++
		case RatingNotApplicable:
			if rating.Value != nil || strings.TrimSpace(rating.Rationale) == "" {
				return 0, fmt.Errorf("dimension %q is not applicable: requires rationale and no value", rating.Dimension)
			}
		case RatingUnknown:
			return 0, fmt.Errorf("dimension %q is unknown", rating.Dimension)
		default:
			return 0, fmt.Errorf("dimension %q has invalid state %q", rating.Dimension, rating.State)
		}
	}
	if len(seen) != 3 {
		return 0, fmt.Errorf("all three rubric dimensions are required")
	}
	if applicable == 0 {
		return 0, fmt.Errorf("at least one applicable dimension is required")
	}
	return 25 * float64(total) / float64(applicable), nil
}
