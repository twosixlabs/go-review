package review

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ImportCoverage preserves standard Go coverprofile byte ranges. Imported data is
// never marked source-verified: the profile format contains no source checksum.
func ImportCoverage(r *Report, reader io.Reader, origin string) error {
	data, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	if !sc.Scan() {
		return fmt.Errorf("empty coverage profile")
	}
	mode := strings.TrimPrefix(sc.Text(), "mode: ")
	if mode != "set" && mode != "count" && mode != "atomic" {
		return fmt.Errorf("invalid coverage mode %q", sc.Text())
	}
	c := &Coverage{Mode: mode, Origin: origin, SHA256: digest(data), Blocks: []CoverageBlock{}}
	files := map[string]File{}
	for _, f := range r.Files {
		files[r.Module+"/"+f.Path] = f
		files[filepath.Join(r.Root, filepath.FromSlash(f.Path))] = f
	}
	for sc.Scan() {
		text := sc.Text()
		if strings.TrimSpace(text) == "" {
			continue
		}
		fields := strings.Fields(text)
		if len(fields) != 3 {
			return fmt.Errorf("malformed coverage block %q", text)
		}
		colon := strings.LastIndex(fields[0], ":")
		if colon < 0 {
			return fmt.Errorf("missing coverage position: %s", text)
		}
		b := CoverageBlock{Path: fields[0][:colon]}
		pos := fields[0][colon+1:]
		var trailing string
		if n, _ := fmt.Sscanf(pos, "%d.%d,%d.%d%s", &b.StartLine, &b.StartColumn, &b.EndLine, &b.EndColumn, &trailing); n != 4 {
			return fmt.Errorf("invalid coverage position %q", pos)
		}
		b.Statements, err = strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return err
		}
		b.Count, err = strconv.ParseInt(fields[2], 10, 64)
		if err != nil {
			return err
		}
		if b.StartLine < 1 || b.StartColumn < 1 || b.EndLine < b.StartLine || b.EndColumn < 1 || (b.EndLine == b.StartLine && b.EndColumn < b.StartColumn) || b.Count < 0 || b.Statements < 0 || (mode == "set" && b.Count > 1) {
			return fmt.Errorf("invalid coverage range or count %q", text)
		}
		excluded := false
		for _, e := range r.Exclusions {
			if b.Path == r.Module+"/"+e.Path || filepath.Clean(b.Path) == filepath.Join(r.Root, filepath.FromSlash(e.Path)) {
				excluded = true
				break
			}
		}
		if excluded {
			continue
		}
		if f, ok := files[b.Path]; ok {
			lines := strings.Split(f.Source, "\n")
			if b.EndLine > len(lines) || b.StartColumn > len(lines[b.StartLine-1])+1 || b.EndColumn > len(lines[b.EndLine-1])+1 {
				return fmt.Errorf("coverage range outside source: %s", text)
			}
			b.FileID = f.ID
		} else {
			r.Warnings = append(r.Warnings, "coverage file not in analyzed sources: "+b.Path)
		}
		c.Statements += b.Statements
		if b.Count > 0 {
			c.CoveredStatements += b.Statements
		}
		c.Blocks = append(c.Blocks, b)
	}
	if err := sc.Err(); err != nil {
		return err
	}
	r.Coverage = c
	if r.SystemFlow != nil {
		ApplyFlowCoverage(r, r.SystemFlow)
	}
	return nil
}

func artifactDirectory(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("an artifact directory is required for explicit test execution")
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return path, os.MkdirAll(path, 0700)
}
