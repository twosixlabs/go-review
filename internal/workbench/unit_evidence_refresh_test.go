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

func TestUnitEvidenceRefreshKeepsSelectedNonFirstModule(t *testing.T) {
	report, err := review.Analyze(context.Background(), review.Options{Dir: "../review/testdata/module"})
	if err != nil {
		t.Fatal(err)
	}
	other := &review.Report{Root: "/other", Module: "other", SourceSHA256: "other-source"}
	previous := &review.RepositoryReview{Root: report.Scope.Root, Directory: "/old",
		Modules: []review.ReviewedModule{{Directory: "/other", Report: other}, {Directory: report.Root, Report: report}}}
	server := NewReviewed(previous)
	selection, err := json.Marshal(map[string]string{"directory": report.Root})
	if err != nil {
		t.Fatal(err)
	}
	selected := httptest.NewRecorder()
	selectRequest := httptest.NewRequest("POST", "http://127.0.0.1:8090/api/review/select", strings.NewReader(string(selection)))
	selectRequest.Header.Set("Content-Type", "application/json")
	server.Handler().ServeHTTP(selected, selectRequest)
	if selected.Code != 200 {
		t.Fatal(selected.Code, selected.Body.String())
	}
	w := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "http://127.0.0.1:8090/api/unit-tests",
		strings.NewReader(`{"execute":true,"selections":[{"package":".","tests":["TestPaths"]}]}`))
	request.Header.Set("Content-Type", "application/json")
	server.Handler().ServeHTTP(w, request)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	result := server.current()
	t.Cleanup(func() {
		if err := os.RemoveAll(result.UnitEvidence.Directory); err != nil {
			t.Error(err)
		}
	})
	if result.Root != report.Root || result.Module != report.Module || result.SourceSHA256 != report.SourceSHA256 {
		t.Fatalf("collection switched modules: got %s (%s), want %s (%s)", result.Root, result.Module, report.Root, report.Module)
	}
	if server.review.Modules[0].Report.UnitEvidence != nil || server.review.Modules[1].Report != result {
		t.Fatal("unit evidence was attached to the wrong retained module")
	}
	data, err := os.ReadFile(filepath.Join(result.UnitEvidence.Directory, "review.json"))
	if err != nil {
		t.Fatal(err)
	}
	var retained review.RepositoryReview
	if err := json.Unmarshal(data, &retained); err != nil {
		t.Fatal(err)
	}
	if retained.Modules[0].Report.Module != "other" || retained.Modules[0].Report.UnitEvidence != nil ||
		retained.Modules[1].Report.Module != report.Module || retained.Modules[1].Report.UnitEvidence == nil {
		t.Fatal("persisted module/evidence identities do not match the selected module")
	}
	if report.UnitEvidence != nil || other.UnitEvidence != nil {
		t.Fatal("collection mutated a previously published module")
	}
}

func TestUnitEvidenceRefreshPreservesUnrelatedFailuresAndManifestIdentity(t *testing.T) {
	report, err := review.Analyze(context.Background(), review.Options{Dir: "../review/testdata/module"})
	if err != nil {
		t.Fatal(err)
	}
	other := &review.Report{Root: "/other", Review: &review.ReviewReference{Directory: "/old"}}
	previous := &review.RepositoryReview{Root: report.Scope.Root, Directory: "/old", Status: "attention",
		Steps:   []review.ReviewStep{{ID: "vet", Module: report.Root, Status: "fail", Detail: "unrelated vet failure"}},
		Modules: []review.ReviewedModule{{Directory: report.Root, Report: report}, {Directory: "/other", Report: other}}}
	server := NewReviewed(previous)
	w := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "http://127.0.0.1:8090/api/unit-tests",
		strings.NewReader(`{"execute":true,"selections":[{"package":".","tests":["TestPaths"]}]}`))
	request.Header.Set("Content-Type", "application/json")
	server.Handler().ServeHTTP(w, request)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	result := server.current()
	t.Cleanup(func() {
		if err := os.RemoveAll(result.UnitEvidence.Directory); err != nil {
			t.Error(err)
		}
	})
	if server.review.Status != "attention" || result.Review.Status != "attention" {
		t.Fatal("passing tests erased an unrelated review failure")
	}
	for _, module := range server.review.Modules {
		if module.Report.Review.Directory != server.review.Directory {
			t.Fatal("new retained manifest has inconsistent report references")
		}
	}
	if previous.Directory != "/old" || other.Review.Directory != "/old" {
		t.Fatal("published prior review was mutated")
	}
}
