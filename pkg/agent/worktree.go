package agent

import "github.com/qinqon/oompa/internal/worktree"

// Worktree management lives in internal/worktree; these aliases keep the
// package-local names used by the agent and cmd/oompa.
type (
	// WorktreeManager manages git worktrees, one per issue branch.
	WorktreeManager = worktree.Manager
	// GitWorktreeManager implements WorktreeManager using git commands.
	GitWorktreeManager = worktree.GitManager
)

// NewGitWorktreeManager constructs a git-backed WorktreeManager; see
// internal/worktree for details.
func NewGitWorktreeManager(runner CommandRunner, cloneDir, repoURL, forkURL string) *GitWorktreeManager {
	return worktree.NewGitManager(runner, cloneDir, repoURL, forkURL)
}
