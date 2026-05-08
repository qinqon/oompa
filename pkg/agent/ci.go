package agent

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const maxCIFixAttempts = 3

// ProcessCIFailures checks CI status for open PRs and invokes Claude to fix failures.
func (a *Agent) ProcessCIFailures(ctx context.Context) {
	a.emit(Event{
		Type:   EventActionStarted,
		Worker: a.workerName(),
		State:  "working",
		Action: "Checking CI status",
	})
	defer a.emit(Event{
		Type:   EventActionCompleted,
		Worker: a.workerName(),
		State:  "idle",
		Action: "CI check complete",
	})
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
		allCompleted := true
		for _, r := range runs {
			if r.Status == "completed" && r.Conclusion == "failure" {
				failures = append(failures, r)
			}
			if r.Status != "completed" {
				allCompleted = false
			}
		}

		if len(runs) == 0 {
			// No check runs registered yet — don't mark as checked.
			// This avoids the vacuous-truth bug where allCompleted is true
			// (its initial value) because the loop over runs never executed.
			a.logger.Debug("no check runs registered yet, waiting", "pr", work.PRNumber, "sha", shortSHA(headSHA))
			continue
		}
		if len(failures) == 0 {
			// If all checks completed with no failures and this SHA was already
			// checked, skip. Only set LastCheckedCISHA when all checks are done
			// to avoid skipping late-finishing failures.
			if allCompleted && work.LastCheckedCISHA != headSHA {
				work.LastCheckedCISHA = headSHA
			}
			continue
		}

		// Fast path: skip if this SHA was fully checked (all completed, no fix attempts)
		// and there are no uninvestigated failures
		if work.LastCheckedCISHA == headSHA && work.CIFixAttempts == 0 && allCompleted {
			continue
		}

		// Filter out failures already investigated for this SHA.
		// This prevents re-investigating a check that was already classified
		// as RELATED or UNRELATED in a previous poll cycle.
		var newFailures []CheckRun
		for _, f := range failures {
			if a.alreadyCheckedCI(ctx, work.PRNumber, headSHA, f.Name) {
				a.logger.Info("CI already investigated for this check, skipping", "pr", work.PRNumber, "sha", shortSHA(headSHA), "check", f.Name)
				continue
			}
			newFailures = append(newFailures, f)
		}

		if len(newFailures) == 0 {
			work.LastCheckedCISHA = headSHA
			continue
		}

		// Fetch logs for each failing check when output is missing or too short.
		// Threshold of 50 chars filters out generic GitHub Actions boilerplate
		// (e.g., "Process completed with exit code 1") that doesn't provide
		// enough context for meaningful analysis.
		// Skip entries with ID==0: these are commit-status entries (e.g. Prow)
		// where Output contains a target_url, not log text, and no check-run
		// log is available via the GitHub Actions API.
		for i, f := range newFailures {
			if f.ID == 0 {
				continue
			}
			trimmed := strings.TrimSpace(f.Output)
			if len(trimmed) < 50 {
				log, err := a.gh.GetCheckRunLog(ctx, a.cfg.Owner, a.cfg.Repo, f.ID)
				if err != nil {
					a.logger.Warn("failed to get check run log", "check", f.Name, "error", err)
				} else if log != "" {
					newFailures[i].Output = log
				}
			}
		}

		a.logger.Info("CI failing, investigating", "pr", work.PRNumber, "failures", len(newFailures), "attempt", work.CIFixAttempts+1)

		if err := a.ensureWorktreeReady(ctx, work); err != nil {
			a.logger.Error("failed to prepare worktree", "pr", work.PRNumber, "error", err)
			continue
		}

		// Get PR diff to help Claude determine if failure is related
		diffOut, _, _ := a.runner.Run(ctx, work.WorktreePath, "git", "diff", "--stat", a.originDefaultBranch())
		diff := string(diffOut)

		// Get commits in the PR to help Claude identify which commit introduced the failure
		commits := a.getPRCommits(ctx, work.WorktreePath)

		// Create one task per failure so each gets its own RELATED/UNRELATED classification.
		// This prevents a single unrelated failure (e.g. a policy check) from masking a
		// related failure (e.g. a unit test) when they fail on the same SHA.
		for _, f := range newFailures {
			tasks = append(tasks, ciTask{
				work:     work,
				headSHA:  headSHA,
				failures: []CheckRun{f},
				diff:     diff,
				commits:  commits,
			})
		}
	}

	// Sequential phase: Claude invocations and post-processing
	runSequential(ctx, tasks, func(ctx context.Context, task ciTask) {
		prompt := buildCIFixPrompt(*task.work, task.failures, task.diff, task.commits, a.cfg.SignedOffBy, a.cfg.SkipFix)
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

		// Check if the response contains UNRELATED, INFRASTRUCTURE, or RELATED.
		// The agent often puts detailed analysis before the keyword, sometimes
		// many lines deep. Scan the entire response for the keyword on any line.
		// Priority: INFRASTRUCTURE > UNRELATED > RELATED (check in this order
		// so RELATED doesn't match the "UNRELATED" substring).
		foundKeyword := ""
		for line := range strings.SplitSeq(cleaned, "\n") {
			trimmed := strings.TrimLeft(strings.TrimSpace(line), "*_-—:>")
			switch {
			case strings.HasPrefix(trimmed, "INFRASTRUCTURE"):
				foundKeyword = "INFRASTRUCTURE"
			case strings.HasPrefix(trimmed, "UNRELATED") && foundKeyword == "":
				foundKeyword = "UNRELATED"
			case strings.HasPrefix(trimmed, "RELATED") && foundKeyword == "":
				foundKeyword = "RELATED"
			}
			if foundKeyword == "INFRASTRUCTURE" {
				break // highest priority, stop scanning
			}
		}

		if foundKeyword == "" {
			a.logger.Warn("agent response did not contain UNRELATED, INFRASTRUCTURE, or RELATED, skipping to avoid noise",
				"pr", task.work.PRNumber,
				"response_preview", truncateString(cleaned, 200))
			task.work.CIFixAttempts++
			task.work.LastCIStatus = "investigation-inconclusive"
			task.work.LastCheckedCISHA = task.headSHA
			return
		}

		if foundKeyword == "INFRASTRUCTURE" {
			a.handleCIInfrastructure(ctx, task, cleaned)
			return
		}

		if foundKeyword == "UNRELATED" {
			a.handleCIUnrelated(ctx, task, cleaned)
			return
		}

		// Claude said RELATED
		a.handleCIRelated(ctx, task, cleaned)
	})
}

// handleCIInfrastructure handles a CI failure classified as an infrastructure issue.
func (a *Agent) handleCIInfrastructure(ctx context.Context, task ciTask, cleaned string) {
	a.logger.Info("CI failure is an infrastructure issue", "pr", task.work.PRNumber)
	idx := strings.Index(cleaned, "INFRASTRUCTURE")
	explanation := strings.TrimSpace(strings.TrimLeft(strings.TrimPrefix(cleaned[idx:], "INFRASTRUCTURE"), " :—–-"))
	marker := ciMarker(task.headSHA, task.failures[0].Name)
	if a.ShouldSkipComment("ci-infrastructure") {
		a.logger.Info("skipping CI infrastructure comment (--skip-comment)", "pr", task.work.PRNumber)
		a.markCIChecked(task.work, task.headSHA, task.failures[0].Name)
	} else {
		comment := fmt.Sprintf("CI check `%s` failed on commit %s due to an infrastructure issue (not a flaky test).", task.failures[0].Name, shortSHA(task.headSHA))
		if explanation != "" {
			comment += "\n\n" + explanation
		}
		comment += "\n\n" + marker
		if err := a.gh.AddIssueComment(ctx, a.cfg.Owner, a.cfg.Repo, task.work.PRNumber, comment); err != nil {
			a.logger.Error("failed to post CI infrastructure comment", "pr", task.work.PRNumber, "error", err)
			task.work.LastCIStatus = "investigation-inconclusive"
			return
		}
	}
	task.work.LastCIStatus = "infrastructure-failure"
	task.work.LastCheckedCISHA = task.headSHA
}

// handleCIUnrelated handles a CI failure classified as unrelated to PR changes.
func (a *Agent) handleCIUnrelated(ctx context.Context, task ciTask, cleaned string) {
	a.logger.Info("CI failure is unrelated to PR changes", "pr", task.work.PRNumber)
	idx := strings.Index(cleaned, "UNRELATED")
	explanation := strings.TrimSpace(strings.TrimLeft(strings.TrimPrefix(cleaned[idx:], "UNRELATED"), " :—–-"))
	marker := ciMarker(task.headSHA, task.failures[0].Name)
	if a.ShouldSkipComment("ci-unrelated") {
		a.logger.Info("skipping CI unrelated comment (--skip-comment)", "pr", task.work.PRNumber)
		a.markCIChecked(task.work, task.headSHA, task.failures[0].Name)
	} else {
		comment := fmt.Sprintf("CI check `%s` failed on commit %s but appears unrelated to this PR's changes.", task.failures[0].Name, shortSHA(task.headSHA))
		if explanation != "" {
			comment += "\n\n" + explanation
		}
		comment += "\n\n" + marker
		if err := a.gh.AddIssueComment(ctx, a.cfg.Owner, a.cfg.Repo, task.work.PRNumber, comment); err != nil {
			a.logger.Error("failed to post CI unrelated comment", "pr", task.work.PRNumber, "error", err)
			task.work.LastCIStatus = "investigation-inconclusive"
			return
		}
	}
	task.work.LastCIStatus = "unrelated-failure"
	task.work.LastCheckedCISHA = task.headSHA

	// Create a flaky CI issue if configured
	if a.cfg.CreateFlakyIssues {
		a.createFlakyIssue(ctx, task, explanation)
	}
}

// createFlakyIssue creates or matches a flaky CI issue for an unrelated failure.
func (a *Agent) createFlakyIssue(ctx context.Context, task ciTask, explanation string) {
	// Skip flaky issue creation if both the check run output and the
	// agent's explanation are too short.
	trimmedOutput := strings.TrimSpace(task.failures[0].Output)
	if len(trimmedOutput) < 50 && len(explanation) < 50 {
		a.logger.Warn("skipping flaky issue creation: insufficient context",
			"pr", task.work.PRNumber,
			"check", task.failures[0].Name,
			"output_length", len(trimmedOutput),
			"explanation_length", len(explanation))
		return
	}

	failingTest := parseFailingTest(explanation)
	var issueTitle string
	if failingTest != "" {
		issueTitle = fmt.Sprintf("Flaky CI: %s / %s", task.failures[0].Name, failingTest)
	} else {
		issueTitle = fmt.Sprintf("Flaky CI: %s", task.failures[0].Name)
	}

	// Search for existing open issues with the flaky label
	searchQuery := fmt.Sprintf("repo:%s/%s is:issue is:open label:%s",
		a.cfg.Owner, a.cfg.Repo, a.cfg.FlakyLabel)
	existingIssues, err := a.gh.SearchIssues(ctx, searchQuery)

	var issueNum int
	if err != nil {
		a.logger.Warn("failed to search for existing flaky issues", "error", err)
	} else if len(existingIssues) > 0 {
		// Fast path: exact title match skips LLM matching entirely.
		for _, existing := range existingIssues {
			if existing.Title == issueTitle {
				issueNum = existing.Number
				a.logger.Info("exact title match for existing flaky CI issue", "issue", issueNum, "check", task.failures[0].Name)
				break
			}
		}

		// If no exact title match, ask the agent for root-cause matching.
		if issueNum == 0 {
			matchOutput := task.failures[0].Output
			if len(strings.TrimSpace(matchOutput)) < 50 {
				matchOutput = explanation
			}
			matchPrompt := buildFlakyMatchPrompt(task.failures[0].Name, matchOutput, existingIssues)
			matchResult, matchErr := a.codeAgent.Run(ctx, a.runner, task.work.WorktreePath, matchPrompt, a.logger, false)
			if matchErr != nil {
				a.logger.Warn("failed to run agent for flaky issue matching", "error", matchErr)
			} else {
				matchResponse := strings.TrimSpace(matchResult.Result)
				if matchedNum, ok := parseFlakyMatch(matchResponse); ok {
					for _, existing := range existingIssues {
						if existing.Number == matchedNum {
							issueNum = matchedNum
							break
						}
					}
					if issueNum > 0 {
						a.logger.Info("agent matched existing flaky CI issue", "issue", issueNum, "check", task.failures[0].Name)
					} else {
						a.logger.Warn("agent returned MATCH for unknown issue", "matched_issue", matchedNum, "check", task.failures[0].Name)
					}
				}
			}
		}

		if issueNum > 0 {
			if !a.ShouldSkipComment("flaky") {
				if err := a.gh.AddIssueComment(ctx, a.cfg.Owner, a.cfg.Repo, task.work.PRNumber,
					fmt.Sprintf("This appears to be a duplicate of existing flaky test issue #%d.\n\n%s", issueNum, a.botComment())); err != nil {
					a.logger.Error("failed to post existing flaky issue reference comment", "pr", task.work.PRNumber, "error", err)
				}
			}
			return
		}
	}

	// No existing issue matched, create a new one
	testNameForBody := task.failures[0].Name
	if failingTest != "" {
		testNameForBody = failingTest
	}
	// Strip the FAILING_TEST: line from the explanation
	cleanedExplanation := explanation
	if failingTest != "" {
		var lines []string
		for line := range strings.SplitSeq(explanation, "\n") {
			if !strings.HasPrefix(strings.TrimSpace(line), "FAILING_TEST:") {
				lines = append(lines, line)
			}
		}
		cleanedExplanation = strings.TrimSpace(strings.Join(lines, "\n"))
	}
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
		testNameForBody, time.Now().Format("2006-01-02"), cleanedExplanation,
		a.botComment())
	issueNum, err = a.gh.CreateIssue(ctx, a.cfg.Owner, a.cfg.Repo, issueTitle, issueBody, []string{a.cfg.FlakyLabel})
	if err != nil {
		a.logger.Error("failed to create flaky CI issue", "error", err)
	} else {
		a.logger.Info("created flaky CI issue", "issue", issueNum)
		if !a.ShouldSkipComment("flaky") {
			if err := a.gh.AddIssueComment(ctx, a.cfg.Owner, a.cfg.Repo, task.work.PRNumber,
				fmt.Sprintf("Opened issue #%d to track this flaky test.\n\n%s", issueNum, a.botComment())); err != nil {
				a.logger.Error("failed to post flaky issue reference comment", "pr", task.work.PRNumber, "error", err)
			}
		}
	}
}

// handleCIRelated handles a CI failure classified as related to PR changes.
func (a *Agent) handleCIRelated(ctx context.Context, task ciTask, cleaned string) {
	if a.cfg.SkipFix {
		// Skip-fix mode: just post the analysis, don't try to fix or push
		a.logger.Info("CI failure is related (skip-fix mode, not pushing)", "pr", task.work.PRNumber)
		idx := strings.Index(cleaned, "RELATED")
		analysis := strings.TrimPrefix(cleaned[idx:], "RELATED")
		analysis = strings.TrimSpace(analysis)
		marker := ciMarker(task.headSHA, task.failures[0].Name)
		if a.ShouldSkipComment("ci-related") {
			a.logger.Info("skipping CI related comment (--skip-comment)", "pr", task.work.PRNumber)
			a.markCIChecked(task.work, task.headSHA, task.failures[0].Name)
		} else {
			if err := a.gh.AddIssueComment(ctx, a.cfg.Owner, a.cfg.Repo, task.work.PRNumber,
				fmt.Sprintf("CI check `%s` is failing on commit %s and appears related to this PR's changes.\n\n%s\n\n%s", task.failures[0].Name, shortSHA(task.headSHA), analysis, marker)); err != nil {
				a.logger.Error("failed to post CI analysis comment", "pr", task.work.PRNumber, "error", err)
			}
		}
		task.work.LastCIStatus = "related-skip-fix"
		task.work.LastCheckedCISHA = task.headSHA
		return
	}

	// Check if there are fixup commits, amended commits, or uncommitted changes
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
		newHeadSHA, err := a.gh.GetPRHeadSHA(ctx, a.cfg.Owner, a.cfg.Repo, task.work.PRNumber)
		if err != nil {
			a.logger.Warn("failed to get new HEAD SHA after push", "pr", task.work.PRNumber, "error", err)
		} else {
			currentHeadSHA = newHeadSHA
		}
	}

	marker := ciMarker(task.headSHA, task.failures[0].Name)
	if pushed {
		a.logger.Info("CI failure is related, pushed a fix", "pr", task.work.PRNumber)
		if a.ShouldSkipComment("ci-related") {
			a.logger.Info("skipping CI fix-pushed comment (--skip-comment)", "pr", task.work.PRNumber)
			a.markCIChecked(task.work, task.headSHA, task.failures[0].Name)
		} else {
			if err := a.gh.AddIssueComment(ctx, a.cfg.Owner, a.cfg.Repo, task.work.PRNumber,
				fmt.Sprintf("CI check `%s` was failing on commit %s. Pushed a fix.\n\n%s", task.failures[0].Name, shortSHA(task.headSHA), marker)); err != nil {
				a.logger.Error("failed to post CI fix comment", "pr", task.work.PRNumber, "error", err)
			}
		}
	} else {
		a.logger.Warn("Claude said RELATED but no changes to push", "pr", task.work.PRNumber)
		if a.ShouldSkipComment("ci-related") {
			a.logger.Info("skipping CI fix-failed comment (--skip-comment)", "pr", task.work.PRNumber)
			a.markCIChecked(task.work, task.headSHA, task.failures[0].Name)
		} else {
			if err := a.gh.AddIssueComment(ctx, a.cfg.Owner, a.cfg.Repo, task.work.PRNumber,
				fmt.Sprintf("CI check `%s` is failing on commit %s. Investigated but could not push a fix.\n\n%s", task.failures[0].Name, shortSHA(task.headSHA), marker)); err != nil {
				a.logger.Error("failed to post CI investigation comment", "pr", task.work.PRNumber, "error", err)
			}
		}
	}
	task.work.CIFixAttempts++
	task.work.LastCIStatus = "failure"
	task.work.LastCheckedCISHA = currentHeadSHA
}
