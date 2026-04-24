package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"sync"
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

func (r *ExecRunner) RunStream(ctx context.Context, workDir string, onLine func(line []byte), name string, args ...string) (stdout, stderr []byte, err error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = workDir
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
// Only passes through git identity and GitHub token; provider-specific vars
// are inherited from the system environment.
func BuildAgentEnv(cfg Config) []string {
	var env []string
	if cfg.GitHubToken != "" {
		env = append(env, fmt.Sprintf("GH_TOKEN=%s", cfg.GitHubToken))
	}
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
