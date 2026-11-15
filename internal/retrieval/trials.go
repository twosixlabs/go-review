package retrieval

import (
	"context"
	"fmt"
	"math"

	"github.com/russellbrasser/go-review/internal/intelligence"
	"github.com/russellbrasser/go-review/internal/review"
)

// Trials uses synthetic source only, applying the same bounded structural
// representation as corpus indexing. Results never certify general capability.
func Trials(ctx context.Context, config intelligence.EmbeddingConfig, embed EmbedFunc) ([]intelligence.EmbeddingTrial, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if !config.Enabled || embed == nil {
		return nil, fmt.Errorf("enabled embedding configuration and encoder required for synthetic trials")
	}
	cases := []struct{ name, source, text, equivalent string }{
		{"inclusive-range", "func f(x,a,b int) bool { return a<=x && x<=b }", "Check whether an integer lies within inclusive bounds.", "func u(n,l,h int) bool { return n>=l && n<=h }"},
		{"normalization", "func g(s string) string { return strings.TrimSpace(strings.ToLower(s)) }", "Normalize text by lowercasing and trimming whitespace.", "func v(s string) string { s=strings.ToLower(s); return strings.TrimSpace(s) }"},
		{"deduplication", "func h(x []int) []int { slices.Sort(x); return slices.Compact(x) }", "Remove duplicate integers from a slice.", "func w(s []int) []int { slices.Sort(s); s=slices.Compact(s); return s }"},
	}
	project := func(source string) (string, error) {
		chunks, err := unitChunks(ctx, sourceUnit{File: review.File{ID: "synthetic", Path: "probe.go", Source: "package probe\n" + source}}, config.InputLimit()-max(len(config.DocumentPrefix), len(config.QueryPrefix)))
		if err != nil {
			return "", err
		}
		if len(chunks) != 1 {
			return "", fmt.Errorf("synthetic trial does not fit the configured input budget")
		}
		return chunks[0].Text, nil
	}
	inputs := []string{}
	for _, c := range cases {
		inputs = append(inputs, config.DocumentPrefix+c.source)
	}
	for _, c := range cases {
		text, err := project(c.source)
		if err != nil {
			return nil, err
		}
		inputs = append(inputs, config.DocumentPrefix+text)
	}
	for _, c := range cases {
		inputs = append(inputs, config.QueryPrefix+c.text)
	}
	for _, c := range cases {
		inputs = append(inputs, config.QueryPrefix+c.equivalent)
	}
	for _, c := range cases {
		text, err := project(c.equivalent)
		if err != nil {
			return nil, err
		}
		inputs = append(inputs, config.QueryPrefix+text)
	}
	vectors := [][]float32{}
	for start := 0; start < len(inputs); {
		end, size := start, 0
		for end < len(inputs) && size+len(inputs[end]) <= config.BatchLimit() {
			if len(inputs[end]) > config.InputLimit() {
				return nil, fmt.Errorf("synthetic trial exceeds configured input budget")
			}
			size += len(inputs[end])
			end++
		}
		if end == start {
			return nil, fmt.Errorf("synthetic trial exceeds configured batch budget")
		}
		batch, err := embed(ctx, inputs[start:end])
		if err != nil {
			return nil, fmt.Errorf("synthetic retrieval trial: %w", err)
		}
		if len(batch) != end-start {
			return nil, fmt.Errorf("synthetic trial vector cardinality mismatch")
		}
		vectors = append(vectors, batch...)
		start = end
	}
	dimensions := len(vectors[0])
	for _, vector := range vectors {
		if len(vector) != dimensions {
			return nil, fmt.Errorf("synthetic trial dimension mismatch")
		}
		if _, err := vectorNorm(vector); err != nil {
			return nil, err
		}
	}
	cosine := func(a, b []float32) float64 {
		na, _ := vectorNorm(a)
		nb, _ := vectorNorm(b)
		var score float64
		for i := range a {
			score += float64(a[i]) * float64(b[i])
		}
		return score / na / nb
	}
	out := []intelligence.EmbeddingTrial{}
	for representation, offset := range []int{0, 3} {
		for direction, queryOffset := range []int{6, 9 + representation*3} {
			for target, c := range cases {
				query := vectors[queryOffset+target]
				targetScore := cosine(query, vectors[offset+target])
				rank, ties := 1, 0
				for candidate := 0; candidate < 3; candidate++ {
					score := cosine(query, vectors[offset+candidate])
					if math.Abs(score-targetScore) <= 1e-6 {
						ties++
					} else if score > targetScore {
						rank++
					}
				}
				out = append(out, intelligence.EmbeddingTrial{Direction: []string{"text-code", "code-code"}[direction], Representation: []string{"raw", "structural"}[representation], Case: c.name, Rank: rank, Ties: ties, Candidates: 3})
			}
		}
	}
	return out, nil
}
