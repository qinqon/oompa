package agent

import (
	"context"
	"fmt"
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
	if len(a.cfg.TriageJobs) == 0 {
		return
	}

	var tasks []triageRunTask

	for _, jobURL := range a.cfg.TriageJobs {
		a.logger.Debug("processing triage job", "url", jobURL)

		// Parse the CI job URL to determine the backend
		ciSource, err := ParseCIJobURL(jobURL, a.gh)
		if err != nil {
			a.logger.Error("failed to parse CI job URL", "url", jobURL, "error", err)
			continue
		}

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

	// Investigate collected failed runs in parallel
	runSequential(ctx, tasks, func(ctx context.Context, task triageRunTask) {
		a.investigateTriageRun(ctx, task.ciSource, task.run)
	})
}

// investigateTriageRun handles the investigation of a single failed CI run.
func (a *Agent) investigateTriageRun(ctx context.Context, ciSource CIJobSource, run JobRun) {
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

	// Create a worktree on the default branch for read-only codebase access
	branchName := fmt.Sprintf("triage/%s", run.ID)

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
<!-- oompa-triage -->`, ciSource.JobName(), run.ID, run.LogURL, analysis)

			issueNumber, err := a.gh.CreateIssue(ctx, a.cfg.Owner, a.cfg.Repo, title, body, []string{a.cfg.FlakyLabel})
			if err != nil {
				a.logger.Error("failed to create issue", "error", err)
			} else {
				a.logger.Info("created issue for CI failure", "job", ciSource.JobName(), "issue", issueNumber)
			}
		}
	}

	// Mark the run as investigated
	a.state.MarkRunInvestigated(ciSource.JobName(), run.ID)

	// Clean up the triage worktree
	if err := a.worktrees.RemoveWorktree(ctx, worktreePath); err != nil {
		a.logger.Warn("failed to remove triage worktree", "path", worktreePath, "error", err)
	}
}
