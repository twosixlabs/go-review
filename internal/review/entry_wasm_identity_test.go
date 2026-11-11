package review

import (
	"context"
	"testing"
)

func TestWasmEntryDuplicateConfiguredWorldIsOneEntry(t *testing.T) {
	r := wasmFixture(t, map[string]string{
		"componentize-go.toml": "worlds = [\"app\", \"app\"]\n",
		"wasmcloud.toml":       "[component]\nwit_world = \"app\"\n",
		"wit/world.wit":        "package test:app; world app { export run: func(); }",
	})
	inv, err := collectWasmEntryPoints(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Entries) != 1 {
		t.Fatalf("duplicate entries: %#v", inv.Entries)
	}
	configSources := map[string]bool{}
	for _, ev := range inv.Entries[0].Evidence {
		if ev.Kind == "configuration" {
			configSources[ev.SourceID] = true
		}
	}
	if len(configSources) != 2 {
		t.Fatalf("selection evidence lost: %#v", inv.Entries[0].Evidence)
	}
}
