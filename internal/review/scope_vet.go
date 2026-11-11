package review

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

func scopedVet(ctx context.Context, r *Report, out string, s *ReviewStep) {
	start := len(r.Commands)
	defer func() { s.Commands = r.Commands[start:] }()
	selection, err := prepareExecutionScope(ctx, r, r.Root, out, []string{"./..."})
	if err != nil {
		s.Status, s.Detail = "error", err.Error()
		return
	}
	overlay, err := writeScopeOverlay(out, selection.overlay)
	if err != nil {
		s.Status, s.Detail = "error", err.Error()
		return
	}
	args := append([]string{"vet", "-mod=readonly", "-json"}, executionArgs(overlay)...)
	_, err = runGo(ctx, r, r.Root, true, append(args, selection.packages...)...)
	if err != nil {
		s.Status, s.Detail = "error", err.Error()
		return
	}
	command := r.Commands[len(r.Commands)-1]
	// go vet -json emits analyzer diagnostics on stderr, potentially interleaved
	// with "# package" headers. Raw diagnostics stay in the retained command.
	var text strings.Builder
	for _, line := range strings.Split(command.Stdout+"\n"+command.Stderr, "\n") {
		if !strings.HasPrefix(line, "#") {
			text.WriteString(line)
			text.WriteByte('\n')
		}
	}
	decoder := json.NewDecoder(strings.NewReader(text.String()))
	positionPattern := regexp.MustCompile(`:(\d+)(?::\d+)?$`)
	// Some analyzers report the physical overlay file, not its virtual source.
	// Map it back before applying source-bound exclusions or showing diagnostics.
	overlaySources := map[string]string{}
	for source, replacement := range selection.overlay {
		overlaySources[filepath.Clean(replacement)] = source
		resolved, e := filepath.EvalSymlinks(replacement)
		if e != nil {
			s.Status, s.Detail = "error", fmt.Sprintf("resolve vet overlay: %v", e)
			return
		}
		overlaySources[resolved] = source
	}
	excluded := 0
	for {
		var group map[string]map[string][]struct{ Posn, Message string }
		e := decoder.Decode(&group)
		if e == io.EOF {
			break
		}
		if e != nil {
			s.Status, s.Detail = "error", fmt.Sprintf("parse vet diagnostics: %v\n%s", e, text.String())
			return
		}
		for _, analyzers := range group {
			for analyzer, diagnostics := range analyzers {
				for _, d := range diagnostics {
					position := positionPattern.FindStringSubmatch(d.Posn)
					line := 0
					if len(position) > 1 {
						line, _ = strconv.Atoi(position[1])
					}
					name := positionPattern.ReplaceAllString(d.Posn, "")
					if !filepath.IsAbs(name) {
						name = filepath.Join(r.Root, name)
					}
					if original, ok := overlaySources[filepath.Clean(name)]; ok {
						name = original
					}
					rel, e := filepath.Rel(r.Root, name)
					if e != nil || !inside(r.Root, name) {
						s.Status = "fail"
						s.Detail += "\n" + analyzer + ": " + d.Posn + ": " + d.Message
						continue
					}
					data, e := os.ReadFile(name)
					if e != nil {
						s.Status, s.Detail = "error", fmt.Sprintf("read vet source %s: %v", name, e)
						return
					}
					if r.Scope.sourcePositionReason(r.Root, filepath.ToSlash(rel), string(data), line) != "" {
						excluded++
						continue
					}
					s.Status = "fail"
					s.Detail += "\n" + analyzer + ": " + d.Posn + ": " + d.Message
				}
			}
		}
	}
	if excluded > 0 {
		s.Detail += fmt.Sprintf("\n%d diagnostics outside review scope; raw output retained.", excluded)
	}
}
