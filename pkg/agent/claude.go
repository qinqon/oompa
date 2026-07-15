package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"unicode/utf8"
)

// ClaudeCodeAgent implements CodeAgent for Claude Code CLI.
type ClaudeCodeAgent struct{}

// Run invokes Claude Code in headless mode with streaming output and parses
// the result. If resume is true, --continue resumes the most recent session
// in workDir. See runCLIAgent for the shared invocation semantics.
func (c *ClaudeCodeAgent) Run(ctx context.Context, runner CommandRunner, workDir, prompt string,
	logger *slog.Logger, resume bool) (AgentResult, error) {
	args := []string{"-p", "--verbose", "--output-format", "stream-json", "--dangerously-skip-permissions"}
	if resume {
		args = append(args, "--continue")
	}
	return runCLIAgent(ctx, runner, workDir, prompt, logger, cliAgentSpec{
		binary:  "claude",
		args:    args,
		logLine: logStreamEvent,
		parse:   parseStreamResult,
	})
}

// streamEvent represents a single event in Claude's stream-json output.
type streamEvent struct {
	Type    string         `json:"type"`
	Message *streamMessage `json:"message,omitempty"`
	// Result fields (only present when Type == "result")
	Result       string  `json:"result,omitempty"`
	CostUSD      float64 `json:"cost_usd,omitempty"`
	TotalCostUSD float64 `json:"total_cost_usd,omitempty"`
	NumTurns     int     `json:"num_turns,omitempty"`
	DurationMs   int64   `json:"duration_ms,omitempty"`
}

type streamMessage struct {
	Content []streamContent `json:"content"`
}

type streamContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	Name string `json:"name,omitempty"` // for tool_use
}

// logPreviewLen caps agent text output in debug logs.
const logPreviewLen = 200

// previewText truncates s for log output without splitting a UTF-8 sequence.
func previewText(s string) string {
	if len(s) <= logPreviewLen {
		return s
	}
	cut := logPreviewLen
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "..."
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
				logger.Debug("claude", "text", previewText(c.Text))
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
