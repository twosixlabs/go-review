package testquality

import (
	"context"
	"fmt"
)

// RunAgentDiagnostic executes one on-demand request under the assessment policy,
// counting all retained requests toward cumulative bounds. It never mutates a;
// the caller serializes access and persists the returned record, including failures.
func RunAgentDiagnostic(ctx context.Context, a *Assessment, request DiagnosticRequest, directory string) (DiagnosticRecord, error) {
	record := DiagnosticRecord{Request: request, Status: "failed"}
	if a == nil {
		err := fmt.Errorf("test quality assessment is required")
		record.Error = err.Error()
		return record, err
	}
	requests := make([]DiagnosticRequest, 0, len(a.Diagnostics)+1)
	for _, prior := range a.Diagnostics {
		requests = append(requests, prior.Request)
	}
	requests = append(requests, request)
	if err := ValidateDiagnostics(a, a.Policy, requests); err != nil {
		record.Error = err.Error()
		return record, err
	}
	record, err := RunDiagnostic(ctx, a, a.Policy, request, directory)
	if err != nil {
		record.Error = err.Error()
	}
	return record, err
}
