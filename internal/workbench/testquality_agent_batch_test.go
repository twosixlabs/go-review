package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/russellbrasser/go-review/internal/intelligence"
	"github.com/russellbrasser/go-review/internal/review"
	"github.com/russellbrasser/go-review/internal/testquality"
)

func TestQualityAgentBatchPagesFiltersAndRecordsFromReturnedSource(t *testing.T) {
	s := qualityFixture(t)
	source := &s.review.Modules[0].Report.TestSources[0]
	source.File.Source = "package example\nimport \"testing\"\nfunc TestF(t *testing.T) {}\nfunc TestG(t *testing.T) {}\nfunc TestOther(t *testing.T) {}\n"
	source.File.SHA256 = testquality.Digest([]byte(source.File.Source))
	source.Declarations = []review.TestDeclaration{
		{ID: "TestF", Name: "TestF", Kind: "test", Location: review.Location{FileID: source.File.ID, Line: 3, EndLine: 3}},
		{ID: "TestG", Name: "TestG", Kind: "test", Location: review.Location{FileID: source.File.ID, Line: 4, EndLine: 4}},
		{ID: "TestOther", Name: "TestOther", Kind: "test", Location: review.Location{FileID: source.File.ID, Line: 5, EndLine: 5}},
	}
	a, err := s.qualityStore.Create(t.Context(), s.review, "", "")
	if err != nil {
		t.Fatal(err)
	}
	item := a.Items[0]
	for _, selected := range a.Items {
		if selected.Name == "TestF" {
			item = selected
		}
	}
	requests := 0
	cfg := qualityAgentProvider(t, func(w http.ResponseWriter, _ *http.Request, request qualityAgentRequest) {
		requests++
		switch requests {
		case 1:
			args, _ := json.Marshal(map[string]any{"calls": []any{
				map[string]any{"tool": "review_tests", "arguments": map[string]any{"snapshotID": item.Identity.SnapshotID, "limit": 1}, "allPages": true, "groupBy": "package"},
				map[string]any{"tool": "review_tests", "arguments": map[string]any{"snapshotID": item.Identity.SnapshotID, "limit": 1}, "allPages": true, "match": map[string]string{"field": "name", "contains": "TestF"}, "fields": []string{"identity", "name", "declaration"}},
				map[string]any{"tool": "review_source", "arguments": qualityFixtureRead(item.Declaration).args},
			}})
			qualityAgentAnswer(w, qualityAgentCall("batch", "review_batch", string(args)))
		case 2:
			var batch struct {
				Results []struct {
					Result struct {
						Groups map[string]int
						Items  []struct {
							Identity testquality.Identity
							Name     string
						}
					}
				}
			}
			last := request.Messages[len(request.Messages)-1].Content
			if err := json.Unmarshal([]byte(last), &batch); err != nil || len(batch.Results) != 3 {
				t.Errorf("compound evidence was not returned in one model turn: %s %v", last, err)
			} else if batch.Results[0].Result.Groups["example"] != 3 || len(batch.Results[1].Result.Items) != 1 ||
				batch.Results[1].Result.Items[0].Identity != item.Identity || batch.Results[1].Result.Items[0].Name != "TestF" {
				t.Errorf("pagination/filtering/grouping lost the selected identities: %s", last)
			}
			qualityFixtureRespond(w, qualityFixtureTest(item, 3, 2, "Inspected the exact selected test."))
		default:
			qualityAgentAnswer(w)
		}
	})
	p, err := newQualityPrimary(t.Context(), s, a, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	options := p.options
	options.Finish = nil
	if err := runQualityAgent(t.Context(), cfg, "Review the selected test.", options, func(name string, args json.RawMessage, seen []testquality.Citation) error {
		return p.record(p.items, true, name, args, seen)
	}); err != nil {
		t.Fatal(err)
	}
	saved, err := s.qualityStore.Load(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	scored := 0
	for _, selected := range saved.Items {
		if selected.Status == testquality.StatusScored {
			scored++
		}
	}
	if requests != 3 || scored != 1 {
		t.Fatalf("batch source did not support immediate persistence: requests=%d scored=%d", requests, scored)
	}
}

func TestQualityAgentBatchCannotExecuteOrGrantHiddenSourceAuthority(t *testing.T) {
	for _, mode := range []string{"execution", "hidden-source", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			requests, executions := 0, 0
			cfg := qualityAgentProvider(t, func(w http.ResponseWriter, _ *http.Request, request qualityAgentRequest) {
				requests++
				if requests == 1 {
					args := `{"calls":[{"tool":"run_test_diagnostic","arguments":{}}]}`
					if mode == "hidden-source" {
						args = `{"calls":[{"tool":"review_source","arguments":{},"fields":["fileID"]}]}`
					} else if mode == "oversized" {
						args = `{"calls":[{"tool":"review_source","arguments":{}}]}`
					}

					qualityAgentAnswer(w, qualityAgentCall("batch", "review_batch", args))
				} else if requests == 2 {
					last := request.Messages[len(request.Messages)-1].Content
					if !strings.Contains(last, `"error"`) {
						t.Errorf("unsafe/oversized batch did not expose its failure: %s", last)
					}
					qualityAgentAnswer(w, qualityAgentCall("record", "record_test", qualityAgentTestArgs))
				} else {
					qualityAgentAnswer(w)
				}
			})
			cfg.ContextBytes = 32768
			options := qualityAgentOptions()
			options.Tools = append(options.Tools, intelligence.Tool{Type: "function", Function: intelligence.Function{Name: "run_test_diagnostic"}})
			options.Read = func(_ context.Context, name string, _ json.RawMessage) (json.RawMessage, []testquality.Citation, error) {
				if name == "run_test_diagnostic" {
					executions++
				}
				raw, _ := json.Marshal(strings.Repeat("x", 20000))
				return raw, []testquality.Citation{{SnapshotID: "hidden"}}, nil
			}
			err := runQualityAgent(t.Context(), cfg, "Review.", options, func(_ string, _ json.RawMessage, seen []testquality.Citation) error {
				if len(seen) != 0 {
					t.Errorf("unreturned source authorized a judgment: %+v", seen)
				}
				return nil
			})
			if err != nil || executions != 0 {
				t.Fatalf("batch escaped read-only boundaries: executions=%d error=%v", executions, err)
			}
		})
	}
}

func TestQualityAgentBatchRejectsStalledPagesAndDoesNotLeakPartialSource(t *testing.T) {
	t.Run("stalled-pagination", func(t *testing.T) {
		options := qualityAgentOptions()
		options.Read = func(context.Context, string, json.RawMessage) (json.RawMessage, []testquality.Citation, error) {
			return json.RawMessage(`{"items":[{"name":"TestF"}],"nextOffset":0}`), nil, nil
		}
		_, seen, err := qualityEvidenceBatch(t.Context(), json.RawMessage(`{"calls":[{"tool":"review_tests","arguments":{},"allPages":true}]}`),
			options, map[string]bool{"review_tests": true}, 8192)
		if err == nil || !strings.Contains(err.Error(), "does not advance") || len(seen) != 0 {
			t.Fatalf("batch did not stop a nonadvancing inventory: citations=%v error=%v", seen, err)
		}
	})
	t.Run("partial-read-failure", func(t *testing.T) {
		failure := errors.New("retained source unavailable")
		reads := 0
		options := qualityAgentOptions()
		options.Read = func(context.Context, string, json.RawMessage) (json.RawMessage, []testquality.Citation, error) {
			reads++
			if reads == 2 {
				return nil, nil, failure
			}
			return json.RawMessage(`{"text":"not delivered"}`), []testquality.Citation{{SnapshotID: "hidden"}}, nil
		}
		raw, seen, err := qualityEvidenceBatch(t.Context(), json.RawMessage(`{"calls":[{"tool":"review_source","arguments":{}},{"tool":"review_source","arguments":{}}]}`),
			options, map[string]bool{"review_source": true}, 8192)
		if !errors.Is(err, failure) || len(seen) != 0 || len(raw) != 0 || reads != 2 {
			t.Fatalf("failed batch hid its cause or admitted unreturned source: result=%s citations=%v error=%v", raw, seen, err)
		}
	})
}
