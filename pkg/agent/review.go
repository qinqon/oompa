package agent

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// advanceReviewCursors moves the three per-PR comment cursors forward to the
// given maxima. Cursors never move backwards.
func advanceReviewCursors(work *IssueWork, maxCommentID, maxReviewID, maxIssueCommentID int64) {
	work.LastCommentID = max(work.LastCommentID, maxCommentID)
	work.LastReviewID = max(work.LastReviewID, maxReviewID)
	work.LastIssueCommentID = max(work.LastIssueCommentID, maxIssueCommentID)
}

// ProcessReviewComments checks for new review comments and review bodies, then runs the coding agent to address them.
func (a *Agent) ProcessReviewComments(ctx context.Context) {
	defer a.trackAction(CategoryCheck, "reviewing", "Checking for review comments", "Review check complete")()
	// Scan phase: filter comments, prepare worktrees, add reactions, build tasks
	var tasks []reviewTask

	for _, work := range a.state.ActiveIssues {
		if work.PRNumber == 0 || work.Status != StatusPROpen {
			continue
		}

		// Skip review processing if this PR has exceeded the per-session cost threshold.
		if a.sessionCostExceeded(work, "reviews") {
			continue
		}

		comments, err := a.gh.GetPRReviewComments(ctx, a.cfg.Owner, a.cfg.Repo, work.PRNumber, work.LastCommentID)
		if err != nil {
			a.logger.Error("failed to get PR comments", "pr", work.PRNumber, "error", err)
			continue
		}

		// Track the max comment ID across ALL fetched comments (including filtered ones)
		// so the cursor advances past bot-posted/already-replied comments that would
		// otherwise be re-fetched and re-filtered on every poll cycle.
		var maxCommentID int64
		for _, c := range comments {
			if c.ID > maxCommentID {
				maxCommentID = c.ID
			}
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

		// Track the max review ID across ALL fetched reviews (including filtered ones).
		var maxReviewID int64
		for _, r := range reviews {
			if r.ID > maxReviewID {
				maxReviewID = r.ID
			}
		}

		// Filter reviews: skip bot-posted and non-whitelisted reviewers.
		// No headCommitDate filter needed here — GetPRReviews already filters
		// by sinceID (work.LastReviewID), so only unprocessed reviews are returned.
		// This prevents the race condition where multiple bot reviewers post
		// simultaneously: oompa addresses one, pushes (creating a new HEAD),
		// and the remaining reviews are correctly processed on the next cycle
		// because they haven't been cursor-advanced past yet.
		var humanReviews []PRReview
		for _, r := range reviews {
			if strings.Contains(r.Body, botMarker) {
				continue
			}
			if !a.isAllowedReviewer(r.User) {
				continue
			}
			humanReviews = append(humanReviews, r)
		}

		// Fetch PR conversation comments (Issues API) and filter for /oompa prefix.
		// These are regular comments on the PR conversation tab, not inline code review comments.
		issueComments, err := a.gh.GetIssueComments(ctx, a.cfg.Owner, a.cfg.Repo, work.PRNumber, work.LastIssueCommentID)
		if err != nil {
			a.logger.Error("failed to get PR issue comments", "pr", work.PRNumber, "error", err)
			continue
		}

		// Track the max issue comment ID across ALL fetched comments (including filtered ones).
		var maxIssueCommentID int64
		for _, c := range issueComments {
			if c.ID > maxIssueCommentID {
				maxIssueCommentID = c.ID
			}
		}

		// Filter PR conversation comments: only /oompa-prefixed, whitelisted, non-bot.
		var prComments []ReviewComment
		for _, c := range issueComments {
			if strings.Contains(c.Body, botMarker) {
				continue
			}
			if !a.isAllowedReviewer(c.User) {
				continue
			}
			fields := strings.Fields(c.Body)
			if len(fields) < 2 || fields[0] != oompaCommandPrefix {
				continue
			}
			prComments = append(prComments, c)
		}

		if len(humanComments) == 0 && len(humanReviews) == 0 && len(prComments) == 0 {
			// No actionable comments/reviews, but still advance cursors past
			// filtered items to avoid re-fetching them on every poll cycle.
			advanceReviewCursors(work, maxCommentID, maxReviewID, maxIssueCommentID)
			continue
		}

		// Skip review processing if this PR has hit the no-op retry limit.
		// This prevents infinite loops where the agent keeps being invoked on
		// the same reviews but can't push (e.g., persistent git corruption).
		// The counter resets when: (1) a push succeeds, or (2) the no-op limit
		// is reached and the problematic batch is skipped (below). This ensures
		// stale reviews are eventually skipped while new reviews can be processed.
		if a.cfg.MaxReviewNoOps > 0 && work.ReviewNoOpCount >= a.cfg.MaxReviewNoOps {
			// Advance cursors past these reviews to stop re-fetching them.
			advanceReviewCursors(work, maxCommentID, maxReviewID, maxIssueCommentID)
			a.logger.Warn("skipping stale reviews (no-op retry limit reached)",
				"pr", work.PRNumber,
				"noOpCount", work.ReviewNoOpCount,
				"limit", a.cfg.MaxReviewNoOps,
			)
			// Reset counter after skipping the problematic batch so new reviews
			// that arrive later can be processed. Without this reset, the PR
			// would be permanently blocked from review processing.
			work.ReviewNoOpCount = 0
			continue
		}

		a.logger.Info("addressing review feedback", "pr", work.PRNumber, "comments", len(humanComments), "reviews", len(humanReviews), "prComments", len(prComments))

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
		for _, c := range prComments {
			if err := a.gh.AddIssueCommentReaction(ctx, a.cfg.Owner, a.cfg.Repo, c.ID, "eyes"); err != nil {
				a.logger.Warn("failed to add issue comment reaction", "comment", c.ID, "error", err)
			}
		}

		tasks = append(tasks, reviewTask{
			work:              work,
			humanComments:     humanComments,
			humanReviews:      humanReviews,
			prComments:        prComments,
			maxCommentID:      maxCommentID,
			maxReviewID:       maxReviewID,
			maxIssueCommentID: maxIssueCommentID,
		})
	}

	// Agent phase: run agent to address review feedback, then push changes
	runSequential(ctx, tasks, func(ctx context.Context, task reviewTask) {
		a.emit(Event{
			Type:      EventAgentInvocation,
			Category:  CategoryAgent,
			Worker:    a.workerName(),
			State:     "reviewing",
			Action:    "Addressing review feedback",
			PRNumbers: []int{task.work.PRNumber},
		})
		agentStart := time.Now()

		// Capture local HEAD before agent runs so we can detect if it committed directly
		headBefore, _, err := a.runner.Run(ctx, task.work.WorktreePath, "git", "rev-parse", "HEAD")
		if err != nil {
			a.logger.Warn("failed to get HEAD before agent", "pr", task.work.PRNumber, "error", err)
		}
		headSHABefore := strings.TrimSpace(string(headBefore))

		// Get PR diff stat to scope the agent's changes to only files in this PR.
		// This prevents the agent from making sweeping unrelated changes when
		// addressing review feedback (e.g. removing recently-merged functionality).
		// Use three-dot diff (merge-base) to exclude upstream changes when the
		// branch is behind the default branch.
		var prDiffStat string
		diffStatOut, _, diffErr := a.runner.Run(ctx, task.work.WorktreePath, "git", "diff", "--stat", a.originDefaultBranch()+"...HEAD")
		if diffErr != nil {
			a.logger.Warn("failed to get PR diff stat for scope constraint", "pr", task.work.PRNumber, "error", diffErr)
		} else {
			prDiffStat = strings.TrimSpace(string(diffStatOut))
		}

		prompt := buildReviewResponsePrompt(*task.work, task.humanComments, task.humanReviews, task.prComments, a.cfg.Owner, a.cfg.Repo, prDiffStat)
		agentResult, err := a.codeAgent.Run(ctx, a.runner, task.work.WorktreePath, prompt, a.logger, true)
		// Track cumulative cost even on failure — failed invocations still consume
		// tokens and incur costs, so the MaxPRSessionCost guard must count them.
		task.work.SessionCostUSD += agentResult.CostUSD
		if err != nil {
			a.logger.Error("agent failed to address review", "pr", task.work.PRNumber, "error", err)
			a.emit(Event{
				Type:      EventError,
				Category:  CategoryError,
				Worker:    a.workerName(),
				State:     "error",
				Action:    "Agent failed to address review",
				PRNumbers: []int{task.work.PRNumber},
				Duration:  time.Since(agentStart).Seconds(),
				Error:     err.Error(),
			})
			// Advance cursors even on agent failure — the reviews were evaluated.
			// Not advancing causes an infinite retry loop where the same reviews
			// are re-fetched and re-processed every poll cycle ($0.50-1.00 each time).
			advanceReviewCursors(task.work, task.maxCommentID, task.maxReviewID, task.maxIssueCommentID)
			task.work.ReviewNoOpCount++
			return
		}
		a.emit(Event{
			Type:      EventAgentCompleted,
			Category:  CategoryAgent,
			Worker:    a.workerName(),
			State:     "idle",
			Action:    "Review feedback addressed",
			PRNumbers: []int{task.work.PRNumber},
			Duration:  time.Since(agentStart).Seconds(),
		})

		// Push if agent made changes (uncommitted or committed directly).
		// Track whether push succeeded so we can gate cursor advancement:
		// if changes were detected but push failed, don't advance the cursor
		// so the comments are retried on the next poll cycle.
		pushed, handled := a.pushFixupsOrAmend(ctx, task.work.WorktreePath, task.work.PRNumber)
		changeDetected := handled
		if !handled {
			headAfter, _, revErr := a.runner.Run(ctx, task.work.WorktreePath, "git", "rev-parse", "HEAD")
			headSHAAfter := strings.TrimSpace(string(headAfter))
			if revErr == nil && headSHABefore != "" && headSHAAfter != headSHABefore {
				changeDetected = true
				// Agent committed directly — squash new commits into the original HEAD
				// to avoid polluting commit history with "Address PR review feedback" commits.
				a.logger.Info("agent committed directly, squashing into original HEAD", "pr", task.work.PRNumber)
				if err := a.gitSquashInto(ctx, task.work.WorktreePath, headSHABefore); err != nil {
					a.logger.Error("failed to squash agent commits, skipping push to avoid unsquashed commits", "pr", task.work.PRNumber, "error", err)
					// Restore HEAD so the worktree is usable on the next attempt
					if _, _, resetErr := a.runner.Run(ctx, task.work.WorktreePath, "git", "reset", "--hard", headSHAAfter); resetErr != nil {
						a.logger.Error("failed to restore HEAD after squash failure", "pr", task.work.PRNumber, "error", resetErr)
					}
				} else if err := a.gitPush(ctx, task.work.WorktreePath, true); err != nil {
					a.logger.Error("failed to push", "pr", task.work.PRNumber, "error", err)
				} else {
					pushed = true
				}
			}
		}

		// Advance cursors based on ALL fetched comment/review IDs (not just
		// the human-filtered subset) to avoid re-fetching and re-filtering
		// bot-posted or already-replied comments on every poll cycle.
		// When changes were detected but push failed, skip cursor advancement
		// so the comments are retried on the next poll cycle.
		if !changeDetected || pushed {
			advanceReviewCursors(task.work, task.maxCommentID, task.maxReviewID, task.maxIssueCommentID)
		}

		// Comment on the PR with a link to the pushed change and a summary.
		if pushed {
			a.commentChangeSummary(ctx, task.work, headSHABefore)
		}

		// Track consecutive no-op cycles for retry loop detection.
		// A "no-op" is when the agent ran but no changes were pushed.
		if pushed {
			task.work.ReviewNoOpCount = 0 // reset on successful push
		} else {
			task.work.ReviewNoOpCount++
			a.logger.Info("review cycle produced no push",
				"pr", task.work.PRNumber,
				"noOpCount", task.work.ReviewNoOpCount,
				"changeDetected", changeDetected,
			)
		}
	})
}

// commentChangeSummary posts a comment on the PR with a compare URL and
// a summary of the changes that were pushed in response to review feedback.
func (a *Agent) commentChangeSummary(ctx context.Context, work *IssueWork, beforeSHA string) {
	afterOut, _, err := a.runner.Run(ctx, work.WorktreePath, "git", "rev-parse", "HEAD")
	if err != nil {
		a.logger.Warn("failed to get HEAD after push", "pr", work.PRNumber, "error", err)
		return
	}
	afterSHA := strings.TrimSpace(string(afterOut))

	if beforeSHA == "" || afterSHA == "" || beforeSHA == afterSHA {
		return
	}

	summary := a.buildChangeSummary(ctx, work, beforeSHA, afterSHA)

	compareURL := fmt.Sprintf("https://github.com/%s/%s/compare/%s..%s",
		a.cfg.Owner, a.cfg.Repo, beforeSHA, afterSHA)

	body := fmt.Sprintf("[Change](%s)\n%s\n\n%s", compareURL, summary, a.botComment())

	if err := a.gh.AddIssueComment(ctx, a.cfg.Owner, a.cfg.Repo, work.PRNumber, body); err != nil {
		a.logger.Warn("failed to post change summary comment", "pr", work.PRNumber, "error", err)
	}
}

// buildChangeSummary returns a semantic bullet-point summary of changes between two SHAs.
// It runs `git diff` to get the actual patch, passes it to the LLM for summarization,
// and returns concise human-readable descriptions of each logical change.
// Falls back to a generic message if the diff or LLM call fails.
// The LLM invocation cost is accumulated into the work item's session budget.
func (a *Agent) buildChangeSummary(ctx context.Context, work *IssueWork, beforeSHA, afterSHA string) string {
	const fallback = "- Updated code to address review feedback"

	out, _, err := a.runner.Run(ctx, work.WorktreePath, "git", "diff", "--no-color", beforeSHA, afterSHA)
	if err != nil {
		return fallback
	}

	diff := strings.TrimSpace(string(out))
	if diff == "" {
		return fallback
	}

	prompt := buildChangeSummaryPrompt(diff)
	result, err := a.codeAgent.Run(ctx, a.runner, work.WorktreePath, prompt, a.logger, false)
	// Track cumulative cost even on failure — failed invocations still
	// consume tokens and count against the per-PR session budget.
	work.SessionCostUSD += result.CostUSD
	if err != nil {
		a.logger.Warn("LLM summarization failed, using fallback", "error", err)
		return fallback
	}

	summary := strings.TrimSpace(result.Result)
	if summary == "" {
		return fallback
	}

	// Reject LLM output that contains raw diff or stat artifacts — posting these
	// would reproduce the original bug this change was meant to fix.
	if containsDiffArtifacts(summary) {
		a.logger.Warn("LLM summary contained diff/stat artifacts, using fallback")
		return fallback
	}

	// Ensure each line is a bullet point; the LLM should produce them but be defensive.
	lines := strings.Split(summary, "\n")
	var bullets []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "- ") {
			line = "- " + line
		}
		bullets = append(bullets, line)
	}

	if len(bullets) == 0 {
		return fallback
	}

	return strings.Join(bullets, "\n")
}

// containsDiffArtifacts returns true if the text contains raw diff or stat
// markers that indicate the LLM returned diff formatting instead of a
// semantic summary.
func containsDiffArtifacts(text string) bool {
	lower := strings.ToLower(text)
	artifacts := []string{
		"diff --git",
		"+++ ",
		"--- a/",
		"--- b/",
		"@@ ",
		" files changed",
		" insertions(+)",
		" deletions(-)",
	}
	for _, marker := range artifacts {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
