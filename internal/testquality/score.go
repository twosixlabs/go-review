package testquality

import (
	"fmt"
	"strings"
)

// Independently supported ratings remain useful when another dimension is unknown.
func AggregateVersion(version string, items []Responsibility) (Summary, error) {
	if version != EvidenceRubricVersion {
		return Summary{}, fmt.Errorf("unsupported Test Quality version %q; start a new assessment", version)
	}
	summary := Summary{}
	identities := map[Identity]bool{}
	sum, n := 0.0, 0
	for _, item := range items {
		id := item.Identity
		if id.SnapshotID == "" || id.Module == "" || id.SymbolID == "" || identities[id] {
			return Summary{}, fmt.Errorf("invalid or duplicate test identity %+v", id)
		}
		identities[id] = true
		switch item.Status {
		case StatusScored:
			summary.Counts.Scored++
		case StatusInsufficient:
			summary.Counts.Insufficient++
		case StatusExcluded:
			summary.Counts.Excluded++
		case StatusUnreviewed:
			summary.Counts.Unreviewed++
		default:
			return Summary{}, fmt.Errorf("invalid test status %s", item.Status)
		}
		if len(item.Ratings) > 0 {
			score, unknown, err := judgmentScore(item.Ratings, []Dimension{Craftsmanship, AssertionStrength})
			if err != nil {
				return Summary{}, err
			}
			if unknown && item.Status == StatusScored {
				return Summary{}, fmt.Errorf("unknown dimension requires a partial test judgment")
			}
			if score != nil {
				sum += *score
				n++
			}
		} else if item.Status == StatusScored {
			return Summary{}, fmt.Errorf("scored test has no ratings")
		}
	}
	if n > 0 {
		score := sum / float64(n)
		summary.Score = &score
	}
	summary.Partial = summary.Counts.Insufficient > 0 || summary.Counts.Unreviewed > 0
	return summary, nil
}

func judgmentScore(ratings []Rating, dimensions []Dimension) (*float64, bool, error) {
	seen := map[Dimension]bool{}
	sum, n := 0, 0
	unknown := false
	for _, rating := range ratings {
		allowed := false
		for _, d := range dimensions {
			if rating.Dimension == d {
				allowed = true
			}
		}
		if !allowed || seen[rating.Dimension] {
			return nil, false, fmt.Errorf("invalid/duplicate judgment dimension %s", rating.Dimension)
		}
		seen[rating.Dimension] = true
		if strings.TrimSpace(rating.Rationale) == "" {
			return nil, false, fmt.Errorf("judgment requires rationale")
		}
		switch rating.State {
		case RatingAssessed:
			if rating.Value == nil || *rating.Value < 0 || *rating.Value > 4 || len(rating.Evidence) == 0 {
				return nil, false, fmt.Errorf("assessed dimension requires 0-4 value and source evidence")
			}
			sum += *rating.Value
			n++
		case RatingUnknown:
			if rating.Value != nil {
				return nil, false, fmt.Errorf("unknown dimension must not have a value")
			}
			unknown = true
		case RatingNotApplicable:
			if rating.Value != nil {
				return nil, false, fmt.Errorf("not-applicable dimension must not have a value")
			}
		default:
			return nil, false, fmt.Errorf("invalid rating state %s", rating.State)
		}
	}
	if len(seen) != len(dimensions) {
		return nil, false, fmt.Errorf("all dimensions for this judgment scope are required")
	}
	if n == 0 {
		return nil, unknown, nil
	}
	score := 25 * float64(sum) / float64(n)
	return &score, unknown, nil
}
