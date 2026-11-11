package review

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestSystemFlowScopeAndIncompleteSourceRemainInspectable(t *testing.T) {
	root := t.TempDir()
	writeReviewFixture(t, root, "go.mod", "module example.test/source\n\ngo 1.24\n")
	writeReviewFixture(t, root, "source.go", "package source\nfunc Run() { missingTransport(\"event\") }\n")
	writeReviewFixture(t, root, "excluded.go", "package source\ntype Hidden struct{}\nfunc (*Hidden) Send() {}\n")
	r, err := Analyze(context.Background(), Options{Dir: root, Scope: &ReviewScope{Root: root, ExcludePaths: []string{"excluded.go"}}})
	if err != nil {
		t.Fatal(err)
	}
	unknown := false
	for _, c := range r.SystemFlow.Components {
		if c.Name == "Hidden" {
			t.Error("excluded implementation became an architectural node")
		}
		if strings.Contains(c.Name, "missingTransport") && c.Kind == "contract" {
			unknown = true
		}
	}
	if !unknown {
		t.Error("incomplete typing silently removed a production handoff")
	}
	empty := BuildSystemFlow(&Report{})
	if len(empty.Components) != 0 || len(empty.Exchanges) != 0 || len(empty.Limitations) == 0 {
		t.Fatalf("missing source facts are not explicit: %+v", empty)
	}
}

func TestSystemFlowDeduplicatesRegistrationsAndIndependentProjection(t *testing.T) {
	r := analyzeOperationSourceFixture(t, map[string]string{"source.go": `package source
import "net/http"
func Routes() { http.HandleFunc("/run", Run) }
func Run(w http.ResponseWriter,r *http.Request) {}
`}, nil)
	if len(r.HTTPEndpoints) != 1 {
		t.Fatalf("fixture has no registration: %+v", r.HTTPEndpoints)
	}
	r.HTTPEndpoints = append(r.HTTPEndpoints, r.HTTPEndpoints[0])
	first := BuildSystemFlow(r)
	ingress := 0
	for _, e := range first.Exchanges {
		if e.Kind == "ingress" {
			ingress++
		}
	}
	if ingress != 1 {
		t.Errorf("duplicate registration expanded the overview: %d ingress exchanges", ingress)
	}
	a, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(BuildSystemFlow(r))
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Error("independent projections have unstable identities/order")
	}
}
