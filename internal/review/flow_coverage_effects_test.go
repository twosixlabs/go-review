package review

import (
	"encoding/json"
	"testing"
)

func TestFlowCoveragePreservesStoreWritesAndNestedRequirements(t *testing.T) {
	r := analyzeOperationSourceFixture(t, map[string]string{"source.go": `package source
type Store struct { rows map[string]string }
func (s *Store) Save(ok,overwrite bool) {
	if ok {
		if overwrite {s.rows["id"]="new"}
	}
}
`}, nil)
	var flow struct {
		Alternatives []struct {
			ID, Decision, Label string
			Requires            []string
		}
	}
	data, err := json.Marshal(r.SystemFlow)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &flow); err != nil {
		t.Fatal(err)
	}
	if len(flow.Alternatives) != 4 {
		t.Fatalf("state-changing decisions disappeared: %+v", flow.Alternatives)
	}
	outer := ""
	for _, a := range flow.Alternatives {
		if a.Decision == "ok" && a.Label == "true" {
			outer = a.ID
		}
	}
	for _, a := range flow.Alternatives {
		if a.Decision == "overwrite" {
			// Inspect the published representation rather than an implementation
			// detail: a reviewer must see the enclosing alternative requirement.
			found := false
			for _, required := range a.Requires {
				found = found || required == outer
			}
			if outer == "" || !found {
				t.Errorf("nested alternative has no parent requirement: %+v", a)
			}
		}
	}
}
