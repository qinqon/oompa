package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// commitMsgFile is the well-known filename an agent writes to request a commit
// message change. The outer automation reads and deletes it during squash/amend.
const commitMsgFile = ".oompa-commit-msg"

// readCommitMsgFile reads and removes the commit message override file from the
// worktree root. Returns the trimmed contents and true if the file existed and
// was non-empty; returns ("", false) otherwise.
func readCommitMsgFile(worktreePath string) (string, bool) {
	path := filepath.Join(worktreePath, commitMsgFile)
	content, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	_ = os.Remove(path) //nolint:errcheck // best-effort cleanup
	msg := strings.TrimSpace(string(content))
	if msg == "" {
		return "", false
	}
	return msg, true
}

// ensureTrailers appends configured Signed-off-by and Assisted-by trailers to
// a commit message if they are not already present. This prevents DCO/policy
// violations when the agent overrides the commit message via .oompa-commit-msg.
func (a *Agent) ensureTrailers(msg string) string {
	var trailers []string
	if a.cfg.SignedOffBy != "" && !strings.Contains(msg, "Signed-off-by:") {
		trailers = append(trailers, fmt.Sprintf("Signed-off-by: %s", a.cfg.SignedOffBy))
	}
	if a.cfg.AssistedBy != "" && !strings.Contains(msg, "Assisted-by:") {
		trailers = append(trailers, fmt.Sprintf("Assisted-by: %s", a.cfg.AssistedBy))
	}
	if len(trailers) > 0 {
		msg += "\n\n" + strings.Join(trailers, "\n")
	}
	return msg
}

// buildPRBody constructs a PR description. If the agent wrote a .pr-body.md file
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
	// Degrade gracefully on error: the body is built without the log section.
	logOut, logStderr, logErr := a.runner.Run(ctx, worktreePath, "git", "log", a.originDefaultBranch()+"..HEAD", "--format=%b")
	if logErr != nil {
		a.logger.Warn("failed to get git log for PR body", "issue", issueNumber, "error", logErr, "stderr", string(logStderr))
	}
	rawBody := strings.TrimSpace(string(logOut))

	body := fmt.Sprintf("Fixes #%d\n\n", issueNumber)
	if rawBody != "" {
		body += stripTrailers(rawBody) + "\n\n"
	}
	body += a.botComment()
	return body
}

// stripTrailers removes "Signed-off-by: ..." and "Assisted-by: ..." trailer lines from a string.
func stripTrailers(s string) string {
	var kept []string
	for line := range strings.SplitSeq(s, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "Signed-off-by:") || strings.HasPrefix(trimmed, "Assisted-by:") {
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

// isCommentOnlyDiff returns true if the diff between the origin default branch
// and HEAD is non-empty and every added/removed line is blank or a comment,
// or when the diff is whitespace-only (empty under `git diff -w`).
// Detection is conservative: any changed line that is not clearly a comment or
// whitespace means the diff is treated as a real (functional) change.
func (a *Agent) isCommentOnlyDiff(ctx context.Context, worktreePath string) bool {
	base := a.originDefaultBranch() + "...HEAD"
	out, stderr, err := a.runner.Run(ctx, worktreePath, "git", "diff", base)
	if err != nil {
		a.logger.Warn("git diff failed, skipping comment-only check", "worktree", worktreePath, "error", err, "stderr", string(stderr))
		return false
	}
	if strings.TrimSpace(string(out)) == "" {
		return false
	}
	if isCommentOnlyDiffText(string(out)) {
		return true
	}
	// Whitespace-only reformatting: the diff is non-empty but disappears when
	// whitespace changes are ignored.
	wOut, wStderr, err := a.runner.Run(ctx, worktreePath, "git", "diff", "-w", base)
	if err != nil {
		a.logger.Warn("git diff -w failed, skipping whitespace-only check", "worktree", worktreePath, "error", err, "stderr", string(wStderr))
		return false
	}
	return strings.TrimSpace(string(wOut)) == ""
}

// isCommentOnlyDiffText parses a unified diff and reports whether all changed
// lines are comments or whitespace. An empty diff returns false.
func isCommentOnlyDiffText(diff string) bool {
	hasChangedLine := false
	for line := range strings.SplitSeq(diff, "\n") {
		var content string
		switch {
		case strings.HasPrefix(line, "+++ "), strings.HasPrefix(line, "--- "):
			continue // file headers
		case strings.HasPrefix(line, "+"):
			content = line[1:]
		case strings.HasPrefix(line, "-"):
			content = line[1:]
		default:
			continue // context, hunk headers, metadata
		}
		hasChangedLine = true
		if !isCommentOrBlankLine(content) {
			return false
		}
	}
	return hasChangedLine
}

// nonCommentDirectives are line prefixes that look like comments but change
// tooling or execution behavior, so they must count as functional changes.
var nonCommentDirectives = []string{"#!", "//nolint", "// nolint", "//go:", "// +build"}

// isCommentOrBlankLine returns true if the line is whitespace-only or starts
// with a common comment marker. Directive-style lines (shebangs, linter or
// compiler directives) are NOT considered comments.
func isCommentOrBlankLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return true
	}
	for _, directive := range nonCommentDirectives {
		if strings.HasPrefix(trimmed, directive) {
			return false
		}
	}
	for _, marker := range []string{"#", "//", "/*", "*/", "* "} {
		if strings.HasPrefix(trimmed, marker) {
			return true
		}
	}
	return trimmed == "*"
}

// gitAmendAll stages all changes and amends the current commit.
// If the agent wrote a .oompa-commit-msg file, its contents replace the commit message.
func (a *Agent) gitAmendAll(ctx context.Context, worktreePath string) error {
	if a.cfg.DryRun {
		a.logger.Info("[dry-run] would amend commit", "worktree", worktreePath)
		return nil
	}
	// Read the commit message override BEFORE git add -A so it doesn't get staged.
	newMsg, hasNewMsg := readCommitMsgFile(worktreePath)

	if _, stderr, err := a.runner.Run(ctx, worktreePath, "git", "add", "-A"); err != nil {
		return fmt.Errorf("git add: %w (stderr: %s)", err, string(stderr))
	}
	if hasNewMsg {
		a.logger.Info("using agent-provided commit message", "worktree", worktreePath)
		newMsg = a.ensureTrailers(newMsg)
		if _, stderr, err := a.runner.Run(ctx, worktreePath, "git", "commit", "--amend", "-m", newMsg); err != nil {
			return fmt.Errorf("git commit --amend -m: %w (stderr: %s)", err, string(stderr))
		}
		return nil
	}
	if _, stderr, err := a.runner.Run(ctx, worktreePath, "git", "commit", "--amend", "--no-edit"); err != nil {
		return fmt.Errorf("git commit --amend: %w (stderr: %s)", err, string(stderr))
	}
	return nil
}

// gitSquashInto squashes all commits after the given SHA into that commit.
// Used to fold agent-created review feedback commits back into the original HEAD.
// If the agent wrote a .oompa-commit-msg file, its contents replace the commit message.
func (a *Agent) gitSquashInto(ctx context.Context, worktreePath, targetSHA string) error {
	if a.cfg.DryRun {
		a.logger.Info("[dry-run] would squash into", "worktree", worktreePath, "target", shortSHA(targetSHA))
		return nil
	}
	// Read the commit message override BEFORE git add -A so it doesn't get staged.
	newMsg, hasNewMsg := readCommitMsgFile(worktreePath)

	// Stage all changes first to capture any unstaged modifications the agent left behind
	if _, stderr, err := a.runner.Run(ctx, worktreePath, "git", "add", "-A"); err != nil {
		return fmt.Errorf("git add: %w (stderr: %s)", err, string(stderr))
	}
	// Reset to the target SHA while keeping all changes staged
	if _, stderr, err := a.runner.Run(ctx, worktreePath, "git", "reset", "--soft", targetSHA); err != nil {
		return fmt.Errorf("git reset --soft %s: %w (stderr: %s)", shortSHA(targetSHA), err, string(stderr))
	}
	// Amend the target commit with the staged changes, using the new message if provided
	if hasNewMsg {
		a.logger.Info("using agent-provided commit message", "worktree", worktreePath)
		newMsg = a.ensureTrailers(newMsg)
		if _, stderr, err := a.runner.Run(ctx, worktreePath, "git", "commit", "--amend", "-m", newMsg); err != nil {
			return fmt.Errorf("git commit --amend -m: %w (stderr: %s)", err, string(stderr))
		}
	} else {
		if _, stderr, err := a.runner.Run(ctx, worktreePath, "git", "commit", "--amend", "--no-edit"); err != nil {
			return fmt.Errorf("git commit --amend: %w (stderr: %s)", err, string(stderr))
		}
	}
	return nil
}

// maxSubjectLen is the maximum length of a commit subject line.
const maxSubjectLen = 72

// truncateSubject shortens a commit subject to fit within maxLen runes.
// If truncation is needed, it breaks at the last word boundary before the limit
// and appends "...". If there is no word boundary, it hard-truncates.
// Operates on runes to avoid splitting multi-byte UTF-8 sequences.
func truncateSubject(subject string, maxLen int) string {
	runes := []rune(subject)
	if len(runes) <= maxLen {
		return subject
	}
	// Reserve space for the ellipsis suffix.
	cutoff := maxLen - 3
	if cutoff <= 0 {
		return string(runes[:maxLen])
	}
	// Try to break at the last space before the cutoff.
	truncated := string(runes[:cutoff])
	if idx := strings.LastIndex(truncated, " "); idx > 0 {
		return truncated[:idx] + "..."
	}
	return truncated + "..."
}

// gitSquashCommits squashes all commits since the origin default branch into a single commit.
func (a *Agent) gitSquashCommits(ctx context.Context, worktreePath string, issueNumber int, issueTitle string) error {
	if a.cfg.DryRun {
		a.logger.Info("[dry-run] would squash commits", "worktree", worktreePath, "issue", issueNumber)
		return nil
	}
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

	// Unstage .pr-body.md if the agent accidentally staged it — it must not be committed.
	a.runner.Run(ctx, worktreePath, "git", "restore", "--staged", ".pr-body.md") //nolint:errcheck // best-effort

	// Create a single commit with a meaningful message.
	// Strip any Signed-off-by/Assisted-by trailers the agent may have added to individual
	// commits before building the body, then append exactly one canonical set of trailers.
	//
	// The subject line uses the issue title directly (truncated to 72 chars).
	// The issue reference goes in the body as "Related-to: #N" to avoid
	// auto-close keywords (Fix, Fixes, Closes) that some CI systems reject
	// (e.g. kubevirt prow's invalid-commit-message check).
	subject := truncateSubject(issueTitle, maxSubjectLen)
	var commitMsg string
	commitMsg = fmt.Sprintf("%s\n\nRelated-to: #%d", subject, issueNumber)
	if commitMessages != "" {
		commitMsg += "\n\n" + stripTrailers(commitMessages)
	}
	var trailers []string
	if a.cfg.SignedOffBy != "" {
		trailers = append(trailers, fmt.Sprintf("Signed-off-by: %s", a.cfg.SignedOffBy))
	}
	if a.cfg.AssistedBy != "" {
		trailers = append(trailers, fmt.Sprintf("Assisted-by: %s", a.cfg.AssistedBy))
	}
	if len(trailers) > 0 {
		commitMsg += "\n\n" + strings.Join(trailers, "\n")
	}

	if _, stderr, err := a.runner.Run(ctx, worktreePath, "git", "commit", "-m", commitMsg); err != nil {
		return fmt.Errorf("git commit after squash: %w (stderr: %s)", err, string(stderr))
	}

	return nil
}

// deleteRemoteBranch removes a branch from the push remote.
func (a *Agent) deleteRemoteBranch(ctx context.Context, worktreePath, branchName string) {
	if a.cfg.DryRun {
		a.logger.Info("[dry-run] would delete remote branch", "branch", branchName)
		return
	}
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

// pushFixupsOrAmend pushes agent-produced changes when they take one of the
// two self-evident forms: fixup commits (autosquashed into their targets) or
// uncommitted changes (amended into HEAD). Returns handled=false when neither
// form is present and the caller must decide how to treat a possible direct
// commit; pushed reports whether a push succeeded.
func (a *Agent) pushFixupsOrAmend(ctx context.Context, worktreePath string, prNumber int) (pushed, handled bool) {
	switch {
	case a.hasFixupCommits(ctx, worktreePath):
		if err := a.gitAutosquashRebase(ctx, worktreePath); err != nil {
			a.logger.Error("failed to autosquash fixup commits", "pr", prNumber, "error", err)
			// A failed autosquash can leave the worktree mid-rebase; abort so
			// later flows find a usable tree.
			a.runner.Run(ctx, worktreePath, "git", "rebase", "--abort") //nolint:errcheck // best-effort
		} else if err := a.gitPush(ctx, worktreePath, true); err != nil {
			a.logger.Error("failed to push", "pr", prNumber, "error", err)
		} else {
			pushed = true
		}
		return pushed, true
	case a.hasUncommittedChanges(ctx, worktreePath):
		if err := a.gitAmendAll(ctx, worktreePath); err != nil {
			a.logger.Error("failed to amend commit", "pr", prNumber, "error", err)
		} else if err := a.gitPush(ctx, worktreePath, true); err != nil {
			a.logger.Error("failed to push", "pr", prNumber, "error", err)
		} else {
			pushed = true
		}
		return pushed, true
	}
	return false, false
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
