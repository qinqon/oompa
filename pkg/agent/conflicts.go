package agent

import (
	"context"
	"fmt"
	"time"
)

const (
	// rebaseQuietWindow is the look-back period to measure merge activity on the default branch.
	rebaseQuietWindow = 2 * time.Hour

	// rebaseQuietThreshold is the maximum number of recent commits on the default branch
	// to consider it "quiet" enough for a rebase. Above this, rebasing is deferred.
	rebaseQuietThreshold = 5

	// rebaseMinInterval is the minimum time between rebases for the same PR.
	// Prevents edge cases where quiet windows oscillate.
	rebaseMinInterval = 4 * time.Hour
)

// rebaseSuccessComment builds the human-visible comment for a successful
// rebase push. The commit reference is omitted when the head SHA could not
// be resolved, and the actual default branch name is used instead of a
// hardcoded "main".
func (a *Agent) rebaseSuccessComment(headSHA, suffix string) string {
	subject := "Rebased"
	if headSHA != "" {
		subject = "Rebased commit " + shortSHA(headSHA)
	}
	return fmt.Sprintf("%s on %s and pushed%s.\n\n%s", subject, a.defaultBranch(), suffix, a.botComment())
}

// autoRebasePR runs the shared per-PR rebase flow used by both
// ProcessConflicts and ProcessRebase: comment-marker dedup, worktree
// preparation, fetch, automatic rebase with retry, and push+comment on
// success. It returns a conflictTask when the rebase failed and should be
// escalated to the coding agent, or nil when the PR was handled (success,
// dedup skip, or logged error).
//
// commentCategory selects the skip-comment category for the success comment
// ("conflict" or "rebase"). When escalateAll is false, only failures that
// look like merge conflicts are escalated; other failures are logged.
func (a *Agent) autoRebasePR(ctx context.Context, work *IssueWork, mergeState, commentCategory string, escalateAll bool) *conflictTask {
	// Check if we already posted a rebase comment for the current HEAD
	headSHA, err := a.gh.GetPRHeadSHA(ctx, a.cfg.Owner, a.cfg.Repo, work.PRNumber)
	if err != nil {
		// Without the SHA, comment-marker dedup below is skipped, so a
		// repeat comment is possible — but proceeding beats stalling.
		a.logger.Warn("failed to get PR head SHA", "pr", work.PRNumber, "error", err)
	}
	if headSHA != "" && a.hasExistingBotComment(ctx, work.PRNumber, "rebase") && a.hasExistingBotComment(ctx, work.PRNumber, shortSHA(headSHA)) {
		return nil
	}

	a.logger.Info("PR needs rebase, attempting", "pr", work.PRNumber, "mergeable_state", mergeState)

	// ensureWorktreeReady fetches all remotes with a fatal error check
	// (SyncWorktree), so the rebase below runs against fresh upstream refs.
	if err := a.ensureWorktreeReady(ctx, work); err != nil {
		a.logger.Error("failed to prepare worktree", "pr", work.PRNumber, "error", err)
		return nil
	}

	// Try automatic rebase (with retry on unstaged changes)
	stderr, rebaseErr := a.gitRebaseWithRetry(ctx, work.WorktreePath, work.PRNumber)
	if rebaseErr == nil {
		// Rebase succeeded, force push
		if pushErr := a.gitPush(ctx, work.WorktreePath, true); pushErr != nil {
			a.logger.Error("failed to push after rebase", "pr", work.PRNumber, "error", pushErr)
			return nil
		}
		work.LastRebaseTime = time.Now()
		a.logger.Info("rebased and pushed successfully", "pr", work.PRNumber)
		a.emit(Event{
			Type:      EventActionCompleted,
			Category:  CategoryRebase,
			Worker:    a.workerName(),
			State:     "idle",
			Action:    "Rebased and pushed",
			PRNumbers: []int{work.PRNumber},
		})
		if !a.ShouldSkipComment(commentCategory) {
			_ = a.gh.AddIssueComment(ctx, a.cfg.Owner, a.cfg.Repo, work.PRNumber,
				a.rebaseSuccessComment(headSHA, ""))
		}
		return nil
	}

	// Rebase failed — abort before deciding whether to escalate
	a.runner.Run(ctx, work.WorktreePath, "git", "rebase", "--abort") //nolint:errcheck // best-effort

	if !escalateAll && !isConflictError(stderr) {
		// Non-conflict rebase failure (e.g., corrupt repo state, hook failure)
		a.logger.Error("rebase failed for non-conflict reason", "pr", work.PRNumber, "stderr", stderr)
		return nil
	}

	a.logger.Info("automatic rebase failed, invoking coding agent to resolve conflicts", "pr", work.PRNumber, "stderr", stderr)
	return &conflictTask{
		work:         work,
		headSHA:      headSHA,
		rebaseErr:    rebaseErr,
		rebaseStderr: stderr,
	}
}

// ProcessConflicts checks for merge conflicts (dirty mergeable_state) and tries to resolve them.
// For simple rebases when a PR is just behind the base branch, use ProcessRebase instead.
func (a *Agent) ProcessConflicts(ctx context.Context) {
	a.emit(Event{
		Type:     EventActionStarted,
		Category: CategoryCheck,
		Worker:   a.workerName(),
		State:    "working",
		Action:   "Checking for merge conflicts",
	})
	defer a.emit(Event{
		Type:     EventActionCompleted,
		Category: CategoryCheck,
		Worker:   a.workerName(),
		State:    "idle",
		Action:   "Conflict check complete",
	})
	// Scan phase: GitHub API calls, worktree setup, git fetch, automatic rebase attempts
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

		// A dirty PR can never auto-rebase cleanly; escalate every failure.
		if task := a.autoRebasePR(ctx, work, mergeState, "conflict", true); task != nil {
			tasks = append(tasks, *task)
		}
	}

	// Agent phase: resolve collected conflicts with the coding agent
	a.resolveConflictsSequential(ctx, tasks)
}

// shouldRebaseNow checks whether conditions are right to rebase a PR.
// It enforces a minimum interval between rebases and defers rebasing when
// the default branch is active (merge storm in progress).
// Returns (true, "") if rebase should proceed, or (false, reason) if deferred.
func (a *Agent) shouldRebaseNow(ctx context.Context, work *IssueWork) (allowed bool, reason string) {
	// Guard: minimum interval between rebases for the same PR
	minInterval := a.cfg.RebaseInterval
	if minInterval <= 0 {
		minInterval = rebaseMinInterval // fallback default (4h)
	}
	if !work.LastRebaseTime.IsZero() && time.Since(work.LastRebaseTime) < minInterval {
		a.logger.Debug("skipping rebase (minimum interval not reached)",
			"pr", work.PRNumber,
			"lastRebase", work.LastRebaseTime,
			"minInterval", minInterval)
		return false, "minimum interval not reached"
	}

	// Check recent merge activity on the default branch
	since := time.Now().Add(-rebaseQuietWindow)
	recentCommits, err := a.gh.CountCommitsSince(ctx, a.cfg.Owner, a.cfg.Repo, since)
	if err != nil {
		a.logger.Warn("failed to check main branch activity, proceeding with rebase", "error", err)
		return true, "" // fail-open: rebase if we can't check
	}

	if recentCommits > rebaseQuietThreshold {
		a.logger.Info("deferring rebase, main branch is active",
			"pr", work.PRNumber,
			"recentCommits", recentCommits,
			"window", rebaseQuietWindow,
			"threshold", rebaseQuietThreshold)
		return false, fmt.Sprintf("main is active, %d commits in last %s", recentCommits, rebaseQuietWindow)
	}

	return true, ""
}

// ProcessRebase rebases PRs that are behind the base branch but have no merge conflicts.
// For PRs with actual merge conflicts (dirty state), use ProcessConflicts instead.
// If a rebase fails due to conflicts, this delegates to the conflict resolution flow.
func (a *Agent) ProcessRebase(ctx context.Context) {
	a.emit(Event{
		Type:     EventActionStarted,
		Category: CategoryCheck,
		Worker:   a.workerName(),
		State:    "rebasing",
		Action:   "Checking for outdated PRs",
	})
	defer a.emit(Event{
		Type:     EventActionCompleted,
		Category: CategoryCheck,
		Worker:   a.workerName(),
		State:    "idle",
		Action:   "Rebase check complete",
	})
	// Scan phase: check states, try automatic rebase, collect failed conflicts into tasks
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

		// Dynamic rebase: check if now is a good time to rebase
		if ok, _ := a.shouldRebaseNow(ctx, work); !ok {
			continue
		}

		// A behind-but-clean PR should rebase without conflicts; escalate
		// only failures that actually look like conflicts.
		if task := a.autoRebasePR(ctx, work, mergeState, "rebase", false); task != nil {
			tasks = append(tasks, *task)
		}
	}

	// Agent phase: resolve collected conflicts with the coding agent
	a.resolveConflictsSequential(ctx, tasks)
}

// postRebaseMarker posts the hidden dedup marker for a failed conflict
// resolution so the SHA is skipped on the next cycle. Skipped when the head
// SHA is unknown: a degenerate marker would be junk and, without a SHA,
// marker dedup cannot apply anyway.
func (a *Agent) postRebaseMarker(ctx context.Context, prNumber int, headSHA string) {
	if headSHA == "" {
		return
	}
	_ = a.gh.AddIssueComment(ctx, a.cfg.Owner, a.cfg.Repo, prNumber,
		fmt.Sprintf("<!-- oompa-bot rebase:%s -->", shortSHA(headSHA)))
}

// resolveConflictsSequential invokes the coding agent to resolve conflicts for a list of tasks sequentially.
func (a *Agent) resolveConflictsSequential(ctx context.Context, tasks []conflictTask) {
	runSequential(ctx, tasks, func(ctx context.Context, task conflictTask) {
		a.emit(Event{
			Type:      EventAgentInvocation,
			Category:  CategoryConflict,
			Worker:    a.workerName(),
			State:     "working",
			Action:    "Resolving merge conflicts",
			PRNumbers: []int{task.work.PRNumber},
		})

		// Get commit count before invoking agent and validate capture
		commitsBefore := a.getPRCommits(ctx, task.work.WorktreePath)
		if commitsBefore == nil {
			a.logger.Error("failed to capture commits before resolution", "pr", task.work.PRNumber)
			return
		}
		commitCountBefore := len(commitsBefore)

		prompt := buildConflictResolutionPrompt(*task.work, a.originDefaultBranch())
		result, err := a.codeAgent.Run(ctx, a.runner, task.work.WorktreePath, prompt, a.logger, true)
		// Track cumulative cost even on failure — failed invocations still
		// consume tokens and count against the per-PR session budget.
		task.work.SessionCostUSD += result.CostUSD
		if err != nil {
			a.logger.Error("agent failed to resolve conflicts", "pr", task.work.PRNumber, "error", err)
			a.emit(Event{
				Type:      EventError,
				Category:  CategoryError,
				Worker:    a.workerName(),
				State:     "error",
				Action:    "Conflict resolution failed",
				PRNumbers: []int{task.work.PRNumber},
				Error:     err.Error(),
			})
			// Post a hidden marker so deduplication skips this SHA on the next cycle
			a.postRebaseMarker(ctx, task.work.PRNumber, task.headSHA)
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
					commitCountAfter-commitCountBefore, commitCountBefore, commitCountAfter, a.botComment())); err != nil {
				a.logger.Error("failed to log warning to github", "pr", task.work.PRNumber, "error", err)
			}
		}

		// Push the rebased branch
		if err := a.gitPush(ctx, task.work.WorktreePath, true); err != nil {
			a.logger.Error("failed to push after conflict resolution", "pr", task.work.PRNumber, "error", err)
			// Post a hidden marker so deduplication skips this SHA on the next cycle
			a.postRebaseMarker(ctx, task.work.PRNumber, task.headSHA)
		} else {
			task.work.LastRebaseTime = time.Now()
			a.emit(Event{
				Type:      EventActionCompleted,
				Category:  CategoryConflict,
				Worker:    a.workerName(),
				State:     "idle",
				Action:    "Conflicts resolved",
				PRNumbers: []int{task.work.PRNumber},
			})
			if !a.ShouldSkipComment("conflict") {
				if err := a.gh.AddIssueComment(ctx, a.cfg.Owner, a.cfg.Repo, task.work.PRNumber,
					a.rebaseSuccessComment(task.headSHA, " (conflicts resolved)")); err != nil {
					a.logger.Error("failed to log success to github", "pr", task.work.PRNumber, "error", err)
				}
			}
		}
	})
}
