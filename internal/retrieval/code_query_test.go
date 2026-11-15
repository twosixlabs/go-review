package retrieval

import (
	"context"
	"testing"
)

func TestEngineCodeQueryReusesIdenticalIndexedInput(t *testing.T) {
	count := 0
	engine := engineFixture(t, t.TempDir(), "one", 2, &count)
	if err := engine.Build(context.Background(), "snapshot", sampleReport()); err != nil {
		t.Fatal(err)
	}
	before := count
	if _, err := engine.Search(context.Background(), "snapshot", "", "reject", 0, 10); err != nil {
		t.Fatal(err)
	}
	if count != before {
		t.Fatalf("identical indexed code was encoded again: %d -> %d", before, count)
	}
}
