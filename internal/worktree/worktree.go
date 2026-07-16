// Package worktree manages the shared clone and per-branch git worktrees
// the agent operates in.
package worktree

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/qinqon/oompa/internal/execx"
)

// Manager manages git worktrees, one per issue branch.
type Manager interface {
	EnsureRepoCloned(ctx context.Context) error
	CreateWorktree(ctx context.Context, branchName string) (worktreePath string, err error)
	RemoveWorktree(ctx context.Context, worktreePath string) error
	SyncWorktree(ctx context.Context, worktreePath string) error
	// DefaultBranch returns the default branch name of the upstream repo
	// (e.g. "main", "master"). Implementations that detect the branch
	// lazily may return "main" as a fallback until detection has run
	// (for GitManager, during EnsureRepoCloned).
	DefaultBranch() string
	// OriginDefaultBranch returns "origin/<default-branch>".
	OriginDefaultBranch() string
	// PushRemote returns the git remote name to push to ("fork" or "origin").
	PushRemote() string
}

// GitManager implements Manager using git commands.
type GitManager struct {
	runner         execx.CommandRunner
	cloneDir       string
	repoURL        string     // upstream repo URL (cloned as origin)
	forkURL        string     // fork repo URL (added as "fork" remote for pushing)
	gitAuthorName  string     // override git user.name in worktrees
	gitAuthorEmail string     // override git user.email in worktrees
	mu             sync.Mutex // serializes git operations that modify .git state

	// defaultBranchMu guards defaultBranch separately: DefaultBranch is
	// called from methods that already hold mu, so reusing mu would
	// deadlock.
	defaultBranchMu sync.Mutex
	defaultBranch   string // detected from origin HEAD (e.g. "main", "master")
}

// NewGitManager creates a new worktree manager.
// repoURL is the upstream repo (cloned as origin).
// forkURL is the user's fork (added as a "fork" remote for pushing); empty if same-repo workflow.
func NewGitManager(runner execx.CommandRunner, cloneDir, repoURL, forkURL string) *GitManager {
	return &GitManager{
		runner:   runner,
		cloneDir: cloneDir,
		repoURL:  repoURL,
		forkURL:  forkURL,
	}
}

// DefaultBranch returns the detected default branch of the upstream repo (e.g. "main", "master").
// Falls back to "main" if not yet detected.
func (g *GitManager) DefaultBranch() string {
	g.defaultBranchMu.Lock()
	defer g.defaultBranchMu.Unlock()
	if g.defaultBranch != "" {
		return g.defaultBranch
	}
	return "main"
}

// OriginDefaultBranch returns "origin/<default-branch>" (e.g. "origin/main", "origin/master").
func (g *GitManager) OriginDefaultBranch() string {
	return "origin/" + g.DefaultBranch()
}

// detectDefaultBranch discovers the default branch from origin's HEAD.
func (g *GitManager) detectDefaultBranch(ctx context.Context) {
	out, _, err := g.runner.Run(ctx, g.cloneDir, "git", "symbolic-ref", "refs/remotes/origin/HEAD")
	if err != nil {
		return
	}
	// Output is like "refs/remotes/origin/master"
	ref := strings.TrimSpace(string(out))
	if branch, ok := strings.CutPrefix(ref, "refs/remotes/origin/"); ok {
		g.defaultBranchMu.Lock()
		g.defaultBranch = branch
		g.defaultBranchMu.Unlock()
	}
}

func (g *GitManager) EnsureRepoCloned(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if _, err := os.Stat(filepath.Join(g.cloneDir, ".git")); err == nil {
		// Verify the base repo is not corrupted
		_, _, revParseErr := g.runner.Run(ctx, g.cloneDir, "git", "rev-parse", "HEAD")
		if revParseErr != nil {
			// Base repo is corrupted — nuke and re-clone
			os.RemoveAll(g.cloneDir) //nolint:errcheck // best-effort
			// Fall through to the clone path below
		} else {
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
func (g *GitManager) SetGitIdentity(name, email string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.gitAuthorName = name
	g.gitAuthorEmail = email
}

// configureGitIdentity sets local git user.name/user.email in the given directory
// and disables commit signing to avoid using the host's GPG/SSH keys.
func (g *GitManager) configureGitIdentity(ctx context.Context, dir string) {
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
func (g *GitManager) ensureForkRemote(ctx context.Context) {
	if g.forkURL == "" || g.forkURL == g.repoURL {
		return
	}
	// Add fork remote (ignore error if it already exists)
	g.runner.Run(ctx, g.cloneDir, "git", "remote", "add", "fork", g.forkURL) //nolint:errcheck // best-effort
}

// PushRemote returns the git remote name to push to ("fork" or "origin").
func (g *GitManager) PushRemote() string {
	if g.forkURL != "" && g.forkURL != g.repoURL {
		return "fork"
	}
	return "origin"
}

func (g *GitManager) CreateWorktree(ctx context.Context, branchName string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	worktreePath := filepath.Join(g.cloneDir, "worktrees", branchName)

	// Reuse existing worktree if it's still healthy
	if g.isWorktreeHealthy(ctx, worktreePath) {
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

func (g *GitManager) SyncWorktree(ctx context.Context, worktreePath string) error {
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
	_, stderr, err = g.runner.Run(ctx, worktreePath, "git", "pull", "--rebase", pushRemote, branch)
	if err != nil {
		if strings.Contains(strings.ToLower(string(stderr)), "couldn't find remote ref") {
			// Branch doesn't exist on the push remote yet (never pushed, or
			// deleted after merge) — nothing to sync.
			return nil
		}
		// The pull can conflict (worktrees are created from the origin default
		// branch, so a PR branch based on an older default conflicts at sync
		// time) or trip over leftover local state from an interrupted run.
		// Swallowing the error here used to hand downstream flows a
		// rebase-in-progress worktree. Instead, abort the rebase and hard-reset
		// to the remote branch: the remote is the source of truth for a PR
		// branch, and conflict handling needs the worktree at that state to
		// reproduce the conflict in the right direction (branch onto default).
		g.runner.Run(ctx, worktreePath, "git", "rebase", "--abort") //nolint:errcheck // best-effort
		_, resetStderr, resetErr := g.runner.Run(ctx, worktreePath, "git", "reset", "--hard", pushRemote+"/"+branch)
		if resetErr != nil {
			return fmt.Errorf("git pull --rebase %s %s: %w (stderr: %s); recovery reset failed (stderr: %s)",
				pushRemote, branch, err, string(stderr), string(resetStderr))
		}
	}
	return nil
}

func (g *GitManager) RemoveWorktree(ctx context.Context, worktreePath string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	_, stderr, err := g.runner.Run(ctx, g.cloneDir, "git", "worktree", "remove", "--force", worktreePath)
	if err != nil {
		return fmt.Errorf("git worktree remove: %w (stderr: %s)", err, string(stderr))
	}
	return nil
}

// isWorktreeHealthy checks whether a worktree directory is usable.
// A healthy worktree has a .git file (not directory) and passes `git status`.
func (g *GitManager) isWorktreeHealthy(ctx context.Context, worktreePath string) bool {
	// Check .git file exists (worktrees have a .git FILE, not directory)
	gitPath := filepath.Join(worktreePath, ".git")
	info, err := os.Stat(gitPath)
	if err != nil || info.IsDir() {
		return false // missing or is a directory (should be a file for worktrees)
	}

	// Check git status works
	_, _, err = g.runner.Run(ctx, worktreePath, "git", "status")
	return err == nil
}
