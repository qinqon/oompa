package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type commandCall struct {
	WorkDir string
	Name    string
	Args    []string
}

type mockCommandRunner struct {
	calls  []commandCall
	stdout []byte
	stderr []byte
	err    error
}

func (m *mockCommandRunner) Run(_ context.Context, workDir string, name string, args ...string) ([]byte, []byte, error) {
	m.calls = append(m.calls, commandCall{WorkDir: workDir, Name: name, Args: args})
	return m.stdout, m.stderr, m.err
}

func TestRunClaude_Success(t *testing.T) {
	expected := ClaudeResult{Result: "Fixed the bug", Cost: 0.05}
	data, _ := json.Marshal(expected)

	runner := &mockCommandRunner{stdout: data}
	cfg := Config{VertexRegion: "us-east5", VertexProject: "my-project"}

	result, err := runClaude(context.Background(), runner, "/tmp/work", "fix the bug", cfg)
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

	_, err := runClaude(context.Background(), runner, "/tmp/work", "fix the bug", cfg)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "claude invocation failed") {
		t.Errorf("expected wrapped error, got: %v", err)
	}
}

func TestRunClaude_VertexEnvVars(t *testing.T) {
	data, _ := json.Marshal(ClaudeResult{Result: "ok"})
	runner := &mockCommandRunner{stdout: data}
	cfg := Config{VertexRegion: "us-east5", VertexProject: "my-project"}

	_, err := runClaude(context.Background(), runner, "/tmp/work", "fix", cfg)
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
	for _, want := range []string{"-p", "--output-format", "json", "--dangerously-skip-permissions"} {
		if !strings.Contains(args, want) {
			t.Errorf("args missing %q: %v", want, call.Args)
		}
	}
}

func TestRunClaude_InvalidJSON(t *testing.T) {
	runner := &mockCommandRunner{stdout: []byte("not json at all")}
	cfg := Config{VertexRegion: "us-east5", VertexProject: "my-project"}

	_, err := runClaude(context.Background(), runner, "/tmp/work", "fix", cfg)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if !strings.Contains(err.Error(), "failed to parse claude output") {
		t.Errorf("expected parse error, got: %v", err)
	}
}

type mockError struct {
	msg string
}

func (e *mockError) Error() string { return e.msg }
