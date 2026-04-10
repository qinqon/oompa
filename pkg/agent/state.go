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
	ActiveIssues map[int]*IssueWork
}

// NewState creates an empty state.
func NewState() *State {
	return &State{
		ActiveIssues: make(map[int]*IssueWork),
	}
}

// BuildStateFromGitHub reconstructs state by scanning labeled issues and their PRs.
func BuildStateFromGitHub(ctx context.Context, gh GitHubClient, cfg Config, cloneDir string, logger *slog.Logger) *State {
	state := NewState()

	issues, err := gh.ListLabeledIssues(ctx, cfg.Owner, cfg.Repo, cfg.Label)
	if err != nil {
		logger.Error("failed to list issues for state rebuild", "error", err)
		return state
	}

	for _, issue := range issues {
		branchName := fmt.Sprintf("ai/issue-%d", issue.Number)
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
			work.PRNumber = prs[0].Number
			work.Status = "pr-open"
			// lastCommentID stays 0 — ProcessReviewComments uses :eyes: reaction to skip already-handled comments

			logger.Info("recovered state from GitHub", "issue", issue.Number, "pr", work.PRNumber)
		} else {
			// No PR yet — check if it has the ai-failed label
			hasFailed := false
			for _, l := range issue.Labels {
				if l == "ai-failed" {
					hasFailed = true
					break
				}
			}
			if hasFailed {
				work.Status = "failed"
				logger.Info("recovered failed issue from GitHub", "issue", issue.Number)
			} else {
				// No PR and not failed — this is a new issue to process
				continue
			}
		}

		state.ActiveIssues[issue.Number] = work
	}

	return state
}
