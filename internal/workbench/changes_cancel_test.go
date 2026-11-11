package workbench

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestChangeCancelCannotCancelAReplacementJob(t *testing.T) {
	s := New(nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	job := &changeJob{ID: "current-job", cancel: cancel, done: make(chan struct{})}
	s.changeJob = job
	go func() { <-ctx.Done(); close(job.done) }()
	call := func(body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest("POST", "http://127.0.0.1:8090/api/changes/cancel", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		s.Handler().ServeHTTP(response, request)
		return response
	}
	stale := call(`{"confirmed":true,"jobID":"previous-job"}`)
	if stale.Code != 409 || ctx.Err() != nil {
		t.Fatalf("stale action canceled newer job: %d %v", stale.Code, ctx.Err())
	}
	current := call(`{"confirmed":true,"jobID":"current-job"}`)
	if current.Code != 200 || ctx.Err() != context.Canceled {
		t.Fatalf("exact cancellation failed: %d %s", current.Code, current.Body.String())
	}
}
