package changes

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// Diff object pairs, never a union of pathspecs: an old file path can become a
// directory containing unrelated additions. The metadata header retains paths
// and modes even when blob contents are unchanged.
func filePatch(ctx context.Context, r Resolved, file FileChange, contextLines int) (_ []byte, oversized bool, err error) {
	header := fmt.Sprintf("Before: %s (%s, mode %s)\nAfter: %s (%s, mode %s)\n",
		strconv.Quote(file.Before.Path), file.Before.OID, file.Before.Mode,
		strconv.Quote(file.After.Path), file.After.OID, file.After.Mode)
	if file.Before.OID == file.After.OID || file.Before.Mode == "160000" || file.After.Mode == "160000" {
		return []byte(header + "No textual blob change; inspect paths, modes and object identities above.\n"), false, nil
	}
	for _, object := range []Object{file.Before, file.After} {
		if object.Path == "" {
			continue
		}
		size, err := blobSize(ctx, r, object)
		if err != nil {
			return nil, false, err
		}
		if size > 16<<20 {
			return []byte(header + "Text comparison exceeds the retained 16 MiB per-blob limit; paths, modes and object identities remain inventoried.\n"), true, nil
		}
	}
	args := []string{"diff", "--no-ext-diff", "--no-textconv", "--no-color", "--full-index", fmt.Sprintf("--unified=%d", contextLines)}
	if file.Before.Path != "" && file.After.Path != "" {
		patch, err := r.git(ctx, append(args, file.Before.OID, file.After.OID, "--")...)
		return append([]byte(header), patch...), false, err
	}
	// Git cannot diff a missing blob OID. For additions/removals use one raw
	// retained blob against /dev/null without writing objects to the checkout.
	directory, err := os.MkdirTemp("", "go-review-blob-diff-")
	if err != nil {
		return nil, false, err
	}
	defer func() {
		if cleanupErr := os.RemoveAll(directory); cleanupErr != nil {
			err = errors.Join(err, fmt.Errorf("remove owned blob diff input: %w", cleanupErr))
		}
	}()
	before, after := "/dev/null", "/dev/null"
	object := file.After
	if file.After.Path == "" {
		object = file.Before
	}
	data, err := r.git(ctx, "cat-file", "blob", object.OID)
	if err != nil {
		return nil, false, err
	}
	path := filepath.Join(directory, "content")
	if err := os.WriteFile(path, data, 0600); err != nil {
		return nil, false, err
	}
	if file.Before.Path == "" {
		after = "content"
	} else {
		before = "content"
	}
	args = append(args, "--no-index", "--", before, after)
	patch, err := gitOutput(ctx, directory, true, args...)
	return append([]byte(header), patch...), false, err
}
