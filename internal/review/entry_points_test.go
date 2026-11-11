package review

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestEntryPointsClassifyCLIServiceAndStandaloneLibrary(t *testing.T) {
	for _, test := range []struct {
		name, source, kind string
		want               int
	}{
		{"cli", "package main\nfunc main() { panic(\"must not execute\") }\nfunc init() {}\nfunc PublicHelper() {}", "cli", 1},
		{"missing-main", "package main\nfunc init() {}\nfunc PublicHelper() {}", "cli", 0},
		{"service", "package main\nimport \"net/http\"\nfunc main(){ http.HandleFunc(\"GET /\", Handle) }\nfunc Handle(http.ResponseWriter,*http.Request){}", "service", 2},
		{"wasm-http", "package main\nimport \"net/http\"\nfunc main(){ http.HandleFunc(\"GET /\", Handle) }\nfunc Handle(http.ResponseWriter,*http.Request){}", "wasm", 2},
		{"library", "package fixture\nfunc Public(){}\nfunc private(){}\ntype API struct{}\nfunc(API) Invoke(){}\nfunc(API) hidden(){}\ntype hidden struct{}\nfunc(hidden) Visible(){}", "library", 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := workspace(t)
			if test.kind == "wasm" {
				if err := os.MkdirAll(filepath.Join(dir, "wit"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "wit", "world.wit"), []byte("package example:entry;\nworld app { export invoke: func(); }\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			for name, content := range map[string]string{
				"go.mod":                  "module example.test/entry\n\ngo 1.24\n",
				"entry.go":                test.source,
				"internal/private/api.go": "package private\nfunc Exported(){}",
				"nested/api.go":           "package nested\nfunc Exported(){}",
				"entry_test.go":           "package " + map[bool]string{true: "main", false: "fixture"}[test.kind != "library"] + "\nfunc ExampleNotAnEntry(){}",
			} {
				path := filepath.Join(dir, name)
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			r, err := Analyze(context.Background(), Options{Dir: dir})
			if err != nil {
				t.Fatal(err)
			}
			if r.EntryPoints == nil || r.EntryPoints.Kind != test.kind {
				t.Fatalf("entry classification: %+v", r.EntryPoints)
			}
			want := test.want
			if test.kind == "library" {
				want++ // Public API in the module's public subpackage.
			}
			if len(r.EntryPoints.Entries) != want {
				t.Fatalf("entries=%+v want=%d", r.EntryPoints.Entries, want)
			}
			for _, e := range r.EntryPoints.Entries {
				if test.kind == "wasm" && e.Kind == "wasm" {
					continue
				}
				if e.ID == "" || e.SymbolID == "" || e.Resolution != "resolved" {
					t.Fatalf("entry missing implementation/provenance: %+v", e)
				}
				if e.Kind == "library" && e.Name == "Visible" {
					t.Fatal("method on unexported receiver exposed as public API")
				}
			}
			if test.kind == "cli" && len(r.EntryPoints.Initializers) != 1 {
				t.Fatal("init must remain supporting initialization, not an external entry")
			}
		})
	}
}
