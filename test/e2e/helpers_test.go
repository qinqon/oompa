package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// writeFile creates a file in the given directory with the specified content.
func writeFile(t interface{ Fatalf(string, ...any) }, dir, name, content string) {
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write file %s: %v", path, err)
	}
}

// newGitCommand creates an exec.Command for a git operation.
func newGitCommand(args ...string) *exec.Cmd {
	return exec.Command("git", args...)
}

// gitEnv returns the environment variables for isolated git operations.
func gitEnv(h *Harness) []string {
	return append(os.Environ(),
		fmt.Sprintf("GIT_CONFIG_GLOBAL=%s", filepath.Join(h.HomeDir(), ".gitconfig")),
		fmt.Sprintf("HOME=%s", h.HomeDir()),
		"GIT_CONFIG_NOSYSTEM=1",
	)
}

// findMarkerFile searches for a named marker file in the directory tree.
func findMarkerFile(t *testing.T, dir, name string) string {
	t.Helper()
	var found string
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.Name() == name {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

// bareBranchSHA returns the commit SHA of a branch in the bare repo.
func bareBranchSHA(t *testing.T, h *Harness, branchName string) string {
	t.Helper()
	cmd := newGitCommand("--git-dir", h.BareRepo(), "rev-parse", "refs/heads/"+branchName)
	cmd.Env = gitEnv(h)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rev-parse %s failed: %v\n%s", branchName, err, out)
	}
	return strings.TrimSpace(string(out))
}
