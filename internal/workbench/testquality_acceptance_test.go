package workbench

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/russellbrasser/go-review/internal/intelligence"
	"github.com/russellbrasser/go-review/internal/testquality"
)

func qualityProposalLabels(data json.RawMessage, receipt string) (json.RawMessage, error) {
	var context struct {
		References map[string]testquality.Citation
	}
	if err := json.Unmarshal([]byte(receipt), &context); err != nil {
		return nil, err
	}
	var proposal map[string]any
	if err := json.Unmarshal(data, &proposal); err != nil {
		return nil, err
	}
	convert := func(owner map[string]any) error {
		ratings, ok := owner["ratings"].([]any)
		if !ok {
			return fmt.Errorf("fixture proposal requires ratings")
		}
		for _, entry := range ratings {
			rating, ok := entry.(map[string]any)
			if !ok {
				return fmt.Errorf("fixture rating must be an object")
			}
			if _, ok := rating["evidence"]; !ok {
				rating["evidence"] = []any{}
			}
			if rating["value"] == nil {
				rating["value"] = rating["state"]
				delete(rating, "state")
				continue
			}
			number, ok := rating["value"].(float64)
			if !ok || number < 0 || number > 4 || number != float64(int(number)) {
				return fmt.Errorf("invalid fixture rubric value: %v", rating["value"])
			}
			rating["value"] = [...]string{"absent", "weak", "material-issues", "strong", "convincing"}[int(number)]
			delete(rating, "state")
		}
		return nil
	}
	if raw, ok := proposal["items"]; ok {
		items, ok := raw.([]any)
		if !ok {
			return nil, fmt.Errorf("fixture items must be an array")
		}
		for _, entry := range items {
			item, ok := entry.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("fixture item must be an object")
			}
			if err := convert(item); err != nil {
				return nil, err
			}
		}
	} else if err := convert(proposal); err != nil {
		return nil, err
	}
	var bind func(any) error
	bind = func(value any) error {
		switch node := value.(type) {
		case map[string]any:
			for key, child := range node {
				if key == "evidence" {
					entries, ok := child.([]any)
					if !ok {
						return fmt.Errorf("fixture evidence requires an array")
					}
					ids := []string{}
					for _, entry := range entries {
						raw, err := json.Marshal(entry)
						if err != nil {
							return err
						}
						var citation testquality.Citation
						if err := json.Unmarshal(raw, &citation); err != nil {
							return err
						}
						id := ""
						for candidate, source := range context.References {
							if citation.SnapshotID == source.SnapshotID && citation.FileID == source.FileID && citation.StartLine >= source.StartLine && citation.EndLine <= source.EndLine {
								id = candidate
								break
							}
						}
						if id == "" {
							return fmt.Errorf("fixture evidence is not inspected: %+v", citation)
						}
						ids = append(ids, id)
					}
					node[key] = ids
				} else if err := bind(child); err != nil {
					return err
				}
			}
		case []any:
			for _, child := range node {
				if err := bind(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := bind(proposal); err != nil {
		return nil, err
	}
	return json.Marshal(proposal)
}

func TestQualityEndToEndProviderSavedHTTPMCPAndChat(t *testing.T) {
	s := qualityFixture(t)
	text := "package example\nimport \"testing\"\nfunc TestF(t *testing.T) { if F()!=1 { t.Fatal(\"wrong\") } }\n"
	s.review.Modules[0].Report.TestSources[0].File.Source = text
	s.review.Modules[0].Report.TestSources[0].File.SHA256 = fmt.Sprintf("%x", sha256.Sum256([]byte(text)))
	var calls atomic.Int32
	var snapshot string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body struct {
			Messages []intelligence.Message `json:"messages"`
			Tools    []intelligence.Tool    `json:"tools"`
			Format   struct {
				Type   string `json:"type"`
				Schema struct {
					Name string `json:"name"`
				} `json:"json_schema"`
			} `json:"response_format"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		var initial struct {
			Tests []struct {
				Test   testquality.Responsibility `json:"test"`
				Source json.RawMessage            `json:"source"`
			} `json:"tests"`
			Shared []json.RawMessage `json:"shared"`
		}
		if err := json.Unmarshal([]byte(body.Messages[1].Content), &initial); err != nil {
			t.Error(err)
		}
		item := initial.Tests[0].Test
		snapshot = item.Identity.SnapshotID
		shared, _ := json.Marshal(initial.Shared)
		if item.Name != "TestF" || !strings.Contains(string(initial.Tests[0].Source), "t.Fatal") || !strings.Contains(string(shared), `func F`) {
			t.Error("assessment must start from the actual test, with production only as context")
		}
		name := "review_source"
		args, _ := json.Marshal(map[string]any{"snapshotID": snapshot, "fileID": "f", "startLine": 2, "endLine": 2})
		collective := body.Format.Schema.Name == "submit_suite"
		for _, tool := range body.Tools {
			collective = collective || tool.Function.Name == "submit_suite"
		}
		if body.Format.Type == "json_schema" || body.Messages[len(body.Messages)-1].Role == "tool" || collective {
			if !collective && body.Format.Type == "" && !strings.Contains(body.Messages[len(body.Messages)-1].Content, "return 1") {
				t.Error("production context unavailable")
			}
			value := 2
			citations := []testquality.Citation{{SnapshotID: snapshot, FileID: "f", StartLine: 2, EndLine: 2}, {SnapshotID: snapshot, FileID: "test", StartLine: 3, EndLine: 3}}
			ratings := []testquality.Rating{}
			dimensions := []testquality.Dimension{testquality.AssertionStrength, testquality.Craftsmanship}
			if collective {
				dimensions = []testquality.Dimension{testquality.BehaviorAdequacy, testquality.ConditionAdequacy}
			}
			for _, d := range dimensions {
				ratings = append(ratings, testquality.Rating{Dimension: d, State: testquality.RatingAssessed, Value: &value, Rationale: "One meaningful return-value assertion; limited case diversity.", Evidence: citations})
			}
			item.Status, item.Ratings, item.Findings = testquality.StatusScored, ratings, []testquality.Finding{{Category: "assertion-strength", Kind: "inference", Title: "Single retained case", Rationale: "The test checks the return value.", Consequence: "Only this scenario is represented.", Suggestion: "Review whether additional contract variations are relevant.", Evidence: citations}}
			name = "submit_batch"
			args, _ = json.Marshal(map[string]any{"items": []testquality.Responsibility{item}})
			if collective {
				name = "submit_suite"
				args, _ = json.Marshal(map[string]any{"status": testquality.StatusScored, "ratings": ratings, "expectations": []testquality.ExpectedBehavior{{ID: "return-one", Description: "Returns one", Basis: "implementation-derived", State: "supported", Rationale: "Combined test checks the return value.", Evidence: citations, Tests: []testquality.Identity{item.Identity}, Conditions: []testquality.ExpectedCondition{}}}, "findings": []testquality.Finding{}})
			}
		}
		delta := map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": fmt.Sprint(calls.Load()), "function": map[string]string{"name": name, "arguments": string(args)}}}}
		finish := "tool_calls"
		if body.Format.Type == "json_schema" {
			if collective {
				var proposal map[string]any
				if err := json.Unmarshal(args, &proposal); err != nil {
					t.Error(err)
				}
				delete(proposal, "status")
				delete(proposal, "reason")
				args, _ = json.Marshal(proposal)
			}
			converted, err := qualityProposalLabels(args, body.Messages[len(body.Messages)-1].Content)
			if err != nil {
				t.Error(err)
				return
			}
			args = converted
			delta, finish = map[string]any{"content": string(args)}, "stop"
		}
		data, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta, "finish_reason": finish}}})
		fmt.Fprintf(w, "data: %s\n\n", data)
	}))
	defer provider.Close()
	s.ConfigureAgent(intelligence.Config{Enabled: true, BaseURL: provider.URL, Model: "deterministic-provider", MaxSteps: 3})
	// Cancelling the initiating request AFTER acceptance must not cancel the job.
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("POST", "http://127.0.0.1/api/test-quality/start", strings.NewReader(`{}`)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	cancel()
	if w.Code != 202 {
		t.Fatal(w.Body.String())
	}
	var launch struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &launch); err != nil {
		t.Fatal(err)
	}
	s.qualityMu.Lock()
	job := s.qualityJobs[launch.ID]
	s.qualityMu.Unlock()
	select {
	case <-job.done:
	case <-time.After(5 * time.Second):
		t.Fatal("assessment did not finish")
	}
	if calls.Load() > 5 {
		t.Fatalf("redundant provider requests: %d", calls.Load())
	}
	assessmentCalls := calls.Load()
	a, err := s.qualityStore.Load(launch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if a.Status != "completed" || a.Items[0].Status != testquality.StatusScored {
		t.Fatalf("assessment: %+v", qualityCompact(a))
	}
	input := testquality.QueryInput{AssessmentID: launch.ID, Operation: "test", SnapshotID: snapshot, SymbolID: "TestF"}
	body, _ := json.Marshal(input)
	httpResult := qualityRequest(t, s, "/api/test-quality/query", string(body))
	if httpResult.Code != 200 {
		t.Fatal(httpResult.Body.String())
	}
	st, ct := mcp.NewInMemoryTransports()
	server, err := s.newChatService().server().Connect(context.Background(), st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-quality-acceptance", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "review_test_quality", Arguments: input})
	if err != nil || result.IsError {
		t.Fatalf("MCP: %+v %v", result, err)
	}
	encoded, _ := json.Marshal(result.StructuredContent)
	var mcpResult struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(encoded, &mcpResult); err != nil {
		t.Fatal(err)
	}
	var left, right any
	json.Unmarshal(httpResult.Body.Bytes(), &left)
	json.Unmarshal(mcpResult.Data, &right)
	l, _ := json.Marshal(left)
	rr, _ := json.Marshal(right)
	if string(l) != string(rr) {
		t.Fatalf("HTTP/MCP evidence differs:\n%s\n%s", l, rr)
	}
	chatProvider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Messages []intelligence.Message `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&b)
		if !strings.Contains(b.Messages[0].Content, launch.ID) || !strings.Contains(b.Messages[len(b.Messages)-1].Content, "Single retained case") {
			t.Error("chat did not refresh pinned assessment detail")
		}
		fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"The saved assessment identifies limited case diversity."},"finish_reason":"stop"}]}`+"\n\n")
	}))
	defer chatProvider.Close()
	err = s.runChat(context.Background(), intelligence.Config{Enabled: true, BaseURL: chatProvider.URL, Model: "chat"}, chatRequest{Message: "Explain this assessment", AssessmentID: launch.ID, Context: body}, nil, s.newChatService(), func(chatEvent) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != assessmentCalls {
		t.Fatal("browsing or chat reran assessment")
	}
}
