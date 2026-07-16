package gh

import "testing"

func TestURLHelpers(t *testing.T) {
	if got := PRURL("org", "repo", 42); got != "https://github.com/org/repo/pull/42" {
		t.Errorf("PRURL: %s", got)
	}
	if got := CommitURL("org", "repo", "abc123"); got != "https://github.com/org/repo/commit/abc123" {
		t.Errorf("CommitURL: %s", got)
	}
	if got := IssueURL("org", "repo", 7); got != "https://github.com/org/repo/issues/7" {
		t.Errorf("IssueURL: %s", got)
	}
}
