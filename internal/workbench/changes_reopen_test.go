package workbench

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/russellbrasser/go-review/internal/changes"
	"github.com/russellbrasser/go-review/internal/review"
)

func TestChangeReopeningDoesNotReplaceApplication(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	storage, err := changes.Storage()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(storage, 0700); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(storage, "comparison-")
	if err != nil {
		t.Fatal(err)
	}
	c := &changes.Comparison{ID: strings.Repeat("a", 64), Directory: dir, Revisions: changes.Resolved{Repository: "/fixture"}}
	if err := changes.Publish(storage, c); err != nil {
		t.Fatal(err)
	}
	s := NewReviewed(&review.RepositoryReview{Root: "/fixture"})
	if s.comparison == nil || s.comparison.ID != c.ID || s.current() != nil {
		t.Fatal("retained comparison was not independently reopened")
	}
	if err := s.ShutdownChanges(context.Background()); err != nil {
		t.Fatal(err)
	}
}
