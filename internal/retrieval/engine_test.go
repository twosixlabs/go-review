package retrieval

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/russellbrasser/go-review/internal/intelligence"
)

func engineFixture(t *testing.T, root, generation string, dimension int, count *int) *Engine {
	t.Helper()
	cfg := intelligence.EmbeddingConfig{Enabled: true, Protocol: "openai", BaseURL: "http://127.0.0.1:1", Model: "fixture", Generation: generation}
	profile := intelligence.EmbeddingProfile{Model: "fixture", Dimensions: dimension, Generation: generation}
	e, err := NewEngine(root, cfg, profile, func(ctx context.Context, inputs []string) ([][]float32, error) {
		*count += len(inputs)
		vectors := make([][]float32, len(inputs))
		for i := range vectors {
			vectors[i] = make([]float32, dimension)
			vectors[i][0] = 1
		}
		return vectors, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := e.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return e
}

func TestEngineIncrementalReuseAndQueries(t *testing.T) {
	count := 0
	e := engineFixture(t, t.TempDir(), "one", 2, &count)
	r := sampleReport()
	if err := e.Build(context.Background(), "first", r); err != nil {
		t.Fatal(err)
	}
	initial := count
	if err := e.Build(context.Background(), "second", r); err != nil {
		t.Fatal(err)
	}
	if count != initial || e.Status("second").Parsed != 0 {
		t.Fatalf("unchanged source recomputed; inputs=%d parsed=%d", count, e.Status("second").Parsed)
	}
	result, err := e.Search(context.Background(), "second", "RejectRange", "", 0, 10)
	if err != nil || len(result.Hits) == 0 || result.SnapshotID != "second" {
		t.Fatalf("retrieval %+v %v", result, err)
	}
	afterQuery := count
	if _, err = e.Search(context.Background(), "second", "RejectRange", "", 0, 10); err != nil {
		t.Fatal(err)
	}
	if count != afterQuery {
		t.Fatal("identical query re-encoded")
	}
	r2 := sampleReport()
	r2.Files[0].Source = "package sample\nfunc RejectRange(n int) bool { return n < 0 || n > 2 }\nfunc Helper(n int) bool { return RejectRange(n) }\n"
	if err = e.Build(context.Background(), "third", r2); err != nil {
		t.Fatal(err)
	}
	if count != afterQuery+1 {
		t.Fatalf("unchanged declarations re-encoded: %d -> %d", afterQuery, count)
	}
	if _, err = e.Search(context.Background(), "third", "", "excluded", 0, 10); err == nil {
		t.Fatal("excluded declaration admitted")
	}
}

func TestCacheProfileIsolationAndCorruption(t *testing.T) {
	root := t.TempDir()
	count := 0
	e := engineFixture(t, root, "one", 2, &count)
	if err := e.Build(context.Background(), "first", sampleReport()); err != nil {
		t.Fatal(err)
	}
	freshCount := 0
	fresh := engineFixture(t, root, "one", 2, &freshCount)
	if err := fresh.Build(context.Background(), "first", sampleReport()); err != nil {
		t.Fatal(err)
	}
	if freshCount != 0 {
		t.Fatal("compatible persistent vectors not reused")
	}
	otherCount := 0
	other := engineFixture(t, root, "two", 3, &otherCount)
	if err := other.Build(context.Background(), "other", sampleReport()); err != nil {
		t.Fatal(err)
	}
	if otherCount == 0 {
		t.Fatal("incompatible embedding space reused")
	}
	files, err := filepath.Glob(filepath.Join(root, "*", "vectors", "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("vector cache missing %v", err)
	}
	for _, file := range files {
		if err = os.WriteFile(file, []byte(`{"corrupt":true}`), 0600); err != nil {
			t.Fatal(err)
		}
	}
	broken := engineFixture(t, root, "one", 2, new(int))
	if err := broken.Build(context.Background(), "broken", sampleReport()); err == nil {
		t.Fatal("corruption became successful empty index")
	}
	if broken.Status("broken").State != "failed" {
		t.Fatalf("failure not surfaced %+v", broken.Status("broken"))
	}
}

func TestEngineCancellationAndClosedOwnership(t *testing.T) {
	e := engineFixture(t, t.TempDir(), "", 2, new(int))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := e.Build(ctx, "canceled", sampleReport()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost %v", err)
	}
	if err := e.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := e.Build(context.Background(), "closed", sampleReport()); err == nil {
		t.Fatal("closed owner started indexing")
	}
}

func TestCacheReturnedModelIdentityIsolated(t *testing.T) {
	root := t.TempDir()
	for _, returned := range []string{"revision-A", "revision-B"} {
		count := 0
		cfg := intelligence.EmbeddingConfig{Enabled: true, Protocol: "openai", BaseURL: "http://127.0.0.1:1", Model: "fixture", Generation: "one"}
		p := intelligence.EmbeddingProfile{Model: "fixture", ReturnedModel: returned, Generation: "one", Dimensions: 2}
		e, err := NewEngine(root, cfg, p, func(ctx context.Context, inputs []string) ([][]float32, error) {
			count += len(inputs)
			out := make([][]float32, len(inputs))
			for i := range out {
				out[i] = []float32{1, 0}
			}
			return out, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := e.Build(context.Background(), "same-source", sampleReport()); err != nil {
			t.Fatal(err)
		}
		if err := e.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		if count == 0 {
			t.Fatal("different returned model reused incompatible vectors")
		}
	}
}

func TestEngineFailedWriteIsVisible(t *testing.T) {
	root := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(root, []byte("occupied"), 0600); err != nil {
		t.Fatal(err)
	}
	e := engineFixture(t, root, "one", 2, new(int))
	if err := e.Build(context.Background(), "failed", sampleReport()); err == nil {
		t.Fatal("failed persistence became success")
	}
	if e.Status("failed").Error == "" {
		t.Fatal("lost write failure diagnostic")
	}
}
