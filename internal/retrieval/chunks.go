// Package retrieval builds source-cited candidates, never test-quality judgments.
package retrieval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/russellbrasser/go-review/internal/review"
)

type Chunk struct {
	ID           string   `json:"id"`
	Kind         string   `json:"kind"`
	EntityID     string   `json:"entityID"`
	Name         string   `json:"name"`
	Path         string   `json:"path"`
	FileID       string   `json:"fileID,omitempty"`
	DocumentID   string   `json:"documentID,omitempty"`
	Package      string   `json:"package,omitempty"`
	StartLine    int      `json:"startLine"`
	EndLine      int      `json:"endLine"`
	SourceSHA256 string   `json:"sourceSHA256"`
	InputSHA256  string   `json:"inputSHA256"`
	Text         string   `json:"-"`
	CallNames    []string `json:"-"`
}

type sourceUnit struct {
	File     review.File
	Symbols  []review.Symbol
	Tests    []review.TestDeclaration
	Excluded []review.Location
	Document bool
}

func digest(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func sourceUnits(r *review.Report) []sourceUnit {
	units := make([]sourceUnit, 0, len(r.Files)+len(r.TestSources)+len(r.Documents))
	byID := make(map[string]int)
	for _, f := range r.Files {
		byID[f.ID] = len(units)
		units = append(units, sourceUnit{File: f})
	}
	for _, s := range r.Symbols {
		if index, ok := byID[s.FileID]; ok {
			units[index].Symbols = append(units[index].Symbols, s)
		}
	}
	for _, t := range r.TestSources {
		units = append(units, sourceUnit{File: t.File, Tests: t.Declarations, Excluded: t.ExcludedRanges})
	}
	documents := r.Documents
	if r.EntryPoints != nil {
		documents = append(append([]review.Document{}, documents...), r.EntryPoints.Sources...)
	}
	for _, d := range documents {
		units = append(units, sourceUnit{File: review.File{ID: d.ID, Path: d.Path, Source: d.Text, SHA256: d.SHA256}, Document: true})
	}
	return units
}

func identifierWords(name string) string {
	runes := []rune(name)
	var out strings.Builder
	for i, r := range runes {
		if r == '_' || r == '.' || r == '/' {
			out.WriteByte(' ')
			continue
		}
		if i > 0 && unicode.IsUpper(r) && (unicode.IsLower(runes[i-1]) || i+1 < len(runes) && unicode.IsLower(runes[i+1]) && unicode.IsUpper(runes[i-1])) {
			out.WriteByte(' ')
		}
		out.WriteRune(r)
	}
	return out.String()
}

func prefixBytes(text string, limit int) string {
	end := min(len(text), limit)
	for end > 0 && !utf8.ValidString(text[:end]) {
		end--
	}
	return text[:end]
}

// Chunks excludes retained-only supporting files and excluded test declarations
// before producing any lexical or embedding input. Citations remain original.
func Chunks(ctx context.Context, r *review.Report, inputBytes int) ([]Chunk, error) {
	if r == nil || inputBytes < 128 {
		return nil, fmt.Errorf("retained report and an input budget of at least 128 bytes are required")
	}
	out := []Chunk{}
	for _, unit := range sourceUnits(r) {
		chunks, err := unitChunks(ctx, unit, inputBytes)
		if err != nil {
			return nil, err
		}
		out = append(out, chunks...)
	}
	return out, nil
}

func unitChunks(ctx context.Context, unit sourceUnit, budget int) ([]Chunk, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file := unit.File
	hash := digest(file.Source)
	if file.SHA256 != "" && file.SHA256 != hash {
		return nil, fmt.Errorf("retained source hash mismatch: %s", file.Path)
	}
	if file.Source == "" {
		return []Chunk{}, nil
	}
	if !utf8.ValidString(file.Source) {
		return nil, fmt.Errorf("retained source is not UTF-8: %s", file.Path)
	}
	out := []Chunk{}
	emit := func(kind, entity, name, body, note string, startLine int) {
		// Metadata is a bounded structural search view, not an original-source
		// quotation or an inferred behavioral contract.
		header := prefixBytes(kind+" "+identifierWords(name)+" "+file.Package+"\n"+note+"\n", min(128, budget/3))
		remaining := body
		line := startLine
		for remaining != "" {
			content := prefixBytes(remaining, budget-len(header))
			if end := strings.LastIndex(content, "\n"); end >= len(content)/2 && len(content) < len(remaining) {
				content = content[:end+1]
			}
			// Source readers admit at most 500 lines; keep individual candidates
			// smaller even when a file contains many very short/blank lines.
			if strings.Count(content, "\n") > 199 {
				end := 0
				for count := 0; count < 199; count++ {
					end += strings.IndexByte(content[end:], '\n') + 1
				}
				content = content[:end]
			}
			endLine := line + strings.Count(strings.TrimSuffix(content, "\n"), "\n")
			text := header + content
			c := Chunk{Kind: kind, EntityID: entity, Name: name, Path: file.Path, Package: file.Package,
				StartLine: line, EndLine: endLine, SourceSHA256: hash, InputSHA256: digest(text), Text: text}
			if unit.Document {
				c.DocumentID = file.ID
			} else {
				c.FileID = file.ID
			}
			c.ID = digest(fmt.Sprintf("%s:%s:%d:%d:%s", file.ID, entity, line, endLine, c.InputSHA256))
			out = append(out, c)
			line += strings.Count(content, "\n")
			remaining = remaining[len(content):]
			if remaining != "" && strings.HasSuffix(content, "\n") {
				trimmed := strings.TrimSuffix(content, "\n")
				overlap := trimmed[strings.LastIndex(trimmed, "\n")+1:] + "\n"
				if len(overlap) > 1 && len(overlap) <= 64 && len(overlap) < len(content) {
					remaining = overlap + remaining
					line--
				}
			}
		}
	}
	if unit.Document {
		lines := strings.SplitAfter(file.Source, "\n")
		start := 0
		fence := ""
		for i := 0; i <= len(lines); i++ {
			heading := false
			if i < len(lines) {
				line := strings.TrimSpace(lines[i])
				if strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") {
					marker := line[:3]
					if fence == "" {
						fence = marker
					} else if fence == marker {
						fence = ""
					}
				} else if fence == "" {
					count := len(line) - len(strings.TrimLeft(line, "#"))
					heading = count > 0 && count <= 6 && (count == len(line) || line[count] == ' ')
				}
			}
			if i == len(lines) || heading {
				section := strings.Join(lines[start:i], "")
				if strings.TrimSpace(section) != "" {
					emit("document", file.ID, file.Path, section, "", start+1)
				}
				start = i
			}
		}
		return out, nil
	}
	fset := token.NewFileSet()
	tree, err := parser.ParseFile(fset, file.Path, file.Source, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse retained source %s: %w", file.Path, err)
	}
	for _, decl := range tree.Decls {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		start, end := fset.Position(decl.Pos()), fset.Position(decl.End())
		excluded := false
		for _, location := range unit.Excluded {
			if location.Line < 1 || location.EndLine < location.Line {
				return nil, fmt.Errorf("invalid retained exclusion in %s", file.Path)
			}
			if start.Line <= location.EndLine && end.Line >= location.Line {
				excluded = true
			}
		}
		if excluded {
			continue
		}
		name, kind := file.Path, "source"
		isFunction := false
		if fn, ok := decl.(*ast.FuncDecl); ok {
			isFunction = true
			name = fn.Name.Name
			if fn.Doc != nil {
				start = fset.Position(fn.Doc.Pos())
			}
		}
		entity := fmt.Sprintf("%s:%d", file.ID, start.Line)
		for _, s := range unit.Symbols {
			if s.Line >= start.Line && s.Line <= end.Line && (s.Name == name || !isFunction) {
				entity, name, kind = s.ID, s.Name, "symbol"
				break
			}
		}
		for _, t := range unit.Tests {
			if t.Name == name {
				entity, kind = t.ID, t.Kind
				break
			}
		}
		words := map[string]bool{}
		calls := map[string]bool{}
		ast.Inspect(decl, func(node ast.Node) bool {
			switch n := node.(type) {
			case *ast.Ident:
				words[identifierWords(n.Name)] = true
			case *ast.BinaryExpr:
				words["contains comparison/operator "+n.Op.String()] = true
			case *ast.BasicLit:
				if n.Kind == token.INT || n.Kind == token.FLOAT {
					words["literal "+n.Value] = true
				}
			case *ast.CallExpr:
				if identifier, ok := n.Fun.(*ast.Ident); ok {
					calls[identifier.Name] = true
				}
			}
			return true
		})
		sorted := make([]string, 0, len(words))
		for word := range words {
			sorted = append(sorted, word)
		}
		sort.Strings(sorted)
		callNames := make([]string, 0, len(calls))
		for call := range calls {
			callNames = append(callNames, call)
		}
		sort.Strings(callNames)
		before := len(out)
		emit(kind, entity, name, file.Source[start.Offset:end.Offset], "Observed identifiers/operators (bounded): "+strings.Join(sorted, "; "), start.Line)
		for i := before; i < len(out); i++ {
			out[i].CallNames = callNames
		}
	}
	return out, nil
}
