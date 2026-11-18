package workbench

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/russellbrasser/go-review/internal/review"
	"github.com/russellbrasser/go-review/internal/testquality"
)

type qualityFileInput struct {
	SnapshotID  string `json:"snapshotID"`
	Source      string `json:"source,omitempty" jsonschema:"retained (default) or live; live reads current files within this module root"`
	Path        string `json:"path,omitempty" jsonschema:"Module-relative file/directory path; default directory is ."`
	Query       string `json:"query,omitempty" jsonschema:"Literal text to find, not a regular expression"`
	FilePattern string `json:"filePattern,omitempty" jsonschema:"Filename glob for search; defaults to *.go"`
	StartLine   int    `json:"startLine,omitempty"`
	EndLine     int    `json:"endLine,omitempty"`
	TextOffset  int    `json:"textOffset,omitempty"`
	TextLimit   int    `json:"textLimit,omitempty"`
	Offset      int    `json:"offset,omitempty"`
	Limit       int    `json:"limit,omitempty"`
}

type qualityRetainedFile struct{ id, path, text, kind string }

func qualityRetainedFiles(report *review.Report) map[string]qualityRetainedFile {
	files := map[string]qualityRetainedFile{}
	add := func(f review.File, kind string) {
		files[filepath.ToSlash(f.Path)] = qualityRetainedFile{f.ID, filepath.ToSlash(f.Path), f.Source, kind}
	}
	for _, f := range report.Files {
		add(f, "production")
	}
	for _, f := range report.SupportingFiles {
		add(f, "supporting")
	}
	for _, t := range report.TestSources {
		add(t.File, "test")
	}
	for _, d := range report.Documents {
		files[filepath.ToSlash(d.Path)] = qualityRetainedFile{d.ID, filepath.ToSlash(d.Path), d.Text, "document"}
	}
	if report.EntryPoints != nil {
		for _, d := range report.EntryPoints.Sources {
			files[filepath.ToSlash(d.Path)] = qualityRetainedFile{d.ID, filepath.ToSlash(d.Path), d.Text, "document"}
		}
	}
	return files
}

func registerQualityFileTools(server *mcp.Server, a *testquality.Assessment) {
	for _, spec := range []struct{ name, description string }{
		{"review_list_files", "List a directory by module-relative path, INCLUDING TEST FILES. source retained (default) lists assessment files; absence is not proof of filesystem absence. source live lists the actual project directory. Page with offset/limit. No execution or writes."},
		{"review_read_file", "Read a source/document by module-relative path and 1-based lines. source retained (default) reads assessment evidence. source live reads current project files; snapshotMatch says whether it is unchanged retained evidence. Changed/new live files grant NO pinned citation authority. Follow textPage.nextOffset or narrow line range. No arbitrary filesystem access."},
		{"review_search_files", "Search literal text in files under a module-relative directory, recursively. Defaults: source retained, path ., filePattern *.go. Live search is bounded and confined to the module root. Results are discovery snippets, NOT inspected citation authority. Page matches with offset/limit; narrow path if the scope exceeds budgets."},
	} {
		name := spec.name
		mcp.AddTool(server, &mcp.Tool{Name: name, Description: spec.description, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}},
			func(ctx context.Context, _ *mcp.CallToolRequest, input qualityFileInput) (*mcp.CallToolResult, map[string]any, error) {
				for _, pinned := range a.Reports {
					if pinned.SnapshotID == input.SnapshotID {
						raw, err := qualityFileCall(ctx, pinned.Report, input, name)
						return nil, raw, err
					}
				}
				return nil, nil, fmt.Errorf("unknown assessment-pinned snapshot")
			})
	}
}

func qualityPrivatePath(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		part = strings.ToLower(part)
		if part == ".git" || part == ".ssh" || part == ".aws" || part == ".kube" || part == ".env" || strings.HasPrefix(part, ".env.") || strings.HasSuffix(part, ".pem") || strings.HasSuffix(part, ".key") {
			return true
		}
	}
	return false
}

// Pin each opened directory to the identity inspected with Lstat. Root alone
// prevents escapes, but would still follow an in-root alias into private paths.
func qualityOpenLive(root *os.Root, path string) (*os.File, error) {
	dir := root
	defer func() {
		if dir != root {
			dir.Close()
		}
	}()
	parts := strings.Split(filepath.Clean(filepath.FromSlash(path)), string(filepath.Separator))
	for _, part := range parts[:len(parts)-1] {
		before, err := dir.Lstat(part)
		if err != nil {
			return nil, err
		}
		if !before.IsDir() {
			return nil, fmt.Errorf("live directory/symlink path is not exposed: %s", path)
		}
		next, err := dir.OpenRoot(part)
		if err != nil {
			return nil, err
		}
		actual, err := next.Stat(".")
		if err != nil {
			next.Close()
			return nil, err
		}
		if !os.SameFile(before, actual) {
			next.Close()
			return nil, fmt.Errorf("project directory changed while opening: %s", path)
		}
		if dir != root {
			dir.Close()
		}
		dir = next
	}
	name := parts[len(parts)-1]
	before, err := dir.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() && !before.IsDir() {
		return nil, fmt.Errorf("live special files and symlinks are not exposed: %s", path)
	}
	file, err := dir.Open(name)
	if err != nil {
		return nil, err
	}
	actual, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	if !os.SameFile(before, actual) {
		file.Close()
		return nil, fmt.Errorf("project file changed while opening: %s", path)
	}
	return file, nil
}

// os.Root confines opens even if a directory is replaced by a symlink between
// listing and reading. Live bytes never replace the assessment's pinned bytes.
func qualityLiveText(root *os.Root, path string) (string, error) {
	file, err := qualityOpenLive(root, path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("not a regular project file: %s", path)
	}
	const maximum = 2 << 20
	raw, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return "", err
	}
	if len(raw) > maximum {
		return "", fmt.Errorf("project file exceeds 2 MiB: %s", path)
	}
	if !utf8.Valid(raw) || strings.IndexByte(string(raw), 0) >= 0 {
		return "", fmt.Errorf("project file is not UTF-8 text: %s", path)
	}
	return string(raw), nil
}

func qualityFilePage(items []map[string]any, input qualityFileInput) (map[string]any, error) {
	if input.Offset < 0 || input.Offset > len(items) || input.Limit < 0 || input.Limit > 100 {
		return nil, fmt.Errorf("invalid file page; limit 1-100")
	}
	limit := input.Limit
	if limit == 0 {
		limit = 20
	}
	end := min(len(items), input.Offset+limit)
	for {
		var next *int
		if end < len(items) {
			next = &end
		}
		result := map[string]any{"items": items[input.Offset:end], "total": len(items), "nextOffset": next}
		raw, err := json.Marshal(result)
		if err != nil {
			return nil, err
		}
		if len(raw) <= 23500 {
			return result, nil
		}
		if end <= input.Offset+1 {
			return nil, fmt.Errorf("file entry exceeds output budget")
		}
		end = input.Offset + (end-input.Offset)/2
	}
}

func qualityFileCall(ctx context.Context, report *review.Report, input qualityFileInput, operation string) (map[string]any, error) {
	if input.Source == "" {
		input.Source = "retained"
	}
	if input.Source != "retained" && input.Source != "live" {
		return nil, fmt.Errorf("source must be retained or live")
	}
	if input.Path == "" {
		input.Path = "."
	}
	if !filepath.IsLocal(input.Path) {
		return nil, fmt.Errorf("path must stay relative to the selected module root")
	}
	input.Path = filepath.ToSlash(filepath.Clean(input.Path))
	if qualityPrivatePath(input.Path) {
		return nil, fmt.Errorf("private configuration and credential paths are not available to review agents")
	}
	retained := qualityRetainedFiles(report)
	var root *os.Root
	if input.Source == "live" {
		if report.Root == "" {
			return nil, fmt.Errorf("live project root is unavailable")
		}
		var err error
		root, err = os.OpenRoot(report.Root)
		if err != nil {
			return nil, fmt.Errorf("open live project root: %w", err)
		}
		defer root.Close()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	read := func(path string) (string, error) {
		if root != nil {
			return qualityLiveText(root, path)
		}
		file, ok := retained[path]
		if !ok {
			return "", fmt.Errorf("file was not retained: %s; use source live for current project files", path)
		}
		return file.text, nil
	}
	if operation == "review_read_file" {
		text, err := read(input.Path)
		if err != nil {
			return nil, err
		}
		pinned, exists := retained[input.Path]
		match := exists && testquality.Digest([]byte(text)) == testquality.Digest([]byte(pinned.text))
		lines := strings.Split(text, "\n")
		start, end := input.StartLine, input.EndLine
		if start == 0 {
			start = 1
		}
		if end == 0 {
			end = min(len(lines), start+99)
		}
		if start < 1 || end < start || end > len(lines) || end-start >= 500 {
			return nil, fmt.Errorf("source range %d-%d is invalid for %s (%d lines; request 1-500 valid lines)", start, end, input.Path, len(lines))
		}
		chars := []rune(strings.Join(lines[start-1:end], "\n"))
		if input.TextOffset < 0 || input.TextOffset > len(chars) || input.TextLimit < 0 || input.TextLimit > 24000 {
			return nil, fmt.Errorf("invalid text page; textLimit must be 1-24000")
		}
		limit := input.TextLimit
		if limit == 0 {
			limit = 6000
		}
		stop := min(len(chars), input.TextOffset+limit)
		for {
			var next *int
			if stop < len(chars) {
				next = &stop
			}
			data := map[string]any{"source": input.Source, "path": input.Path, "sha256": testquality.Digest([]byte(text)), "snapshotMatch": match, "fileID": pinned.id, "startLine": start, "endLine": end, "text": string(chars[input.TextOffset:stop]), "textPage": map[string]any{"offset": input.TextOffset, "nextOffset": next, "total": len(chars)}}
			if match {
				if citation := qualityTextCitation(input.SnapshotID, pinned.id, start, chars, input.TextOffset, stop); citation != nil {
					data["citation"] = citation
				}
			}
			raw, err := json.Marshal(data)
			if err != nil {
				return nil, err
			}
			if len(raw) <= 24000 {
				return map[string]any{"data": data}, nil
			}
			if stop == input.TextOffset {
				return nil, fmt.Errorf("file metadata exceeds output budget")
			}
			stop = input.TextOffset + (stop-input.TextOffset)/2
		}
	}
	var names []string
	directories := map[string]bool{}
	if root == nil {
		prefix := input.Path + "/"
		if input.Path == "." {
			prefix = ""
		}
		for path := range retained {
			if path == input.Path {
				names = append(names, path)
				continue
			}
			if !strings.HasPrefix(path, prefix) || qualityPrivatePath(path) {
				continue
			}
			remainder := strings.TrimPrefix(path, prefix)
			if operation == "review_list_files" && strings.Contains(remainder, "/") {
				directories[prefix+strings.SplitN(remainder, "/", 2)[0]] = true
			} else {
				names = append(names, path)
			}
		}
	} else {
		visited := 0
		var walk func(string) error
		walk = func(path string) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			file, err := qualityOpenLive(root, path)
			if err != nil {
				return err
			}
			defer file.Close()
			info, err := file.Stat()
			if err != nil {
				return err
			}
			if info.Mode().IsRegular() {
				names = append(names, path)
				return nil
			}
			if !info.IsDir() {
				return fmt.Errorf("not a project file/directory: %s", path)
			}
			entries, err := file.ReadDir(10001)
			if err != nil && err != io.EOF {
				return err
			}
			for _, entry := range entries {
				visited++
				if visited > 10000 {
					return fmt.Errorf("file discovery exceeds 10000 entries; narrow the directory path")
				}
				child := filepath.ToSlash(filepath.Join(path, entry.Name()))
				if qualityPrivatePath(child) {
					continue
				}
				if entry.Type()&os.ModeSymlink != 0 {
					continue
				}
				if entry.IsDir() {
					if operation == "review_list_files" {
						directories[child] = true
					} else if entry.Name() != "node_modules" && entry.Name() != ".work" {
						if err := walk(child); err != nil {
							return err
						}
					}
				} else if entry.Type().IsRegular() {
					names = append(names, child)
				}
			}
			return nil
		}
		if err := walk(input.Path); err != nil {
			return nil, err
		}
	}
	sort.Strings(names)
	items := []map[string]any{}
	if operation == "review_list_files" {
		for path := range directories {
			items = append(items, map[string]any{"path": path, "type": "directory"})
		}
		for _, path := range names {
			entry := map[string]any{"path": path, "type": "file", "retained": retained[path].id != ""}
			if file, ok := retained[path]; ok {
				entry["fileID"], entry["kind"] = file.id, file.kind
			}
			items = append(items, entry)
		}
		sort.Slice(items, func(i, j int) bool { return fmt.Sprint(items[i]["path"]) < fmt.Sprint(items[j]["path"]) })
	} else {
		if strings.TrimSpace(input.Query) == "" {
			return nil, fmt.Errorf("search requires nonempty literal query")
		}
		if input.FilePattern == "" {
			input.FilePattern = "*.go"
		}
		if _, err := filepath.Match(input.FilePattern, ""); err != nil {
			return nil, err
		}
		if input.Offset < 0 || input.Limit < 0 || input.Limit > 100 {
			return nil, fmt.Errorf("invalid search page; limit 1-100")
		}
		limit := input.Limit
		if limit == 0 {
			limit = 20
		}
		totalBytes, totalMatches := 0, 0
		for _, path := range names {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			matches, _ := filepath.Match(input.FilePattern, filepath.Base(path))
			if !matches {
				continue
			}
			text, err := read(path)
			if err != nil {
				return nil, fmt.Errorf("search %s: %w", path, err)
			}
			totalBytes += len(text)
			if totalBytes > 16<<20 {
				return nil, fmt.Errorf("search exceeds 16 MiB; narrow the directory or filename pattern")
			}
			for line, value := range strings.Split(text, "\n") {
				if !strings.Contains(value, input.Query) {
					continue
				}
				totalMatches++
				if totalMatches <= input.Offset || len(items) >= limit {
					continue
				}
				excerpt := []rune(value)
				truncated := len(excerpt) > 160
				if truncated {
					excerpt = excerpt[:160]
				}
				items = append(items, map[string]any{"path": path, "line": line + 1, "text": string(excerpt), "excerptTruncated": truncated})
			}
		}
		if input.Offset > totalMatches {
			return nil, fmt.Errorf("search offset exceeds match count")
		}
		pageInput := input
		pageInput.Offset = 0
		result, err := qualityFilePage(items, pageInput)
		if err != nil {
			return nil, err
		}
		next := input.Offset + len(result["items"].([]map[string]any))
		result["total"], result["nextOffset"] = totalMatches, nil
		if next < totalMatches {
			result["nextOffset"] = &next
		}
		result["source"], result["scope"] = input.Source, "selected module; private paths, symlinks, and recursive node_modules/.work discovery excluded"
		return result, nil
	}
	result, err := qualityFilePage(items, input)
	if err != nil {
		return nil, err
	}
	result["source"], result["scope"] = input.Source, "selected module; private paths, symlinks, and recursive node_modules/.work discovery excluded"
	return result, nil
}
