package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// oompaBinary is the path to the compiled oompa binary, built once in TestMain.
var oompaBinary string

// BuildOompa compiles the oompa binary into a temporary directory.
// Call this from TestMain. The binary is shared across all tests in the package.
func BuildOompa(m *testing.M) int {
	// Find the module root (two levels up from test/e2e/)
	_, thisFile, _, _ := runtime.Caller(0)
	moduleRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")

	tmpDir, err := os.MkdirTemp("", "oompa-e2e-bin-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create temp dir for binary: %v\n", err)
		return 1
	}
	defer os.RemoveAll(tmpDir)

	oompaBinary = filepath.Join(tmpDir, "oompa")
	cmd := exec.Command("go", "build", "-o", oompaBinary, "./cmd/oompa")
	cmd.Dir = moduleRoot
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to build oompa: %v\n", err)
		return 1
	}

	return m.Run()
}

// Harness holds all per-test temporary state for running oompa end-to-end.
type Harness struct {
	t        *testing.T
	tmpDir   string // root temp dir for this test
	bareRepo string // path to bare git repo (acts as "origin")
	homeDir  string // isolated HOME
	binDir   string // holds fake-claude
	cloneDir string // passed to oompa --clone-dir
	owner    string
	repo     string
	label    string
}

// NewHarness creates an isolated test environment with:
// - a bare git repo seeded with one commit on main
// - a fake claude script on PATH
// - an isolated HOME with a gitconfig using insteadOf to redirect GitHub URLs
func NewHarness(t *testing.T, owner, repo, label string) *Harness {
	t.Helper()

	tmpDir := t.TempDir()

	h := &Harness{
		t:        t,
		tmpDir:   tmpDir,
		bareRepo: filepath.Join(tmpDir, "bare.git"),
		homeDir:  filepath.Join(tmpDir, "home"),
		binDir:   filepath.Join(tmpDir, "bin"),
		cloneDir: filepath.Join(tmpDir, "clones"),
		owner:    owner,
		repo:     repo,
		label:    label,
	}

	// Create directories
	for _, dir := range []string{h.homeDir, h.binDir, h.cloneDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	h.seedBareRepo()
	h.installFakeClaude()
	h.writeGitConfig()

	return h
}

// seedBareRepo creates a bare git repo with one commit on main.
func (h *Harness) seedBareRepo() {
	h.t.Helper()

	// Create bare repo with main as the default branch
	h.run("", "git", "init", "--bare", "--initial-branch=main", h.bareRepo)

	// Create a scratch clone, add a commit, push to establish main
	scratch := filepath.Join(h.tmpDir, "scratch")
	h.run("", "git", "clone", h.bareRepo, scratch)
	h.run(scratch, "git", "config", "user.name", "e2e")
	h.run(scratch, "git", "config", "user.email", "e2e@example.com")
	h.run(scratch, "git", "checkout", "-b", "main")

	// Create an initial file and commit
	readmePath := filepath.Join(scratch, "README.md")
	if err := os.WriteFile(readmePath, []byte("# Test Repo\n"), 0o644); err != nil {
		h.t.Fatalf("write README: %v", err)
	}
	h.run(scratch, "git", "add", "README.md")
	h.run(scratch, "git", "commit", "-m", "initial commit")
	h.run(scratch, "git", "push", "-u", "origin", "main")
}

// installFakeClaude copies the fake-claude.sh script into the bin dir as "claude".
func (h *Harness) installFakeClaude() {
	h.t.Helper()
	h.InstallFakeClaudeScript("fake-claude.sh")
}

// InstallFakeClaudeScript installs a specific fake-claude script from testdata/ as "claude".
func (h *Harness) InstallFakeClaudeScript(scriptName string) {
	h.t.Helper()

	_, thisFile, _, _ := runtime.Caller(0)
	srcScript := filepath.Join(filepath.Dir(thisFile), "testdata", scriptName)

	data, err := os.ReadFile(srcScript)
	if err != nil {
		h.t.Fatalf("read %s: %v", scriptName, err)
	}

	dst := filepath.Join(h.binDir, "claude")
	if err := os.WriteFile(dst, data, 0o755); err != nil {
		h.t.Fatalf("write claude: %v", err)
	}
}

// writeGitConfig creates an isolated gitconfig that redirects GitHub URLs
// to the local bare repo via insteadOf.
func (h *Harness) writeGitConfig() {
	h.t.Helper()

	githubURL := fmt.Sprintf("https://github.com/%s/%s.git", h.owner, h.repo)

	config := fmt.Sprintf(`[url "file://%s"]
    insteadOf = %s
[user]
    name = e2e
    email = e2e@example.com
[init]
    defaultBranch = main
[safe]
    directory = *
[commit]
    gpgsign = false
[tag]
    gpgsign = false
`, h.bareRepo, githubURL)

	gitconfigPath := filepath.Join(h.homeDir, ".gitconfig")
	if err := os.WriteFile(gitconfigPath, []byte(config), 0o644); err != nil {
		h.t.Fatalf("write gitconfig: %v", err)
	}
}

// RunOompaOpts configures optional arguments for RunOompa.
type RunOompaOpts struct {
	ExtraArgs []string // additional CLI flags
	ExtraEnv  []string // additional environment variables
	WatchPRs  []int    // --watch-prs values
	Reactions []string // --reactions values
}

// RunOompa executes the oompa binary with the given FakeGitHub server URL.
// Returns stdout, stderr, and exit error (nil on success).
func (h *Harness) RunOompa(githubURL string, opts ...RunOompaOpts) (stdout, stderr string, err error) {
	h.t.Helper()

	args := []string{
		"--repo", h.owner + "/" + h.repo,
		"--label", h.label,
		"--github-user", h.owner,
		"--git-author-name", "e2e",
		"--git-author-email", "e2e@example.com",
		"--agent", "claudecode",
		"--one-shot",
		"--clone-dir", h.cloneDir,
		"--log-level", "debug",
	}

	env := append(os.Environ(),
		"GITHUB_TOKEN=fake-token",
		fmt.Sprintf("OOMPA_GITHUB_API_URL=%s", githubURL),
		fmt.Sprintf("GIT_CONFIG_GLOBAL=%s", filepath.Join(h.homeDir, ".gitconfig")),
		fmt.Sprintf("HOME=%s", h.homeDir),
		fmt.Sprintf("PATH=%s:%s", h.binDir, os.Getenv("PATH")),
		// Prevent git from reading system configs that might interfere
		"GIT_CONFIG_NOSYSTEM=1",
		// Isolate XDG state from host
		fmt.Sprintf("XDG_STATE_HOME=%s", filepath.Join(h.tmpDir, "xdg-state")),
		fmt.Sprintf("XDG_RUNTIME_DIR=%s", filepath.Join(h.tmpDir, "xdg-runtime")),
	)

	if len(opts) > 0 {
		opt := opts[0]
		if len(opt.WatchPRs) > 0 {
			var nums []string
			for _, n := range opt.WatchPRs {
				nums = append(nums, strconv.Itoa(n))
			}
			args = append(args, "--watch-prs", strings.Join(nums, ","))
		}
		if len(opt.Reactions) > 0 {
			args = append(args, "--reactions", strings.Join(opt.Reactions, ","))
		}
		args = append(args, opt.ExtraArgs...)
		env = append(env, opt.ExtraEnv...)
	}

	cmd := exec.Command(oompaBinary, args...)
	cmd.Env = env

	var stdoutBuf, stderrBuf strings.Builder
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err = cmd.Run()

	return stdoutBuf.String(), stderrBuf.String(), err
}

// CloneDir returns the clone directory path for external use (e.g., pre-creating corrupt worktrees).
func (h *Harness) CloneDir() string {
	return h.cloneDir
}

// TmpDir returns the root temporary directory for this test harness.
func (h *Harness) TmpDir() string {
	return h.tmpDir
}

// BareRepo returns the bare repo path for external manipulation.
func (h *Harness) BareRepo() string {
	return h.bareRepo
}

// HomeDir returns the isolated HOME directory.
func (h *Harness) HomeDir() string {
	return h.homeDir
}

// AddCommitToBareMain adds a commit to the bare repo's main branch.
// This simulates upstream activity on main.
func (h *Harness) AddCommitToBareMain(filename, content, message string) {
	h.t.Helper()

	scratch := filepath.Join(h.tmpDir, "scratch-diverge")
	// Clean up any previous scratch dir
	os.RemoveAll(scratch)
	h.run("", "git", "clone", h.bareRepo, scratch)
	h.run(scratch, "git", "config", "user.name", "e2e")
	h.run(scratch, "git", "config", "user.email", "e2e@example.com")
	h.run(scratch, "git", "checkout", "main")

	fpath := filepath.Join(scratch, filename)
	if err := os.WriteFile(fpath, []byte(content), 0o644); err != nil {
		h.t.Fatalf("write file %s: %v", filename, err)
	}
	h.run(scratch, "git", "add", filename)
	h.run(scratch, "git", "commit", "-m", message)
	h.run(scratch, "git", "push", "origin", "main")
}

// BareRepoHasBranch returns true if the bare repo contains the given branch.
func (h *Harness) BareRepoHasBranch(branch string) bool {
	h.t.Helper()
	cmd := exec.Command("git", "-C", h.bareRepo, "rev-parse", "--verify", branch)
	// Use the same isolated git config as run() to avoid safe.directory
	// or other host git config issues.
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("GIT_CONFIG_GLOBAL=%s", filepath.Join(h.homeDir, ".gitconfig")),
		fmt.Sprintf("HOME=%s", h.homeDir),
		"GIT_CONFIG_NOSYSTEM=1",
	)
	return cmd.Run() == nil
}

// run executes a command and fails the test on error.
func (h *Harness) run(dir, name string, args ...string) {
	h.t.Helper()
	cmd := exec.Command(name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	// Use isolated git config for all git operations
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("GIT_CONFIG_GLOBAL=%s", filepath.Join(h.homeDir, ".gitconfig")),
		fmt.Sprintf("HOME=%s", h.homeDir),
		"GIT_CONFIG_NOSYSTEM=1",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		h.t.Fatalf("%s %s failed: %v\n%s", name, strings.Join(args, " "), err, string(out))
	}
}
