package webui

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/russellbrasser/go-review/internal/review"
)

func TestReviewQueryRetainedEvidence(t *testing.T) {
	r := &review.Report{Module: "sample", SourceSHA256: "source",
		Symbols:   []review.Symbol{{ID: "f", Name: "Work", Doc: "Complete rationale."}},
		Documents: []review.Document{{ID: "readme", Path: "README.md", Text: "What it does."}}}
	q, err := NewReviewQuery(r)
	if err != nil {
		t.Fatal(err)
	}
	result, err := q.Query(context.Background(), "document", json.RawMessage(`{"id":"readme"}`))
	if err != nil {
		t.Fatal(err)
	}
	var doc review.Document
	if err := json.Unmarshal(result, &doc); err != nil || doc.Text != "What it does." {
		t.Fatalf("document=%s error=%v", result, err)
	}
	if _, err := q.Query(context.Background(), "source", json.RawMessage(`{"fileID":"/etc/passwd"}`)); err == nil {
		t.Fatal("arbitrary file accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := q.Query(ctx, "overview", json.RawMessage(`{}`)); err == nil {
		t.Fatal("canceled query accepted")
	}
	if _, err := q.Query(context.Background(), "overview", json.RawMessage(`{}`)); err != nil {
		t.Fatalf("query after cancellation: %v", err)
	}
}

func TestReviewQueryByteBoundedUnicodeText(t *testing.T) {
	text := strings.Repeat("Full rationale λ 🧭\x00\n", 3000)
	r := &review.Report{Module: "sample", Documents: []review.Document{{ID: "doc", Text: text}}}
	q, err := NewReviewQuery(r)
	if err != nil {
		t.Fatal(err)
	}
	var restored strings.Builder
	for offset := 0; ; {
		args, err := json.Marshal(map[string]any{"id": "doc", "textOffset": offset})
		if err != nil {
			t.Fatal(err)
		}
		data, err := q.Query(context.Background(), "document", args)
		if err != nil || len(data) > 32768 {
			t.Fatalf("document bytes=%d error=%v", len(data), err)
		}
		var page struct {
			Text     string `json:"text"`
			TextPage struct {
				NextOffset *int `json:"nextOffset"`
			} `json:"textPage"`
		}
		if err := json.Unmarshal(data, &page); err != nil {
			t.Fatal(err)
		}
		restored.WriteString(page.Text)
		if page.TextPage.NextOffset == nil {
			break
		}
		if *page.TextPage.NextOffset <= offset {
			t.Fatal("text cursor failed to advance")
		}
		offset = *page.TextPage.NextOffset
	}
	if restored.String() != text {
		t.Fatal("paged Unicode documentation lost content in embedded Goja runtime")
	}
}

func TestReviewQueryBuilder(t *testing.T) {
	path := os.Getenv("GO_REVIEW_ENDPOINT_SNAPSHOT")
	if path == "" {
		t.Skip("set GO_REVIEW_ENDPOINT_SNAPSHOT for retained real-project projection")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var r review.Report
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatal(err)
	}
	q, err := NewReviewQuery(&r)
	if err != nil {
		t.Fatal(err)
	}
	overview, err := q.Query(context.Background(), "overview", json.RawMessage(`{"limit":2}`))
	if err != nil || len(overview) > 65536 {
		t.Fatalf("progressive overview bytes=%d error=%v", len(overview), err)
	}
	for _, e := range r.HTTPEndpoints {
		if e.Pattern != "POST /api/missions/{missionID}/builds" {
			continue
		}
		args, _ := json.Marshal(map[string]string{"entryID": e.ID})
		data, err := q.Query(context.Background(), "flow", args)
		if err != nil {
			t.Fatal(err)
		}
		var flow struct{ Nodes []json.RawMessage }
		if err := json.Unmarshal(data, &flow); err != nil || len(flow.Nodes) != 8 {
			t.Fatalf("nodes=%d error=%v", len(flow.Nodes), err)
		}
		return
	}
	t.Fatal("retained Builder route absent")
}
