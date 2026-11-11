package workbench

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/russellbrasser/go-review/internal/intelligence"
	"github.com/russellbrasser/go-review/internal/review"
)

// These opt-in checks exercise the actual model and MCP, not prompt wording.
func TestAgentPromptLive(t *testing.T) {
	base := os.Getenv("GO_REVIEW_AGENT_URL")
	if base == "" {
		t.Skip("opt-in local prompt evaluation")
	}
	config := intelligence.Config{Enabled: true, BaseURL: base, Model: os.Getenv("GO_REVIEW_AGENT_MODEL")}
	for _, scenario := range []struct {
		name, question string
		want           []string
		maxTools       int
	}{
		{
			"purpose",
			"What does this project do, and how does its main flow work?",
			[]string{"reservation", "inventory", "[document:purpose]"},
			5,
		},
		{
			"scoped-complexity",
			"The application maximum complexity is 43 but the handler overview maximum is 9. Find the owner of 43 and explain whether the application gauge means lines of code instead. Investigate rather than guessing.",
			[]string{"43", "9", "Validate", "[symbol:validator]"},
			8,
		},
		{
			"unavailable-symbol",
			"Assess the cyclomatic complexity of RetryShipment. If it is not retained here, say so rather than guessing.",
			[]string{"RetryShipment"},
			5,
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			report := &review.Report{
				Module:   "example.com/reservations",
				Packages: []review.Package{{Path: "example.com/reservations", Name: "reservations"}},
				Documents: []review.Document{{
					ID: "purpose", Path: "README.md",
					Text: "This reservation service receives HTTP reservation requests, validates inventory availability, and persists confirmed reservations. Inventory is owned by the inventory package. Validation failures return errors without committing a reservation.",
				}},
				Symbols: []review.Symbol{
					{ID: "handler", Name: "Reserve", Qualified: "api.Reserve", Package: "example.com/reservations", Kind: "function", Cyclomatic: 9},
					{ID: "validator", Name: "Validate", Qualified: "inventory.State.Validate", Signature: "func (s State) Validate() error", Package: "example.com/reservations", Kind: "function", Cyclomatic: 43},
				},
				HTTPEndpoints: []review.HTTPEndpoint{{ID: "reserve", HandlerID: "handler", Pattern: "POST /reservations", PatternKnown: true}},
			}
			server := New(report)
			service := server.newChatService()
			snapshot, _, err := service.snapshot("", "")
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			var answer strings.Builder
			var tools []string
			seen := map[string]int{}
			err = server.runChat(ctx, config, chatRequest{Message: scenario.question}, snapshot, service, func(event chatEvent) error {
				switch event.Type {
				case "tool":
					args, _ := json.Marshal(event.Args)
					key := event.Tool + string(args)
					seen[key]++
					if seen[key] > 2 {
						t.Errorf("repeated unproductive query: %s", key)
					}
					tools = append(tools, event.Tool)
				case "text":
					answer.WriteString(event.Text)
				}
				if event.Navigation != nil {
					t.Error("ordinary evidence question unexpectedly proposed navigation")
				}
				return nil
			})
			t.Logf("tools=%v\nanswer=%s", tools, answer.String())
			if err != nil {
				t.Fatal(err)
			}
			if len(tools) > scenario.maxTools {
				t.Errorf("used %d tools, budget %d", len(tools), scenario.maxTools)
			}
			for _, text := range scenario.want {
				if !strings.Contains(strings.ToLower(answer.String()), strings.ToLower(text)) {
					t.Errorf("missing supported answer detail %q", text)
				}
			}
			if scenario.name == "scoped-complexity" && !strings.Contains(strings.Join(tools, ","), "review_evidence") {
				t.Error("did not verify the maximum owner's own metric")
			}
			if scenario.name == "unavailable-symbol" {
				lower := strings.ToLower(answer.String())
				if !strings.Contains(lower, "not") && !strings.Contains(lower, "unavailable") && !strings.Contains(lower, "cannot") {
					t.Error("did not acknowledge missing evidence")
				}
			}
		})
	}
}
