package agent

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// Agent holds all dependencies and runs the main processing loop.
type Agent struct {
	gh        GitHubClient
	runner    CommandRunner
	worktrees WorktreeManager
	state     *State
	cfg       Config
	logger    *slog.Logger
}

// NewAgent creates a new Agent with all dependencies wired.
func NewAgent(gh GitHubClient, runner CommandRunner, worktrees WorktreeManager, state *State, cfg Config, logger *slog.Logger) *Agent {
	return &Agent{
		gh:        gh,
		runner:    runner,
		worktrees: worktrees,
		state:     state,
		cfg:       cfg,
		logger:    logger,
	}
}

// ProcessNewIssues finds labeled issues and spawns Claude to implement fixes.
func (a *Agent) ProcessNewIssues(ctx context.Context) {
	issues, err := a.gh.ListLabeledIssues(ctx, a.cfg.Owner, a.cfg.Repo, a.cfg.Label)
	if err != nil {
		a.logger.Error("failed to list issues", "error", err)
		return
	}

	for _, issue := range issues {
		if _, exists := a.state.ActiveIssues[issue.Number]; exists {
			a.logger.Debug("skipping already tracked issue", "issue", issue.Number)
			continue
		}

		a.logger.Info("processing new issue", "issue", issue.Number, "title", issue.Title)

		branchName := fmt.Sprintf("ai/issue-%d", issue.Number)

		if err := a.worktrees.EnsureRepoCloned(ctx); err != nil {
			a.logger.Error("failed to ensure repo cloned", "error", err)
			return
		}

		worktreePath, err := a.worktrees.CreateWorktree(ctx, branchName)
		if err != nil {
			a.logger.Error("failed to create worktree", "issue", issue.Number, "error", err)
			continue
		}

		work := &IssueWork{
			IssueNumber:  issue.Number,
			IssueTitle:   issue.Title,
			WorktreePath: worktreePath,
			BranchName:   branchName,
			Status:       "implementing",
			CreatedAt:    time.Now(),
		}

		prompt := buildImplementationPrompt(issue, a.cfg.SignedOffBy)
		_, err = runClaude(ctx, a.runner, worktreePath, prompt, a.cfg)
		if err != nil {
			a.logger.Error("claude failed", "issue", issue.Number, "error", err)
			work.Status = "failed"
			a.state.ActiveIssues[issue.Number] = work

			_ = a.gh.AddLabel(ctx, a.cfg.Owner, a.cfg.Repo, issue.Number, "ai-failed")
			_ = a.gh.AddIssueComment(ctx, a.cfg.Owner, a.cfg.Repo, issue.Number,
				fmt.Sprintf("AI agent failed to implement this issue: %v", err))

			if err := a.state.Save(); err != nil {
				a.logger.Error("failed to save state", "error", err)
			}
			continue
		}

		// Find the PR created by Claude
		prs, err := a.gh.ListPRsByHead(ctx, a.cfg.Owner, a.cfg.Repo, branchName)
		if err != nil {
			a.logger.Error("failed to list PRs", "issue", issue.Number, "error", err)
		} else if len(prs) > 0 {
			work.PRNumber = prs[0].Number
			work.Status = "pr-open"
		}

		a.state.ActiveIssues[issue.Number] = work
		if err := a.state.Save(); err != nil {
			a.logger.Error("failed to save state", "error", err)
		}
	}
}

// ProcessReviewComments checks for new review comments and runs Claude to address them.
func (a *Agent) ProcessReviewComments(ctx context.Context) {
	for _, work := range a.state.ActiveIssues {
		if work.PRNumber == 0 || work.Status != "pr-open" {
			continue
		}

		comments, err := a.gh.GetPRReviewComments(ctx, a.cfg.Owner, a.cfg.Repo, work.PRNumber, work.LastCommentID)
		if err != nil {
			a.logger.Error("failed to get PR comments", "pr", work.PRNumber, "error", err)
			continue
		}

		// Filter out bot comments
		var humanComments []ReviewComment
		for _, c := range comments {
			if c.User == a.cfg.Owner+"[bot]" || c.User == "github-actions[bot]" {
				continue
			}
			humanComments = append(humanComments, c)
		}

		if len(humanComments) == 0 {
			continue
		}

		a.logger.Info("addressing review comments", "pr", work.PRNumber, "count", len(humanComments))

		prompt := buildReviewResponsePrompt(*work, humanComments, a.cfg.SignedOffBy)
		_, err = runClaude(ctx, a.runner, work.WorktreePath, prompt, a.cfg)
		if err != nil {
			a.logger.Error("claude failed to address review", "pr", work.PRNumber, "error", err)
			continue
		}

		// Update last seen comment ID
		for _, c := range humanComments {
			if c.ID > work.LastCommentID {
				work.LastCommentID = c.ID
			}
		}

		if err := a.state.Save(); err != nil {
			a.logger.Error("failed to save state", "error", err)
		}
	}
}

// CleanupDone removes worktrees for merged or closed PRs.
func (a *Agent) CleanupDone(ctx context.Context) {
	for issueNum, work := range a.state.ActiveIssues {
		if work.PRNumber == 0 {
			continue
		}

		state, err := a.gh.GetPRState(ctx, a.cfg.Owner, a.cfg.Repo, work.PRNumber)
		if err != nil {
			a.logger.Error("failed to get PR state", "pr", work.PRNumber, "error", err)
			continue
		}

		if state != "merged" && state != "closed" {
			continue
		}

		a.logger.Info("cleaning up done PR", "pr", work.PRNumber, "state", state)

		if err := a.worktrees.RemoveWorktree(ctx, work.WorktreePath); err != nil {
			a.logger.Error("failed to remove worktree", "path", work.WorktreePath, "error", err)
		}

		delete(a.state.ActiveIssues, issueNum)

		if err := a.state.Save(); err != nil {
			a.logger.Error("failed to save state", "error", err)
		}
	}
}
