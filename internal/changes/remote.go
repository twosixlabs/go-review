package changes

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// ResolveRemote fetches a source revision into owned storage without changing
// the checkout. The target revision must be available in the local repository.
func ResolveRemote(ctx context.Context, request Request, storage string) (_ Resolved, err error) {
	if request.Remote == "" || strings.HasPrefix(request.Remote, "-") || request.After == "" || strings.HasPrefix(request.After, "-") {
		return Resolved{}, fmt.Errorf("a Git remote and source revision are required")
	}
	local, err := Resolve(ctx, Request{Directory: request.Directory, Before: request.Before, After: request.Before})
	if err != nil {
		return Resolved{}, err
	}
	remote := request.Remote
	if out, lookupErr := git(ctx, local.Repository, "remote", "get-url", "--", remote); lookupErr == nil {
		remote = strings.TrimSpace(string(out))
	}
	if u, parseErr := url.Parse(remote); parseErr == nil && u.User != nil {
		if _, hasPassword := u.User.Password(); hasPassword || u.Scheme == "http" || u.Scheme == "https" {
			return Resolved{}, fmt.Errorf("do not embed credentials in the Git remote URL")
		}
	}
	if !strings.Contains(remote, "://") && !strings.Contains(remote, ":") && !filepath.IsAbs(remote) {
		remote = filepath.Join(local.Repository, remote)
	}
	if err := os.MkdirAll(storage, 0700); err != nil {
		return Resolved{}, err
	}
	objectDir, err := os.MkdirTemp(storage, "revision-input-")
	if err != nil {
		return Resolved{}, err
	}
	defer func() {
		if err != nil {
			err = cleanupError(err, objectDir)
		}
	}()
	if _, err = git(ctx, objectDir, "init", "--bare", "-q"); err != nil {
		return Resolved{}, err
	}
	args := []string{"-c", "protocol.ext.allow=never", "--git-dir=" + objectDir, "fetch", "--no-tags", "--no-recurse-submodules", "--"}
	if _, err = git(ctx, objectDir, append(args, local.Repository, local.Before.Commit)...); err != nil {
		return Resolved{}, fmt.Errorf("fetch local target revision: %w", err)
	}
	if _, err = git(ctx, objectDir, append(args, remote, request.After)...); err != nil {
		return Resolved{}, fmt.Errorf("fetch source revision: %w", err)
	}
	head, err := git(ctx, objectDir, "--git-dir="+objectDir, "rev-parse", "--verify", "FETCH_HEAD^{commit}")
	if err != nil {
		return Resolved{}, fmt.Errorf("resolve fetched source revision: %w", err)
	}
	local.After = Revision{Label: request.After, Commit: strings.TrimSpace(string(head))}
	local.ObjectDirectory, local.inputStorage = objectDir, filepath.Clean(storage)
	if request.MergeBase {
		target := local.Before
		local.Target = &target
		bases, mergeErr := local.git(ctx, "merge-base", "--all", target.Commit, local.After.Commit)
		if mergeErr != nil {
			return Resolved{}, fmt.Errorf("resolve merge base: %w", mergeErr)
		}
		ancestors := strings.Fields(string(bases))
		if len(ancestors) != 1 {
			return Resolved{}, fmt.Errorf("comparison requires one unambiguous merge base; found %d", len(ancestors))
		}
		local.Before = Revision{Label: "merge-base(" + target.Label + ", " + local.After.Label + ")", Commit: ancestors[0]}
	}
	return local, nil
}

func (r Resolved) objects() string {
	if r.ObjectDirectory != "" {
		return r.ObjectDirectory
	}
	return r.Repository
}

func (r Resolved) objectArgs(args ...string) []string {
	if r.ObjectDirectory != "" {
		return append([]string{"--git-dir=" + r.ObjectDirectory}, args...)
	}
	return args
}

func (r Resolved) git(ctx context.Context, args ...string) ([]byte, error) {
	return git(ctx, r.objects(), r.objectArgs(args...)...)
}

func ReleaseInputs(r Resolved) error {
	if r.ObjectDirectory == "" {
		return nil
	}
	if !filepath.IsAbs(r.ObjectDirectory) || r.inputStorage == "" || filepath.Dir(r.ObjectDirectory) != r.inputStorage ||
		!strings.HasPrefix(filepath.Base(r.ObjectDirectory), "revision-input-") {
		return fmt.Errorf("refusing to release unowned revision directory")
	}
	info, err := os.Lstat(r.ObjectDirectory)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("revision input directory must not be a symlink")
	}
	return os.RemoveAll(r.ObjectDirectory)
}
