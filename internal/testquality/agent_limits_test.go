package testquality

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/russellbrasser/go-review/internal/intelligence"
	"github.com/russellbrasser/go-review/internal/review"
)

func TestAgentEnforcesReadOnlyProtocolStepBudgetAndCancellation(t *testing.T) {
	for _, name := range []string{"shell", "read_evidence", "submit_assessment"} {
		t.Run(name, func(t *testing.T) {
			r := testReport(t.TempDir())
			a, err := (Store{Directory: t.TempDir()}).Create(context.Background(), &review.RepositoryReview{Root: r.Root, Modules: []review.ReviewedModule{{Report: r}}}, "", "")
			if err != nil {
				t.Fatal(err)
			}
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Even instruction-like test/source text cannot grant the model a
				// write/execute tool, invent IDs or supply an invalid final object.
				args := `{}`
				if name == "read_evidence" {
					args = `{\"snapshotID\":\"invented\",\"operation\":\"source\",\"args\":{}}`
				}
				fmt.Fprintf(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call","function":{"name":%q,"arguments":"%s"}}]},"finish_reason":"tool_calls"}]}`+"\n\n", name, args)
			}))
			defer provider.Close()
			if _, err := Assess(context.Background(), a, a.Items[0], intelligence.Config{Enabled: true, BaseURL: provider.URL, Model: "test", MaxSteps: 1}); err == nil {
				t.Fatal("invalid provider response accepted")
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := Assess(ctx, a, a.Items[0], intelligence.Config{Enabled: true, BaseURL: provider.URL, Model: "test"}); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation lost: %v", err)
			}
		})
	}
}

func TestAgentContextBudgetIncludesEvidenceAndToolSchemas(t *testing.T) {
	r := testReport(t.TempDir())
	for i := 0; i < 30; i++ {
		source := "package example\n"
		r.Files = append(r.Files, review.File{ID: fmt.Sprint(i), Path: strings.Repeat("x", 1500) + fmt.Sprintf("%d.go", i), Source: source, SHA256: Digest([]byte(source))})
	}
	a, err := (Store{Directory: t.TempDir()}).Create(context.Background(), &review.RepositoryReview{Root: r.Root, Modules: []review.ReviewedModule{{Report: r}}}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	defer provider.Close()
	_, err = Assess(context.Background(), a, a.Items[0], intelligence.Config{Enabled: true, BaseURL: provider.URL, Model: "test", ContextBytes: 32768})
	if err == nil || !strings.Contains(err.Error(), "context budget") || calls.Load() != 0 {
		t.Fatalf("budget not enforced before sending: %v calls=%d", err, calls.Load())
	}
}
