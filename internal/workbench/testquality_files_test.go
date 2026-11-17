package workbench

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/russellbrasser/go-review/internal/testquality"
)

func TestQualityAgentFileBrowsingRejectsRacingPrivateAliases(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"alias", ".git"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name, "config"), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if text, err := qualityLiveText(root, "alias/config"); err != nil || text != "alias" {
		t.Fatalf("ordinary file unavailable: %q %v", text, err)
	}
	stop := make(chan struct{})
	var readers sync.WaitGroup
	var leaked atomic.Bool
	for n := 0; n < 8; n++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				text, err := qualityLiveText(root, "alias/config")
				if err == nil && text == ".git" {
					leaked.Store(true)
					return
				}
			}
		}()
	}
	defer func() { close(stop); readers.Wait() }()
	for n := 0; n < 1000 && !leaked.Load(); n++ {
		if err := os.Rename(filepath.Join(dir, "alias"), filepath.Join(dir, "normal")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(".git", filepath.Join(dir, "alias")); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(dir, "alias")); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(filepath.Join(dir, "normal"), filepath.Join(dir, "alias")); err != nil {
			t.Fatal(err)
		}
	}
	if leaked.Load() {
		t.Fatal("a raced symlink exposed excluded .git contents")
	}
}

func TestQualityAgentFilePagesBoundEncodedOutput(t *testing.T) {
	s := qualityFixture(t)
	report := s.review.Modules[0].Report
	// Escaping makes these snippets much larger in JSON than their rune count.
	report.Files[0].Source = strings.Repeat(strings.Repeat("\t", 159)+"x\n", 200)
	raw, err := qualityFileCall(context.Background(), report, qualityFileInput{Query: "x", Limit: 100}, "review_search_files")
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(raw)
	if len(encoded) > 24000 {
		t.Fatalf("search result exceeds encoded budget: %d bytes", len(encoded))
	}
	items := []map[string]any{}
	for n := 0; n < 100; n++ {
		items = append(items, map[string]any{"path": fmt.Sprintf("%03d%s", n, strings.Repeat("\t", 1000))})
	}
	page, err := qualityFilePage(items, qualityFileInput{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ = json.Marshal(page)
	if len(encoded) > 23500 || page["nextOffset"] == nil {
		t.Fatalf("directory page was not byte-bounded: %d bytes", len(encoded))
	}
}

func TestQualityAgentFileBrowsingRejectsPrivateSymlinkAliases(t *testing.T) {
	s := qualityFixture(t)
	root := s.review.Modules[0].Report.Root
	if err := os.Mkdir(filepath.Join(root, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "config"), []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(".git", filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"review_read_file", "review_list_files", "review_search_files"} {
		if _, err := qualityFileCall(t.Context(), s.review.Modules[0].Report, qualityFileInput{Source: "live", Path: "alias/config", Query: "private", FilePattern: "*"}, operation); err == nil {
			t.Fatalf("%s exposed private symlink alias", operation)
		}
	}
}

func TestQualityAgentFileBrowsingSeparatesLiveAndRetainedEvidence(t *testing.T) {
	s := qualityFixture(t)
	report := s.review.Modules[0].Report
	original := report.TestSources[0].File
	if err := os.WriteFile(filepath.Join(report.Root, original.Path), []byte(original.Source), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(report.Root, "new_test.go"), []byte("package example\n// live-only marker\n"), 0600); err != nil {
		t.Fatal(err)
	}
	a, err := s.qualityStore.Create(t.Context(), s.review, "", "")
	if err != nil {
		t.Fatal(err)
	}
	options, closeSession, err := s.qualityEvidence(t.Context(), a, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeSession()
	read := func(name string, args map[string]any) (json.RawMessage, []testquality.Citation, error) {
		t.Helper()
		args["snapshotID"] = a.Reports[0].SnapshotID
		raw, _ := json.Marshal(args)
		return options.Read(t.Context(), name, raw)
	}
	for _, source := range []string{"retained", "live"} {
		raw, _, err := read("review_list_files", map[string]any{"source": source, "path": "."})
		if err != nil || !strings.Contains(string(raw), "f_test.go") {
			t.Fatalf("%s inventory hid test files: %s %v", source, raw, err)
		}
		if strings.Contains(string(raw), "new_test.go") != (source == "live") {
			t.Fatalf("inventory mixed live/retained files: %s", raw)
		}
	}
	raw, seen, err := read("review_read_file", map[string]any{"source": "live", "path": original.Path, "startLine": 3, "endLine": 3})
	if err != nil || len(seen) != 1 || seen[0].FileID != original.ID {
		t.Fatalf("unchanged live source lost pinned authority: %s %+v %v", raw, seen, err)
	}
	if err := os.WriteFile(filepath.Join(report.Root, original.Path), []byte("package example\n// changed live source\n"), 0600); err != nil {
		t.Fatal(err)
	}
	raw, seen, err = read("review_read_file", map[string]any{"source": "live", "path": original.Path})
	if err != nil || len(seen) != 0 || !strings.Contains(string(raw), "changed live source") || !strings.Contains(string(raw), `"snapshotMatch":false`) {
		t.Fatalf("changed live text misrepresented as pinned source: %s %+v %v", raw, seen, err)
	}
	raw, seen, err = read("review_read_file", map[string]any{"source": "retained", "path": original.Path, "startLine": 3, "endLine": 3})
	if err != nil || len(seen) != 1 || !strings.Contains(string(raw), "t.Fatal") {
		t.Fatalf("live edit changed assessment evidence: %s %v", raw, err)
	}
	raw, seen, err = read("review_search_files", map[string]any{"source": "live", "path": ".", "query": "live-only marker", "filePattern": "*_test.go"})
	if err != nil || len(seen) != 0 || !strings.Contains(string(raw), "new_test.go") {
		t.Fatalf("live scoped search failed: %s %v", raw, err)
	}
	for _, args := range []map[string]any{
		{"source": "live", "path": "../outside"},
		{"source": "live", "path": report.Root},
		{"source": "live", "path": ".env"},
		{"source": "retained", "path": "missing.go"},
	} {
		if _, _, err := read("review_read_file", args); err == nil {
			t.Fatalf("invalid source read accepted: %+v", args)
		}
	}
}

func TestQualityAgentFileReadsRejectEscapingSymlinksAndPageText(t *testing.T) {
	s := qualityFixture(t)
	report := s.review.Modules[0].Report
	outside := filepath.Join(t.TempDir(), "outside.go")
	if err := os.WriteFile(outside, []byte("not project data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(report.Root, "escape.go")); err != nil {
		t.Fatal(err)
	}
	text := "package example\n// " + strings.Repeat("x", 13000) + "\n"
	report.Files[0].Source, report.Files[0].SHA256 = text, testquality.Digest([]byte(text))
	a, err := s.qualityStore.Create(t.Context(), s.review, "", "")
	if err != nil {
		t.Fatal(err)
	}
	options, closeSession, err := s.qualityEvidence(t.Context(), a, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeSession()
	raw, _ := json.Marshal(map[string]any{"snapshotID": a.Reports[0].SnapshotID, "source": "live", "path": "escape.go"})
	if _, _, err := options.Read(t.Context(), "review_read_file", raw); err == nil {
		t.Fatal("symlink escaped the project root")
	}
	var recovered strings.Builder
	offset := 0
	for {
		raw, _ = json.Marshal(map[string]any{"snapshotID": a.Reports[0].SnapshotID, "path": "f.go", "startLine": 2, "endLine": 2, "textOffset": offset, "textLimit": 1000})
		result, seen, err := options.Read(t.Context(), "review_read_file", raw)
		if err != nil || len(seen) != 0 {
			t.Fatalf("partial line authorized as full source: %s %v", result, err)
		}
		var page struct {
			Data struct {
				Text     string
				TextPage struct{ NextOffset *int }
			}
		}
		if err := json.Unmarshal(result, &page); err != nil {
			t.Fatal(err)
		}
		recovered.WriteString(page.Data.Text)
		if page.Data.TextPage.NextOffset == nil {
			break
		}
		offset = *page.Data.TextPage.NextOffset
	}
	if recovered.String() != "// "+strings.Repeat("x", 13000) {
		t.Fatal("paged source was truncated or skipped")
	}
}
