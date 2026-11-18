package workbench

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/russellbrasser/go-review/internal/intelligence"
	"github.com/russellbrasser/go-review/internal/testquality"
)

func qualityTextCitation(snapshotID, fileID string, base int, chars []rune, start, end int) *testquality.Citation {
	startLine := base + strings.Count(string(chars[:start]), "\n")
	if start > 0 && chars[start-1] != '\n' {
		startLine++
	}
	endLine := base + strings.Count(string(chars[:end]), "\n")
	if end < len(chars) {
		endLine--
	}
	if startLine > endLine {
		return nil
	}
	return &testquality.Citation{SnapshotID: snapshotID, FileID: fileID, StartLine: startLine, EndLine: endLine}
}

// Reassemble only successful source pages still present in the conversation.
// A page boundary through a line must not leave a permanent inspection gap,
// but dropped pages, unread fragments and changed live source grant no authority.
func qualityAgentPagedCitations(messages []intelligence.Message) []testquality.Citation {
	type key struct {
		snapshot, file   string
		start, end, size int
	}
	type page struct {
		offset int
		text   []rune
	}
	pages := map[key][]page{}
	add := func(name string, args, result json.RawMessage) {
		if name != "review_source" && name != "review_read_file" && name != "review_document" {
			return
		}
		var input struct{ SnapshotID string }
		var output struct {
			Data struct {
				FileID, ID, Text   string
				StartLine, EndLine int
				SnapshotMatch      bool
				TextPage           *struct{ Offset, Total int }
			}
		}
		// Non-source results and failed tool calls have no source page.
		if json.Unmarshal(args, &input) != nil || json.Unmarshal(result, &output) != nil || output.Data.TextPage == nil {
			return
		}
		data := output.Data
		if name == "review_read_file" && !data.SnapshotMatch {
			return
		}
		if name == "review_document" {
			data.FileID, data.StartLine = data.ID, 1
		}
		part := page{offset: data.TextPage.Offset, text: []rune(data.Text)}
		if input.SnapshotID == "" || data.FileID == "" || data.StartLine < 1 ||
			part.offset < 0 || data.TextPage.Total < 1 || part.offset+len(part.text) > data.TextPage.Total {
			return
		}
		k := key{input.SnapshotID, data.FileID, data.StartLine, data.EndLine, data.TextPage.Total}
		pages[k] = append(pages[k], part)
	}
	calls := map[string]intelligence.ToolCall{}
	for _, message := range messages {
		if message.Role == "assistant" {
			for _, call := range message.ToolCalls {
				calls[call.ID] = call
			}
		}
		if message.Role != "tool" {
			continue
		}
		call := calls[message.ToolCallID]
		if call.Function.Name != "review_batch" {
			add(call.Function.Name, json.RawMessage(call.Function.Arguments), json.RawMessage(message.Content))
			continue
		}
		var input struct{ Calls []qualityEvidenceCall }
		var output struct {
			Results []struct {
				Tool   string
				Result json.RawMessage
			}
		}
		if json.Unmarshal([]byte(call.Function.Arguments), &input) != nil ||
			json.Unmarshal([]byte(message.Content), &output) != nil || len(input.Calls) != len(output.Results) {
			continue
		}
		for i, entry := range input.Calls {
			if entry.Tool == output.Results[i].Tool {
				add(entry.Tool, entry.Arguments, output.Results[i].Result)
			}
		}
	}
	var citations []testquality.Citation
	for k, parts := range pages {
		sort.Slice(parts, func(i, j int) bool { return parts[i].offset < parts[j].offset })
		next, lines := 0, 0
		for _, part := range parts {
			if part.offset > next {
				break
			}
			if end := part.offset + len(part.text); end > next {
				lines += strings.Count(string(part.text[next-part.offset:]), "\n")
				next = end
			}
		}
		if next == k.size {
			citations = append(citations, testquality.Citation{SnapshotID: k.snapshot, FileID: k.file, StartLine: k.start, EndLine: k.start + lines})
		}
	}
	return citations
}
