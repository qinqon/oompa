package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
)

// CommandRunner executes external commands.
type CommandRunner interface {
	Run(ctx context.Context, workDir string, name string, args ...string) (stdout []byte, stderr []byte, err error)
}

// StreamingRunner extends CommandRunner with line-by-line stdout streaming.
type StreamingRunner interface {
	CommandRunner
	RunStream(ctx context.Context, workDir string, onLine func(line []byte), name string, args ...string) (stdout []byte, stderr []byte, err error)
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

func (r *ExecRunner) RunStream(ctx context.Context, workDir string, onLine func(line []byte), name string, args ...string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = workDir
	if len(r.Env) > 0 {
		cmd.Env = append(cmd.Environ(), r.Env...)
	}

	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}

	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}

	var stdoutBuf bytes.Buffer
	scanner := bufio.NewScanner(pipe)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		stdoutBuf.Write(line)
		stdoutBuf.WriteByte('\n')
		if onLine != nil {
			onLine(append([]byte{}, line...))
		}
	}

	err = cmd.Wait()
	return stdoutBuf.Bytes(), stderrBuf.Bytes(), err
}

// streamEvent represents a single event in Claude's stream-json output.
type streamEvent struct {
	Type    string         `json:"type"`
	Subtype string         `json:"subtype,omitempty"`
	Message *streamMessage `json:"message,omitempty"`
	// Result fields (only present when Type == "result")
	Result     string  `json:"result,omitempty"`
	CostUSD    float64 `json:"cost_usd,omitempty"`
	NumTurns   int     `json:"num_turns,omitempty"`
	DurationMs int64   `json:"duration_ms,omitempty"`
}

type streamMessage struct {
	Content []streamContent `json:"content"`
}

type streamContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	Name string `json:"name,omitempty"` // for tool_use
}

// logStreamEvent logs a stream-json event at appropriate levels.
func logStreamEvent(logger *slog.Logger, line []byte) {
	var event streamEvent
	if err := json.Unmarshal(line, &event); err != nil {
		return
	}

	switch event.Type {
	case "assistant":
		if event.Message == nil {
			return
		}
		for _, c := range event.Message.Content {
			switch c.Type {
			case "tool_use":
				logger.Info("claude using tool", "tool", c.Name)
			case "text":
				text := c.Text
				if len(text) > 200 {
					text = text[:200] + "..."
				}
				logger.Debug("claude", "text", text)
			}
		}
	case "result":
		logger.Info("claude finished", "cost_usd", event.CostUSD, "duration_ms", event.DurationMs, "turns", event.NumTurns)
	}
}

// parseStreamResult extracts the final ClaudeResult from stream-json output.
func parseStreamResult(stdout []byte) (ClaudeResult, error) {
	lines := bytes.Split(stdout, []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		line := bytes.TrimSpace(lines[i])
		if len(line) == 0 {
			continue
		}
		var event streamEvent
		if err := json.Unmarshal(line, &event); err != nil {
			continue
		}
		if event.Type == "result" {
			return ClaudeResult{
				Result: event.Result,
				Cost:   event.CostUSD,
			}, nil
		}
	}
	return ClaudeResult{}, fmt.Errorf("no result event found in stream output (stdout: %s)", string(stdout))
}

// runClaude invokes Claude CLI in headless mode with streaming output and parses the result.
func runClaude(ctx context.Context, runner CommandRunner, workDir, prompt string, cfg Config, logger *slog.Logger) (ClaudeResult, error) {
	// Set Vertex env vars by wrapping the runner call with env-setting command
	if execRunner, ok := runner.(*ExecRunner); ok {
		execRunner.Env = append(execRunner.Env,
			"CLAUDE_CODE_USE_VERTEX=1",
			fmt.Sprintf("CLOUD_ML_REGION=%s", cfg.VertexRegion),
			fmt.Sprintf("ANTHROPIC_VERTEX_PROJECT_ID=%s", cfg.VertexProject),
		)
	}

	var stdout, stderr []byte
	var err error

	if sr, ok := runner.(StreamingRunner); ok && logger != nil {
		stdout, stderr, err = sr.RunStream(ctx, workDir, func(line []byte) {
			logStreamEvent(logger, line)
		}, "claude", "-p", "--output-format", "stream-json", "--dangerously-skip-permissions", prompt)
	} else {
		stdout, stderr, err = runner.Run(ctx, workDir,
			"claude", "-p", "--output-format", "stream-json", "--dangerously-skip-permissions", prompt,
		)
	}

	if err != nil {
		return ClaudeResult{}, fmt.Errorf("claude invocation failed: %w (stderr: %s)", err, string(stderr))
	}

	return parseStreamResult(stdout)
}
