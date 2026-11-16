package workbench

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/russellbrasser/go-review/internal/intelligence"
	"github.com/russellbrasser/go-review/internal/review"
	"github.com/russellbrasser/go-review/internal/testquality"
)

func TestQualityAgentLiveRetained(t *testing.T) {
	if os.Getenv("GO_REVIEW_QUALITY_LIVE") != "1" {
		t.Skip("explicit configured-local-model acceptance")
	}
	directory := os.Getenv("GO_REVIEW_QUALITY_TRACE_DIR")
	archive, id, pkg := os.Getenv("GO_REVIEW_QUALITY_ARCHIVE"), os.Getenv("GO_REVIEW_QUALITY_ASSESSMENT"), os.Getenv("GO_REVIEW_QUALITY_PACKAGE")
	if directory == "" || archive == "" || id == "" || pkg == "" {
		t.Fatal("trace directory, archive, assessment and package are required")
	}
	cfgPath, err := intelligence.ConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := intelligence.LoadConfig(cfgPath)
	if err != nil || !cfg.Enabled || cfg.External() {
		t.Fatalf("configured local model required: %v", err)
	}
	original, err := (testquality.Store{Directory: archive}).Load(id)
	if err != nil {
		t.Fatal(err)
	}
	var selected *review.Report
	testCount := 0
	for _, pinned := range original.Reports {
		copy := *pinned.Report
		copy.TestSources = append([]review.TestSource(nil), copy.TestSources...)
		count := 0
		for i := range copy.TestSources {
			source := &copy.TestSources[i]
			if source.File.Package != pkg {
				source.Declarations = nil
			} else {
				count += len(source.Declarations)
			}
		}
		if count > 0 {
			if selected != nil {
				t.Fatal("package occurs in multiple snapshots; select a unique package")
			}
			selected, testCount = &copy, count
		}
	}
	if selected == nil || testCount > 12 {
		t.Fatalf("select a bounded package with 1-12 declarations, found %d", testCount)
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	trace, err := os.CreateTemp(directory, "retained-requests-*.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer trace.Close()
	target, err := url.Parse(cfg.BaseURL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	var traceMu sync.Mutex
	transport := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			http.Error(w, err.Error(), 500)
			return
		}
		r.Body.Close()
		traceMu.Lock()
		err = json.NewEncoder(trace).Encode(map[string]any{"time": time.Now(), "request": json.RawMessage(body)})
		traceMu.Unlock()
		if err != nil {
			t.Error(err)
			http.Error(w, err.Error(), 500)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		proxy.ServeHTTP(w, r)
	}))
	defer transport.Close()
	cfg.BaseURL = transport.URL
	server := New(selected)
	server.qualityStore = testquality.Store{Directory: filepath.Join(directory, "retained-archive")}
	server.ConfigureAgent(cfg)
	start := qualityRequest(t, server, "/api/test-quality/start", `{"allowExecution":false}`)
	if start.Code != 202 {
		t.Fatal(start.Body.String())
	}
	var launched struct{ ID string }
	if err := json.Unmarshal(start.Body.Bytes(), &launched); err != nil {
		t.Fatal(err)
	}
	server.qualityMu.Lock()
	job := server.qualityJobs[launched.ID]
	server.qualityMu.Unlock()
	defer func() { job.cancel(); <-job.done }()
	t.Logf("assessment=%s package=%s declarations=%d trajectory=%s", launched.ID, pkg, testCount, trace.Name())
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
running:
	for {
		select {
		case <-job.done:
			break running
		case <-ctx.Done():
			job.cancel()
			<-job.done
			break running
		case <-ticker.C:
			server.qualityMu.Lock()
			summary, err := testquality.AggregateVersion(job.data.Version, job.data.Items)
			activity, requests := job.data.Activity, job.data.Review.Requests
			server.qualityMu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("scored=%d unreviewed=%d requests=%d activity=%s", summary.Counts.Scored, summary.Counts.Unreviewed, requests, activity)
		}
	}
	saved, err := server.qualityStore.Load(launched.ID)
	if err != nil {
		t.Fatal(err)
	}
	scored := 0
	for _, item := range saved.Items {
		if item.Status == testquality.StatusScored {
			scored++
			for _, rating := range item.Ratings {
				if rating.Rationale == "" || len(rating.Evidence) == 0 {
					t.Fatalf("unexplained or uncited advice: %+v", rating)
				}
			}
		}
	}
	t.Logf("status=%s scored=%d requests=%d error=%s", saved.Status, scored, saved.Review.Requests, saved.Error)
	if scored == 0 || saved.Status == "failed" {
		t.Fatalf("live agent failed advisory delivery: %s %s", saved.Status, saved.Error)
	}
	if len(saved.Diagnostics) != 0 {
		t.Fatal("execution-disabled review recorded fresh diagnostics")
	}
	for _, operation := range []string{"overview", "tests", "suite", "suite-groups", "indicators"} {
		if _, err := server.queryTestQuality(t.Context(), testquality.QueryInput{AssessmentID: saved.ID, Operation: operation}); err != nil {
			t.Fatalf("saved %s unavailable: %v", operation, err)
		}
	}
}
