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
	s.qualityReady = true
	return nil
}

func qualityCompact(a *testquality.Assessment) map[string]any {
	summary, _ := testquality.AggregateVersion(a.Version, a.Items)
	if a.Items == nil && a.SavedSummary != nil {
		summary = *a.SavedSummary
	}
	summary.Partial = summary.Partial || len(a.Unavailable) > 0 || a.Status != "completed"
	return map[string]any{"id": a.ID, "status": a.Status, "root": a.Root, "reviewID": a.ReviewID,
		"summary": summary, "unit": "test", "version": a.Version, "error": a.Error, "createdAt": a.CreatedAt, "updatedAt": a.UpdatedAt, "model": a.Model, "phase": a.Phase, "policy": a.Policy, "analysis": a.Analysis, "review": a.Review, "persistence": a.Persistence}
}

func (s *Server) qualityStart(w http.ResponseWriter, r *http.Request) {
	var request struct {
		ConfirmExternal bool  `json:"confirmExternal"`
		AllowExecution  *bool `json:"allowExecution,omitempty"`
	}
	if !decodeRequest(w, r, &request) {
		return
	}
	cfg := s.agentConfig()
	if err := cfg.Validate(); cfg.Enabled && err != nil {
		failure(w, 400, err.Error())
		return
	}
	if cfg.Enabled && cfg.External() && !request.ConfirmExternal {
		failure(w, 409, "Explicit external-provider consent is required: retained production and test source will leave this machine.")
		return
	}
	if state := s.retrievalOwner(); state != nil && (intelligence.Config{BaseURL: state.config.BaseURL}).External() && !request.ConfirmExternal {
		failure(w, 409, "Explicit external embedding-provider consent required: retained source will leave this machine.")
		return
	}
	allowExecution := true
	if request.AllowExecution != nil {
		allowExecution = *request.AllowExecution
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
		if job.data.ReviewID == reviewID && job.data.Version == testquality.EvidenceRubricVersion && job.data.Policy.AllowExecution == allowExecution {
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
	a.Policy = testquality.DefaultDiagnosticPolicy(allowExecution)
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
	go s.runEvidenceQuality(ctx, job, predecessor, cfg)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(202)
	_ = json.NewEncoder(w).Encode(qualityCompact(a))
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
	list, err := s.qualityStore.ListSummaries(root)
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
		list, err := s.qualityStore.ListSummaries(root)
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
	var a *testquality.Assessment
	var err error
	if job := s.qualityJobs[input.AssessmentID]; job != nil {
		a = job.data
	} else {
		a, err = s.qualityStore.Load(input.AssessmentID)
	}

	if err != nil {
		return nil, err
	}
	return testquality.Query(ctx, a, input)
}

func registerTestQualityTool(server *mcp.Server, s *Server) {
	mcp.AddTool(server, &mcp.Tool{Name: "review_test_quality",
		Description: "Saved AI test-quality judgments; never starts jobs/tests. operation: assessments (discover IDs), overview, modules, tests, test/finding (snapshotID+symbolID, findingIndex), source (snapshotID+fileID+lines), evidence (snapshotID+evidenceOperation+args; catalog/symbols/test-runs/source/search/evidence/coverage/dataflow/logical/document/packages). symbolID identifies the TEST declaration; production IDs only supply supporting evidence. Page with offset/limit, follow nextOffset.",
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
