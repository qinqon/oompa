package agent

import (
	"context"
	"strings"
	"time"
)

// oompaPrefix is the command prefix that triggers processing of PR conversation comments.
const oompaPrefix = "/oompa"

// prCommentTask holds the data needed to process a batch of /oompa directives on a PR.
type prCommentTask struct {
	work            *IssueWork
	directives      []ReviewComment
	maxPRCommentID  int64 // max ID across ALL fetched PR comments (including filtered ones)
}

// ProcessPRComments checks for new PR conversation-tab comments starting with /oompa,
// then runs the coding agent to address them.
func (a *Agent) ProcessPRComments(ctx context.Context) {
	a.emit(Event{
		Type:     EventActionStarted,
		Category: CategoryCheck,
		Worker:   a.workerName(),
		State:    "reviewing",
		Action:   "Checking for /oompa PR comments",
	})
	defer a.emit(Event{
		Type:     EventActionCompleted,
		Category: CategoryCheck,
		Worker:   a.workerName(),
		State:    "idle",
		Action:   "PR comment check complete",
	})

	// Sequential phase: filter comments, prepare worktrees, add reactions, build tasks
	var tasks []prCommentTask

	for _, work := range a.state.ActiveIssues {
		if work.PRNumber == 0 || work.Status != StatusPROpen {
			continue
		}

		// Skip if this PR has exceeded the per-session cost threshold.
		if a.cfg.MaxPRSessionCost > 0 && work.SessionCostUSD >= a.cfg.MaxPRSessionCost {
			a.logger.Warn("skipping PR comments (per-PR session cost limit reached)",
				"pr", work.PRNumber,
				"sessionCostUSD", work.SessionCostUSD,
				"limit", a.cfg.MaxPRSessionCost,
			)
			continue
		}

		// GetIssueComments fetches conversation-tab comments (not inline review comments).
		comments, err := a.gh.GetIssueComments(ctx, a.cfg.Owner, a.cfg.Repo, work.PRNumber, work.LastPRCommentID)
		if err != nil {
			a.logger.Error("failed to get PR issue comments", "pr", work.PRNumber, "error", err)
			continue
		}

		// Track the max comment ID across ALL fetched comments so the cursor advances
		// past bot-posted and non-matching comments.
		var maxPRCommentID int64
		for _, c := range comments {
			if c.ID > maxPRCommentID {
				maxPRCommentID = c.ID
			}
		}

		// Filter: only /oompa-prefixed comments from allowed reviewers, skip bot comments.
		var directives []ReviewComment
		for _, c := range comments {
			if strings.Contains(c.Body, botMarker) {
				continue
			}
			if !a.isAllowedReviewer(c.User) {
				continue
			}
			if !strings.HasPrefix(strings.TrimSpace(c.Body), oompaPrefix) {
				continue
			}
			directives = append(directives, c)
		}

		if len(directives) == 0 {
			// No actionable directives, but still advance cursor.
			if maxPRCommentID > work.LastPRCommentID {
				work.LastPRCommentID = maxPRCommentID
			}
			continue
		}

		a.logger.Info("addressing /oompa PR comment directives", "pr", work.PRNumber, "directives", len(directives))

		if err := a.ensureWorktreeReady(ctx, work); err != nil {
			a.logger.Error("failed to prepare worktree", "pr", work.PRNumber, "error", err)
			continue
		}

		tasks = append(tasks, prCommentTask{
			work:           work,
			directives:     directives,
			maxPRCommentID: maxPRCommentID,
		})
	}

	// Sequential phase: run agent to address directives, then push changes
	runSequential(ctx, tasks, func(ctx context.Context, task prCommentTask) {
		a.emit(Event{
			Type:      EventAgentInvocation,
			Category:  CategoryAgent,
			Worker:    a.workerName(),
			State:     "reviewing",
			Action:    "Addressing /oompa PR comment directives",
			PRNumbers: []int{task.work.PRNumber},
		})
		agentStart := time.Now()

		// Capture local HEAD before agent runs so we can detect if it committed directly
		headBefore, _, err := a.runner.Run(ctx, task.work.WorktreePath, "git", "rev-parse", "HEAD")
		if err != nil {
			a.logger.Warn("failed to get HEAD before agent", "pr", task.work.PRNumber, "error", err)
		}
		headSHABefore := strings.TrimSpace(string(headBefore))

		prompt := buildPRCommentDirectivePrompt(*task.work, task.directives, a.cfg.Owner, a.cfg.Repo)
		agentResult, err := a.codeAgent.Run(ctx, a.runner, task.work.WorktreePath, prompt, a.logger, true)
		task.work.SessionCostUSD += agentResult.CostUSD
		if err != nil {
			a.logger.Error("agent failed to address PR comment directives", "pr", task.work.PRNumber, "error", err)
			a.emit(Event{
				Type:      EventError,
				Category:  CategoryError,
				Worker:    a.workerName(),
				State:     "error",
				Action:    "Agent failed to address /oompa directives",
				PRNumbers: []int{task.work.PRNumber},
				Duration:  time.Since(agentStart).Seconds(),
				Error:     err.Error(),
			})
			// Advance cursor even on agent failure to avoid infinite retry loops.
			if task.maxPRCommentID > task.work.LastPRCommentID {
				task.work.LastPRCommentID = task.maxPRCommentID
			}
			return
		}
		a.emit(Event{
			Type:      EventAgentCompleted,
			Category:  CategoryAgent,
			Worker:    a.workerName(),
			State:     "idle",
			Action:    "PR comment directives addressed",
			PRNumbers: []int{task.work.PRNumber},
			Duration:  time.Since(agentStart).Seconds(),
		})

		// Push if agent made changes (uncommitted or committed directly).
		pushed, changeDetected := false, false
		hasFixups := a.hasFixupCommits(ctx, task.work.WorktreePath)
		hasUncommitted := a.hasUncommittedChanges(ctx, task.work.WorktreePath)

		switch {
		case hasFixups:
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
				a.logger.Info("agent committed directly, squashing into original HEAD", "pr", task.work.PRNumber)
				if err := a.gitSquashInto(ctx, task.work.WorktreePath, headSHABefore); err != nil {
					a.logger.Error("failed to squash agent commits, skipping push", "pr", task.work.PRNumber, "error", err)
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

		// Advance cursor and react with :eyes: only after successful completion.
		// Skip when changes were detected but push failed — the directives
		// will be retried on the next poll cycle.
		if !changeDetected || pushed {
			for _, d := range task.directives {
				if err := a.gh.AddIssueCommentReaction(ctx, a.cfg.Owner, a.cfg.Repo, d.ID, "eyes"); err != nil {
					a.logger.Warn("failed to add reaction to PR comment", "comment", d.ID, "error", err)
				}
			}
			if task.maxPRCommentID > task.work.LastPRCommentID {
				task.work.LastPRCommentID = task.maxPRCommentID
			}
		}

		if pushed {
			a.logger.Info("pushed changes for /oompa directives", "pr", task.work.PRNumber)
		} else if changeDetected {
			a.logger.Warn("changes detected but push failed for /oompa directives", "pr", task.work.PRNumber)
		}
	})
}
