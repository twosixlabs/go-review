package workbench

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/russellbrasser/go-review/internal/changes"
	"github.com/russellbrasser/go-review/internal/intelligence"
)

func TestChangeChatWithoutOrdinarySnapshotDoesNotInjectApplicationIdentity(t *testing.T) {
	c := &changes.Comparison{ID: "comparison", Directory: filepath.Join(t.TempDir(), "comparison-fixture"),
		Modules: []changes.Module{}, Files: []changes.FileChange{}, Records: []changes.Record{}, Flows: []changes.Flow{}, Correspondences: []changes.Correspondence{}, Limitations: []string{}}
	s := New(nil)
	s.comparison = c
	calls := 0
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var request struct {
			Messages []intelligence.Message `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		last := request.Messages[len(request.Messages)-1].Content
		if !strings.Contains(last, `"comparisonID":"comparison"`) {
			t.Errorf("missing comparison evidence: %s", last)
		}
		if strings.Contains(last, "unknown field") || strings.Contains(last, "snapshotID") {
			t.Errorf("ordinary snapshot leaked into comparison query: %s", last)
		}
		if calls == 1 {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"changes\",\"function\":{\"name\":\"review_changes\",\"arguments\":\"{\\\"operation\\\":\\\"progress\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n")
		} else {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"No changes pending.\"},\"finish_reason\":\"stop\"}]}\n\n")
		}
	}))
	defer model.Close()
	s.ConfigureAgent(intelligence.Config{Enabled: true, BaseURL: model.URL, Model: "fixture"})
	request := httptest.NewRequest("POST", "http://127.0.0.1:8090/api/chat", strings.NewReader(`{"message":"Explain these changes","comparisonID":"comparison","context":{"kind":"changes","comparisonID":"comparison"}}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, request)
	if response.Code != 200 || calls != 2 || !strings.Contains(response.Body.String(), "No changes pending.") {
		t.Fatalf("status %d calls %d: %s", response.Code, calls, response.Body.String())
	}
	if s.current() != nil {
		t.Fatal("comparison chat invented ordinary report")
	}
	if _, err := s.queryChanges(context.Background(), changeQueryInput{ComparisonID: "stale"}); err == nil {
		t.Fatal("stale comparison accepted")
	}
}
