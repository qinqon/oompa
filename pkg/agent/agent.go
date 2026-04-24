package agent

import (
	"context"
	"fmt"
	"log/slog"
)

// CodeAgent abstracts the CLI coding agent (Claude Code or OpenCode).
type CodeAgent interface {
	Run(ctx context.Context, runner CommandRunner, workDir, prompt string,
		logger *slog.Logger, resume bool) (AgentResult, error)
}

// ClaudeCodeAgent implements CodeAgent for Claude Code CLI.
type ClaudeCodeAgent struct{}

// Run invokes Claude Code in headless mode with streaming output and parses the result.
// If resume is true, --continue is passed to resume the most recent session in workDir.
func (c *ClaudeCodeAgent) Run(ctx context.Context, runner CommandRunner, workDir, prompt string,
	logger *slog.Logger, resume bool) (AgentResult, error) {
	args := []string{"-p", "--verbose", "--output-format", "stream-json", "--dangerously-skip-permissions"}
	if resume {
		args = append(args, "--continue")
	}
	args = append(args, prompt)

	var stdout, stderr []byte
	var err error

	if sr, ok := runner.(StreamingRunner); ok && logger != nil {
		stdout, stderr, err = sr.RunStream(ctx, workDir, func(line []byte) {
			logStreamEvent(logger, line)
		}, "claude", args...)
	} else {
		stdout, stderr, err = runner.Run(ctx, workDir, "claude", args...)
	}

	if err != nil {
		return AgentResult{}, fmt.Errorf("claude invocation failed: %w (stderr: %s)", err, string(stderr))
	}

	return parseStreamResult(stdout)
}
