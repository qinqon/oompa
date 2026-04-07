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
		_, _, err := g.runner.Run(ctx, g.cloneDir, "git", "fetch", "origin")
		if err != nil {
			return fmt.Errorf("git fetch: %w", err)
		}
		return nil
	}

	_, _, err := g.runner.Run(ctx, "", "git", "clone", g.repoURL, g.cloneDir)
	if err != nil {
		return fmt.Errorf("git clone: %w", err)
	}
	return nil
}

func (g *GitWorktreeManager) CreateWorktree(ctx context.Context, branchName string) (string, error) {
	worktreePath := filepath.Join(g.cloneDir, "worktrees", branchName)

	_, _, err := g.runner.Run(ctx, g.cloneDir, "git", "worktree", "add", "-b", branchName, worktreePath, "origin/main")
	if err != nil {
		return "", fmt.Errorf("git worktree add: %w", err)
	}

	return worktreePath, nil
}

func (g *GitWorktreeManager) RemoveWorktree(ctx context.Context, worktreePath string) error {
	_, _, err := g.runner.Run(ctx, g.cloneDir, "git", "worktree", "remove", "--force", worktreePath)
	if err != nil {
		return fmt.Errorf("git worktree remove: %w", err)
	}
	return nil
}
