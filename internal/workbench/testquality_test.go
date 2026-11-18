package workbench

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/russellbrasser/go-review/internal/intelligence"
	"github.com/russellbrasser/go-review/internal/review"
	"github.com/russellbrasser/go-review/internal/testquality"
)

func qualityFixture(t *testing.T) *Server {
	t.Helper()
	root := t.TempDir()
	text := "package example\nfunc F() int { return 1 }\n"
	tests := "package example\nimport \"testing\"\nfunc TestF(t *testing.T) { if F()!=1 { t.Fatal(\"wrong\") } }\n"
	r := &review.Report{Root: root, Module: "example", SourceSHA256: "source",
		Files: []review.File{{ID: "f", Package: "example", Path: "f.go", Source: text, SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(text)))}},
		TestSources: []review.TestSource{{File: review.File{ID: "test", Package: "example", Path: "f_test.go", Source: tests, SHA256: testquality.Digest([]byte(tests))},
			Declarations: []review.TestDeclaration{{ID: "TestF", Name: "TestF", Kind: "test", Location: review.Location{FileID: "test", Line: 3, EndLine: 3}}}}},
		Symbols: []review.Symbol{{ID: "F", Name: "F", Kind: "function", Package: "example", Location: review.Location{FileID: "f", Line: 2, EndLine: 2}}}}
	s := NewReviewed(&review.RepositoryReview{Root: root, Directory: "review-one", Modules: []review.ReviewedModule{{Report: r}}})
	s.qualityStore = testquality.Store{Directory: t.TempDir()}
	s.ConfigureAgent(intelligence.Config{Enabled: true, BaseURL: "http://127.0.0.1:12345", Model: "fake"})
	return s
}

func TestQualityProviderFailureStopsJobWithoutGradingTests(t *testing.T) {
	var requests atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "provider unavailable", http.StatusServiceUnavailable)
	}))
	defer provider.Close()
	s := qualityFixture(t)
	s.ConfigureAgent(intelligence.Config{Enabled: true, BaseURL: provider.URL, Model: "fixture"})
	start := qualityRequest(t, s, "/api/test-quality/start", `{"allowExecution":false}`)
	if start.Code != 202 {
		t.Fatal(start.Body.String())
	}
	var response struct{ ID string }
	if err := json.Unmarshal(start.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	s.qualityMu.Lock()
	job := s.qualityJobs[response.ID]
	s.qualityMu.Unlock()
	defer job.cancel()
	select {
	case <-job.done:
	case <-time.After(5 * time.Second):
		t.Fatal("provider failure did not stop the job")
	}
	saved, err := s.qualityStore.Load(response.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Status != "failed" || !strings.Contains(saved.Error, "503") || requests.Load() != 1 {
		t.Fatalf("provider failure was hidden or repeated: status=%s error=%s requests=%d", saved.Status, saved.Error, requests.Load())
	}
	if len(saved.Items) != 1 || saved.Items[0].Status != testquality.StatusUnreviewed {
		t.Fatalf("transport failure was stored as a test judgment: %+v", saved.Items)
	}
}

func TestQualitySavedNavigationReusesVerifiedEvidence(t *testing.T) {
	s := qualityFixture(t)
	a, err := s.qualityStore.Create(t.Context(), s.review, "", "")
	if err != nil {
		t.Fatal(err)
	}
	a.Status = "completed"
	if err := s.qualityStore.Save(a); err != nil {
		t.Fatal(err)
	}
	input := testquality.QueryInput{AssessmentID: a.ID, Operation: "overview"}
	if _, err := s.queryTestQuality(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	// A browsing session owns the verified immutable snapshot, just as a live
	// job does. Subsequent drill-down must not re-read its report records.
	dir := filepath.Join(s.qualityStore.Directory, a.ID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != "assessment.json" {
			if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil {
				t.Fatal(err)
			}
		}
	}
	input.Operation = "tests"
	data, err := s.queryTestQuality(t.Context(), input)
	if err != nil || !strings.Contains(string(data), "TestF") {
		t.Fatalf("drill-down reloaded immutable evidence: %s %v", data, err)
	}
	if _, err := s.qualityStore.Load(a.ID); err == nil {
		t.Fatal("fresh archive opens must still verify retained evidence")
	}
}

func TestQualityLargeSavedResultThroughHTTPAndMCP(t *testing.T) {
	s := qualityFixture(t)
	a, err := s.qualityStore.Create(t.Context(), s.review, "", "")
	if err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("retained advice ", 3000)
	a.Items[0].Reason = long
	if err := s.qualityStore.Save(a); err != nil {
		t.Fatal(err)
	}
	input := testquality.QueryInput{AssessmentID: a.ID, Operation: "test", SnapshotID: a.Items[0].Identity.SnapshotID, SymbolID: a.Items[0].Identity.SymbolID}
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	response := qualityRequest(t, s, "/api/test-quality/query", string(body))
	if response.Code != 200 || !strings.Contains(response.Body.String(), long) {
		t.Fatalf("HTTP lost retained advice: status=%d", response.Code)
	}
	st, ct := mcp.NewInMemoryTransports()
	server, err := s.newChatService().server().Connect(t.Context(), st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "large-quality-result", Version: "1"}, nil)
	session, err := client.Connect(t.Context(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "review_test_quality", Arguments: input})
	if err != nil || result.IsError {
		t.Fatalf("MCP lost retained advice: %+v %v", result, err)
	}
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil || !strings.Contains(string(encoded), long) {
		t.Fatalf("MCP response lost retained advice: %v", err)
	}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []intelligence.Message `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		if !strings.Contains(request.Messages[len(request.Messages)-1].Content, long) {
			t.Error("chat lost retained advice")
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Retained advice.\"},\"finish_reason\":\"stop\"}]}\n\n")
	}))
	defer provider.Close()
	err = s.runChat(t.Context(), intelligence.Config{Enabled: true, BaseURL: provider.URL, Model: "fixture"}, chatRequest{Message: "Explain the saved result", AssessmentID: a.ID, Context: body}, nil, s.newChatService(), func(chatEvent) error { return nil })
	if err != nil {
		t.Fatalf("chat could not open retained advice: %v", err)
	}
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
		for _, d := range []testquality.Dimension{testquality.AssertionStrength, testquality.Craftsmanship} {
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
	w = qualityRequest(t, other, "/api/test-quality/query", `{"assessmentID":"`+launch.ID+`","operation":"tests"}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"symbolID":"TestF"`) {
		t.Fatalf("reopened: %s", w.Body.String())
	}
	previousID := launch.ID
	s.review.Modules[0].Report.GeneratedAt = time.Now()
	var repeated atomic.Int32
	s.qualityAssess = func(_ context.Context, _ *testquality.Assessment, item testquality.Responsibility, _ intelligence.Config) (testquality.Responsibility, error) {
		repeated.Add(1)
		return item, fmt.Errorf("unchanged saved work was unnecessarily reassessed")
	}
	w = qualityRequest(t, s, "/api/test-quality/start", `{}`)
	if w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &launch); err != nil {
		t.Fatal(err)
	}
	s.qualityMu.Lock()
	job = s.qualityJobs[launch.ID]
	s.qualityMu.Unlock()
	select {
	case <-job.done:
	case <-time.After(5 * time.Second):
		t.Fatal("new assessment did not finish")
	}
	saved, err := s.qualityStore.Load(launch.ID)
	if err != nil {
		t.Fatal(err)
	}
	// This fixture has no collective provider: saved individual work survives
	// its failure, but must not make the new assessment falsely complete.
	if launch.ID == previousID || saved.Status != "failed" || !strings.Contains(saved.Error, "model request") || repeated.Load() != 0 ||
		saved.Items[0].Status != testquality.StatusScored || saved.Items[0].ReusedFrom == nil ||
		saved.Items[0].ReusedFrom.AssessmentID != previousID {
		t.Fatalf("new job did not preserve validated saved progress: %+v", saved)
	}
}
