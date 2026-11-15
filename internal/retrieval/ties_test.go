package retrieval

import "testing"

func TestRankConstantVectorsDoNotOverruleLexicalEvidence(t *testing.T) {
	chunks := []Chunk{{ID: "a", Text: "alpha only"}, {ID: "b", Text: "beta only"}, {ID: "z", Text: "alpha somewhere beta"}}
	result, err := Rank(chunks, [][]float32{{1}, {1}, {1}}, "alpha beta", []float32{1}, 0, 3)
	if err != nil {
		t.Fatal(err)
	}
	if result.Hits[0].Chunk.ID != "z" {
		t.Fatalf("meaningless embedding ties overruled stronger lexical evidence: %+v", result.Hits)
	}
	for _, hit := range result.Hits {
		if hit.SemanticRank != 1 {
			t.Fatalf("tied vectors received invented distinct ranks: %+v", hit)
		}
	}
}
