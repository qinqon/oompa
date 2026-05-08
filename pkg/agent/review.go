package agent

import (
	"context"
	"strings"
	"time"
)

// ProcessReviewComments checks for new review comments and review bodies, then runs Claude to address them.
func (a *Agent) ProcessReviewComments(ctx context.Context) {
	a.emit(Event{
		Type:   EventActionStarted,
		Worker: a.workerName(),
		State:  "reviewing",
		Action: "Checking for review comments",
	})
	defer a.emit(Event{
		Type:   EventActionCompleted,
		Worker: a.workerName(),
		State:  "idle",
		Action: "Review check complete",
	})
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
			// No actionable comments/reviews, but still advance cursors past
			// filtered items to avoid re-fetching them on every poll cycle.
			if maxCommentID > work.LastCommentID {
				work.LastCommentID = maxCommentID
			}
			if maxReviewID > work.LastReviewID {
				work.LastReviewID = maxReviewID
			}
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
			maxCommentID:  maxCommentID,
			maxReviewID:   maxReviewID,
		})
	}

	// Sequential phase: run agent to address review feedback, then push changes
	runSequential(ctx, tasks, func(ctx context.Context, task reviewTask) {
		a.emit(Event{
			Type:      EventAgentInvocation,
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

		prompt := buildReviewResponsePrompt(*task.work, task.humanComments, task.humanReviews, a.cfg.Owner, a.cfg.Repo)
		_, err = a.codeAgent.Run(ctx, a.runner, task.work.WorktreePath, prompt, a.logger, true)
		if err != nil {
			a.logger.Error("agent failed to address review", "pr", task.work.PRNumber, "error", err)
			a.emit(Event{
				Type:      EventError,
				Worker:    a.workerName(),
				State:     "error",
				Action:    "Agent failed to address review",
				PRNumbers: []int{task.work.PRNumber},
				Duration:  time.Since(agentStart).Seconds(),
				Error:     err.Error(),
			})
			return
		}
		a.emit(Event{
			Type:      EventAgentCompleted,
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
		pushed, changeDetected := false, false
		hasFixups := a.hasFixupCommits(ctx, task.work.WorktreePath)
		hasUncommitted := a.hasUncommittedChanges(ctx, task.work.WorktreePath)

		switch {
		case hasFixups:
			// Agent created fixup commits — autosquash them into their targets
			changeDetected = true
			if err := a.gitAutosquashRebase(ctx, task.work.WorktreePath); err != nil {
				a.logger.Error("failed to autosquash fixup commits", "pr", task.work.PRNumber, "error", err)
			} else if err := a.gitPush(ctx, task.work.WorktreePath, true); err != nil {
				a.logger.Error("failed to push", "pr", task.work.PRNumber, "error", err)
			} else {
				pushed = true
			}
		case hasUncommitted:
			changeDetected = true
			if err := a.gitAmendAll(ctx, task.work.WorktreePath); err != nil {
				a.logger.Error("failed to amend commit", "pr", task.work.PRNumber, "error", err)
			} else if err := a.gitPush(ctx, task.work.WorktreePath, true); err != nil {
				a.logger.Error("failed to push", "pr", task.work.PRNumber, "error", err)
			} else {
				pushed = true
			}
		default:
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
			if task.maxCommentID > task.work.LastCommentID {
				task.work.LastCommentID = task.maxCommentID
			}
			if task.maxReviewID > task.work.LastReviewID {
				task.work.LastReviewID = task.maxReviewID
			}
		}
	})
}
