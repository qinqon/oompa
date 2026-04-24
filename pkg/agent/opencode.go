package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
)

// OpenCodeAgent implements CodeAgent for OpenCode CLI.
type OpenCodeAgent struct {
	Model string // optional model override
}

// opencodeEvent represents a single event in OpenCode's JSONL output.
// OpenCode emits newline-delimited JSON with event-specific data nested under "part".
type opencodeEvent struct {
	Type      string        `json:"type"`
	Timestamp int64         `json:"timestamp"`
	SessionID string        `json:"sessionID"`
	Part      opencodePart  `json:"part"`
	Error     *opencodeError `json:"error,omitempty"`
}

// opencodePart contains event-specific data for OpenCode events.
type opencodePart struct {
	Type   string `json:"type"`
	Text   string `json:"text,omitempty"`
	Tool   string `json:"tool,omitempty"`
	Reason string `json:"reason,omitempty"` // "stop" or "tool-calls" for step_finish
	Cost   float64 `json:"cost,omitempty"`
	Tokens *opencodeTokens `json:"tokens,omitempty"`
	State  *opencodeToolState `json:"state,omitempty"`
	Time   *opencodeTime `json:"time,omitempty"`
}

// opencodeTokens contains token usage information.
type opencodeTokens struct {
	Input     int `json:"input"`
	Output    int `json:"output"`
	Reasoning int `json:"reasoning"`
	Cache     *struct {
		Read  int `json:"read"`
		Write int `json:"write"`
	} `json:"cache,omitempty"`
}

// opencodeToolState contains tool execution state.
type opencodeToolState struct {
	Status string `json:"status,omitempty"`
	Output string `json:"output,omitempty"`
	Input  map[string]interface{} `json:"input,omitempty"`
}

// opencodeTime contains timing information.
type opencodeTime struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
}

// opencodeError contains error information.
type opencodeError struct {
	Name string `json:"name,omitempty"`
	Data *struct {
		Message string `json:"message,omitempty"`
	} `json:"data,omitempty"`
}

// logOpencodeEvent logs an OpenCode JSON event at appropriate levels.
func logOpencodeEvent(logger *slog.Logger, line []byte) {
	var event opencodeEvent
	if err := json.Unmarshal(line, &event); err != nil {
		return
	}

	switch event.Type {
	case "tool_use":
		if event.Part.Tool != "" {
			status := ""
			if event.Part.State != nil {
				status = event.Part.State.Status
			}
			logger.Debug("opencode using tool", "tool", event.Part.Tool, "status", status)
		}
	case "text":
		text := event.Part.Text
		if len(text) > 200 {
			text = text[:200] + "..."
		}
		if text != "" {
			logger.Debug("opencode", "text", text)
		}
	case "step_finish":
		cost := event.Part.Cost
		inputTokens := 0
		outputTokens := 0
		if event.Part.Tokens != nil {
			inputTokens = event.Part.Tokens.Input
			outputTokens = event.Part.Tokens.Output
		}
		logger.Info("opencode step finished", "reason", event.Part.Reason, "cost_usd", cost, "input_tokens", inputTokens, "output_tokens", outputTokens)
	case "error":
		if event.Error != nil {
			msg := event.Error.Name
			if event.Error.Data != nil && event.Error.Data.Message != "" {
				msg = event.Error.Data.Message
			}
			logger.Error("opencode error", "error", msg)
		}
	}
}

// parseOpencodeResult extracts the final AgentResult from OpenCode's JSONL output.
// OpenCode outputs newline-delimited JSON events. The final result is indicated by
// a step_finish event with part.reason == "stop". Text is accumulated from text events,
// and costs are summed across all step_finish events.
func parseOpencodeResult(stdout []byte) (AgentResult, error) {
	lines := bytes.Split(stdout, []byte("\n"))

	var textParts []string
	var totalCost float64
	var foundFinalStep bool

	// Parse line-by-line JSONL
	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}

		var event opencodeEvent
		if err := json.Unmarshal(line, &event); err != nil {
			continue
		}

		switch event.Type {
		case "text":
			// Accumulate text from text events
			if event.Part.Text != "" {
				textParts = append(textParts, event.Part.Text)
			}
		case "step_finish":
			// Sum costs from all step_finish events
			if event.Part.Cost > 0 {
				totalCost += event.Part.Cost
			}
			// Check if this is the final step
			if event.Part.Reason == "stop" {
				foundFinalStep = true
			}
		case "error":
			// Return error if OpenCode reported one
			if event.Error != nil {
				msg := event.Error.Name
				if event.Error.Data != nil && event.Error.Data.Message != "" {
					msg = event.Error.Data.Message
				}
				return AgentResult{}, fmt.Errorf("opencode error: %s", msg)
			}
		}
	}

	if !foundFinalStep {
		return AgentResult{}, fmt.Errorf("no final step_finish event (reason=stop) found in OpenCode output (stdout: %s)", string(stdout))
	}

	// Join accumulated text
	result := ""
	if len(textParts) > 0 {
		result = textParts[len(textParts)-1] // Use the last text output
	}

	return AgentResult{
		Result:  result,
		CostUSD: totalCost,
	}, nil
}

// Run invokes OpenCode CLI with JSON output and parses the result.
// If resume is true, --continue is passed to resume the most recent session in workDir.
func (o *OpenCodeAgent) Run(ctx context.Context, runner CommandRunner, workDir, prompt string,
	logger *slog.Logger, resume bool) (AgentResult, error) {
	args := []string{"run", "--format", "json", "--dangerously-skip-permissions"}
	if resume {
		args = append(args, "--continue")
	}
	if o.Model != "" {
		args = append(args, "--model", o.Model)
	}
	args = append(args, prompt)

	var stdout, stderr []byte
	var err error

	if sr, ok := runner.(StreamingRunner); ok && logger != nil {
		stdout, stderr, err = sr.RunStream(ctx, workDir, func(line []byte) {
			logOpencodeEvent(logger, line)
		}, "opencode", args...)
	} else {
		stdout, stderr, err = runner.Run(ctx, workDir, "opencode", args...)
	}

	if err != nil {
		return AgentResult{}, fmt.Errorf("opencode invocation failed: %w (stderr: %s)", err, string(stderr))
	}

	return parseOpencodeResult(stdout)
}
