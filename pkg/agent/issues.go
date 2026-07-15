package agent

import (
	"context"
	"fmt"
	"strings"
)

// ProcessNewIssues finds labeled issues and runs the coding agent to implement fixes.
func (a *Agent) ProcessNewIssues(ctx context.Context) {
	if a.cfg.Label == "" {
		return
	}

	a.emit(Event{
		Type:     EventActionStarted,
		Category: CategoryCheck,
		Worker:   a.workerName(),
		State:    "working",
		Action:   "Scanning for new issues",
	})
	defer a.emit(Event{
		Type:     EventActionCompleted,
		Category: CategoryCheck,
		Worker:   a.workerName(),
		State:    "idle",
		Action:   "Issue scanning complete",
	})

	issues, err := a.gh.ListLabeledIssues(ctx, a.cfg.Owner, a.cfg.Repo, a.cfg.Label)
	if err != nil {
		a.logger.Error("failed to list issues", "error", err)
		a.emit(Event{Type: EventError, Category: CategoryError, Worker: a.workerName(), State: "error", Error: "failed to list issues: " + err.Error()})
		return
	}

	// Scan phase: filter issues, create worktrees, insert into state
	var tasks []newIssueTask

	for _, issue := range issues {
		issueKey := IssueKey(a.cfg.Owner, a.cfg.Repo, issue.Number)
		if work, exists := a.state.ActiveIssues[issueKey]; exists {
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

		// Check if a PR already exists for this issue (open, closed, or merged).
		// Fail closed on error: without PR visibility we risk opening a
		// duplicate — defer the issue to the next poll cycle instead.
		prs, err := a.gh.ListPRsByHead(ctx, a.cfg.Owner, a.cfg.Repo, a.cfg.GitHubHeadOwner, branchName)
		if err != nil {
			a.logger.Warn("failed to list PRs for issue, deferring to next poll", "issue", issue.Number, "error", err)
			continue
		}
		if len(prs) > 0 {
			// Find the first open PR, or check if any was merged
			openPR, hasMerged := classifyPRs(prs)
			if openPR != nil {
				a.logger.Info("PR already exists for issue", "issue", issue.Number, "pr", openPR.Number)
				a.state.ActiveIssues[issueKey] = &IssueWork{
					IssueNumber: issue.Number,
					IssueTitle:  issue.Title,
					BranchName:  branchName,
					PRNumber:    openPR.Number,
					Status:      StatusPROpen,
				}
				continue
			} else if hasMerged {
				// PR was merged — skip to avoid reprocessing a completed issue
				a.logger.Info("skipping issue with merged PR", "issue", issue.Number, "pr", prs[0].Number)
				continue
			}
			// PR was closed (rejected) — fall through to allow retry
		}

		// Check if any open PR already references this issue (e.g., created by
		// a human). Fail closed on error: defer to the next poll cycle rather
		// than risk creating a duplicate PR.
		if linked, err := a.gh.HasLinkedPR(ctx, a.cfg.Owner, a.cfg.Repo, issue.Number); err != nil {
			a.logger.Warn("failed to check for linked PRs, deferring to next poll", "issue", issue.Number, "error", err)
			continue
		} else if linked {
			a.logger.Info("skipping issue with existing linked PR", "issue", issue.Number)
			continue
		}

		// Only post in-progress comment if we haven't already
		if !a.hasExistingBotComment(ctx, issue.Number, "working on this issue") {
			if err := a.gh.AssignIssue(ctx, a.cfg.Owner, a.cfg.Repo, issue.Number, a.cfg.GitHubUser); err != nil {
				a.logger.Warn("failed to assign issue", "issue", issue.Number, "error", err)
			}
			if !a.ShouldSkipComment("issue-in-progress") {
				if err := a.gh.AddIssueComment(ctx, a.cfg.Owner, a.cfg.Repo, issue.Number,
					fmt.Sprintf("Oompa is working on this issue. A PR will be created shortly.\n\n%s", a.botComment())); err != nil {
					a.logger.Warn("failed to add in-progress comment", "issue", issue.Number, "error", err)
				}
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
		}

		// Insert into state map before the agent phase
		a.state.ActiveIssues[issueKey] = work

		tasks = append(tasks, newIssueTask{
			issue:        issue,
			branchName:   branchName,
			worktreePath: worktreePath,
			work:         work,
		})
	}

	// Agent phase: implement each collected issue, push, create PR
	runSequential(ctx, tasks, func(ctx context.Context, task newIssueTask) {
		var success bool
		defer func() {
			if !success {
				a.emit(Event{
					Type:     EventAgentCompleted,
					Category: CategoryAgent,
					Worker:   a.workerName(),
					State:    "idle",
					Action:   fmt.Sprintf("Agent failed implementing issue #%d", task.issue.Number),
				})
				a.emit(Event{
					Type:     EventActionCompleted,
					Category: CategoryIssue,
					Worker:   a.workerName(),
					State:    "idle",
					Action:   fmt.Sprintf("Failed to process issue #%d", task.issue.Number),
				})
			} else {
				a.emit(Event{
					Type:     EventAgentCompleted,
					Category: CategoryAgent,
					Worker:   a.workerName(),
					State:    "idle",
					Action:   fmt.Sprintf("Agent finished implementing issue #%d", task.issue.Number),
				})
			}
		}()

		a.emit(Event{
			Type:     EventActionStarted,
			Category: CategoryIssue,
			Worker:   a.workerName(),
			State:    "working",
			Action:   fmt.Sprintf("Working on issue #%d: %s", task.issue.Number, task.issue.Title),
		})
		a.emit(Event{
			Type:     EventAgentInvocation,
			Category: CategoryAgent,
			Worker:   a.workerName(),
			State:    "working",
			Action:   fmt.Sprintf("Agent implementing issue #%d", task.issue.Number),
		})
		prompt := buildImplementationPrompt(task.issue, a.cfg.SignedOffBy, a.cfg.AssistedBy)
		result, err := a.codeAgent.Run(ctx, a.runner, task.worktreePath, prompt, a.logger, false)
		// Track cumulative cost even on failure — failed invocations still
		// consume tokens and count against the per-PR session budget.
		task.work.SessionCostUSD += result.CostUSD
		if err != nil {
			a.logger.Error("agent failed", "issue", task.issue.Number, "error", err)
			a.markIssueFailed(ctx, task.issue.Number, task.work)
			return
		}

		// Check if the agent produced any commits
		logOut, logStderr, logErr := a.runner.Run(ctx, task.worktreePath, "git", "log", a.originDefaultBranch()+"..HEAD", "--oneline")
		if logErr != nil {
			// Distinguish a broken worktree from a genuinely empty branch so
			// the failure log doesn't blame the agent for a git error.
			a.logger.Error("failed to check for commits", "issue", task.issue.Number, "error", logErr, "stderr", string(logStderr))
			a.markIssueFailed(ctx, task.issue.Number, task.work)
			return
		}
		if strings.TrimSpace(string(logOut)) == "" {
			a.logger.Warn("agent finished but produced no commits", "issue", task.issue.Number)
			a.markIssueFailed(ctx, task.issue.Number, task.work)
			return
		}

		// Treat comment-only changes as a failed fix: no functional code changed,
		// so opening a PR would waste reviewer time.
		if a.isCommentOnlyDiff(ctx, task.worktreePath) {
			a.logger.Warn("agent produced comment-only changes, treating as failed fix", "issue", task.issue.Number)
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
		a.emit(Event{
			Type:      EventActionCompleted,
			Category:  CategoryIssue,
			Worker:    a.workerName(),
			State:     "idle",
			Action:    fmt.Sprintf("Created PR #%d for issue #%d", prNumber, task.issue.Number),
			PRNumbers: []int{prNumber},
		})
		success = true

		_ = a.gh.UnassignIssue(ctx, a.cfg.Owner, a.cfg.Repo, task.issue.Number, a.cfg.GitHubUser)
	})
}
