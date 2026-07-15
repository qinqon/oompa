package agent

import (
	"fmt"

	"github.com/qinqon/oompa/internal/execx"
)

// Command execution lives in internal/execx; these aliases keep the
// package-local names used throughout the agent.
type (
	// CommandRunner executes external commands.
	CommandRunner = execx.CommandRunner
	// StreamingRunner extends CommandRunner with line-by-line stdout streaming.
	StreamingRunner = execx.StreamingRunner
	// ExecRunner runs commands via os/exec.
	ExecRunner = execx.ExecRunner
)

// BuildAgentEnv builds the environment variable slice for agent invocations.
// Only passes through git identity; other variables (like GH_TOKEN) are
// inherited from the system environment. This allows subprocesses to use
// system-level authentication or credential helpers (e.g. gh auth git-credential).
func BuildAgentEnv(cfg Config) []string {
	var env []string
	if cfg.GitAuthorName != "" {
		env = append(env,
			fmt.Sprintf("GIT_AUTHOR_NAME=%s", cfg.GitAuthorName),
			fmt.Sprintf("GIT_COMMITTER_NAME=%s", cfg.GitAuthorName),
		)
	}
	if cfg.GitAuthorEmail != "" {
		env = append(env,
			fmt.Sprintf("GIT_AUTHOR_EMAIL=%s", cfg.GitAuthorEmail),
			fmt.Sprintf("GIT_COMMITTER_EMAIL=%s", cfg.GitAuthorEmail),
		)
	}
	return env
}
