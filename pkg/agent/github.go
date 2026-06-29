package agent

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/go-github/v84/github"
)

// GitHubClient defines all GitHub operations needed by the agent.
type GitHubClient interface {
	ListLabeledIssues(ctx context.Context, owner, repo, label string) ([]Issue, error)
	GetPRReviewComments(ctx context.Context, owner, repo string, prNumber int, sinceID int64) ([]ReviewComment, error)
	GetIssueComments(ctx context.Context, owner, repo string, issueNumber int, sinceID int64) ([]ReviewComment, error)
	GetPRState(ctx context.Context, owner, repo string, prNumber int) (string, error)
	AddIssueComment(ctx context.Context, owner, repo string, issueNumber int, body string) error
	AddLabel(ctx context.Context, owner, repo string, issueNumber int, label string) error
	RemoveLabel(ctx context.Context, owner, repo string, issueNumber int, label string) error
	ListPRsByHead(ctx context.Context, owner, repo, headOwner, branch string) ([]PR, error)
	AddPRCommentReaction(ctx context.Context, owner, repo string, commentID int64, reaction string) error
	AddIssueCommentReaction(ctx context.Context, owner, repo string, commentID int64, reaction string) error
	GetCheckRuns(ctx context.Context, owner, repo, ref string) ([]CheckRun, error)
	GetCheckRunLog(ctx context.Context, owner, repo string, checkRunID int64) (string, error)
	GetPRHeadSHA(ctx context.Context, owner, repo string, prNumber int) (string, error)
	HasPRCommentReaction(ctx context.Context, owner, repo string, commentID int64, reaction, user string) (bool, error)
	ReplyToPRComment(ctx context.Context, owner, repo string, prNumber int, commentID int64, body string) error
	AssignIssue(ctx context.Context, owner, repo string, issueNumber int, user string) error
	UnassignIssue(ctx context.Context, owner, repo string, issueNumber int, user string) error
	GetPRMergeable(ctx context.Context, owner, repo string, prNumber int) (string, error)
	GetPRReviews(ctx context.Context, owner, repo string, prNumber int, sinceID int64) ([]PRReview, error)
	GetPRHeadCommitDate(ctx context.Context, owner, repo string, prNumber int) (time.Time, error)
	CreatePR(ctx context.Context, owner, repo, title, body, head, base string) (int, error)
	HasLinkedPR(ctx context.Context, owner, repo string, issueNumber int) (bool, error)
	GetPR(ctx context.Context, owner, repo string, prNumber int) (PR, error)
	IsPRBehind(ctx context.Context, owner, repo string, prNumber int) (bool, error)
	CreateIssue(ctx context.Context, owner, repo, title, body string, labels []string) (int, error)
	SearchIssues(ctx context.Context, query string) ([]Issue, error)
	GetCommitStatuses(ctx context.Context, owner, repo, ref string) ([]CheckRun, error)
	ListWorkflowRuns(ctx context.Context, owner, repo, workflowID string, status string, limit int, since time.Time) ([]WorkflowRun, error)
	ListWorkflowJobs(ctx context.Context, owner, repo string, runID int64) ([]WorkflowJob, error)
	GetWorkflowJobLogs(ctx context.Context, owner, repo string, jobID int64) (string, error)
	CountCommitsSince(ctx context.Context, owner, repo string, since time.Time) (int, error)
}

// GoGitHubClient implements GitHubClient using go-github.
type GoGitHubClient struct {
	client *github.Client
}

// NewGoGitHubClient creates a new client authenticated with the given token.
// If OOMPA_GITHUB_API_URL is set, the client uses it as the GitHub Enterprise
// base URL (for testing with a fake GitHub server).
func NewGoGitHubClient(token string) *GoGitHubClient {
	httpClient := &http.Client{Transport: NewCachingTransport(http.DefaultTransport)}
	client := github.NewClient(httpClient).WithAuthToken(token)
	if base := os.Getenv("OOMPA_GITHUB_API_URL"); base != "" {
		enterpriseClient, err := client.WithEnterpriseURLs(base, base)
		if err != nil {
			slog.Warn("invalid OOMPA_GITHUB_API_URL, falling back to api.github.com", "url", base, "error", err)
		} else {
			client = enterpriseClient
		}
	}
	return &GoGitHubClient{client: client}
}

// NewGoGitHubClientFromHTTPClient creates a new client using a custom HTTP client
// (e.g., one with a GitHub App installation transport).
func NewGoGitHubClientFromHTTPClient(httpClient *http.Client) *GoGitHubClient {
	transport := httpClient.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	cachedClient := &http.Client{Transport: NewCachingTransport(transport)}
	return &GoGitHubClient{
		client: github.NewClient(cachedClient),
	}
}

func (g *GoGitHubClient) ListLabeledIssues(ctx context.Context, owner, repo, label string) ([]Issue, error) {
	opts := &github.IssueListByRepoOptions{
		Labels: []string{label},
		State:  "open",
	}

	ghIssues, _, err := g.client.Issues.ListByRepo(ctx, owner, repo, opts)
	if err != nil {
		return nil, fmt.Errorf("listing issues: %w", err)
	}

	var issues []Issue
	for _, gi := range ghIssues {
		if gi.IsPullRequest() {
			continue
		}
		var labels []string
		for _, l := range gi.Labels {
			labels = append(labels, l.GetName())
		}
		var assignees []string
		for _, a := range gi.Assignees {
			assignees = append(assignees, a.GetLogin())
		}
		issues = append(issues, Issue{
			Number:    gi.GetNumber(),
			Title:     gi.GetTitle(),
			Body:      gi.GetBody(),
			Labels:    labels,
			Assignees: assignees,
		})
	}
	return issues, nil
}

func (g *GoGitHubClient) GetPRReviewComments(ctx context.Context, owner, repo string, prNumber int, sinceID int64) ([]ReviewComment, error) {
	opts := &github.PullRequestListCommentsOptions{
		ListOptions: github.ListOptions{PerPage: 100},
	}
	var comments []ReviewComment
	for {
		ghComments, resp, err := g.client.PullRequests.ListComments(ctx, owner, repo, prNumber, opts)
		if err != nil {
			return nil, fmt.Errorf("listing PR comments: %w", err)
		}
		for _, c := range ghComments {
			if c.GetID() <= sinceID {
				continue
			}
			comments = append(comments, ReviewComment{
				ID:          c.GetID(),
				InReplyToID: c.GetInReplyTo(),
				User:        c.GetUser().GetLogin(),
				Body:        c.GetBody(),
				Path:        c.GetPath(),
				Line:        c.GetLine(),
				CreatedAt:   c.GetCreatedAt().Time,
			})
		}
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return comments, nil
}

func (g *GoGitHubClient) GetIssueComments(ctx context.Context, owner, repo string, issueNumber int, sinceID int64) ([]ReviewComment, error) {
	opts := &github.IssueListCommentsOptions{
		ListOptions: github.ListOptions{PerPage: 100},
	}
	var comments []ReviewComment
	for {
		ghComments, resp, err := g.client.Issues.ListComments(ctx, owner, repo, issueNumber, opts)
		if err != nil {
			return nil, fmt.Errorf("listing issue comments: %w", err)
		}
		for _, c := range ghComments {
			if c.GetID() <= sinceID {
				continue
			}
			comments = append(comments, ReviewComment{
				ID:   c.GetID(),
				User: c.GetUser().GetLogin(),
				Body: c.GetBody(),
			})
		}
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return comments, nil
}

func (g *GoGitHubClient) GetPRState(ctx context.Context, owner, repo string, prNumber int) (string, error) {
	pr, _, err := g.client.PullRequests.Get(ctx, owner, repo, prNumber)
	if err != nil {
		return "", fmt.Errorf("getting PR state: %w", err)
	}

	if pr.GetMerged() {
		return "merged", nil
	}
	return pr.GetState(), nil
}

func (g *GoGitHubClient) AddIssueComment(ctx context.Context, owner, repo string, issueNumber int, body string) error {
	comment := &github.IssueComment{Body: new(body)}
	_, _, err := g.client.Issues.CreateComment(ctx, owner, repo, issueNumber, comment)
	if err != nil {
		return fmt.Errorf("adding comment: %w", err)
	}
	return nil
}

func (g *GoGitHubClient) AddLabel(ctx context.Context, owner, repo string, issueNumber int, label string) error {
	_, _, err := g.client.Issues.AddLabelsToIssue(ctx, owner, repo, issueNumber, []string{label})
	if err != nil {
		return fmt.Errorf("adding label: %w", err)
	}
	return nil
}

func (g *GoGitHubClient) RemoveLabel(ctx context.Context, owner, repo string, issueNumber int, label string) error {
	_, err := g.client.Issues.RemoveLabelForIssue(ctx, owner, repo, issueNumber, label)
	if err != nil {
		return fmt.Errorf("removing label: %w", err)
	}
	return nil
}

func (g *GoGitHubClient) ListPRsByHead(ctx context.Context, owner, repo, headOwner, branch string) ([]PR, error) {
	head := branch
	if headOwner != "" {
		head = fmt.Sprintf("%s:%s", headOwner, branch)
	}

	prs, err := g.listPRsByHead(ctx, owner, repo, head, branch)
	if err != nil {
		return nil, err
	}
	if len(prs) > 0 || headOwner == "" {
		return prs, nil
	}

	// Retry without owner prefix — GitHub's head filter doesn't match
	// when the fork repo has a different name than the base repo
	// (e.g. head from kubernetes-nmstate-ci against kubernetes-nmstate).
	return g.listPRsByHead(ctx, owner, repo, branch, branch)
}

func (g *GoGitHubClient) listPRsByHead(ctx context.Context, owner, repo, head, branch string) ([]PR, error) {
	opts := &github.PullRequestListOptions{
		Head:  head,
		State: "all",
	}

	ghPRs, _, err := g.client.PullRequests.List(ctx, owner, repo, opts)
	if err != nil {
		return nil, fmt.Errorf("listing PRs: %w", err)
	}

	var prs []PR
	for _, p := range ghPRs {
		// Verify the head ref actually matches — the GitHub API may ignore
		// the head filter when the branch doesn't exist on the upstream repo
		if p.GetHead().GetRef() != branch {
			continue
		}
		prs = append(prs, PR{
			Number: p.GetNumber(),
			Title:  p.GetTitle(),
			State:  p.GetState(),
			Merged: p.GetMerged(),
			Head:   p.GetHead().GetRef(),
		})
	}
	return prs, nil
}

func (g *GoGitHubClient) AddPRCommentReaction(ctx context.Context, owner, repo string, commentID int64, reaction string) error {
	_, _, err := g.client.Reactions.CreatePullRequestCommentReaction(ctx, owner, repo, commentID, reaction)
	if err != nil {
		return fmt.Errorf("adding reaction: %w", err)
	}
	return nil
}

func (g *GoGitHubClient) AddIssueCommentReaction(ctx context.Context, owner, repo string, commentID int64, reaction string) error {
	_, _, err := g.client.Reactions.CreateIssueCommentReaction(ctx, owner, repo, commentID, reaction)
	if err != nil {
		return fmt.Errorf("adding issue comment reaction: %w", err)
	}
	return nil
}

func (g *GoGitHubClient) GetCheckRuns(ctx context.Context, owner, repo, ref string) ([]CheckRun, error) {
	opts := &github.ListCheckRunsOptions{
		ListOptions: github.ListOptions{PerPage: 100},
	}
	result, _, err := g.client.Checks.ListCheckRunsForRef(ctx, owner, repo, ref, opts)
	if err != nil {
		return nil, fmt.Errorf("listing check runs: %w", err)
	}

	var runs []CheckRun
	for _, r := range result.CheckRuns {
		var output string
		if r.Output != nil {
			output = r.Output.GetText()
			if output == "" {
				output = r.Output.GetSummary()
			}
		}
		var completedAt time.Time
		if t := r.GetCompletedAt(); !t.IsZero() {
			completedAt = t.Time
		}
		runs = append(runs, CheckRun{
			ID:          r.GetID(),
			Name:        r.GetName(),
			Status:      r.GetStatus(),
			Conclusion:  r.GetConclusion(),
			Output:      output,
			HTMLURL:     r.GetHTMLURL(),
			CompletedAt: completedAt,
		})
	}
	return runs, nil
}

// GetCommitStatuses queries the Combined Status API and returns failures as
// CheckRun values. Commit-status entries have ID==0 (no check-run ID exists)
// and Output contains the status description and target_url (a link to logs,
// e.g. Prow job page) rather than log text. Callers must not pass ID==0 to
// GetCheckRunLog.
func (g *GoGitHubClient) GetCommitStatuses(ctx context.Context, owner, repo, ref string) ([]CheckRun, error) {
	var runs []CheckRun
	opts := &github.ListOptions{PerPage: 100}
	for {
		status, resp, err := g.client.Repositories.GetCombinedStatus(ctx, owner, repo, ref, opts)
		if err != nil {
			return nil, fmt.Errorf("getting combined status: %w", err)
		}
		for _, s := range status.Statuses {
			if s.GetState() == "failure" || s.GetState() == "error" {
				output := s.GetTargetURL()
				if desc := s.GetDescription(); desc != "" {
					output = desc + "\n" + output
				}
				runs = append(runs, CheckRun{
					Name:       s.GetContext(),
					Status:     "completed",
					Conclusion: "failure",
					Output:     output,
				})
			}
		}
		if resp == nil || resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return runs, nil
}

func (g *GoGitHubClient) GetCheckRunLog(ctx context.Context, owner, repo string, checkRunID int64) (string, error) {
	// The check run ID is the same as the job ID for GitHub Actions
	_, resp, err := g.client.Actions.GetWorkflowJobLogs(ctx, owner, repo, checkRunID, 4)
	if err != nil {
		return "", fmt.Errorf("getting job logs: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading job logs: %w", err)
	}

	log := string(data)
	// Truncate to last 50000 chars to keep enough context for investigation
	// while avoiding excessively large prompts
	const maxLogSize = 50000
	if len(log) > maxLogSize {
		log = "...(truncated)...\n" + log[len(log)-maxLogSize:]
	}
	return log, nil
}

func (g *GoGitHubClient) HasPRCommentReaction(ctx context.Context, owner, repo string, commentID int64, reaction, user string) (bool, error) {
	reactions, _, err := g.client.Reactions.ListPullRequestCommentReactions(ctx, owner, repo, commentID, nil)
	if err != nil {
		return false, fmt.Errorf("listing reactions: %w", err)
	}
	for _, r := range reactions {
		if r.GetContent() == reaction && r.GetUser().GetLogin() == user {
			return true, nil
		}
	}
	return false, nil
}

func (g *GoGitHubClient) ReplyToPRComment(ctx context.Context, owner, repo string, prNumber int, commentID int64, body string) error {
	_, _, err := g.client.PullRequests.CreateCommentInReplyTo(ctx, owner, repo, prNumber, body, commentID)
	if err != nil {
		return fmt.Errorf("replying to PR comment: %w", err)
	}
	return nil
}

func (g *GoGitHubClient) GetPRHeadSHA(ctx context.Context, owner, repo string, prNumber int) (string, error) {
	pr, _, err := g.client.PullRequests.Get(ctx, owner, repo, prNumber)
	if err != nil {
		return "", fmt.Errorf("getting PR head SHA: %w", err)
	}
	return pr.GetHead().GetSHA(), nil
}

func (g *GoGitHubClient) AssignIssue(ctx context.Context, owner, repo string, issueNumber int, user string) error {
	_, _, err := g.client.Issues.AddAssignees(ctx, owner, repo, issueNumber, []string{user})
	if err != nil {
		return fmt.Errorf("assigning issue: %w", err)
	}
	return nil
}

func (g *GoGitHubClient) UnassignIssue(ctx context.Context, owner, repo string, issueNumber int, user string) error {
	_, _, err := g.client.Issues.RemoveAssignees(ctx, owner, repo, issueNumber, []string{user})
	if err != nil {
		return fmt.Errorf("unassigning issue: %w", err)
	}
	return nil
}

func (g *GoGitHubClient) GetPRMergeable(ctx context.Context, owner, repo string, prNumber int) (string, error) {
	pr, _, err := g.client.PullRequests.Get(ctx, owner, repo, prNumber)
	if err != nil {
		return "", fmt.Errorf("getting PR mergeable state: %w", err)
	}
	return pr.GetMergeableState(), nil
}

func (g *GoGitHubClient) GetPRReviews(ctx context.Context, owner, repo string, prNumber int, sinceID int64) ([]PRReview, error) {
	ghReviews, _, err := g.client.PullRequests.ListReviews(ctx, owner, repo, prNumber, nil)
	if err != nil {
		return nil, fmt.Errorf("listing PR reviews: %w", err)
	}

	var reviews []PRReview
	for _, r := range ghReviews {
		if r.GetID() <= sinceID {
			continue
		}
		body := strings.TrimSpace(r.GetBody())
		if body == "" {
			continue
		}
		reviews = append(reviews, PRReview{
			ID:          r.GetID(),
			User:        r.GetUser().GetLogin(),
			State:       r.GetState(),
			Body:        body,
			SubmittedAt: r.GetSubmittedAt().Time,
		})
	}
	return reviews, nil
}

func (g *GoGitHubClient) GetPRHeadCommitDate(ctx context.Context, owner, repo string, prNumber int) (time.Time, error) {
	pr, _, err := g.client.PullRequests.Get(ctx, owner, repo, prNumber)
	if err != nil {
		return time.Time{}, fmt.Errorf("getting PR: %w", err)
	}
	sha := pr.GetHead().GetSHA()
	commit, _, err := g.client.Repositories.GetCommit(ctx, owner, repo, sha, nil)
	if err != nil {
		return time.Time{}, fmt.Errorf("getting commit: %w", err)
	}
	return commit.GetCommit().GetCommitter().GetDate().Time, nil
}

func (g *GoGitHubClient) CreatePR(ctx context.Context, owner, repo, title, body, head, base string) (int, error) {
	pr, _, err := g.client.PullRequests.Create(ctx, owner, repo, &github.NewPullRequest{
		Title: new(title),
		Body:  new(body),
		Head:  new(head),
		Base:  new(base),
	})
	if err != nil {
		return 0, fmt.Errorf("creating PR: %w", err)
	}
	return pr.GetNumber(), nil
}

func (g *GoGitHubClient) HasLinkedPR(ctx context.Context, owner, repo string, issueNumber int) (bool, error) {
	events, _, err := g.client.Issues.ListIssueTimeline(ctx, owner, repo, issueNumber, nil)
	if err != nil {
		return false, fmt.Errorf("listing issue timeline: %w", err)
	}

	for _, e := range events {
		if e.GetEvent() != "cross-referenced" {
			continue
		}
		src := e.GetSource()
		if src == nil || src.GetIssue() == nil {
			continue
		}
		// GitHub's timeline returns PRs as issues with a PullRequestLinks field
		if src.GetIssue().IsPullRequest() && src.GetIssue().GetState() == "open" {
			return true, nil
		}
	}
	return false, nil
}

func (g *GoGitHubClient) GetPR(ctx context.Context, owner, repo string, prNumber int) (PR, error) {
	pr, _, err := g.client.PullRequests.Get(ctx, owner, repo, prNumber)
	if err != nil {
		return PR{}, fmt.Errorf("getting PR: %w", err)
	}
	return PR{
		Number: pr.GetNumber(),
		Title:  pr.GetTitle(),
		State:  pr.GetState(),
		Merged: pr.GetMerged(),
		Head:   pr.GetHead().GetRef(),
	}, nil
}

func (g *GoGitHubClient) IsPRBehind(ctx context.Context, owner, repo string, prNumber int) (bool, error) {
	pr, _, err := g.client.PullRequests.Get(ctx, owner, repo, prNumber)
	if err != nil {
		return false, fmt.Errorf("getting PR: %w", err)
	}

	base := pr.GetBase().GetRef()
	headLabel := pr.GetHead().GetLabel() // "owner:branch"

	comparison, _, err := g.client.Repositories.CompareCommits(ctx, owner, repo, base, headLabel, nil)
	if err != nil {
		return false, fmt.Errorf("comparing commits: %w", err)
	}

	return comparison.GetBehindBy() > 0, nil
}

func (g *GoGitHubClient) CreateIssue(ctx context.Context, owner, repo, title, body string, labels []string) (int, error) {
	req := &github.IssueRequest{
		Title:  &title,
		Body:   &body,
		Labels: &labels,
	}
	issue, _, err := g.client.Issues.Create(ctx, owner, repo, req)
	if err != nil {
		return 0, fmt.Errorf("creating issue: %w", err)
	}
	return issue.GetNumber(), nil
}

func (g *GoGitHubClient) SearchIssues(ctx context.Context, query string) ([]Issue, error) {
	var allIssues []Issue
	opts := &github.SearchOptions{
		ListOptions: github.ListOptions{
			PerPage: 100,
		},
	}

	for {
		result, resp, err := g.client.Search.Issues(ctx, query, opts)
		if err != nil {
			return nil, fmt.Errorf("searching issues: %w", err)
		}

		for _, ghIssue := range result.Issues {
			if ghIssue.IsPullRequest() {
				continue
			}
			var labels []string
			for _, l := range ghIssue.Labels {
				labels = append(labels, l.GetName())
			}
			allIssues = append(allIssues, Issue{
				Number: ghIssue.GetNumber(),
				Title:  ghIssue.GetTitle(),
				Body:   ghIssue.GetBody(),
				Labels: labels,
			})
		}

		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	return allIssues, nil
}

// GetAuthenticatedUser returns the login, name, and email of the authenticated user.
func (g *GoGitHubClient) GetAuthenticatedUser(ctx context.Context) (login, name, email string, err error) {
	user, _, err := g.client.Users.Get(ctx, "")
	if err != nil {
		return "", "", "", fmt.Errorf("getting authenticated user: %w", err)
	}

	login = user.GetLogin()

	name = user.GetName()
	if name == "" {
		name = login
	}

	email = user.GetEmail()
	if email == "" {
		email = fmt.Sprintf("%s@users.noreply.github.com", login)
	}

	return login, name, email, nil
}

// GetLatestReleaseSHA returns the target commit SHA of the latest GitHub release.
func (g *GoGitHubClient) GetLatestReleaseSHA(ctx context.Context, owner, repo string) (string, error) {
	release, _, err := g.client.Repositories.GetLatestRelease(ctx, owner, repo)
	if err != nil {
		return "", fmt.Errorf("getting latest release: %w", err)
	}
	return release.GetTargetCommitish(), nil
}

// ListWorkflowRuns lists recent workflow runs filtered by status.
// When since is non-zero, the GitHub API's created filter is used for
// server-side date filtering (e.g. ">=2026-06-15T09:00:00Z"). Results are
// paginated when the window exceeds one page.
func (g *GoGitHubClient) ListWorkflowRuns(ctx context.Context, owner, repo, workflowID, status string, limit int, since time.Time) ([]WorkflowRun, error) {
	perPage := min(limit, 100)

	opts := &github.ListWorkflowRunsOptions{
		Status: status,
		ListOptions: github.ListOptions{
			PerPage: perPage,
		},
	}

	// Apply server-side date filter when a lookback window is specified.
	if !since.IsZero() {
		opts.Created = ">=" + since.UTC().Format(time.RFC3339)
	}

	var workflowRuns []WorkflowRun
	for {
		runs, resp, err := g.client.Actions.ListWorkflowRunsByFileName(ctx, owner, repo, workflowID, opts)
		if err != nil {
			return nil, fmt.Errorf("listing workflow runs: %w", err)
		}

		for _, run := range runs.WorkflowRuns {
			workflowRuns = append(workflowRuns, WorkflowRun{
				ID:           run.GetID(),
				Status:       run.GetStatus(),
				Conclusion:   run.GetConclusion(),
				CreatedAt:    run.GetCreatedAt().Time,
				HTMLURL:      run.GetHTMLURL(),
				Event:        run.GetEvent(),
				HeadBranch:   run.GetHeadBranch(),
				DisplayTitle: run.GetDisplayTitle(),
			})
		}

		// Stop if we have enough results or no more pages
		if len(workflowRuns) >= limit || resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	// Trim to limit
	if len(workflowRuns) > limit {
		workflowRuns = workflowRuns[:limit]
	}

	return workflowRuns, nil
}

// ListWorkflowJobs lists jobs for a specific workflow run.
func (g *GoGitHubClient) ListWorkflowJobs(ctx context.Context, owner, repo string, runID int64) ([]WorkflowJob, error) {
	opts := &github.ListWorkflowJobsOptions{}

	jobs, _, err := g.client.Actions.ListWorkflowJobs(ctx, owner, repo, runID, opts)
	if err != nil {
		return nil, fmt.Errorf("listing workflow jobs: %w", err)
	}

	var workflowJobs []WorkflowJob
	for _, job := range jobs.Jobs {
		workflowJobs = append(workflowJobs, WorkflowJob{
			ID:         job.GetID(),
			Name:       job.GetName(),
			Conclusion: job.GetConclusion(),
		})
	}

	return workflowJobs, nil
}

// CountCommitsSince returns the number of commits on the default branch since the given time.
// Uses a small page size since callers typically only need to know whether the count
// exceeds a small threshold, avoiding unnecessary API calls on very active repos.
func (g *GoGitHubClient) CountCommitsSince(ctx context.Context, owner, repo string, since time.Time) (int, error) {
	opts := &github.CommitsListOptions{
		Since:       since,
		ListOptions: github.ListOptions{PerPage: 10},
	}

	var total int
	for {
		commits, resp, err := g.client.Repositories.ListCommits(ctx, owner, repo, opts)
		if err != nil {
			return 0, fmt.Errorf("counting commits since %s: %w", since.Format(time.RFC3339), err)
		}
		total += len(commits)
		// Short-circuit: stop paginating once we have enough to exceed any
		// reasonable threshold — callers only need to know "quiet vs active".
		if total > rebaseQuietThreshold {
			return total, nil
		}
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return total, nil
}

// GetWorkflowJobLogs fetches the logs for a specific workflow job.
func (g *GoGitHubClient) GetWorkflowJobLogs(ctx context.Context, owner, repo string, jobID int64) (string, error) {
	logURL, _, err := g.client.Actions.GetWorkflowJobLogs(ctx, owner, repo, jobID, 2)
	if err != nil {
		return "", fmt.Errorf("getting workflow job logs: %w", err)
	}

	// Fetch the log content from the redirect URL
	req, err := http.NewRequestWithContext(ctx, "GET", logURL.String(), http.NoBody)
	if err != nil {
		return "", fmt.Errorf("creating log request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetching log: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading log: %w", err)
	}

	return string(body), nil
}
