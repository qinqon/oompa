package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// botMarker is a hidden HTML comment added to all agent-posted comments
	// so they can be distinguished from manual comments by the same user.
	botMarker = "<!-- oompa-bot -->"

	// Status values for IssueWork
	StatusImplementing = "implementing"
	StatusPROpen       = "pr-open"
	StatusFailed       = "failed"

	// Labels
	labelAIFailed = "ai-failed"
)

func ciMarker(sha string) string {
	return fmt.Sprintf("<!-- oompa-bot ci:%s -->", shortSHA(sha))
}

// issueBranchName returns the branch name for a given issue number.
func issueBranchName(issueNumber int) string {
	return fmt.Sprintf("ai/issue-%d", issueNumber)
}

// classifyPRs finds the first open PR and checks if any PR was merged.
func classifyPRs(prs []PR) (openPR *PR, hasMerged bool) {
	for i := range prs {
		if prs[i].State == "open" && openPR == nil {
			openPR = &prs[i]
		}
		if prs[i].Merged {
			hasMerged = true
		}
		if openPR != nil && hasMerged {
			break
		}
	}
	return openPR, hasMerged
}

// Agent holds all dependencies and runs the main processing loop.
type Agent struct {
	gh        GitHubClient
	runner    CommandRunner
	worktrees WorktreeManager
	state     *State
	cfg       Config
	logger    *slog.Logger
	tokenFunc func(context.Context) (string, error) // optional: provides fresh GitHub tokens (for App auth)
	codeAgent CodeAgent                              // coding agent backend (Claude Code or OpenCode)
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
	os.Setenv("GH_TOKEN", token)

	// Update the runner's GH_TOKEN environment variable
	if execRunner, ok := a.runner.(*ExecRunner); ok {
		execRunner.SetGHToken(token)
	}

	return nil
}

// runParallel executes a function on each item in parallel with a bounded worker pool.
// If maxWorkers <= 1 or len(items) <= 1, runs sequentially instead.
func runParallel[T any](ctx context.Context, maxWorkers int, items []T, fn func(context.Context, T)) {
	if maxWorkers <= 1 || len(items) <= 1 {
		for _, item := range items {
			fn(ctx, item)
		}
		return
	}

	workers := maxWorkers
	if len(items) < workers {
		workers = len(items)
	}

	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup

	for _, item := range items {
		wg.Add(1)
		sem <- struct{}{}

		// Capture item for goroutine
		item := item
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			fn(ctx, item)
		}()
	}

	wg.Wait()
}

// NewAgent creates a new Agent with all dependencies wired.
// Task structs for parallel execution

type newIssueTask struct {
	issue        Issue
	branchName   string
	worktreePath string
	work         *IssueWork
}

type reviewTask struct {
	work          *IssueWork
	humanComments []ReviewComment
	humanReviews  []PRReview
}

type ciTask struct {
	work     *IssueWork
	headSHA  string
	failures []CheckRun
	diff     string
	commits  []Commit
}

type conflictTask struct {
	work         *IssueWork
	headSHA      string
	rebaseErr    error
	rebaseStderr string
}

func NewAgent(gh GitHubClient, runner CommandRunner, worktrees WorktreeManager, state *State, cfg Config, logger *slog.Logger, codeAgent CodeAgent) *Agent {
	return &Agent{
		gh:        gh,
		runner:    runner,
		worktrees: worktrees,
		state:     state,
		cfg:       cfg,
		logger:    logger,
		codeAgent: codeAgent,
	}
}

// ProcessNewIssues finds labeled issues and spawns Claude to implement fixes.
func (a *Agent) ProcessNewIssues(ctx context.Context) {
	issues, err := a.gh.ListLabeledIssues(ctx, a.cfg.Owner, a.cfg.Repo, a.cfg.Label)
	if err != nil {
		a.logger.Error("failed to list issues", "error", err)
		return
	}

	// Sequential phase: filter issues, create worktrees, insert into state
	var tasks []newIssueTask

	for _, issue := range issues {
		if work, exists := a.state.ActiveIssues[issue.Number]; exists {
			// Re-check for PR if we lost track of it
			if work.PRNumber == 0 && work.Status == StatusImplementing {
				prs, err := a.gh.ListPRsByHead(ctx, a.cfg.Owner, a.cfg.Repo, a.cfg.GitHubHeadOwner, work.BranchName)
				if err == nil {
					openPR, _ := classifyPRs(prs)
					if openPR != nil {
						work.PRNumber = openPR.Number
						work.Status = StatusPROpen
						a.logger.Info("found PR for tracked issue", "issue", issue.Number, "pr", work.PRNumber)
					}
				}
			}
			a.logger.Debug("skipping already tracked issue", "issue", issue.Number)
			continue
		}

		if a.cfg.OnlyAssigned && !issueAssignedTo(issue, a.cfg.GitHubUser) {
			a.logger.Debug("skipping issue not assigned to agent user", "issue", issue.Number, "user", a.cfg.GitHubUser)
			continue
		}

		a.logger.Info("processing new issue", "issue", issue.Number, "title", issue.Title)

		branchName := issueBranchName(issue.Number)

		// Check if a PR already exists for this issue (open, closed, or merged)
		prs, err := a.gh.ListPRsByHead(ctx, a.cfg.Owner, a.cfg.Repo, a.cfg.GitHubHeadOwner, branchName)
		if err == nil && len(prs) > 0 {
			// Find the first open PR, or check if any was merged
			openPR, hasMerged := classifyPRs(prs)
			if openPR != nil {
				a.logger.Info("PR already exists for issue", "issue", issue.Number, "pr", openPR.Number)
				a.state.ActiveIssues[issue.Number] = &IssueWork{
					IssueNumber: issue.Number,
					IssueTitle:  issue.Title,
					BranchName:  branchName,
					PRNumber:    openPR.Number,
					Status:      StatusPROpen,
					CreatedAt:   time.Now(),
				}
				continue
			} else if hasMerged {
				// PR was merged — skip to avoid reprocessing a completed issue
				a.logger.Info("skipping issue with merged PR", "issue", issue.Number, "pr", prs[0].Number)
				continue
			}
			// PR was closed (rejected) — fall through to allow retry
		}

		// Check if any open PR already references this issue (e.g., created by a human)
		if linked, err := a.gh.HasLinkedPR(ctx, a.cfg.Owner, a.cfg.Repo, issue.Number); err != nil {
			a.logger.Warn("failed to check for linked PRs", "issue", issue.Number, "error", err)
		} else if linked {
			a.logger.Info("skipping issue with existing linked PR", "issue", issue.Number)
			continue
		}

		// Only post in-progress comment if we haven't already
		if !a.hasExistingBotComment(ctx, issue.Number, "working on this issue") {
			if err := a.gh.AssignIssue(ctx, a.cfg.Owner, a.cfg.Repo, issue.Number, a.cfg.GitHubUser); err != nil {
				a.logger.Warn("failed to assign issue", "issue", issue.Number, "error", err)
			}
			if err := a.gh.AddIssueComment(ctx, a.cfg.Owner, a.cfg.Repo, issue.Number,
				fmt.Sprintf("Oompa is working on this issue. A PR will be created shortly.\n\n%s", botMarker)); err != nil {
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
			Status:       StatusImplementing,
			CreatedAt:    time.Now(),
		}

		// Insert into state map before parallel phase
		a.state.ActiveIssues[issue.Number] = work

		tasks = append(tasks, newIssueTask{
			issue:        issue,
			branchName:   branchName,
			worktreePath: worktreePath,
			work:         work,
		})
	}

	// Parallel phase: run Claude, push, create PR
	runParallel(ctx, a.cfg.MaxWorkers, tasks, func(ctx context.Context, task newIssueTask) {
		prompt := buildImplementationPrompt(task.issue, a.cfg.SignedOffBy)
		_, err := a.codeAgent.Run(ctx, a.runner, task.worktreePath, prompt, a.logger, false)
		if err != nil {
			a.logger.Error("agent failed", "issue", task.issue.Number, "error", err)
			a.markIssueFailed(ctx, task.issue.Number, task.work)
			return
		}

		// Check if Claude produced any commits
		logOut, _, _ := a.runner.Run(ctx, task.worktreePath, "git", "log", a.originDefaultBranch()+"..HEAD", "--oneline")
		if strings.TrimSpace(string(logOut)) == "" {
			a.logger.Warn("claude finished but produced no commits", "issue", task.issue.Number)
			a.markIssueFailed(ctx, task.issue.Number, task.work)
			return
		}

		// Squash all commits into a single commit before pushing
		if err := a.gitSquashCommits(ctx, task.worktreePath, task.issue.Number, task.issue.Title); err != nil {
			a.logger.Error("failed to squash commits", "issue", task.issue.Number, "error", err)
			a.markIssueFailed(ctx, task.issue.Number, task.work)
			return
		}

		// Push the branch (force push because squashing rewrites history)
		if err := a.gitPush(ctx, task.worktreePath, true); err != nil {
			a.logger.Error("failed to push branch", "issue", task.issue.Number, "error", err)
			a.markIssueFailed(ctx, task.issue.Number, task.work)
			return
		}

		// Create PR
		prHead := task.branchName
		if a.cfg.GitHubHeadOwner != "" && a.cfg.GitHubHeadOwner != a.cfg.Owner {
			prHead = a.cfg.GitHubHeadOwner + ":" + task.branchName
		}
		prTitle := task.issue.Title
		prBody := a.buildPRBody(ctx, task.worktreePath, task.issue.Number)
		prNumber, err := a.gh.CreatePR(ctx, a.cfg.Owner, a.cfg.Repo, prTitle, prBody, prHead, a.defaultBranch())
		if err != nil {
			a.logger.Error("failed to create PR", "issue", task.issue.Number, "error", err)
			// Clean up the remote branch to avoid orphaned branches
			a.deleteRemoteBranch(ctx, task.worktreePath, task.branchName)
			a.markIssueFailed(ctx, task.issue.Number, task.work)
			return
		}

		task.work.PRNumber = prNumber
		task.work.Status = StatusPROpen
		a.logger.Info("created PR", "issue", task.issue.Number, "pr", prNumber)

		_ = a.gh.UnassignIssue(ctx, a.cfg.Owner, a.cfg.Repo, task.issue.Number, a.cfg.GitHubUser)
	})
}

// ProcessReviewComments checks for new review comments and review bodies, then runs Claude to address them.
func (a *Agent) ProcessReviewComments(ctx context.Context) {
	// Sequential phase: filter comments, prepare worktrees, add reactions, build tasks
	var tasks []reviewTask

	for _, work := range a.state.ActiveIssues {
		if work.PRNumber == 0 || work.Status != StatusPROpen {
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
			if c.InReplyToID != 0 {
				continue
			}
			if strings.Contains(c.Body, botMarker) {
				continue
			}
			if !a.isAllowedReviewer(c.User) {
				continue
			}
			if repliedTo[c.ID] {
				if already, err := a.gh.HasPRCommentReaction(ctx, a.cfg.Owner, a.cfg.Repo, c.ID, "eyes", a.cfg.GitHubUser); err == nil && already {
					continue
				}
			}
			humanComments = append(humanComments, c)
		}

		// Fetch PR review bodies
		reviews, err := a.gh.GetPRReviews(ctx, a.cfg.Owner, a.cfg.Repo, work.PRNumber, work.LastReviewID)
		if err != nil {
			a.logger.Warn("failed to get PR reviews", "pr", work.PRNumber, "error", err)
		}

		// Filter reviews
		var humanReviews []PRReview
		if len(reviews) > 0 {
			headCommitDate, _ := a.gh.GetPRHeadCommitDate(ctx, a.cfg.Owner, a.cfg.Repo, work.PRNumber)

			for _, r := range reviews {
				if strings.Contains(r.Body, botMarker) {
					continue
				}
				if !a.isAllowedReviewer(r.User) {
					continue
				}
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

		// React with eyes to signal we're processing
		for _, c := range humanComments {
			if err := a.gh.AddPRCommentReaction(ctx, a.cfg.Owner, a.cfg.Repo, c.ID, "eyes"); err != nil {
				a.logger.Warn("failed to add reaction", "comment", c.ID, "error", err)
			}
		}

		tasks = append(tasks, reviewTask{
			work:          work,
			humanComments: humanComments,
			humanReviews:  humanReviews,
		})
	}

	// Parallel phase: run Claude, amend/push, post fallback replies
	runParallel(ctx, a.cfg.MaxWorkers, tasks, func(ctx context.Context, task reviewTask) {
		// Capture local HEAD before Claude runs so we can detect if Claude committed directly
		headBefore, _, err := a.runner.Run(ctx, task.work.WorktreePath, "git", "rev-parse", "HEAD")
		if err != nil {
			a.logger.Warn("failed to get HEAD before Claude", "pr", task.work.PRNumber, "error", err)
		}
		headSHABefore := strings.TrimSpace(string(headBefore))

		prompt := buildReviewResponsePrompt(*task.work, task.humanComments, task.humanReviews, a.cfg.Owner, a.cfg.Repo)
		_, err = a.codeAgent.Run(ctx, a.runner, task.work.WorktreePath, prompt, a.logger, true)
		if err != nil {
			a.logger.Error("agent failed to address review", "pr", task.work.PRNumber, "error", err)
			return
		}

		// Amend and push if Claude made changes
		pushed := false
		hasChanges := a.hasUncommittedChanges(ctx, task.work.WorktreePath)
		if hasChanges {
			if err := a.gitAmendAll(ctx, task.work.WorktreePath); err != nil {
				a.logger.Error("failed to amend commit", "pr", task.work.PRNumber, "error", err)
			} else if err := a.gitPush(ctx, task.work.WorktreePath, true); err != nil {
				a.logger.Error("failed to push", "pr", task.work.PRNumber, "error", err)
			} else {
				pushed = true
			}
		} else {
			// No uncommitted changes — Claude may have committed or amended directly.
			// Check if HEAD changed since before Claude ran.
			headAfter, _, revErr := a.runner.Run(ctx, task.work.WorktreePath, "git", "rev-parse", "HEAD")
			headSHAAfter := strings.TrimSpace(string(headAfter))
			if revErr == nil && headSHABefore != "" && headSHAAfter != headSHABefore {
				a.logger.Info("Claude committed directly, pushing", "pr", task.work.PRNumber)
				if err := a.gitPush(ctx, task.work.WorktreePath, true); err != nil {
					a.logger.Error("failed to push", "pr", task.work.PRNumber, "error", err)
				} else {
					pushed = true
				}
			}
		}

		// Post fallback reply for comments Claude didn't reply to
		updatedComments, _ := a.gh.GetPRReviewComments(ctx, a.cfg.Owner, a.cfg.Repo, task.work.PRNumber, 0)
		repliedTo := make(map[int64]bool)
		for _, c := range updatedComments {
			if c.InReplyToID != 0 && c.User == a.cfg.GitHubUser {
				repliedTo[c.InReplyToID] = true
			}
		}
		for _, c := range task.humanComments {
			if repliedTo[c.ID] {
				continue
			}
			fallback := "Reviewed — no code changes needed for this comment.\n\n" + botMarker
			if pushed {
				fallback = "Addressed in the latest push.\n\n" + botMarker
			}
			if err := a.gh.ReplyToPRComment(ctx, a.cfg.Owner, a.cfg.Repo, task.work.PRNumber, c.ID, fallback); err != nil {
				a.logger.Warn("failed to reply to comment", "comment", c.ID, "error", err)
			}
		}

		// Update last seen comment ID
		for _, c := range task.humanComments {
			if c.ID > task.work.LastCommentID {
				task.work.LastCommentID = c.ID
			}
		}

		// Update last seen review ID
		for _, r := range task.humanReviews {
			if r.ID > task.work.LastReviewID {
				task.work.LastReviewID = r.ID
			}
		}
	})
}

const maxCIFixAttempts = 3

// ProcessCIFailures checks CI status for open PRs and invokes Claude to fix failures.
func (a *Agent) ProcessCIFailures(ctx context.Context) {
	// Sequential phase: GitHub API calls, check run fetching, worktree setup
	var tasks []ciTask

	for _, work := range a.state.ActiveIssues {
		if work.PRNumber == 0 || work.Status != StatusPROpen {
			continue
		}

		headSHA, err := a.gh.GetPRHeadSHA(ctx, a.cfg.Owner, a.cfg.Repo, work.PRNumber)
		if err != nil {
			a.logger.Error("failed to get PR head SHA", "pr", work.PRNumber, "error", err)
			continue
		}

		// Reset fix attempts counter if HEAD SHA changed (new commits pushed)
		if work.LastCheckedCISHA != "" && work.LastCheckedCISHA != headSHA {
			if work.CIFixAttempts > 0 {
				a.logger.Info("new commits detected, resetting CI fix attempts", "pr", work.PRNumber, "old_sha", shortSHA(work.LastCheckedCISHA), "new_sha", shortSHA(headSHA), "previous_attempts", work.CIFixAttempts)
				work.CIFixAttempts = 0
				work.LastCIStatus = ""
			}
		}

		if work.CIFixAttempts >= maxCIFixAttempts {
			if work.LastCIStatus != "max-retries-reached" {
				a.logger.Warn("CI fix attempts exhausted", "pr", work.PRNumber, "attempts", work.CIFixAttempts)
				work.LastCIStatus = "max-retries-reached"
			}
			continue
		}

		if work.LastCheckedCISHA == headSHA && work.CIFixAttempts == 0 {
			continue
		}

		if a.alreadyCheckedCI(ctx, work.PRNumber, headSHA) {
			work.LastCheckedCISHA = headSHA
			a.logger.Info("CI already investigated for this SHA (found existing comment), skipping", "pr", work.PRNumber, "sha", shortSHA(headSHA))
			continue
		}

		runs, err := a.gh.GetCheckRuns(ctx, a.cfg.Owner, a.cfg.Repo, headSHA)
		if err != nil {
			a.logger.Error("failed to get check runs", "pr", work.PRNumber, "error", err)
			continue
		}

		// Also query commit statuses (e.g. Prow) and merge into results
		statusFailures, err := a.gh.GetCommitStatuses(ctx, a.cfg.Owner, a.cfg.Repo, headSHA)
		if err != nil {
			a.logger.Warn("failed to get commit statuses", "pr", work.PRNumber, "error", err)
			// Non-fatal: continue with check runs only
		} else {
			runs = append(runs, statusFailures...)
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

		// Fetch logs for each failing check when output is missing or too short.
		// Threshold of 50 chars filters out generic GitHub Actions boilerplate
		// (e.g., "Process completed with exit code 1") that doesn't provide
		// enough context for meaningful analysis.
		// Skip entries with ID==0: these are commit-status entries (e.g. Prow)
		// where Output contains a target_url, not log text, and no check-run
		// log is available via the GitHub Actions API.
		for i, f := range failures {
			if f.ID == 0 {
				continue
			}
			trimmed := strings.TrimSpace(f.Output)
			if len(trimmed) < 50 {
				log, err := a.gh.GetCheckRunLog(ctx, a.cfg.Owner, a.cfg.Repo, f.ID)
				if err != nil {
					a.logger.Warn("failed to get check run log", "check", f.Name, "error", err)
				} else if log != "" {
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
		diffOut, _, _ := a.runner.Run(ctx, work.WorktreePath, "git", "diff", "--stat", a.originDefaultBranch())
		diff := string(diffOut)

		// Get commits in the PR to help Claude identify which commit introduced the failure
		commits := a.getPRCommits(ctx, work.WorktreePath)

		tasks = append(tasks, ciTask{
			work:     work,
			headSHA:  headSHA,
			failures: failures,
			diff:     diff,
			commits:  commits,
		})
	}

	// Parallel phase: Claude invocations and post-processing
	runParallel(ctx, a.cfg.MaxWorkers, tasks, func(ctx context.Context, task ciTask) {
		prompt := buildCIFixPrompt(*task.work, task.failures, task.diff, task.commits, a.cfg.SignedOffBy)
		result, err := a.codeAgent.Run(ctx, a.runner, task.work.WorktreePath, prompt, a.logger, true)
		if err != nil {
			a.logger.Error("agent failed to investigate CI", "pr", task.work.PRNumber, "error", err)
			task.work.CIFixAttempts++
			task.work.LastCIStatus = "failure"
			task.work.LastCheckedCISHA = task.headSHA
			return
		}

		// Strip markdown formatting (bold, italic) before checking prefix
		cleaned := strings.TrimLeft(strings.TrimSpace(result.Result), "*_")

		// Check if Claude followed the expected format (must start with UNRELATED or RELATED)
		startsWithUnrelated := strings.HasPrefix(cleaned, "UNRELATED")
		startsWithRelated := strings.HasPrefix(cleaned, "RELATED")

		if !startsWithUnrelated && !startsWithRelated {
			a.logger.Warn("Claude response did not start with UNRELATED or RELATED, skipping to avoid noise",
				"pr", task.work.PRNumber,
				"response_preview", truncateString(cleaned, 100))
			task.work.CIFixAttempts++
			task.work.LastCIStatus = "investigation-inconclusive"
			task.work.LastCheckedCISHA = task.headSHA
			return
		}

		if startsWithUnrelated {
			a.logger.Info("CI failure is unrelated to PR changes", "pr", task.work.PRNumber)
			explanation := strings.TrimPrefix(cleaned, "UNRELATED")
			explanation = strings.TrimSpace(explanation)
			comment := fmt.Sprintf("CI check failed on commit %s but appears unrelated to this PR's changes.\n\n%s\n\n%s", shortSHA(task.headSHA), explanation, ciMarker(task.headSHA))
			if err := a.gh.AddIssueComment(ctx, a.cfg.Owner, a.cfg.Repo, task.work.PRNumber, comment); err != nil {
				a.logger.Error("failed to post CI unrelated comment", "pr", task.work.PRNumber, "error", err)
			}
			task.work.LastCIStatus = "unrelated-failure"
			task.work.LastCheckedCISHA = task.headSHA

			// Create a flaky CI issue if configured
			if a.cfg.CreateFlakyIssues {
				// Skip flaky issue creation if check run output is still too short after
				// attempting to fetch full logs. Without sufficient output, Claude cannot
				// meaningfully match against existing issues or describe the root cause.
				trimmedOutput := strings.TrimSpace(task.failures[0].Output)
				if len(trimmedOutput) < 50 {
					a.logger.Warn("skipping flaky issue creation: check run output is empty or too short",
						"pr", task.work.PRNumber,
						"check", task.failures[0].Name,
						"output_length", len(trimmedOutput))
					return
				}

				issueTitle := fmt.Sprintf("Flaky CI: %s", task.failures[0].Name)

				// Search for existing open issues with the flaky label
				searchQuery := fmt.Sprintf("repo:%s/%s is:issue is:open label:%s",
					a.cfg.Owner, a.cfg.Repo, a.cfg.FlakyLabel)
				existingIssues, err := a.gh.SearchIssues(ctx, searchQuery)

				var issueNum int
				if err != nil {
					a.logger.Warn("failed to search for existing flaky issues", "error", err)
				} else if len(existingIssues) > 0 {
					// Ask the agent if any existing issue matches this failure
					matchPrompt := buildFlakyMatchPrompt(task.failures[0].Name, task.failures[0].Output, existingIssues)
					matchResult, matchErr := a.codeAgent.Run(ctx, a.runner, task.work.WorktreePath, matchPrompt, a.logger, false)
					if matchErr != nil {
						a.logger.Warn("failed to run agent for flaky issue matching", "error", matchErr)
					} else {
						matchResponse := strings.TrimSpace(matchResult.Result)
						if matchedNum, ok := parseFlakyMatch(matchResponse); ok {
							issueNum = matchedNum
							a.logger.Info("agent matched existing flaky CI issue", "issue", issueNum, "check", task.failures[0].Name)
							if err := a.gh.AddIssueComment(ctx, a.cfg.Owner, a.cfg.Repo, task.work.PRNumber,
								fmt.Sprintf("This appears to be a duplicate of existing flaky test issue #%d.\n\n%s", issueNum, botMarker)); err != nil {
								a.logger.Error("failed to post existing flaky issue reference comment", "pr", task.work.PRNumber, "error", err)
							}
							return
						}
					}
				}

				// No existing issue matched, create a new one
				issueBody := fmt.Sprintf("### Which jobs are flaking?\n\n"+
					"%s\n\n"+
					"Detected in PR #%d, commit %s.\n\n"+
					"### Which tests are flaking?\n\n"+
					"%s\n\n"+
					"### Since when has it been flaking?\n\n"+
					"%s\n\n"+
					"### Reason for failure (if possible)\n\n"+
					"%s\n\n"+
					"### Anything else we need to know?\n\n"+
					"Automatically created by [oompa](https://github.com/qinqon/oompa).\n\n"+
					"%s",
					task.failures[0].Name, task.work.PRNumber, shortSHA(task.headSHA),
					task.failures[0].Name, time.Now().Format("2006-01-02"), explanation,
					botMarker)
				issueNum, err = a.gh.CreateIssue(ctx, a.cfg.Owner, a.cfg.Repo, issueTitle, issueBody, []string{a.cfg.FlakyLabel})
				if err != nil {
					a.logger.Error("failed to create flaky CI issue", "error", err)
				} else {
					a.logger.Info("created flaky CI issue", "issue", issueNum)
					if err := a.gh.AddIssueComment(ctx, a.cfg.Owner, a.cfg.Repo, task.work.PRNumber,
						fmt.Sprintf("Opened issue #%d to track this flaky test.\n\n%s", issueNum, botMarker)); err != nil {
						a.logger.Error("failed to post flaky issue reference comment", "pr", task.work.PRNumber, "error", err)
					}
				}
			}

			return
		}

		// Claude said RELATED — check if there are fixup commits, amended commits, or uncommitted changes
		pushed := false
		hasFixupCommits := a.hasFixupCommits(ctx, task.work.WorktreePath)
		hasUncommitted := a.hasUncommittedChanges(ctx, task.work.WorktreePath)

		switch {
		case hasFixupCommits:
			// Run autosquash rebase to merge fixup commits into their targets
			if err := a.gitAutosquashRebase(ctx, task.work.WorktreePath); err != nil {
				a.logger.Error("failed to autosquash fixup commits", "pr", task.work.PRNumber, "error", err)
			} else if err := a.gitPush(ctx, task.work.WorktreePath, true); err != nil {
				a.logger.Error("failed to push CI fix", "pr", task.work.PRNumber, "error", err)
			} else {
				pushed = true
			}
		case hasUncommitted:
			// Claude left uncommitted changes — amend them into HEAD as fallback
			if err := a.gitAmendAll(ctx, task.work.WorktreePath); err != nil {
				a.logger.Error("failed to amend commit for CI fix", "pr", task.work.PRNumber, "error", err)
			} else if err := a.gitPush(ctx, task.work.WorktreePath, true); err != nil {
				a.logger.Error("failed to push CI fix", "pr", task.work.PRNumber, "error", err)
			} else {
				pushed = true
			}
		default:
			// No fixups and no uncommitted changes — Claude may have amended
			// the commit directly. Check if HEAD differs from the remote SHA.
			localSHA, _, err := a.runner.Run(ctx, task.work.WorktreePath, "git", "rev-parse", "HEAD")
			if err == nil && strings.TrimSpace(string(localSHA)) != task.headSHA {
				a.logger.Info("Claude amended commit directly, pushing", "pr", task.work.PRNumber)
				if err := a.gitPush(ctx, task.work.WorktreePath, true); err != nil {
					a.logger.Error("failed to push CI fix", "pr", task.work.PRNumber, "error", err)
				} else {
					pushed = true
				}
			}
		}

		// After pushing (or not pushing), fetch the current HEAD SHA to update state
		currentHeadSHA := task.headSHA
		if pushed {
			// Get the new HEAD SHA after the push
			newHeadSHA, err := a.gh.GetPRHeadSHA(ctx, a.cfg.Owner, a.cfg.Repo, task.work.PRNumber)
			if err != nil {
				a.logger.Warn("failed to get new HEAD SHA after push", "pr", task.work.PRNumber, "error", err)
				// Fall back to old SHA if we can't get the new one
			} else {
				currentHeadSHA = newHeadSHA
			}
		}

		if pushed {
			a.logger.Info("CI failure is related, pushed a fix", "pr", task.work.PRNumber)
			if err := a.gh.AddIssueComment(ctx, a.cfg.Owner, a.cfg.Repo, task.work.PRNumber,
				fmt.Sprintf("CI was failing on commit %s. Pushed a fix.\n\n%s", shortSHA(task.headSHA), ciMarker(task.headSHA))); err != nil {
				a.logger.Error("failed to post CI fix comment", "pr", task.work.PRNumber, "error", err)
			}
		} else {
			a.logger.Warn("Claude said RELATED but no changes to push", "pr", task.work.PRNumber)
			if err := a.gh.AddIssueComment(ctx, a.cfg.Owner, a.cfg.Repo, task.work.PRNumber,
				fmt.Sprintf("CI is failing on commit %s. Investigated but could not push a fix.\n\n%s", shortSHA(task.headSHA), ciMarker(task.headSHA))); err != nil {
				a.logger.Error("failed to post CI investigation comment", "pr", task.work.PRNumber, "error", err)
			}
		}
		task.work.CIFixAttempts++
		task.work.LastCIStatus = "failure"
		task.work.LastCheckedCISHA = currentHeadSHA
	})
}

// ProcessConflicts checks for merge conflicts (dirty mergeable_state) and tries to resolve them.
// For simple rebases when a PR is just behind the base branch, use ProcessRebase instead.
func (a *Agent) ProcessConflicts(ctx context.Context) {
	// Sequential phase: GitHub API calls, worktree setup, git fetch, automatic rebase attempts
	var tasks []conflictTask

	for _, work := range a.state.ActiveIssues {
		if work.PRNumber == 0 || work.Status != StatusPROpen {
			continue
		}

		mergeState, err := a.gh.GetPRMergeable(ctx, a.cfg.Owner, a.cfg.Repo, work.PRNumber)
		if err != nil {
			a.logger.Error("failed to get PR mergeable state", "pr", work.PRNumber, "error", err)
			continue
		}

		a.logger.Debug("PR mergeable state", "pr", work.PRNumber, "state", mergeState)

		// Only handle actual merge conflicts
		if mergeState != "dirty" {
			continue
		}

		// Check if we already posted a conflict comment for the current HEAD
		headSHA, _ := a.gh.GetPRHeadSHA(ctx, a.cfg.Owner, a.cfg.Repo, work.PRNumber)
		if headSHA != "" && a.hasExistingBotComment(ctx, work.PRNumber, "rebase") && a.hasExistingBotComment(ctx, work.PRNumber, shortSHA(headSHA)) {
			continue
		}

		a.logger.Info("PR needs rebase, attempting", "pr", work.PRNumber, "mergeable_state", mergeState)

		if err := a.ensureWorktreeReady(ctx, work); err != nil {
			a.logger.Error("failed to prepare worktree", "pr", work.PRNumber, "error", err)
			continue
		}

		// Fetch all remotes and try automatic rebase against the upstream default branch
		a.runner.Run(ctx, work.WorktreePath, "git", "fetch", "--all") //nolint:errcheck // best-effort

		// Try automatic rebase
		_, stderr, rebaseErr := a.runner.Run(ctx, work.WorktreePath, "git", "rebase", a.originDefaultBranch())
		if rebaseErr == nil {
			// Rebase succeeded, force push
			if pushErr := a.gitPush(ctx, work.WorktreePath, true); pushErr != nil {
				a.logger.Error("failed to push after rebase", "pr", work.PRNumber, "error", pushErr)
			} else {
				a.logger.Info("rebased and pushed successfully", "pr", work.PRNumber)
				_ = a.gh.AddIssueComment(ctx, a.cfg.Owner, a.cfg.Repo, work.PRNumber,
					fmt.Sprintf("Rebased commit %s on main and pushed.\n\n%s", shortSHA(headSHA), botMarker))
			}
			continue
		}

		// Rebase failed — abort and let Claude try
		a.runner.Run(ctx, work.WorktreePath, "git", "rebase", "--abort") //nolint:errcheck // best-effort
		a.logger.Info("automatic rebase failed, invoking Claude to resolve conflicts", "pr", work.PRNumber, "stderr", string(stderr))

		tasks = append(tasks, conflictTask{
			work:         work,
			headSHA:      headSHA,
			rebaseErr:    rebaseErr,
			rebaseStderr: string(stderr),
		})
	}

	// Parallel phase: Claude invocations for conflict resolution
	a.resolveConflictsParallel(ctx, tasks)
}

// ProcessRebase rebases PRs that are behind the base branch but have no merge conflicts.
// For PRs with actual merge conflicts (dirty state), use ProcessConflicts instead.
// If a rebase fails due to conflicts, this delegates to the conflict resolution flow.
func (a *Agent) ProcessRebase(ctx context.Context) {
	// Sequential phase: check states, try automatic rebase, collect failed conflicts into tasks
	var tasks []conflictTask

	for _, work := range a.state.ActiveIssues {
		if work.PRNumber == 0 || work.Status != StatusPROpen {
			continue
		}

		mergeState, err := a.gh.GetPRMergeable(ctx, a.cfg.Owner, a.cfg.Repo, work.PRNumber)
		if err != nil {
			a.logger.Error("failed to get PR mergeable state", "pr", work.PRNumber, "error", err)
			continue
		}

		a.logger.Debug("PR mergeable state", "pr", work.PRNumber, "state", mergeState)

		needsRebase := mergeState == "behind"

		// When mergeable_state is something else (e.g. "unstable", "blocked"), it may mask
		// the branch being behind. Use the compare API as a fallback.
		if !needsRebase && mergeState != "dirty" {
			behind, err := a.gh.IsPRBehind(ctx, a.cfg.Owner, a.cfg.Repo, work.PRNumber)
			if err != nil {
				a.logger.Warn("failed to check if PR is behind", "pr", work.PRNumber, "error", err)
			}
			needsRebase = behind
		}

		if !needsRebase {
			continue
		}

		headSHA, _ := a.gh.GetPRHeadSHA(ctx, a.cfg.Owner, a.cfg.Repo, work.PRNumber)
		if headSHA != "" && a.hasExistingBotComment(ctx, work.PRNumber, "rebase") && a.hasExistingBotComment(ctx, work.PRNumber, shortSHA(headSHA)) {
			continue
		}

		a.logger.Info("PR is behind base branch, rebasing", "pr", work.PRNumber, "mergeable_state", mergeState)

		if err := a.ensureWorktreeReady(ctx, work); err != nil {
			a.logger.Error("failed to prepare worktree", "pr", work.PRNumber, "error", err)
			continue
		}

		a.runner.Run(ctx, work.WorktreePath, "git", "fetch", "--all") //nolint:errcheck // best-effort

		_, stderr, rebaseErr := a.runner.Run(ctx, work.WorktreePath, "git", "rebase", a.originDefaultBranch())
		if rebaseErr != nil {
			a.runner.Run(ctx, work.WorktreePath, "git", "rebase", "--abort") //nolint:errcheck // best-effort

			// Check if the rebase failed due to conflicts
			if isConflictError(string(stderr)) {
				a.logger.Info("rebase failed with conflicts, will invoke conflict resolution", "pr", work.PRNumber)
				tasks = append(tasks, conflictTask{
					work:         work,
					headSHA:      headSHA,
					rebaseErr:    rebaseErr,
					rebaseStderr: string(stderr),
				})
			} else {
				// Non-conflict rebase failure (e.g., corrupt repo state, hook failure)
				a.logger.Error("rebase failed for non-conflict reason", "pr", work.PRNumber, "stderr", string(stderr))
			}
			continue
		}

		if pushErr := a.gitPush(ctx, work.WorktreePath, true); pushErr != nil {
			a.logger.Error("failed to push after rebase", "pr", work.PRNumber, "error", pushErr)
		} else {
			a.logger.Info("rebased and pushed successfully", "pr", work.PRNumber)
			_ = a.gh.AddIssueComment(ctx, a.cfg.Owner, a.cfg.Repo, work.PRNumber,
				fmt.Sprintf("Rebased commit %s on main and pushed.\n\n%s", shortSHA(headSHA), botMarker))
		}
	}

	// Parallel phase: invoke Claude for conflict resolution on collected tasks
	a.resolveConflictsParallel(ctx, tasks)
}

// ProcessTriageJobs monitors periodic CI jobs for failures and investigates them.
func (a *Agent) ProcessTriageJobs(ctx context.Context) {
	if len(a.cfg.TriageJobs) == 0 {
		return
	}

	for _, jobURL := range a.cfg.TriageJobs {
		a.logger.Debug("processing triage job", "url", jobURL)

		// Parse the CI job URL to determine the backend
		ciSource, err := ParseCIJobURL(jobURL, a.gh)
		if err != nil {
			a.logger.Error("failed to parse CI job URL", "url", jobURL, "error", err)
			continue
		}

		// Fetch the latest run(s)
		runs, err := ciSource.ListRecentRuns(ctx, 5)
		if err != nil {
			a.logger.Error("failed to list recent runs", "job", ciSource.JobName(), "error", err)
			continue
		}

		if len(runs) == 0 {
			a.logger.Debug("no recent runs found", "job", ciSource.JobName())
			continue
		}

		// Process the most recent run
		latestRun := runs[0]

		// Skip if already investigated
		if a.state.IsRunInvestigated(ciSource.JobName(), latestRun.ID) {
			a.logger.Debug("run already investigated", "job", ciSource.JobName(), "runID", latestRun.ID)
			continue
		}

		// Skip if the run passed
		if latestRun.Status == "success" {
			a.logger.Info("run passed, skipping", "job", ciSource.JobName(), "runID", latestRun.ID)
			a.state.MarkRunInvestigated(ciSource.JobName(), latestRun.ID)
			continue
		}

		a.logger.Info("investigating failed run", "job", ciSource.JobName(), "runID", latestRun.ID, "status", latestRun.Status)

		// Fetch build log
		buildLog, err := ciSource.FetchLog(ctx, latestRun.ID)
		if err != nil {
			a.logger.Error("failed to fetch build log", "job", ciSource.JobName(), "runID", latestRun.ID, "error", err)
			continue
		}

		// Truncate log if too large (keep last 50KB to focus on recent failures)
		const maxLogSize = 50000
		if len(buildLog) > maxLogSize {
			buildLog = "...\n[Log truncated, showing last 50KB]\n...\n\n" + buildLog[len(buildLog)-maxLogSize:]
		}

		// Create a worktree on the default branch for read-only codebase access
		branchName := fmt.Sprintf("triage/%s", latestRun.ID)

		// Ensure repo is cloned
		if err := a.worktrees.EnsureRepoCloned(ctx); err != nil {
			a.logger.Error("failed to ensure repo cloned", "error", err)
			continue
		}

		// Create worktree on default branch
		worktreePath, err := a.worktrees.CreateWorktree(ctx, branchName)
		if err != nil {
			a.logger.Error("failed to create triage worktree", "error", err)
			continue
		}

		// Checkout the default branch (CreateWorktree creates a new branch, we want default)
		a.runner.Run(ctx, worktreePath, "git", "checkout", a.defaultBranch()) //nolint:errcheck // best-effort

		// Build the triage prompt
		prompt := buildPeriodicCITriagePrompt(ciSource.JobName(), latestRun.ID, buildLog, a.cfg.Owner, a.cfg.Repo)

		// Run agent in the worktree
		a.logger.Info("running agent for CI triage", "job", ciSource.JobName(), "runID", latestRun.ID)
		result, err := a.codeAgent.Run(ctx, a.runner, worktreePath, prompt, a.logger, false)
		if err != nil {
			a.logger.Error("agent failed during CI triage", "job", ciSource.JobName(), "runID", latestRun.ID, "error", err)
			_ = a.worktrees.RemoveWorktree(ctx, worktreePath)
			continue
		}

		analysis := result.Result
		a.logger.Info("CI triage analysis complete", "job", ciSource.JobName(), "runID", latestRun.ID)
		a.logger.Debug("analysis output", "output", analysis)

		// If --create-flaky-issues is set, create a GitHub issue with the analysis
		if a.cfg.CreateFlakyIssues {
			a.logger.Info("creating issue for CI failure", "job", ciSource.JobName(), "runID", latestRun.ID)

			// Search for existing issues about this job to avoid duplicates
			searchQuery := fmt.Sprintf("repo:%s/%s is:issue is:open in:title %q", a.cfg.Owner, a.cfg.Repo, ciSource.JobName())
			existingIssues, err := a.gh.SearchIssues(ctx, searchQuery)
			if err != nil {
				a.logger.Warn("failed to search for existing issues", "error", err)
			}

			if len(existingIssues) > 0 {
				a.logger.Info("found existing issue for this job, skipping issue creation", "job", ciSource.JobName(), "issue", existingIssues[0].Number)
			} else {
				// Create a new issue
				title := fmt.Sprintf("CI Failure: %s", ciSource.JobName())
				body := fmt.Sprintf(`Periodic CI job **%s** failed in run [%s](%s).

## Analysis

%s

---
*This issue was automatically created by oompa based on CI failure analysis.*
<!-- oompa-triage -->`, ciSource.JobName(), latestRun.ID, latestRun.LogURL, analysis)

				issueNumber, err := a.gh.CreateIssue(ctx, a.cfg.Owner, a.cfg.Repo, title, body, []string{a.cfg.FlakyLabel})
				if err != nil {
					a.logger.Error("failed to create issue", "error", err)
				} else {
					a.logger.Info("created issue for CI failure", "job", ciSource.JobName(), "issue", issueNumber)
				}
			}
		}

		// Mark the run as investigated
		a.state.MarkRunInvestigated(ciSource.JobName(), latestRun.ID)

		// Clean up the triage worktree
		if err := a.worktrees.RemoveWorktree(ctx, worktreePath); err != nil {
			a.logger.Warn("failed to remove triage worktree", "path", worktreePath, "error", err)
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

// HasWatchedPRs returns true if the agent is configured to watch specific PRs.
func (a *Agent) HasWatchedPRs() bool {
	return len(a.cfg.WatchPRs) > 0
}

// ShouldRunReaction returns true if the given reaction type should be executed.
// If cfg.Reactions is empty, all reactions are enabled.
// Valid reaction names: "reviews", "ci", "conflicts", "rebase".
func (a *Agent) ShouldRunReaction(reaction string) bool {
	if len(a.cfg.Reactions) == 0 {
		return true
	}
	for _, r := range a.cfg.Reactions {
		if r == reaction {
			return true
		}
	}
	return false
}

// BootstrapWatchedPRs creates IssueWork entries for directly-specified PR numbers.
// Called each poll cycle to ensure watched PRs are tracked in state.
func (a *Agent) BootstrapWatchedPRs(ctx context.Context) {
	for _, prNumber := range a.cfg.WatchPRs {
		// Check if already tracked by any key
		alreadyTracked := false
		for _, work := range a.state.ActiveIssues {
			if work.PRNumber == prNumber {
				alreadyTracked = true
				break
			}
		}
		if alreadyTracked {
			continue
		}

		pr, err := a.gh.GetPR(ctx, a.cfg.Owner, a.cfg.Repo, prNumber)
		if err != nil {
			a.logger.Error("failed to get watched PR details", "pr", prNumber, "error", err)
			continue
		}

		if pr.Merged || pr.State == "closed" {
			a.logger.Info("skipping watched PR (already closed/merged)", "pr", prNumber, "state", pr.State)
			continue
		}

		branchName := pr.Head
		worktreePath := filepath.Join(a.cfg.CloneDir, "worktrees", branchName)

		work := &IssueWork{
			IssueTitle:   pr.Title,
			WorktreePath: worktreePath,
			BranchName:   branchName,
			PRNumber:     prNumber,
			Status:       "pr-open",
			CreatedAt:    time.Now(),
		}

		a.state.ActiveIssues[prNumber] = work
		a.logger.Info("bootstrapped watched PR", "pr", prNumber, "branch", branchName, "title", pr.Title)
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

// parseFlakyMatch parses Claude's response to a flaky match prompt.
// Returns the matched issue number and true if the response is "MATCH <number>".
func parseFlakyMatch(response string) (int, bool) {
	response = strings.TrimLeft(response, "*_")
	response = strings.TrimSpace(response)
	if !strings.HasPrefix(response, "MATCH") {
		return 0, false
	}
	numStr := strings.TrimSpace(strings.TrimPrefix(response, "MATCH"))
	numStr = strings.TrimPrefix(numStr, "#")
	n, err := strconv.Atoi(numStr)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// shortSHA returns the first 7 characters of a SHA, or the full string if shorter.
func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func (a *Agent) alreadyCheckedCI(ctx context.Context, prNumber int, sha string) bool {
	comments, err := a.gh.GetIssueComments(ctx, a.cfg.Owner, a.cfg.Repo, prNumber, 0)
	if err != nil {
		return false
	}
	marker := ciMarker(sha)
	for _, c := range comments {
		if strings.Contains(c.Body, marker) {
			return true
		}
	}
	return false
}

// originDefaultBranch returns "origin/<default-branch>" (e.g. "origin/main", "origin/master").
func (a *Agent) originDefaultBranch() string {
	if wtm, ok := a.worktrees.(*GitWorktreeManager); ok {
		return wtm.OriginDefaultBranch()
	}
	return "origin/main"
}

// defaultBranch returns the default branch name (e.g. "main", "master").
func (a *Agent) defaultBranch() string {
	if wtm, ok := a.worktrees.(*GitWorktreeManager); ok {
		return wtm.DefaultBranch()
	}
	return "main"
}

// pushRemote returns the name of the push remote (e.g. "origin", "fork").
func (a *Agent) pushRemote() string {
	if wtm, ok := a.worktrees.(*GitWorktreeManager); ok {
		return wtm.PushRemote()
	}
	return "origin"
}

// buildPRBody constructs a PR description. If Claude wrote a .pr-body.md file
// (filled from the repo's PR template), that is used. Otherwise falls back to
// constructing a body from the git log.
func (a *Agent) buildPRBody(ctx context.Context, worktreePath string, issueNumber int) string {
	prBodyFile := filepath.Join(worktreePath, ".pr-body.md")
	if content, err := os.ReadFile(prBodyFile); err == nil {
		os.Remove(prBodyFile)
		body := strings.TrimSpace(string(content))
		if !strings.Contains(body, botMarker) {
			body += "\n\n" + botMarker
		}
		return body
	}

	// Fallback: construct from git log body only (skip subject to avoid duplication).
	logOut, _, _ := a.runner.Run(ctx, worktreePath, "git", "log", a.originDefaultBranch()+"..HEAD", "--format=%b---")
	rawBody := strings.TrimSpace(string(logOut))

	body := fmt.Sprintf("Fixes #%d\n\n", issueNumber)
	if rawBody != "" {
		body += stripSignedOffBy(rawBody) + "\n\n"
	}
	body += botMarker
	return body
}

// stripSignedOffBy removes "Signed-off-by: ..." trailer lines from a string.
func stripSignedOffBy(s string) string {
	var kept []string
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "Signed-off-by:") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
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
	return strings.TrimSpace(string(out)) != ""
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

// gitSquashCommits squashes all commits since the origin default branch into a single commit.
func (a *Agent) gitSquashCommits(ctx context.Context, worktreePath string, issueNumber int, issueTitle string) error {
	// Get all commit messages since origin default branch
	logOut, _, err := a.runner.Run(ctx, worktreePath, "git", "log", a.originDefaultBranch()+"..HEAD", "--format=%B")
	if err != nil {
		return fmt.Errorf("getting commit messages: %w", err)
	}
	commitMessages := strings.TrimSpace(string(logOut))

	// Reset to origin default branch while keeping changes staged
	if _, stderr, err := a.runner.Run(ctx, worktreePath, "git", "reset", "--soft", a.originDefaultBranch()); err != nil {
		return fmt.Errorf("git reset --soft: %w (stderr: %s)", err, string(stderr))
	}

	// Unstage .pr-body.md if Claude accidentally staged it — it must not be committed.
	a.runner.Run(ctx, worktreePath, "git", "restore", "--staged", ".pr-body.md") //nolint:errcheck // best-effort

	// Create a single commit with a meaningful message.
	// Strip any Signed-off-by trailers Claude may have added to individual commits
	// before building the body, then append exactly one canonical trailer at the end.
	commitMsg := fmt.Sprintf("Fix #%d: %s", issueNumber, issueTitle)
	if commitMessages != "" {
		commitMsg += "\n\n" + stripSignedOffBy(commitMessages)
	}
	if a.cfg.SignedOffBy != "" {
		commitMsg += fmt.Sprintf("\n\nSigned-off-by: %s", a.cfg.SignedOffBy)
	}

	if _, stderr, err := a.runner.Run(ctx, worktreePath, "git", "commit", "-m", commitMsg); err != nil {
		return fmt.Errorf("git commit after squash: %w (stderr: %s)", err, string(stderr))
	}

	return nil
}

// deleteRemoteBranch removes a branch from the push remote.
func (a *Agent) deleteRemoteBranch(ctx context.Context, worktreePath, branchName string) {
	pushRemote := a.pushRemote()
	_, stderr, err := a.runner.Run(ctx, worktreePath, "git", "push", pushRemote, "--delete", branchName)
	if err != nil {
		a.logger.Warn("failed to delete remote branch", "branch", branchName, "error", err, "stderr", string(stderr))
	} else {
		a.logger.Info("deleted remote branch", "branch", branchName)
	}
}

// gitPush pushes the current branch to the push remote.
func (a *Agent) gitPush(ctx context.Context, worktreePath string, force bool) error {
	pushRemote := a.pushRemote()

	// Get the current branch name for the refspec
	branchOut, _, err := a.runner.Run(ctx, worktreePath, "git", "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return fmt.Errorf("getting branch name: %w", err)
	}
	branch := strings.TrimSpace(string(branchOut))

	args := []string{"push", pushRemote, "HEAD:" + branch}
	if force {
		// Fetch first so --force-with-lease has up-to-date tracking refs
		a.runner.Run(ctx, worktreePath, "git", "fetch", pushRemote) //nolint:errcheck // best-effort
		args = append(args, "--force-with-lease")
	}

	if _, stderr, err := a.runner.Run(ctx, worktreePath, "git", args...); err != nil {
		return fmt.Errorf("git push: %w (stderr: %s)", err, string(stderr))
	}
	return nil
}

// getPRCommits returns the list of commits in the PR (commits between origin default branch and HEAD).
func (a *Agent) getPRCommits(ctx context.Context, worktreePath string) []Commit {
	logOut, _, err := a.runner.Run(ctx, worktreePath, "git", "log", a.originDefaultBranch()+"..HEAD", "--format=%H %s")
	if err != nil {
		return nil
	}

	lines := strings.Split(strings.TrimSpace(string(logOut)), "\n")
	commits := make([]Commit, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) < 2 {
			continue
		}
		commits = append(commits, Commit{
			SHA:     parts[0],
			Subject: parts[1],
		})
	}
	return commits
}

// hasFixupCommits returns true if there are any fixup commits in the current branch.
func (a *Agent) hasFixupCommits(ctx context.Context, worktreePath string) bool {
	logOut, _, err := a.runner.Run(ctx, worktreePath, "git", "log", a.originDefaultBranch()+"..HEAD", "--format=%s")
	if err != nil {
		return false
	}
	return strings.Contains(string(logOut), "fixup!")
}

// gitAutosquashRebase performs an interactive rebase with autosquash to merge fixup commits.
func (a *Agent) gitAutosquashRebase(ctx context.Context, worktreePath string) error {
	// Set GIT_SEQUENCE_EDITOR to cat so rebase doesn't wait for interactive input
	_, stderr, err := a.runner.Run(ctx, worktreePath, "sh", "-c",
		fmt.Sprintf("GIT_SEQUENCE_EDITOR=cat git rebase -i --autosquash %s", a.originDefaultBranch()))
	if err != nil {
		return fmt.Errorf("git rebase --autosquash: %w (stderr: %s)", err, string(stderr))
	}
	return nil
}

// issueAssignedTo returns true if the given user is among the issue's assignees.
func issueAssignedTo(issue Issue, user string) bool {
	for _, a := range issue.Assignees {
		if a == user {
			return true
		}
	}
	return false
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

// truncateString returns the first maxLen characters of s, with "..." appended if truncated.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// isConflictError returns true if the stderr from a git rebase contains conflict indicators.
func isConflictError(stderr string) bool {
	lowerStderr := strings.ToLower(stderr)
	return strings.Contains(lowerStderr, "conflict") || strings.Contains(lowerStderr, "could not apply")
}

// resolveConflictsParallel invokes the coding agent to resolve conflicts for a list of tasks in parallel.
func (a *Agent) resolveConflictsParallel(ctx context.Context, tasks []conflictTask) {
	runParallel(ctx, a.cfg.MaxWorkers, tasks, func(ctx context.Context, task conflictTask) {
		// Get commit count before invoking agent and validate capture
		commitsBefore := a.getPRCommits(ctx, task.work.WorktreePath)
		if commitsBefore == nil {
			a.logger.Error("failed to capture commits before resolution", "pr", task.work.PRNumber)
			return
		}
		commitCountBefore := len(commitsBefore)

		prompt := buildConflictResolutionPrompt(*task.work, a.originDefaultBranch(), a.cfg.SignedOffBy)
		_, err := a.codeAgent.Run(ctx, a.runner, task.work.WorktreePath, prompt, a.logger, true)
		if err != nil {
			a.logger.Error("agent failed to resolve conflicts", "pr", task.work.PRNumber, "error", err)
			// Post a hidden marker so deduplication skips this SHA on the next cycle
			_ = a.gh.AddIssueComment(ctx, a.cfg.Owner, a.cfg.Repo, task.work.PRNumber,
				fmt.Sprintf("<!-- oompa-bot rebase:%s -->", shortSHA(task.headSHA)))
			return
		}

		// Verify that no unexpected new commits were created
		commitsAfter := a.getPRCommits(ctx, task.work.WorktreePath)
		commitCountAfter := len(commitsAfter)

		if commitCountAfter > commitCountBefore {
			a.logger.Warn("conflict resolution created new commits instead of resolving within rebase",
				"pr", task.work.PRNumber,
				"before", commitCountBefore,
				"after", commitCountAfter,
				"new_commits", commitCountAfter-commitCountBefore)

			if err := a.gh.AddIssueComment(ctx, a.cfg.Owner, a.cfg.Repo, task.work.PRNumber,
				fmt.Sprintf("⚠️ Conflict resolution created %d unexpected new commit(s) instead of resolving within the rebase flow.\n\n"+
					"Expected: %d commits (original structure preserved)\n"+
					"Got: %d commits (new commits added)\n\n"+
					"Please review the commit history and manually squash or rebase to preserve the original commit structure.\n\n%s",
					commitCountAfter-commitCountBefore, commitCountBefore, commitCountAfter, botMarker)); err != nil {
				a.logger.Error("failed to log warning to github", "pr", task.work.PRNumber, "error", err)
			}
		}

		// Push the rebased branch
		if err := a.gitPush(ctx, task.work.WorktreePath, true); err != nil {
			a.logger.Error("failed to push after conflict resolution", "pr", task.work.PRNumber, "error", err)
			// Post a hidden marker so deduplication skips this SHA on the next cycle
			_ = a.gh.AddIssueComment(ctx, a.cfg.Owner, a.cfg.Repo, task.work.PRNumber,
				fmt.Sprintf("<!-- oompa-bot rebase:%s -->", shortSHA(task.headSHA)))
		} else {
			if err := a.gh.AddIssueComment(ctx, a.cfg.Owner, a.cfg.Repo, task.work.PRNumber,
				fmt.Sprintf("Rebased commit %s on main and pushed (conflicts resolved).\n\n%s", shortSHA(task.headSHA), botMarker)); err != nil {
				a.logger.Error("failed to log success to github", "pr", task.work.PRNumber, "error", err)
			}
		}
	})
}

// markIssueFailed marks an issue as failed, unassigns the agent, and adds the ai-failed label.
func (a *Agent) markIssueFailed(ctx context.Context, issueNumber int, work *IssueWork) {
	work.Status = StatusFailed
	if err := a.gh.UnassignIssue(ctx, a.cfg.Owner, a.cfg.Repo, issueNumber, a.cfg.GitHubUser); err != nil {
		a.logger.Warn("failed to unassign issue", "issue", issueNumber, "error", err)
	}
	if err := a.gh.AddLabel(ctx, a.cfg.Owner, a.cfg.Repo, issueNumber, labelAIFailed); err != nil {
		a.logger.Warn("failed to add failure label", "issue", issueNumber, "error", err)
	}
}
