package workbench

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/russellbrasser/go-review/internal/review"
)

// These opt-in real-repository checks use the normal API and exact, inspected
// unit selections. They never start Builder, deploy Wasm or run repository hooks.
func TestSystemFlowBuilderAcceptance(t *testing.T) {
	root := os.Getenv("GO_REVIEW_BUILDER_DIR")
	if root == "" {
		t.Skip("use the named builder selector with a Builder server checkout")
	}
	scope, err := review.LoadReviewScope(root)
	if err != nil {
		t.Fatal(err)
	}
	prefix, err := filepath.Rel(scope.Root, root)
	if err != nil {
		t.Fatal(err)
	}
	// These are dedicated test-server/benchmark executables, not Builder server
	// capabilities. This is acceptance scope, not a production grouping rule.
	for _, path := range []string{"cmd/builder-testserver", "cmd/cosmonic-bench-*"} {
		scope.ExcludePaths = append(scope.ExcludePaths, filepath.ToSlash(filepath.Join(prefix, path)))
	}
	report, err := review.Analyze(context.Background(), review.Options{Dir: root, Scope: &scope})
	if err != nil {
		t.Fatal(err)
	}
	selections := []review.UnitTestSelection{
		{Package: "./cmd/builder", Tests: []string{"TestServerHealth"}},
		{Package: "./internal/app", Tests: []string{"TestHandlerServesHealth", "TestNewPreservesTheGitLabClientStartupCause"}},
		{Package: "./internal/app/assist/build", Tests: []string{
			"TestBuilderStartIsIdempotent", "TestInitialClarificationPersistsQuestionBeforeAwaitingPhase",
			"TestMissionReviewApproveCreatesRevisionAndFreshAuthorAttempt",
			"TestAcceptThatFailsToCommitLeavesTheBuildInReviewForAnotherAttempt",
		}},
		{Package: "./internal/app/runner", Tests: []string{
			"TestStartRejectsUnopenableWorkflowsMismatchedFixturesAndBlankInput",
			"TestCancelRecordsTerminalStateAndStopsRuntime", "TestRuntimeFailureRetainsUnderlyingCauseInFailedEvent",
		}},
		{Package: "./internal/app/workflowgit", Tests: []string{
			"TestCreateRejectsInvalidRequestsBeforeTouchingGitLab",
			"TestCreateCommitsOnlyTheAcceptedSourceAndCIPolicyToTheAuthorBranch",
		}},
	}
	data, err := json.Marshal(map[string]any{"execute": true, "selections": selections, "runtimeTrace": true})
	if err != nil {
		t.Fatal(err)
	}
	server := New(report)
	request := httptest.NewRequest("POST", "http://127.0.0.1:8090/api/unit-tests", strings.NewReader(string(data)))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	result := server.current()
	manifest := filepath.Join(result.UnitEvidence.Directory, "review.json")
	t.Log("Retained Builder review:", manifest)
	if len(result.UnitEvidence.Runs) != len(selections) {
		t.Fatal("selection provenance is incomplete")
	}
	for _, run := range result.UnitEvidence.Runs {
		if run.Status != "pass" {
			t.Errorf("%s: %s", run.Selection.Package, run.Error)
		}
		if len(run.MatchedTests) != len(run.Selection.Tests) {
			t.Error("selected tests did not all execute:", run.Selection)
		}
	}
	for _, suffix := range []string{"/httpapi", "/assist/build", "/assist/authoring", "/assist/clarification",
		"/runner", "/workflowgit", "/catalog", "/scriptcheck", "/scriptsample", "/assist/scriptsuggest"} {
		found := false
		for _, c := range result.SystemFlow.Components {
			found = found || strings.HasSuffix(c.Package, suffix)
		}
		if !found {
			t.Error("server capability missing:", suffix)
		}
	}
	for _, c := range result.SystemFlow.Components {
		if strings.Contains(c.Package, "/testsupport/") {
			t.Error("test stand-in became a production component:", c.Package)
		}
	}
	ingress := 0
	for _, endpoint := range result.HTTPEndpoints {
		var file review.File
		for _, candidate := range result.Files {
			if candidate.ID == endpoint.Location.FileID {
				file = candidate
			}
		}
		if !strings.HasSuffix(file.Package, "/httpapi") {
			continue
		}
		ingress++
		found := false
		for _, exchange := range result.SystemFlow.Exchanges {
			for _, location := range exchange.Sources {
				found = found || exchange.Kind == "ingress" && location == endpoint.Location
			}
		}
		if !found {
			t.Error("HTTP registration omitted:", endpoint.Pattern)
		}
	}
	if ingress == 0 {
		t.Error("no real server ingress inventoried")
	}
	states := map[string]int{}
	for _, alternative := range result.SystemFlow.Alternatives {
		states[alternative.Execution]++
	}
	if states["exercised"] == 0 || states["not-observed"] == 0 {
		t.Error("execution alternatives were erased:", states)
	}
	exchanges := map[string]int{}
	for _, exchange := range result.SystemFlow.Exchanges {
		exchanges[exchange.Resolution]++
	}
	files := append(append([]review.File{}, result.Files...), result.SupportingFiles...)
	for _, source := range result.TestSources {
		files = append(files, source.File)
	}
	for _, file := range files {
		bytes, err := os.ReadFile(filepath.Join(result.Root, file.Path))
		if err != nil || fmt.Sprintf("%x", sha256.Sum256(bytes)) != file.SHA256 {
			t.Fatalf("target source changed: %s (%v)", file.Path, err)
		}
	}
	commands := len(result.Commands)
	reopened := NewReviewed(server.review)
	if len(reopened.current().Commands) != commands || reopened.reviewRunning {
		t.Error("retained browsing executed collection")
	}
	retainAcceptanceSummary(t, "builder", map[string]any{
		"review": manifest, "sourceSHA256": result.SourceSHA256, "httpRegistrations": ingress,
		"components": len(result.SystemFlow.Components), "operations": len(result.SystemFlow.Operations),
		"exchanges": exchanges, "alternatives": states,
	})
	t.Logf("%d HTTP registrations; %d components; alternatives %v; exchanges %v", ingress, len(result.SystemFlow.Components), states, exchanges)
}

func TestSystemFlowWasmAcceptance(t *testing.T) {
	root := os.Getenv("GO_REVIEW_WASM_DIR")
	if root == "" {
		t.Skip("use the named wasm selector with a workflow-handler checkout")
	}
	report, err := review.Analyze(context.Background(), review.Options{Dir: root})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, op := range report.SystemFlow.Operations {
		found = found || op.Name == "HandleMessage"
	}
	if !found {
		t.Fatal("handwritten Wasm entry operation missing")
	}
	if report.EntryPoints == nil || report.EntryPoints.Kind != "wasm" || len(report.EntryPoints.Entries) == 0 {
		t.Fatal("WIT exports were not retained as Wasm entry points")
	}
	linked := false
	for _, entry := range report.EntryPoints.Entries {
		if entry.Kind != "wasm" || entry.World == "" || len(entry.Evidence) == 0 {
			t.Fatalf("entry lacks WIT provenance: %+v", entry)
		}
		for _, symbol := range report.Symbols {
			if symbol.ID == entry.SymbolID && symbol.Name == "HandleMessage" {
				linked = true
			}
			if symbol.ID == entry.SymbolID && symbol.Name == "main" {
				t.Fatal("Wasm public entry incorrectly replaced by main")
			}
		}
	}
	if !linked {
		t.Fatalf("WIT export did not link to handwritten HandleMessage: %+v", report.EntryPoints.Entries)
	}
	service := New(report).newChatService()
	snapshot, _, err := service.snapshot("", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range report.EntryPoints.Entries {
		args, err := json.Marshal(map[string]any{"entryID": entry.ID, "limit": 100})
		if err != nil {
			t.Fatal(err)
		}
		flow, err := snapshot.query.Query(context.Background(), "flow", args)
		if err != nil {
			t.Fatalf("real Wasm entry flow: %v", err)
		}
		var projection struct {
			Nodes []struct {
				Title    string `json:"title"`
				Boundary string `json:"boundary"`
			} `json:"nodes"`
			NodePage struct {
				NextOffset *int `json:"nextOffset"`
			} `json:"nodePage"`
		}
		if err := json.Unmarshal(flow, &projection); err != nil {
			t.Fatal(err)
		}
		handoff := false
		for {
			for _, node := range projection.Nodes {
				if strings.Contains(node.Title, "Handle") && strings.Contains(strings.ToLower(node.Title), "handler") && !strings.Contains(node.Title, "HandleMessage") {
					handoff = node.Boundary != ""
				}
			}
			if projection.NodePage.NextOffset == nil {
				break
			}
			args, err = json.Marshal(map[string]any{"entryID": entry.ID, "limit": 100, "offset": *projection.NodePage.NextOffset})
			if err != nil {
				t.Fatal(err)
			}
			flow, err = snapshot.query.Query(context.Background(), "flow", args)
			if err != nil {
				t.Fatal(err)
			}
			projection.NodePage.NextOffset = nil
			if err := json.Unmarshal(flow, &projection); err != nil {
				t.Fatal(err)
			}
		}
		if !handoff {
			t.Fatalf("Wasm flow hides the external Builder handler implementation: %s", flow)
		}
	}
	retainAcceptanceSummary(t, "wasm-entry-review", &review.RepositoryReview{
		Root: report.Scope.Root, Status: "static-only", Scope: report.Scope,
		Modules: []review.ReviewedModule{{Directory: report.Root, Report: report}},
	})
	// The candidates decodeEvent/prepareDeploymentHandler were inspected, but
	// the package also links generated async Wasm imports at initialization.
	// Running a native test or deploying the component is not a substitute.
	retainAcceptanceSummary(t, "wasm", map[string]any{
		"sourceSHA256": report.SourceSHA256, "components": len(report.SystemFlow.Components),
		"operations": len(report.SystemFlow.Operations), "warnings": report.Warnings, "entryPoints": report.EntryPoints,
		"unitEvidence": "Not collected: generated async Wasm imports require a component-aware unit-test harness; no native execution or deployment attempted.",
	})
	t.Logf("Wasm source: %d components, %d operations. Component-aware unit execution not collected.", len(report.SystemFlow.Components), len(report.SystemFlow.Operations))
}

func retainAcceptanceSummary(t *testing.T, name string, value any) {
	t.Helper()
	dir := os.Getenv("GO_REVIEW_ACCEPTANCE_DIR")
	if dir == "" {
		t.Fatal("GO_REVIEW_ACCEPTANCE_DIR is required for persistent acceptance evidence")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err == nil {
		err = os.WriteFile(filepath.Join(dir, name+"-acceptance.json"), data, 0600)
	}
	if err != nil {
		t.Fatal(err)
	}
}
