package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/dop251/goja"
	"github.com/russellbrasser/go-review/internal/review"
)

// ReviewQuery runs the browser's pure evidence projections on an immutable
// snapshot. No filesystem, network, process or Go callbacks are exposed to JS.
type ReviewQuery struct {
	mu   sync.Mutex
	vm   *goja.Runtime
	call goja.Callable
}

func NewReviewQuery(report *review.Report, repository ...json.RawMessage) (*ReviewQuery, error) {
	vm := goja.New()
	for _, name := range []string{"graph-tools.js", "assessment-tools.js", "application-tools.js",
		"endpoint-flow-tools.js", "visual-review-tools.js", "system-flow-tools.js",
		"logical-tools.js", "documentation-tools.js", "review-tools.js", "dataflow-tools.js", "review-query-tools.js"} {
		data, err := assets.ReadFile(name)
		if err != nil {
			return nil, fmt.Errorf("load shared projection %s: %w", name, err)
		}
		if _, err := vm.RunScript(name, string(data)); err != nil {
			return nil, fmt.Errorf("initialize shared projection %s: %w", name, err)
		}
	}
	data, err := json.Marshal(report)
	if err != nil {
		return nil, fmt.Errorf("encode query snapshot: %w", err)
	}
	if err := vm.Set("snapshotJSON", string(data)); err != nil {
		return nil, err
	}
	run := "null"
	if len(repository) > 0 {
		run = string(repository[0])
	}
	if err := vm.Set("repositoryJSON", run); err != nil {
		return nil, err
	}
	value, err := vm.RunString(`(function() {
		const report=JSON.parse(snapshotJSON);
		const repository=JSON.parse(repositoryJSON);
		delete globalThis.snapshotJSON;
		delete globalThis.repositoryJSON;
		return function(operation, args) {
			return JSON.stringify(GoReviewQuery.query(report,operation,JSON.parse(args),repository));
		};
	})()`)
	if err != nil {
		return nil, fmt.Errorf("initialize snapshot: %w", err)
	}
	call, ok := goja.AssertFunction(value)
	if !ok {
		return nil, fmt.Errorf("shared review query is not callable")
	}
	return &ReviewQuery{vm: vm, call: call}, nil
}

func (q *ReviewQuery) Query(ctx context.Context, operation string, args json.RawMessage) (json.RawMessage, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	// Wait for an in-flight cancellation callback before reusing this runtime;
	// otherwise a previous request could interrupt the next one.
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		q.vm.Interrupt(ctx.Err())
		close(done)
	})
	defer func() {
		if !stop() {
			<-done
		}
		q.vm.ClearInterrupt()
	}()
	value, err := q.call(goja.Undefined(), q.vm.ToValue(operation), q.vm.ToValue(string(args)))
	if err != nil {
		return nil, fmt.Errorf("review %s: %w", operation, err)
	}
	return json.RawMessage(value.String()), nil
}
