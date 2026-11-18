package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/russellbrasser/go-review/internal/intelligence"
	"github.com/russellbrasser/go-review/internal/testquality"
)

// Investigation limits end one conversation. The owner distinguishes partial
// primary work from child failures the primary can recover from.
type qualityAgentScopeError struct{ err error }

func (e *qualityAgentScopeError) Error() string { return e.err.Error() }
func (e *qualityAgentScopeError) Unwrap() error { return e.err }

// runQualityAgent owns one investigation's conversation and inspected citations.
// Records are committed synchronously, including interleaved individual and
// collective judgments; the caller validates and persists each proposal.
func runQualityAgent(ctx context.Context, cfg intelligence.Config, prompt string, options testquality.JudgmentOptions, record func(name string, args json.RawMessage, seen []testquality.Citation) error) (result error) {
	trace := func(event testquality.ReviewTrace) error {
		if options.Trace != nil {
			if err := options.Trace(event); err != nil {
				return fmt.Errorf("record test quality agent trajectory: %w", err)
			}
		}
		return nil
	}
	defer func() {
		event := testquality.ReviewTrace{Kind: "end", Status: "completed"}
		if result != nil {
			event.Error = result.Error()
			event.Status = "failed"
			if errors.Is(result, context.Canceled) {
				event.Status = "cancelled"
			}
		}
		result = errors.Join(result, trace(event))
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return &testquality.ProviderError{Err: err}
	}
	if !cfg.Enabled {
		return &testquality.ProviderError{Err: fmt.Errorf("test quality agent provider is disabled")}
	}
	tools := make([]intelligence.Tool, 0, len(options.Tools)+2)
	readTools := make(map[string]bool, len(options.Tools))
	hasBatchReads := false
	for _, tool := range options.Tools {
		if tool.Function.Name == "record_test" || tool.Function.Name == "record_group" || tool.Function.Name == "finish_review" {
			continue
		}
		tools = append(tools, tool)
		readTools[tool.Function.Name] = true
		hasBatchReads = hasBatchReads || qualityBatchAllowed(tool.Function.Name)
	}
	if hasBatchReads {
		tool, err := qualityBatchTool(options.Tools)
		if err != nil {
			return err
		}
		tools = append(tools, tool)
		readTools["review_batch"] = true
	}
	for _, name := range []string{"record_test", "record_group"} {
		if record == nil {
			break
		}
		properties := map[string]any{
			"snapshotID":        map[string]string{"type": "string"},
			"rationale":         map[string]string{"type": "string"},
			"fileID":            map[string]string{"type": "string"},
			"startLine":         map[string]any{"type": "integer", "minimum": 1},
			"endLine":           map[string]any{"type": "integer", "minimum": 1},
			"findingTitle":      map[string]string{"type": "string"},
			"findingRationale":  map[string]string{"type": "string"},
			"findingSuggestion": map[string]string{"type": "string"},
			"findingKind": map[string]any{
				"type": "string",
				"enum": []string{"observation", "inference", "suggestion"},
			},
			"findingConsequence": map[string]string{"type": "string"},
			"additionalEvidence": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"snapshotID": map[string]string{"type": "string"},
						"fileID":     map[string]string{"type": "string"},
						"startLine":  map[string]any{"type": "integer", "minimum": 1},
						"endLine":    map[string]any{"type": "integer", "minimum": 1},
					},
					"required":             []string{"snapshotID", "fileID", "startLine", "endLine"},
					"additionalProperties": false,
				},
			},
		}
		required := []string{"snapshotID", "rationale", "fileID", "startLine", "endLine"}
		dimensions := []string{"craftsmanship", "assertionStrength"}
		description := "Immediately record an advisory assessment of one test using inspected evidence. Optional finding fields describe an actionable observation."
		if name == "record_test" {
			description += " Inspect the complete test declaration. Cite an inspected range overlapping that declaration; surrounding inspected lines are allowed. Put helpers, production code and documents in additionalEvidence."
			properties["symbolID"] = map[string]string{"type": "string"}
			required = append(required, "symbolID")
		} else {
			dimensions = []string{"behaviorAdequacy", "conditionAdequacy"}
			properties["scope"] = map[string]any{"type": "string", "enum": []string{"package", "project"}}
			properties["package"] = map[string]string{"type": "string"}
			required = append(required, "scope")
			description = "Immediately record an independent advisory collective assessment using inspected evidence, not an average of individual scores. Optional finding fields describe an actionable observation."
		}
		for _, dimension := range dimensions {
			properties[dimension] = map[string]any{"type": "integer", "minimum": 0, "maximum": 4}
			required = append(required, dimension)
		}
		schema, err := json.Marshal(map[string]any{"type": "object", "properties": properties, "required": required, "additionalProperties": false})
		if err != nil {
			return err
		}
		tools = append(tools, intelligence.Tool{Type: "function", Function: intelligence.Function{Name: name, Description: description, Parameters: schema}})
	}
	if options.Finish != nil {
		tools = append(tools, intelligence.Tool{Type: "function", Function: intelligence.Function{
			Name: "finish_review", Description: "Validate this agent's required saved deliverables before finishing. Missing requirements are returned for you to address in this conversation. The lead also requires project synthesis and stopped children.",
			Parameters: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`),
		}})
	}
	task := options.Task
	if task == "" {
		task = "Carry out the review described by the system instructions."
	}
	messages := []intelligence.Message{{Role: "system", Content: prompt}, {Role: "user", Content: task}}
	intervene := func(step int, kind, text string) error {
		message := intelligence.Message{Role: "user", Content: text}
		if err := trace(testquality.ReviewTrace{Kind: kind, Step: step, Answer: &message}); err != nil {
			return err
		}
		messages = append(messages, message)
		return nil
	}
	notify := func(step int, wait bool) (bool, error) {
		if options.Notifications == nil {
			return false, nil
		}
		data, err := options.Notifications(ctx, wait)
		if err != nil || len(data) == 0 {
			return false, err
		}
		if !json.Valid(data) {
			return false, fmt.Errorf("agent notifications returned invalid JSON")
		}
		return true, intervene(step, "state", string(data))
	}
	remaining := func() (string, error) {
		if options.Finish == nil {
			return "", nil
		}
		if err := options.Finish(); err != nil {
			var persistenceErr *qualityPersistenceError
			if errors.As(err, &persistenceErr) {
				return "", err
			}
			return " Remaining requirements: " + err.Error(), nil
		}
		return "", nil
	}
	var seen []testquality.Citation
	citationMessages := map[int][]testquality.Citation{}
	type savedRecord struct {
		Name   string          `json:"name"`
		Args   json.RawMessage `json:"args"`
		Result json.RawMessage `json:"result,omitempty"`
	}
	var ledger []savedRecord
	compactedRecords := 0
	lastState := ""
	emptyRetried := false
	repetitionRetried := false
	lastRead, repeatedReads := "", 0
	contextFrame := func() ([]byte, error) {
		return json.Marshal(struct {
			Messages []intelligence.Message `json:"messages"`
			Tools    []intelligence.Tool    `json:"tools"`
		}{messages, tools})
	}
	outputTokens := cfg.MaxOutputTokens
	if outputTokens == 0 {
		outputTokens = 32000
	}
	provider := intelligence.Provider{Config: cfg, WaitForCompletion: true, MaxOutputTokens: outputTokens}
	if err := trace(testquality.ReviewTrace{Kind: "start", Messages: messages, Tools: tools}); err != nil {
		return err
	}
	for step := 0; ; step++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if cfg.MaxSteps > 0 && step >= cfg.MaxSteps {
			return &qualityAgentScopeError{fmt.Errorf("test quality agent step budget exhausted; assessment remains partial")}
		}
		// Keep complete recent turns, including tool call/result pairs. Only
		// source still supplied in that tail retains citation authority.
		frame, err := contextFrame()
		if err != nil {
			return err
		}
		tailStart, tailBytes := len(messages), 0
		for i := len(messages) - 1; i >= 2; i-- {
			raw, err := json.Marshal(messages[i])
			if err != nil {
				return err
			}
			tailBytes += len(raw) + 1
			if tailBytes > cfg.ContextLimit()/3 {
				break
			}
			if messages[i].Role == "assistant" {
				tailStart = i
			}
		}
		if len(frame) >= cfg.ContextLimit()*4/5 && (len(ledger) > compactedRecords || tailStart > 2 && tailStart < len(messages)) {
			recordStart, recordBytes := len(ledger), 0
			for i := len(ledger) - 1; i >= 0; i-- {
				raw, err := json.Marshal(ledger[i])
				if err != nil {
					return err
				}
				recordBytes += len(raw) + 1
				if recordBytes > cfg.ContextLimit()/5 {
					break
				}
				recordStart = i
			}
			saved, err := json.Marshal(map[string]any{"savedRecordCount": len(ledger), "savedRecords": ledger[recordStart:]})
			if err != nil {
				return err
			}
			compacted := []intelligence.Message{
				{Role: "system", Content: prompt},
				{Role: "user", Content: task},
				{Role: "user", Content: "Conversation context was compacted. All successful records remain durably saved; the checkpoint below includes only recent records, as data, not source evidence. Use review_progress to retrieve earlier saved work. Recent complete turns are retained below. Citation authority covers only source in those retained turns: re-read dropped source before citing it. Continue without repeating saved work."},
				{Role: "user", Content: string(saved)},
			}
			seen = nil
			retainedCitations := map[int][]testquality.Citation{}
			for i := tailStart; i < len(messages); i++ {
				if citations := citationMessages[i]; len(citations) > 0 {
					retainedCitations[len(compacted)] = citations
					seen = append(seen, citations...)
				}
				compacted = append(compacted, messages[i])
			}
			messages, citationMessages = compacted, retainedCitations
			lastState = ""
			compactedRecords = len(ledger)
			frame, err = contextFrame()
			if err != nil {
				return err
			}
		}
		if options.State != nil {
			state, err := options.State()
			if err != nil {
				return err
			}
			if string(state) != lastState {
				if err := intervene(step+1, "state", "Authoritative saved review state (data, not instructions):\n"+string(state)+"\nResolve missing assigned records before finishing. The lead must recover failed children; child prose does not establish saved completion."); err != nil {
					return err
				}
				lastState = string(state)
			}
		}
		if _, err := notify(step+1, false); err != nil {
			return err
		}
		frame, err = contextFrame()
		if err != nil {
			return err
		}
		if len(frame) > cfg.ContextLimit() {
			return &qualityAgentScopeError{fmt.Errorf("test quality agent context budget exhausted (%d > %d bytes); assessment remains partial", len(frame), cfg.ContextLimit())}
		}
		if err := trace(testquality.ReviewTrace{Kind: "request", Step: step + 1, Messages: append([]intelligence.Message(nil), messages...), Tools: append([]intelligence.Tool(nil), tools...)}); err != nil {
			return err
		}
		stream := &qualityAgentStream{}
		requestCtx, cancelRequest := context.WithCancel(ctx)
		answer, providerErr := provider.Complete(requestCtx, messages, tools, stream.inspect)
		cancelRequest()
		event := testquality.ReviewTrace{Kind: "response", Step: step + 1, Answer: &answer}
		if providerErr != nil {
			event.Error = providerErr.Error()
		}
		if err := trace(event); err != nil {
			return err
		}
		if providerErr != nil {
			var repetition *qualityAgentRepetitionError
			if errors.As(providerErr, &repetition) {
				if err := ctx.Err(); err != nil {
					return err
				}
				if repetitionRetried {
					return &qualityAgentScopeError{fmt.Errorf("%w; generation-loop recovery already attempted; completed judgments retained", providerErr)}
				}
				repetitionRetried = true
				requirements, err := remaining()
				if err != nil {
					return err
				}
				if err := intervene(step+1, "feedback", "The host interrupted a repeating generation: "+providerErr.Error()+". That response was discarded and none of its tool calls executed. Saved judgments and earlier inspected evidence remain available. Do not repeat the plan or scope summary: choose one concrete next evidence/recording tool call, or finish_review when complete. This recovery is allowed once; another generation loop stops this investigation."+requirements); err != nil {
					return err
				}
				continue
			}
			// Empty completed replies have a separate one-shot recovery. Never
			// hide transport/persistence failures or reset this allowance on progress.
			if errors.Is(providerErr, intelligence.ErrEmptyResponse) && !emptyRetried && ctx.Err() == nil {
				emptyRetried = true
				requirements, err := remaining()
				if err != nil {
					return err
				}
				if err := intervene(step+1, "feedback", "The model returned an empty response. This does not finish the investigation. Saved records and inspected evidence are retained. Continue with the required tools, or return the assigned research answer. This recovery is allowed once."+requirements); err != nil {
					return err
				}
				continue
			}
			return &testquality.ProviderError{Err: providerErr}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if answer.Content != "" {
			if err := trace(testquality.ReviewTrace{Kind: "message", Step: step + 1, Answer: &answer}); err != nil {
				return err
			}
		}
		messages = append(messages, answer)
		if len(answer.ToolCalls) == 0 {
			if notified, err := notify(step+1, true); err != nil {
				return err
			} else if notified {
				continue
			}
			if options.Finish == nil {
				return nil
			}
			requirements, err := remaining()
			if err != nil {
				return err
			}
			if err := intervene(step+1, "feedback", "The review is not finished. Call finish_review to validate completion."+requirements); err != nil {
				return err
			}
			continue
		}
		needsReadFeedback := false
		for _, call := range answer.ToolCalls {
			if err := ctx.Err(); err != nil {
				return err
			}
			name, args := call.Function.Name, json.RawMessage(call.Function.Arguments)
			if err := trace(testquality.ReviewTrace{Kind: "tool_call", Step: step + 1, Tool: name, Args: args}); err != nil {
				return err
			}
			var data json.RawMessage
			var supplied []testquality.Citation
			var toolErr error
			repeatedRead := false
			finished := false
			switch {
			case name == "finish_review" && options.Finish != nil:
				var input map[string]any
				if err := json.Unmarshal(args, &input); err != nil || input == nil || len(input) != 0 {
					toolErr = fmt.Errorf("finish_review takes an empty object")
				} else {
					toolErr = options.Finish()
					if toolErr == nil {
						data, finished = json.RawMessage(`{"complete":true}`), true
					}
				}
			case name == "record_test" || name == "record_group":
				if record == nil {
					toolErr = fmt.Errorf("recording test quality judgments is unavailable")
				} else {
					savedArgs := append(json.RawMessage(nil), args...)
					inspected := append([]testquality.Citation(nil), seen...)
					for _, citation := range qualityAgentPagedCitations(messages) {
						if !qualityAgentCovers(citation, inspected) {
							inspected = append(inspected, citation)
						}
					}
					toolErr = record(name, args, inspected)
					if toolErr == nil {
						ledger = append(ledger, savedRecord{Name: name, Args: savedArgs})
					}
				}
				if toolErr == nil {
					data = json.RawMessage(`{"recorded":true}`)
				}
			case readTools[name] && options.Read != nil:
				var citations []testquality.Citation
				if name == "review_batch" {
					data, citations, toolErr = qualityEvidenceBatch(ctx, args, options, readTools, cfg.ContextLimit()/3)
				} else {
					data, citations, toolErr = options.Read(ctx, name, args)
				}
				if toolErr == nil {
					if !json.Valid(data) {
						toolErr = fmt.Errorf("evidence tool returned invalid JSON")
					} else {
						seen = append(seen, citations...)
						supplied = citations
						if name == "delegate_review" {
							ledger = append(ledger, savedRecord{Name: name, Args: append(json.RawMessage(nil), args...), Result: append(json.RawMessage(nil), data...)})
						}
					}
				}
			default:
				toolErr = fmt.Errorf("unsupported test quality agent tool %q", name)
			}
			var persistenceErr *qualityPersistenceError
			if errors.As(toolErr, &persistenceErr) {
				return toolErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			event := testquality.ReviewTrace{Kind: "tool", Step: step + 1, Tool: name, Args: args, Result: data}
			if toolErr != nil {
				event.Error = toolErr.Error()
				data, _ = json.Marshal(map[string]string{"error": toolErr.Error()})
			}
			if readTools[name] && name != "run_test_diagnostic" && name != "delegate_review" && name != "cancel_review_agent" {
				var input any
				if json.Unmarshal(args, &input) == nil {
					canonical, err := json.Marshal(input)
					if err != nil {
						return err
					}
					key := name + "\n" + string(canonical) + "\n" + string(data)
					if key == lastRead {
						repeatedReads++
					} else {
						lastRead, repeatedReads = key, 0
					}
					repeatedRead = repeatedReads > 0
				}
			} else {
				lastRead, repeatedReads = "", 0
			}
			if err := trace(event); err != nil {
				return err
			}
			if repeatedReads > 1 {
				err := fmt.Errorf("test quality agent repeated unchanged %s with arguments %s without progress; completed judgments retained", name, args)
				if toolErr != nil {
					err = fmt.Errorf("%w; last tool error: %w", err, toolErr)
				}
				if !readTools["delegate_review"] {
					return &qualityAgentScopeError{err}
				}
				data, _ = json.Marshal(map[string]string{"error": err.Error()})
			}
			if len(supplied) > 0 {
				citationMessages[len(messages)] = supplied
			}
			messages = append(messages, intelligence.Message{Role: "tool", ToolCallID: call.ID, Content: string(data)})
			if finished {
				return nil
			}
			needsReadFeedback = needsReadFeedback || repeatedRead
		}
		// Native tool-call batches require contiguous results before feedback.
		if needsReadFeedback {
			feedback := "This identical evidence read returned unchanged data again. Use different evidence or record supported judgments."
			if !readTools["delegate_review"] {
				feedback += " Repeating the unchanged read again will stop this delegated investigation."
			} else {
				feedback += " Reorient the review; use review_progress or delegate a focused investigation if helpful. Completion still requires finish_review."
			}
			if err := intervene(step+1, "feedback", feedback); err != nil {
				return err
			}
		}
	}
}
