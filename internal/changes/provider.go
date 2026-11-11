package changes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

type providerInput struct {
	Host       string
	Repository string
	Number     string
	GitHub     bool
}

var changeNumber = regexp.MustCompile(`^[1-9][0-9]*$`)
var commitOID = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

func parseProvider(input string) (providerInput, error) {
	p := providerInput{}
	u, err := url.Parse(input)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return p, fmt.Errorf("PR/MR input requires an HTTPS URL without embedded credentials, query or fragment")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 4 {
		return p, fmt.Errorf("expected a GitHub pull request or GitLab merge request URL")
	}
	p.Host = providerHost(u)
	p.Number = parts[len(parts)-1]
	if !changeNumber.MatchString(p.Number) {
		return p, fmt.Errorf("invalid PR/MR number")
	}
	if len(parts) == 4 && parts[2] == "pull" {
		p.GitHub = true
		p.Repository = strings.Join(parts[:2], "/")
	} else if len(parts) >= 5 && parts[len(parts)-3] == "-" && parts[len(parts)-2] == "merge_requests" {
		p.Repository = strings.Join(parts[:len(parts)-3], "/")
	} else {
		return p, fmt.Errorf("URL is not a PR/MR revision source")
	}
	for _, segment := range strings.Split(p.Repository, "/") {
		if segment == "" || segment == "." || segment == ".." || strings.HasPrefix(segment, "-") {
			return p, fmt.Errorf("invalid provider repository path")
		}
	}
	return p, nil
}

func providerJSON(ctx context.Context, executable string, args []string, target any) error {
	command := exec.CommandContext(ctx, executable, args...)
	command.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1", "GLAB_PROMPT_DISABLED=1")
	var stderr bytes.Buffer
	command.Stderr = &stderr
	out, err := command.Output()
	if err != nil {
		return fmt.Errorf("%s revision lookup (use existing local authentication): %w: %s", executable, err, strings.TrimSpace(stderr.String()))
	}
	if err := json.Unmarshal(out, target); err != nil {
		return fmt.Errorf("decode %s revision metadata: %w", executable, err)
	}
	return nil
}

func remoteIdentity(raw string) string {
	if strings.HasPrefix(raw, "git@") {
		raw = "ssh://" + strings.Replace(raw, ":", "/", 1)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return providerHost(u) + "/" + strings.TrimSuffix(strings.Trim(u.Path, "/"), ".git")
}

// ResolveProvider reads provider metadata and fetches only Git inputs into an
// owned bare object directory. It has no provider write/approval/status surface.
func ResolveProvider(ctx context.Context, request Request, storage string) (_ Resolved, err error) {
	var result Resolved
	p, err := parseProvider(request.Provider)
	if err != nil {
		return result, err
	}
	root, err := git(ctx, request.Directory, "rev-parse", "--show-toplevel")
	if err != nil {
		return result, err
	}
	repository, err := filepath.EvalSymlinks(strings.TrimSpace(string(root)))
	if err != nil {
		return result, err
	}
	remote, err := git(ctx, repository, "remote", "get-url", "origin")
	if err != nil {
		return result, fmt.Errorf("provider input requires the corresponding origin remote: %w", err)
	}
	if remoteIdentity(strings.TrimSpace(string(remote))) != strings.ToLower(p.Host)+"/"+p.Repository {
		return result, fmt.Errorf("PR/MR repository does not match local origin")
	}
	var base, head, baseLabel string
	if p.GitHub {
		var metadata struct {
			BaseRefOID  string `json:"baseRefOid"`
			HeadRefOID  string `json:"headRefOid"`
			BaseRefName string `json:"baseRefName"`
		}
		if err := providerJSON(ctx, "gh", []string{"pr", "view", request.Provider, "--json", "baseRefOid,headRefOid,baseRefName"}, &metadata); err != nil {
			return result, err
		}
		base, head, baseLabel = metadata.BaseRefOID, metadata.HeadRefOID, metadata.BaseRefName
	} else {
		var metadata struct {
			SHA             string `json:"sha"`
			TargetBranch    string `json:"target_branch"`
			TargetProjectID int    `json:"target_project_id"`
		}
		if err := providerJSON(ctx, "glab", []string{"api", "--hostname", p.Host, "projects/" + url.PathEscape(p.Repository) + "/merge_requests/" + p.Number}, &metadata); err != nil {
			return result, err
		}
		var branch struct {
			Commit struct {
				ID string `json:"id"`
			} `json:"commit"`
		}
		if err := providerJSON(ctx, "glab", []string{"api", "--hostname", p.Host, fmt.Sprintf("projects/%d/repository/branches/%s", metadata.TargetProjectID, url.PathEscape(metadata.TargetBranch))}, &branch); err != nil {
			return result, err
		}
		base, head, baseLabel = branch.Commit.ID, metadata.SHA, metadata.TargetBranch
	}
	if !commitOID.MatchString(base) || !commitOID.MatchString(head) {
		return result, fmt.Errorf("provider did not return full immutable commit OIDs")
	}
	if err := os.MkdirAll(storage, 0700); err != nil {
		return result, err
	}
	objectDir, err := os.MkdirTemp(storage, "revision-input-")
	if err != nil {
		return result, err
	}
	defer func() {
		if err != nil {
			err = cleanupError(err, objectDir)
		}
	}()
	if _, err := git(ctx, objectDir, "init", "--bare", "-q"); err != nil {
		return result, err
	}
	remoteURL := "https://" + p.Host + "/" + p.Repository + ".git"
	if _, err := git(ctx, objectDir, "--git-dir="+objectDir, "fetch", "--no-tags", "--no-recurse-submodules", remoteURL, base, head); err != nil {
		return result, fmt.Errorf("fetch pinned PR/MR commits without modifying checkout: %w", err)
	}
	result, err = Resolve(ctx, Request{Directory: repository, Before: "HEAD", After: "HEAD"})
	if err != nil {
		return result, err
	}
	bases, err := git(ctx, objectDir, "--git-dir="+objectDir, "merge-base", "--all", base, head)
	if err != nil {
		return result, fmt.Errorf("provider revisions have no available common history: %w", err)
	}
	ancestors := strings.Fields(string(bases))
	if len(ancestors) != 1 {
		return result, fmt.Errorf("provider revisions require one unambiguous merge base; found %d", len(ancestors))
	}
	result.Before = Revision{Label: "merge-base(" + baseLabel + ", source)", Commit: ancestors[0]}
	result.After = Revision{Label: "PR/MR source head", Commit: head}
	result.Target = &Revision{Label: baseLabel, Commit: base}
	result.Provider = request.Provider
	result.ObjectDirectory = objectDir
	result.inputStorage = filepath.Clean(storage)
	return result, nil
}

func (r Resolved) objects() string {
	if r.ObjectDirectory != "" {
		return r.ObjectDirectory
	}

	return r.Repository
}

// Explicit --git-dir respects machines that require safe.bareRepository=explicit.
// Only the provider-owned object store uses it; ordinary checkout discovery is unchanged.
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
