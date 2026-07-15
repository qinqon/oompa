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
// The prompt is passed via stdin to avoid hitting the OS ARG_MAX limit for large prompts.
// On invocation failure the partial stream output is still parsed so any cost
// it reports can be billed against session budgets alongside the error.
func (c *ClaudeCodeAgent) Run(ctx context.Context, runner CommandRunner, workDir, prompt string,
	logger *slog.Logger, resume bool) (AgentResult, error) {
	args := []string{"-p", "--verbose", "--output-format", "stream-json", "--dangerously-skip-permissions"}
	if resume {
		args = append(args, "--continue")
	}

	var stdout, stderr []byte
	var err error

	if sr, ok := runner.(StreamingRunner); ok && logger != nil {
		stdout, stderr, err = sr.RunStreamWithStdin(ctx, workDir, prompt, func(line []byte) {
			logStreamEvent(logger, line)
		}, "claude", args...)
	} else {
		stdout, stderr, err = runner.RunWithStdin(ctx, workDir, prompt, "claude", args...)
	}

	if err != nil {
		// Best-effort cost salvage: a run that failed after emitting its
		// result event still consumed tokens.
		salvaged, _ := parseStreamResult(stdout)
		return AgentResult{CostUSD: salvaged.CostUSD}, fmt.Errorf("claude invocation failed: %w (stderr: %s)", err, string(stderr))
	}

	return parseStreamResult(stdout)
}
