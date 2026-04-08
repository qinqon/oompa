package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

const (
	// botMarker is a hidden HTML comment added to all agent-posted comments
	// so they can be distinguished from manual comments by the same user.
	botMarker = "<!-- ai-agent-bot -->"
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
		if work, exists := a.state.ActiveIssues[issue.Number]; exists {
			// Re-check for PR if we lost track of it
			if work.PRNumber == 0 && work.Status == "implementing" {
				prs, err := a.gh.ListPRsByHead(ctx, a.cfg.Owner, a.cfg.Repo, work.BranchName)
				if err == nil && len(prs) > 0 {
					work.PRNumber = prs[0].Number
					work.Status = "pr-open"
					a.logger.Info("found PR for tracked issue", "issue", issue.Number, "pr", work.PRNumber)
				}
			}
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

		// Build a set of comment IDs that have a reply from our user
		repliedTo := make(map[int64]bool)
		for _, c := range comments {
			if c.InReplyToID != 0 && c.User == a.cfg.GitHubUser {
				repliedTo[c.InReplyToID] = true
			}
		}

		// Filter comments: skip bot-posted, only whitelisted reviewers, skip already-processed
		var humanComments []ReviewComment
		for _, c := range comments {
			// Skip comments posted by the agent itself
			if strings.Contains(c.Body, botMarker) {
				continue
			}
			if !a.isAllowedReviewer(c.User) {
				continue
			}
			// A comment is processed only if we reacted with :eyes: AND replied to it
			if repliedTo[c.ID] {
				if already, err := a.gh.HasPRCommentReaction(ctx, a.cfg.Owner, a.cfg.Repo, c.ID, "eyes", a.cfg.GitHubUser); err == nil && already {
					continue
				}
			}
			humanComments = append(humanComments, c)
		}

		if len(humanComments) == 0 {
			continue
		}

		a.logger.Info("addressing review comments", "pr", work.PRNumber, "count", len(humanComments))

		// Pull latest changes (someone may have pushed manual commits)
		if err := a.worktrees.SyncWorktree(ctx, work.WorktreePath); err != nil {
			a.logger.Error("failed to sync worktree", "pr", work.PRNumber, "error", err)
			continue
		}

		// React with eyes to signal we're processing
		for _, c := range humanComments {
			if err := a.gh.AddPRCommentReaction(ctx, a.cfg.Owner, a.cfg.Repo, c.ID, "eyes"); err != nil {
				a.logger.Warn("failed to add reaction", "comment", c.ID, "error", err)
			}
		}

		prompt := buildReviewResponsePrompt(*work, humanComments, a.cfg.SignedOffBy, a.cfg.Owner, a.cfg.Repo)
		_, err = runClaude(ctx, a.runner, work.WorktreePath, prompt, a.cfg)
		if err != nil {
			a.logger.Error("claude failed to address review", "pr", work.PRNumber, "error", err)
			continue
		}

		// Check if Claude made any code changes by looking for uncommitted or amended changes
		diffOut, _, _ := a.runner.Run(ctx, work.WorktreePath, "git", "diff", "--stat", "origin/main")
		hasChanges := len(strings.TrimSpace(string(diffOut))) > 0

		// Post fallback reply for comments Claude didn't reply to
		updatedComments, _ := a.gh.GetPRReviewComments(ctx, a.cfg.Owner, a.cfg.Repo, work.PRNumber, 0)
		repliedTo = make(map[int64]bool)
		for _, c := range updatedComments {
			if c.InReplyToID != 0 && c.User == a.cfg.GitHubUser {
				repliedTo[c.InReplyToID] = true
			}
		}
		for _, c := range humanComments {
			if repliedTo[c.ID] {
				continue
			}
			fallback := "Reviewed — no code changes needed for this comment.\n\n" + botMarker
			if hasChanges {
				fallback = "Addressed in the latest push.\n\n" + botMarker
			}
			if err := a.gh.ReplyToPRComment(ctx, a.cfg.Owner, a.cfg.Repo, work.PRNumber, c.ID, fallback); err != nil {
				a.logger.Warn("failed to reply to comment", "comment", c.ID, "error", err)
			}
		}

		// Update last seen comment ID
		for _, c := range humanComments {
			if c.ID > work.LastCommentID {
				work.LastCommentID = c.ID
			}
		}
	}
}

const maxCIFixAttempts = 3

// ProcessCIFailures checks CI status for open PRs and invokes Claude to fix failures.
func (a *Agent) ProcessCIFailures(ctx context.Context) {
	for _, work := range a.state.ActiveIssues {
		if work.PRNumber == 0 || work.Status != "pr-open" {
			continue
		}

		if work.CIFixAttempts >= maxCIFixAttempts {
			if work.LastCIStatus != "max-retries-reached" {
				a.logger.Warn("CI fix attempts exhausted", "pr", work.PRNumber, "attempts", work.CIFixAttempts)
				_ = a.gh.AddIssueComment(ctx, a.cfg.Owner, a.cfg.Repo, work.IssueNumber,
					fmt.Sprintf("CI is still failing after %d fix attempts on PR #%d. Human intervention needed.", maxCIFixAttempts, work.PRNumber))
				work.LastCIStatus = "max-retries-reached"
			}
			continue
		}

		headSHA, err := a.gh.GetPRHeadSHA(ctx, a.cfg.Owner, a.cfg.Repo, work.PRNumber)
		if err != nil {
			a.logger.Error("failed to get PR head SHA", "pr", work.PRNumber, "error", err)
			continue
		}

		runs, err := a.gh.GetCheckRuns(ctx, a.cfg.Owner, a.cfg.Repo, headSHA)
		if err != nil {
			a.logger.Error("failed to get check runs", "pr", work.PRNumber, "error", err)
			continue
		}

		// Collect completed failures — act immediately without waiting for all checks
		var failures []CheckRun
		for _, r := range runs {
			if r.Status == "completed" && r.Conclusion == "failure" {
				failures = append(failures, r)
			}
		}

		if len(runs) == 0 || len(failures) == 0 {
			continue
		}

		a.logger.Info("CI failing, invoking Claude to fix", "pr", work.PRNumber, "failures", len(failures), "attempt", work.CIFixAttempts+1)

		// Pull latest changes
		if err := a.worktrees.SyncWorktree(ctx, work.WorktreePath); err != nil {
			a.logger.Error("failed to sync worktree", "pr", work.PRNumber, "error", err)
			continue
		}

		prompt := buildCIFixPrompt(*work, failures, a.cfg.SignedOffBy)
		_, err = runClaude(ctx, a.runner, work.WorktreePath, prompt, a.cfg)
		if err != nil {
			a.logger.Error("claude failed to fix CI", "pr", work.PRNumber, "error", err)
		}

		work.CIFixAttempts++
		work.LastCIStatus = "failure"
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
	}
}

// isAllowedReviewer returns true if the user is in the reviewers whitelist.
// If the whitelist is empty, all users are allowed.
func (a *Agent) isAllowedReviewer(user string) bool {
	if len(a.cfg.Reviewers) == 0 {
		return true
	}
	for _, r := range a.cfg.Reviewers {
		if r == user {
			return true
		}
	}
	return false
}
