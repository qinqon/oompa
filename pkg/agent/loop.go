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
	tokenFunc func(context.Context) (string, error) // optional: provides fresh GitHub tokens (for App auth)
}

// SetTokenFunc sets a function that provides fresh GitHub tokens.
// Used with GitHub App authentication where installation tokens expire after ~1 hour.
func (a *Agent) SetTokenFunc(fn func(context.Context) (string, error)) {
	a.tokenFunc = fn
}

// RefreshToken updates the GitHub token if a token function is set.
// Call this before each poll cycle to ensure the token is fresh.
func (a *Agent) RefreshToken(ctx context.Context) error {
	if a.tokenFunc == nil {
		return nil
	}
	token, err := a.tokenFunc(ctx)
	if err != nil {
		return fmt.Errorf("refreshing GitHub token: %w", err)
	}
	a.cfg.GitHubToken = token
	return nil
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
				prs, err := a.gh.ListPRsByHead(ctx, a.cfg.Owner, a.cfg.Repo, a.cfg.GitHubHeadOwner, work.BranchName)
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

		// Check if a PR already exists for this issue
		prs, err := a.gh.ListPRsByHead(ctx, a.cfg.Owner, a.cfg.Repo, a.cfg.GitHubHeadOwner, branchName)
		if err == nil && len(prs) > 0 {
			a.logger.Info("PR already exists for issue", "issue", issue.Number, "pr", prs[0].Number)
			a.state.ActiveIssues[issue.Number] = &IssueWork{
				IssueNumber: issue.Number,
				IssueTitle:  issue.Title,
				BranchName:  branchName,
				PRNumber:    prs[0].Number,
				Status:      "pr-open",
				CreatedAt:   time.Now(),
			}
			continue
		}

		// Only post in-progress comment if we haven't already
		if !a.hasExistingBotComment(ctx, issue.Number, "working on this issue") {
			if err := a.gh.AssignIssue(ctx, a.cfg.Owner, a.cfg.Repo, issue.Number, a.cfg.GitHubUser); err != nil {
				a.logger.Warn("failed to assign issue", "issue", issue.Number, "error", err)
			}
			if err := a.gh.AddIssueComment(ctx, a.cfg.Owner, a.cfg.Repo, issue.Number,
				fmt.Sprintf("AI agent is working on this issue. A PR will be created shortly.\n\n%s", botMarker)); err != nil {
				a.logger.Warn("failed to add in-progress comment", "issue", issue.Number, "error", err)
			}
		}

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

		prompt := buildImplementationPrompt(issue, a.cfg.Owner, a.cfg.Repo)
		_, err = runClaude(ctx, a.runner, worktreePath, prompt, a.cfg, a.logger, false)
		if err != nil {
			a.logger.Error("claude failed", "issue", issue.Number, "error", err)
			work.Status = "failed"
			a.state.ActiveIssues[issue.Number] = work

			_ = a.gh.UnassignIssue(ctx, a.cfg.Owner, a.cfg.Repo, issue.Number, a.cfg.GitHubUser)
			_ = a.gh.AddLabel(ctx, a.cfg.Owner, a.cfg.Repo, issue.Number, "ai-failed")
			_ = a.gh.AddIssueComment(ctx, a.cfg.Owner, a.cfg.Repo, issue.Number,
				fmt.Sprintf("AI agent failed to implement this issue: %v", err))
			continue
		}

		_ = a.gh.UnassignIssue(ctx, a.cfg.Owner, a.cfg.Repo, issue.Number, a.cfg.GitHubUser)

		// Commit, push, and create PR
		if a.hasUncommittedChanges(ctx, worktreePath) {
			commitMsg := fmt.Sprintf("%s\n\nFixes: https://github.com/%s/%s/issues/%d",
				issue.Title, a.cfg.Owner, a.cfg.Repo, issue.Number)
			if err := a.gitCommitAll(ctx, worktreePath, commitMsg); err != nil {
				a.logger.Error("failed to commit", "issue", issue.Number, "error", err)
				continue
			}
		}

		if err := a.gitPush(ctx, worktreePath, true); err != nil {
			a.logger.Error("failed to push", "issue", issue.Number, "error", err)
			continue
		}

		prTitle := issue.Title
		prBody := fmt.Sprintf("Fixes #%d\n\n%s", issue.Number, botMarker)
		head := branchName
		if a.cfg.GitHubHeadOwner != a.cfg.Owner {
			head = fmt.Sprintf("%s:%s", a.cfg.GitHubHeadOwner, branchName)
		}
		prNumber, err := a.gh.CreatePR(ctx, a.cfg.Owner, a.cfg.Repo, prTitle, prBody, head, "main")
		if err != nil {
			a.logger.Error("failed to create PR", "issue", issue.Number, "error", err)
		} else {
			work.PRNumber = prNumber
			work.Status = "pr-open"
			a.logger.Info("created PR", "issue", issue.Number, "pr", prNumber)
		}

		a.state.ActiveIssues[issue.Number] = work
	}
}

// ProcessReviewComments checks for new review comments and review bodies, then runs Claude to address them.
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

		// Filter comments: skip replies, skip bot-posted, only whitelisted reviewers, skip already-processed
		var humanComments []ReviewComment
		for _, c := range comments {
			// Skip replies — only process top-level review comments
			if c.InReplyToID != 0 {
				continue
			}
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

		// Fetch PR review bodies (approve, request changes, etc.)
		reviews, err := a.gh.GetPRReviews(ctx, a.cfg.Owner, a.cfg.Repo, work.PRNumber, work.LastReviewID)
		if err != nil {
			a.logger.Warn("failed to get PR reviews", "pr", work.PRNumber, "error", err)
		}

		// Filter reviews: skip bot-posted, only whitelisted reviewers, skip already-addressed
		var humanReviews []PRReview
		if len(reviews) > 0 {
			// Get the PR head commit date to determine if reviews were already addressed
			headCommitDate, _ := a.gh.GetPRHeadCommitDate(ctx, a.cfg.Owner, a.cfg.Repo, work.PRNumber)

			for _, r := range reviews {
				if strings.Contains(r.Body, botMarker) {
					continue
				}
				if !a.isAllowedReviewer(r.User) {
					continue
				}
				// Skip reviews that were submitted before the latest push
				if !headCommitDate.IsZero() && r.SubmittedAt.Before(headCommitDate) {
					continue
				}
				humanReviews = append(humanReviews, r)
			}
		}

		if len(humanComments) == 0 && len(humanReviews) == 0 {
			continue
		}

		a.logger.Info("addressing review feedback", "pr", work.PRNumber, "comments", len(humanComments), "reviews", len(humanReviews))

		if err := a.ensureWorktreeReady(ctx, work); err != nil {
			a.logger.Error("failed to prepare worktree", "pr", work.PRNumber, "error", err)
			continue
		}

		// React with eyes to signal we're processing inline comments
		for _, c := range humanComments {
			if err := a.gh.AddPRCommentReaction(ctx, a.cfg.Owner, a.cfg.Repo, c.ID, "eyes"); err != nil {
				a.logger.Warn("failed to add reaction", "comment", c.ID, "error", err)
			}
		}


		prompt := buildReviewResponsePrompt(*work, humanComments, humanReviews, a.cfg.Owner, a.cfg.Repo)
		_, err = runClaude(ctx, a.runner, work.WorktreePath, prompt, a.cfg, a.logger, true)
		if err != nil {
			a.logger.Error("claude failed to address review", "pr", work.PRNumber, "error", err)
			continue
		}

		// Amend and push if Claude made changes
		hasChanges := a.hasUncommittedChanges(ctx, work.WorktreePath)
		if hasChanges {
			if err := a.gitAmendAll(ctx, work.WorktreePath); err != nil {
				a.logger.Error("failed to amend commit", "pr", work.PRNumber, "error", err)
			} else if err := a.gitPush(ctx, work.WorktreePath, true); err != nil {
				a.logger.Error("failed to push", "pr", work.PRNumber, "error", err)
			}
		}

		// Post fallback reply for inline comments Claude didn't reply to
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
				_ = a.gh.AddIssueComment(ctx, a.cfg.Owner, a.cfg.Repo, work.PRNumber,
					fmt.Sprintf("CI is still failing after %d fix attempts. Human intervention needed.\n\n%s", maxCIFixAttempts, botMarker))
				work.LastCIStatus = "max-retries-reached"
			}
			continue
		}

		headSHA, err := a.gh.GetPRHeadSHA(ctx, a.cfg.Owner, a.cfg.Repo, work.PRNumber)
		if err != nil {
			a.logger.Error("failed to get PR head SHA", "pr", work.PRNumber, "error", err)
			continue
		}

		// Check if we already investigated this SHA by looking for a bot comment
		if a.alreadyCheckedCI(ctx, work.PRNumber, headSHA) {
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

		// Fetch logs for each failing check
		for i, f := range failures {
			if f.Output == "" {
				log, err := a.gh.GetCheckRunLog(ctx, a.cfg.Owner, a.cfg.Repo, f.ID)
				if err != nil {
					a.logger.Warn("failed to get check run log", "check", f.Name, "error", err)
				} else {
					failures[i].Output = log
				}
			}
		}

		a.logger.Info("CI failing, investigating", "pr", work.PRNumber, "failures", len(failures), "attempt", work.CIFixAttempts+1)

		if err := a.ensureWorktreeReady(ctx, work); err != nil {
			a.logger.Error("failed to prepare worktree", "pr", work.PRNumber, "error", err)
			continue
		}

		// Get PR diff to help Claude determine if failure is related
		diffOut, _, _ := a.runner.Run(ctx, work.WorktreePath, "git", "diff", "--stat", "origin/main")
		diff := string(diffOut)

		prompt := buildCIFixPrompt(*work, failures, diff)
		result, err := runClaude(ctx, a.runner, work.WorktreePath, prompt, a.cfg, a.logger, true)
		if err != nil {
			a.logger.Error("claude failed to investigate CI", "pr", work.PRNumber, "error", err)
			work.CIFixAttempts++
			work.LastCIStatus = "failure"
			_ = a.gh.AddIssueComment(ctx, a.cfg.Owner, a.cfg.Repo, work.PRNumber,
				fmt.Sprintf("CI investigation failed for commit %s: %v\n\n%s", shortSHA(headSHA), err, botMarker))
			continue
		}

		if strings.HasPrefix(strings.TrimSpace(result.Result), "UNRELATED") {
			a.logger.Info("CI failure is unrelated to PR changes", "pr", work.PRNumber)
			explanation := strings.TrimPrefix(strings.TrimSpace(result.Result), "UNRELATED")
			explanation = strings.TrimSpace(explanation)
			comment := fmt.Sprintf("CI check failed on commit %s but appears unrelated to this PR's changes.\n\n%s\n\n%s", shortSHA(headSHA), explanation, botMarker)
			_ = a.gh.AddIssueComment(ctx, a.cfg.Owner, a.cfg.Repo, work.PRNumber, comment)
			work.LastCIStatus = "unrelated-failure"
			continue
		}

		// Claude said RELATED — amend and push if there are changes
		pushed := false
		if a.hasUncommittedChanges(ctx, work.WorktreePath) {
			if err := a.gitAmendAll(ctx, work.WorktreePath); err != nil {
				a.logger.Error("failed to amend commit for CI fix", "pr", work.PRNumber, "error", err)
			} else if err := a.gitPush(ctx, work.WorktreePath, true); err != nil {
				a.logger.Error("failed to push CI fix", "pr", work.PRNumber, "error", err)
			} else {
				pushed = true
			}
		}

		if pushed {
			a.logger.Info("CI failure is related, pushed a fix", "pr", work.PRNumber)
			_ = a.gh.AddIssueComment(ctx, a.cfg.Owner, a.cfg.Repo, work.PRNumber,
				fmt.Sprintf("CI was failing on commit %s. Pushed a fix.\n\n%s", shortSHA(headSHA), botMarker))
		} else {
			a.logger.Warn("Claude said RELATED but no changes to push", "pr", work.PRNumber)
			_ = a.gh.AddIssueComment(ctx, a.cfg.Owner, a.cfg.Repo, work.PRNumber,
				fmt.Sprintf("CI is failing on commit %s. Investigated but could not push a fix.\n\n%s", shortSHA(headSHA), botMarker))
		}
		work.CIFixAttempts++
		work.LastCIStatus = "failure"
	}
}

const maxConflictFixAttempts = 2

// ProcessConflicts checks for merge conflicts and tries to rebase/resolve them.
func (a *Agent) ProcessConflicts(ctx context.Context) {
	for _, work := range a.state.ActiveIssues {
		if work.PRNumber == 0 || work.Status != "pr-open" {
			continue
		}

		mergeState, err := a.gh.GetPRMergeable(ctx, a.cfg.Owner, a.cfg.Repo, work.PRNumber)
		if err != nil {
			a.logger.Error("failed to get PR mergeable state", "pr", work.PRNumber, "error", err)
			continue
		}

		if mergeState != "dirty" {
			continue
		}

		// Check if we already posted a conflict comment for the current HEAD
		headSHA, _ := a.gh.GetPRHeadSHA(ctx, a.cfg.Owner, a.cfg.Repo, work.PRNumber)
		if headSHA != "" && a.hasExistingBotComment(ctx, work.PRNumber, "conflict") && a.hasExistingBotComment(ctx, work.PRNumber, shortSHA(headSHA)) {
			continue
		}

		a.logger.Info("PR has merge conflicts, attempting rebase", "pr", work.PRNumber)

		if err := a.ensureWorktreeReady(ctx, work); err != nil {
			a.logger.Error("failed to prepare worktree", "pr", work.PRNumber, "error", err)
			continue
		}

		// Fetch all remotes and try automatic rebase against origin/main (upstream)
		a.runner.Run(ctx, work.WorktreePath, "git", "fetch", "--all")

		// Try automatic rebase
		_, stderr, rebaseErr := a.runner.Run(ctx, work.WorktreePath, "git", "rebase", "origin/main")
		if rebaseErr == nil {
			// Rebase succeeded, force push
			pushRemote := "origin"
			if wtm, ok := a.worktrees.(*GitWorktreeManager); ok {
				pushRemote = wtm.PushRemote()
			}
			_, stderr, pushErr := a.runner.Run(ctx, work.WorktreePath, "git", "push", pushRemote, "--force-with-lease")
			if pushErr != nil {
				a.logger.Error("failed to push after rebase", "pr", work.PRNumber, "error", pushErr, "stderr", string(stderr))
			} else {
				a.logger.Info("rebased and pushed successfully", "pr", work.PRNumber)
				_ = a.gh.AddIssueComment(ctx, a.cfg.Owner, a.cfg.Repo, work.PRNumber,
					fmt.Sprintf("Merge conflicts on commit %s resolved by rebasing on main.\n\n%s", shortSHA(headSHA), botMarker))
			}
			continue
		}

		// Rebase failed — abort and let Claude try
		a.runner.Run(ctx, work.WorktreePath, "git", "rebase", "--abort")
		a.logger.Info("automatic rebase failed, invoking Claude to resolve conflicts", "pr", work.PRNumber, "stderr", string(stderr))

		prompt := buildConflictResolutionPrompt(*work)
		_, err = runClaude(ctx, a.runner, work.WorktreePath, prompt, a.cfg, a.logger, true)
		if err != nil {
			a.logger.Error("claude failed to resolve conflicts", "pr", work.PRNumber, "error", err)
			_ = a.gh.AddIssueComment(ctx, a.cfg.Owner, a.cfg.Repo, work.PRNumber,
				fmt.Sprintf("PR has merge conflicts on commit %s. Attempted to resolve automatically but failed. Human intervention needed.\n\n%s", shortSHA(headSHA), botMarker))
			continue
		}

		// Push the rebased branch
		if err := a.gitPush(ctx, work.WorktreePath, true); err != nil {
			a.logger.Error("failed to push after conflict resolution", "pr", work.PRNumber, "error", err)
			_ = a.gh.AddIssueComment(ctx, a.cfg.Owner, a.cfg.Repo, work.PRNumber,
				fmt.Sprintf("PR has merge conflicts on commit %s. Attempted to resolve automatically but failed. Human intervention needed.\n\n%s", shortSHA(headSHA), botMarker))
		} else {
			_ = a.gh.AddIssueComment(ctx, a.cfg.Owner, a.cfg.Repo, work.PRNumber,
				fmt.Sprintf("Merge conflicts on commit %s resolved and pushed.\n\n%s", shortSHA(headSHA), botMarker))
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
	}
}

// hasExistingBotComment returns true if a bot comment containing the given text
// already exists on the issue.
func (a *Agent) hasExistingBotComment(ctx context.Context, issueNumber int, text string) bool {
	comments, err := a.gh.GetIssueComments(ctx, a.cfg.Owner, a.cfg.Repo, issueNumber, 0)
	if err != nil {
		return false
	}
	for _, c := range comments {
		if strings.Contains(c.Body, botMarker) && strings.Contains(c.Body, text) {
			return true
		}
	}
	return false
}

// shortSHA returns the first 7 characters of a SHA, or the full string if shorter.
func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// alreadyCheckedCI returns true if a bot comment mentioning the given SHA
// already exists on the PR, indicating this commit was already investigated.
func (a *Agent) alreadyCheckedCI(ctx context.Context, prNumber int, sha string) bool {
	comments, err := a.gh.GetIssueComments(ctx, a.cfg.Owner, a.cfg.Repo, prNumber, 0)
	if err != nil {
		return false
	}
	short := shortSHA(sha)
	for _, c := range comments {
		if strings.Contains(c.Body, botMarker) && strings.Contains(c.Body, short) {
			return true
		}
	}
	return false
}

// ensureWorktreeReady ensures the repo is cloned and the worktree exists for the given work item.
// If the worktree was lost (e.g. after a restart with a fresh volume), it recreates it.
func (a *Agent) ensureWorktreeReady(ctx context.Context, work *IssueWork) error {
	if err := a.worktrees.EnsureRepoCloned(ctx); err != nil {
		return err
	}
	worktreePath, err := a.worktrees.CreateWorktree(ctx, work.BranchName)
	if err != nil {
		return err
	}
	work.WorktreePath = worktreePath

	// Pull the latest from the push remote
	return a.worktrees.SyncWorktree(ctx, worktreePath)
}

// hasUncommittedChanges returns true if the worktree has staged or unstaged changes.
func (a *Agent) hasUncommittedChanges(ctx context.Context, worktreePath string) bool {
	out, _, _ := a.runner.Run(ctx, worktreePath, "git", "status", "--porcelain")
	return len(strings.TrimSpace(string(out))) > 0
}

// gitCommitAll stages all changes and creates a new commit with the configured author and signed-off-by.
func (a *Agent) gitCommitAll(ctx context.Context, worktreePath, message string) error {
	if _, stderr, err := a.runner.Run(ctx, worktreePath, "git", "add", "-A"); err != nil {
		return fmt.Errorf("git add: %w (stderr: %s)", err, string(stderr))
	}

	commitMsg := message
	if a.cfg.SignedOffBy != "" {
		commitMsg += fmt.Sprintf("\n\nSigned-off-by: %s", a.cfg.SignedOffBy)
	}

	if _, stderr, err := a.runner.Run(ctx, worktreePath, "git", "commit", "-m", commitMsg); err != nil {
		return fmt.Errorf("git commit: %w (stderr: %s)", err, string(stderr))
	}
	return nil
}

// gitAmendAll stages all changes and amends the current commit.
func (a *Agent) gitAmendAll(ctx context.Context, worktreePath string) error {
	if _, stderr, err := a.runner.Run(ctx, worktreePath, "git", "add", "-A"); err != nil {
		return fmt.Errorf("git add: %w (stderr: %s)", err, string(stderr))
	}
	if _, stderr, err := a.runner.Run(ctx, worktreePath, "git", "commit", "--amend", "--no-edit"); err != nil {
		return fmt.Errorf("git commit --amend: %w (stderr: %s)", err, string(stderr))
	}
	return nil
}

// gitPush pushes the current branch to the push remote.
func (a *Agent) gitPush(ctx context.Context, worktreePath string, force bool) error {
	pushRemote := "origin"
	if wtm, ok := a.worktrees.(*GitWorktreeManager); ok {
		pushRemote = wtm.PushRemote()
	}

	// Get the current branch name for the refspec
	branchOut, _, err := a.runner.Run(ctx, worktreePath, "git", "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return fmt.Errorf("getting branch name: %w", err)
	}
	branch := strings.TrimSpace(string(branchOut))

	args := []string{"push", pushRemote, "HEAD:" + branch}
	if force {
		args = append(args, "--force-with-lease")
	}

	if _, stderr, err := a.runner.Run(ctx, worktreePath, "git", args...); err != nil {
		return fmt.Errorf("git push: %w (stderr: %s)", err, string(stderr))
	}
	return nil
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
