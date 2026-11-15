package retrieval

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/russellbrasser/go-review/internal/intelligence"
)

func TestEmbeddingLive(t *testing.T) {
	if os.Getenv("GO_REVIEW_EMBEDDING_LIVE") != "1" {
		t.Skip("explicit embedding-live selector required")
	}
	path, err := intelligence.EmbeddingConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	config, err := intelligence.LoadEmbeddingConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if !config.Enabled {
		t.Skip("configured embeddings are absent or disabled; no model quality validation performed")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	provider := intelligence.EmbeddingProvider{Config: config}
	profile, err := provider.Discover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	observations, err := Trials(ctx, config, func(ctx context.Context, inputs []string) ([][]float32, error) {
		return provider.EmbedForProfile(ctx, inputs, profile)
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Configured model %s; dimensions %d; metadata %s; synthetic observations (not certification): %+v", profile.Model, profile.Dimensions, profile.MetadataState, observations)
}
