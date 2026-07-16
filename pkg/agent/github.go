package agent

import (
	"log/slog"
	"net/http"

	"github.com/qinqon/oompa/internal/gh"
)

// The GitHub client lives in internal/gh; these aliases keep the
// package-local names used by the agent and cmd/oompa.
type (
	// GitHubClient is the interface the agent uses to talk to GitHub.
	GitHubClient = gh.Client
	// GoGitHubClient is the go-github-backed REST implementation.
	GoGitHubClient = gh.RESTClient
	// DryRunGitHubClient wraps a client, logging mutations instead of applying them.
	DryRunGitHubClient = gh.DryRunClient
	// CachingTransport adds etag-based response caching to a RoundTripper.
	CachingTransport = gh.CachingTransport
	// GitHubAppAuth holds GitHub App authentication state.
	GitHubAppAuth = gh.AppAuth

	// Issue is a GitHub issue.
	Issue = gh.Issue
	// ReviewComment is a PR review or issue comment.
	ReviewComment = gh.ReviewComment
	// PR is a GitHub pull request.
	PR = gh.PR
	// PRReview is a PR review submission.
	PRReview = gh.PRReview
	// CheckRun is a CI check run or commit status.
	CheckRun = gh.CheckRun
	// WorkflowRun is a GitHub Actions workflow run.
	WorkflowRun = gh.WorkflowRun
	// WorkflowJob is a job within a workflow run.
	WorkflowJob = gh.WorkflowJob
)

// NewGoGitHubClient creates a token-authenticated GitHub client.
func NewGoGitHubClient(token string) (*GoGitHubClient, error) {
	return gh.NewRESTClient(token)
}

// NewGoGitHubClientFromHTTPClient creates a GitHub client from a custom HTTP client.
func NewGoGitHubClientFromHTTPClient(httpClient *http.Client) (*GoGitHubClient, error) {
	return gh.NewRESTClientFromHTTPClient(httpClient)
}

// NewDryRunGitHubClient wraps inner, logging mutations instead of applying them.
func NewDryRunGitHubClient(inner GitHubClient, logger *slog.Logger) *DryRunGitHubClient {
	return gh.NewDryRunClient(inner, logger)
}

// NewCachingTransport adds etag caching to base.
func NewCachingTransport(base http.RoundTripper) *CachingTransport {
	return gh.NewCachingTransport(base)
}

// NewGitHubAppAuth creates a GitHub client and token provider from GitHub App credentials.
func NewGitHubAppAuth(appID, installationID int64, privateKey []byte) (*GitHubAppAuth, error) {
	return gh.NewAppAuth(appID, installationID, privateKey)
}
