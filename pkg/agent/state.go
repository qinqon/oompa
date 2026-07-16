package agent

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// IssueKey returns the composite state key for an issue or PR number.
// Format: "owner/repo#number" for cross-repo safety.
//
// Issue-driven work is keyed by issue number and watched PRs by PR number;
// the two cannot collide because GitHub allocates issues and pull requests
// from one shared number sequence per repository. The same PR could still be
// tracked twice under different keys (once via its issue, once via a watch
// entry), which is why the watched-PR bootstrap paths scan existing entries
// for a matching PRNumber before inserting.
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
// setting each cursor to the max ID among comments that show evidence of prior
// processing. This prevents both re-processing old comments after a restart AND
// skipping unprocessed comments that arrived after the last processing cycle.
//
// Processing evidence differs by cursor type:
//
// LastCommentID (PR review comments): bot-posted, bot-replied-to, contains
// bot marker, or has an eyes reaction from the bot.
//
// LastReviewID (PR reviews): bot-posted or contains bot marker. The agent
// never creates PR reviews in practice, so this cursor typically recovers
// to 0 — all reviews are re-fetched once and the cursor advances on the
// first poll cycle after filtering.
//
// LastIssueCommentID (issue comments): bot-posted or contains bot marker.
//
// Comments from external reviewers that have NOT been processed remain above
// the cursor so they get picked up in the next review cycle.
func recoverCommentCursors(ctx context.Context, gh GitHubClient, cfg Config, work *IssueWork, prNumber int, logger *slog.Logger) {
	// Recover LastCommentID from PR review comments.
	// Only advance past comments that show evidence of processing:
	// bot-posted, bot-replied-to, containing bot marker, or having eyes reaction.
	comments, err := gh.GetPRReviewComments(ctx, cfg.Owner, cfg.Repo, prNumber, 0)
	if err != nil {
		logger.Warn("failed to get PR review comments for cursor recovery", "pr", prNumber, "error", err)
	} else {
		// Build a set of comment IDs that the bot has replied to.
		botRepliedTo := make(map[int64]bool)
		for _, c := range comments {
			if c.User == cfg.GitHubUser && c.InReplyToID != 0 {
				botRepliedTo[c.InReplyToID] = true
			}
		}

		for _, c := range comments {
			processed := c.User == cfg.GitHubUser ||
				botRepliedTo[c.ID] ||
				strings.Contains(c.Body, botMarker)
			if !processed {
				// Check for eyes reaction from bot — the definitive processing marker.
				// This catches comments that were processed (eyes added) but got no
				// bot reply (e.g., agent pushed changes without replying individually).
				if hasEyes, eyesErr := gh.HasPRCommentReaction(ctx, cfg.Owner, cfg.Repo, c.ID, "eyes", cfg.GitHubUser); eyesErr != nil {
					logger.Warn("failed to check eyes reaction for cursor recovery", "pr", prNumber, "comment", c.ID, "error", eyesErr)
				} else if hasEyes {
					processed = true
				}
			}
			if processed && c.ID > work.LastCommentID {
				work.LastCommentID = c.ID
			}
		}
	}

	// Recover LastReviewID from PR reviews.
	// Only advance past reviews from the bot user or containing the bot marker.
	// The agent never creates PR reviews, so this cursor typically recovers to 0.
	// All reviews are re-fetched once at startup; ProcessReviewComments filters
	// them and advances the cursor on the first poll cycle.
	reviews, err := gh.GetPRReviews(ctx, cfg.Owner, cfg.Repo, prNumber, 0)
	if err != nil {
		logger.Warn("failed to get PR reviews for cursor recovery", "pr", prNumber, "error", err)
	} else {
		for _, r := range reviews {
			processed := r.User == cfg.GitHubUser || strings.Contains(r.Body, botMarker)
			if processed && r.ID > work.LastReviewID {
				work.LastReviewID = r.ID
			}
		}
	}

	// Recover LastIssueCommentID from PR conversation comments (Issues API).
	// Only advance past comments from the bot user or containing the bot marker.
	issueComments, err := gh.GetIssueComments(ctx, cfg.Owner, cfg.Repo, prNumber, 0)
	if err != nil {
		logger.Warn("failed to get issue comments for cursor recovery", "pr", prNumber, "error", err)
	} else {
		for _, c := range issueComments {
			processed := c.User == cfg.GitHubUser || strings.Contains(c.Body, botMarker)
			if processed && c.ID > work.LastIssueCommentID {
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
		}

		recoverCommentCursors(ctx, gh, cfg, work, prNumber, logger)
		recoverLastRebaseTime(ctx, gh, cfg, work, prNumber, logger)

		state.ActiveIssues[IssueKey(cfg.Owner, cfg.Repo, prNumber)] = work
		logger.Info("recovered watched PR state", "pr", prNumber, "branch", pr.Head)
	}

	return state
}
