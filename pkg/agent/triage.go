package agent

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
)

// triageLookbackRunLimit is the maximum number of runs to fetch when scanning a lookback window.
const triageLookbackRunLimit = 50

// triageRunTask pairs a CI source with a run for parallel investigation.
type triageRunTask struct {
	ciSource CIJobSource
	run      JobRun
}

// ProcessTriageJobs monitors periodic CI jobs for failures and investigates them.
func (a *Agent) ProcessTriageJobs(ctx context.Context) {
	if len(a.cfg.TriageJobs) == 0 && a.cfg.TriageWorkflow == "" {
		return
	}

	var tasks []triageRunTask

	// Build the list of CI sources to check
	var ciSources []CIJobSource

	// URL-based sources (existing: Prow/GCS/cross-repo GHA)
	for _, jobURL := range a.cfg.TriageJobs {
		a.logger.Debug("processing triage job", "url", jobURL)

		ciSource, err := ParseCIJobURL(jobURL, a.gh)
		if err != nil {
			a.logger.Error("failed to parse CI job URL", "url", jobURL, "error", err)
			continue
		}
		if ghaSource, ok := ciSource.(*GitHubActionsJobSource); ok {
			ghaSource.lookback = a.cfg.TriageLookback
		}
		ciSources = append(ciSources, ciSource)
	}

	// Workflow + lanes source (new: lane-level GHA triage)
	if a.cfg.TriageWorkflow != "" && len(a.cfg.TriageLanePatterns) > 0 {
		ciSources = append(ciSources, &GitHubActionsJobSource{
			owner:        a.cfg.Owner,
			repo:         a.cfg.Repo,
			workflow:     a.cfg.TriageWorkflow,
			jobName:      fmt.Sprintf("%s/%s/%s", a.cfg.Owner, a.cfg.Repo, a.cfg.TriageWorkflow),
			gh:           a.gh,
			lanePatterns: a.cfg.TriageLanePatterns,
			matchedJobs:  make(map[string]int64),
			lookback:     a.cfg.TriageLookback,
		})
	}

	for _, ciSource := range ciSources {
		// Fetch more runs when scanning a time window
		limit := 5
		if a.cfg.TriageLookback > 0 {
			limit = triageLookbackRunLimit
		}

		runs, err := ciSource.ListRecentRuns(ctx, limit)
		if err != nil {
			a.logger.Error("failed to list recent runs", "job", ciSource.JobName(), "error", err)
			continue
		}

		if len(runs) == 0 {
			a.logger.Debug("no recent runs found", "job", ciSource.JobName())
			continue
		}

		// Determine which runs to process
		var cutoff time.Time
		if a.cfg.TriageLookback > 0 {
			cutoff = time.Now().Add(-a.cfg.TriageLookback)
		}

		runsToProcess := runs
		if cutoff.IsZero() {
			// Default: only the most recent run
			runsToProcess = runs[:1]
		}

		for _, run := range runsToProcess {
			// Stop once we hit runs older than the lookback window (runs are sorted descending)
			if !cutoff.IsZero() && run.Timestamp.Before(cutoff) {
				break
			}

			// Skip if already investigated
			if a.state.IsRunInvestigated(ciSource.JobName(), run.ID) {
				a.logger.Debug("run already investigated", "job", ciSource.JobName(), "runID", run.ID)
				continue
			}

			// Skip if the run passed (mark as investigated)
			if run.Status == "success" {
				a.logger.Info("run passed, skipping", "job", ciSource.JobName(), "runID", run.ID)
				a.state.MarkRunInvestigated(ciSource.JobName(), run.ID)
				continue
			}

			a.logger.Info("investigating failed run", "job", ciSource.JobName(), "runID", run.ID, "status", run.Status)
			tasks = append(tasks, triageRunTask{ciSource: ciSource, run: run})
		}
	}

	// Track issues created during this triage cycle so that subsequent
	// investigations can find them without relying on GitHub search
	// indexing (which is eventually consistent). This prevents duplicate
	// issues when multiple runs of the same job or different jobs fail
	// for the same root cause within the same triage cycle.
	var cycleIssues []Issue

	// Collect the unique names of all jobs failing in this triage cycle.
	// This metadata is passed to the LLM matcher so it can recognize
	// correlated failures across different jobs (e.g. many jobs failing
	// in the same cycle suggests a shared root cause like an
	// infrastructure outage or a bad PR).
	// Deduplicate: multiple runs of the same job should not inflate the
	// concurrent failure count.
	// Use run.JobName (lane-specific name) rather than ciSource.JobName()
	// (workflow-level name) so that lane-level triage correctly counts
	// each failing lane as a separate job. When a single CIJobSource
	// produces multiple lanes (e.g. matrix workflows), ciSource.JobName()
	// returns the same value for all of them, causing the deterministic
	// fallback condition (len(cycleFailedJobs) > 1) to never trigger.
	var cycleFailedJobs []string
	for _, task := range tasks {
		jobName := task.run.JobName
		if !slices.Contains(cycleFailedJobs, jobName) {
			cycleFailedJobs = append(cycleFailedJobs, jobName)
		}
	}

	// Investigate collected failed runs sequentially
	runSequential(ctx, tasks, func(ctx context.Context, task triageRunTask) {
		a.investigateTriageRun(ctx, task.ciSource, task.run, &cycleIssues, cycleFailedJobs)
	})

	// Flush Slack findings collected during this triage cycle.
	// Triage runs on its own schedule goroutine, separate from the poll loop
	// that normally calls Flush(). Without this, triage findings would stay
	// buffered until the next poll-loop flush (which may never run for
	// triage-only configurations).
	a.FlushSlackReport(ctx)
}

// investigateTriageRun handles the investigation of a single failed CI run.
// cycleIssues accumulates issues created during the current triage cycle so
// that subsequent investigations can match against them without relying on
// GitHub's eventually-consistent search index.
// cycleFailedJobs lists all job names that failed in the same triage cycle,
// providing the LLM matcher with cross-job correlation context.
func (a *Agent) investigateTriageRun(ctx context.Context, ciSource CIJobSource, run JobRun, cycleIssues *[]Issue, cycleFailedJobs []string) {
	// Fetch build log
	buildLog, err := ciSource.FetchLog(ctx, run.ID)
	if err != nil {
		a.logger.Error("failed to fetch build log", "job", ciSource.JobName(), "runID", run.ID, "error", err)
		return
	}

	// Truncate log if too large (keep last 50KB to focus on recent failures)
	const maxLogSize = 50000
	if len(buildLog) > maxLogSize {
		buildLog = "...\n[Log truncated, showing last 50KB]\n...\n\n" + buildLog[len(buildLog)-maxLogSize:]
	}

	// Create a worktree on the default branch for read-only codebase access.
	// Sanitize the run ID for use as a git branch name: lane-level triage
	// produces IDs like "runID:jobID" and colons are invalid in git refs.
	branchName := fmt.Sprintf("triage/%s", strings.ReplaceAll(run.ID, ":", "-"))

	// Ensure repo is cloned
	if err := a.worktrees.EnsureRepoCloned(ctx); err != nil {
		a.logger.Error("failed to ensure repo cloned", "error", err)
		return
	}

	// Create worktree on default branch
	worktreePath, err := a.worktrees.CreateWorktree(ctx, branchName)
	if err != nil {
		a.logger.Error("failed to create triage worktree", "error", err)
		return
	}

	// Checkout the default branch (CreateWorktree creates a new branch, we want default)
	a.runner.Run(ctx, worktreePath, "git", "checkout", a.defaultBranch()) //nolint:errcheck // best-effort

	// Build the triage prompt
	prompt := buildPeriodicCITriagePrompt(ciSource.JobName(), run.ID, buildLog, a.cfg.Owner, a.cfg.Repo)

	// Run agent in the worktree
	a.logger.Info("running agent for CI triage", "job", ciSource.JobName(), "runID", run.ID)
	result, err := a.codeAgent.Run(ctx, a.runner, worktreePath, prompt, a.logger, false)
	if err != nil {
		a.logger.Error("agent failed during CI triage", "job", ciSource.JobName(), "runID", run.ID, "error", err)
		_ = a.worktrees.RemoveWorktree(ctx, worktreePath)
		return
	}

	analysis := result.Result
	a.logger.Info("CI triage analysis complete", "job", ciSource.JobName(), "runID", run.ID)
	a.logger.Debug("analysis output", "output", analysis)

	// If --create-flaky-issues is set, create a GitHub issue with the analysis
	if a.cfg.CreateFlakyIssues {
		a.logger.Info("creating issue for CI failure", "job", ciSource.JobName(), "runID", run.ID)

		// Extract a short failure signature from the analysis for content-aware dedup
		failureSig := extractFailureSignature(analysis)

		// Build a content-aware title that includes the failure signature
		var title string
		if failureSig != "" {
			title = fmt.Sprintf("CI Failure: %s / %s", ciSource.JobName(), failureSig)
		} else {
			title = fmt.Sprintf("CI Failure: %s", ciSource.JobName())
		}

		// Search for existing triage issues to avoid duplicates.
		// Search broadly (not scoped to this job's name) so that issues
		// created for other jobs in the same triage cycle can be found
		// when the root cause is shared (e.g. all jobs failing on the
		// same PR). Constrained by the flaky label and a 30-day window
		// to keep result sets small and limit LLM token costs.
		// NOTE: No title filter — issues may be titled "CI Failure:" (triage)
		// or "Flaky CI:" (PR-based CI), and both need to be found for cross-
		// system dedup.
		cutoffDate := time.Now().AddDate(0, 0, -30).Format("2006-01-02")
		searchQuery := fmt.Sprintf("repo:%s/%s is:issue is:open label:%q created:>%s",
			a.cfg.Owner, a.cfg.Repo, a.cfg.FlakyLabel, cutoffDate)
		existingIssues, err := a.gh.SearchIssues(ctx, searchQuery)
		if err != nil {
			a.logger.Warn("failed to search for existing issues", "error", err)
		}

		// Merge in issues created during this triage cycle that may not
		// yet be indexed by GitHub's search API. Deduplicate by number
		// to avoid presenting the same issue twice to the LLM matcher.
		existingIssues = mergeIssues(existingIssues, *cycleIssues)

		matchedIssue := a.matchExistingTriageIssue(ctx, ciSource.JobName(), title, analysis, existingIssues, worktreePath, *cycleIssues, cycleFailedJobs)

		if matchedIssue > 0 {
			a.logger.Info("found matching issue for this failure, adding run link", "job", ciSource.JobName(), "issue", matchedIssue)
			a.addTriageRunLinkComment(ctx, ciSource.JobName(), run, matchedIssue)
		} else {
			// Create a new issue with the failure signature in the title
			body := fmt.Sprintf("Periodic CI job **%s** failed in run [%s](%s).\n\n"+
			"## Analysis\n\n"+
			"%s\n\n"+
			"%s\n<!-- oompa-triage -->", ciSource.JobName(), run.ID, run.LogURL, analysis, a.botComment())

			issueNumber, err := a.gh.CreateIssue(ctx, a.cfg.Owner, a.cfg.Repo, title, body, []string{a.cfg.FlakyLabel})
			if err != nil {
				a.logger.Error("failed to create issue", "error", err)
			} else {
				a.logger.Info("created issue for CI failure", "job", ciSource.JobName(), "issue", issueNumber)
				// Track newly created issue for same-cycle dedup
				*cycleIssues = append(*cycleIssues, Issue{
					Number: issueNumber,
					Title:  title,
					Body:   body,
					Labels: []string{a.cfg.FlakyLabel},
				})
			}
		}
	}

	// Emit Slack finding for triage results
	if a.SlackEnabled() {
		failureSig := extractFailureSignature(analysis)

		// Vary wording: "lane" for matrix job sources with lane patterns,
		// "triage job" for standalone CI jobs (including workflow-level GHA)
		label := "triage job"
		if gha, ok := ciSource.(*GitHubActionsJobSource); ok && len(gha.lanePatterns) > 0 {
			label = "lane"
		}

		var msg string
		if failureSig != "" {
			msg = fmt.Sprintf(":red_circle: %s *%s* failed in run <%s|%s>: %s", label, run.JobName, run.LogURL, run.ID, failureSig)
		} else {
			msg = fmt.Sprintf(":red_circle: %s *%s* failed in run <%s|%s>", label, run.JobName, run.LogURL, run.ID)
		}
		a.CollectSlackFindings([]SlackFinding{{
			Owner:    a.cfg.Owner,
			Repo:     a.cfg.Repo,
			Category: "triage",
			Message:  msg,
			DedupKey: fmt.Sprintf("triage:%s:%s", run.JobName, run.ID),
		}})
	}

	// Mark the run as investigated
	a.state.MarkRunInvestigated(ciSource.JobName(), run.ID)

	// Clean up the triage worktree
	if err := a.worktrees.RemoveWorktree(ctx, worktreePath); err != nil {
		a.logger.Warn("failed to remove triage worktree", "path", worktreePath, "error", err)
	}
}

// matchExistingTriageIssue searches for an existing open issue that matches the
// current failure by content, not just job name. Returns the issue number if found, 0 otherwise.
//
// Strategy (mirrors matchExistingFlakyIssue in ci.go):
//  1. Fast path: exact title match skips LLM matching entirely.
//  2. LLM-based root-cause matching when no exact title match exists.
//  3. Deterministic fallback: when multiple jobs fail concurrently and an issue
//     was already created in this triage cycle, match to the most recent cycle
//     issue. This prevents the LLM's inconsistent NONE responses from creating
//     duplicate issues for correlated failures.
//
// cycleIssues provides issues created earlier in the same triage cycle.
// cycleFailedJobs provides the names of all jobs that failed in the same triage
// cycle, giving the LLM matcher context about correlated failures.
func (a *Agent) matchExistingTriageIssue(ctx context.Context, jobName, title, analysis string, existingIssues []Issue, worktreePath string, cycleIssues []Issue, cycleFailedJobs []string) int {
	// Deterministic fallback (checked first): when multiple jobs fail
	// concurrently and an issue was already created in this triage cycle,
	// match to it. In a batch of concurrent failures, the first investigation
	// creates an issue and all subsequent ones should group under it.
	// The LLM is unreliable here because each job's error output looks
	// superficially different even when they share an underlying cause
	// (infrastructure outage, bad merge, etc.).
	//
	// Defense-in-depth: the caller (investigateTriageRun) merges cycleIssues
	// into existingIssues before calling this function, so in normal
	// production flow existingIssues will be non-empty when cycleIssues is.
	// This early check guards against callers that don't merge, and also
	// makes the function's contract self-contained.
	useDeterministicFallback := len(cycleFailedJobs) > 1 && len(cycleIssues) > 0

	if len(existingIssues) == 0 {
		if useDeterministicFallback {
			target := cycleIssues[len(cycleIssues)-1]
			a.logger.Info("deterministic same-cycle dedup: matching to cycle issue (no existing issues)",
				"issue", target.Number, "job", jobName, "concurrent_jobs", len(cycleFailedJobs))
			return target.Number
		}
		return 0
	}

	// Fast path: exact title match
	for _, existing := range existingIssues {
		if existing.Title == title {
			a.logger.Info("exact title match for existing triage issue", "issue", existing.Number, "job", jobName)
			return existing.Number
		}
	}

	// Slow path: LLM-based root-cause matching
	matchPrompt := buildTriageMatchPrompt(jobName, analysis, existingIssues, cycleFailedJobs)
	matchResult, matchErr := a.codeAgent.Run(ctx, a.runner, worktreePath, matchPrompt, a.logger, false)
	if matchErr != nil {
		a.logger.Warn("failed to run agent for triage issue matching", "error", matchErr)
		// Fall through to deterministic fallback below
	} else {
		matchResponse := strings.TrimSpace(matchResult.Result)
		if matchedNum, ok := parseFlakyMatch(matchResponse); ok {
			for _, existing := range existingIssues {
				if existing.Number == matchedNum {
					a.logger.Info("agent matched existing triage issue", "issue", matchedNum, "job", jobName)
					return matchedNum
				}
			}
			a.logger.Warn("agent returned MATCH for unknown issue", "matched_issue", matchedNum, "job", jobName)
		}
	}

	// Deterministic fallback after LLM says NONE or errors.
	if useDeterministicFallback {
		target := cycleIssues[len(cycleIssues)-1] // most recent cycle issue
		a.logger.Info("deterministic same-cycle dedup: matching to cycle issue",
			"issue", target.Number, "job", jobName, "concurrent_jobs", len(cycleFailedJobs))
		return target.Number
	}

	return 0
}

// addTriageRunLinkComment posts a comment on an existing triage issue with the
// new run's details, building up evidence of repeated failures.
func (a *Agent) addTriageRunLinkComment(ctx context.Context, jobName string, run JobRun, issueNum int) {
	comment := fmt.Sprintf("Same failure observed in periodic job **%s**, run [%s](%s).\n\n%s",
		jobName, run.ID, run.LogURL, a.botComment())

	if err := a.gh.AddIssueComment(ctx, a.cfg.Owner, a.cfg.Repo, issueNum, comment); err != nil {
		a.logger.Error("failed to post run link comment on triage issue", "issue", issueNum, "error", err)
	}
}

// mergeIssues combines two issue slices, deduplicating by issue number.
// Issues from the primary slice take precedence over extras.
func mergeIssues(primary, extras []Issue) []Issue {
	if len(extras) == 0 {
		return primary
	}
	seen := make(map[int]bool, len(primary))
	for _, issue := range primary {
		seen[issue.Number] = true
	}
	merged := make([]Issue, 0, len(primary)+len(extras))
	merged = append(merged, primary...)
	for _, issue := range extras {
		if !seen[issue.Number] {
			merged = append(merged, issue)
			seen[issue.Number] = true
		}
	}
	return merged
}

// extractFailureSignature extracts a short failure identifier from the agent's
// analysis output. It looks for common structured patterns in the analysis:
//   - "## Summary" section content
//   - "## Classification" section content
//
// Returns a short (< 60 char) signature for inclusion in the issue title.
// Returns empty string if no signature can be extracted.
func extractFailureSignature(analysis string) string {
	// Try to extract from "## Summary" section
	sig := extractSection(analysis, "## Summary")
	if sig != "" {
		return truncateSignature(sig)
	}

	// Fall back to first non-empty line
	for line := range strings.SplitSeq(analysis, "\n") {
		trimmed := strings.TrimSpace(line)
		// Skip markdown headings and empty lines
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "---") {
			continue
		}
		return truncateSignature(trimmed)
	}

	return ""
}

// extractSection extracts the first paragraph of content under a markdown heading.
func extractSection(text, heading string) string {
	_, after, ok := strings.Cut(text, heading)
	if !ok {
		return ""
	}
	// Skip the heading line itself
	rest := after
	if newline := strings.IndexByte(rest, '\n'); newline >= 0 {
		rest = rest[newline+1:]
	} else {
		return ""
	}

	// Take text until the next heading or empty line, skipping leading empty lines
	var lines []string
	for line := range strings.SplitSeq(rest, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if len(lines) > 0 {
				break
			}
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			break
		}
		lines = append(lines, trimmed)
	}

	return strings.Join(lines, " ")
}

// truncateSignature returns a signature truncated to at most 60 runes.
// Uses rune-aware truncation to avoid splitting multi-byte UTF-8 characters.
func truncateSignature(s string) string {
	s = strings.TrimSpace(s)
	runes := []rune(s)
	if len(runes) > 60 {
		runes = runes[:60]
		// Try to break at a word boundary
		truncated := string(runes)
		if lastSpace := strings.LastIndexByte(truncated, ' '); lastSpace > 30 {
			truncated = truncated[:lastSpace]
		}
		return truncated
	}
	return s
}
