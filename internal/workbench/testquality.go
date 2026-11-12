package workbench

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/russellbrasser/go-review/internal/intelligence"
	"github.com/russellbrasser/go-review/internal/review"
	"github.com/russellbrasser/go-review/internal/testquality"
)

type qualityJob struct {
	data   *testquality.Assessment
	cancel context.CancelFunc
	done   chan struct{}
}

var qualityRecovery struct {
	sync.Once
	err error
}

// qualityInit runs with qualityMu held. Startup recovery is process-wide for
// the default archive; additional UI/MCP sessions must not interrupt live jobs.
func (s *Server) qualityInit() error {
	if s.qualityReady {
		return nil
	}
	if s.qualityStore.Directory == "" {
		store, err := testquality.DefaultStore()
		if err != nil {
			return err
		}
		s.qualityStore = store
		qualityRecovery.Do(func() { qualityRecovery.err = store.Recover() })
		if qualityRecovery.err != nil {
			return qualityRecovery.err
		}
	} else if err := s.qualityStore.Recover(); err != nil {
		return err
	}
	s.qualityJobs = make(map[string]*qualityJob)
	if s.qualityAssess == nil {
		s.qualityAssess = testquality.Assess
	}
	s.qualityReady = true
	return nil
}

func qualityCompact(a *testquality.Assessment) map[string]any {
	summary, _ := testquality.Aggregate(a.Items)
	summary.Partial = summary.Partial || len(a.Unavailable) > 0 || a.Status != "completed"
	return map[string]any{"id": a.ID, "status": a.Status, "root": a.Root, "reviewID": a.ReviewID,
		"summary": summary, "error": a.Error, "createdAt": a.CreatedAt, "updatedAt": a.UpdatedAt, "model": a.Model}
}

func (s *Server) qualityStart(w http.ResponseWriter, r *http.Request) {
	var request struct {
		ConfirmExternal bool `json:"confirmExternal"`
	}
	if !decodeRequest(w, r, &request) {
		return
	}
	cfg := s.agentConfig()
	if !cfg.Enabled {
		failure(w, 409, "Configure and enable the review assistant before starting Test Quality.")
		return
	}
	if err := cfg.Validate(); err != nil {
		failure(w, 400, err.Error())
		return
	}
	if cfg.External() && !request.ConfirmExternal {
		failure(w, 409, "Explicit external-provider consent is required: retained production and test source will leave this machine.")
		return
	}
	s.qualityMu.Lock()
	defer s.qualityMu.Unlock()
	if s.qualityClosed {
		failure(w, 409, "Test Quality is shutting down")
		return
	}
	if err := s.qualityInit(); err != nil {
		failure(w, 500, err.Error())
		return
	}
	s.mu.RLock()
	run := s.review
	if run == nil && s.report != nil {
		run = &review.RepositoryReview{Root: s.report.Root, Modules: []review.ReviewedModule{{Directory: s.report.Root, Report: s.report}}}
	}
	if run == nil {
		s.mu.RUnlock()
		failure(w, 409, "Open retained project evidence first.")
		return
	}
	// Compute content identity as well as directory: Analyze-only reviews can
	// have no run directory, and replacement must not attach to an older job.
	encoded, err := json.Marshal(run)
	if err != nil {
		s.mu.RUnlock()
		failure(w, 500, err.Error())
		return
	}
	reviewID := testquality.Digest(encoded)
	active := 0
	for _, job := range s.qualityJobs {
		if job.data.Status != "queued" && job.data.Status != "running" {
			continue
		}
		active++
		if job.data.ReviewID == reviewID {
			s.mu.RUnlock()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(202)
			_ = json.NewEncoder(w).Encode(qualityCompact(job.data))
			return
		}
	}
	if active >= 3 {
		s.mu.RUnlock()
		failure(w, 409, "Test Quality queue is full (one running, two waiting).")
		return
	}
	a, err := s.qualityStore.Create(r.Context(), run, cfg.BaseURL, cfg.Model)
	s.mu.RUnlock()
	if err != nil {
		failure(w, 422, "pin Test Quality evidence: "+err.Error())
		return
	}
	a.ReviewID = reviewID
	if err := s.qualityStore.Save(a); err != nil {
		failure(w, 500, err.Error())
		return
	}
	// Only the workbench owns this context. Returning the HTTP response, closing
	// a tab or navigating evidence must not cancel a provider call.
	ctx, cancel := context.WithCancel(context.Background())
	job := &qualityJob{data: a, cancel: cancel, done: make(chan struct{})}
	predecessor := s.qualityTail
	s.qualityTail = job.done
	s.qualityJobs[a.ID] = job
	go s.runQuality(ctx, job, predecessor, cfg)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(202)
	_ = json.NewEncoder(w).Encode(qualityCompact(a))
}

func (s *Server) runQuality(ctx context.Context, job *qualityJob, previous <-chan struct{}, cfg intelligence.Config) {
	defer close(job.done)
	defer job.cancel()
	if previous != nil {
		// A cancelled queued job must still wait before closing done; otherwise
		// its successor could overtake the still-running predecessor.
		<-previous
	}
	s.qualityMu.Lock()
	if ctx.Err() == nil {
		job.data.Status = "running"
	} else {
		job.data.Status = "cancelled"
	}
	err := s.qualityStore.Save(job.data)
	s.qualityMu.Unlock()
	var assessmentErr error
	failed := 0
	if err == nil && ctx.Err() == nil {
		for i := range job.data.Items {
			s.qualityMu.Lock()
			item := job.data.Items[i]
			s.qualityMu.Unlock()
			if item.Status != testquality.StatusUnreviewed {
				continue
			}
			if ctx.Err() != nil {
				break
			}
			// Reports are immutable. The agent gets only a shallow metadata copy,
			// not the evolving results slice exposed to status/query readers.
			input := &testquality.Assessment{ID: job.data.ID, Reports: job.data.Reports}
			result, callErr := s.qualityAssess(ctx, input, item, cfg)
			if ctx.Err() != nil {
				break
			}
			if callErr != nil {
				result = item
				result.Status = testquality.StatusInsufficient
				result.Reason = callErr.Error()
				failed++
				if assessmentErr == nil {
					assessmentErr = fmt.Errorf("%s: %w", item.Name, callErr)
				}
			}
			s.qualityMu.Lock()
			job.data.Items[i] = result
			err = s.qualityStore.Save(job.data)
			s.qualityMu.Unlock()
			if err != nil {
				break
			}
		}
	}
	s.qualityMu.Lock()
	defer s.qualityMu.Unlock()
	if err != nil {
		job.data.Status = "failed"
		job.data.Error = "persist assessment: " + err.Error()
	} else if ctx.Err() != nil {
		job.data.Status = "cancelled"
		job.data.Error = "Assessment cancelled; completed results were retained."
	} else {
		summary, summaryErr := testquality.Aggregate(job.data.Items)
		switch {
		case summaryErr != nil:
			job.data.Status = "failed"
			job.data.Error = summaryErr.Error()
		case summary.Partial || len(job.data.Unavailable) > 0:
			job.data.Status = "partial"
		default:
			job.data.Status = "completed"
		}
		if assessmentErr != nil {
			job.data.Error = fmt.Sprintf("%d responsibilities could not be assessed; inspect their retained reasons. First: %v", failed, assessmentErr)
		}
	}
	if saveErr := s.qualityStore.Save(job.data); saveErr != nil {
		job.data.Status = "failed"
		job.data.Error = "persist final assessment: " + saveErr.Error()
	}
}

func (s *Server) qualityStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		failure(w, 405, "GET required")
		return
	}
	s.qualityMu.Lock()
	defer s.qualityMu.Unlock()
	if err := s.qualityInit(); err != nil {
		failure(w, 500, err.Error())
		return
	}
	root := r.URL.Query().Get("root")
	if root == "" {
		s.mu.RLock()
		if s.review != nil {
			root = s.review.Root
		} else if s.report != nil {
			root = s.report.Root
		}
		s.mu.RUnlock()
	}
	list, err := s.qualityStore.List(root)
	if err != nil {
		failure(w, 500, err.Error())
		return
	}
	sort.Slice(list, func(i, j int) bool { return list[i].CreatedAt.After(list[j].CreatedAt) })
	rows := []map[string]any{}
	for _, a := range list {
		if job := s.qualityJobs[a.ID]; job != nil {
			a = job.data
		}
		rows = append(rows, qualityCompact(a))
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"assessments": rows, "queueLimit": 3})
}

func (s *Server) qualityCancel(w http.ResponseWriter, r *http.Request) {
	var request struct {
		AssessmentID string `json:"assessmentID"`
	}
	if !decodeRequest(w, r, &request) {
		return
	}
	s.qualityMu.Lock()
	defer s.qualityMu.Unlock()
	job := s.qualityJobs[request.AssessmentID]
	if job == nil {
		failure(w, 404, "No active job with that assessment ID")
		return
	}
	if job.data.Status == "running" || job.data.Status == "queued" {
		job.cancel()
		if job.data.Status == "queued" {
			// Publish cancellation immediately, but retain the predecessor wait
			// before closing done so later jobs cannot overtake the active one.
			job.data.Status = "cancelled"
			job.data.Error = "Assessment cancelled while queued."
			if err := s.qualityStore.Save(job.data); err != nil {
				job.data.Error = "persist queued cancellation: " + err.Error()
				failure(w, 500, job.data.Error)
				return
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(qualityCompact(job.data))
}

func (s *Server) queryTestQuality(ctx context.Context, input testquality.QueryInput) (json.RawMessage, error) {
	s.qualityMu.Lock()
	defer s.qualityMu.Unlock()
	if err := s.qualityInit(); err != nil {
		return nil, err
	}
	if input.Operation == "assessments" {
		s.mu.RLock()
		root := ""
		if s.review != nil {
			root = s.review.Root
		} else if s.report != nil {
			root = s.report.Root
		}
		s.mu.RUnlock()
		list, err := s.qualityStore.List(root)
		if err != nil {
			return nil, err
		}
		sort.Slice(list, func(i, j int) bool { return list[i].CreatedAt.After(list[j].CreatedAt) })
		if input.Offset < 0 || input.Offset > len(list) || input.Limit < 0 || input.Limit > 100 {
			return nil, fmt.Errorf("invalid assessment page")
		}
		limit := input.Limit
		if limit == 0 {
			limit = 20
		}
		end := min(len(list), input.Offset+limit)
		var next *int
		if end < len(list) {
			next = &end
		}
		rows := []map[string]any{}
		for _, a := range list[input.Offset:end] {
			if job := s.qualityJobs[a.ID]; job != nil {
				a = job.data
			}
			rows = append(rows, qualityCompact(a))
		}
		return json.Marshal(map[string]any{"items": rows, "total": len(list), "nextOffset": next})
	}
	a, err := s.qualityStore.Load(input.AssessmentID)
	if job := s.qualityJobs[input.AssessmentID]; job != nil {
		a = job.data
		err = nil
	}

	if err != nil {
		return nil, err
	}
	return testquality.Query(ctx, a, input)
}

func registerTestQualityTool(server *mcp.Server, s *Server) {
	mcp.AddTool(server, &mcp.Tool{Name: "review_test_quality",
		Description: "Saved AI test-quality judgments; never starts jobs/tests. operation: assessments (discover IDs), overview, modules, responsibilities, responsibility/finding (snapshotID+symbolID, findingIndex), source (snapshotID+fileID+lines), evidence (snapshotID+evidenceOperation+args; catalog/source/search/evidence/dataflow/logical/document/packages). IDs belong to this archive, not Application. Page with offset/limit, follow nextOffset.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: boolPointer(false)}},
		func(ctx context.Context, _ *mcp.CallToolRequest, input testquality.QueryInput) (*mcp.CallToolResult, map[string]any, error) {
			data, err := s.queryTestQuality(ctx, input)
			if err != nil {
				return nil, nil, err
			}
			return nil, map[string]any{"data": data}, nil
		})
}

func (s *Server) qualityQuery(w http.ResponseWriter, r *http.Request) {
	var input testquality.QueryInput
	if !decodeRequest(w, r, &input) {
		return
	}
	data, err := s.queryTestQuality(r.Context(), input)
	if err != nil {
		failure(w, 422, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}

func (s *Server) ShutdownTestQuality(ctx context.Context) error {
	s.qualityMu.Lock()
	s.qualityClosed = true
	jobs := []*qualityJob{}
	for _, job := range s.qualityJobs {
		job.cancel()
		jobs = append(jobs, job)
	}
	s.qualityMu.Unlock()
	for _, job := range jobs {
		select {
		case <-job.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
