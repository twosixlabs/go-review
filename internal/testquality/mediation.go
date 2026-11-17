package testquality

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/russellbrasser/go-review/internal/intelligence"
)

func citationReferences(a *Assessment, seen []Citation) map[string]Citation {
	references := map[string]Citation{}
	for i, citation := range seen {
		prefix := "source"
		key := citation.SnapshotID + "\n" + citation.FileID
		if a.factsIndex != nil {
			if file, ok := a.factsIndex.sources[key]; ok && !strings.HasSuffix(file.Path, ".go") {
				prefix = "contract"
			}
			for _, comment := range a.factsIndex.comments[key] {
				if comment.StartLine <= citation.EndLine && comment.EndLine >= citation.StartLine {
					prefix = "contract"
				}
			}
		}
		if prefix == "source" {
			for _, item := range a.Items {
				if item.Declaration.SnapshotID == citation.SnapshotID && item.Declaration.FileID == citation.FileID {
					prefix = "test"
				}
			}
		}
		references[fmt.Sprintf("%s-%d", prefix, i+1)] = citation
	}
	checks := 0
	for _, fact := range a.Facts {
		for _, check := range fact.Checks {
			if checkedCitations([]Citation{check.Evidence}, seen) == nil {
				checks++
				references[fmt.Sprintf("check-%d", checks)] = check.Evidence
			}
		}
	}
	return references
}

func referenceSchema(raw json.RawMessage, references map[string]Citation) (json.RawMessage, error) {
	var schema any
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, err
	}
	ids := []string{}
	for id := range references {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	basisIDs := []string{}
	for _, id := range ids {
		if strings.HasPrefix(id, "contract-") || strings.HasPrefix(id, "source-") {
			basisIDs = append(basisIDs, id)
		}
	}
	var bind func(any) any
	bind = func(value any) any {
		switch node := value.(type) {
		case map[string]any:
			if properties, ok := node["properties"].(map[string]any); ok && len(properties) == 4 && properties["snapshotID"] != nil && properties["fileID"] != nil && properties["startLine"] != nil && properties["endLine"] != nil {
				if len(ids) == 0 {
					return map[string]any{"type": "string"}
				}
				return map[string]any{"type": "string", "enum": ids}
			}
			for key, child := range node {
				node[key] = bind(child)
			}
			if properties, ok := node["properties"].(map[string]any); ok && properties["basis"] != nil && properties["conditions"] != nil {
				// Expectation evidence establishes the contract/source basis;
				// contributing checks are cited on its supported conditions.
				evidence := properties["evidence"].(map[string]any)
				if len(basisIDs) > 0 {
					evidence["items"] = map[string]any{"type": "string", "enum": basisIDs}
					evidence["minItems"] = 1
				} else {
					evidence["maxItems"] = 0
				}
			}
		case []any:
			for i, child := range node {
				node[i] = bind(child)
			}
		}
		return value
	}
	return json.Marshal(bind(schema))
}

func expandCitationReferences(raw json.RawMessage, references map[string]Citation) (json.RawMessage, error) {
	var proposal any
	if err := json.Unmarshal(raw, &proposal); err != nil {
		var syntax *json.SyntaxError
		if errors.As(err, &syntax) {
			offset := int(syntax.Offset)
			return nil, fmt.Errorf("assessment JSON at byte %d near %q: %w", offset, raw[max(0, offset-60):min(len(raw), offset+20)], err)
		}
		return nil, err
	}
	var expand func(any) error
	expand = func(value any) error {
		switch node := value.(type) {
		case map[string]any:
			for key, child := range node {
				if key == "evidence" {
					ids, ok := child.([]any)
					if !ok {
						return fmt.Errorf("evidence requires a reference array")
					}
					citations := []Citation{}
					for _, entry := range ids {
						id, ok := entry.(string)
						if !ok {
							return fmt.Errorf("evidence requires short source references")
						}
						citation, ok := references[id]
						if !ok {
							return fmt.Errorf("unknown inspected-source reference %q", id)
						}
						citations = append(citations, citation)
					}
					node[key] = citations
				} else if err := expand(child); err != nil {
					return err
				}
			}
		case []any:
			for _, child := range node {
				if err := expand(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := expand(proposal); err != nil {
		return nil, err
	}
	return json.Marshal(proposal)
}

type modelRating struct {
	Rating
	Value *string `json:"value,omitempty"`
}

func submittedRatings(proposals []modelRating) ([]Rating, error) {
	ratings := []Rating{}
	for _, proposal := range proposals {
		rating := proposal.Rating
		if proposal.Value == nil {
			return nil, fmt.Errorf("%s: rubric label is required", rating.Dimension)
		}
		rating.Value = nil
		if *proposal.Value == "unknown" {
			rating.State = RatingUnknown
		} else if *proposal.Value == "not-applicable" {
			rating.State = RatingNotApplicable
		} else {
			var value int
			switch *proposal.Value {
			case "absent":
				value = 0
			case "weak":
				value = 1
			case "material-issues":
				value = 2
			case "strong":
				value = 3
			case "convincing":
				value = 4
			default:
				return nil, fmt.Errorf("%s: unknown rubric label %q", rating.Dimension, *proposal.Value)
			}
			rating.State = RatingAssessed
			rating.Value = &value
		}
		ratings = append(ratings, rating)
	}
	return ratings, nil
}

func derivedJudgmentStatus(ratings []Rating, priorReason string) (Status, string) {
	if len(ratings) == 0 {
		if priorReason == "" {
			priorReason = "Required judgment dimensions were not supplied."
		}
		return StatusInsufficient, priorReason
	}
	var reasons []string
	for _, rating := range ratings {
		if rating.State == RatingUnknown {
			reasons = append(reasons, string(rating.Dimension)+": "+rating.Rationale)
		}
	}
	if len(reasons) > 0 {
		return StatusInsufficient, strings.Join(reasons, "; ")
	}
	return StatusScored, ""
}

// The job owns the evidence session and its lifetime. Reads return only
// citations for source actually supplied, not retrieval candidates.
type JudgmentOptions struct {
	Retrieve RetrievalFunc
	Tools    []intelligence.Tool
	Read     func(context.Context, string, json.RawMessage) (json.RawMessage, []Citation, error)
	Trace    func(ReviewTrace) error
	// Finish validates required deliverables. When set, the native primary
	// conversation can end successfully only through finish_review.
	Finish func() error
	// The job owner checkpoints completed collective groups before synthesis.
	CheckpointSuite func(SuiteAssessment) error
}

type ReviewTrace struct {
	Kind     string                 `json:"kind"`
	Status   string                 `json:"status,omitempty"`
	Step     int                    `json:"step"`
	Submit   string                 `json:"submit"`
	Messages []intelligence.Message `json:"messages,omitempty"`
	Tools    []intelligence.Tool    `json:"tools,omitempty"`
	Answer   *intelligence.Message  `json:"answer,omitempty"`
	Tool     string                 `json:"tool,omitempty"`
	Args     json.RawMessage        `json:"args,omitempty"`
	Result   json.RawMessage        `json:"result,omitempty"`
	Error    string                 `json:"error,omitempty"`
}
