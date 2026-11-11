package changes

import (
	"context"
	"strings"
	"testing"

	"github.com/russellbrasser/go-review/internal/review"
)

func TestChangeHTTPRegistrationChangesKeepDistinctSharedHandlerEntries(t *testing.T) {
	root := t.TempDir()
	gitFixture(t, root, "init", "-q")
	fixtureFile(t, root, "go.mod", "module example.test/routes\n\ngo 1.24\n")
	source := "package main\nimport \"net/http\"\nfunc handler(http.ResponseWriter,*http.Request) {}\nfunc main(){\n http.HandleFunc(\"GET /old\",handler)\n http.HandleFunc(\"GET /unchanged\",handler)\n}\n"
	fixtureFile(t, root, "main.go", source)
	gitFixture(t, root, "add", ".")
	gitFixture(t, root, "commit", "-qm", "before")
	before := gitFixture(t, root, "rev-parse", "HEAD")
	fixtureFile(t, root, "main.go", strings.Replace(source, "GET /old", "GET /new", 1))
	gitFixture(t, root, "commit", "-qam", "after")
	r, err := Resolve(context.Background(), Request{Directory: root, Before: before, After: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := Collect(context.Background(), r, t.TempDir(), review.ReviewScope{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var routes []Flow
	for _, flow := range c.Flows {
		if flow.Kind == "http" {
			routes = append(routes, flow)
		}
	}
	if len(routes) != 1 || routes[0].Before == nil || routes[0].After == nil || routes[0].Before.Name != "GET /old" || routes[0].After.Name != "GET /new" {
		t.Fatalf("changed registration not paired by unique remaining source provenance: %+v", routes)
	}
	found := false
	for _, a := range routes[0].Associations {
		for _, step := range a.Witness {
			found = found || step.Kind == "http-registration"
		}
	}
	if !found {
		t.Fatal("registration inclusion has no source witness")
	}
}

func TestChangeWITContractRemainsAssociatedWithoutGoBinding(t *testing.T) {
	root := t.TempDir()
	gitFixture(t, root, "init", "-q")
	fixtureFile(t, root, "go.mod", "module example.test/contract\n\ngo 1.24\n")
	fixtureFile(t, root, "main.go", "package main\nfunc main() {}\n")
	fixtureFile(t, root, "wit/world.wit", "package example:app;\nworld app {\n export run: func(value: string);\n}\n")
	gitFixture(t, root, "add", ".")
	gitFixture(t, root, "commit", "-qm", "before")
	before := gitFixture(t, root, "rev-parse", "HEAD")
	fixtureFile(t, root, "wit/world.wit", "package example:app;\nworld app {\n export run: func(value: list<u8>);\n}\n")
	gitFixture(t, root, "commit", "-qam", "after")
	r, err := Resolve(context.Background(), Request{Directory: root, Before: before, After: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := Collect(context.Background(), r, t.TempDir(), review.ReviewScope{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Flows) != 1 || c.Flows[0].Kind != "wasm" || c.Flows[0].Before == nil || c.Flows[0].After == nil || len(c.Flows[0].Associations) != 2 {
		t.Fatalf("changed WIT contract lost because handler is unresolved: %+v", c.Flows)
	}
	if c.Flows[0].Before.Resolution != "unresolved" || c.Flows[0].Associations[0].Quality != "entry-contract-change" {
		t.Fatalf("contract association invented handler reachability: %+v", c.Flows[0])
	}
}
