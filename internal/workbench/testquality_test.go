package workbench

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/russellbrasser/go-review/internal/intelligence"
	"github.com/russellbrasser/go-review/internal/review"
	"github.com/russellbrasser/go-review/internal/testquality"
)

func qualityFixture(t *testing.T) *Server {
	t.Helper()
	root := t.TempDir()
	text := "package example\nfunc F() int { return 1 }\n"
	r := &review.Report{Root: root, Module: "example", SourceSHA256: "source",
		Files:   []review.File{{ID: "f", Path: "f.go", Source: text, SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(text)))}},
		Symbols: []review.Symbol{{ID: "F", Name: "F", Kind: "function", Package: "example", Location: review.Location{FileID: "f", Line: 2, EndLine: 2}}}}
	s := NewReviewed(&review.RepositoryReview{Root: root, Directory: "review-one", Modules: []review.ReviewedModule{{Report: r}}})
	s.qualityStore = testquality.Store{Directory: t.TempDir()}
	s.ConfigureAgent(intelligence.Config{Enabled: true, BaseURL: "http://127.0.0.1:12345", Model: "fake"})
	return s
}

func qualityRequest(t *testing.T, s *Server, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	method := "POST"
	if body == "" {
		method = "GET"
	}
	req := httptest.NewRequest(method, "http://127.0.0.1"+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	return w
}

func TestQualityJobOwnsContextAndCancellation(t *testing.T) {
	s := qualityFixture(t)
	started := make(chan string, 1)
	s.qualityAssess = func(ctx context.Context, a *testquality.Assessment, item testquality.Responsibility, _ intelligence.Config) (testquality.Responsibility, error) {
		started <- a.Reports[0].Report.Files[0].Source
		<-ctx.Done()
		return item, ctx.Err()
	}
	w := qualityRequest(t, s, "/api/test-quality/start", `{"confirmExternal":false}`)
	if w.Code != 202 {
		t.Fatalf("launch: %d %s", w.Code, w.Body.String())
	}
	var launched struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &launched); err != nil {
		t.Fatal(err)
	}
	select {
	case text := <-started:
		if !strings.Contains(text, "return 1") {
			t.Fatal(text)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("assessment not started")
	}
	w = qualityRequest(t, s, "/api/test-quality/start", `{}`)
	if !strings.Contains(w.Body.String(), launched.ID) {
		t.Fatal("duplicate job")
	}
	// Browsing a different module/report cannot alter the job's pinned source.
	s.mu.Lock()
	s.report = &review.Report{Root: "other", Module: "other"}
	s.mu.Unlock()
	w = qualityRequest(t, s, "/api/test-quality/query", `{"assessmentID":"`+launched.ID+`","operation":"source","snapshotID":"bad","fileID":"f"}`)
	if w.Code == 200 {
		t.Fatal("unknown snapshot accepted")
	}
	w = qualityRequest(t, s, "/api/test-quality/cancel", `{"assessmentID":"`+launched.ID+`"}`)
	if w.Code != 200 {
		t.Fatalf("cancel: %s", w.Body.String())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.ShutdownTestQuality(ctx); err != nil {
		t.Fatal(err)
	}
	a, err := s.qualityStore.Load(launched.ID)
	if err != nil || a.Status != "cancelled" || a.Items[0].Status != testquality.StatusUnreviewed {
		t.Fatalf("saved: %+v %v", a, err)
	}
}

func TestQualityFinishesAndQueriesSavedEvidence(t *testing.T) {
	s := qualityFixture(t)
	s.qualityAssess = func(_ context.Context, a *testquality.Assessment, item testquality.Responsibility, _ intelligence.Config) (testquality.Responsibility, error) {
		value := 3
		item.Status = testquality.StatusScored
		for _, d := range []testquality.Dimension{testquality.AssertionStrength, testquality.RepresentativeCases, testquality.BehaviorPaths} {
			item.Ratings = append(item.Ratings, testquality.Rating{Dimension: d, State: testquality.RatingAssessed, Value: &value, Rationale: "Tests check observable behavior.", Evidence: []testquality.Citation{{SnapshotID: item.Identity.SnapshotID, FileID: "f", StartLine: 2, EndLine: 2}}})
		}
		return item, nil
	}
	w := qualityRequest(t, s, "/api/test-quality/start", `{}`)
	if w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	var launch struct {
		ID string `json:"id"`
	}
	json.Unmarshal(w.Body.Bytes(), &launch)
	s.qualityMu.Lock()
	job := s.qualityJobs[launch.ID]
	s.qualityMu.Unlock()
	select {
	case <-job.done:
	case <-time.After(5 * time.Second):
		t.Fatal("unfinished")
	}
	w = qualityRequest(t, s, "/api/test-quality/query", `{"assessmentID":"`+launch.ID+`","operation":"overview"}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"score":75`) {
		t.Fatalf("query: %d %s", w.Code, w.Body.String())
	}
	other := qualityFixture(t)
	other.qualityStore = s.qualityStore
	w = qualityRequest(t, other, "/api/test-quality/query", `{"assessmentID":"`+launch.ID+`","operation":"responsibilities"}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"symbolID":"F"`) {
		t.Fatalf("reopened: %s", w.Body.String())
	}
}
