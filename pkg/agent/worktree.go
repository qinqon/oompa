package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// WorktreeManager manages git worktrees for parallel issue work.
type WorktreeManager interface {
	EnsureRepoCloned(ctx context.Context) error
	CreateWorktree(ctx context.Context, branchName string) (worktreePath string, err error)
	RemoveWorktree(ctx context.Context, worktreePath string) error
	SyncWorktree(ctx context.Context, worktreePath string) error
}

// GitWorktreeManager implements WorktreeManager using git commands.
type GitWorktreeManager struct {
	runner   CommandRunner
	cloneDir string
	repoURL  string // upstream repo URL (cloned as origin)
	forkURL  string // fork repo URL (added as "fork" remote for pushing)
}

// NewGitWorktreeManager creates a new worktree manager.
// repoURL is the upstream repo (cloned as origin).
// forkURL is the user's fork (added as a "fork" remote for pushing); empty if same-repo workflow.
func NewGitWorktreeManager(runner CommandRunner, cloneDir, repoURL, forkURL string) *GitWorktreeManager {
	return &GitWorktreeManager{
		runner:   runner,
		cloneDir: cloneDir,
		repoURL:  repoURL,
		forkURL:  forkURL,
	}
}

func (g *GitWorktreeManager) EnsureRepoCloned(ctx context.Context) error {
	if _, err := os.Stat(filepath.Join(g.cloneDir, ".git")); err == nil {
		_, stderr, err := g.runner.Run(ctx, g.cloneDir, "git", "fetch", "origin")
		if err != nil {
			return fmt.Errorf("git fetch origin: %w (stderr: %s)", err, string(stderr))
		}
		g.ensureForkRemote(ctx)
		return nil
	}

	_, stderr, err := g.runner.Run(ctx, "", "git", "clone", g.repoURL, g.cloneDir)
	if err != nil {
		return fmt.Errorf("git clone: %w (stderr: %s)", err, string(stderr))
	}
	g.ensureForkRemote(ctx)
	return nil
}

// ensureForkRemote adds a "fork" remote for the user's fork if it differs from origin.
func (g *GitWorktreeManager) ensureForkRemote(ctx context.Context) {
	if g.forkURL == "" || g.forkURL == g.repoURL {
		return
	}
	// Add fork remote (ignore error if it already exists)
	g.runner.Run(ctx, g.cloneDir, "git", "remote", "add", "fork", g.forkURL)
}

// PushRemote returns the git remote name to push to ("fork" or "origin").
func (g *GitWorktreeManager) PushRemote() string {
	if g.forkURL != "" && g.forkURL != g.repoURL {
		return "fork"
	}
	return "origin"
}

func (g *GitWorktreeManager) CreateWorktree(ctx context.Context, branchName string) (string, error) {
	worktreePath := filepath.Join(g.cloneDir, "worktrees", branchName)

	// Reuse existing worktree if it's still valid
	if _, err := os.Stat(filepath.Join(worktreePath, ".git")); err == nil {
		return worktreePath, nil
	}

	// Clean up stale worktree state
	g.runner.Run(ctx, g.cloneDir, "git", "worktree", "remove", "--force", worktreePath)
	os.RemoveAll(worktreePath)
	g.runner.Run(ctx, g.cloneDir, "git", "worktree", "prune")
	g.runner.Run(ctx, g.cloneDir, "git", "branch", "-D", branchName)

	_, stderr, err := g.runner.Run(ctx, g.cloneDir, "git", "worktree", "add", "-b", branchName, worktreePath, "origin/main")
	if err != nil {
		return "", fmt.Errorf("git worktree add: %w (stderr: %s)", err, string(stderr))
	}

	return worktreePath, nil
}

func (g *GitWorktreeManager) SyncWorktree(ctx context.Context, worktreePath string) error {
	// Fetch latest from all remotes
	_, stderr, err := g.runner.Run(ctx, worktreePath, "git", "fetch", "--all")
	if err != nil {
		return fmt.Errorf("git fetch: %w (stderr: %s)", err, string(stderr))
	}

	// Get the current branch name
	branchOut, _, err := g.runner.Run(ctx, worktreePath, "git", "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return fmt.Errorf("git rev-parse: %w", err)
	}
	branch := strings.TrimSpace(string(branchOut))

	// Try to reset to the push remote's branch (fork or origin)
	pushRemote := g.PushRemote()
	_, stderr, err = g.runner.Run(ctx, worktreePath, "git", "reset", "--hard", pushRemote+"/"+branch)
	if err != nil {
		// Branch may not exist on the push remote yet, that's OK
		return nil
	}
	return nil
}

func (g *GitWorktreeManager) RemoveWorktree(ctx context.Context, worktreePath string) error {
	_, stderr, err := g.runner.Run(ctx, g.cloneDir, "git", "worktree", "remove", "--force", worktreePath)
	if err != nil {
		return fmt.Errorf("git worktree remove: %w (stderr: %s)", err, string(stderr))
	}
	return nil
}
