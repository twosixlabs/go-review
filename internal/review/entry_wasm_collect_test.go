package review

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func wasmFixture(t *testing.T, files map[string]string) *Report {
	t.Helper()
	root, err := filepath.Abs(filepath.Join(".work", t.Name()))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	for name, text := range files {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return &Report{Root: root, Scope: ReviewScope{Root: root}}
}

func TestWasmEntrySelectedWorldAndVersionedInterface(t *testing.T) {
	r := wasmFixture(t, map[string]string{
		"componentize-go.toml":   "worlds = [\"builder:app/worker@0.1.0\"]\nwit_paths = [\"wit\"]\n",
		"wit/world.wit":          "package builder:app@0.1.0;\nworld worker {\n import wasmcloud:nats/host@0.1.0;\n export wasmcloud:nats/handler@0.1.0;\n}\nworld other { export wrong: func(); }\n",
		"wit/deps/nats/nats.wit": "package wasmcloud:nats@0.1.0;\ninterface host { publish: func(); }\ninterface handler {\n handle-message: async func();\n}\n",
	})
	r.Files = []File{{ID: "f", Path: "wit_exports.go", Source: "package main\n//go:wasmexport [async-lift]wasmcloud:nats/handler@0.1.0#handle-message\nfunc boundary() {}\n//go:wasmexport [callback][async-lift]wasmcloud:nats/handler@0.1.0#handle-message\nfunc callback() {}\nfunc main() {}\n"}}
	r.Files = append(r.Files, File{ID: "hand", Path: "handler.go", Source: "package main\nfunc HandleMessage() {}\n"})
	r.Symbols = []Symbol{
		{ID: "boundary", Name: "boundary", Location: Location{FileID: "f", Line: 3, EndLine: 3}},
		{ID: "handler", Name: "HandleMessage", Location: Location{FileID: "hand", Line: 1}},
		{ID: "main", Name: "main", Location: Location{FileID: "f", Line: 6}},
	}
	r.Calls = []Call{{ID: "c", From: "boundary", To: "handler", Location: Location{FileID: "f", Line: 3}}}
	inv, err := collectWasmEntryPoints(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if inv == nil || inv.Kind != "wasm" || len(inv.Entries) != 1 {
		t.Fatalf("inventory = %#v", inv)
	}
	e := inv.Entries[0]
	if e.Kind != "wasm" || e.Function != "handle-message" || e.Interface != "wasmcloud:nats/handler@0.1.0" || e.SymbolID != "handler" || e.BoundarySymbolID != "boundary" {
		t.Fatalf("entry = %#v", e)
	}
	if len(inv.Sources) != 3 {
		t.Fatalf("sources = %#v", inv.Sources)
	}
	kinds := map[string]bool{}
	for _, ev := range e.Evidence {
		kinds[ev.Kind] = true
		if ev.SourceID != "" && ev.Line < 1 {
			t.Fatalf("unlocated evidence: %#v", ev)
		}
	}
	if !kinds["wit-export"] || !kinds["configuration"] || !kinds["wasmexport"] || !kinds["wrapper-call"] {
		t.Fatalf("evidence = %#v", e.Evidence)
	}
	for _, src := range inv.Sources {
		if src.ID == "" || src.SHA256 != digest([]byte(src.Text)) || src.Text == "" {
			t.Fatalf("source not retained: %#v", src)
		}
	}
	again, err := collectWasmEntryPoints(context.Background(), r)
	if err != nil || again.Entries[0].ID != e.ID || again.Sources[0].ID != inv.Sources[0].ID {
		t.Fatal("unstable identity")
	}
}

func TestWasmEntryMissingBindingsAndImports(t *testing.T) {
	r := wasmFixture(t, map[string]string{"wit/world.wit": "package test:app;\nworld app {\n import host: interface { read: func(); }\n export run: func();\n export missing:vendor/api@1.0.0;\n}\n"})
	inv, err := collectWasmEntryPoints(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if inv == nil || len(inv.Entries) != 2 {
		t.Fatalf("inventory = %#v", inv)
	}
	for _, e := range inv.Entries {
		if e.Resolution != "unresolved" || e.SymbolID != "" || e.Function == "read" || e.Reason == "" {
			t.Fatalf("entry = %#v", e)
		}
	}
}

func TestWasmEntryNoMainFallbackAndSDK(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		wasm         bool
		count        int
	}{
		{"ordinary", "package main\nfunc main() {}\n", false, 0},
		{"sdk", "package main\n//go:wasmexport wasi:http/incoming-handler@0.2.0#handle\nfunc handle() {}\n//export cabi_post_handle\nfunc post() {}\nfunc main() {}\n", true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := wasmFixture(t, nil)
			r.Files = []File{{ID: "f", Path: "main.go", Source: tc.source}}
			r.Symbols = []Symbol{{ID: "handle", Name: "handle", Location: Location{FileID: "f", Line: 3, EndLine: 3}}}
			inv, err := collectWasmEntryPoints(context.Background(), r)
			if err != nil {
				t.Fatal(err)
			}
			if (inv != nil) != tc.wasm {
				t.Fatalf("inventory=%#v", inv)
			}
			if inv != nil && (len(inv.Entries) != tc.count || len(inv.Limitations) == 0) {
				t.Fatalf("inventory=%#v", inv)
			}
		})
	}
}

func TestWasmEntryConfigSafetyAndMalformed(t *testing.T) {
	for _, config := range []string{"worlds = [", "worlds = \"not-an-array\""} {
		r := wasmFixture(t, map[string]string{"componentize-go.toml": config})
		if _, err := collectWasmEntryPoints(context.Background(), r); err == nil {
			t.Fatalf("accepted malformed config %q", config)
		}
	}
	r := wasmFixture(t, map[string]string{
		"componentize-go.toml": "worlds = [\"app\"]\nwit_paths = [\"../outside\"]\n",
		"wit/world.wit":        "package test:app; world app { export good: func(); }",
		"excluded/bad.wit":     "package test:bad; world bad { export bad: func(); }",
		"nested/go.mod":        "module nested\n", "nested/wit/world.wit": "package nested:bad; world bad { export bad: func(); }",
	})
	r.Scope.ExcludePaths = []string{"excluded"}
	if err := os.Symlink(filepath.Join(r.Root, "excluded"), filepath.Join(r.Root, "linked")); err != nil {
		t.Fatal(err)
	}
	inv, err := collectWasmEntryPoints(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Entries) != 1 || inv.Entries[0].Function != "good" || len(inv.Limitations) == 0 {
		t.Fatalf("inventory=%#v", inv)
	}
	for _, src := range inv.Sources {
		if strings.Contains(src.Path, "excluded") || strings.Contains(src.Path, "nested") || strings.Contains(src.Path, "linked") {
			t.Fatalf("unsafe source %s", src.Path)
		}
	}
}

func TestWITInlineIncludeAndSelection(t *testing.T) {
	r := wasmFixture(t, map[string]string{
		"wasmcloud.toml": "[component]\nwit_world = \"app\"\nwit_path = \"wit\"\n",
		"wit/world.wit":  "package test:app;\nworld base { export inherited: func(); }\nworld app { include base; export api: interface { run: async func(); } }\n",
	})
	inv, err := collectWasmEntryPoints(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if inv == nil || len(inv.Entries) != 2 {
		t.Fatalf("inventory=%#v", inv)
	}
}

func TestWasmEntryWorkloadAndUnreferencedDependency(t *testing.T) {
	r := wasmFixture(t, map[string]string{
		"workload.yaml":             "apiVersion: runtime.wasmcloud.dev/v1alpha1\nkind: Workload\nmetadata:\n  name: worker\nspec:\n  component: file://worker.wasm\n",
		"arbitrary.yaml":            "key: arbitrary\n",
		"wit/deps/unused/world.wit": "package unused:dep; world unused { export wrong: func(); }",
	})
	inv, err := collectWasmEntryPoints(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}

	if inv == nil || len(inv.Entries) != 0 || len(inv.Sources) != 2 || len(inv.Limitations) == 0 {
		t.Fatalf("inventory=%#v", inv)
	}
}

func TestWasmEntryConfiguredRepositoryWIT(t *testing.T) {
	r := wasmFixture(t, map[string]string{
		"module/componentize-go.toml": "worlds = [\"worker\"]\nwit_paths = [\"../shared\"]\n",
		"shared/world.wit":            "package shared:app; world worker { export run: func(); }",
	})
	r.Scope.Root = r.Root
	r.Root = filepath.Join(r.Root, "module")
	inv, err := collectWasmEntryPoints(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if inv == nil || len(inv.Entries) != 1 || inv.Entries[0].Function != "run" {
		t.Fatalf("inventory=%#v", inv)
	}
}

func TestWasmEntryMalformedWorkloadAndCancellation(t *testing.T) {
	r := wasmFixture(t, map[string]string{
		"workload.yaml": "apiVersion: runtime.wasmcloud.dev/v1alpha1\nkind: Workload\nspec: [",
	})
	if _, err := collectWasmEntryPoints(context.Background(), r); err == nil {
		t.Fatal("accepted malformed workload")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := collectWasmEntryPoints(ctx, r); err != context.Canceled {
		t.Fatalf("cancellation=%v", err)
	}
}

func TestWasmEntryRequiresBindingEvidence(t *testing.T) {
	r := wasmFixture(t, map[string]string{"wit/world.wit": "package test:app; world app { export run: func(); }"})
	r.Files = []File{{ID: "f", Path: "main.go", Source: "package main\nfunc Run() {}\n"}}
	r.Symbols = []Symbol{{ID: "hand", Name: "Run", Location: Location{FileID: "f", Line: 2}}}
	inv, err := collectWasmEntryPoints(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}

	if inv.Entries[0].SymbolID != "" || inv.Entries[0].Resolution != "unresolved" {
		t.Fatalf("name-only resolution: %#v", inv.Entries[0])
	}
}

func TestWasmEntryGeneratedSupportingWrapper(t *testing.T) {
	r := wasmFixture(t, map[string]string{
		"componentize-go.toml": "worlds = [\"worker\"]\nwit_paths = [\"wit\"]\n",
		"wit/world.wit":        "package test:app; world worker { export wasmcloud:nats/handler@0.1.0; }",
		"wit/deps/nats.wit":    "package wasmcloud:nats@0.1.0; interface handler { handle-message: async func(); }",
	})
	r.Files = []File{{ID: "hand", Path: "export_handler/handler.go", Package: "test/app/export_handler", Source: "package export_handler\nfunc HandleMessage() {}\n"}}
	r.Symbols = []Symbol{{ID: "handler", Package: "test/app/export_handler", Name: "HandleMessage", Kind: "function", Location: Location{FileID: "hand", Line: 2, EndLine: 2}}}
	r.SupportingFiles = []File{{ID: "generated", Path: "wit_exports.go", Source: "// Generated by `wit-bindgen` 0.61.1. DO NOT EDIT!\npackage main\nimport (\n async \"example.org/async\"\n exports \"test/app/export_handler\"\n)\n//go:wasmexport [async-lift]wasmcloud:nats/handler@0.1.0#handle-message\nfunc boundary() { async.Run(func() { exports.HandleMessage() }) }\n"}}
	inv, err := collectWasmEntryPoints(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	e := inv.Entries[0]
	if e.SymbolID != "handler" || e.BoundarySymbolID != "" || e.Resolution != "resolved" {
		t.Fatalf("supporting wrapper lost: %#v", e)
	}
	found := false
	for _, ev := range e.Evidence {
		if ev.Kind == "wrapper-call" && ev.Location != nil && ev.Location.FileID == "generated" && ev.Location.Line == 8 {
			found = true
		}
	}
	if !found {
		t.Fatalf("wrapper evidence missing: %#v", e.Evidence)
	}
}

func TestWITLocalInterfaceCanonicalIdentityAndAnnotations(t *testing.T) {
	r := wasmFixture(t, map[string]string{
		"wit/world.wit": "package test:app@1.2.0;\ninterface api { @since(version = 1.0.0) run: func(); }\nworld worker { @since(version = 1.0.0) export api; }",
	})
	inv, err := collectWasmEntryPoints(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if inv == nil || len(inv.Entries) != 1 || inv.Entries[0].Interface != "test:app/api@1.2.0" || inv.Entries[0].Function != "run" {
		t.Fatalf("inventory=%#v", inv)
	}
}
