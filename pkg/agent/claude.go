package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
)

// CommandRunner executes external commands.
type CommandRunner interface {
	Run(ctx context.Context, workDir string, name string, args ...string) (stdout []byte, stderr []byte, err error)
}

// ExecRunner is the concrete CommandRunner using os/exec.
type ExecRunner struct {
	// Env holds additional environment variables to set on commands.
	Env []string
}

func (r *ExecRunner) Run(ctx context.Context, workDir string, name string, args ...string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = workDir
	if len(r.Env) > 0 {
		cmd.Env = append(cmd.Environ(), r.Env...)
	}

	stdout, err := cmd.Output()
	var stderr []byte
	if exitErr, ok := err.(*exec.ExitError); ok {
		stderr = exitErr.Stderr
	}
	return stdout, stderr, err
}

// runClaude invokes Claude CLI in headless mode and parses the result.
func runClaude(ctx context.Context, runner CommandRunner, workDir, prompt string, cfg Config) (ClaudeResult, error) {
	// Set Vertex env vars by wrapping the runner call with env-setting command
	if execRunner, ok := runner.(*ExecRunner); ok {
		execRunner.Env = append(execRunner.Env,
			"CLAUDE_CODE_USE_VERTEX=1",
			fmt.Sprintf("CLOUD_ML_REGION=%s", cfg.VertexRegion),
			fmt.Sprintf("ANTHROPIC_VERTEX_PROJECT_ID=%s", cfg.VertexProject),
		)
	}

	stdout, stderr, err := runner.Run(ctx, workDir,
		"claude", "-p", "--output-format", "json", "--dangerously-skip-permissions", prompt,
	)
	if err != nil {
		return ClaudeResult{}, fmt.Errorf("claude invocation failed: %w (stderr: %s)", err, string(stderr))
	}

	var result ClaudeResult
	if err := json.Unmarshal(stdout, &result); err != nil {
		return ClaudeResult{}, fmt.Errorf("failed to parse claude output: %w (stdout: %s)", err, string(stdout))
	}

	return result, nil
}
