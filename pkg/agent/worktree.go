package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	runner         CommandRunner
	cloneDir       string
	repoURL        string     // upstream repo URL (cloned as origin)
	forkURL        string     // fork repo URL (added as "fork" remote for pushing)
	gitAuthorName  string     // override git user.name in worktrees
	gitAuthorEmail string     // override git user.email in worktrees
	defaultBranch  string     // detected from origin HEAD (e.g. "main", "master")
	mu             sync.Mutex // serializes git operations that modify .git state
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

// DefaultBranch returns the detected default branch of the upstream repo (e.g. "main", "master").
// Falls back to "main" if not yet detected.
func (g *GitWorktreeManager) DefaultBranch() string {
	if g.defaultBranch != "" {
		return g.defaultBranch
	}
	return "main"
}

// OriginDefaultBranch returns "origin/<default-branch>" (e.g. "origin/main", "origin/master").
func (g *GitWorktreeManager) OriginDefaultBranch() string {
	return "origin/" + g.DefaultBranch()
}

// detectDefaultBranch discovers the default branch from origin's HEAD.
func (g *GitWorktreeManager) detectDefaultBranch(ctx context.Context) {
	out, _, err := g.runner.Run(ctx, g.cloneDir, "git", "symbolic-ref", "refs/remotes/origin/HEAD")
	if err != nil {
		return
	}
	// Output is like "refs/remotes/origin/master"
	ref := strings.TrimSpace(string(out))
	if branch, ok := strings.CutPrefix(ref, "refs/remotes/origin/"); ok {
		g.defaultBranch = branch
	}
}

func (g *GitWorktreeManager) EnsureRepoCloned(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if _, err := os.Stat(filepath.Join(g.cloneDir, ".git")); err == nil {
		// Verify origin URL matches the configured repo to prevent stale clones
		urlOut, _, _ := g.runner.Run(ctx, g.cloneDir, "git", "remote", "get-url", "origin")
		currentURL := strings.TrimSpace(string(urlOut))
		if currentURL != g.repoURL {
			// Origin points to a different repo — re-set it
			g.runner.Run(ctx, g.cloneDir, "git", "remote", "set-url", "origin", g.repoURL) //nolint:errcheck // best-effort
		}

		_, stderr, err := g.runner.Run(ctx, g.cloneDir, "git", "fetch", "origin")
		if err != nil {
			return fmt.Errorf("git fetch origin: %w (stderr: %s)", err, string(stderr))
		}
		g.ensureForkRemote(ctx)
		g.detectDefaultBranch(ctx)
		return nil
	}

	_, stderr, err := g.runner.Run(ctx, "", "git", "clone", g.repoURL, g.cloneDir)
	if err != nil {
		return fmt.Errorf("git clone: %w (stderr: %s)", err, string(stderr))
	}
	g.ensureForkRemote(ctx)
	g.configureGitIdentity(ctx, g.cloneDir)
	g.detectDefaultBranch(ctx)
	return nil
}

// SetGitIdentity configures the git author/committer identity for worktrees.
// When set, git user.name and user.email are written to the local git config
// of every clone and worktree, overriding the global git config.
func (g *GitWorktreeManager) SetGitIdentity(name, email string) {
	g.gitAuthorName = name
	g.gitAuthorEmail = email
}

// configureGitIdentity sets local git user.name/user.email in the given directory
// and disables commit signing to avoid using the host's GPG/SSH keys.
func (g *GitWorktreeManager) configureGitIdentity(ctx context.Context, dir string) {
	if g.gitAuthorName != "" {
		g.runner.Run(ctx, dir, "git", "config", "user.name", g.gitAuthorName) //nolint:errcheck // best-effort
	}
	if g.gitAuthorEmail != "" {
		g.runner.Run(ctx, dir, "git", "config", "user.email", g.gitAuthorEmail) //nolint:errcheck // best-effort
	}
	if g.gitAuthorName != "" || g.gitAuthorEmail != "" {
		g.runner.Run(ctx, dir, "git", "config", "commit.gpgsign", "false") //nolint:errcheck // best-effort
		g.runner.Run(ctx, dir, "git", "config", "tag.gpgsign", "false")    //nolint:errcheck // best-effort
	}
}

// ensureForkRemote adds a "fork" remote for the user's fork if it differs from origin.
func (g *GitWorktreeManager) ensureForkRemote(ctx context.Context) {
	if g.forkURL == "" || g.forkURL == g.repoURL {
		return
	}
	// Add fork remote (ignore error if it already exists)
	g.runner.Run(ctx, g.cloneDir, "git", "remote", "add", "fork", g.forkURL) //nolint:errcheck // best-effort
}

// PushRemote returns the git remote name to push to ("fork" or "origin").
func (g *GitWorktreeManager) PushRemote() string {
	if g.forkURL != "" && g.forkURL != g.repoURL {
		return "fork"
	}
	return "origin"
}

func (g *GitWorktreeManager) CreateWorktree(ctx context.Context, branchName string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	worktreePath := filepath.Join(g.cloneDir, "worktrees", branchName)

	// Reuse existing worktree if it's still valid
	if _, err := os.Stat(filepath.Join(worktreePath, ".git")); err == nil {
		g.configureGitIdentity(ctx, worktreePath)
		return worktreePath, nil
	}

	// Clean up stale worktree state
	g.runner.Run(ctx, g.cloneDir, "git", "worktree", "remove", "--force", worktreePath) //nolint:errcheck // best-effort
	os.RemoveAll(worktreePath)                                                          //nolint:errcheck // best-effort
	g.runner.Run(ctx, g.cloneDir, "git", "worktree", "prune")                           //nolint:errcheck // best-effort
	g.runner.Run(ctx, g.cloneDir, "git", "branch", "-D", branchName)                    //nolint:errcheck // best-effort

	_, stderr, err := g.runner.Run(ctx, g.cloneDir, "git", "worktree", "add", "-b", branchName, worktreePath, g.OriginDefaultBranch())
	if err != nil {
		return "", fmt.Errorf("git worktree add: %w (stderr: %s)", err, string(stderr))
	}

	g.configureGitIdentity(ctx, worktreePath)
	return worktreePath, nil
}

func (g *GitWorktreeManager) SyncWorktree(ctx context.Context, worktreePath string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

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

	// Pull latest from the push remote, rebasing to preserve external contributions
	pushRemote := g.PushRemote()
	_, _, err = g.runner.Run(ctx, worktreePath, "git", "pull", "--rebase", pushRemote, branch)
	if err != nil {
		// Branch may not exist on the push remote yet, that's OK
		return nil
	}
	return nil
}

func (g *GitWorktreeManager) RemoveWorktree(ctx context.Context, worktreePath string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	_, stderr, err := g.runner.Run(ctx, g.cloneDir, "git", "worktree", "remove", "--force", worktreePath)
	if err != nil {
		return fmt.Errorf("git worktree remove: %w (stderr: %s)", err, string(stderr))
	}
	return nil
}
