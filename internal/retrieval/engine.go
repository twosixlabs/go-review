package retrieval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/russellbrasser/go-review/internal/intelligence"
	"github.com/russellbrasser/go-review/internal/review"
)

type EmbedFunc func(context.Context, []string) ([][]float32, error)

type Status struct {
	State         string                        `json:"state"`
	SnapshotID    string                        `json:"snapshotID"`
	Error         string                        `json:"error,omitempty"`
	Completed     int                           `json:"completed"`
	Total         int                           `json:"total"`
	Requests      int                           `json:"requests"`
	InputBytes    int                           `json:"inputBytes"`
	Reused        int                           `json:"reused"`
	Parsed        int                           `json:"parsed"`
	ElapsedMillis int64                         `json:"elapsedMillis"`
	Profile       intelligence.EmbeddingProfile `json:"profile"`
}

type neighbor struct{ id, reason string }
type index struct {
	chunks    []Chunk
	vectors   [][]float32
	entities  map[string][]int
	neighbors map[string][]neighbor
	signature string
}

// Engine owns immutable indexes and one indexing writer. Model calls are
// serialized separately from state locks, with cancellable waiting and shutdown.
type Engine struct {
	mu         sync.RWMutex
	config     intelligence.EmbeddingConfig
	profile    intelligence.EmbeddingProfile
	root       string
	profileID  string
	persistent bool
	embed      EmbedFunc
	lifetime   context.Context
	stop       context.CancelFunc
	modelGate  chan struct{}
	work       sync.WaitGroup
	closed     bool
	buildID    string
	cancel     context.CancelFunc
	status     map[string]Status
	indexes    map[string]*index
	unitCache  map[string][]Chunk
	vectors    map[string][]float32
	queries    map[string][]float32
	signatures map[*review.Report]string
}

func NewEngine(root string, config intelligence.EmbeddingConfig, profile intelligence.EmbeddingProfile, embed EmbedFunc) (*Engine, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if !config.Enabled || embed == nil || profile.Dimensions <= 0 || profile.Model != config.Model || root == "" {
		return nil, fmt.Errorf("enabled config, discovered matching model, dimensions, cache root and encoder required")
	}
	identity := struct {
		Protocol, BaseURL, Model, ReturnedModel, Revision, Generation, QueryPrefix, DocumentPrefix string
		Dimensions, InputBytes, Format                                                             int
	}{config.Protocol, config.BaseURL, config.Model, profile.ReturnedModel, profile.Revision, profile.Generation,
		config.QueryPrefix, config.DocumentPrefix, profile.Dimensions, config.InputLimit(), 1}
	data, err := json.Marshal(identity)
	if err != nil {
		return nil, err
	}
	lifetime, stop := context.WithCancel(context.Background())
	profile.Limitations = append([]string{}, profile.Limitations...)
	return &Engine{config: config, profile: profile, root: root, profileID: digest(string(data)),
		persistent: profile.Revision != "" || profile.Generation != "", embed: embed,
		lifetime: lifetime, stop: stop, modelGate: make(chan struct{}, 1),
		status: map[string]Status{}, indexes: map[string]*index{}, unitCache: map[string][]Chunk{},
		vectors: map[string][]float32{}, queries: map[string][]float32{}, signatures: map[*review.Report]string{}}, nil
}

func (e *Engine) Status(snapshotID string) Status {
	e.mu.RLock()
	defer e.mu.RUnlock()
	status, ok := e.status[snapshotID]
	if !ok {
		status = Status{State: "not-ready", SnapshotID: snapshotID, Profile: e.profile}
	}
	status.Profile.Limitations = append([]string{}, status.Profile.Limitations...)
	return status
}

func (e *Engine) encode(ctx context.Context, inputs []string) ([][]float32, error) {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil, fmt.Errorf("retrieval owner is closed")
	}
	e.work.Add(1)
	e.mu.Unlock()
	defer e.work.Done()
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(e.lifetime, cancel)
	defer func() { stop(); cancel() }()
	select {
	case e.modelGate <- struct{}{}:
		defer func() { <-e.modelGate }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	vectors, err := e.embed(ctx, inputs)
	if err != nil {
		return nil, fmt.Errorf("encode retrieval input: %w", err)
	}
	if len(vectors) != len(inputs) {
		return nil, fmt.Errorf("encoder cardinality %d != %d", len(vectors), len(inputs))
	}
	normalized := make([][]float32, len(vectors))
	for i, vector := range vectors {
		if len(vector) != e.profile.Dimensions {
			return nil, fmt.Errorf("encoder dimensions %d != profile %d", len(vector), e.profile.Dimensions)
		}
		norm, err := vectorNorm(vector)
		if err != nil {
			return nil, err
		}
		normalized[i] = make([]float32, len(vector))
		for j, value := range vector {
			normalized[i][j] = float32(float64(value) / norm)
		}
	}
	return normalized, nil
}

func unitKey(unit sourceUnit, budget int) (string, error) {
	data, err := json.Marshal(unit)
	if err != nil {
		return "", err
	}
	return digest(fmt.Sprintf("%d:%s", budget, data)), nil
}

func reportSignature(r *review.Report) (string, error) {
	data, err := json.Marshal(struct {
		Module, Version string
		Units           []sourceUnit
		Calls           []review.Call
		References      []review.Reference
	}{r.Module, r.ToolVersion, sourceUnits(r), r.Calls, r.References})
	if err != nil {
		return "", err
	}
	return digest(string(data)), nil
}

// Published reports are immutable. Cache source/scope identity per report
// pointer so model tool calls never serialize the whole source corpus again.
func (e *Engine) signature(r *review.Report) (string, error) {
	e.mu.RLock()
	signature, found := e.signatures[r]
	e.mu.RUnlock()
	if found {
		return signature, nil
	}
	signature, err := reportSignature(r)
	if err != nil {
		return "", err
	}
	e.mu.Lock()
	e.signatures[r] = signature
	e.mu.Unlock()
	return signature, nil
}

// BindSnapshot reuses an already indexed identical retained source/scope under a
// pinned assessment identity. It never builds, encodes, or queries a live module.
func (e *Engine) BindSnapshot(snapshotID string, r *review.Report) error {
	if r == nil || snapshotID == "" {
		return fmt.Errorf("retained snapshot identity required")
	}
	signature, err := e.signature(r)
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return fmt.Errorf("retrieval owner is closed")
	}
	if ready := e.indexes[snapshotID]; ready != nil {
		if ready.signature != signature {
			return fmt.Errorf("snapshot identity reused for different retained source")
		}
		return nil
	}
	for _, ready := range e.indexes {
		if ready.signature == signature {
			e.indexes[snapshotID] = ready
			e.status[snapshotID] = Status{State: "ready", SnapshotID: snapshotID, Total: len(ready.chunks),
				Completed: len(ready.chunks), Profile: e.profile}
			return nil
		}
	}
	return fmt.Errorf("retrieval index is not ready for this pinned source; prepare its retained snapshot first")
}

func (e *Engine) Build(ctx context.Context, snapshotID string, r *review.Report) (result error) {
	if snapshotID == "" || r == nil {
		return fmt.Errorf("retained snapshot identity and report required")
	}
	signature, err := e.signature(r)
	if err != nil {
		return err
	}
	e.mu.Lock()
	if e.closed || e.buildID != "" {
		e.mu.Unlock()
		return fmt.Errorf("retrieval owner is closed or indexing already running")
	}
	if ready := e.indexes[snapshotID]; ready != nil {
		e.mu.Unlock()
		if ready.signature != signature {
			return fmt.Errorf("immutable retrieval snapshot changed")
		}
		return nil
	}
	ctx, cancel := context.WithCancel(ctx)
	e.buildID, e.cancel = snapshotID, cancel
	e.work.Add(1)
	status := Status{State: "indexing", SnapshotID: snapshotID, Profile: e.profile}
	e.status[snapshotID] = status
	e.mu.Unlock()
	started := time.Now()
	defer func() {
		cancel()
		e.mu.Lock()
		e.buildID, e.cancel = "", nil
		status.ElapsedMillis = time.Since(started).Milliseconds()
		if result != nil {
			status.State, status.Error = "failed", result.Error()
			if errors.Is(result, context.Canceled) || errors.Is(result, context.DeadlineExceeded) {
				status.State = "canceled"
			}
		} else {
			status.State = "ready"
		}
		e.status[snapshotID] = status
		e.mu.Unlock()
		e.work.Done()
	}()
	chunks := []Chunk{}
	budget := e.config.InputLimit() - max(len(e.config.QueryPrefix), len(e.config.DocumentPrefix))
	for _, unit := range sourceUnits(r) {
		if err := ctx.Err(); err != nil {
			return err
		}
		key, err := unitKey(unit, budget)
		if err != nil {
			return err
		}
		e.mu.RLock()
		cached, ok := e.unitCache[key]
		e.mu.RUnlock()
		if !ok {
			cached, err = unitChunks(ctx, unit, budget)
			if err != nil {
				return err
			}
			if !unit.Document {
				status.Parsed++
			}
			e.mu.Lock()
			e.unitCache[key] = cached
			e.mu.Unlock()
		}
		chunks = append(chunks, cached...)
	}
	status.Total = len(chunks)
	vectors := make([][]float32, len(chunks))
	pending := []int{}
	pendingKeys := map[string]int{}
	for i, chunk := range chunks {
		e.mu.RLock()
		vector, found := e.vectors[chunk.InputSHA256]
		e.mu.RUnlock()
		if !found {
			var err error
			vector, found, err = e.readVector(chunk.InputSHA256)
			if err != nil {
				return err
			}
		}
		if found {
			vectors[i] = vector
			status.Reused++
			status.Completed++
			e.mu.Lock()
			e.vectors[chunk.InputSHA256] = vector
			e.mu.Unlock()
		} else if _, duplicate := pendingKeys[chunk.InputSHA256]; !duplicate {
			pendingKeys[chunk.InputSHA256] = i
			pending = append(pending, i)
		}
	}
	for len(pending) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		inputs := []string{}
		bytes := 0
		end := 0
		for end < len(pending) && end < 16 {
			input := e.config.DocumentPrefix + chunks[pending[end]].Text
			if bytes+len(input) > e.config.BatchLimit() {
				break
			}
			inputs = append(inputs, input)
			bytes += len(input)
			end++
		}
		if end == 0 {
			return fmt.Errorf("configured batch cannot fit a retrieval chunk")
		}
		status.Requests++
		status.InputBytes += bytes
		e.mu.Lock()
		e.status[snapshotID] = status
		e.mu.Unlock()
		encoded, err := e.encode(ctx, inputs)
		if err != nil {
			return err
		}
		for i, vector := range encoded {
			inputHash := chunks[pending[i]].InputSHA256
			if err := e.writeVector(inputHash, vector); err != nil {
				return err
			}
			e.mu.Lock()
			e.vectors[inputHash] = vector
			e.mu.Unlock()
		}
		status.Completed += end
		pending = pending[end:]
	}
	for i, chunk := range chunks {
		if vectors[i] == nil {
			e.mu.RLock()
			vectors[i] = e.vectors[chunk.InputSHA256]
			e.mu.RUnlock()
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := e.saveManifest(snapshotID, chunks); err != nil {
		return err
	}
	ready := &index{chunks: chunks, vectors: vectors, signature: signature, entities: map[string][]int{}, neighbors: map[string][]neighbor{}}
	for i, chunk := range chunks {
		ready.entities[chunk.EntityID] = append(ready.entities[chunk.EntityID], i)
	}
	attachNeighbors(ready, r)
	e.mu.Lock()
	e.indexes[snapshotID] = ready
	status.Completed = len(chunks)
	e.mu.Unlock()
	return nil
}

func attachNeighbors(ready *index, r *review.Report) {
	add := func(from, to, reason string) {
		if from == to || len(ready.entities[from]) == 0 || len(ready.entities[to]) == 0 {
			return
		}
		ready.neighbors[from] = append(ready.neighbors[from], neighbor{to, reason})
		ready.neighbors[to] = append(ready.neighbors[to], neighbor{from, reason})
	}
	for _, call := range r.Calls {
		add(call.From, call.To, "retained call neighbor")
	}
	for _, reference := range r.References {
		add(reference.From, reference.To, "retained reference neighbor")
	}
	names := map[string][]string{}
	for entity, positions := range ready.entities {
		chunk := ready.chunks[positions[0]]
		key := chunk.Package + "::" + chunk.Name
		names[key] = append(names[key], entity)
	}
	for entity, positions := range ready.entities {
		chunk := ready.chunks[positions[0]]
		for _, name := range chunk.CallNames {
			matches := names[chunk.Package+"::"+name]
			if len(matches) == 1 {
				add(entity, matches[0], "same-package call-name candidate (not resolved)")
			}
		}
	}
	for id := range ready.neighbors {
		sort.Slice(ready.neighbors[id], func(i, j int) bool { return ready.neighbors[id][i].id < ready.neighbors[id][j].id })
	}
}

func (e *Engine) Search(ctx context.Context, snapshotID, text, entityID string, offset, limit int) (Result, error) {
	e.mu.RLock()
	ready, closed := e.indexes[snapshotID], e.closed
	e.mu.RUnlock()
	if closed || ready == nil {
		return Result{}, fmt.Errorf("retrieval index is closed or not ready for snapshot")
	}
	limitations := []string{}
	if entityID != "" {
		if text != "" {
			return Result{}, fmt.Errorf("use query text or retained entityID, not both")
		}
		indices := ready.entities[entityID]
		if len(indices) == 0 {
			return Result{}, fmt.Errorf("entity is not in the indexed retained scope")
		}
		text = ready.chunks[indices[0]].Text
		if len(indices) > 1 {
			limitations = append(limitations, "Code query uses the first bounded chunk of a larger declaration.")
		}
	}
	text = strings.TrimSpace(text)
	input := e.config.QueryPrefix + text
	if text == "" || len(input) > e.config.InputLimit() {
		return Result{}, fmt.Errorf("query is empty or exceeds configured input byte budget")
	}
	e.mu.RLock()
	queryVector, cached := e.queries[input]
	e.mu.RUnlock()
	// Identical query/document inputs already have a vector in this immutable
	// index. Different role prefixes still require their own encoding.
	if !cached && entityID != "" && e.config.QueryPrefix == e.config.DocumentPrefix {
		position := ready.entities[entityID][0]
		if text == ready.chunks[position].Text {
			queryVector, cached = ready.vectors[position], true
		}
	}
	if !cached {
		vectors, err := e.encode(ctx, []string{input})
		if err != nil {
			return Result{}, err
		}
		queryVector = vectors[0]
		e.mu.Lock()
		if len(e.queries) >= 128 {
			clear(e.queries)
		}
		e.queries[input] = queryVector
		e.mu.Unlock()
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	result, err := Rank(ready.chunks, ready.vectors, text, queryVector, offset, limit)
	if err != nil {
		return result, err
	}
	result.SnapshotID = snapshotID
	result.Limitations = append(result.Limitations, e.profile.Limitations...)
	result.Limitations = append(result.Limitations, limitations...)
	seen := map[string]bool{}
	queue := []string{}
	for _, hit := range result.Hits {
		seen[hit.Chunk.EntityID] = true
		queue = append(queue, hit.Chunk.EntityID)
	}
	added := 0
	for depth := 0; depth < 2; depth++ {
		next := []string{}
		for _, id := range queue {
			for _, n := range ready.neighbors[id] {
				if seen[n.id] {
					continue
				}
				if added == 20 {
					result.Limitations = append(result.Limitations, "Structural expansion truncated at 20 additional candidates.")
					return result, nil
				}
				seen[n.id] = true
				added++
				next = append(next, n.id)
				result.Hits = append(result.Hits, Hit{Chunk: ready.chunks[ready.entities[n.id][0]], Reasons: []string{n.reason}})
			}
		}
		queue = next
	}
	return result, nil
}

func (e *Engine) Cancel(snapshotID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.buildID != snapshotID || e.cancel == nil {
		return fmt.Errorf("no indexing job running for snapshot")
	}
	e.cancel()
	return nil
}

func (e *Engine) Close(ctx context.Context) error {
	e.mu.Lock()
	e.closed = true
	e.stop()
	if e.cancel != nil {
		e.cancel()
	}
	e.mu.Unlock()
	done := make(chan struct{})
	go func() { e.work.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
