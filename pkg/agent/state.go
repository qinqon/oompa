package agent

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"time"
)

// IssueKey returns the composite state key for an issue or PR number.
// Format: "owner/repo#number" for cross-repo safety.
func IssueKey(owner, repo string, number int) string {
	return fmt.Sprintf("%s/%s#%d", owner, repo, number)
}

// State holds all active issue work in memory.
type State struct {
	ActiveIssues     map[string]*IssueWork // key: "owner/repo#number"
	InvestigatedRuns map[string]bool       // jobName:runID -> true
}

// NewState creates an empty state.
func NewState() *State {
	return &State{
		ActiveIssues:     make(map[string]*IssueWork),
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

// recoverLastRebaseTime recovers LastRebaseTime from the PR head commit date as
// an approximation. This prevents an unnecessary rebase immediately after restart.
// Future timestamps are clamped to now to avoid deferring rebases longer than configured.
func recoverLastRebaseTime(ctx context.Context, gh GitHubClient, cfg Config, work *IssueWork, prNumber int, logger *slog.Logger) {
	headCommitDate, err := gh.GetPRHeadCommitDate(ctx, cfg.Owner, cfg.Repo, prNumber)
	if err != nil {
		logger.Warn("failed to get PR head commit date for rebase time recovery", "pr", prNumber, "error", err)
		return
	}
	if !headCommitDate.IsZero() {
		if headCommitDate.After(time.Now()) {
			headCommitDate = time.Now()
		}
		work.LastRebaseTime = headCommitDate
	}
}

// recoverCommentCursors recovers the comment cursors (LastCommentID, LastReviewID,
// LastIssueCommentID) from GitHub by fetching all existing comments/reviews and
// setting each cursor to the max ID. This prevents re-processing old comments
// after a restart (e.g., --exit-on-new-version).
func recoverCommentCursors(ctx context.Context, gh GitHubClient, cfg Config, work *IssueWork, prNumber int, logger *slog.Logger) {
	// Recover LastCommentID from PR review comments
	comments, err := gh.GetPRReviewComments(ctx, cfg.Owner, cfg.Repo, prNumber, 0)
	if err != nil {
		logger.Warn("failed to get PR review comments for cursor recovery", "pr", prNumber, "error", err)
	} else {
		for _, c := range comments {
			if c.ID > work.LastCommentID {
				work.LastCommentID = c.ID
			}
		}
	}

	// Recover LastReviewID from PR reviews
	reviews, err := gh.GetPRReviews(ctx, cfg.Owner, cfg.Repo, prNumber, 0)
	if err != nil {
		logger.Warn("failed to get PR reviews for cursor recovery", "pr", prNumber, "error", err)
	} else {
		for _, r := range reviews {
			if r.ID > work.LastReviewID {
				work.LastReviewID = r.ID
			}
		}
	}

	// Recover LastIssueCommentID from PR conversation comments (Issues API)
	issueComments, err := gh.GetIssueComments(ctx, cfg.Owner, cfg.Repo, prNumber, 0)
	if err != nil {
		logger.Warn("failed to get issue comments for cursor recovery", "pr", prNumber, "error", err)
	} else {
		for _, c := range issueComments {
			if c.ID > work.LastIssueCommentID {
				work.LastIssueCommentID = c.ID
			}
		}
	}
}

// BuildStateFromGitHub reconstructs state by scanning labeled issues and their PRs.
func BuildStateFromGitHub(ctx context.Context, gh GitHubClient, cfg Config, cloneDir string, logger *slog.Logger) *State {
	state := NewState()

	// Skip labeled issue scanning when --watch-prs is configured
	// (watch mode bypasses issue discovery) or when no label is set
	// (triage roles have no label and should not scan for issues)
	if len(cfg.WatchPRs) == 0 && cfg.Label != "" {
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
					recoverCommentCursors(ctx, gh, cfg, work, openPR.Number, logger)
					recoverLastRebaseTime(ctx, gh, cfg, work, openPR.Number, logger)
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
				hasFailed := slices.Contains(issue.Labels, labelAIFailed)
				if hasFailed {
					work.Status = StatusFailed
					logger.Info("recovered failed issue from GitHub", "issue", issue.Number)
				} else {
					// No PR and not failed — this is a new issue to process
					continue
				}
			}

			state.ActiveIssues[IssueKey(cfg.Owner, cfg.Repo, issue.Number)] = work
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

		recoverCommentCursors(ctx, gh, cfg, work, prNumber, logger)
		recoverLastRebaseTime(ctx, gh, cfg, work, prNumber, logger)

		state.ActiveIssues[IssueKey(cfg.Owner, cfg.Repo, prNumber)] = work
		logger.Info("recovered watched PR state", "pr", prNumber, "branch", pr.Head)
	}

	return state
}
