package agent

import (
	"context"
	"fmt"
	"io"
	"net/http"
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
}

// GoGitHubClient implements GitHubClient using go-github.
type GoGitHubClient struct {
	client *github.Client
}

// NewGoGitHubClient creates a new client authenticated with the given token.
func NewGoGitHubClient(token string) *GoGitHubClient {
	return &GoGitHubClient{
		client: github.NewClient(nil).WithAuthToken(token),
	}
}

// NewGoGitHubClientFromHTTPClient creates a new client using a custom HTTP client
// (e.g., one with a GitHub App installation transport).
func NewGoGitHubClientFromHTTPClient(httpClient *http.Client) *GoGitHubClient {
	return &GoGitHubClient{
		client: github.NewClient(httpClient),
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
		issues = append(issues, Issue{
			Number: gi.GetNumber(),
			Title:  gi.GetTitle(),
			Body:   gi.GetBody(),
			Labels: labels,
		})
	}
	return issues, nil
}

func (g *GoGitHubClient) GetPRReviewComments(ctx context.Context, owner, repo string, prNumber int, sinceID int64) ([]ReviewComment, error) {
	ghComments, _, err := g.client.PullRequests.ListComments(ctx, owner, repo, prNumber, nil)
	if err != nil {
		return nil, fmt.Errorf("listing PR comments: %w", err)
	}

	var comments []ReviewComment
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
		})
	}
	return comments, nil
}

func (g *GoGitHubClient) GetIssueComments(ctx context.Context, owner, repo string, issueNumber int, sinceID int64) ([]ReviewComment, error) {
	ghComments, _, err := g.client.Issues.ListComments(ctx, owner, repo, issueNumber, nil)
	if err != nil {
		return nil, fmt.Errorf("listing issue comments: %w", err)
	}

	var comments []ReviewComment
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
	comment := &github.IssueComment{Body: github.Ptr(body)}
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

func (g *GoGitHubClient) GetCheckRuns(ctx context.Context, owner, repo, ref string) ([]CheckRun, error) {
	result, _, err := g.client.Checks.ListCheckRunsForRef(ctx, owner, repo, ref, nil)
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
		runs = append(runs, CheckRun{
			ID:         r.GetID(),
			Name:       r.GetName(),
			Status:     r.GetStatus(),
			Conclusion: r.GetConclusion(),
			Output:     output,
		})
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
	// Truncate to last 3000 chars to avoid huge prompts
	if len(log) > 3000 {
		log = "...(truncated)...\n" + log[len(log)-3000:]
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
		Title: github.Ptr(title),
		Body:  github.Ptr(body),
		Head:  github.Ptr(head),
		Base:  github.Ptr(base),
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
