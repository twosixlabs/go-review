package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/russellbrasser/go-review/internal/intelligence"
	"github.com/russellbrasser/go-review/internal/review"
	"github.com/russellbrasser/go-review/internal/testquality"
)

func (s *Server) prepareQualityRetrieval(ctx context.Context, a *testquality.Assessment) (testquality.RetrievalFunc, error) {
	state := s.retrievalOwner()
	if state == nil {
		return nil, nil
	}
	for _, report := range a.Reports {
		for {
			state.mu.Lock()
			if state.closed {
				state.mu.Unlock()
				return nil, fmt.Errorf("semantic retrieval is shutting down")
			}
			if state.cancel != nil {
				done := state.done
				state.mu.Unlock()
				select {
				case <-done:
					continue
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			runCtx, cancel := context.WithCancel(ctx)
			state.cancel, state.done, state.jobID, state.phase = cancel, make(chan struct{}), report.SnapshotID, "discovering"
			state.mu.Unlock()
			state.build(runCtx, &querySnapshot{info: snapshotInfo{ID: report.SnapshotID}, report: report.Report})
			state.mu.Lock()
			errText, phase := state.err, state.phase
			state.mu.Unlock()
			if phase != "ready" {
				return nil, fmt.Errorf("semantic preparation: %s", errText)
			}
			break
		}
	}
	state.mu.Lock()
	engine := state.engine
	state.mu.Unlock()
	return func(ctx context.Context, report *review.Report, snapshot string, args json.RawMessage) (json.RawMessage, error) {
		var input retrievalInput
		if err := json.Unmarshal(args, &input); err != nil {
			return nil, err
		}
		input.SnapshotID = snapshot
		result, err := retrievePinned(ctx, engine, report, input)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	}, nil
}

// One queue owner performs parsing, diagnostics and model calls. Readers see
// only published results; model input never shares the evolving item slice.
func (s *Server) runEvidenceQuality(ctx context.Context, job *qualityJob, previous <-chan struct{}, cfg intelligence.Config) {
	defer close(job.done)
	defer job.cancel()
	if previous != nil {
		<-previous
	}
	s.qualityMu.Lock()
	a := job.data
	input := &testquality.Assessment{ID: a.ID, Version: a.Version, Root: a.Root, Reports: a.Reports, Items: append([]testquality.Responsibility{}, a.Items...), Unavailable: a.Unavailable, Policy: a.Policy}
	a.PhaseMillis = map[string]int64{}
	s.qualityMu.Unlock()
	var pipelineErr error
	publish := func(phase string) error {
		s.qualityMu.Lock()
		defer s.qualityMu.Unlock()
		a.Phase = phase
		a.Facts, a.Analysis, a.Review = input.Facts, input.Analysis, input.Review
		a.Diagnostics = append([]testquality.DiagnosticRecord{}, input.Diagnostics...)
		a.DiagnosticReason = input.DiagnosticReason
		a.Suite = input.Suite
		a.Items = append([]testquality.Responsibility{}, input.Items...)
		return s.qualityStore.Save(a)
	}
	stage := func(name string, run func() error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := publish(name); err != nil {
			return err
		}
		start := time.Now()
		err := run()
		s.qualityMu.Lock()
		a.PhaseMillis[name] = time.Since(start).Milliseconds()
		s.qualityMu.Unlock()
		return err
	}
	s.qualityMu.Lock()
	a.Status = "running"
	s.qualityMu.Unlock()
	err := stage("deterministic-evidence", func() error { _, err := testquality.AnalyzeFacts(input); return err })
	if err == nil {
		err = stage("diagnostics", func() error {
			requests, reason, planErr := testquality.PlanDiagnostics(input, input.Policy)
			input.DiagnosticReason = reason
			if planErr != nil {
				return planErr
			}
			if err := testquality.ValidateDiagnostics(input, input.Policy, requests); err != nil {
				return err
			}
			for i, request := range requests {
				record, runErr := testquality.RunDiagnostic(ctx, input, input.Policy, request, filepath.Join(s.qualityStore.Directory, a.ID, "diagnostics", fmt.Sprintf("request-%d", i)))
				input.Diagnostics = append(input.Diagnostics, record)
				pipelineErr = errors.Join(pipelineErr, runErr)
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if err := publish("diagnostics"); err != nil {
					return err
				}
			}
			return nil
		})
	}
	var retrieve testquality.RetrievalFunc
	if err == nil {
		err = stage("semantic-index", func() error {
			var indexErr error
			retrieve, indexErr = s.prepareQualityRetrieval(ctx, input)
			// A retrieval failure is visible, but does not erase deterministic
			// evidence or independently supported source-backed judgments.
			pipelineErr = errors.Join(pipelineErr, indexErr)
			return ctx.Err()
		})
	}
	var environment testquality.JudgmentOptions
	if err == nil && cfg.Enabled {
		var closeSession func()
		environment, closeSession, err = s.qualityEvidence(ctx, input, retrieve)
		if err == nil {
			defer closeSession()
		}
	}
	if err == nil {
		err = stage("individual-judgments", func() error {
			if !cfg.Enabled {
				for i := range input.Items {
					if input.Items[i].Status == testquality.StatusUnreviewed {
						input.Items[i].Status = testquality.StatusInsufficient
						input.Items[i].Reason = "AI disabled; deterministic facts retained without invented judgments."
					}
				}
				pipelineErr = errors.Join(pipelineErr, fmt.Errorf("AI disabled; semantic judgments unavailable"))
				return nil
			}
			batches, batchErr := testquality.PlanBatches(input, input.Items, cfg)
			if batchErr != nil {
				return batchErr
			}
			for _, batch := range batches {
				var results []testquality.Responsibility
				var callErr error
				if s.qualityAssess != nil {
					for _, item := range batch {
						result, resultErr := s.qualityAssess(ctx, input, item, cfg)
						if resultErr != nil {
							result = item
							result.Status = testquality.StatusInsufficient
							result.Reason = resultErr.Error()
						}
						results = append(results, result)
						callErr = errors.Join(callErr, resultErr)
					}
				} else {
					results, callErr = testquality.AssessBatch(ctx, input, batch, cfg, environment)
				}
				pipelineErr = errors.Join(pipelineErr, callErr)
				if ctx.Err() != nil {
					return ctx.Err()
				}
				for _, result := range results {
					for i := range input.Items {
						if input.Items[i].Identity == result.Identity {
							input.Items[i] = result
							break
						}
					}
					if result.Status == testquality.StatusInsufficient {
						pipelineErr = errors.Join(pipelineErr, fmt.Errorf("%s: %s", result.Name, result.Reason))
					}
				}
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if err := publish("individual-judgments"); err != nil {
					return err
				}
			}
			return nil
		})
	}
	if err == nil && cfg.Enabled {
		err = stage("collective-judgments", func() error {
			environment.CheckpointSuite = func(suite testquality.SuiteAssessment) error {
				input.Suite = &suite
				return publish("collective-judgments")
			}
			var suite testquality.SuiteAssessment
			var suiteErr error
			suite, suiteErr = testquality.AssessSuite(ctx, input, cfg, environment)
			if suiteErr != nil {
				suite.Status = testquality.StatusInsufficient
				suite.Reason = suiteErr.Error()
			}
			input.Suite = &suite
			pipelineErr = errors.Join(pipelineErr, suiteErr)
			return ctx.Err()
		})
	}
	s.qualityMu.Lock()
	switch {
	case ctx.Err() != nil:
		a.Status = "cancelled"
		a.Error = "Assessment cancelled; completed evidence and results retained."
	case err != nil:
		a.Status = "failed"
		a.Error = err.Error()
	default:
		summary, summaryErr := testquality.AggregateVersion(a.Version, input.Items)
		if summaryErr != nil {
			a.Status = "failed"
			a.Error = summaryErr.Error()
		} else if summary.Partial || len(input.Unavailable) > 0 || pipelineErr != nil || input.Suite == nil || input.Suite.Status != testquality.StatusScored {
			a.Status = "partial"
		} else {
			a.Status = "completed"
		}
		if pipelineErr != nil {
			a.Error = pipelineErr.Error()
		}
	}
	s.qualityMu.Unlock()
	if saveErr := publish("finished"); saveErr != nil {
		s.qualityMu.Lock()
		a.Status = "failed"
		a.Error = "persist final assessment: " + saveErr.Error()
		s.qualityMu.Unlock()
	}
}
