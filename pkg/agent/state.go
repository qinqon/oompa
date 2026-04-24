package agent

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"
)

// State holds all active issue work in memory.
type State struct {
	ActiveIssues     map[int]*IssueWork
	InvestigatedRuns map[string]bool // jobName:runID -> true
}

// NewState creates an empty state.
func NewState() *State {
	return &State{
		ActiveIssues:     make(map[int]*IssueWork),
		InvestigatedRuns: make(map[string]bool),
	}
}

// MarkRunInvestigated marks a CI job run as investigated.
func (s *State) MarkRunInvestigated(jobName, runID string) {
	key := fmt.Sprintf("%s:%s", jobName, runID)
	s.InvestigatedRuns[key] = true
}

// IsRunInvestigated checks if a CI job run has already been investigated.
func (s *State) IsRunInvestigated(jobName, runID string) bool {
	key := fmt.Sprintf("%s:%s", jobName, runID)
	return s.InvestigatedRuns[key]
}

// BuildStateFromGitHub reconstructs state by scanning labeled issues and their PRs.
func BuildStateFromGitHub(ctx context.Context, gh GitHubClient, cfg Config, cloneDir string, logger *slog.Logger) *State {
	state := NewState()

	// Skip labeled issue scanning when --watch-prs is configured
	// (watch mode bypasses issue discovery)
	if len(cfg.WatchPRs) == 0 {
		issues, err := gh.ListLabeledIssues(ctx, cfg.Owner, cfg.Repo, cfg.Label)
		if err != nil {
			logger.Error("failed to list issues for state rebuild", "error", err)
			return state
		}

		for _, issue := range issues {
		branchName := issueBranchName(issue.Number)
		worktreePath := filepath.Join(cloneDir, "worktrees", branchName)

		work := &IssueWork{
			IssueNumber:  issue.Number,
			IssueTitle:   issue.Title,
			WorktreePath: worktreePath,
			BranchName:   branchName,
			CreatedAt:    time.Now(),
		}

		// Check if a PR already exists for this branch
		prs, err := gh.ListPRsByHead(ctx, cfg.Owner, cfg.Repo, cfg.GitHubHeadOwner, branchName)
		if err != nil {
			logger.Warn("failed to list PRs for issue", "issue", issue.Number, "error", err)
			continue
		}

		if len(prs) > 0 {
			// Find the first open PR
			openPR, hasMerged := classifyPRs(prs)
			switch {
			case openPR != nil:
				work.PRNumber = openPR.Number
				work.Status = StatusPROpen
				// lastCommentID stays 0 — ProcessReviewComments uses :eyes: reaction to skip already-handled comments
				logger.Info("recovered state from GitHub", "issue", issue.Number, "pr", work.PRNumber)
			case hasMerged:
				// PR was merged — skip to avoid reprocessing a completed issue
				logger.Info("skipping issue with merged PR", "issue", issue.Number, "pr", prs[0].Number)
				continue
			default:
				// PR was closed (rejected) — allow retry by treating as new
				continue
			}
		} else {
			// No PR yet — check if it has the ai-failed label
			hasFailed := false
			for _, l := range issue.Labels {
				if l == labelAIFailed {
					hasFailed = true
					break
				}
			}
			if hasFailed {
				work.Status = StatusFailed
				logger.Info("recovered failed issue from GitHub", "issue", issue.Number)
			} else {
				// No PR and not failed — this is a new issue to process
				continue
			}
		}

		state.ActiveIssues[issue.Number] = work
		}
	}

	// Bootstrap state for watched PRs
	for _, prNumber := range cfg.WatchPRs {
		alreadyTracked := false
		for _, work := range state.ActiveIssues {
			if work.PRNumber == prNumber {
				alreadyTracked = true
				break
			}
		}
		if alreadyTracked {
			continue
		}

		pr, err := gh.GetPR(ctx, cfg.Owner, cfg.Repo, prNumber)
		if err != nil {
			logger.Warn("failed to get watched PR details", "pr", prNumber, "error", err)
			continue
		}

		if pr.Merged || pr.State == "closed" {
			logger.Info("skipping watched PR (already closed/merged)", "pr", prNumber)
			continue
		}

		work := &IssueWork{
			IssueTitle:   pr.Title,
			WorktreePath: filepath.Join(cloneDir, "worktrees", pr.Head),
			BranchName:   pr.Head,
			PRNumber:     prNumber,
			Status:       StatusPROpen,
			CreatedAt:    time.Now(),
		}

		state.ActiveIssues[prNumber] = work
		logger.Info("recovered watched PR state", "pr", prNumber, "branch", pr.Head)
	}

	return state
}
