package review

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWasmEntryRepositoryWorkload(t *testing.T) {
	r := wasmFixture(t, map[string]string{
		"module/main.go":      "package main\nfunc main() {}\n",
		"deploy/worker.yaml":  "apiVersion: runtime.wasmcloud.dev/v1alpha1\nkind: Workload\nspec:\n  component: file://worker.wasm\n",
		"other/go.mod":        "module other\n",
		"other/workload.yaml": "apiVersion: runtime.wasmcloud.dev/v1alpha1\nkind: Workload\n",
	})
	r.Scope.Root = r.Root
	r.Root = filepath.Join(r.Root, "module")
	inv, err := collectWasmEntryPoints(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if inv == nil || inv.Kind != "wasm" || len(inv.Sources) != 1 || inv.Sources[0].Path != "../deploy/worker.yaml" {
		t.Fatalf("inventory=%#v", inv)
	}
}

func TestWasmEntryBoundsAndMalformedWITAreExplicit(t *testing.T) {
	for _, tc := range []struct{ name, source, warning string }{
		{"malformed", "package test:app; world app { export run: func();", "unterminated"},
		{"oversized", strings.Repeat("x", (2<<20)+1), "2 MiB"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := wasmFixture(t, map[string]string{"wit/world.wit": tc.source})
			inv, err := collectWasmEntryPoints(context.Background(), r)
			if err != nil {
				t.Fatal(err)
			}
			if inv == nil || !strings.Contains(strings.Join(inv.Limitations, "\n"), tc.warning) {
				t.Fatalf("inventory=%#v", inv)
			}
		})
	}
}

func TestWasmEntryConfiguredSymlinkAndSnapshotHashChange(t *testing.T) {
	r := wasmFixture(t, map[string]string{
		"module/componentize-go.toml": "worlds = [\"worker\"]\nwit_paths = [\"../shared/link\"]",
		"shared/real/world.wit":       "package test:app; world worker { export run: func(); }",
	})
	if err := os.Symlink(filepath.Join(r.Root, "shared", "real"), filepath.Join(r.Root, "shared", "link")); err != nil {
		t.Fatal(err)
	}
	r.Scope.Root = r.Root
	r.Root = filepath.Join(r.Root, "module")
	inv, err := collectWasmEntryPoints(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	for _, src := range inv.Sources {
		if strings.HasSuffix(src.Path, "world.wit") {
			t.Fatalf("followed symlink: %#v", src)
		}
	}
	first := inv.Sources[0]
	if err := os.WriteFile(filepath.Join(r.Root, "componentize-go.toml"), []byte("worlds = [\"different\"]"), 0600); err != nil {
		t.Fatal(err)
	}
	again, err := collectWasmEntryPoints(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if again.Sources[0].SHA256 == first.SHA256 || again.Sources[0].ID != first.ID || again.Sources[0].Text == first.Text {
		t.Fatal("snapshot change not reflected")
	}
}
