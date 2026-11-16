package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/russellbrasser/go-review/internal/intelligence"
	"github.com/russellbrasser/go-review/internal/testquality"
)

type qualityAgentRequest struct {
	Messages       []intelligence.Message `json:"messages"`
	Tools          []intelligence.Tool    `json:"tools"`
	ResponseFormat json.RawMessage        `json:"response_format"`
	MaxTokens      *int                   `json:"max_tokens"`
	ToolChoice     string                 `json:"tool_choice"`
}

func qualityAgentProvider(t *testing.T, respond func(http.ResponseWriter, *http.Request, qualityAgentRequest)) intelligence.Config {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request qualityAgentRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		respond(w, r, request)
	}))
	t.Cleanup(server.Close)
	return intelligence.Config{Enabled: true, BaseURL: server.URL, Model: "fixture"}
}

func qualityAgentAnswer(w http.ResponseWriter, calls ...intelligence.ToolCall) {
	delta := map[string]any{"content": "Finished."}
	finish := "stop"
	if len(calls) > 0 {
		parts := make([]any, 0, len(calls))
		for i, call := range calls {
			parts = append(parts, map[string]any{"index": i, "id": call.ID, "type": "function", "function": call.Function})
		}
		delta = map[string]any{"tool_calls": parts}
		finish = "tool_calls"
	}
	raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta, "finish_reason": finish}}})
	fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", raw)
}

func qualityAgentCall(id, name, args string) intelligence.ToolCall {
	return intelligence.ToolCall{ID: id, Type: "function", Function: intelligence.Function{Name: name, Arguments: args}}
}

func qualityAgentOptions() testquality.JudgmentOptions {
	return testquality.JudgmentOptions{Tools: []intelligence.Tool{{Type: "function", Function: intelligence.Function{
		Name: "review_source", Parameters: json.RawMessage(`{"type":"object"}`),
	}}}}
}

const qualityAgentTestArgs = `{"snapshotID":"snapshot","symbolID":"test","craftsmanship":3,"assertionStrength":2,"rationale":"Checks the return value.","fileID":"file","startLine":3,"endLine":4,"findingTitle":"Missing error check","findingRationale":"Errors can pass unnoticed.","findingSuggestion":"Assert the returned error.","additionalEvidence":[{"snapshotID":"snapshot","fileID":"file","startLine":7,"endLine":8}]}`
const qualityAgentGroupArgs = `{"snapshotID":"snapshot","behaviorAdequacy":1,"conditionAdequacy":4,"rationale":"Independent collective judgment.","fileID":"file","startLine":3,"endLine":4}`

func TestQualityAgentLoopDetectsUnchangedReadStalls(t *testing.T) {
	for _, mode := range []string{"recover", "stop", "changing"} {
		t.Run(mode, func(t *testing.T) {
			requests, reads := 0, 0
			cfg := qualityAgentProvider(t, func(w http.ResponseWriter, _ *http.Request, request qualityAgentRequest) {
				requests++
				if mode == "recover" && requests == 3 {
					last := request.Messages[len(request.Messages)-1]
					if last.Role != "system" || !strings.Contains(last.Content, "unchanged") {
						t.Errorf("missing stalled-investigation guidance: %+v", last)
					}
					qualityAgentAnswer(w, qualityAgentCall("record", "record_test", qualityAgentTestArgs))
				} else if requests <= 4 {
					qualityAgentAnswer(w, qualityAgentCall(fmt.Sprint(requests), "review_source", `{}`))
				} else {
					qualityAgentAnswer(w)
				}
			})
			options := qualityAgentOptions()
			options.Read = func(context.Context, string, json.RawMessage) (json.RawMessage, []testquality.Citation, error) {
				reads++
				if mode == "changing" {
					return json.RawMessage(fmt.Sprintf(`{"revision":%d}`, reads)), nil, nil
				}
				return json.RawMessage(`{"items":[]}`), nil, nil
			}
			err := runQualityAgent(t.Context(), cfg, "Review.", options, func(string, json.RawMessage, []testquality.Citation) error { return nil })
			if mode == "stop" {
				if err == nil || !strings.Contains(err.Error(), "repeated unchanged") || requests != 3 {
					t.Fatalf("read stall was not stopped: requests=%d error=%v", requests, err)
				}
			} else if err != nil || requests != 5 {
				t.Fatalf("productive review was stopped: requests=%d error=%v", requests, err)
			}
		})
	}
}

func TestQualityAgentLoopRecordsInterleavedWithInspectedEvidence(t *testing.T) {
	citation := testquality.Citation{SnapshotID: "snapshot", FileID: "file", StartLine: 3, EndLine: 8}
	var recorded []string
	var requests int
	cfg := qualityAgentProvider(t, func(w http.ResponseWriter, _ *http.Request, request qualityAgentRequest) {
		requests++
		if len(request.ResponseFormat) != 0 || request.MaxTokens != nil || request.ToolChoice != "auto" {
			t.Errorf("not an unconstrained native tool request: %+v", request)
		}
		if requests == 1 {
			for _, name := range []string{"record_test", "record_group"} {
				var schema struct {
					Properties map[string]struct {
						Type    string          `json:"type"`
						Minimum *int            `json:"minimum"`
						Maximum *int            `json:"maximum"`
						Items   json.RawMessage `json:"items"`
						Enum    []string        `json:"enum"`
					} `json:"properties"`
					Required []string `json:"required"`
				}
				found := false
				for _, tool := range request.Tools {
					if tool.Function.Name == name {
						found = true
						if err := json.Unmarshal(tool.Function.Parameters, &schema); err != nil {
							t.Fatal(err)
						}
					}
				}
				if !found {
					t.Errorf("missing native tool %s", name)
					continue
				}
				dimensions := []string{"craftsmanship", "assertionStrength"}
				if name == "record_group" {
					dimensions = []string{"behaviorAdequacy", "conditionAdequacy"}
				}
				for _, dimension := range dimensions {
					p := schema.Properties[dimension]
					if p.Type != "integer" || p.Minimum == nil || *p.Minimum != 0 || p.Maximum == nil || *p.Maximum != 4 {
						t.Errorf("%s is not a bounded integer: %+v", dimension, p)
					}
				}
				if got := strings.Join(schema.Properties["findingKind"].Enum, ","); got != "observation,inference,suggestion" {
					t.Errorf("%s findingKind must distinguish observation, inference and suggestion: %q", name, got)
				}
				for _, field := range []string{"findingTitle", "findingRationale", "findingSuggestion", "findingKind", "findingConsequence"} {
					if schema.Properties[field].Type != "string" {
						t.Errorf("missing optional finding field %s", field)
					}
					for _, required := range schema.Required {
						if required == field {
							t.Errorf("finding field %s must be optional", field)
						}
					}
				}
				extra := schema.Properties["additionalEvidence"]
				var item struct {
					Type       string `json:"type"`
					Properties map[string]struct {
						Type string `json:"type"`
					} `json:"properties"`
					Required []string `json:"required"`
				}
				if extra.Type != "array" || json.Unmarshal(extra.Items, &item) != nil || item.Type != "object" {
					t.Errorf("%s must accept additional evidence citation objects", name)
				} else {
					for field, want := range map[string]string{"snapshotID": "string", "fileID": "string", "startLine": "integer", "endLine": "integer"} {
						if item.Properties[field].Type != want {
							t.Errorf("%s citation field %s must have type %s", name, field, want)
						}
						if !strings.Contains(","+strings.Join(item.Required, ",")+",", ","+field+",") {
							t.Errorf("%s citation must require %s", name, field)
						}
					}
					for _, required := range schema.Required {
						if required == "additionalEvidence" {
							t.Errorf("%s additional evidence must be optional", name)
						}
					}
				}
			}
			qualityAgentAnswer(w,
				qualityAgentCall("read", "review_source", `{"fileID":"file"}`),
				qualityAgentCall("test", "record_test", qualityAgentTestArgs),
				qualityAgentCall("group", "record_group", qualityAgentGroupArgs),
				qualityAgentCall("test-again", "record_test", qualityAgentTestArgs))
			return
		}
		if strings.Join(recorded, ",") != "record_test,record_group,record_test" {
			t.Errorf("records not committed before next request: %v", recorded)
		}
		if len(request.Messages) < 5 {
			t.Errorf("lost tool-call history: %+v", request.Messages)
		}
		var ids []string
		for _, message := range request.Messages {
			if message.Role == "tool" {
				ids = append(ids, message.ToolCallID)
			}
		}
		if strings.Join(ids, ",") != "read,test,group,test-again" {
			t.Errorf("tool results do not match native calls: %v", ids)
		}
		qualityAgentAnswer(w)
	})
	options := qualityAgentOptions()
	options.Read = func(_ context.Context, name string, args json.RawMessage) (json.RawMessage, []testquality.Citation, error) {
		if name != "review_source" || string(args) != `{"fileID":"file"}` {
			t.Errorf("wrong pinned evidence request: %s %s", name, args)
		}
		return json.RawMessage(`{"text":"retained source"}`), []testquality.Citation{citation}, nil
	}
	err := runQualityAgent(t.Context(), cfg, "Review pinned tests.", options, func(name string, args json.RawMessage, seen []testquality.Citation) error {
		if len(seen) != 1 || seen[0] != citation {
			t.Errorf("record lacks inspected citations: %+v", seen)
		}
		if name == "record_test" && string(args) != qualityAgentTestArgs || name == "record_group" && string(args) != qualityAgentGroupArgs {
			t.Errorf("record arguments changed: %s", args)
		}
		recorded = append(recorded, name)
		seen[0].FileID = "callback mutation"
		return nil
	})
	if err != nil || requests != 2 || len(recorded) != 3 {
		t.Fatalf("run: requests=%d records=%v error=%v", requests, recorded, err)
	}
}

func TestQualityAgentLoopReturnsToolFailuresToAssistant(t *testing.T) {
	var requests, reads, records int
	cfg := qualityAgentProvider(t, func(w http.ResponseWriter, _ *http.Request, request qualityAgentRequest) {
		requests++
		switch requests {
		case 1:
			qualityAgentAnswer(w, qualityAgentCall("bad-read", "review_source", `{}`),
				qualityAgentCall("unknown", "not_advertised", `{}`),
				qualityAgentCall("bad-record", "record_test", qualityAgentTestArgs))
		case 2:
			var failures int
			for _, message := range request.Messages {
				if message.Role == "tool" {
					var result map[string]any
					if json.Unmarshal([]byte(message.Content), &result) == nil && result["error"] != nil {
						failures++
					}
				}
			}
			if failures != 3 {
				t.Errorf("assistant did not receive all tool errors: %+v", request.Messages)
			}
			qualityAgentAnswer(w, qualityAgentCall("retry", "record_group", qualityAgentGroupArgs))
		default:
			qualityAgentAnswer(w)
		}
	})
	options := qualityAgentOptions()
	options.Read = func(context.Context, string, json.RawMessage) (json.RawMessage, []testquality.Citation, error) {
		reads++
		return nil, []testquality.Citation{{SnapshotID: "must-not-be-seen"}}, errors.New("source unavailable")
	}
	err := runQualityAgent(t.Context(), cfg, "Review.", options, func(_ string, _ json.RawMessage, seen []testquality.Citation) error {
		records++
		if len(seen) != 0 {
			t.Errorf("failed evidence read admitted citations: %+v", seen)
		}
		if records == 1 {
			return errors.New("citation not inspected")
		}
		return nil
	})
	if err != nil || reads != 1 || records != 2 || requests != 3 {
		t.Fatalf("tool recovery: reads=%d records=%d requests=%d error=%v", reads, records, requests, err)
	}
}

func TestQualityAgentLoopProviderFailurePreservesImmediateRecord(t *testing.T) {
	var requests, records int
	cfg := qualityAgentProvider(t, func(w http.ResponseWriter, _ *http.Request, _ qualityAgentRequest) {
		requests++
		if requests == 1 {
			qualityAgentAnswer(w, qualityAgentCall("record", "record_test", qualityAgentTestArgs))
		} else {
			if records != 1 {
				t.Error("record was deferred")
			}
			http.Error(w, "provider unavailable", http.StatusServiceUnavailable)
		}
	})
	err := runQualityAgent(t.Context(), cfg, "Review.", testquality.JudgmentOptions{}, func(string, json.RawMessage, []testquality.Citation) error {
		records++
		return nil
	})
	var providerErr *testquality.ProviderError
	if !errors.As(err, &providerErr) || !strings.Contains(err.Error(), "503") || records != 1 {
		t.Fatalf("provider failure lost or hidden: records=%d error=%v", records, err)
	}
}

func TestQualityAgentLoopContinuesWithoutDefaultStepLimit(t *testing.T) {
	var requests int
	cfg := qualityAgentProvider(t, func(w http.ResponseWriter, _ *http.Request, _ qualityAgentRequest) {
		requests++
		if requests <= 65 {
			qualityAgentAnswer(w, qualityAgentCall(fmt.Sprint(requests), "record_group", qualityAgentGroupArgs))
		} else {
			qualityAgentAnswer(w)
		}
	})
	err := runQualityAgent(t.Context(), cfg, "Review.", testquality.JudgmentOptions{}, func(string, json.RawMessage, []testquality.Citation) error { return nil })
	if err != nil || requests != 66 {
		t.Fatalf("unexpected implicit step limit: requests=%d error=%v", requests, err)
	}
}

func TestQualityAgentLoopExplicitBudgetsDoNotTruncate(t *testing.T) {
	for _, mode := range []string{"steps", "initial-context", "evidence-context"} {
		t.Run(mode, func(t *testing.T) {
			var requests int
			cfg := qualityAgentProvider(t, func(w http.ResponseWriter, _ *http.Request, _ qualityAgentRequest) {
				requests++
				qualityAgentAnswer(w, qualityAgentCall("read", "review_source", `{}`))
			})
			options := qualityAgentOptions()
			options.Read = func(context.Context, string, json.RawMessage) (json.RawMessage, []testquality.Citation, error) {
				data, _ := json.Marshal(strings.Repeat("x", 40000))
				return data, nil, nil
			}
			prompt := "Review."
			wantRequests, wantError := 1, "context"
			cfg.ContextBytes = 32768
			if mode == "steps" {
				cfg.MaxSteps = 1
				options.Read = func(context.Context, string, json.RawMessage) (json.RawMessage, []testquality.Citation, error) {
					return json.RawMessage(`{}`), nil, nil
				}
				wantError = "step"
			}
			if mode == "initial-context" {
				prompt = strings.Repeat("x", 40000)
				wantRequests = 0
			}
			err := runQualityAgent(t.Context(), cfg, prompt, options, nil)
			if err == nil || !strings.Contains(err.Error(), wantError) || requests != wantRequests {
				t.Fatalf("budget not surfaced honestly: requests=%d error=%v", requests, err)
			}
		})
	}
}

func TestQualityAgentLoopCancellation(t *testing.T) {
	t.Run("before-request", func(t *testing.T) {
		var requests atomic.Int32
		cfg := qualityAgentProvider(t, func(w http.ResponseWriter, _ *http.Request, _ qualityAgentRequest) {
			requests.Add(1)
			qualityAgentAnswer(w)
		})
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := runQualityAgent(ctx, cfg, "Review.", testquality.JudgmentOptions{}, nil); !errors.Is(err, context.Canceled) || requests.Load() != 0 {
			t.Fatalf("cancellation ignored: requests=%d error=%v", requests.Load(), err)
		}
	})
	t.Run("between-tools", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		cfg := qualityAgentProvider(t, func(w http.ResponseWriter, _ *http.Request, _ qualityAgentRequest) {
			qualityAgentAnswer(w, qualityAgentCall("read", "review_source", `{}`), qualityAgentCall("record", "record_test", qualityAgentTestArgs))
		})
		options := qualityAgentOptions()
		options.Read = func(context.Context, string, json.RawMessage) (json.RawMessage, []testquality.Citation, error) {
			cancel()
			return nil, nil, context.Canceled
		}
		records := 0
		err := runQualityAgent(ctx, cfg, "Review.", options, func(string, json.RawMessage, []testquality.Citation) error {
			records++
			return nil
		})
		if !errors.Is(err, context.Canceled) || records != 0 {
			t.Fatalf("continued recording after cancellation: records=%d error=%v", records, err)
		}
	})
}

func TestQualityAgentLoopTracesReadableProgress(t *testing.T) {
	cfg := qualityAgentProvider(t, func(w http.ResponseWriter, _ *http.Request, _ qualityAgentRequest) {
		qualityAgentAnswer(w)
	})
	var kinds, progress []string
	err := runQualityAgent(t.Context(), cfg, "Review pinned tests.", testquality.JudgmentOptions{
		Trace: func(event testquality.ReviewTrace) error {
			kinds = append(kinds, event.Kind)
			if event.Kind == "request" && (len(event.Messages) == 0 || event.Messages[0].Content != "Review pinned tests." || len(event.Tools) < 2) {
				t.Errorf("request trace lacks model context: %+v", event)
			}
			if event.Kind == "message" {
				if event.Answer == nil {
					t.Error("progress trace lacks assistant answer")
				} else {
					progress = append(progress, event.Answer.Content)
				}
			}
			return nil
		},
	}, nil)
	if err != nil || len(kinds) == 0 || kinds[0] != "request" || strings.Join(progress, ",") != "Finished." {
		t.Fatalf("readable progress missing: kinds=%v progress=%v error=%v", kinds, progress, err)
	}
}

func TestQualityAgentLoopPropagatesTraceErrors(t *testing.T) {
	for _, kind := range []string{"request", "message", "tool"} {
		t.Run(kind, func(t *testing.T) {
			var requests, records int
			cfg := qualityAgentProvider(t, func(w http.ResponseWriter, _ *http.Request, _ qualityAgentRequest) {
				requests++
				if kind == "tool" {
					qualityAgentAnswer(w, qualityAgentCall("record", "record_test", qualityAgentTestArgs))
				} else {
					qualityAgentAnswer(w)
				}
			})
			traceErr := errors.New("progress publication failed")
			err := runQualityAgent(t.Context(), cfg, "Review.", testquality.JudgmentOptions{
				Trace: func(event testquality.ReviewTrace) error {
					if event.Kind == kind {
						return traceErr
					}
					return nil
				},
			}, func(string, json.RawMessage, []testquality.Citation) error {
				records++
				return nil
			})
			wantRequests, wantRecords := 1, 0
			if kind == "request" {
				wantRequests = 0
			}
			if kind == "tool" {
				wantRecords = 1
			}
			if !errors.Is(err, traceErr) || requests != wantRequests || records != wantRecords {
				t.Fatalf("trace failure ignored: requests=%d records=%d error=%v", requests, records, err)
			}
		})
	}
}

func TestQualityAgentLoopCompactsSavedRecordsAndRequiresEvidenceReread(t *testing.T) {
	const prompt = "Review the pinned tests and contracts; preserve this original instruction."
	citation := testquality.Citation{SnapshotID: "snapshot", FileID: "file", StartLine: 3, EndLine: 8}
	var requests, reads, records, rejected int
	cfg := qualityAgentProvider(t, func(w http.ResponseWriter, _ *http.Request, request qualityAgentRequest) {
		requests++
		switch requests {
		case 1:
			qualityAgentAnswer(w,
				qualityAgentCall("read", "review_source", `{}`),
				qualityAgentCall("test", "record_test", qualityAgentTestArgs),
				qualityAgentCall("group", "record_group", qualityAgentGroupArgs))
		case 2:
			if len(request.Messages) == 0 || request.Messages[0].Content != prompt {
				t.Errorf("compaction lost original prompt: %+v", request.Messages)
			}
			var content strings.Builder
			for _, message := range request.Messages {
				if message.Role == "tool" || len(message.ToolCalls) != 0 {
					t.Errorf("compaction retained old %s tool conversation", message.Role)
				}
				content.WriteString(message.Content)
			}
			text := content.String()
			for _, want := range []string{"compacted", "saved", "record_test", "record_group", "Checks the return value.", "Independent collective judgment."} {
				if !strings.Contains(text, want) {
					t.Errorf("compaction omitted %q", want)
				}
			}
			if strings.Contains(text, "DROPPED-SOURCE") {
				t.Error("old source was retained")
			}
			qualityAgentAnswer(w, qualityAgentCall("stale", "record_test", qualityAgentTestArgs))
		case 3:
			last := request.Messages[len(request.Messages)-1]
			if last.Role != "tool" || !strings.Contains(last.Content, "re-read") {
				t.Errorf("missing stale evidence rejection for role %s", last.Role)
			}
			qualityAgentAnswer(w, qualityAgentCall("reread", "review_source", `{}`), qualityAgentCall("fresh", "record_group", qualityAgentGroupArgs))
		default:
			qualityAgentAnswer(w)
		}
	})
	cfg.ContextBytes = 32768
	options := qualityAgentOptions()
	options.Read = func(context.Context, string, json.RawMessage) (json.RawMessage, []testquality.Citation, error) {
		reads++
		text := "re-read source"
		if reads == 1 {
			text = "DROPPED-SOURCE" + strings.Repeat("x", 25000)
		}
		raw, _ := json.Marshal(text)
		return raw, []testquality.Citation{citation}, nil
	}
	err := runQualityAgent(t.Context(), cfg, prompt, options, func(_ string, _ json.RawMessage, seen []testquality.Citation) error {
		if len(seen) == 0 {
			rejected++
			return errors.New("re-read inspected source before recording")
		}
		if len(seen) != 1 || seen[0] != citation {
			t.Errorf("unexpected citation authority after compaction: %+v", seen)
		}
		records++
		return nil
	})
	if err != nil || requests != 4 || reads != 2 || records != 3 || rejected != 1 {
		t.Fatalf("compacted continuation: requests=%d reads=%d records=%d rejected=%d error=%v", requests, reads, records, rejected, err)
	}
}

func TestQualityAgentLoopRejectsLedgerThatCannotFitContext(t *testing.T) {
	var requests, records int
	args, err := json.Marshal(map[string]any{
		"snapshotID": "snapshot", "symbolID": "test", "craftsmanship": 3, "assertionStrength": 2,
		"rationale": strings.Repeat("x", 40000), "fileID": "file", "startLine": 3, "endLine": 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := qualityAgentProvider(t, func(w http.ResponseWriter, _ *http.Request, _ qualityAgentRequest) {
		requests++
		qualityAgentAnswer(w, qualityAgentCall("record", "record_test", string(args)))
	})
	cfg.ContextBytes = 32768
	err = runQualityAgent(t.Context(), cfg, "Review.", testquality.JudgmentOptions{}, func(string, json.RawMessage, []testquality.Citation) error {
		records++
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "context") || requests != 1 || records != 1 {
		t.Fatalf("oversized saved ledger was truncated or lost: requests=%d records=%d error=%v", requests, records, err)
	}
}

type qualityAgentPersistenceFailure struct{ cause error }

func (e qualityAgentPersistenceFailure) Error() string { return "persistence failure" }
func (e qualityAgentPersistenceFailure) Unwrap() error { return e.cause }

func TestQualityAgentLoopStopsImmediatelyOnPersistenceFailure(t *testing.T) {
	for _, name := range []string{"record_test", "record_group", "review_source"} {
		t.Run(name, func(t *testing.T) {
			failure := qualityAgentPersistenceFailure{cause: new(qualityPersistenceError)}
			var requests, records, reads int
			cfg := qualityAgentProvider(t, func(w http.ResponseWriter, _ *http.Request, _ qualityAgentRequest) {
				requests++
				if requests == 1 {
					args := qualityAgentTestArgs
					if name == "record_group" {
						args = qualityAgentGroupArgs
					}
					qualityAgentAnswer(w,
						qualityAgentCall("failure", name, args),
						qualityAgentCall("must-not-run", "record_test", qualityAgentTestArgs))
				} else {
					qualityAgentAnswer(w)
				}
			})
			options := qualityAgentOptions()
			options.Read = func(context.Context, string, json.RawMessage) (json.RawMessage, []testquality.Citation, error) {
				reads++
				return nil, nil, failure
			}
			err := runQualityAgent(t.Context(), cfg, "Review.", options, func(string, json.RawMessage, []testquality.Citation) error {
				records++
				return failure
			})
			wantReads, wantRecords := 0, 1
			if name == "review_source" {
				wantReads, wantRecords = 1, 0
			}
			if !errors.Is(err, failure) || requests != 1 || reads != wantReads || records != wantRecords {
				t.Fatalf("persistence failure did not stop tool processing: requests=%d reads=%d records=%d error=%v", requests, reads, records, err)
			}
		})
	}
}
