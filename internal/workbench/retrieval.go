package workbench

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/russellbrasser/go-review/internal/intelligence"
	"github.com/russellbrasser/go-review/internal/retrieval"
	"github.com/russellbrasser/go-review/internal/review"
)

type retrievalState struct {
	mu      sync.Mutex
	config  intelligence.EmbeddingConfig
	root    string
	service *reviewMCP
	engine  *retrieval.Engine
	jobID   string
	phase   string
	err     string
	cancel  context.CancelFunc
	done    chan struct{}
	closed  bool
}

type retrievalInput struct {
	SnapshotID string `json:"snapshotID" jsonschema:"Exact retained snapshot ID from review_overview"`
	Query      string `json:"query,omitempty" jsonschema:"Natural-language query; use query or entityID, not both"`
	EntityID   string `json:"entityID,omitempty" jsonschema:"Retained symbol/test/document ID for code-to-code candidates"`
	Offset     int    `json:"offset,omitempty" jsonschema:"Zero-based offset; follow nextOffset"`
	Limit      int    `json:"limit,omitempty" jsonschema:"1-100, default 10; structural neighbors may supplement results"`
}

func (s *Server) ConfigureEmbeddings(config intelligence.EmbeddingConfig, root string) error {
	if err := config.Validate(); err != nil {
		return err
	}
	s.retrievalMu.Lock()
	defer s.retrievalMu.Unlock()
	if s.retrievalClosed || s.retrieval != nil {
		return fmt.Errorf("embedding configuration is already initialized or shutting down")
	}
	if config.Enabled {
		if root == "" {
			return fmt.Errorf("embedding cache root is required")
		}
		s.retrieval = &retrievalState{config: config, root: root, service: s.newChatService(), phase: "not-ready"}
	}
	return nil
}

func (s *Server) retrievalOwner() *retrievalState {
	s.retrievalMu.Lock()
	defer s.retrievalMu.Unlock()
	return s.retrieval
}

func (s *Server) retrievalAvailable(snapshotID string) bool {
	state := s.retrievalOwner()
	if state == nil {
		return false
	}
	state.mu.Lock()
	engine, closed := state.engine, state.closed
	state.mu.Unlock()
	return !closed && engine != nil && engine.Status(snapshotID).State == "ready"
}

func (s *Server) retrievalStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		failure(w, 405, "GET required")
		return
	}
	state := s.retrievalOwner()
	w.Header().Set("Content-Type", "application/json")
	if state == nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"enabled": false, "state": "disabled"})
		return
	}
	snapshot, _, err := state.service.snapshot(r.URL.Query().Get("snapshotID"), "")
	if err != nil {
		failure(w, 409, err.Error())
		return
	}
	state.mu.Lock()
	engine, phase, diagnostic, jobID := state.engine, state.phase, state.err, state.jobID
	state.mu.Unlock()
	output := map[string]any{"enabled": true, "model": state.config.Model, "protocol": state.config.Protocol,
		"external": (intelligence.Config{BaseURL: state.config.BaseURL}).External(),
		"snapshot": snapshot.info, "state": phase, "error": diagnostic, "jobSnapshotID": jobID}
	if engine != nil {
		status := engine.Status(snapshot.info.ID)
		output["status"] = status
		if jobID != snapshot.info.ID || phase != "discovering" {
			output["state"] = status.State
			if status.Error != "" {
				output["error"] = status.Error
			}
		}
	}
	_ = json.NewEncoder(w).Encode(output)
}

func (s *Server) retrievalIndex(w http.ResponseWriter, r *http.Request) {
	var input struct {
		SnapshotID      string `json:"snapshotID"`
		Action          string `json:"action"`
		ConfirmExternal bool   `json:"confirmExternal"`
	}
	if !decodeRequest(w, r, &input) {
		return
	}
	state := s.retrievalOwner()
	if state == nil {
		failure(w, 409, "Configure and enable embeddings.json before preparing semantic retrieval.")
		return
	}
	if input.SnapshotID == "" {
		failure(w, 400, "snapshotID is required")
		return
	}
	snapshot, _, err := state.service.snapshot(input.SnapshotID, "")
	if err != nil {
		failure(w, 409, err.Error())
		return
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if input.Action == "cancel" {
		if state.jobID != input.SnapshotID || state.cancel == nil {
			failure(w, 409, "No indexing job running for this snapshot.")
			return
		}
		state.cancel()
		w.WriteHeader(202)
		return
	}
	if input.Action != "start" {
		failure(w, 400, "action must be start or cancel")
		return
	}
	if state.closed || state.cancel != nil {
		failure(w, 409, "Semantic indexing is shutting down or already running.")
		return
	}
	if (intelligence.Config{BaseURL: state.config.BaseURL}).External() && !input.ConfirmExternal {
		failure(w, 409, "Explicit embedding-provider consent is required: retained source will leave this machine.")
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	state.cancel, state.done, state.jobID = cancel, make(chan struct{}), input.SnapshotID
	state.phase, state.err = "discovering", ""
	go state.build(ctx, snapshot)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(202)
	_ = json.NewEncoder(w).Encode(map[string]string{"state": "preparing", "snapshotID": input.SnapshotID})
}

func (state *retrievalState) build(ctx context.Context, snapshot *querySnapshot) {
	state.mu.Lock()
	engine, done, cancel := state.engine, state.done, state.cancel
	state.mu.Unlock()
	defer close(done)
	defer cancel()
	var err error
	if engine == nil {
		provider := intelligence.EmbeddingProvider{Config: state.config}
		var profile intelligence.EmbeddingProfile
		profile, err = provider.Discover(ctx)
		if err == nil {
			profile.Trials, err = retrieval.Trials(ctx, state.config, func(ctx context.Context, inputs []string) ([][]float32, error) {
				return provider.EmbedForProfile(ctx, inputs, profile)
			})
		}
		if err == nil {
			engine, err = retrieval.NewEngine(state.root, state.config, profile, func(ctx context.Context, inputs []string) ([][]float32, error) {
				return provider.EmbedForProfile(ctx, inputs, profile)
			})
		}
		if err == nil {
			state.mu.Lock()
			state.engine = engine
			state.mu.Unlock()
		}
	}
	if err == nil {
		state.mu.Lock()
		state.phase = "indexing"
		state.mu.Unlock()
		err = engine.Build(ctx, snapshot.info.ID, snapshot.report)
	}
	state.mu.Lock()
	state.cancel, state.phase = nil, "ready"
	if err != nil {
		state.phase, state.err = "failed", err.Error()
		if ctx.Err() != nil {
			state.phase = "canceled"
		}
	}
	state.mu.Unlock()
}

func (s *Server) queryRetrieval(ctx context.Context, input retrievalInput) (retrieval.Result, error) {
	state := s.retrievalOwner()
	if state == nil {
		return retrieval.Result{}, fmt.Errorf("embeddings are disabled")
	}
	if input.SnapshotID == "" {
		return retrieval.Result{}, fmt.Errorf("snapshotID is required")
	}
	snapshot, _, err := state.service.snapshot(input.SnapshotID, "")
	if err != nil {
		return retrieval.Result{}, err
	}
	state.mu.Lock()
	engine := state.engine
	state.mu.Unlock()
	if engine == nil {
		return retrieval.Result{}, fmt.Errorf("semantic index is not ready; prepare the retained snapshot explicitly")
	}
	return retrievePinned(ctx, engine, snapshot.report, input)
}

func retrievePinned(ctx context.Context, engine *retrieval.Engine, report *review.Report, input retrievalInput) (retrieval.Result, error) {
	if input.Limit == 0 {
		input.Limit = 10
	}
	if input.Offset < 0 || input.Limit < 1 || input.Limit > 100 {
		return retrieval.Result{}, fmt.Errorf("retrieval requires nonnegative offset and limit 1-100")
	}
	if err := engine.BindSnapshot(input.SnapshotID, report); err != nil {
		return retrieval.Result{}, err
	}
	result, err := engine.Search(ctx, input.SnapshotID, input.Query, input.EntityID, input.Offset, input.Limit)
	if err != nil {
		return result, err
	}
	data, err := json.Marshal(result)
	if err != nil {
		return result, err
	}
	if len(data) > 32<<10 {
		return result, fmt.Errorf("retrieval response exceeds 32 KiB; request fewer candidates")
	}
	return result, nil
}

func (s *Server) retrievalQuery(w http.ResponseWriter, r *http.Request) {
	var input retrievalInput
	if !decodeRequest(w, r, &input) {
		return
	}
	result, err := s.queryRetrieval(r.Context(), input)
	if err != nil {
		failure(w, 422, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

func registerRetrievalTool(server *mcp.Server, work *Server) {
	external := false
	if state := work.retrievalOwner(); state != nil {
		external = (intelligence.Config{BaseURL: state.config.BaseURL}).External()
	}
	mcp.AddTool(server, &mcp.Tool{Name: "review_retrieve",
		Description: "Best-effort hybrid code/text candidates from an explicitly prepared retained snapshot. Uses only the configured embedding model; returns source IDs/ranges, not inspected source or proof of equivalence. Existing review_search remains literal.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: boolPointer(external)}},
		func(ctx context.Context, _ *mcp.CallToolRequest, input retrievalInput) (*mcp.CallToolResult, retrieval.Result, error) {
			result, err := work.queryRetrieval(ctx, input)
			return nil, result, err
		})
}

func (s *Server) ShutdownRetrieval(ctx context.Context) error {
	s.retrievalMu.Lock()
	s.retrievalClosed = true
	state := s.retrieval
	s.retrievalMu.Unlock()
	if state == nil {
		return nil
	}
	state.mu.Lock()
	state.closed = true
	if state.cancel != nil {
		state.cancel()
	}
	done := state.done
	state.mu.Unlock()
	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	state.mu.Lock()
	engine := state.engine
	state.mu.Unlock()
	if engine != nil {
		return engine.Close(ctx)
	}
	return nil
}
