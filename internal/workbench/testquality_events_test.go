package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/russellbrasser/go-review/internal/intelligence"
	"github.com/russellbrasser/go-review/internal/testquality"
)

func TestQualityAgentEventHistoryShowsInFlightToolCalls(t *testing.T) {
	s := qualityFixture(t)
	a, err := s.qualityStore.Create(t.Context(), s.review, "", "")
	if err != nil {
		t.Fatal(err)
	}
	cfg := qualityAgentProvider(t, func(w http.ResponseWriter, _ *http.Request, _ qualityAgentRequest) {
		qualityAgentAnswer(w, qualityAgentCall("read", "review_source", `{"fileID":"test"}`))
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- runQualityAgent(ctx, cfg, "Inspect source.", testquality.JudgmentOptions{
			Tools: []intelligence.Tool{{Type: "function", Function: intelligence.Function{Name: "review_source", Parameters: json.RawMessage(`{"type":"object"}`)}}},
			Trace: s.qualityAgentTrace(a, "Lead"),
			Read: func(ctx context.Context, _ string, _ json.RawMessage) (json.RawMessage, []testquality.Citation, error) {
				close(started)
				<-ctx.Done()
				return nil, nil, ctx.Err()
			},
		}, nil)
	}()
	defer func() { cancel(); <-done }()
	select {
	case <-started:
	case <-t.Context().Done():
		t.Fatal(t.Context().Err())
	}
	raw, err := s.queryTestQuality(t.Context(), testquality.QueryInput{AssessmentID: a.ID, Operation: "events"})
	if err != nil || !strings.Contains(string(raw), `"kind":"tool_call"`) || !strings.Contains(string(raw), "review_source") {
		t.Fatalf("in-flight tool call is invisible: %s %v", raw, err)
	}
}

func TestQualityAgentEventHistoryPagesOriginalToolPayloadAndPropagatesWriteFailure(t *testing.T) {
	s := qualityFixture(t)
	a, err := s.qualityStore.Create(t.Context(), s.review, "", "")
	if err != nil {
		t.Fatal(err)
	}
	trace := s.qualityAgentTrace(a, "Lead")
	want := strings.Repeat("\t<&", 20000)
	payload, _ := json.Marshal(want)
	if err := trace(testquality.ReviewTrace{Kind: "tool", Tool: "review_source", Args: json.RawMessage(`{`), Result: payload, Error: "malformed original arguments"}); err != nil {
		t.Fatal(err)
	}
	index, err := s.qualityStore.AgentEvents(testquality.QueryInput{AssessmentID: a.ID, Operation: "events"})
	if err != nil || len(index) > 24000 || !strings.Contains(string(index), "malformed original arguments") {
		t.Fatalf("tool event index lost error/bounds: %s %v", index, err)
	}
	offset := 0
	var reconstructed strings.Builder
	for {
		raw, err := s.qualityStore.AgentEvents(testquality.QueryInput{AssessmentID: a.ID, Operation: "event", TextOffset: offset, TextLimit: 24000})
		if err != nil || len(raw) > 24000 {
			t.Fatalf("event page exceeded output budget: %d %v", len(raw), err)
		}
		var page struct {
			Text     string
			TextPage struct{ NextOffset *int }
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			t.Fatal(err)
		}
		reconstructed.WriteString(page.Text)
		if page.TextPage.NextOffset == nil {
			break
		}
		offset = *page.TextPage.NextOffset
	}
	var event testquality.AgentEvent
	if err := json.Unmarshal([]byte(reconstructed.String()), &event); err != nil {
		t.Fatal(err)
	}
	var args, result string
	if err := json.Unmarshal(event.Args, &args); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(event.Result, &result); err != nil {
		t.Fatal(err)
	}
	if args != "{" || result != want {
		t.Fatal("actual malformed arguments or tool result were truncated")
	}
	if _, err := s.qualityStore.AgentEvents(testquality.QueryInput{AssessmentID: a.ID, Operation: "events", Offset: 1}); err == nil {
		t.Fatal("accepted a cursor inside an event")
	}
	path := filepath.Join(s.qualityStore.Directory, a.ID, "agent-events.jsonl")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	var persistenceErr *qualityPersistenceError
	if err := trace(testquality.ReviewTrace{Kind: "message"}); !errors.As(err, &persistenceErr) {
		t.Fatalf("event write failure was hidden: %v", err)
	}
}

func TestQualityAgentEventHistoryKeepsCancelledChildAssignment(t *testing.T) {
	s := qualityFixture(t)
	a, err := s.qualityStore.Create(t.Context(), s.review, "", "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cfg := qualityAgentProvider(t, func(w http.ResponseWriter, _ *http.Request, _ qualityAgentRequest) { qualityFixtureRespond(w) })
	p, err := newQualityPrimary(ctx, s, a, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	if _, _, err := p.call(t.Context(), "delegate_review", json.RawMessage(`{"mode":"research","instructions":"Inspect the assigned package only."}`)); err != nil {
		t.Fatal(err)
	}
	cancel()
	result, _, readErr := qualityChildReport(t, p)
	if readErr != nil || !strings.Contains(string(result), `"status":"cancelled"`) {
		t.Fatalf("cancellation misreported: %s %v", result, readErr)
	}
	p.close()
	raw, err := s.qualityStore.AgentEvents(testquality.QueryInput{AssessmentID: a.ID, Operation: "events", Limit: 100})
	if err != nil || !strings.Contains(string(raw), `"kind":"assignment"`) || !strings.Contains(string(raw), `"kind":"end"`) || !strings.Contains(string(raw), `"status":"cancelled"`) {
		t.Fatalf("cancelled child lost assignment/terminal history: %s %v", raw, err)
	}
}

func TestQualityAgentEventHistoryRetainsChildAssignmentAndActualErrors(t *testing.T) {
	s := qualityFixture(t)
	a, err := s.qualityStore.Create(t.Context(), s.review, "", "")
	if err != nil {
		t.Fatal(err)
	}
	cfg := qualityAgentProvider(t, func(w http.ResponseWriter, _ *http.Request, _ qualityAgentRequest) {
		http.Error(w, "specific child provider failure", 503)
	})
	p, err := newQualityPrimary(t.Context(), s, a, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	if _, _, err := p.call(t.Context(), "delegate_review", json.RawMessage(`{"mode":"research","instructions":"Find the clarification tests and report exact identities."}`)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := qualityChildReport(t, p); err != nil {
		t.Fatal(err)
	}
	p.close()
	reopened := New(s.review.Modules[0].Report)
	reopened.qualityStore = s.qualityStore
	raw, err := reopened.queryTestQuality(t.Context(), testquality.QueryInput{AssessmentID: a.ID, Operation: "events", Limit: 1})
	if err != nil {
		t.Fatalf("agent history cannot be inspected after reload: %v", err)
	}
	var page struct {
		Items []struct {
			Offset        int
			AgentID, Kind string
		}
		NextOffset *int
	}
	if err := json.Unmarshal(raw, &page); err != nil || len(page.Items) != 1 || page.Items[0].AgentID != "agent-1" || page.Items[0].Kind != "assignment" || page.NextOffset == nil {
		t.Fatalf("scope/start event lost: %s %v", raw, err)
	}
	var assignment strings.Builder
	textOffset := 0
	for {
		first, err := reopened.queryTestQuality(t.Context(), testquality.QueryInput{AssessmentID: a.ID, Operation: "event", Offset: page.Items[0].Offset, TextOffset: textOffset, TextLimit: 1000})
		if err != nil {
			t.Fatal(err)
		}
		var detail struct {
			Text     string
			TextPage struct{ NextOffset *int }
		}
		if err := json.Unmarshal(first, &detail); err != nil {
			t.Fatal(err)
		}
		assignment.WriteString(detail.Text)
		if detail.TextPage.NextOffset == nil {
			break
		}
		textOffset = *detail.TextPage.NextOffset
	}
	if !strings.Contains(assignment.String(), "Find the clarification tests") {
		t.Fatalf("actual child assignment unavailable: %s", assignment.String())
	}
	foundFailure := false
	for page.NextOffset != nil {
		raw, err = reopened.queryTestQuality(t.Context(), testquality.QueryInput{AssessmentID: a.ID, Operation: "events", Offset: *page.NextOffset, Limit: 1})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "specific child provider failure") {
			foundFailure = true
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			t.Fatal(err)
		}
	}
	if !foundFailure {
		t.Fatal("original child provider error was not retained")
	}
}
