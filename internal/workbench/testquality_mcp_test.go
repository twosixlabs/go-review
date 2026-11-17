package workbench

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/russellbrasser/go-review/internal/review"
	"github.com/russellbrasser/go-review/internal/testquality"
)

func TestQualityMCPPreparedRetrievalReturnsCandidatesWithoutCitationAuthority(t *testing.T) {
	report := &review.Report{Module: "example"}
	a := &testquality.Assessment{Reports: []testquality.PinnedReport{{SnapshotID: "owned", Report: report}}}
	options, closeSession, err := New(&review.Report{Module: "unrelated"}).qualityEvidence(t.Context(), a,
		func(_ context.Context, selected *review.Report, snapshotID string, args json.RawMessage) (json.RawMessage, error) {
			if selected != report || snapshotID != "owned" {
				return nil, fmt.Errorf("prepared retrieval escaped assessment ownership")
			}
			return json.RawMessage(`{"items":[{"entityID":"candidate","text":"matching code"}]}`), nil
		})
	if err != nil {
		t.Fatal(err)
	}
	defer closeSession()
	raw, citations, err := options.Read(t.Context(), "quality_retrieve", json.RawMessage(`{"snapshotID":"owned","query":"matching code"}`))
	if err != nil || !strings.Contains(string(raw), `"entityID":"candidate"`) || len(citations) != 0 {
		t.Fatalf("prepared retrieval lost candidates or granted source authority: %s %+v %v", raw, citations, err)
	}
	if _, _, err := options.Read(t.Context(), "quality_retrieve", json.RawMessage(`{"snapshotID":"unrelated","query":"matching code"}`)); err == nil {
		t.Fatal("prepared retrieval accepted an unowned snapshot")
	}
}

func TestQualityMCPReadsRetainedFreshDiagnosticAttempts(t *testing.T) {
	a := &testquality.Assessment{ID: "quality", Version: testquality.EvidenceRubricVersion,
		Reports: []testquality.PinnedReport{{SnapshotID: "owned", Report: &review.Report{Module: "example"}}},
		Diagnostics: []testquality.DiagnosticRecord{{
			Status: "partial", SourceVerified: true,
			Runs: []review.UnitTestRun{{
				Attempts: []review.UnitTestAttempt{{Test: "example::TestF", Attempt: 2, Outcome: "fail"}},
				Commands: []review.Command{{Stdout: "retained fresh command output"}},
			}},
		}},
	}
	options, closeSession, err := New(&review.Report{Module: "unrelated"}).qualityEvidence(t.Context(), a, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeSession()
	raw, citations, err := options.Read(t.Context(), "quality_diagnostics", json.RawMessage(`{"snapshotID":"owned","operation":"diagnostic-attempts","diagnosticIndex":0,"runIndex":0}`))
	if err != nil || !strings.Contains(string(raw), `"outcome":"fail"`) || len(citations) != 0 {
		t.Fatalf("fresh attempts must remain inspectable without source citation authority: %s %+v %v", raw, citations, err)
	}
	raw, citations, err = options.Read(t.Context(), "quality_diagnostics", json.RawMessage(`{"snapshotID":"owned","operation":"diagnostic-commands","diagnosticIndex":0,"runIndex":0}`))
	if err != nil || !strings.Contains(string(raw), "retained fresh command output") || len(citations) != 0 {
		t.Fatalf("raw command evidence is not available on demand: %s %+v %v", raw, citations, err)
	}
	for _, args := range []string{
		`{"snapshotID":"unrelated","operation":"diagnostic-attempts","diagnosticIndex":0,"runIndex":0}`,
		`{"snapshotID":"owned","operation":"source"}`,
	} {
		if _, _, err := options.Read(t.Context(), "quality_diagnostics", json.RawMessage(args)); err == nil {
			t.Fatalf("invalid diagnostic read accepted: %s", args)
		}
	}
}

func TestQualityMCPOverviewSelectsPinnedSnapshot(t *testing.T) {
	a := &testquality.Assessment{Reports: []testquality.PinnedReport{
		{SnapshotID: "owned", Report: &review.Report{Module: "example"}},
		{SnapshotID: "other", Report: &review.Report{Module: "other"}},
	}}
	options, closeSession, err := New(&review.Report{Module: "unrelated"}).qualityEvidence(t.Context(), a, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeSession()
	raw, citations, err := options.Read(t.Context(), "review_overview", json.RawMessage(`{"snapshotID":"owned"}`))
	if err != nil || len(citations) != 0 || !strings.Contains(string(raw), `"module":"example"`) {
		t.Fatalf("pinned overview unavailable: %s %+v %v", raw, citations, err)
	}
	if _, _, err := options.Read(t.Context(), "review_overview", json.RawMessage(`{"snapshotID":"unrelated"}`)); err == nil {
		t.Fatal("overview accepted an unowned snapshot")
	}
}

func TestQualityMCPDiscoversSelectedTestsByPackage(t *testing.T) {
	s := qualityFixture(t)
	a, err := s.qualityStore.Create(t.Context(), s.review, "", "")
	if err != nil {
		t.Fatal(err)
	}
	options, closeSession, err := s.qualityEvidence(t.Context(), a, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeSession()
	args, _ := json.Marshal(map[string]any{"snapshotID": a.Items[0].Identity.SnapshotID, "package": "example", "limit": 1})
	raw, citations, err := options.Read(t.Context(), "review_tests", args)
	if err != nil || len(citations) != 0 {
		t.Fatalf("selected test inventory unavailable: %s %v", raw, err)
	}
	var result struct {
		Items []struct {
			Identity    testquality.Identity `json:"identity"`
			Declaration testquality.Citation `json:"declaration"`
			Path        string               `json:"path"`
		}
		Total int
	}
	if err := json.Unmarshal(raw, &result); err != nil || result.Total != 1 || result.Items[0].Identity != a.Items[0].Identity || result.Items[0].Declaration != a.Items[0].Declaration || result.Items[0].Path != "f_test.go" {
		t.Fatalf("discovery omitted exact assessment identity/source: %s %v", raw, err)
	}
	args, _ = json.Marshal(map[string]any{"snapshotID": a.Items[0].Identity.SnapshotID, "package": "other"})
	raw, _, err = options.Read(t.Context(), "review_tests", args)
	if err != nil || !strings.Contains(string(raw), `"total":0`) {
		t.Fatalf("package filter escaped selection: %s %v", raw, err)
	}
}

func TestQualityMCPPinsReadOnlyToolsToAssessment(t *testing.T) {
	source := "package example\nfunc F() int {return 1}\n"
	report := &review.Report{Module: "example", Files: []review.File{{ID: "source", Path: "sample.go", Package: "example", Source: source, SHA256: testquality.Digest([]byte(source))}}}
	document := "Contract: F returns one.\n"
	report.Documents = []review.Document{{ID: "contract", Path: "contract.md", Text: document, SHA256: testquality.Digest([]byte(document))}}
	a := &testquality.Assessment{Reports: []testquality.PinnedReport{{SnapshotID: "owned", Report: report}}}
	s := New(&review.Report{Module: "unrelated"})
	options, closeSession, err := s.qualityEvidence(t.Context(), a, nil)
	if err != nil {
		t.Fatal(err)
	}

	defer closeSession()
	found := false
	for _, tool := range options.Tools {
		switch tool.Function.Name {
		case "review_source":
			found = true
			if !strings.Contains(string(tool.Function.Parameters), "fileID") {
				t.Fatal("canonical typed source schema was lost")
			}
		case "navigate_ui", "propose_change_action", "read_evidence", "review_evidence", "review_dataflow":
			t.Fatalf("unexpected assessment authority: %s", tool.Function.Name)
		}
	}
	if !found {
		t.Fatal("real MCP source tool unavailable")
	}
	if _, _, err := options.Read(t.Context(), "review_source", json.RawMessage(`{"snapshotID":"owned","fileID":"missing"}`)); err == nil || !strings.Contains(err.Error(), "Unknown retained file ID") {
		t.Fatalf("canonical MCP diagnostic was replaced by a pointer/generic error: %v", err)
	}
	raw, citations, err := options.Read(t.Context(), "review_source", json.RawMessage(`{"snapshotID":"owned","fileID":"source","startLine":2,"endLine":2}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "return 1") || len(citations) != 1 || citations[0].SnapshotID != "owned" {
		t.Fatalf("pinned MCP source lost provenance: %s %+v", raw, citations)
	}
	if _, citations, err := options.Read(t.Context(), "review_source", json.RawMessage(`{"snapshotID":"owned","fileID":"source","startLine":2,"endLine":2,"textLimit":2}`)); err != nil || len(citations) != 0 {
		t.Fatalf("partial line became complete inspected source: %+v %v", citations, err)
	}
	if _, citations, err := options.Read(t.Context(), "review_document", json.RawMessage(`{"snapshotID":"owned","id":"contract"}`)); err != nil || len(citations) != 1 || citations[0].FileID != "contract" {
		t.Fatalf("inspected document lost provenance: %+v %v", citations, err)
	}
	for _, input := range []struct{ name, args string }{
		{"review_source", `{"snapshotID":"unrelated","fileID":"source"}`},
		{"review_source", `{"snapshotID":"owned"}`},
		{"navigate_ui", `{}`},
	} {
		if _, _, err := options.Read(t.Context(), input.name, json.RawMessage(input.args)); err == nil {
			t.Fatalf("invalid or unauthorized tool call accepted: %+v", input)
		}
	}
}
