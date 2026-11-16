package workbench

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/russellbrasser/go-review/internal/intelligence"
	"github.com/russellbrasser/go-review/internal/review"
	"github.com/russellbrasser/go-review/internal/testquality"
)

func TestQualityStatusAvailableWhileEvidenceIsBeingPinned(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("named pipe fixture requires a Unix system")
	}
	s := qualityFixture(t)
	s.ConfigureAgent(intelligence.Config{})
	dir := t.TempDir()
	pipe := filepath.Join(dir, "events")
	if err := exec.Command("mkfifo", pipe).Run(); err != nil {
		t.Fatal(err)
	}
	s.review.Modules[0].Report.UnitEvidence = &review.UnitEvidence{Directory: dir, Runs: []review.UnitTestRun{{EventsPath: pipe}}}
	start := make(chan int, 1)
	go func() {
		w := qualityRequest(t, s, "/api/test-quality/start", `{"allowExecution":false}`)
		start <- w.Code
	}()
	writers := make(chan *os.File, 1)
	go func() {
		f, err := os.OpenFile(pipe, os.O_WRONLY, 0600)
		if err != nil {
			t.Error(err)
		}
		writers <- f
	}()
	var writer *os.File
	select {
	case writer = <-writers:
	case <-time.After(5 * time.Second):
		t.Fatal("assessment did not begin reading retained evidence")
	}
	if writer == nil {
		t.Fatal("could not open test pipe")
	}
	reviewUpdated := make(chan struct{})
	go func() {
		s.mu.Lock()
		s.review.Directory = "updated"
		s.mu.Unlock()
		close(reviewUpdated)
	}()
	select {
	case <-reviewUpdated:
	case <-time.After(time.Second):
		t.Error("review update blocked while assessment was pinning evidence")
	}
	status := make(chan int, 1)
	go func() { status <- qualityRequest(t, s, "/api/test-quality/status", "").Code }()
	var statusCode int
	select {
	case statusCode = <-status:
	case <-time.After(time.Second):
	}
	writer.Close()
	<-reviewUpdated
	if statusCode == 0 {
		statusCode = <-status
		t.Error("status blocked while assessment was pinning evidence")
	}
	if statusCode != 200 {
		t.Errorf("status failed: %d", statusCode)
	}
	if code := <-start; code != 202 {
		t.Errorf("assessment start failed: %d", code)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.ShutdownTestQuality(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestQualityShutdownWaitsForEvidencePinning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("named pipe fixture requires a Unix system")
	}
	s := qualityFixture(t)
	dir := t.TempDir()
	pipe := filepath.Join(dir, "events")
	if err := exec.Command("mkfifo", pipe).Run(); err != nil {
		t.Fatal(err)
	}
	s.review.Modules[0].Report.UnitEvidence = &review.UnitEvidence{Directory: dir, Runs: []review.UnitTestRun{{EventsPath: pipe}}}
	start := make(chan int, 1)
	go func() { start <- qualityRequest(t, s, "/api/test-quality/start", `{"allowExecution":false}`).Code }()
	writers := make(chan *os.File, 1)
	go func() {
		f, err := os.OpenFile(pipe, os.O_WRONLY, 0600)
		if err != nil {
			t.Error(err)
		}
		writers <- f
	}()
	var writer *os.File
	select {
	case writer = <-writers:
	case <-time.After(5 * time.Second):
		t.Fatal("assessment did not begin reading retained evidence")
	}
	if writer == nil {
		t.Fatal("could not open test pipe")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stopped := make(chan error, 1)
	go func() { stopped <- s.ShutdownTestQuality(ctx) }()
	returned := false
	select {
	case err := <-stopped:
		returned = true
		t.Errorf("shutdown returned before evidence pinning finished: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	writer.Close()
	if code := <-start; code != 409 {
		t.Errorf("start after shutdown: got %d, want 409", code)
	}
	if !returned {
		select {
		case err := <-stopped:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
}

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
