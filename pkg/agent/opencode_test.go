package agent

import (
	"context"
	"strings"
	"testing"
)

// TestOpenCodeAgent_ParseResult_Success tests parsing a successful OpenCode output.
func TestOpenCodeAgent_ParseResult_Success(t *testing.T) {
	// Simulate OpenCode JSONL output with text events and final step_finish
	stdout := `{"type":"text","timestamp":1767036064273,"sessionID":"ses_XXX","part":{"type":"text","text":"Hello! How can I help?"}}
{"type":"step_finish","timestamp":1767036064400,"sessionID":"ses_XXX","part":{"type":"step-finish","reason":"stop","cost":0.001,"tokens":{"input":671,"output":8,"reasoning":0,"cache":{"read":21415,"write":0}}}}
`
	result, err := parseOpencodeResult([]byte(stdout))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Result != "Hello! How can I help?" {
		t.Errorf("expected result %q, got %q", "Hello! How can I help?", result.Result)
	}
	if result.CostUSD != 0.001 {
		t.Errorf("expected cost 0.001, got %f", result.CostUSD)
	}
}

// TestOpenCodeAgent_ParseResult_MultipleTextEvents tests accumulating text from multiple events.
func TestOpenCodeAgent_ParseResult_MultipleTextEvents(t *testing.T) {
	stdout := `{"type":"text","timestamp":1767036064100,"sessionID":"ses_XXX","part":{"type":"text","text":"First line"}}
{"type":"text","timestamp":1767036064200,"sessionID":"ses_XXX","part":{"type":"text","text":"Second line"}}
{"type":"text","timestamp":1767036064300,"sessionID":"ses_XXX","part":{"type":"text","text":"Final output"}}
{"type":"step_finish","timestamp":1767036064400,"sessionID":"ses_XXX","part":{"type":"step-finish","reason":"stop","cost":0.002}}
`
	result, err := parseOpencodeResult([]byte(stdout))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should use the last text event
	if result.Result != "Final output" {
		t.Errorf("expected result %q, got %q", "Final output", result.Result)
	}
	if result.CostUSD != 0.002 {
		t.Errorf("expected cost 0.002, got %f", result.CostUSD)
	}
}

// TestOpenCodeAgent_ParseResult_MultipleCosts tests summing costs from multiple step_finish events.
func TestOpenCodeAgent_ParseResult_MultipleCosts(t *testing.T) {
	stdout := `{"type":"step_finish","timestamp":1767036064100,"sessionID":"ses_XXX","part":{"type":"step-finish","reason":"tool-calls","cost":0.001}}
{"type":"step_finish","timestamp":1767036064200,"sessionID":"ses_XXX","part":{"type":"step-finish","reason":"tool-calls","cost":0.002}}
{"type":"text","timestamp":1767036064300,"sessionID":"ses_XXX","part":{"type":"text","text":"Done"}}
{"type":"step_finish","timestamp":1767036064400,"sessionID":"ses_XXX","part":{"type":"step-finish","reason":"stop","cost":0.003}}
`
	result, err := parseOpencodeResult([]byte(stdout))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should sum all costs: 0.001 + 0.002 + 0.003 = 0.006
	expected := 0.006
	if result.CostUSD != expected {
		t.Errorf("expected total cost %f, got %f", expected, result.CostUSD)
	}
}

// TestOpenCodeAgent_ParseResult_NoFinalStep tests error when no final step_finish is found.
func TestOpenCodeAgent_ParseResult_NoFinalStep(t *testing.T) {
	// Missing step_finish with reason="stop"
	stdout := `{"type":"text","timestamp":1767036064273,"sessionID":"ses_XXX","part":{"type":"text","text":"Hello"}}
{"type":"step_finish","timestamp":1767036064400,"sessionID":"ses_XXX","part":{"type":"step-finish","reason":"tool-calls","cost":0.001}}
`
	_, err := parseOpencodeResult([]byte(stdout))
	if err == nil {
		t.Fatal("expected error for missing final step")
	}
	if !strings.Contains(err.Error(), "no final step_finish event (reason=stop) found") {
		t.Errorf("expected specific error message, got: %v", err)
	}
}

// TestOpenCodeAgent_ParseResult_ErrorEvent tests handling of error events.
func TestOpenCodeAgent_ParseResult_ErrorEvent(t *testing.T) {
	stdout := `{"type":"text","timestamp":1767036064273,"sessionID":"ses_XXX","part":{"type":"text","text":"Starting"}}
{"type":"error","timestamp":1767036064400,"sessionID":"ses_XXX","error":{"name":"CommandFailed","data":{"message":"command not found"}}}
`
	_, err := parseOpencodeResult([]byte(stdout))
	if err == nil {
		t.Fatal("expected error from error event")
	}
	if !strings.Contains(err.Error(), "opencode error: command not found") {
		t.Errorf("expected error message with details, got: %v", err)
	}
}

// TestOpenCodeAgent_ParseResult_EmptyOutput tests handling of empty output.
func TestOpenCodeAgent_ParseResult_EmptyOutput(t *testing.T) {
	_, err := parseOpencodeResult([]byte(""))
	if err == nil {
		t.Fatal("expected error for empty output")
	}
	if !strings.Contains(err.Error(), "no final step_finish event") {
		t.Errorf("expected specific error message, got: %v", err)
	}
}

// TestOpenCodeAgent_ParseResult_InvalidJSON tests handling of malformed JSON.
func TestOpenCodeAgent_ParseResult_InvalidJSON(t *testing.T) {
	stdout := `not valid json
{"type":"step_finish","timestamp":1767036064400,"sessionID":"ses_XXX","part":{"type":"step-finish","reason":"stop","cost":0.001}}
`
	// Should skip invalid lines and still parse valid ones
	result, err := parseOpencodeResult([]byte(stdout))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.CostUSD != 0.001 {
		t.Errorf("expected cost 0.001, got %f", result.CostUSD)
	}
}

// TestOpenCodeAgent_Run_Success tests the full Run method with mock runner.
func TestOpenCodeAgent_Run_Success(t *testing.T) {
	stdout := `{"type":"text","timestamp":1767036064273,"sessionID":"ses_XXX","part":{"type":"text","text":"Task completed"}}
{"type":"step_finish","timestamp":1767036064400,"sessionID":"ses_XXX","part":{"type":"step-finish","reason":"stop","cost":0.005}}
`
	runner := &mockCommandRunner{stdout: []byte(stdout)}
	agent := &OpenCodeAgent{Model: "anthropic/claude-sonnet-4"}

	result, err := agent.Run(context.Background(), runner, "/tmp/work", "do something", nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Result != "Task completed" {
		t.Errorf("expected result %q, got %q", "Task completed", result.Result)
	}
	if result.CostUSD != 0.005 {
		t.Errorf("expected cost 0.005, got %f", result.CostUSD)
	}

	// Verify correct command was called
	if len(runner.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(runner.calls))
	}
	call := runner.calls[0]
	if call.Name != "opencode" {
		t.Errorf("expected command 'opencode', got %q", call.Name)
	}

	// Verify args include required flags and model
	args := strings.Join(call.Args, " ")
	for _, want := range []string{"run", "--format", "json", "--dangerously-skip-permissions", "--model", "anthropic/claude-sonnet-4"} {
		if !strings.Contains(args, want) {
			t.Errorf("args missing %q: %v", want, call.Args)
		}
	}
}

// TestOpenCodeAgent_Run_WithResume tests the --continue flag is passed when resume=true.
func TestOpenCodeAgent_Run_WithResume(t *testing.T) {
	stdout := `{"type":"step_finish","timestamp":1767036064400,"sessionID":"ses_XXX","part":{"type":"step-finish","reason":"stop","cost":0.001}}
`
	runner := &mockCommandRunner{stdout: []byte(stdout)}
	agent := &OpenCodeAgent{}

	_, err := agent.Run(context.Background(), runner, "/tmp/work", "continue work", nil, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	args := strings.Join(runner.calls[0].Args, " ")
	if !strings.Contains(args, "--continue") {
		t.Errorf("expected --continue flag when resume=true, got: %v", runner.calls[0].Args)
	}
}

// TestOpenCodeAgent_Run_Failure tests handling of command execution failures.
func TestOpenCodeAgent_Run_Failure(t *testing.T) {
	runner := &mockCommandRunner{
		err:    &mockError{msg: "exit status 1"},
		stderr: []byte("opencode: command not found"),
	}
	agent := &OpenCodeAgent{}

	_, err := agent.Run(context.Background(), runner, "/tmp/work", "task", nil, false)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "opencode invocation failed") {
		t.Errorf("expected wrapped error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "command not found") {
		t.Errorf("expected stderr in error message, got: %v", err)
	}
}
