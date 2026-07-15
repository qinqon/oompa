package agent

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
)

const maxCIFixAttempts = 3

// ciResult holds the result of investigating a single CI check failure.
// Results for the same PR+SHA are consolidated into a single comment.
type ciResult struct {
	check          string // check name
	category       string // "related", "unrelated", "infrastructure"
	explanation    string // agent's full explanation (raw text after classification keyword)
	errorSummary   string // one-line error summary (from ERROR_SUMMARY field)
	rootCause      string // root cause explanation (from ROOT_CAUSE field)
	evidence       string // relevant log lines (from EVIDENCE field)
	recommendation string // action recommendation (from RECOMMENDATION field)
	failingTest    string // specific test name (from FAILING_TEST field)
	flakyIssue     int    // linked flaky issue number (0 if none)
	pushed         bool   // whether a fix was pushed
}

// ProcessCIFailures checks CI status for open PRs and invokes the coding agent to fix failures.
func (a *Agent) ProcessCIFailures(ctx context.Context) {
	a.emit(Event{
		Type:     EventActionStarted,
		Category: CategoryCheck,
		Worker:   a.workerName(),
		State:    "working",
		Action:   "Checking CI status",
	})
	defer a.emit(Event{
		Type:     EventActionCompleted,
		Category: CategoryCheck,
		Worker:   a.workerName(),
		State:    "idle",
		Action:   "CI check complete",
	})
	// Scan phase: GitHub API calls, check run fetching, worktree setup
	var tasks []ciTask

	for _, work := range a.state.ActiveIssues {
		if work.PRNumber == 0 || work.Status != StatusPROpen {
			continue
		}

		// Skip CI processing if this PR has exceeded the per-session cost threshold.
		if a.sessionCostExceeded(work, "CI processing") {
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
			if slices.Contains(a.cfg.SkipChecks, r.Name) {
				a.logger.Debug("skipping excluded CI check", "pr", work.PRNumber, "check", r.Name)
				continue
			}
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

		// Get PR diff to help the agent determine if failure is related.
		// Degrade gracefully on error: the investigation proceeds with an
		// empty diff rather than being skipped.
		diffOut, diffStderr, diffErr := a.runner.Run(ctx, work.WorktreePath, "git", "diff", "--stat", a.originDefaultBranch())
		if diffErr != nil {
			a.logger.Warn("failed to get PR diff for CI context", "pr", work.PRNumber, "error", diffErr, "stderr", string(diffStderr))
		}
		diff := string(diffOut)

		// Get commits in the PR to help the agent identify which commit introduced the failure
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

	// Collect results grouped by PR+SHA for consolidated commenting.
	// Key format: "prNumber:headSHA"
	type resultGroup struct {
		work    *IssueWork
		headSHA string
		results []ciResult
	}
	groups := make(map[string]*resultGroup)
	// Track insertion order to preserve the task processing sequence.
	var groupOrder []string

	// Agent phase: investigate each collected failure and gather results
	runSequential(ctx, tasks, func(ctx context.Context, task ciTask) {
		// Re-check cost guard before each invocation — multiple tasks can be
		// enqueued for the same PR, and earlier invocations may have pushed
		// SessionCostUSD over the limit.
		if a.sessionCostExceeded(task.work, "CI investigation") {
			return
		}

		a.emit(Event{
			Type:      EventAgentInvocation,
			Category:  CategoryCI,
			Worker:    a.workerName(),
			State:     "working",
			Action:    fmt.Sprintf("Investigating CI failure: %s", task.failures[0].Name),
			PRNumbers: []int{task.work.PRNumber},
		})
		prompt := buildCIFixPrompt(*task.work, task.failures, task.diff, task.commits, a.cfg.SkipFix)
		result, err := a.codeAgent.Run(ctx, a.runner, task.work.WorktreePath, prompt, a.logger, true)
		// Track cumulative cost even on failure — failed invocations still consume
		// tokens and incur costs, so the MaxPRSessionCost guard must count them.
		task.work.SessionCostUSD += result.CostUSD
		if err != nil {
			a.logger.Error("agent failed to investigate CI", "pr", task.work.PRNumber, "error", err)
			a.emit(Event{
				Type:      EventError,
				Category:  CategoryError,
				Worker:    a.workerName(),
				State:     "error",
				Action:    "CI investigation failed",
				PRNumbers: []int{task.work.PRNumber},
				Error:     err.Error(),
			})
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

		var res ciResult
		switch foundKeyword {
		case "INFRASTRUCTURE":
			res = a.classifyCIInfrastructure(ctx, task, cleaned)
		case "UNRELATED":
			res = a.classifyCIUnrelated(ctx, task, cleaned)
		case "RELATED":
			res = a.classifyCIRelated(ctx, task, cleaned)
		}

		// Collect result into the group for this PR+SHA
		key := fmt.Sprintf("%d:%s", task.work.PRNumber, task.headSHA)
		if groups[key] == nil {
			groups[key] = &resultGroup{work: task.work, headSHA: task.headSHA}
			groupOrder = append(groupOrder, key)
		}
		groups[key].results = append(groups[key].results, res)
	})

	// Post consolidated comments for each PR+SHA group
	for _, key := range groupOrder {
		g := groups[key]
		a.postConsolidatedCIComment(ctx, g.work, g.headSHA, g.results)
	}
}

// classifyCIInfrastructure handles a CI failure classified as an infrastructure issue.
// Returns a ciResult for consolidation; does NOT post a comment.
func (a *Agent) classifyCIInfrastructure(_ context.Context, task ciTask, cleaned string) ciResult {
	a.logger.Info("CI failure is an infrastructure issue", "pr", task.work.PRNumber)
	a.emit(Event{
		Type:      EventAgentCompleted,
		Category:  CategoryCI,
		Worker:    a.workerName(),
		State:     "idle",
		Action:    fmt.Sprintf("CI infrastructure: %s", task.failures[0].Name),
		PRNumbers: []int{task.work.PRNumber},
	})
	explanation := extractKeywordExplanation(cleaned, "INFRASTRUCTURE")

	// Always mark as checked in memory to prevent re-investigation on next poll.
	// Comment markers serve as a secondary dedup mechanism but can be lost
	// (e.g. if comments are deleted), so in-memory state is the primary guard.
	a.markCIChecked(task.work, task.headSHA, task.failures[0].Name)

	task.work.LastCIStatus = "infrastructure-failure"
	task.work.LastCheckedCISHA = task.headSHA

	errorSummary, rootCause, evidence, recommendation, failingTest := parseCIStructuredFields(explanation)

	return ciResult{
		check:          task.failures[0].Name,
		category:       "infrastructure",
		explanation:    explanation,
		errorSummary:   errorSummary,
		rootCause:      rootCause,
		evidence:       evidence,
		recommendation: recommendation,
		failingTest:    failingTest,
	}
}

// classifyCIUnrelated handles a CI failure classified as unrelated to PR changes.
// Returns a ciResult for consolidation; does NOT post a comment.
// Flaky issue handling (search/create) is performed as a per-check side effect.
func (a *Agent) classifyCIUnrelated(ctx context.Context, task ciTask, cleaned string) ciResult {
	a.logger.Info("CI failure is unrelated to PR changes", "pr", task.work.PRNumber)
	a.emit(Event{
		Type:      EventAgentCompleted,
		Category:  CategoryCI,
		Worker:    a.workerName(),
		State:     "idle",
		Action:    fmt.Sprintf("CI unrelated: %s", task.failures[0].Name),
		PRNumbers: []int{task.work.PRNumber},
	})
	explanation := extractKeywordExplanation(cleaned, "UNRELATED")

	// Always mark as checked in memory to prevent re-investigation on next poll.
	// Comment markers serve as a secondary dedup mechanism but can be lost
	// (e.g. if comments are deleted), so in-memory state is the primary guard.
	a.markCIChecked(task.work, task.headSHA, task.failures[0].Name)

	task.work.LastCIStatus = "unrelated-failure"
	task.work.LastCheckedCISHA = task.headSHA

	errorSummary, rootCause, evidence, recommendation, failingTest := parseCIStructuredFields(explanation)

	res := ciResult{
		check:          task.failures[0].Name,
		category:       "unrelated",
		explanation:    explanation,
		errorSummary:   errorSummary,
		rootCause:      rootCause,
		evidence:       evidence,
		recommendation: recommendation,
		failingTest:    failingTest,
	}

	// Search for existing flaky issues and optionally create new ones.
	// When flaky-label is configured, always search for existing issues.
	// Only create new issues when create-flaky-issues is also enabled.
	// Flaky issue handling is a per-check side effect, not consolidated.
	if a.cfg.FlakyLabel != "" {
		res.flakyIssue = a.handleFlakyIssue(ctx, task, explanation)
	}

	return res
}

// handleFlakyIssue searches for existing flaky issues and optionally creates new ones.
// Always runs when FlakyLabel is configured (even without CreateFlakyIssues).
// When a match is found: adds CI lane link to the existing issue.
// When no match is found: creates a new issue only if CreateFlakyIssues is enabled.
// Returns the linked/created flaky issue number (0 if none).
// The PR reference comment is now part of the consolidated CI comment.
func (a *Agent) handleFlakyIssue(ctx context.Context, task ciTask, explanation string) int {
	// Skip if both the check run output and the agent's explanation are too short.
	trimmedOutput := strings.TrimSpace(task.failures[0].Output)
	if len(trimmedOutput) < 50 && len(explanation) < 50 {
		a.logger.Warn("skipping flaky issue handling: insufficient context",
			"pr", task.work.PRNumber,
			"check", task.failures[0].Name,
			"output_length", len(trimmedOutput),
			"explanation_length", len(explanation))
		return 0
	}

	issueNum, found := a.matchExistingFlakyIssue(ctx, task, explanation)
	if found {
		// Add CI lane link as a comment on the existing flaky issue
		// (respects skip-comment: flaky to suppress all flaky-related comments)
		if !a.ShouldSkipComment("flaky") {
			a.addCILaneLinkComment(ctx, task, issueNum)
		}
		return issueNum
	}

	// No existing match — create a new issue only if enabled
	if a.cfg.CreateFlakyIssues {
		return a.createNewFlakyIssue(ctx, task, explanation)
	}
	return 0
}

// matchExistingFlakyIssue searches for an existing open issue with the flaky label
// that matches the failing test. Returns the issue number and true if found.
func (a *Agent) matchExistingFlakyIssue(ctx context.Context, task ciTask, explanation string) (int, bool) {
	failingTest := parseFailingTest(explanation)
	var issueTitle string
	if failingTest != "" {
		issueTitle = fmt.Sprintf("Flaky CI: %s / %s", task.failures[0].Name, failingTest)
	} else {
		issueTitle = fmt.Sprintf("Flaky CI: %s", task.failures[0].Name)
	}

	searchQuery := fmt.Sprintf("repo:%s/%s is:issue is:open label:%q",
		a.cfg.Owner, a.cfg.Repo, a.cfg.FlakyLabel)
	existingIssues, err := a.gh.SearchIssues(ctx, searchQuery)
	if err != nil {
		a.logger.Warn("failed to search for existing flaky issues", "error", err)
		return 0, false
	}

	if len(existingIssues) == 0 {
		return 0, false
	}

	// Fast path: exact title match skips LLM matching entirely.
	for _, existing := range existingIssues {
		if existing.Title == issueTitle {
			a.logger.Info("exact title match for existing flaky CI issue", "issue", existing.Number, "check", task.failures[0].Name)
			return existing.Number, true
		}
	}

	// If no exact title match, ask the agent for root-cause matching.
	matchOutput := task.failures[0].Output
	if len(strings.TrimSpace(matchOutput)) < 50 {
		matchOutput = explanation
	}
	matchPrompt := buildFlakyMatchPrompt(task.failures[0].Name, matchOutput, existingIssues)
	matchResult, matchErr := a.codeAgent.Run(ctx, a.runner, task.work.WorktreePath, matchPrompt, a.logger, false)
	// Track cumulative cost even on failure — failed invocations still consume tokens.
	task.work.SessionCostUSD += matchResult.CostUSD
	if matchErr != nil {
		a.logger.Warn("failed to run agent for flaky issue matching", "error", matchErr)
		return 0, false
	}

	matchResponse := strings.TrimSpace(matchResult.Result)
	if matchedNum, ok := parseFlakyMatch(matchResponse); ok {
		for _, existing := range existingIssues {
			if existing.Number == matchedNum {
				a.logger.Info("agent matched existing flaky CI issue", "issue", matchedNum, "check", task.failures[0].Name)
				return matchedNum, true
			}
		}
		a.logger.Warn("agent returned MATCH for unknown issue", "matched_issue", matchedNum, "check", task.failures[0].Name)
	}

	return 0, false
}

// addCILaneLinkComment posts a comment on an existing flaky issue with the CI lane
// link and PR reference, building up evidence of repeated failures.
func (a *Agent) addCILaneLinkComment(ctx context.Context, task ciTask, issueNum int) {
	// Build CI lane link from the check run's HTML URL (preferred),
	// or extract from output text (for commit statuses like Prow).
	var ciLink string
	if task.failures[0].HTMLURL != "" {
		ciLink = task.failures[0].HTMLURL
	} else if url := extractURL(task.failures[0].Output); url != "" {
		ciLink = url
	}

	comment := fmt.Sprintf("CI failure on PR #%d (commit %s) matches this flaky test.\n\n"+
		"**CI lane:** `%s`",
		task.work.PRNumber, shortSHA(task.headSHA), task.failures[0].Name)
	if ciLink != "" {
		comment += fmt.Sprintf("\n**Link:** %s", ciLink)
	}
	comment += "\n\n" + a.botComment()

	if err := a.gh.AddIssueComment(ctx, a.cfg.Owner, a.cfg.Repo, issueNum, comment); err != nil {
		a.logger.Error("failed to post CI lane link comment on flaky issue", "issue", issueNum, "error", err)
	}
}

// createNewFlakyIssue creates a new flaky CI issue for an unrelated failure.
// Returns the created issue number (0 on failure).
// The PR reference comment is now part of the consolidated CI comment.
func (a *Agent) createNewFlakyIssue(ctx context.Context, task ciTask, explanation string) int {
	failingTest := parseFailingTest(explanation)
	var issueTitle string
	if failingTest != "" {
		issueTitle = fmt.Sprintf("Flaky CI: %s / %s", task.failures[0].Name, failingTest)
	} else {
		issueTitle = fmt.Sprintf("Flaky CI: %s", task.failures[0].Name)
	}

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
	issueNum, err := a.gh.CreateIssue(ctx, a.cfg.Owner, a.cfg.Repo, issueTitle, issueBody, []string{a.cfg.FlakyLabel})
	if err != nil {
		a.logger.Error("failed to create flaky CI issue", "error", err)
		return 0
	}
	a.logger.Info("created flaky CI issue", "issue", issueNum)
	return issueNum
}

// extractURL extracts the first URL from a string.
func extractURL(s string) string {
	for word := range strings.FieldsSeq(s) {
		if strings.HasPrefix(word, "https://") || strings.HasPrefix(word, "http://") {
			return strings.TrimRight(word, ".,;)]}")
		}
	}
	return ""
}

// parseCIStructuredFields extracts structured fields from the agent's CI analysis output.
// It looks for lines starting with known field prefixes (ERROR_SUMMARY:, ROOT_CAUSE:, etc.)
// and extracts the value. For EVIDENCE:, it captures all lines until the next field.
// Fields not found in the output are left as empty strings.
func parseCIStructuredFields(text string) (errorSummary, rootCause, evidence, recommendation, failingTest string) {
	lines := strings.Split(text, "\n")
	type fieldState int
	const (
		stateNone fieldState = iota
		stateEvidence
	)
	state := stateNone
	var evidenceLines []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// When capturing evidence, only treat a line as a field boundary
		// if the untrimmed line starts with the prefix at column 0.
		// This prevents indented log lines like "  RECOMMENDATION: ..."
		// from prematurely terminating evidence capture.
		isFieldBoundary := func(prefix string) bool {
			if state == stateEvidence {
				return strings.HasPrefix(line, prefix)
			}
			return strings.HasPrefix(trimmed, prefix)
		}

		// Check if this line starts a known field — terminates EVIDENCE capture
		switch {
		case isFieldBoundary("ERROR_SUMMARY:"):
			state = stateNone
			errorSummary = strings.TrimSpace(strings.TrimPrefix(trimmed, "ERROR_SUMMARY:"))
		case isFieldBoundary("ROOT_CAUSE:"):
			state = stateNone
			rootCause = strings.TrimSpace(strings.TrimPrefix(trimmed, "ROOT_CAUSE:"))
		case isFieldBoundary("EVIDENCE:"):
			state = stateEvidence
			// Check if there's content on the same line
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "EVIDENCE:"))
			if rest != "" {
				evidenceLines = append(evidenceLines, rest)
			}
		case isFieldBoundary("RECOMMENDATION:"):
			state = stateNone
			recommendation = strings.TrimSpace(strings.TrimPrefix(trimmed, "RECOMMENDATION:"))
		case isFieldBoundary("FAILING_TEST:"):
			state = stateNone
			failingTest = strings.TrimSpace(strings.TrimPrefix(trimmed, "FAILING_TEST:"))
		case isFieldBoundary("CLASSIFICATION:"):
			state = stateNone
			// Skip — classification is handled by the keyword scanner
		default:
			if state == stateEvidence {
				evidenceLines = append(evidenceLines, line)
			}
		}
	}

	if len(evidenceLines) > 0 {
		evidence = strings.TrimSpace(strings.Join(evidenceLines, "\n"))
	}

	return errorSummary, rootCause, evidence, recommendation, failingTest
}

// extractKeywordExplanation returns the text following the classification
// keyword in the agent's response, stripped of separator punctuation the
// models like to insert after the keyword (colons, dashes, markdown).
func extractKeywordExplanation(cleaned, keyword string) string {
	idx := strings.Index(cleaned, keyword)
	if idx < 0 {
		return ""
	}
	return strings.TrimSpace(strings.TrimLeft(strings.TrimPrefix(cleaned[idx:], keyword), " :—–-"))
}

// classifyCIRelated handles a CI failure classified as related to PR changes.
// Returns a ciResult for consolidation; does NOT post a comment.
func (a *Agent) classifyCIRelated(ctx context.Context, task ciTask, cleaned string) ciResult {
	a.emit(Event{
		Type:      EventAgentCompleted,
		Category:  CategoryCI,
		Worker:    a.workerName(),
		State:     "working",
		Action:    fmt.Sprintf("CI related: %s", task.failures[0].Name),
		PRNumbers: []int{task.work.PRNumber},
	})

	explanation := extractKeywordExplanation(cleaned, "RELATED")

	errorSummary, rootCause, evidence, recommendation, failingTest := parseCIStructuredFields(explanation)

	if a.cfg.SkipFix {
		// Skip-fix mode: just record the analysis, don't try to fix or push
		a.logger.Info("CI failure is related (skip-fix mode, not pushing)", "pr", task.work.PRNumber)
		// Always mark as checked in memory to prevent re-investigation on next poll.
		a.markCIChecked(task.work, task.headSHA, task.failures[0].Name)
		task.work.LastCIStatus = "related-skip-fix"
		task.work.LastCheckedCISHA = task.headSHA
		return ciResult{
			check:          task.failures[0].Name,
			category:       "related",
			explanation:    explanation,
			errorSummary:   errorSummary,
			rootCause:      rootCause,
			evidence:       evidence,
			recommendation: recommendation,
			failingTest:    failingTest,
		}
	}

	// Push fixups or uncommitted changes; otherwise the agent may have
	// amended the commit directly — push when HEAD differs from the remote.
	pushed, handled := a.pushFixupsOrAmend(ctx, task.work.WorktreePath, task.work.PRNumber)
	if !handled {
		localSHA, _, err := a.runner.Run(ctx, task.work.WorktreePath, "git", "rev-parse", "HEAD")
		if err == nil && strings.TrimSpace(string(localSHA)) != task.headSHA {
			a.logger.Info("agent amended commit directly, pushing", "pr", task.work.PRNumber)
			if err := a.gitPush(ctx, task.work.WorktreePath, true); err != nil {
				a.logger.Error("failed to push", "pr", task.work.PRNumber, "error", err)
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

	if pushed {
		a.logger.Info("CI failure is related, pushed a fix", "pr", task.work.PRNumber)
	} else {
		a.logger.Warn("agent said RELATED but no changes to push", "pr", task.work.PRNumber)
	}

	// Always mark as checked in memory to prevent re-investigation on next poll.
	// Comment markers serve as a secondary dedup mechanism but can be lost
	// (e.g. if comments are deleted), so in-memory state is the primary guard.
	a.markCIChecked(task.work, task.headSHA, task.failures[0].Name)

	task.work.CIFixAttempts++
	task.work.LastCIStatus = "failure"
	task.work.LastCheckedCISHA = currentHeadSHA

	return ciResult{
		check:          task.failures[0].Name,
		category:       "related",
		explanation:    explanation,
		errorSummary:   errorSummary,
		rootCause:      rootCause,
		evidence:       evidence,
		recommendation: recommendation,
		failingTest:    failingTest,
		pushed:         pushed,
	}
}

// postConsolidatedCIComment builds and posts a single comment for all CI results
// on a given PR+SHA. Uses structured format with collapsible details sections.
// Infrastructure failures with the same error summary are grouped into a single section.
func (a *Agent) postConsolidatedCIComment(ctx context.Context, work *IssueWork, headSHA string, results []ciResult) {
	if len(results) == 0 {
		return
	}

	// Group results by category
	var infrastructure, unrelated, related []ciResult
	for _, r := range results {
		switch r.category {
		case "infrastructure":
			infrastructure = append(infrastructure, r)
		case "unrelated":
			unrelated = append(unrelated, r)
		case "related":
			related = append(related, r)
		}
	}

	// Check if we have any visible content (non-skipped sections)
	hasVisibleContent := (len(infrastructure) > 0 && !a.ShouldSkipComment("ci-infrastructure")) ||
		(len(unrelated) > 0 && !a.ShouldSkipComment("ci-unrelated")) ||
		(len(related) > 0 && !a.ShouldSkipComment("ci-related"))

	if !hasVisibleContent {
		return
	}

	var comment strings.Builder

	// Summary header
	comment.WriteString(formatCISummaryHeader(headSHA, infrastructure, unrelated, related))

	// Related section — individual <details> per check
	if len(related) > 0 && !a.ShouldSkipComment("ci-related") {
		for _, r := range related {
			comment.WriteString(formatCIRelatedDetails(r))
		}

		// Note any fixes that were pushed
		pushedChecks := 0
		for _, r := range related {
			if r.pushed {
				pushedChecks++
			}
		}
		if pushedChecks > 0 {
			comment.WriteString("\nPushed a fix for the related failure")
			if pushedChecks > 1 {
				comment.WriteString("s")
			}
			comment.WriteString(".\n")
		}
	}

	// Unrelated section — individual <details> per check
	if len(unrelated) > 0 && !a.ShouldSkipComment("ci-unrelated") {
		for _, r := range unrelated {
			comment.WriteString(formatCIUnrelatedDetails(r, a))
		}
	}

	// Infrastructure section — grouped when they share the same error summary
	if len(infrastructure) > 0 && !a.ShouldSkipComment("ci-infrastructure") {
		comment.WriteString(formatCIInfrastructureSection(infrastructure))
	}

	// Visible attribution footer
	comment.WriteString("\n")
	comment.WriteString(a.botComment())
	comment.WriteString("\n")

	// Append per-check dedup markers so alreadyCheckedCI still works
	comment.WriteString("\n")
	for _, r := range results {
		comment.WriteString(ciMarker(headSHA, r.check) + "\n")
	}

	if err := a.gh.AddIssueComment(ctx, a.cfg.Owner, a.cfg.Repo, work.PRNumber, comment.String()); err != nil {
		a.logger.Error("failed to post consolidated CI comment", "pr", work.PRNumber, "error", err)
	}
}

// formatCISummaryHeader builds the summary section at the top of the consolidated CI comment.
func formatCISummaryHeader(headSHA string, infrastructure, unrelated, related []ciResult) string {
	var header strings.Builder
	fmt.Fprintf(&header, "## CI Failure Analysis — commit %s\n\n", shortSHA(headSHA))

	// Build category breakdown
	var categories []string
	if len(infrastructure) > 0 {
		categories = append(categories, fmt.Sprintf("%d infrastructure", len(infrastructure)))
	}
	if len(unrelated) > 0 {
		categories = append(categories, fmt.Sprintf("%d unrelated", len(unrelated)))
	}
	if len(related) > 0 {
		categories = append(categories, fmt.Sprintf("%d related", len(related)))
	}

	total := len(infrastructure) + len(unrelated) + len(related)
	fmt.Fprintf(&header, "**Total failures**: %d", total)
	if len(categories) > 0 {
		header.WriteString(" (")
		header.WriteString(strings.Join(categories, ", "))
		header.WriteString(")")
	}
	header.WriteString("\n")

	// Note action taken
	pushedFixes := 0
	for _, r := range related {
		if r.pushed {
			pushedFixes++
		}
	}
	if pushedFixes > 0 {
		header.WriteString("**Action taken**: Pushed fix for related failure")
		if pushedFixes > 1 {
			header.WriteString("s")
		}
		header.WriteString("\n")
	}

	header.WriteString("\n---\n")
	return header.String()
}

// formatCIRelatedDetails builds a collapsible <details> block for a related CI failure.
func formatCIRelatedDetails(r ciResult) string {
	var d strings.Builder
	summary := resultSummaryLine(r)
	action := "fix needed"
	if r.pushed {
		action = "fix pushed"
	}
	fmt.Fprintf(&d, "\n<details>\n<summary>🔴 Related: <code>%s</code> — %s</summary>\n", escapeHTML(r.check), action)
	writeStructuredBody(&d, summary, r)
	if r.pushed {
		d.WriteString("\n### Action\nPushed fix for this failure.\n")
	}
	d.WriteString("\n</details>\n")
	return d.String()
}

// formatCIUnrelatedDetails builds a collapsible <details> block for an unrelated CI failure.
func formatCIUnrelatedDetails(r ciResult, a *Agent) string {
	var d strings.Builder
	summary := resultSummaryLine(r)
	label := "flaky test"
	if r.flakyIssue != 0 {
		label = fmt.Sprintf("flaky test (#%d)", r.flakyIssue)
	}
	fmt.Fprintf(&d, "\n<details>\n<summary>⚠️ Unrelated: <code>%s</code> — %s</summary>\n", escapeHTML(r.check), label)
	writeStructuredBody(&d, summary, r)
	if r.flakyIssue != 0 && !a.ShouldSkipComment("flaky") {
		fmt.Fprintf(&d, "\n### Known Issue\nTracked in #%d\n", r.flakyIssue)
	}
	d.WriteString("\n</details>\n")
	return d.String()
}

// formatCIInfrastructureSection builds the infrastructure section, grouping failures
// that share the same error summary into a single <details> block with a table.
func formatCIInfrastructureSection(infra []ciResult) string {
	var section strings.Builder

	// Group by error summary (or first sentence of explanation as fallback)
	type infraGroup struct {
		summary string
		results []ciResult
	}
	var groups []infraGroup
	groupIdx := make(map[string]int) // summary -> index in groups

	for _, r := range infra {
		key := r.errorSummary
		if key == "" {
			key = firstSentence(r.explanation)
		}
		if key == "" {
			key = "unknown error"
		}
		if idx, ok := groupIdx[key]; ok {
			groups[idx].results = append(groups[idx].results, r)
		} else {
			groupIdx[key] = len(groups)
			groups = append(groups, infraGroup{summary: key, results: []ciResult{r}})
		}
	}

	for _, g := range groups {
		if len(g.results) > 1 {
			// Grouped: multiple checks with the same error
			fmt.Fprintf(&section, "\n<details>\n<summary>🔧 Infrastructure (%d): %s</summary>\n",
				len(g.results), escapeHTML(g.summary))
			section.WriteString("\n| Check | Error |\n|-------|-------|\n")
			for _, r := range g.results {
				errMsg := r.errorSummary
				if errMsg == "" {
					errMsg = firstSentence(r.explanation)
				}
				fmt.Fprintf(&section, "| `%s` | %s |\n", escapeTableCell(r.check), escapeTableCell(errMsg))
			}
			// Use root cause from the first result (they share the same cause)
			if g.results[0].rootCause != "" {
				fmt.Fprintf(&section, "\n### Root Cause\n%s\n", g.results[0].rootCause)
			}
			if g.results[0].recommendation != "" {
				fmt.Fprintf(&section, "\n### Recommendation\n%s\n", g.results[0].recommendation)
			}
			section.WriteString("\n</details>\n")
		} else {
			// Single infrastructure failure — individual <details>
			r := g.results[0]
			summary := resultSummaryLine(r)
			fmt.Fprintf(&section, "\n<details>\n<summary>🔧 Infrastructure: <code>%s</code> — %s</summary>\n",
				escapeHTML(r.check), escapeHTML(summary))
			writeStructuredBody(&section, summary, r)
			section.WriteString("\n</details>\n")
		}
	}

	return section.String()
}

// resultSummaryLine returns a one-line summary for a ciResult.
// Prefers errorSummary, falls back to firstSentence of explanation.
func resultSummaryLine(r ciResult) string {
	if r.errorSummary != "" {
		return r.errorSummary
	}
	return firstSentence(r.explanation)
}

// writeStructuredBody writes the Error, Root Cause, and Recommendation sections
// into a <details> body, using structured fields when available.
func writeStructuredBody(w *strings.Builder, summary string, r ciResult) {
	// Error section with evidence
	if r.evidence != "" {
		writeFenced(w, "### Error", r.evidence)
	} else if summary != "" {
		writeFenced(w, "### Error", summary)
	}

	// Root cause
	if r.rootCause != "" {
		fmt.Fprintf(w, "\n### Root Cause\n%s\n", r.rootCause)
	}

	// Recommendation
	if r.recommendation != "" {
		fmt.Fprintf(w, "\n### Recommendation\n%s\n", r.recommendation)
	}
}

// writeFenced writes a markdown heading followed by a fenced code block.
// The fence length adapts to the content: if the body contains backtick runs,
// the fence is made longer than the longest run to prevent breakout.
func writeFenced(w *strings.Builder, heading, body string) {
	fence := "```"
	// Find the longest consecutive run of backticks in the body
	maxRun := 0
	currentRun := 0
	for _, ch := range body {
		if ch == '`' {
			currentRun++
			if currentRun > maxRun {
				maxRun = currentRun
			}
		} else {
			currentRun = 0
		}
	}
	// Use a fence longer than the longest backtick run (minimum 3)
	if maxRun >= 3 {
		fence = strings.Repeat("`", maxRun+1)
	}
	fmt.Fprintf(w, "\n%s\n%s\n%s\n%s\n", heading, fence, body, fence)
}

// escapeHTML escapes characters that break HTML rendering in markdown.
func escapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// escapeTableCell escapes characters that break markdown table rendering.
func escapeTableCell(s string) string {
	s = strings.ReplaceAll(s, "|", `\|`)
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", "")
	return s
}

// firstSentence returns text up to the first period-followed-by-space, newline, or 120 chars.
func firstSentence(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	// Take up to first newline
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		s = s[:idx]
	}
	// Take up to first period followed by space or end.
	// If the string already ends with a period, keep it as-is.
	if idx := strings.Index(s, ". "); idx >= 0 {
		s = s[:idx+1]
	}
	// Truncate if too long
	if len(s) > 120 {
		s = s[:120] + "..."
	}
	return s
}
