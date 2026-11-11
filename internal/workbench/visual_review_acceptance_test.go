package workbench

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/russellbrasser/go-review/internal/review"
)

func TestVisualReviewProjects(t *testing.T) {
	for name, source := range map[string]string{
		"service": `package main
import "net/http"
type Store struct{}
func (*Store) Save(v string) bool { return v != "" }
type Service struct{ store *Store }
// Submit validates and stores an item before starting asynchronous work.
//
// The second paragraph explains the durable boundary without shortening it.
func (s *Service) Submit(v string) bool {
 if v == "" { return false }
 if !s.store.Save(v) { return false }
 go s.Process(v)
 return true
}
func (*Service) Process(v string) {}
func main() {
 s := &Service{store: &Store{}}
 http.HandleFunc("POST /items", func(w http.ResponseWriter, r *http.Request) {
  if !s.Submit(r.URL.Query().Get("item")) { http.Error(w, "invalid", 400); return }
  w.WriteHeader(202)
 })
}
`,
		"cli": `package main
type Store struct{}
func (*Store) Save(v int) int { if v < 1 { return 0 }; return v }
type Engine struct{ store *Store }
func (e *Engine) Run(v int) int { if v == 0 { return 0 }; return e.store.Save(v) }
func main() { e := &Engine{store: &Store{}}; e.Run(3) }
`,
		"library": `package library
// Compute validates an input and returns its normalized value.
func Compute(v int) int { if v < 1 { return 0 }; return normalize(v) }
func normalize(v int) int { return v + 1 }
`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			for path, content := range map[string]string{"go.mod": "module example.test/" + name + "\n\ngo 1.24\n", "main.go": source} {
				if err := os.WriteFile(filepath.Join(dir, path), []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			report, err := review.Analyze(context.Background(), review.Options{Dir: dir})
			if err != nil {
				t.Fatal(err)
			}
			if report.SystemFlow == nil || len(report.SystemFlow.Components) == 0 {
				t.Fatal("collector did not produce source-backed components")
			}
			if name == "service" && len(report.HTTPEndpoints) != 1 {
				t.Fatalf("HTTP registrations = %d, want 1", len(report.HTTPEndpoints))
			}
			if report.EntryPoints == nil || report.EntryPoints.Kind != name {
				t.Fatalf("entry strategy=%+v, want %s", report.EntryPoints, name)
			}
			if out := os.Getenv("GO_REVIEW_VISUAL_FIXTURES"); out != "" {
				if err := os.MkdirAll(out, 0700); err != nil {
					t.Fatal(err)
				}
				data, err := json.Marshal(report)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(out, name+".json"), data, 0600); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
