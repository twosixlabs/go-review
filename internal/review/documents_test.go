package review

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDocumentationDefaultsToContainingRepository(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{
		".git":                    "gitdir: elsewhere\n",
		"README.md":               "# Repository overview\n",
		"docs/architecture.md":    "# Architecture\n",
		"server/go.mod":           "module example.test/server\n\ngo 1.24\n",
		"server/main.go":          "package main\nfunc main() {}\n",
		"server/README.md":        "# Backend\n",
		"node_modules/ignored.md": "ignored",
	} {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	r, err := Analyze(context.Background(), Options{Dir: filepath.Join(dir, "server")})
	if err != nil {
		t.Fatal(err)
	}
	if r.DocumentationRoot != dir {
		t.Fatalf("documentation root %s, want %s", r.DocumentationRoot, dir)
	}
	var paths []string
	for _, doc := range r.Documents {
		paths = append(paths, doc.Path)
	}
	if !reflect.DeepEqual(paths, []string{"README.md", "docs/architecture.md", "server/README.md"}) {
		t.Fatalf("repository documents: %v", paths)
	}
	r, err = Analyze(context.Background(), Options{Dir: filepath.Join(dir, "server"), DocumentationDir: filepath.Join(dir, "docs")})
	if err != nil {
		t.Fatal(err)
	}
	if r.DocumentationRoot != filepath.Join(dir, "docs") || len(r.Documents) != 1 {
		t.Fatalf("explicit documentation root ignored: %s (%d docs)", r.DocumentationRoot, len(r.Documents))
	}
}

func TestDocumentationWithoutRepositoryDefaultsToModule(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{
		"go.mod":    "module example.test/standalone\n\ngo 1.24\n",
		"main.go":   "package main\nfunc main() {}\n",
		"README.md": "# Standalone\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	r, err := Analyze(context.Background(), Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if r.DocumentationRoot != dir || len(r.Documents) != 1 {
		t.Fatalf("standalone docs: %s %d", r.DocumentationRoot, len(r.Documents))
	}
}
