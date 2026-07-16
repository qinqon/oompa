package gh

import "fmt"

// PRURL returns the web URL of a pull request.
func PRURL(owner, repo string, prNumber int) string {
	return fmt.Sprintf("https://github.com/%s/%s/pull/%d", owner, repo, prNumber)
}

// CommitURL returns the web URL of a commit.
func CommitURL(owner, repo, sha string) string {
	return fmt.Sprintf("https://github.com/%s/%s/commit/%s", owner, repo, sha)
}

// IssueURL returns the web URL of an issue.
func IssueURL(owner, repo string, issueNumber int) string {
	return fmt.Sprintf("https://github.com/%s/%s/issues/%d", owner, repo, issueNumber)
}
