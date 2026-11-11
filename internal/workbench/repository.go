package workbench

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/russellbrasser/go-review/internal/review"
)

type moduleSummary struct {
	Directory  string                  `json:"directory"`
	Module     string                  `json:"module,omitempty"`
	Error      string                  `json:"error,omitempty"`
	Exclusions []review.ScopeExclusion `json:"exclusions,omitempty"`
}

type runSummary struct {
	Root       string                  `json:"root"`
	Directory  string                  `json:"directory"`
	Status     string                  `json:"status"`
	Modules    []moduleSummary         `json:"modules"`
	Steps      []review.ReviewStep     `json:"steps"`
	Scope      review.ReviewScope      `json:"scope"`
	Exclusions []review.ScopeExclusion `json:"exclusions"`
	FinishedAt time.Time               `json:"finishedAt"`
}

func summarizeRun(run *review.RepositoryReview) *runSummary {
	if run == nil {
		return nil
	}
	summary := &runSummary{Root: run.Root, Directory: run.Directory, Status: run.Status, Modules: []moduleSummary{}, Steps: run.Steps, Scope: run.Scope, Exclusions: run.Exclusions, FinishedAt: run.FinishedAt}
	for _, m := range run.Modules {
		item := moduleSummary{Directory: m.Directory, Error: m.Error}
		if m.Report != nil {
			item.Module = m.Report.Module
			item.Exclusions = m.Report.Exclusions
		}
		summary.Modules = append(summary.Modules, item)
	}
	return summary
}

func NewReviewed(run *review.RepositoryReview) *Server {
	s := New(nil)
	s.review = run
	for _, m := range run.Modules {
		review.PrepareRecordedObservations(m.Report)
		review.PrepareSystemFlow(m.Report)
	}
	for _, m := range run.Modules {
		if m.Report != nil {
			s.report = m.Report
			break
		}
	}
	return s
}

func (s *Server) repositoryReview(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		s.mu.RLock()
		state := struct {
			Running  bool                  `json:"running"`
			Progress review.ReviewProgress `json:"progress"`
			Run      *runSummary           `json:"run"`
			Error    string                `json:"error,omitempty"`
		}{s.reviewRunning, s.progress, summarizeRun(s.review), s.reviewError}
		s.mu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(state)
		return
	}
	var request struct {
		Directory string `json:"directory"`
		Execute   bool   `json:"execute"`
	}
	if !decodeRequest(w, r, &request) {
		return
	}
	if !request.Execute || request.Directory == "" {
		failure(w, 400, "repository directory and explicit execution consent are required")
		return
	}
	if !s.collect.TryLock() {
		failure(w, 409, "collection already running")
		return
	}
	defer s.collect.Unlock()
	s.mu.Lock()
	started := time.Now().UTC()
	s.reviewRunning, s.progress = true, review.ReviewProgress{Phase: "Discovering Go modules", Root: request.Directory, StartedAt: started, PhaseStartedAt: started}
	s.reviewError = ""
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.reviewRunning = false
		s.mu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(r.Context(), time.Hour)
	defer cancel()
	run, err := review.ReviewRepository(ctx, request.Directory, "", func(p review.ReviewProgress) {
		s.mu.Lock()
		s.progress = p
		s.mu.Unlock()
	})
	if err != nil {
		s.mu.Lock()
		s.reviewError = err.Error()
		s.mu.Unlock()
		failure(w, 422, err.Error())
		return
	}
	s.mu.Lock()
	s.review = run
	s.report = nil
	for _, m := range run.Modules {
		if m.Report != nil {
			s.report = m.Report
			break
		}
	}
	report := s.report
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		Run    *runSummary    `json:"run"`
		Report *review.Report `json:"report"`
	}{summarizeRun(run), report})
}

func (s *Server) selectReviewModule(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Directory  string `json:"directory"`
		SnapshotID string `json:"snapshotID"`
	}
	if !decodeRequest(w, r, &request) {
		return
	}
	if !s.collect.TryLock() {
		failure(w, 409, "collection already running")
		return
	}
	defer s.collect.Unlock()
	if request.SnapshotID != "" {
		snapshot, _, err := s.newChatService().snapshot(request.SnapshotID, "")
		if err != nil {
			failure(w, 409, err.Error())
			return
		}
		s.publish(w, snapshot.report)
		return
	}
	s.mu.RLock()
	run := s.review
	s.mu.RUnlock()
	if run != nil {
		for _, m := range run.Modules {
			if m.Directory == request.Directory && m.Report != nil {
				s.publish(w, m.Report)
				return
			}
		}
	}
	failure(w, 404, "no retained module report for that directory")
}
