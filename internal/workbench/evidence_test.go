package workbench

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/russellbrasser/go-review/internal/review"
)

func TestCoverageImportPreservesSnapshotAndShowsExternalProvenance(t *testing.T) {
	report, err := review.Analyze(context.Background(), review.Options{Dir: "../review/testdata/module"})
	if err != nil {
		t.Fatal(err)
	}
	h := New(report).Handler()
	path := filepath.Join(t.TempDir(), "coverage.out")
	if err = os.WriteFile(path, []byte("mode: count\nexample.test/fixture/flow.go:18.2,19.6 1 4\n"), 0600); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"path": path})
	r := httptest.NewRequest("POST", "http://127.0.0.1:8090/api/coverage", strings.NewReader(string(body)))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("coverage: %d %s", w.Code, w.Body.String())
	}
	var result review.Report
	if err = json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Coverage == nil || result.Coverage.Blocks[0].Count != 4 || result.Coverage.SourceVerified {
		t.Fatal("missing coverage or imported source incorrectly certified")
	}
	if report.Coverage != nil {
		t.Fatal("published snapshot mutated")
	}
	if result.SourceSHA256 != report.SourceSHA256 {
		t.Fatal("source provenance lost")
	}
}

func TestUnitEvidenceRequiresExplicitConsentAndSelection(t *testing.T) {
	h := New(nil).Handler()
	for _, body := range []string{`{"selections":[{"package":".","tests":["TestPaths"]}]}`, `{"execute":true,"selections":[]}`} {
		r := httptest.NewRequest("POST", "http://127.0.0.1:8090/api/unit-tests", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatalf("unsafe request accepted: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestUnitEvidenceAPIRetainsSelectedCoverageWithoutMutatingPublishedSnapshot(t *testing.T) {
	report, err := review.Analyze(context.Background(), review.Options{Dir: "../review/testdata/module"})
	if err != nil {
		t.Fatal(err)
	}
	if err = review.ImportCoverage(report, strings.NewReader("mode: count\nexample.test/fixture/flow.go:18.2,19.6 1 4\n"), "test.cover"); err != nil {
		t.Fatal(err)
	}
	h := New(report).Handler()
	r := httptest.NewRequest("POST", "http://127.0.0.1:8090/api/unit-tests", strings.NewReader(`{"execute":true,"selections":[{"package":".","tests":["TestPaths"]}]}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("trace: %d %s", w.Code, w.Body.String())
	}
	var result review.Report
	if err = json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.UnitEvidence == nil || result.UnitEvidence.Runs[0].Status != "pass" || result.Trace != nil {
		t.Fatal("selected unit coverage missing or rejected recorder still active")
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(result.UnitEvidence.Directory); err != nil {
			t.Error(err)
		}
	})
	if result.Coverage == nil || !result.Coverage.SourceVerified {
		t.Fatal("new source-verified selected coverage missing")
	}

	if report.UnitEvidence != nil || report.Coverage.SourceVerified {
		t.Fatal("published input snapshot was mutated")
	}
	if _, err := os.Stat(result.UnitEvidence.Runs[0].EventsPath); err != nil {
		t.Fatal("unit evidence was deleted after API collection:", err)
	}
	if _, err := os.Stat(filepath.Join(result.UnitEvidence.Directory, "review.json")); err != nil {
		t.Fatal("review cannot be reopened:", err)
	}
	r = httptest.NewRequest("POST", "http://127.0.0.1:8090/api/unit-tests", strings.NewReader(`{"execute":true,"selections":[{"package":".","tests":["NoMatchingTest"]}]}`))
	r.Header.Set("Content-Type", "application/json")
	missing := httptest.NewRecorder()
	h.ServeHTTP(missing, r)
	if missing.Code != 422 {
		t.Fatalf("invalid/missing test was not rejected: %d %s", missing.Code, missing.Body.String())
	}
}
