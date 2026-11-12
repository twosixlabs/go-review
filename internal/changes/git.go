package changes

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

func git(ctx context.Context, root string, args ...string) ([]byte, error) {
	return gitOutput(ctx, root, false, args...)
}

func gitOutput(ctx context.Context, root string, differenceExit bool, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, "git", append([]string{"--no-pager", "--literal-pathspecs", "-c", "core.hooksPath=/dev/null", "-C", root}, args...)...)
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "GIT_NO_REPLACE_OBJECTS=1")
	var stderr bytes.Buffer
	command.Stderr = &stderr
	out, err := command.Output()
	var exit *exec.ExitError
	if differenceExit && errors.As(err, &exit) && exit.ExitCode() == 1 {
		return out, nil
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("git %s: %w", args[0], ctx.Err())
		}
		return nil, fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

func Resolve(ctx context.Context, request Request) (Resolved, error) {
	var result Resolved
	root, err := git(ctx, request.Directory, "rev-parse", "--show-toplevel")
	if err != nil {
		return result, err
	}
	result.Repository, err = filepath.EvalSymlinks(strings.TrimSpace(string(root)))
	if err != nil {
		return result, fmt.Errorf("resolve repository: %w", err)
	}
	resolve := func(label string) (Revision, error) {
		if label == "" || strings.HasPrefix(label, "-") {
			return Revision{}, fmt.Errorf("a committed revision is required: %q", label)
		}
		out, err := git(ctx, result.Repository, "rev-parse", "--verify", "--end-of-options", label+"^{commit}")
		return Revision{Label: label, Commit: strings.TrimSpace(string(out))}, err
	}
	result.Before, err = resolve(request.Before)
	if err != nil {
		return result, fmt.Errorf("resolve Before: %w", err)
	}
	result.After, err = resolve(request.After)
	if err != nil {
		return result, fmt.Errorf("resolve After: %w", err)
	}
	result.Policy = Policy
	if request.MergeBase {
		target := result.Before
		result.Target = &target
		out, err := git(ctx, result.Repository, "merge-base", "--all", target.Commit, result.After.Commit)
		if err != nil {
			return result, fmt.Errorf("resolve merge base: %w", err)
		}
		bases := strings.Fields(string(out))
		if len(bases) != 1 {
			return result, fmt.Errorf("comparison requires one unambiguous merge base; found %d", len(bases))
		}
		result.Before = Revision{Label: "merge-base(" + target.Label + ", " + result.After.Label + ")", Commit: bases[0]}
	}
	return result, nil
}

var hunkHeader = regexp.MustCompile(`(?m)^@@ -([0-9]+)(?:,([0-9]+))? \+([0-9]+)(?:,([0-9]+))? @@`)
var commitOID = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
var binaryDiagnostic = regexp.MustCompile(`(?m)^Binary files .+ differ$`)
var lfsPointer = regexp.MustCompile(`\Aversion https://git-lfs.github.com/spec/v1\noid sha256:[0-9a-f]{64}\nsize [0-9]+\n?\z`)

func blobSize(ctx context.Context, r Resolved, object Object) (int64, error) {
	size, err := r.git(ctx, "cat-file", "-s", object.OID)
	if err != nil {
		return 0, err
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(size)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid blob size for %q: %w", object.Path, err)
	}
	return n, nil
}

func isLFSPointer(ctx context.Context, r Resolved, object Object) (bool, error) {
	if object.Path == "" || (object.Mode != "100644" && object.Mode != "100755") {
		return false, nil
	}
	n, err := blobSize(ctx, r, object)
	if err != nil {
		return false, err
	}
	// LFS pointers are small blobs. Read only candidates, never invoke filters.
	if n > 1024 {
		return false, nil
	}
	data, err := r.git(ctx, "cat-file", "blob", object.OID)
	if err != nil {
		return false, err
	}
	return lfsPointer.Match(data), nil
}

func Inventory(ctx context.Context, r Resolved) ([]FileChange, error) {
	raw, err := r.git(ctx, "diff", "--raw", "-z", "--no-abbrev", "--no-ext-diff", "--no-textconv", "-M", r.Before.Commit, r.After.Commit, "--")
	if err != nil {
		return nil, err
	}
	tokens := bytes.Split(raw, []byte{0})
	files := []FileChange{}
	for i := 0; i < len(tokens) && len(tokens[i]) > 0; {
		metadata := strings.Fields(string(tokens[i]))
		i++
		if len(metadata) != 5 || !strings.HasPrefix(metadata[0], ":") || i >= len(tokens) {
			return nil, fmt.Errorf("invalid raw Git inventory record")
		}
		oldPath := string(tokens[i])
		i++
		newPath := oldPath
		status := metadata[4]
		if status[0] == 'R' || status[0] == 'C' {
			if i >= len(tokens) {
				return nil, fmt.Errorf("missing rename destination")
			}
			newPath = string(tokens[i])
			i++
		}
		file := FileChange{Before: Object{Path: oldPath, OID: metadata[2], Mode: strings.TrimPrefix(metadata[0], ":")},
			After: Object{Path: newPath, OID: metadata[3], Mode: metadata[1]}, Content: "text", Items: []Item{}}
		switch status[0] {
		case 'A':
			file.Status = "added"
			file.Before.Path = ""
		case 'D':
			file.Status = "removed"
			file.After.Path = ""
		case 'R':
			file.Status = "renamed"
		case 'T':
			file.Status = "type-changed"
		default:
			file.Status = "changed"
		}
		file.ID = identity(r.Before.Commit, r.After.Commit, oldPath, newPath, metadata[2], metadata[3], metadata[0], metadata[1])
		var hunks [][]string
		if file.Before.Mode == "160000" || file.After.Mode == "160000" {
			file.Content = "submodule"
		} else {
			patch, oversized, err := filePatch(ctx, r, file, 0)
			if err != nil {
				return nil, err
			}
			if oversized {
				file.Content = "oversized"
			} else if binaryDiagnostic.Match(patch) {
				file.Content = "binary"
			} else {
				hunks = hunkHeader.FindAllStringSubmatch(string(patch), -1)
			}
			for _, object := range []Object{file.Before, file.After} {
				pointer, err := isLFSPointer(ctx, r, object)
				if err != nil {
					return nil, err
				}
				if pointer {
					file.Content = "lfs-pointer"
				}
			}
		}
		for n, h := range hunks {
			startBefore, _ := strconv.Atoi(h[1])
			countBefore := 1
			if h[2] != "" {
				countBefore, _ = strconv.Atoi(h[2])
			}
			startAfter, _ := strconv.Atoi(h[3])
			countAfter := 1
			if h[4] != "" {
				countAfter, _ = strconv.Atoi(h[4])
			}
			file.Items = append(file.Items, Item{ID: identity(file.ID, strconv.Itoa(n)), FileID: file.ID, Kind: "text",
				Before: Range{Start: startBefore, Lines: countBefore}, After: Range{Start: startAfter, Lines: countAfter}})
		}
		if len(file.Items) == 0 {
			file.Items = append(file.Items, Item{ID: identity(file.ID, "tree"), FileID: file.ID, Kind: "tree"})
		}
		files = append(files, file)
	}
	return files, nil
}

// Materialize reads raw tree/blob objects, not a checkout or Git archive.
// Attributes cannot omit/substitute source; hooks, filters and symlinks do not run.
func Materialize(ctx context.Context, repository, commit, target string) ([]string, error) {
	return materialize(ctx, Resolved{Repository: repository}, commit, target)
}

func materialize(ctx context.Context, r Resolved, commit, target string) ([]string, error) {
	if err := os.Mkdir(target, 0700); err != nil {
		return nil, fmt.Errorf("create revision directory: %w", err)
	}
	tree, err := r.git(ctx, "ls-tree", "-rz", "--full-tree", commit)
	if err != nil {
		return nil, err
	}
	type entry struct{ path, oid string }
	entries := []entry{}
	var input strings.Builder
	limits := []string{}
	for _, record := range bytes.Split(tree, []byte{0}) {
		if len(record) == 0 {
			continue
		}
		parts := bytes.SplitN(record, []byte{'\t'}, 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid Git tree record")
		}
		meta := strings.Fields(string(parts[0]))
		if len(meta) != 3 {
			return nil, fmt.Errorf("invalid Git tree metadata")
		}
		path := string(parts[1])
		unsafe := false
		for _, part := range strings.Split(filepath.ToSlash(path), "/") {
			unsafe = unsafe || strings.EqualFold(part, ".git")
		}
		if unsafe {
			limits = append(limits, "Not materialized (Git metadata path): "+path)
			continue
		}
		if meta[0] != "100644" && meta[0] != "100755" {
			limits = append(limits, "Not materialized (non-regular input): "+path)
			continue
		}
		entries = append(entries, entry{path: path, oid: meta[2]})
		input.WriteString(meta[2] + "\n")
	}
	command := exec.CommandContext(ctx, "git", append([]string{"-c", "core.hooksPath=/dev/null", "-C", r.objects()}, r.objectArgs("cat-file", "--batch")...)...)
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_NO_REPLACE_OBJECTS=1")
	command.Stdin = strings.NewReader(input.String())
	var stderr bytes.Buffer
	command.Stderr = &stderr
	stream, err := command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := command.Start(); err != nil {
		return nil, err
	}
	readErr := func() error {
		reader := bufio.NewReader(stream)
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			header, err := reader.ReadString('\n')
			if err != nil {
				return err
			}
			fields := strings.Fields(header)
			if len(fields) != 3 || fields[0] != entry.oid || fields[1] != "blob" {
				return fmt.Errorf("invalid blob batch response: %q", header)
			}
			size, err := strconv.ParseInt(fields[2], 10, 64)
			if err != nil || size < 0 {
				return fmt.Errorf("invalid blob size: %q", header)
			}
			path := filepath.Join(target, filepath.FromSlash(entry.path))
			rel, err := filepath.Rel(target, path)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return fmt.Errorf("tree path escapes revision: %q", entry.path)
			}
			if size > 16<<20 {
				limits = append(limits, "Not materialized (over 16 MiB): "+entry.path)
				if _, err := io.CopyN(io.Discard, reader, size); err != nil {
					return err
				}
			} else {
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					return err
				}
				file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
				if err != nil {
					return err
				}
				_, copyErr := io.CopyN(file, reader, size)
				closeErr := file.Close()
				if copyErr != nil {
					return copyErr
				}
				if closeErr != nil {
					return closeErr
				}
			}
			if delimiter, err := reader.ReadByte(); err != nil || delimiter != '\n' {
				return fmt.Errorf("invalid blob delimiter: %v", err)
			}
		}
		return nil
	}()
	if readErr != nil {
		_ = stream.Close()
	}
	waitErr := command.Wait()
	if readErr != nil {
		return nil, fmt.Errorf("materialize revision: %w", readErr)
	}
	if waitErr != nil {
		return nil, fmt.Errorf("git cat-file: %w: %s", waitErr, strings.TrimSpace(stderr.String()))
	}
	return limits, nil
}
