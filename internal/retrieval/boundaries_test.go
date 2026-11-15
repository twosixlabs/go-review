package retrieval

import (
	"context"
	"strings"
	"testing"

	"github.com/russellbrasser/go-review/internal/review"
)

func TestChunksDocumentSectionBoundaries(t *testing.T) {
	r := &review.Report{Documents: []review.Document{{ID: "doc", Path: "README.md", Text: "# One\nFIRST_SECTION_MARKER\n```sh\n# Not a section\n```\n# Two\nSECOND_SECTION_MARKER\n"}}}
	chunks, err := Chunks(context.Background(), r, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 2 {
		t.Fatalf("sections were merged: %d chunks", len(chunks))
	}
	for _, chunk := range chunks {
		if strings.Contains(chunk.Text, "FIRST_SECTION_MARKER") && strings.Contains(chunk.Text, "SECOND_SECTION_MARKER") {
			t.Fatal("unrelated sections share a vector")
		}
	}
}

func TestChunksNeighboringSourceWindowsOverlap(t *testing.T) {
	r := &review.Report{Files: []review.File{{ID: "file", Path: "file.go", Source: "package sample\nfunc F() {\n" + strings.Repeat("println(\"context\")\n", 80) + "}\n"}}}
	chunks, err := Chunks(context.Background(), r, 256)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(chunks); i++ {
		if chunks[i].StartLine <= chunks[i-1].EndLine {
			return
		}
	}
	t.Fatal("long declaration chunks lack bounded neighboring context")
}
