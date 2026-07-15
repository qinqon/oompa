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

// cliAgentSpec describes how to invoke and parse one CLI coding agent backend.
// The invocation flow is identical for every backend (stream when possible,
// pipe the prompt via stdin, salvage cost on failure); only the binary, its
// arguments, the per-line log decoder, and the result parser differ.
type cliAgentSpec struct {
	binary  string
	args    []string
	logLine func(logger *slog.Logger, line []byte)
	parse   func(stdout []byte) (AgentResult, error)
}

// runCLIAgent invokes a CLI coding agent backend described by spec.
// The prompt is passed via stdin to avoid hitting the OS ARG_MAX limit for
// large prompts. Output is streamed line-by-line to the logger when the
// runner supports it. On invocation failure the partial output is still
// parsed so any cost it reports can be billed against session budgets
// alongside the error (cost-only: result text never propagates on failure).
func runCLIAgent(ctx context.Context, runner CommandRunner, workDir, prompt string,
	logger *slog.Logger, spec cliAgentSpec) (AgentResult, error) {
	var stdout, stderr []byte
	var err error

	if sr, ok := runner.(StreamingRunner); ok && logger != nil {
		stdout, stderr, err = sr.RunStreamWithStdin(ctx, workDir, prompt, func(line []byte) {
			spec.logLine(logger, line)
		}, spec.binary, spec.args...)
	} else {
		stdout, stderr, err = runner.RunWithStdin(ctx, workDir, prompt, spec.binary, spec.args...)
	}

	if err != nil {
		// Best-effort cost salvage: failed runs still bill the tokens they
		// consumed before dying.
		salvaged, _ := spec.parse(stdout)
		return AgentResult{CostUSD: salvaged.CostUSD}, fmt.Errorf("%s invocation failed: %w (stderr: %s)", spec.binary, err, string(stderr))
	}

	return spec.parse(stdout)
}
