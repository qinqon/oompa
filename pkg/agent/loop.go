package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	// botMarker is a hidden HTML comment added to all agent-posted comments
	// so they can be distinguished from manual comments by the same user.
	botMarker = "<!-- oompa-bot -->"
)

func ciMarker(sha string) string {
	return fmt.Sprintf("<!-- oompa-bot ci:%s -->", shortSHA(sha))
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

	// Sequential phase: filter issues, create worktrees, insert into state
	var tasks []newIssueTask

	for _, issue := range issues {
		if work, exists := a.state.ActiveIssues[issue.Number]; exists {
			// Re-check for PR if we lost track of it
			if work.PRNumber == 0 && work.Status == "implementing" {
				prs, err := a.gh.ListPRsByHead(ctx, a.cfg.Owner, a.cfg.Repo, a.cfg.GitHubHeadOwner, work.BranchName)
				if err == nil {
					for _, p := range prs {
						if p.State == "open" {
							work.PRNumber = p.Number
							work.Status = "pr-open"
							a.logger.Info("found PR for tracked issue", "issue", issue.Number, "pr", work.PRNumber)
							break
						}
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

		branchName := fmt.Sprintf("ai/issue-%d", issue.Number)

		// Check if a PR already exists for this issue (open, closed, or merged)
		prs, err := a.gh.ListPRsByHead(ctx, a.cfg.Owner, a.cfg.Repo, a.cfg.GitHubHeadOwner, branchName)
		if err == nil && len(prs) > 0 {
			// Find the first open PR, or check if any was merged
			var openPR *PR
			hasMerged := false
			for i := range prs {
				if prs[i].State == "open" {
					openPR = &prs[i]
					break
				}
				if prs[i].Merged {
					hasMerged = true
				}
			}
			if openPR != nil {
				a.logger.Info("PR already exists for issue", "issue", issue.Number, "pr", openPR.Number)
				a.state.ActiveIssues[issue.Number] = &IssueWork{
					IssueNumber: issue.Number,
					IssueTitle:  issue.Title,
					BranchName:  branchName,
					PRNumber:    openPR.Number,
					Status:      "pr-open",
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
			Status:       "implementing",
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
		_, err := runClaude(ctx, a.runner, task.worktreePath, prompt, a.cfg, a.logger, false)
		if err != nil {
			a.logger.Error("claude failed", "issue", task.issue.Number, "error", err)
			task.work.Status = "failed"
			_ = a.gh.UnassignIssue(ctx, a.cfg.Owner, a.cfg.Repo, task.issue.Number, a.cfg.GitHubUser)
			_ = a.gh.AddLabel(ctx, a.cfg.Owner, a.cfg.Repo, task.issue.Number, "ai-failed")
			return
		}

		// Check if Claude produced any commits
		logOut, _, _ := a.runner.Run(ctx, task.worktreePath, "git", "log", a.originDefaultBranch()+"..HEAD", "--oneline")
		if len(strings.TrimSpace(string(logOut))) == 0 {
			a.logger.Warn("claude finished but produced no commits", "issue", task.issue.Number)
			task.work.Status = "failed"
			_ = a.gh.UnassignIssue(ctx, a.cfg.Owner, a.cfg.Repo, task.issue.Number, a.cfg.GitHubUser)
			_ = a.gh.AddLabel(ctx, a.cfg.Owner, a.cfg.Repo, task.issue.Number, "ai-failed")
			return
		}

		// Squash all commits into a single commit before pushing
		if err := a.gitSquashCommits(ctx, task.worktreePath, task.issue.Number, task.issue.Title); err != nil {
			a.logger.Error("failed to squash commits", "issue", task.issue.Number, "error", err)
			task.work.Status = "failed"
			_ = a.gh.UnassignIssue(ctx, a.cfg.Owner, a.cfg.Repo, task.issue.Number, a.cfg.GitHubUser)
			_ = a.gh.AddLabel(ctx, a.cfg.Owner, a.cfg.Repo, task.issue.Number, "ai-failed")
			return
		}

		// Push the branch (force push because squashing rewrites history)
		if err := a.gitPush(ctx, task.worktreePath, true); err != nil {
			a.logger.Error("failed to push branch", "issue", task.issue.Number, "error", err)
			task.work.Status = "failed"
			_ = a.gh.UnassignIssue(ctx, a.cfg.Owner, a.cfg.Repo, task.issue.Number, a.cfg.GitHubUser)
			_ = a.gh.AddLabel(ctx, a.cfg.Owner, a.cfg.Repo, task.issue.Number, "ai-failed")
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
			task.work.Status = "failed"
			_ = a.gh.UnassignIssue(ctx, a.cfg.Owner, a.cfg.Repo, task.issue.Number, a.cfg.GitHubUser)
			_ = a.gh.AddLabel(ctx, a.cfg.Owner, a.cfg.Repo, task.issue.Number, "ai-failed")
			return
		}

		task.work.PRNumber = prNumber
		task.work.Status = "pr-open"
		a.logger.Info("created PR", "issue", task.issue.Number, "pr", prNumber)

		_ = a.gh.UnassignIssue(ctx, a.cfg.Owner, a.cfg.Repo, task.issue.Number, a.cfg.GitHubUser)
	})
}

// ProcessReviewComments checks for new review comments and review bodies, then runs Claude to address them.
func (a *Agent) ProcessReviewComments(ctx context.Context) {
	// Sequential phase: filter comments, prepare worktrees, add reactions, build tasks
	var tasks []reviewTask

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
		prompt := buildReviewResponsePrompt(*task.work, task.humanComments, task.humanReviews, a.cfg.Owner, a.cfg.Repo)
		_, err := runClaude(ctx, a.runner, task.work.WorktreePath, prompt, a.cfg, a.logger, true)
		if err != nil {
			a.logger.Error("claude failed to address review", "pr", task.work.PRNumber, "error", err)
			return
		}

		// Amend and push if Claude made changes
		hasChanges := a.hasUncommittedChanges(ctx, task.work.WorktreePath)
		if hasChanges {
			if err := a.gitAmendAll(ctx, task.work.WorktreePath); err != nil {
				a.logger.Error("failed to amend commit", "pr", task.work.PRNumber, "error", err)
			} else if err := a.gitPush(ctx, task.work.WorktreePath, true); err != nil {
				a.logger.Error("failed to push", "pr", task.work.PRNumber, "error", err)
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
			if hasChanges {
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
		if work.PRNumber == 0 || work.Status != "pr-open" {
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
				_ = a.gh.AddIssueComment(ctx, a.cfg.Owner, a.cfg.Repo, work.PRNumber,
					fmt.Sprintf("CI is still failing after %d fix attempts. Human intervention needed.\n\n%s", maxCIFixAttempts, botMarker))
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
		result, err := runClaude(ctx, a.runner, task.work.WorktreePath, prompt, a.cfg, a.logger, true)
		if err != nil {
			a.logger.Error("claude failed to investigate CI", "pr", task.work.PRNumber, "error", err)
			task.work.CIFixAttempts++
			task.work.LastCIStatus = "failure"
			task.work.LastCheckedCISHA = task.headSHA
			return
		}

		// Strip markdown formatting (bold, italic) before checking prefix
		cleaned := strings.TrimLeft(strings.TrimSpace(result.Result), "*_")
		if strings.HasPrefix(cleaned, "UNRELATED") {
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
				issueTitle := fmt.Sprintf("Flaky CI: %s", task.failures[0].Name)

				// Search for existing open issues with the same check name
				searchQuery := fmt.Sprintf("repo:%s/%s is:issue is:open label:%s \"%s\"",
					a.cfg.Owner, a.cfg.Repo, a.cfg.FlakyLabel, issueTitle)
				existingIssues, err := a.gh.SearchIssues(ctx, searchQuery)

				var issueNum int
				if err != nil {
					a.logger.Warn("failed to search for existing flaky issues", "error", err)
					// Continue with creation despite search failure
				} else if len(existingIssues) > 0 {
					// Issue already exists, reference it instead of creating a duplicate
					issueNum = existingIssues[0].Number
					a.logger.Info("found existing flaky CI issue", "issue", issueNum, "check", task.failures[0].Name)
					if err := a.gh.AddIssueComment(ctx, a.cfg.Owner, a.cfg.Repo, task.work.PRNumber,
						fmt.Sprintf("This appears to be a duplicate of existing flaky test issue #%d.\n\n%s", issueNum, botMarker)); err != nil {
						a.logger.Error("failed to post existing flaky issue reference comment", "pr", task.work.PRNumber, "error", err)
					}
					return
				}

				// No existing issue found, create a new one
				issueBody := fmt.Sprintf("A CI failure was detected that appears unrelated to PR changes.\n\n"+
					"**Detected in PR**: #%d\n"+
					"**Commit**: %s\n"+
					"**Failed check**: %s\n\n"+
					"**Analysis**:\n%s\n\n"+
					"**Check output**:\n```\n%s\n```\n\n"+
					"%s",
					task.work.PRNumber, shortSHA(task.headSHA), task.failures[0].Name, explanation, task.failures[0].Output, botMarker)
				issueNum, err = a.gh.CreateIssue(ctx, a.cfg.Owner, a.cfg.Repo, issueTitle, issueBody, []string{a.cfg.FlakyLabel})
				if err != nil {
					a.logger.Error("failed to create flaky CI issue", "error", err)
				} else {
					a.logger.Info("created flaky CI issue", "issue", issueNum)
					// Update the PR comment to reference the created issue
					if err := a.gh.AddIssueComment(ctx, a.cfg.Owner, a.cfg.Repo, task.work.PRNumber,
						fmt.Sprintf("Opened issue #%d to track this flaky test.\n\n%s", issueNum, botMarker)); err != nil {
						a.logger.Error("failed to post flaky issue reference comment", "pr", task.work.PRNumber, "error", err)
					}
				}
			}

			return
		}

		// Claude said RELATED — check if there are fixup commits or uncommitted changes
		pushed := false
		hasFixupCommits := a.hasFixupCommits(ctx, task.work.WorktreePath)
		hasUncommitted := a.hasUncommittedChanges(ctx, task.work.WorktreePath)

		if hasFixupCommits {
			// Run autosquash rebase to merge fixup commits into their targets
			if err := a.gitAutosquashRebase(ctx, task.work.WorktreePath); err != nil {
				a.logger.Error("failed to autosquash fixup commits", "pr", task.work.PRNumber, "error", err)
			} else if err := a.gitPush(ctx, task.work.WorktreePath, true); err != nil {
				a.logger.Error("failed to push CI fix", "pr", task.work.PRNumber, "error", err)
			} else {
				pushed = true
			}
		} else if hasUncommitted {
			// Only amend HEAD if this is a single-commit PR
			// For multi-commit PRs, Claude should have created fixup commits
			if len(task.commits) > 1 {
				a.logger.Error("CI fix produced uncommitted changes for multi-commit PR but no fixup commits", "pr", task.work.PRNumber, "commits", len(task.commits))
				// Don't amend HEAD — this would attach the fix to the wrong commit
			} else {
				// For single-commit PRs, amending HEAD is correct
				if err := a.gitAmendAll(ctx, task.work.WorktreePath); err != nil {
					a.logger.Error("failed to amend commit for CI fix", "pr", task.work.PRNumber, "error", err)
				} else if err := a.gitPush(ctx, task.work.WorktreePath, true); err != nil {
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

const maxConflictFixAttempts = 2

// ProcessConflicts checks for merge conflicts (dirty mergeable_state) and tries to resolve them.
// For simple rebases when a PR is just behind the base branch, use ProcessRebase instead.
func (a *Agent) ProcessConflicts(ctx context.Context) {
	// Sequential phase: GitHub API calls, worktree setup, git fetch, automatic rebase attempts
	var tasks []conflictTask

	for _, work := range a.state.ActiveIssues {
		if work.PRNumber == 0 || work.Status != "pr-open" {
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
		a.runner.Run(ctx, work.WorktreePath, "git", "fetch", "--all")

		// Try automatic rebase
		_, stderr, rebaseErr := a.runner.Run(ctx, work.WorktreePath, "git", "rebase", a.originDefaultBranch())
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
					fmt.Sprintf("Rebased commit %s on main and pushed.\n\n%s", shortSHA(headSHA), botMarker))
			}
			continue
		}

		// Rebase failed — abort and let Claude try
		a.runner.Run(ctx, work.WorktreePath, "git", "rebase", "--abort")
		a.logger.Info("automatic rebase failed, invoking Claude to resolve conflicts", "pr", work.PRNumber, "stderr", string(stderr))

		tasks = append(tasks, conflictTask{
			work:         work,
			headSHA:      headSHA,
			rebaseErr:    rebaseErr,
			rebaseStderr: string(stderr),
		})
	}

	// Parallel phase: Claude invocations for conflict resolution
	runParallel(ctx, a.cfg.MaxWorkers, tasks, func(ctx context.Context, task conflictTask) {
		// Get commit count before invoking Claude
		commitsBefore := a.getPRCommits(ctx, task.work.WorktreePath)
		commitCountBefore := len(commitsBefore)

		prompt := buildConflictResolutionPrompt(*task.work, a.originDefaultBranch(), a.cfg.SignedOffBy)
		_, err := runClaude(ctx, a.runner, task.work.WorktreePath, prompt, a.cfg, a.logger, true)
		if err != nil {
			a.logger.Error("claude failed to resolve conflicts", "pr", task.work.PRNumber, "error", err)
			_ = a.gh.AddIssueComment(ctx, a.cfg.Owner, a.cfg.Repo, task.work.PRNumber,
				fmt.Sprintf("Could not resolve conflicts on commit %s automatically. Human intervention needed.\n\n%s", shortSHA(task.headSHA), botMarker))
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

			// Warn user - the improved prompt should prevent this, but if it happens, request human intervention
			_ = a.gh.AddIssueComment(ctx, a.cfg.Owner, a.cfg.Repo, task.work.PRNumber,
				fmt.Sprintf("⚠️ Conflict resolution created %d unexpected new commit(s) instead of resolving within the rebase flow.\n\n"+
					"Expected: %d commits (original structure preserved)\n"+
					"Got: %d commits (new commits added)\n\n"+
					"Please review the commit history and manually squash or rebase to preserve the original commit structure.\n\n%s",
					commitCountAfter-commitCountBefore, commitCountBefore, commitCountAfter, botMarker))
		}

		// Push the rebased branch
		if err := a.gitPush(ctx, task.work.WorktreePath, true); err != nil {
			a.logger.Error("failed to push after conflict resolution", "pr", task.work.PRNumber, "error", err)
			_ = a.gh.AddIssueComment(ctx, a.cfg.Owner, a.cfg.Repo, task.work.PRNumber,
				fmt.Sprintf("Could not push conflict resolution for commit %s. Human intervention needed.\n\n%s", shortSHA(task.headSHA), botMarker))
		} else {
			_ = a.gh.AddIssueComment(ctx, a.cfg.Owner, a.cfg.Repo, task.work.PRNumber,
				fmt.Sprintf("Rebased commit %s on main and pushed (conflicts resolved).\n\n%s", shortSHA(task.headSHA), botMarker))
		}
	})
}

// ProcessRebase rebases PRs that are behind the base branch but have no merge conflicts.
// For PRs with actual merge conflicts (dirty state), use ProcessConflicts instead.
func (a *Agent) ProcessRebase(ctx context.Context) {
	for _, work := range a.state.ActiveIssues {
		if work.PRNumber == 0 || work.Status != "pr-open" {
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

		a.runner.Run(ctx, work.WorktreePath, "git", "fetch", "--all")

		_, stderr, rebaseErr := a.runner.Run(ctx, work.WorktreePath, "git", "rebase", a.originDefaultBranch())
		if rebaseErr != nil {
			// Rebase should not fail since there are no conflicts — abort and log
			a.runner.Run(ctx, work.WorktreePath, "git", "rebase", "--abort")
			a.logger.Error("rebase failed unexpectedly (no conflicts expected)", "pr", work.PRNumber, "stderr", string(stderr))
			continue
		}

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
				fmt.Sprintf("Rebased commit %s on main and pushed.\n\n%s", shortSHA(headSHA), botMarker))
		}
	}
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
		a.runner.Run(ctx, worktreePath, "git", "checkout", a.defaultBranch())

		// Build the triage prompt
		prompt := buildPeriodicCITriagePrompt(ciSource.JobName(), latestRun.ID, buildLog, a.cfg.Owner, a.cfg.Repo)

		// Run Claude in the worktree
		a.logger.Info("running Claude for CI triage", "job", ciSource.JobName(), "runID", latestRun.ID)
		stdout, stderr, err := a.runner.Run(ctx, worktreePath, "claude", "-p", prompt)
		if err != nil {
			a.logger.Error("Claude failed during CI triage", "job", ciSource.JobName(), "runID", latestRun.ID, "error", err, "stderr", string(stderr))
			_ = a.worktrees.RemoveWorktree(ctx, worktreePath)
			continue
		}

		analysis := string(stdout)
		a.logger.Info("CI triage analysis complete", "job", ciSource.JobName(), "runID", latestRun.ID)
		a.logger.Debug("analysis output", "output", analysis)

		// If --create-flaky-issues is set, create a GitHub issue with the analysis
		if a.cfg.CreateFlakyIssues {
			a.logger.Info("creating issue for CI failure", "job", ciSource.JobName(), "runID", latestRun.ID)

			// Search for existing issues about this job to avoid duplicates
			searchQuery := fmt.Sprintf("repo:%s/%s is:issue is:open in:title \"%s\"", a.cfg.Owner, a.cfg.Repo, ciSource.JobName())
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

// pushRemoteName returns the git remote name used for pushing ("fork" or "origin").
func (a *Agent) pushRemoteName() string {
	if wtm, ok := a.worktrees.(*GitWorktreeManager); ok {
		return wtm.PushRemote()
	}
	return "origin"
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
	a.runner.Run(ctx, worktreePath, "git", "restore", "--staged", ".pr-body.md")

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
	pushRemote := "origin"
	if wtm, ok := a.worktrees.(*GitWorktreeManager); ok {
		pushRemote = wtm.PushRemote()
	}
	_, stderr, err := a.runner.Run(ctx, worktreePath, "git", "push", pushRemote, "--delete", branchName)
	if err != nil {
		a.logger.Warn("failed to delete remote branch", "branch", branchName, "error", err, "stderr", string(stderr))
	} else {
		a.logger.Info("deleted remote branch", "branch", branchName)
	}
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
		// Fetch first so --force-with-lease has up-to-date tracking refs
		a.runner.Run(ctx, worktreePath, "git", "fetch", pushRemote)
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
