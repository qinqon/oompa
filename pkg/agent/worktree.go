package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
	repoURL  string
}

// NewGitWorktreeManager creates a new worktree manager.
func NewGitWorktreeManager(runner CommandRunner, cloneDir, repoURL string) *GitWorktreeManager {
	return &GitWorktreeManager{
		runner:   runner,
		cloneDir: cloneDir,
		repoURL:  repoURL,
	}
}

func (g *GitWorktreeManager) EnsureRepoCloned(ctx context.Context) error {
	if _, err := os.Stat(filepath.Join(g.cloneDir, ".git")); err == nil {
		_, stderr, err := g.runner.Run(ctx, g.cloneDir, "git", "fetch", "origin")
		if err != nil {
			return fmt.Errorf("git fetch: %w (stderr: %s)", err, string(stderr))
		}
		return nil
	}

	_, stderr, err := g.runner.Run(ctx, "", "git", "clone", g.repoURL, g.cloneDir)
	if err != nil {
		return fmt.Errorf("git clone: %w (stderr: %s)", err, string(stderr))
	}
	return nil
}

func (g *GitWorktreeManager) CreateWorktree(ctx context.Context, branchName string) (string, error) {
	worktreePath := filepath.Join(g.cloneDir, "worktrees", branchName)

	// Clean up from a previous failed attempt
	g.runner.Run(ctx, g.cloneDir, "git", "worktree", "remove", "--force", worktreePath)
	g.runner.Run(ctx, g.cloneDir, "git", "worktree", "prune")
	g.runner.Run(ctx, g.cloneDir, "git", "branch", "-D", branchName)

	_, stderr, err := g.runner.Run(ctx, g.cloneDir, "git", "worktree", "add", "-b", branchName, worktreePath, "origin/main")
	if err != nil {
		return "", fmt.Errorf("git worktree add: %w (stderr: %s)", err, string(stderr))
	}

	return worktreePath, nil
}

func (g *GitWorktreeManager) SyncWorktree(ctx context.Context, worktreePath string) error {
	_, stderr, err := g.runner.Run(ctx, worktreePath, "git", "pull", "--rebase", "origin")
	if err != nil {
		return fmt.Errorf("git pull --rebase: %w (stderr: %s)", err, string(stderr))
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
