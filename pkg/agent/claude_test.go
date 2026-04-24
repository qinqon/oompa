package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

type commandCall struct {
	WorkDir string
	Name    string
	Args    []string
}

type mockCommandRunner struct {
	mu             sync.Mutex
	calls          []commandCall
	stdout         []byte
	stderr         []byte
	err            error
	claudeResults  [][]byte
	claudeIndex    int
}

func (m *mockCommandRunner) Run(_ context.Context, workDir, name string, args ...string) (stdout, stderr []byte, err error) {
	m.mu.Lock()
	m.calls = append(m.calls, commandCall{WorkDir: workDir, Name: name, Args: args})
	stdout = m.stdout
	if name == "claude" && len(m.claudeResults) > 0 {
		if m.claudeIndex < len(m.claudeResults) {
			stdout = m.claudeResults[m.claudeIndex]
		} else {
			stdout = m.claudeResults[len(m.claudeResults)-1]
		}
		m.claudeIndex++
	}
	stderr, err = m.stderr, m.err
	m.mu.Unlock()
	return stdout, stderr, err
}

// streamResultJSON builds a stream-json result line for testing.
func streamResultJSON(r AgentResult) []byte {
	event := streamEvent{
		Type:    "result",
		Subtype: "success",
		Result:  r.Result,
		CostUSD: r.CostUSD,
	}
	data, _ := json.Marshal(event)
	return append(data, '\n')
}

func TestClaudeCodeAgent_Success(t *testing.T) {
	expected := AgentResult{Result: "Fixed the bug", CostUSD: 0.05}

	runner := &mockCommandRunner{stdout: streamResultJSON(expected)}
	agent := &ClaudeCodeAgent{}

	result, err := agent.Run(context.Background(), runner, "/tmp/work", "fix the bug", nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Result != expected.Result {
		t.Errorf("expected result %q, got %q", expected.Result, result.Result)
	}
	if result.CostUSD != expected.CostUSD {
		t.Errorf("expected cost %f, got %f", expected.CostUSD, result.CostUSD)
	}
}

func TestClaudeCodeAgent_Failure(t *testing.T) {
	runner := &mockCommandRunner{
		err:    &mockError{msg: "exit status 1"},
		stderr: []byte("something went wrong"),
	}
	agent := &ClaudeCodeAgent{}

	_, err := agent.Run(context.Background(), runner, "/tmp/work", "fix the bug", nil, false)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "claude invocation failed") {
		t.Errorf("expected wrapped error, got: %v", err)
	}
}

func TestClaudeCodeAgent_RequiredFlags(t *testing.T) {
	runner := &mockCommandRunner{stdout: streamResultJSON(AgentResult{Result: "ok"})}
	agent := &ClaudeCodeAgent{}

	_, err := agent.Run(context.Background(), runner, "/tmp/work", "fix", nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(runner.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(runner.calls))
	}

	call := runner.calls[0]
	if call.Name != "claude" {
		t.Errorf("expected command 'claude', got %q", call.Name)
	}

	// Verify args include the required flags
	args := strings.Join(call.Args, " ")
	for _, want := range []string{"-p", "--verbose", "--output-format", "stream-json", "--dangerously-skip-permissions"} {
		if !strings.Contains(args, want) {
			t.Errorf("args missing %q: %v", want, call.Args)
		}
	}
}

func TestClaudeCodeAgent_ResumePassesContinue(t *testing.T) {
	runner := &mockCommandRunner{stdout: streamResultJSON(AgentResult{Result: "ok"})}
	agent := &ClaudeCodeAgent{}

	_, err := agent.Run(context.Background(), runner, "/tmp/work", "fix", nil, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	args := strings.Join(runner.calls[0].Args, " ")
	if !strings.Contains(args, "--continue") {
		t.Errorf("expected --continue flag when resume=true, got: %v", runner.calls[0].Args)
	}
}

func TestClaudeCodeAgent_NoResumeOmitsContinue(t *testing.T) {
	runner := &mockCommandRunner{stdout: streamResultJSON(AgentResult{Result: "ok"})}
	agent := &ClaudeCodeAgent{}

	_, err := agent.Run(context.Background(), runner, "/tmp/work", "fix", nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	args := strings.Join(runner.calls[0].Args, " ")
	if strings.Contains(args, "--continue") {
		t.Errorf("expected no --continue flag when resume=false, got: %v", runner.calls[0].Args)
	}
}

func TestClaudeCodeAgent_InvalidJSON(t *testing.T) {
	runner := &mockCommandRunner{stdout: []byte("not json at all")}
	agent := &ClaudeCodeAgent{}

	_, err := agent.Run(context.Background(), runner, "/tmp/work", "fix", nil, false)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if !strings.Contains(err.Error(), "no result event found") {
		t.Errorf("expected stream parse error, got: %v", err)
	}
}

type mockError struct {
	msg string
}

func (e *mockError) Error() string { return e.msg }
