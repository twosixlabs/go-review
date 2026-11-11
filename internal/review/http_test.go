package review

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"testing"
)

func TestHTTPRegistrationsPreservePatternsHandlersAndUnknowns(t *testing.T) {
	const source = `package fixture
import web "net/http"
const prefix = "/api/items"
func handle(web.ResponseWriter, *web.Request) {}
type handler struct{}
func (handler) ServeHTTP(web.ResponseWriter, *web.Request) {}
type unrelated struct{}
func (unrelated) HandleFunc(string, func(web.ResponseWriter, *web.Request)) {}
func wrap(h web.HandlerFunc) web.HandlerFunc { return h }
func register(mux *web.ServeMux, pattern string, fn web.HandlerFunc, h web.Handler) {
	mux.HandleFunc("GET " + prefix + "/{id}", handle)
	web.HandleFunc("/all", func(web.ResponseWriter, *web.Request) {})
	mux.Handle("POST /api/items", web.HandlerFunc(handle))
	mux.Handle("/object", handler{})
	mux.HandleFunc(pattern, fn)
	mux.HandleFunc("/wrapped", wrap(handle))
	mux.Handle("/interface", h)
	(*web.ServeMux).HandleFunc(mux, "DELETE /api/items/{id}", handle)
	unrelated{}.HandleFunc("/not-an-endpoint", handle)
	var registerFunc = mux.HandleFunc
	registerFunc("/indirect", handle)
}`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "http.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}

	info := &types.Info{Defs: map[*ast.Ident]types.Object{}, Uses: map[*ast.Ident]types.Object{}, Types: map[ast.Expr]types.TypeAndValue{}, Selections: map[*ast.SelectorExpr]*types.Selection{}}
	pkg, err := (&types.Config{Importer: importer.Default()}).Check("fixture", fset, []*ast.File{file}, info)
	if err != nil {
		t.Fatal(err)
	}
	r := &Report{Files: []File{{ID: "source", Source: source}}}
	a := &analyzer{report: r, fset: fset, fileIDs: map[string]string{"http.go": "source"}, objects: map[types.Object]string{}, literals: map[*ast.FuncLit]string{}}
	p := &parsedPackage{meta: listedPackage{ImportPath: "fixture"}, ast: []*ast.File{file}, info: info, pkg: pkg}
	a.collectSymbols(p)
	a.collectEdges(p)
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		HTTPEndpoints []struct {
			ID, Pattern, PatternExpression, Handler, HandlerID, Reason, RegistrationID string
			PatternKnown                                                               bool
			Location                                                                   Location
		}
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire.HTTPEndpoints) != 8 {
		t.Fatalf("got %d registrations, want 8: %s", len(wire.HTTPEndpoints), data)
	}
	for i, endpoint := range wire.HTTPEndpoints {
		if endpoint.ID == "" || endpoint.RegistrationID != symbol(t, r, "register").ID || endpoint.Location.FileID != "source" || endpoint.Handler == "" {
			t.Errorf("registration %d lost evidence: %+v", i, endpoint)
		}
		if i == 4 {
			if endpoint.PatternKnown || endpoint.PatternExpression != "pattern" || endpoint.HandlerID != "" || endpoint.Reason == "" {
				t.Errorf("dynamic registration misrepresented: %+v", endpoint)
			}
		} else if !endpoint.PatternKnown {
			t.Errorf("constant pattern lost: %+v", endpoint)
		}
		if i == 5 || i == 6 {
			if endpoint.HandlerID != "" || endpoint.Reason == "" {
				t.Errorf("wrapper/interface target invented: %+v", endpoint)
			}
		}
		if i < 4 || i == 7 {
			if endpoint.HandlerID == "" || endpoint.Reason != "" {
				t.Errorf("local handler unresolved: %+v", endpoint)
			}
		}
	}
	if wire.HTTPEndpoints[0].Pattern != "GET /api/items/{id}" {
		t.Fatalf("constant expression not evaluated: %+v", wire.HTTPEndpoints[0])
	}
}

func TestHTTPAnalysisLinksImportedHandlersWithoutRunningTarget(t *testing.T) {
	dir := workspace(t)
	files := map[string]string{
		"go.mod": "module example.test/httpfixture\n\ngo 1.24\n",
		"main.go": `package main
import (
	"net/http"
	"example.test/httpfixture/handlers"
)
func main() {
	http.HandleFunc("GET /items", handlers.Items)
	http.HandleFunc("GET /items", handlers.Items)
}
`,
		"handlers/items.go": `package handlers
import "net/http"
func Items(http.ResponseWriter, *http.Request) {}
`,
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	first, err := Analyze(context.Background(), Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Analyze(context.Background(), Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.HTTPEndpoints) != 2 || len(second.HTTPEndpoints) != 2 {
		t.Fatalf("duplicate registration sites were lost: %+v", first.HTTPEndpoints)
	}
	handler := symbol(t, first, "Items")
	for i, endpoint := range first.HTTPEndpoints {
		if endpoint.HandlerID != handler.ID || endpoint.Pattern != "GET /items" || endpoint.ID != second.HTTPEndpoints[i].ID {
			t.Fatalf("imported handler or stable evidence lost: %+v", endpoint)
		}
	}
	if first.HTTPEndpoints[0].ID == first.HTTPEndpoints[1].ID {
		t.Fatal("duplicate patterns must retain separate source identities")
	}
	for _, command := range first.Commands {
		if len(command.Args) > 0 && command.Args[0] == "test" {
			t.Fatal("endpoint discovery ran tests")
		}
	}
}
