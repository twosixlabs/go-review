package retrieval

import (
	"context"
	"testing"

	"github.com/russellbrasser/go-review/internal/intelligence"
)

func TestTrialsConstantVectorsRemainTied(t *testing.T) {
	config := intelligence.EmbeddingConfig{Enabled: true, Protocol: "openai", BaseURL: "http://localhost:1234/v1", Model: "configured", InputBytes: 256, BatchBytes: 256}
	observations, err := Trials(context.Background(), config, func(_ context.Context, inputs []string) ([][]float32, error) {
		size := 0
		out := make([][]float32, len(inputs))
		for i, input := range inputs {
			size += len(input)
			if len(input) > 256 {
				t.Fatal("input budget exceeded")
			}
			out[i] = []float32{1, 0}
		}
		if size > 256 {
			t.Fatal("batch budget exceeded")
		}
		return out, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(observations) != 12 {
		t.Fatalf("observations = %d", len(observations))
	}
	for _, observation := range observations {
		if observation.Ties != 3 || observation.Rank != 1 {
			t.Fatalf("constant vectors falsely separated candidates: %+v", observation)
		}
	}
}
