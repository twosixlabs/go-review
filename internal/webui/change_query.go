package webui

import (
	"encoding/json"
	"fmt"

	"github.com/dop251/goja"
	"github.com/russellbrasser/go-review/internal/changes"
)

// NewChangeQuery evaluates the same pure inventory/progress projection as the UI.
// Side reports are deliberately absent; callers hydrate one requested module.
func NewChangeQuery(c *changes.Comparison, state changes.State) (*ReviewQuery, error) {
	vm := goja.New()
	data, err := assets.ReadFile("change-tools.js")
	if err != nil {
		return nil, err
	}
	if _, err := vm.RunScript("change-tools.js", string(data)); err != nil {
		return nil, err
	}
	comparisonJSON, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	stateJSON, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}
	if err := vm.Set("comparisonJSON", string(comparisonJSON)); err != nil {
		return nil, err
	}
	if err := vm.Set("stateJSON", string(stateJSON)); err != nil {
		return nil, err
	}
	value, err := vm.RunString(`(function(){
		const comparison=JSON.parse(comparisonJSON),state=JSON.parse(stateJSON);
		delete globalThis.comparisonJSON;delete globalThis.stateJSON;
		return function(operation,args){return JSON.stringify(GoReviewChanges.query(comparison,state,operation,JSON.parse(args)));};
	})()`)
	if err != nil {
		return nil, err
	}
	call, ok := goja.AssertFunction(value)
	if !ok {
		return nil, fmt.Errorf("comparison query is not callable")
	}
	return &ReviewQuery{vm: vm, call: call}, nil
}
