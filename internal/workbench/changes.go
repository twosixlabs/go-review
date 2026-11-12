package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/russellbrasser/go-review/internal/changes"
	"github.com/russellbrasser/go-review/internal/review"
	"github.com/russellbrasser/go-review/internal/webui"
)

type changeJob struct {
	ID       string
	cancel   context.CancelFunc
	done     chan struct{}
	progress changes.Progress
	err      string
}

type changeQueryInput struct {
	ComparisonID     string   `json:"comparisonID,omitempty" jsonschema:"Pinned comparison identity from overview; required after discovery"`
	Operation        string   `json:"operation,omitempty" jsonschema:"overview (default), status, files, items, records, flows, record, witnesses, correspondences, progress, source, patch, flow, side"`
	ModuleID         string   `json:"moduleID,omitempty"`
	Side             string   `json:"side,omitempty" jsonschema:"before or after"`
	SnapshotID       string   `json:"snapshotID,omitempty" jsonschema:"Exact selected side/module snapshot for flow or side operations"`
	RecordID         string   `json:"recordID,omitempty"`
	AssociationIndex *int     `json:"associationIndex,omitempty" jsonschema:"Selected witness path index; section steps pages its complete source evidence"`
	FileID           string   `json:"fileID,omitempty" jsonschema:"Comparison inventory file ID for source/patch"`
	FlowID           string   `json:"flowID,omitempty"`
	Perspective      string   `json:"perspective,omitempty" jsonschema:"all, affected, shared or other"`
	Status           string   `json:"status,omitempty" jsonschema:"all, remaining, not-reviewed, reviewed, follow-up"`
	Query            string   `json:"query,omitempty"`
	View             string   `json:"view,omitempty" jsonschema:"For side operations: evidence, logical, dataflow, source, document, packages, inspect, architecture or search"`
	Package          string   `json:"package,omitempty"`
	Level            string   `json:"level,omitempty"`
	ComponentID      string   `json:"componentID,omitempty"`
	OperationID      string   `json:"operationID,omitempty"`
	Group            string   `json:"group,omitempty"`
	FunctionView     string   `json:"functionView,omitempty"`
	From             string   `json:"from,omitempty"`
	To               string   `json:"to,omitempty"`
	Kind             string   `json:"kind,omitempty"`
	SymbolID         string   `json:"symbolID,omitempty"`
	SourceFileID     string   `json:"sourceFileID,omitempty"`
	DocumentID       string   `json:"documentID,omitempty"`
	StartLine        *int     `json:"startLine,omitempty"`
	EndLine          *int     `json:"endLine,omitempty"`
	ValueID          string   `json:"valueID,omitempty"`
	Direction        string   `json:"direction,omitempty"`
	Depth            *int     `json:"depth,omitempty"`
	FocusSymbolID    string   `json:"focusSymbolID,omitempty"`
	FocusPath        []string `json:"focusPath,omitempty"`
	ExpandedIDs      []string `json:"expandedIDs,omitempty"`
	NodeID           string   `json:"nodeID,omitempty"`
	pageInput
}

func (s *Server) currentComparison(id string) (*changes.Comparison, error) {
	s.mu.RLock()
	c := s.comparison
	s.mu.RUnlock()
	if c == nil {
		return nil, fmt.Errorf("no Change Review has been collected")
	}
	if id != "" && id != c.ID {
		return nil, fmt.Errorf("stale comparison; reload Change Review")
	}
	return c, nil
}

func (s *Server) queryChanges(ctx context.Context, input changeQueryInput) (result json.RawMessage, err error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	defer func() {
		if err == nil && len(result) > 32<<10 {
			result = nil
			err = fmt.Errorf("comparison result exceeds 32 KiB; select a smaller page")
		}
	}()
	s.mu.RLock()
	defer s.mu.RUnlock()
	c := s.comparison
	if input.Operation == "status" {
		if input.ComparisonID != "" && (c == nil || input.ComparisonID != c.ID) {
			return nil, fmt.Errorf("stale comparison")
		}
		return json.Marshal(s.changeStatusLocked())
	}
	if c == nil {
		if input.ComparisonID == "" && (input.Operation == "" || input.Operation == "overview") {
			return json.RawMessage(`{"status":"not-collected","reason":"Open Change Review to compare committed revisions."}`), nil
		}
		return nil, fmt.Errorf("no Change Review has been collected")
	}
	if input.ComparisonID != "" && input.ComparisonID != c.ID {
		return nil, fmt.Errorf("stale comparison; reload Change Review")
	}
	if input.Operation == "" {
		input.Operation = "overview"
	}
	if input.ComparisonID == "" && input.Operation != "overview" {
		return nil, fmt.Errorf("comparisonID is required after discovery")
	}
	state, err := c.State()
	if err != nil {
		return nil, err
	}
	query, err := webui.NewChangeQuery(c, state)
	if err != nil {
		return nil, err
	}
	args, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	if _, err := query.Query(ctx, "overview", args); err != nil {
		return nil, err
	}
	if input.Operation == "source" || input.Operation == "patch" {
		var file *changes.FileChange
		for i := range c.Files {
			if c.Files[i].ID == input.FileID {
				file = &c.Files[i]
				break
			}
		}
		if file == nil {
			return nil, fmt.Errorf("select retained comparison fileID")
		}
		var data []byte
		if input.Operation == "patch" {
			data, err = os.ReadFile(filepath.Join(c.Directory, "patches", file.ID))
		} else {
			if input.Side != "before" && input.Side != "after" {
				return nil, fmt.Errorf("source requires explicit before/after side")
			}
			object := file.Before
			if input.Side == "after" {
				object = file.After
			}
			if object.Path == "" {
				return nil, fmt.Errorf("file is absent on %s", input.Side)
			}
			if object.Mode == "160000" {
				return nil, fmt.Errorf("submodule object %s (no source blob)", object.OID)
			}
			data, err = os.ReadFile(filepath.Join(c.Directory, "blobs", object.OID))
			if err != nil {
				return nil, fmt.Errorf("retained content unavailable (possibly over 16 MiB): %w", err)
			}
			if !utf8.Valid(data) || strings.ContainsRune(string(data), 0) {
				return nil, fmt.Errorf("binary/non-UTF-8 content; inspect retained object identities")
			}
		}
		if err != nil {
			return nil, err
		}
		firstLine := 1
		if input.Operation == "source" && (input.StartLine != nil || input.EndLine != nil) {
			lines := strings.Split(string(data), "\n")
			endLine := len(lines)
			if input.StartLine != nil {
				firstLine = *input.StartLine
			}
			if input.EndLine != nil {
				endLine = *input.EndLine
			}
			if firstLine < 1 || endLine < firstLine || firstLine > len(lines) {
				return nil, fmt.Errorf("invalid retained source line range")
			}
			endLine = min(endLine, len(lines))
			data = []byte(strings.Join(lines[firstLine-1:endLine], "\n"))
		}
		chars := []rune(string(data))
		offset, limit := 0, 1500
		if input.TextOffset != nil {
			offset = *input.TextOffset
		}
		if input.TextLimit != nil {
			limit = *input.TextLimit
		}
		if offset < 0 || limit < 1 || limit > 6000 {
			return nil, fmt.Errorf("invalid source text page")
		}
		start := min(offset, len(chars))
		end := min(start+limit, len(chars))
		// JSON escaping can exceed UTF-8 bytes; bound the encoded text, not chars.
		for end > start {
			encoded, _ := json.Marshal(string(chars[start:end]))
			if len(encoded) <= 6144 {
				break
			}
			end--
		}
		var next *int
		if end < len(chars) {
			next = &end
		}
		return json.Marshal(map[string]any{"comparisonID": c.ID, "side": input.Side, "fileID": file.ID, "before": file.Before, "after": file.After,
			"line": firstLine + strings.Count(string(chars[:start]), "\n"), "text": string(chars[start:end]), "textPage": map[string]any{"offset": offset, "total": len(chars), "nextOffset": next}})
	}
	if input.Operation == "flow" || input.Operation == "side" {
		if input.ModuleID == "" || input.Side == "" || input.SnapshotID == "" {
			return nil, fmt.Errorf("moduleID, side and side snapshotID are required")
		}
		var module *changes.Module
		for i := range c.Modules {
			if c.Modules[i].ID == input.ModuleID {
				module = &c.Modules[i]
				break
			}
		}
		if module == nil {
			return nil, fmt.Errorf("unknown comparison module")
		}
		side := module.Before
		if input.Side == "after" {
			side = module.After
		}
		if side.SnapshotID != input.SnapshotID {
			return nil, fmt.Errorf("stale or foreign revision-side snapshot")
		}
		report, err := c.Report(input.ModuleID, input.Side)
		if err != nil {
			return nil, err
		}
		sideQuery, err := webui.NewReviewQuery(report)
		if err != nil {
			return nil, err
		}
		var values map[string]any
		if err := json.Unmarshal(args, &values); err != nil {
			return nil, err
		}
		operation := input.View
		if input.Operation == "flow" || input.View == "inspect" {
			var entry *review.EntryPoint
			for _, flow := range c.Flows {
				if flow.ID == input.FlowID {
					entry = flow.Before
					if input.Side == "after" {
						entry = flow.After
					}
					break
				}
			}
			if entry == nil {
				return nil, fmt.Errorf("flow has no counterpart on selected side")
			}
			values["entryID"] = entry.ID
			operation = "flow"
			if input.View == "inspect" {
				operation = "inspect"
			}
		} else {
			switch operation {
			case "evidence", "logical", "dataflow", "source", "document", "packages", "architecture", "search":
			default:
				return nil, fmt.Errorf("unsupported side evidence view")
			}
			if operation == "source" {
				values["fileID"] = input.SourceFileID
			}
			if operation == "document" {
				values["id"] = input.DocumentID
			}
		}
		encoded, err := json.Marshal(values)
		if err != nil {
			return nil, err
		}
		data, err := sideQuery.Query(ctx, operation, encoded)
		if err != nil {
			return nil, err
		}
		if input.Operation == "flow" {
			var scene map[string]any
			if err := json.Unmarshal(data, &scene); err != nil {
				return nil, err
			}
			key := "nodes"
			if input.Section == "nodes" {
				key = "items"
			}
			if nodes, ok := scene[key]; ok && input.Section != "edges" {
				annotatedArgs, _ := json.Marshal(map[string]any{"comparisonID": c.ID, "flowID": input.FlowID, "side": input.Side, "nodes": nodes})
				annotations, err := query.Query(ctx, "annotations", annotatedArgs)
				if err != nil {
					return nil, err
				}
				var value struct {
					Nodes json.RawMessage `json:"nodes"`
				}
				if err := json.Unmarshal(annotations, &value); err != nil {
					return nil, err
				}
				scene[key] = value.Nodes
				data, err = json.Marshal(scene)
				if err != nil {
					return nil, err
				}
			}
		}
		return json.Marshal(map[string]any{"comparisonID": c.ID, "moduleID": module.ID, "side": input.Side, "snapshotID": side.SnapshotID, "evidence": data})
	}
	return query.Query(ctx, input.Operation, args)
}

func (s *Server) changesQuery(w http.ResponseWriter, r *http.Request) {
	var input changeQueryInput
	if !decodeRequest(w, r, &input) {
		return
	}
	data, err := s.queryChanges(r.Context(), input)
	if err != nil {
		failure(w, 409, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}

func (s *Server) changesState(w http.ResponseWriter, r *http.Request) {
	var request struct {
		changes.StateAction
		Confirmed bool `json:"confirmed"`
	}
	if !decodeRequest(w, r, &request) {
		return
	}
	if !request.Confirmed {
		failure(w, 400, "explicit local review-state confirmation is required")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.comparison
	if c == nil || request.ComparisonID != c.ID {
		failure(w, 409, "stale comparison; reload Change Review")
		return
	}
	state, err := c.UpdateState(request.StateAction)
	if err != nil {
		failure(w, 409, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(state)
}

func (s *Server) changesResolve(w http.ResponseWriter, r *http.Request) {
	var request changes.Request
	if !decodeRequest(w, r, &request) {
		return
	}
	s.mu.RLock()
	busy := s.changeJob != nil || s.changeClosed
	s.mu.RUnlock()
	if busy {
		failure(w, 409, "cannot replace revision inputs while comparison collection is running")
		return
	}
	var resolved changes.Resolved
	var err error
	if request.Remote != "" {
		var storage string
		storage, err = changes.Storage()
		if err == nil {
			resolved, err = changes.ResolveRemote(r.Context(), request, storage)
		}
	} else {
		resolved, err = changes.Resolve(r.Context(), request)
	}
	if err != nil {
		failure(w, 422, err.Error())
		return
	}
	scope, err := review.LoadReviewScope(resolved.Repository)
	if err != nil {
		err = errors.Join(err, changes.ReleaseInputs(resolved))
		failure(w, 422, err.Error())
		return
	}
	s.mu.Lock()
	if s.changeJob != nil || s.changeClosed {
		s.mu.Unlock()
		err := errors.Join(fmt.Errorf("comparison collection started while resolving"), changes.ReleaseInputs(resolved))
		failure(w, 409, err.Error())
		return
	}
	oldPreview := s.changePreview
	if oldPreview != nil {
		if err := changes.ReleaseInputs(*oldPreview); err != nil {
			s.mu.Unlock()
			err = errors.Join(fmt.Errorf("could not release superseded input: %w", err), changes.ReleaseInputs(resolved))
			failure(w, 500, err.Error())
			return
		}
	}
	s.changePreview = &resolved
	s.changePreviewID = fmt.Sprintf("%d", time.Now().UnixNano())
	previewID := s.changePreviewID
	s.changeScope = scope
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		changes.Resolved
		PreviewID string             `json:"previewID"`
		Scope     review.ReviewScope `json:"scope"`
	}{resolved, previewID, scope})
}

func (s *Server) changesStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		failure(w, 405, "GET required")
		return
	}
	s.mu.RLock()
	status := s.changeStatusLocked()
	s.mu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(status)
}

func (s *Server) changesCollect(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Confirmed bool   `json:"confirmed"`
		Before    string `json:"before"`
		After     string `json:"after"`
		ReplaceID string `json:"replaceID,omitempty"`
		PreviewID string `json:"previewID"`
	}
	if !decodeRequest(w, r, &request) {
		return
	}
	if !request.Confirmed {
		failure(w, 400, "confirm pinned revisions and static Go tooling/dependency activity; target tests/scripts will not execute")
		return
	}
	s.mu.Lock()
	preview := s.changePreview
	if preview == nil || preview.Before.Commit != request.Before || preview.After.Commit != request.After || request.PreviewID != s.changePreviewID {
		s.mu.Unlock()
		failure(w, 409, "resolve and confirm the exact immutable revision pair first")
		return
	}
	if s.changeJob != nil || s.changeClosed {
		s.mu.Unlock()
		failure(w, 409, "comparison collection already running or server stopping")
		return
	}
	if s.comparison != nil && request.ReplaceID != s.comparison.ID {
		s.mu.Unlock()
		failure(w, 409, "refresh requires confirmation of the current comparison and evidence-bound progress reset")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	job := &changeJob{ID: s.changePreviewID, cancel: cancel, done: make(chan struct{}), progress: changes.Progress{Phase: "Starting"}}
	s.changeJob = job
	s.changePreview = nil
	s.changeError = ""
	scope := s.changeScope
	s.mu.Unlock()
	go func() {
		defer cancel()
		defer close(job.done)
		storage, err := changes.Storage()
		var c *changes.Comparison
		if err == nil {
			c, err = changes.Collect(ctx, *preview, storage, scope, func(p changes.Progress) { s.mu.Lock(); job.progress = p; s.mu.Unlock() })
		}
		// Inputs stay job-owned until collection and release both finish; a late
		// cleanup result must not overwrite a newer job's lifecycle status.
		if releaseErr := changes.ReleaseInputs(*preview); releaseErr != nil {
			err = errors.Join(err, fmt.Errorf("release comparison input: %w", releaseErr))
		}
		s.mu.Lock()
		if err == nil {
			err = ctx.Err()
		}
		if err == nil {
			err = changes.Publish(storage, c)
		}
		if err != nil {
			if cleanupErr := changes.Discard(storage, c); cleanupErr != nil {
				err = errors.Join(err, cleanupErr)
			}
			s.changeError = err.Error()
		} else {
			previous := s.comparison
			s.comparison = c
			if previous != nil && previous.Directory != c.Directory {
				if cleanupErr := changes.Discard(storage, previous); cleanupErr != nil {
					s.changeError = "comparison published; old artifact cleanup failed: " + cleanupErr.Error()
				}
			}
		}
		s.changeJob = nil
		s.mu.Unlock()
	}()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]bool{"running": true})
}

func (s *Server) changesCancel(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Confirmed bool   `json:"confirmed"`
		JobID     string `json:"jobID"`
	}
	if !decodeRequest(w, r, &request) {
		return
	}
	if !request.Confirmed {
		failure(w, 400, "explicit comparison cancellation required")
		return
	}
	s.mu.Lock()
	job := s.changeJob
	if job != nil && job.ID != request.JobID {
		s.mu.Unlock()
		failure(w, 409, "stale collection job; reload current progress")
		return
	}
	if job != nil {
		job.cancel()
	}
	s.mu.Unlock()
	if job == nil {
		failure(w, 409, "no comparison job is running")
		return
	}
	select {
	case <-job.done:
	case <-r.Context().Done():
		failure(w, 408, r.Context().Err().Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "canceled; previous comparison remains available"})
}

func (s *Server) reopenComparison(repository string) error {
	storage, err := changes.Storage()
	if err != nil {
		return err
	}
	c, err := changes.OpenCurrent(storage, repository)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.comparison = c
	s.mu.Unlock()
	return nil
}

func registerChangesTool(server *mcp.Server, s *Server) {
	mcp.AddTool(server, &mcp.Tool{Name: "review_changes", Description: "Read current Change Review: complete inventory, affected/shared/other changes, source witnesses, paired retained source and explicit local progress. Start operation overview; preserve comparisonID. Side/module snapshot IDs are separate from ordinary Application. Never collects, changes progress, or interacts with provider approvals.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: boolPointer(false)}},
		func(ctx context.Context, _ *mcp.CallToolRequest, input changeQueryInput) (*mcp.CallToolResult, map[string]any, error) {
			data, err := s.queryChanges(ctx, input)
			if err != nil {
				return nil, nil, err
			}
			var value map[string]any
			if err := json.Unmarshal(data, &value); err != nil {
				return nil, nil, err
			}
			return nil, value, nil
		})
}
