package retrieval

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"
)

type Hit struct {
	Chunk        Chunk    `json:"chunk"`
	LexicalRank  int      `json:"lexicalRank,omitempty"`
	SemanticRank int      `json:"semanticRank,omitempty"`
	Reasons      []string `json:"reasons"`
}

type Result struct {
	SnapshotID  string   `json:"snapshotID"`
	Hits        []Hit    `json:"hits"`
	NextOffset  *int     `json:"nextOffset"`
	Limitations []string `json:"limitations"`
}

func vectorNorm(vector []float32) (float64, error) {
	var norm float64
	for _, value := range vector {
		v := float64(value)
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return 0, fmt.Errorf("invalid vector value")
		}
		norm += v * v
	}
	if norm == 0 {
		return 0, fmt.Errorf("empty or zero vector")
	}
	return math.Sqrt(norm), nil
}

// Rank fuses ranks instead of incompatible score scales. Whole literal matches
// are protected from weak embedding rankings; similarity is candidate evidence.
func Rank(chunks []Chunk, vectors [][]float32, query string, queryVector []float32, offset, limit int) (Result, error) {
	result := Result{Hits: []Hit{}, Limitations: []string{"Best-effort retrieval; absence of a hit does not establish absent tests or behavior."}}
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" || offset < 0 || limit < 1 || limit > 100 || len(chunks) != len(vectors) {
		return result, fmt.Errorf("nonempty query, valid pagination and aligned chunk vectors required")
	}
	queryNorm, err := vectorNorm(queryVector)
	if err != nil {
		return result, err
	}
	type candidate struct {
		index                   int
		exact                   bool
		lexical, cosine, fusion float64
		hit                     Hit
	}
	items := make([]candidate, len(chunks))
	terms := strings.FieldsFunc(query, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	for i, chunk := range chunks {
		if len(vectors[i]) != len(queryVector) {
			return result, fmt.Errorf("embedding dimensions differ: %d != %d", len(vectors[i]), len(queryVector))
		}
		norm, err := vectorNorm(vectors[i])
		if err != nil {
			return result, err
		}
		var dot float64
		for j, value := range vectors[i] {
			dot += float64(value) * float64(queryVector[j])
		}
		text := strings.ToLower(chunk.Text)
		item := candidate{index: i, exact: strings.Contains(text, query), cosine: dot / (norm * queryNorm),
			hit: Hit{Chunk: chunk, Reasons: []string{}}}
		for _, term := range terms {
			if strings.Contains(text, term) {
				item.lexical++
			}
		}
		if item.exact {
			item.lexical += float64(len(terms) + 1)
		}
		items[i] = item
	}
	lex := make([]int, len(items))
	sem := make([]int, len(items))
	for i := range items {
		lex[i], sem[i] = i, i
	}
	tie := func(a, b int) bool { return chunks[a].ID < chunks[b].ID }
	sort.Slice(lex, func(i, j int) bool {
		a, b := lex[i], lex[j]
		if items[a].lexical == items[b].lexical {
			return tie(a, b)
		}
		return items[a].lexical > items[b].lexical
	})
	sort.Slice(sem, func(i, j int) bool {
		a, b := sem[i], sem[j]
		if items[a].cosine == items[b].cosine {
			return tie(a, b)
		}
		return items[a].cosine > items[b].cosine
	})
	// Equal scores contribute equal ranks; arbitrary ID ordering must not
	// invent evidence, especially when a weak model emits constant vectors.
	lexicalRank := 1
	for rank, index := range lex {
		if rank > 0 && items[index].lexical != items[lex[rank-1]].lexical {
			lexicalRank = rank + 1
		}
		if items[index].lexical > 0 {
			items[index].hit.LexicalRank = lexicalRank
			items[index].hit.Reasons = append(items[index].hit.Reasons, "lexical")
			items[index].fusion += 1 / float64(60+lexicalRank)
		}
	}
	semanticRank := 1
	for rank, index := range sem {
		if rank > 0 && items[index].cosine != items[sem[rank-1]].cosine {
			semanticRank = rank + 1
		}
		items[index].hit.SemanticRank = semanticRank
		items[index].hit.Reasons = append(items[index].hit.Reasons, "embedding candidate (capability unverified)")
		items[index].fusion += 1 / float64(60+semanticRank)
	}
	sort.Slice(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.exact != b.exact {
			return a.exact
		}
		if a.fusion == b.fusion {
			return tie(a.index, b.index)
		}
		return a.fusion > b.fusion
	})
	if offset > len(items) {
		return result, fmt.Errorf("retrieval offset exceeds candidate count")
	}
	end := min(len(items), offset+limit)
	for _, item := range items[offset:end] {
		result.Hits = append(result.Hits, item.hit)
	}
	if end < len(items) {
		result.NextOffset = &end
	}
	return result, nil
}
