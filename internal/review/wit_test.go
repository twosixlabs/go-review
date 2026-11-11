package review

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWITPackageHeaderSharedAcrossFiles(t *testing.T) {
	r := wasmFixture(t, map[string]string{
		"wit/world.wit": "package test:app@1.0.0; world app { export api; }",
		"wit/api.wit":   "interface api { run: func(); }",
	})
	inv, err := collectWasmEntryPoints(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Entries) != 1 || inv.Entries[0].Function != "run" || inv.Entries[0].Interface != "test:app/api@1.0.0" {
		t.Fatalf("inventory=%#v", inv)
	}
}

func TestWITUnsupportedIncludesAndResourcesAreExplicit(t *testing.T) {
	r := wasmFixture(t, map[string]string{
		"wit/world.wit":        "package test:app; world base { export inherited: func(); } world app { include base with { inherited as renamed }; export api: interface { resource client { run: func(); } } }",
		"componentize-go.toml": "worlds = [\"app\"]",
	})
	inv, err := collectWasmEntryPoints(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Entries) != 2 || len(inv.Limitations) < 2 {
		t.Fatalf("inventory=%#v", inv)
	}
	for _, e := range inv.Entries {
		if e.Resolution != "unresolved" || e.Reason == "" {
			t.Fatalf("entry=%#v", e)
		}
	}
}

func TestWasmEntryBuilderReadOnly(t *testing.T) {
	root := os.Getenv("GO_REVIEW_WASM_ENTRY_DIR")
	if root == "" {
		t.Skip("opt-in read-only local component source acceptance")
	}
	exports, err := os.ReadFile(filepath.Join(root, "wit_exports.go"))
	if err != nil {
		t.Fatal(err)
	}
	handler, err := os.ReadFile(filepath.Join(root, "export_wasmcloud_nats_jetstream_handler", "handler.go"))
	if err != nil {
		t.Fatal(err)
	}
	pkg := "builder/components/workflow-handler/export_wasmcloud_nats_jetstream_handler"
	r := &Report{Root: root, Scope: ReviewScope{Root: root},
		Files:           []File{{ID: "hand", Path: "export_wasmcloud_nats_jetstream_handler/handler.go", Package: pkg, Source: string(handler)}},
		SupportingFiles: []File{{ID: "generated", Path: "wit_exports.go", Source: string(exports)}},
		Symbols:         []Symbol{{ID: "handler", Package: pkg, Name: "HandleMessage", Kind: "function", Location: Location{FileID: "hand"}}},
	}
	inv, err := collectWasmEntryPoints(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if inv == nil || len(inv.Entries) != 1 {
		t.Fatalf("inventory=%#v", inv)
	}
	e := inv.Entries[0]
	if e.Interface != "wasmcloud:nats/jetstream-handler@0.1.0" || e.Function != "handle-message" || e.SymbolID != "handler" {
		t.Fatalf("entry=%#v", e)
	}
	for _, s := range inv.Sources {
		if s.SHA256 == "" || s.Text == "" {
			t.Fatal("missing source snapshot")
		}
	}
	t.Logf("%s -> %s (%s); %d retained sources", e.Name, e.SymbolID, e.Reason, len(inv.Sources))
}

func TestWasmEntryWorkloadTriggerEvidence(t *testing.T) {
	r := wasmFixture(t, map[string]string{
		"wit/world.wit": "package test:app; world app { export run: func(); }",
		"workload.yaml": "apiVersion: runtime.wasmcloud.dev/v1alpha1\nkind: Workload\nspec:\n  bindings:\n    - name: nats\n      subjects:\n        - events.orders\n",
	})
	inv, err := collectWasmEntryPoints(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ev := range inv.Entries[0].Evidence {
		if ev.Kind == "trigger-configuration" && ev.Line == 7 && strings.Contains(ev.Detail, "events.orders") {
			found = true
		}
	}
	if !found {
		t.Fatalf("trigger not retained: %#v", inv.Entries[0].Evidence)
	}
}
