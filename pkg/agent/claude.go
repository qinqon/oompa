package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
)

const (
	// scannerInitBufSize is the initial buffer size for the bufio.Scanner
	// used when streaming command output (64 KB).
	scannerInitBufSize = 64 * 1024
	// scannerMaxBufSize is the maximum buffer size the scanner will grow to
	// when reading long lines from command output (10 MB).
	scannerMaxBufSize = 10 * 1024 * 1024
)

// CommandRunner executes external commands.
type CommandRunner interface {
	Run(ctx context.Context, workDir string, name string, args ...string) (stdout []byte, stderr []byte, err error)
	RunWithStdin(ctx context.Context, workDir string, stdin string, name string, args ...string) (stdout []byte, stderr []byte, err error)
}

// StreamingRunner extends CommandRunner with line-by-line stdout streaming.
type StreamingRunner interface {
	CommandRunner
	RunStreamWithStdin(ctx context.Context, workDir string, stdin string, onLine func(line []byte), name string, args ...string) (stdout []byte, stderr []byte, err error)
}

// ExecRunner is the concrete CommandRunner using os/exec.
type ExecRunner struct {
	// Env holds additional environment variables to set on commands.
	Env []string
	mu  sync.RWMutex // protects Env
}

// SetGHToken updates the GH_TOKEN environment variable in a thread-safe manner.
func (r *ExecRunner) SetGHToken(token string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Update existing GH_TOKEN or append if not found
	ghTokenKey := "GH_TOKEN="
	found := false
	for i, env := range r.Env {
		if len(env) >= len(ghTokenKey) && env[:len(ghTokenKey)] == ghTokenKey {
			r.Env[i] = ghTokenKey + token
			found = true
			break
		}
	}
	if !found {
		r.Env = append(r.Env, ghTokenKey+token)
	}
}

func (r *ExecRunner) Run(ctx context.Context, workDir, name string, args ...string) (stdout, stderr []byte, err error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = workDir
	r.mu.RLock()
	if len(r.Env) > 0 {
		cmd.Env = append(cmd.Environ(), r.Env...)
	}
	r.mu.RUnlock()

	stdout, err = cmd.Output()
	if exitErr, ok := err.(*exec.ExitError); ok {
		stderr = exitErr.Stderr
	}
	return stdout, stderr, err
}

func (r *ExecRunner) RunWithStdin(ctx context.Context, workDir, stdin, name string, args ...string) (stdout, stderr []byte, err error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = workDir
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	r.mu.RLock()
	if len(r.Env) > 0 {
		cmd.Env = append(cmd.Environ(), r.Env...)
	}
	r.mu.RUnlock()

	stdout, err = cmd.Output()
	if exitErr, ok := err.(*exec.ExitError); ok {
		stderr = exitErr.Stderr
	}
	return stdout, stderr, err
}

func (r *ExecRunner) RunStreamWithStdin(ctx context.Context, workDir, stdin string, onLine func(line []byte), name string, args ...string) (stdout, stderr []byte, err error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = workDir
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	r.mu.RLock()
	if len(r.Env) > 0 {
		cmd.Env = append(cmd.Environ(), r.Env...)
	}
	r.mu.RUnlock()

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
	scanner.Buffer(make([]byte, 0, scannerInitBufSize), scannerMaxBufSize)
	for scanner.Scan() {
		line := scanner.Bytes()
		stdoutBuf.Write(line)
		stdoutBuf.WriteByte('\n')
		if onLine != nil {
			onLine(append([]byte{}, line...))
		}
	}
	scanErr := scanner.Err()

	// Always call Wait to release child process resources and avoid zombies.
	waitErr := cmd.Wait()

	// Prefer the scanner error if present -- it describes the read failure.
	if scanErr != nil {
		return stdoutBuf.Bytes(), stderrBuf.Bytes(), scanErr
	}
	return stdoutBuf.Bytes(), stderrBuf.Bytes(), waitErr
}

// streamEvent represents a single event in Claude's stream-json output.
type streamEvent struct {
	Type    string         `json:"type"`
	Subtype string         `json:"subtype,omitempty"`
	Message *streamMessage `json:"message,omitempty"`
	// Result fields (only present when Type == "result")
	Result        string  `json:"result,omitempty"`
	CostUSD       float64 `json:"cost_usd,omitempty"`
	TotalCostUSD  float64 `json:"total_cost_usd,omitempty"`
	NumTurns      int     `json:"num_turns,omitempty"`
	DurationMs    int64   `json:"duration_ms,omitempty"`
	DurationAPIMs int64   `json:"duration_api_ms,omitempty"`
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
				logger.Debug("claude using tool", "tool", c.Name)
			case "text":
				text := c.Text
				if len(text) > 200 {
					text = text[:200] + "..."
				}
				logger.Debug("claude", "text", text)
			}
		}
	case "result":
		cost := event.CostUSD
		if cost == 0 {
			cost = event.TotalCostUSD
		}
		logger.Info("claude finished", "cost_usd", cost, "duration_ms", event.DurationMs, "turns", event.NumTurns)
	}
}

// parseStreamResult extracts the final AgentResult from stream-json output.
func parseStreamResult(stdout []byte) (AgentResult, error) {
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
			cost := event.CostUSD
			if cost == 0 {
				cost = event.TotalCostUSD
			}
			return AgentResult{
				Result:  event.Result,
				CostUSD: cost,
			}, nil
		}
	}
	return AgentResult{}, fmt.Errorf("no result event found in stream output (stdout: %s)", string(stdout))
}

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
