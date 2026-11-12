package workbench

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/russellbrasser/go-review/internal/intelligence"
	"github.com/russellbrasser/go-review/internal/testquality"
)

func TestQualityQueueCancellationDoesNotReleaseRunningPredecessor(t *testing.T) {
	s := qualityFixture(t)
	entered := make(chan string, 3)
	release := make(chan struct{})
	s.qualityAssess = func(ctx context.Context, a *testquality.Assessment, item testquality.Responsibility, cfg intelligence.Config) (testquality.Responsibility, error) {
		entered <- a.ID
		select {
		case <-release:
		case <-ctx.Done():
			return item, ctx.Err()
		}
		item.Status = testquality.StatusInsufficient
		item.Reason = "No retained test source."
		return item, nil
	}
	start := func(revision string) string {
		t.Helper()
		s.mu.Lock()
		s.review.Directory = revision
		s.mu.Unlock()
		w := qualityRequest(t, s, "/api/test-quality/start", `{}`)
		if w.Code != 202 {
			t.Fatal(w.Body.String())
		}
		var a struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &a); err != nil {
			t.Fatal(err)
		}
		return a.ID
	}
	first := start("one")
	select {
	case id := <-entered:
		if id != first {
			t.Fatal(id)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first job not started")
	}
	second := start("two")
	w := qualityRequest(t, s, "/api/test-quality/cancel", `{"assessmentID":"`+second+`"}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"status":"cancelled"`) {
		t.Fatalf("queued cancellation not visible: %s", w.Body.String())
	}
	third := start("three")
	select {
	case id := <-entered:
		t.Fatalf("job %s entered while %s was running (third %s)", id, first, third)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.ShutdownTestQuality(ctx); err != nil {
		t.Fatal(err)
	}
}
