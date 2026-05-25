package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// buildPRBody constructs a PR description. If Claude wrote a .pr-body.md file
// (filled from the repo's PR template), that is used. Otherwise falls back to
// constructing a body from the git log.
func (a *Agent) buildPRBody(ctx context.Context, worktreePath string, issueNumber int) string {
	prBodyFile := filepath.Join(worktreePath, ".pr-body.md")
	if content, err := os.ReadFile(prBodyFile); err == nil {
		_ = os.Remove(prBodyFile) //nolint:errcheck // best-effort cleanup
		body := strings.TrimSpace(string(content))
		if !strings.Contains(body, botMarker) {
			body += "\n\n" + a.botComment()
		}
		return body
	}

	// Fallback: construct from git log body only (skip subject to avoid duplication).
	logOut, _, _ := a.runner.Run(ctx, worktreePath, "git", "log", a.originDefaultBranch()+"..HEAD", "--format=%b---")
	rawBody := strings.TrimSpace(string(logOut))

	body := fmt.Sprintf("Fixes #%d\n\n", issueNumber)
	if rawBody != "" {
		body += stripSignedOffBy(rawBody) + "\n\n"
	}
	body += a.botComment()
	return body
}

// stripSignedOffBy removes "Signed-off-by: ..." trailer lines from a string.
func stripSignedOffBy(s string) string {
	var kept []string
	for line := range strings.SplitSeq(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "Signed-off-by:") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

// ensureWorktreeReady ensures the repo is cloned and the worktree exists for the given work item.
// If the worktree was lost (e.g. after a restart with a fresh volume), it recreates it.
// If the worktree is corrupted (e.g. after a kill mid-operation), CreateWorktree auto-recovers.
func (a *Agent) ensureWorktreeReady(ctx context.Context, work *IssueWork) error {
	if err := a.worktrees.EnsureRepoCloned(ctx); err != nil {
		return err
	}
	worktreePath, err := a.worktrees.CreateWorktree(ctx, work.BranchName)
	if err != nil {
		return err
	}
	work.WorktreePath = worktreePath

	// Pull the latest from the push remote
	return a.worktrees.SyncWorktree(ctx, worktreePath)
}

// hasUncommittedChanges returns true if the worktree has staged or unstaged changes.
func (a *Agent) hasUncommittedChanges(ctx context.Context, worktreePath string) bool {
	out, _, _ := a.runner.Run(ctx, worktreePath, "git", "status", "--porcelain")
	return strings.TrimSpace(string(out)) != ""
}

// gitAmendAll stages all changes and amends the current commit.
func (a *Agent) gitAmendAll(ctx context.Context, worktreePath string) error {
	if a.cfg.DryRun {
		a.logger.Info("[dry-run] would amend commit", "worktree", worktreePath)
		return nil
	}
	if _, stderr, err := a.runner.Run(ctx, worktreePath, "git", "add", "-A"); err != nil {
		return fmt.Errorf("git add: %w (stderr: %s)", err, string(stderr))
	}
	if _, stderr, err := a.runner.Run(ctx, worktreePath, "git", "commit", "--amend", "--no-edit"); err != nil {
		return fmt.Errorf("git commit --amend: %w (stderr: %s)", err, string(stderr))
	}
	return nil
}

// gitSquashInto squashes all commits after the given SHA into that commit.
// Used to fold agent-created review feedback commits back into the original HEAD.
func (a *Agent) gitSquashInto(ctx context.Context, worktreePath, targetSHA string) error {
	if a.cfg.DryRun {
		a.logger.Info("[dry-run] would squash into", "worktree", worktreePath, "target", shortSHA(targetSHA))
		return nil
	}
	// Stage all changes first to capture any unstaged modifications the agent left behind
	if _, stderr, err := a.runner.Run(ctx, worktreePath, "git", "add", "-A"); err != nil {
		return fmt.Errorf("git add: %w (stderr: %s)", err, string(stderr))
	}
	// Reset to the target SHA while keeping all changes staged
	if _, stderr, err := a.runner.Run(ctx, worktreePath, "git", "reset", "--soft", targetSHA); err != nil {
		return fmt.Errorf("git reset --soft %s: %w (stderr: %s)", shortSHA(targetSHA), err, string(stderr))
	}
	// Amend the target commit with the staged changes
	if _, stderr, err := a.runner.Run(ctx, worktreePath, "git", "commit", "--amend", "--no-edit"); err != nil {
		return fmt.Errorf("git commit --amend: %w (stderr: %s)", err, string(stderr))
	}
	return nil
}

// gitSquashCommits squashes all commits since the origin default branch into a single commit.
func (a *Agent) gitSquashCommits(ctx context.Context, worktreePath string, issueNumber int, issueTitle string) error {
	// Get all commit messages since origin default branch
	logOut, _, err := a.runner.Run(ctx, worktreePath, "git", "log", a.originDefaultBranch()+"..HEAD", "--format=%B")
	if err != nil {
		return fmt.Errorf("getting commit messages: %w", err)
	}
	commitMessages := strings.TrimSpace(string(logOut))

	// Reset to origin default branch while keeping changes staged
	if _, stderr, err := a.runner.Run(ctx, worktreePath, "git", "reset", "--soft", a.originDefaultBranch()); err != nil {
		return fmt.Errorf("git reset --soft: %w (stderr: %s)", err, string(stderr))
	}

	// Unstage .pr-body.md if Claude accidentally staged it — it must not be committed.
	a.runner.Run(ctx, worktreePath, "git", "restore", "--staged", ".pr-body.md") //nolint:errcheck // best-effort

	// Create a single commit with a meaningful message.
	// Strip any Signed-off-by trailers Claude may have added to individual commits
	// before building the body, then append exactly one canonical trailer at the end.
	commitMsg := fmt.Sprintf("Fix #%d: %s", issueNumber, issueTitle)
	if commitMessages != "" {
		commitMsg += "\n\n" + stripSignedOffBy(commitMessages)
	}
	if a.cfg.SignedOffBy != "" {
		commitMsg += fmt.Sprintf("\n\nSigned-off-by: %s", a.cfg.SignedOffBy)
	}

	if _, stderr, err := a.runner.Run(ctx, worktreePath, "git", "commit", "-m", commitMsg); err != nil {
		return fmt.Errorf("git commit after squash: %w (stderr: %s)", err, string(stderr))
	}

	return nil
}

// deleteRemoteBranch removes a branch from the push remote.
func (a *Agent) deleteRemoteBranch(ctx context.Context, worktreePath, branchName string) {
	pushRemote := a.pushRemote()
	_, stderr, err := a.runner.Run(ctx, worktreePath, "git", "push", pushRemote, "--delete", branchName)
	if err != nil {
		a.logger.Warn("failed to delete remote branch", "branch", branchName, "error", err, "stderr", string(stderr))
	} else {
		a.logger.Info("deleted remote branch", "branch", branchName)
	}
}

// gitPush pushes the current branch to the push remote.
func (a *Agent) gitPush(ctx context.Context, worktreePath string, force bool) error {
	if a.cfg.DryRun {
		a.logger.Info("[dry-run] would push", "worktree", worktreePath, "force", force)
		return nil
	}
	pushRemote := a.pushRemote()

	// Get the current branch name for the refspec
	branchOut, _, err := a.runner.Run(ctx, worktreePath, "git", "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return fmt.Errorf("getting branch name: %w", err)
	}
	branch := strings.TrimSpace(string(branchOut))

	args := []string{"push", pushRemote, "HEAD:" + branch}
	if force {
		// Fetch first so --force-with-lease has up-to-date tracking refs
		a.runner.Run(ctx, worktreePath, "git", "fetch", pushRemote) //nolint:errcheck // best-effort
		args = append(args, "--force-with-lease")
	}

	if _, stderr, err := a.runner.Run(ctx, worktreePath, "git", args...); err != nil {
		return fmt.Errorf("git push: %w (stderr: %s)", err, string(stderr))
	}
	return nil
}

// getPRCommits returns the list of commits in the PR (commits between origin default branch and HEAD).
func (a *Agent) getPRCommits(ctx context.Context, worktreePath string) []Commit {
	logOut, _, err := a.runner.Run(ctx, worktreePath, "git", "log", a.originDefaultBranch()+"..HEAD", "--format=%H %s")
	if err != nil {
		return nil
	}

	lines := strings.Split(strings.TrimSpace(string(logOut)), "\n")
	commits := make([]Commit, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) < 2 {
			continue
		}
		commits = append(commits, Commit{
			SHA:     parts[0],
			Subject: parts[1],
		})
	}
	return commits
}

// hasFixupCommits returns true if there are any fixup commits in the current branch.
func (a *Agent) hasFixupCommits(ctx context.Context, worktreePath string) bool {
	logOut, _, err := a.runner.Run(ctx, worktreePath, "git", "log", a.originDefaultBranch()+"..HEAD", "--format=%s")
	if err != nil {
		return false
	}
	return strings.Contains(string(logOut), "fixup!")
}

// gitRebaseWithRetry attempts a rebase and, if it fails due to unstaged changes
// (e.g. upstream file deletions), cleans the worktree and retries once.
// Returns the stderr output (as a string) and any error from the final rebase attempt.
func (a *Agent) gitRebaseWithRetry(ctx context.Context, worktreePath string, prNumber int) (string, error) {
	_, stderr, rebaseErr := a.runner.Run(ctx, worktreePath, "git", "rebase", a.originDefaultBranch())
	if rebaseErr != nil && isUnstagedChangesError(string(stderr)) {
		// Upstream file deletions can leave unstaged changes that block rebase.
		// Clean the worktree and retry.
		a.logger.Info("cleaning unstaged changes before rebase retry", "pr", prNumber)
		a.runner.Run(ctx, worktreePath, "git", "checkout", "--", ".") //nolint:errcheck // best-effort
		_, stderr, rebaseErr = a.runner.Run(ctx, worktreePath, "git", "rebase", a.originDefaultBranch())
	}
	return string(stderr), rebaseErr
}

// gitAutosquashRebase performs an interactive rebase with autosquash to merge fixup commits.
func (a *Agent) gitAutosquashRebase(ctx context.Context, worktreePath string) error {
	// Set GIT_SEQUENCE_EDITOR to cat so rebase doesn't wait for interactive input
	_, stderr, err := a.runner.Run(ctx, worktreePath, "sh", "-c",
		fmt.Sprintf("GIT_SEQUENCE_EDITOR=cat git rebase -i --autosquash %s", a.originDefaultBranch()))
	if err != nil {
		return fmt.Errorf("git rebase --autosquash: %w (stderr: %s)", err, string(stderr))
	}
	return nil
}
