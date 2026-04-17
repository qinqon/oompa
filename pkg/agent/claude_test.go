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
	mu     sync.Mutex
	calls  []commandCall
	stdout []byte
	stderr []byte
	err    error
}

func (m *mockCommandRunner) Run(_ context.Context, workDir string, name string, args ...string) ([]byte, []byte, error) {
	m.mu.Lock()
	m.calls = append(m.calls, commandCall{WorkDir: workDir, Name: name, Args: args})
	stdout, stderr, err := m.stdout, m.stderr, m.err
	m.mu.Unlock()
	return stdout, stderr, err
}

// streamResultJSON builds a stream-json result line for testing.
func streamResultJSON(r ClaudeResult) []byte {
	event := streamEvent{
		Type:    "result",
		Subtype: "success",
		Result:  r.Result,
		CostUSD: r.Cost,
	}
	data, _ := json.Marshal(event)
	return append(data, '\n')
}

func TestRunClaude_Success(t *testing.T) {
	expected := ClaudeResult{Result: "Fixed the bug", Cost: 0.05}

	runner := &mockCommandRunner{stdout: streamResultJSON(expected)}
	cfg := Config{VertexRegion: "us-east5", VertexProject: "my-project"}

	result, err := runClaude(context.Background(), runner, "/tmp/work", "fix the bug", cfg, nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Result != expected.Result {
		t.Errorf("expected result %q, got %q", expected.Result, result.Result)
	}
	if result.Cost != expected.Cost {
		t.Errorf("expected cost %f, got %f", expected.Cost, result.Cost)
	}
}

func TestRunClaude_Failure(t *testing.T) {
	runner := &mockCommandRunner{
		err:    &mockError{msg: "exit status 1"},
		stderr: []byte("something went wrong"),
	}
	cfg := Config{VertexRegion: "us-east5", VertexProject: "my-project"}

	_, err := runClaude(context.Background(), runner, "/tmp/work", "fix the bug", cfg, nil, false)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "claude invocation failed") {
		t.Errorf("expected wrapped error, got: %v", err)
	}
}

func TestRunClaude_VertexEnvVars(t *testing.T) {
	runner := &mockCommandRunner{stdout: streamResultJSON(ClaudeResult{Result: "ok"})}
	cfg := Config{VertexRegion: "us-east5", VertexProject: "my-project"}

	_, err := runClaude(context.Background(), runner, "/tmp/work", "fix", cfg, nil, false)
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

func TestRunClaude_ResumePassesContinue(t *testing.T) {
	runner := &mockCommandRunner{stdout: streamResultJSON(ClaudeResult{Result: "ok"})}
	cfg := Config{VertexRegion: "us-east5", VertexProject: "my-project"}

	_, err := runClaude(context.Background(), runner, "/tmp/work", "fix", cfg, nil, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	args := strings.Join(runner.calls[0].Args, " ")
	if !strings.Contains(args, "--continue") {
		t.Errorf("expected --continue flag when resume=true, got: %v", runner.calls[0].Args)
	}
}

func TestRunClaude_NoResumeOmitsContinue(t *testing.T) {
	runner := &mockCommandRunner{stdout: streamResultJSON(ClaudeResult{Result: "ok"})}
	cfg := Config{VertexRegion: "us-east5", VertexProject: "my-project"}

	_, err := runClaude(context.Background(), runner, "/tmp/work", "fix", cfg, nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	args := strings.Join(runner.calls[0].Args, " ")
	if strings.Contains(args, "--continue") {
		t.Errorf("expected no --continue flag when resume=false, got: %v", runner.calls[0].Args)
	}
}

func TestRunClaude_InvalidJSON(t *testing.T) {
	runner := &mockCommandRunner{stdout: []byte("not json at all")}
	cfg := Config{VertexRegion: "us-east5", VertexProject: "my-project"}

	_, err := runClaude(context.Background(), runner, "/tmp/work", "fix", cfg, nil, false)
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
