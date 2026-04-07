package agent

import (
	"context"
	"fmt"

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
	ListPRsByHead(ctx context.Context, owner, repo, branch string) ([]PR, error)
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
			ID:   c.GetID(),
			User: c.GetUser().GetLogin(),
			Body: c.GetBody(),
			Path: c.GetPath(),
			Line: c.GetLine(),
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

func (g *GoGitHubClient) ListPRsByHead(ctx context.Context, owner, repo, branch string) ([]PR, error) {
	opts := &github.PullRequestListOptions{
		Head: fmt.Sprintf("%s:%s", owner, branch),
	}

	ghPRs, _, err := g.client.PullRequests.List(ctx, owner, repo, opts)
	if err != nil {
		return nil, fmt.Errorf("listing PRs: %w", err)
	}

	var prs []PR
	for _, p := range ghPRs {
		prs = append(prs, PR{
			Number: p.GetNumber(),
			State:  p.GetState(),
			Merged: p.GetMerged(),
			Head:   p.GetHead().GetRef(),
		})
	}
	return prs, nil
}
