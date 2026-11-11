package changes

import "testing"

func TestChangeProviderInputsAreRevisionSourcesNotReviewActions(t *testing.T) {
	for _, tc := range []struct{ url, host, repo, number string }{
		{"https://github.com/team/project/pull/42", "github.com", "team/project", "42"},
		{"https://gitlab.example.test/team/nested/project/-/merge_requests/7", "gitlab.example.test", "team/nested/project", "7"},
	} {
		p, err := parseProvider(tc.url)
		if err != nil || p.Host != tc.host || p.Repository != tc.repo || p.Number != tc.number {
			t.Fatalf("invalid revision input: %+v %v", p, err)
		}
	}
	for _, url := range []string{"https://user:secret@github.com/team/repo/pull/1", "file:///tmp/repo", "https://github.com/team/repo/issues/1", "https://github.com/team/repo/pull/--help"} {
		if _, err := parseProvider(url); err == nil {
			t.Fatalf("accepted non-revision/credential-bearing input %q", url)
		}
	}
}
