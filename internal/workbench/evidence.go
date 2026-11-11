package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/russellbrasser/go-review/internal/review"
)

func decodeRequest(w http.ResponseWriter, r *http.Request, request interface{}) bool {
	if r.Method != http.MethodPost {
		failure(w, 405, "POST required")
		return false
	}
	if r.Header.Get("Content-Type") != "application/json" {
		failure(w, 415, "application/json required")
		return false
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	d.DisallowUnknownFields()
	if err := d.Decode(request); err != nil {
		failure(w, 400, fmt.Sprintf("invalid request: %v", err))
		return false
	}
	return true
}

func (s *Server) current() *review.Report {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.report
}

func (s *Server) publish(w http.ResponseWriter, report *review.Report) {
	s.mu.Lock()
	s.report = report
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(report)
}

func (s *Server) coverage(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Path string `json:"path"`
	}
	if !decodeRequest(w, r, &request) {
		return
	}
	if request.Path == "" {
		failure(w, 400, "coverage profile path is required")
		return
	}
	if !s.collect.TryLock() {
		failure(w, 409, "collection already running")
		return
	}
	defer s.collect.Unlock()
	previous := s.current()
	if previous == nil {
		failure(w, 409, "analyze a module first")
		return
	}
	// Published snapshots are immutable while other requests encode them.
	data, err := json.Marshal(previous)
	if err != nil {
		failure(w, 500, err.Error())
		return
	}
	var report review.Report
	if err = json.Unmarshal(data, &report); err != nil {
		failure(w, 500, err.Error())
		return
	}
	file, err := os.Open(request.Path)
	if err != nil {
		failure(w, 422, err.Error())
		return
	}
	defer file.Close()
	if err = review.ImportCoverage(&report, file, request.Path); err != nil {
		failure(w, 422, err.Error())
		return
	}
	s.publish(w, &report)
}

func (s *Server) unitTests(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Execute        bool                       `json:"execute"`
		Selections     []review.UnitTestSelection `json:"selections"`
		RuntimeTrace   bool                       `json:"runtimeTrace"`
		SaveSelections bool                       `json:"saveSelections"`
	}
	if !decodeRequest(w, r, &request) {
		return
	}
	if !request.Execute || len(request.Selections) == 0 {
		failure(w, 400, "explicit execution consent and exact unit-test selections required")
		return
	}
	if !s.collect.TryLock() {
		failure(w, 409, "collection already running")
		return
	}
	defer s.collect.Unlock()
	current := s.current()
	if current == nil {
		failure(w, 409, "analyze a module first")
		return
	}
	data, err := json.Marshal(current)
	if err != nil {
		failure(w, 500, err.Error())
		return
	}
	var report review.Report
	if err = json.Unmarshal(data, &report); err != nil {
		failure(w, 500, err.Error())
		return
	}
	report.UnitEvidence = nil
	cache, err := os.UserCacheDir()
	if err != nil {
		failure(w, 500, err.Error())
		return
	}
	storage := filepath.Join(cache, "go-review", "runs")
	if err = os.MkdirAll(storage, 0700); err != nil {
		failure(w, 500, err.Error())
		return
	}
	artifacts, err := os.MkdirTemp(storage, "unit-evidence-")
	if err != nil {
		failure(w, 500, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	started := time.Now().UTC()
	err = review.CollectUnitEvidence(ctx, &report, review.UnitEvidenceOptions{Selections: request.Selections, ArtifactDir: artifacts, RuntimeTrace: request.RuntimeTrace})
	if err != nil && report.UnitEvidence == nil {
		if cleanupErr := os.RemoveAll(artifacts); cleanupErr != nil {
			err = errors.Join(err, fmt.Errorf("remove unused unit artifact directory: %w", cleanupErr))
		}
		failure(w, 422, err.Error())
		return
	}
	state := "completed"
	if err != nil {
		state = "attention"
		report.Warnings = append(report.Warnings, "Selected unit evidence: "+err.Error())
	}
	finished := time.Now().UTC()
	report.Review = &review.ReviewReference{Root: report.Scope.Root, Directory: artifacts, Status: state, StartedAt: started, FinishedAt: finished, ModuleCount: 1}
	retained := review.RepositoryReview{Root: report.Scope.Root, Directory: artifacts, Status: state, StartedAt: started, FinishedAt: finished, Scope: report.Scope,
		Steps: []review.ReviewStep{}, Modules: []review.ReviewedModule{{Directory: report.Root, Report: &report}}}
	s.mu.RLock()
	previousRun := s.review
	s.mu.RUnlock()
	if previousRun != nil {
		// Decode into a fresh value: reusing retained would decode its first
		// module through &report and overwrite the user's selected module.
		var copied review.RepositoryReview
		data, err = json.Marshal(previousRun)
		if err == nil {
			err = json.Unmarshal(data, &copied)
		}
		if err != nil {
			failure(w, 500, "copy retained review: "+err.Error())
			return
		}
		retained = copied
		retained.Directory, retained.Status, retained.StartedAt, retained.FinishedAt = artifacts, state, started, finished
		for i := range retained.Modules {
			if retained.Modules[i].Directory == report.Root {
				retained.Modules[i].Report = &report
			}
		}
		report.Review.ModuleCount = len(retained.Modules)
		for i := range retained.Steps {
			step := &retained.Steps[i]
			if step.Module != report.Root || step.ID != "tests" {
				continue
			}
			step.StartedAt, step.FinishedAt = started, finished
			step.Status, step.Detail = "pass", "Selected unit-test evidence refreshed."
			step.Commands = nil
			for _, run := range report.UnitEvidence.Runs {
				step.Commands = append(step.Commands, run.Commands...)
				if run.Status != "pass" {
					step.Status, step.Detail = "fail", run.Error
				}
			}
		}
	}
	for _, step := range retained.Steps {
		if step.Status == "fail" || step.Status == "error" {
			retained.Status = "attention"
		}
	}
	for i := range retained.Modules {
		module := &retained.Modules[i]
		if module.Error != "" {
			retained.Status = "attention"
		}
	}
	// References address the newly published manifest; raw evidence paths
	// retain their original selection directories and source identities.
	for i := range retained.Modules {
		if module := &retained.Modules[i]; module.Report != nil {
			module.Report.Review = &review.ReviewReference{Root: retained.Root, Directory: artifacts, Status: retained.Status,
				StartedAt: started, FinishedAt: finished, ModuleCount: len(retained.Modules)}
		}
	}
	if request.SaveSelections {
		savedScope := report.Scope
		savedScope.UnitSelections = nil
		for _, run := range report.UnitEvidence.Runs {
			savedScope.UnitSelections = append(savedScope.UnitSelections, run.Selection)
		}
		if err = review.SaveReviewScope(report.Scope.Root, savedScope); err != nil {
			report.Warnings = append(report.Warnings, "Unit selections were not saved: "+err.Error())
		}
	}
	data, err = json.Marshal(retained)
	if err == nil {
		err = os.WriteFile(filepath.Join(artifacts, "review.json"), data, 0600)
	}
	if err != nil {
		failure(w, 500, "retain unit evidence: "+err.Error())
		return
	}
	s.mu.Lock()
	s.report, s.review = &report, &retained
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(&report)
}
