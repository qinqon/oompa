package execx_test

import (
	"context"
	"strings"
	"testing"

	"github.com/qinqon/oompa/internal/execx"
)

func TestExecRunner_Run(t *testing.T) {
	r := &execx.ExecRunner{}
	stdout, stderr, err := r.Run(context.Background(), t.TempDir(), "sh", "-c", "echo out; echo err >&2")
	if err != nil {
		t.Fatalf("unexpected error: %v (stderr %q)", err, stderr)
	}
	if got := string(stdout); got != "out\n" {
		t.Errorf("stdout = %q, want %q", got, "out\n")
	}
	// Contract: stderr is only captured on failure (from exec.ExitError);
	// successful runs return empty stderr.
	if len(stderr) != 0 {
		t.Errorf("stderr = %q, want empty on success", stderr)
	}
}

func TestExecRunner_RunReturnsErrorWithStderr(t *testing.T) {
	r := &execx.ExecRunner{}
	_, stderr, err := r.Run(context.Background(), t.TempDir(), "sh", "-c", "echo boom >&2; exit 3")
	if err == nil {
		t.Fatal("expected error for nonzero exit")
	}
	if !strings.Contains(string(stderr), "boom") {
		t.Errorf("stderr = %q, want it to contain %q", stderr, "boom")
	}
}

func TestExecRunner_RunWithStdin(t *testing.T) {
	r := &execx.ExecRunner{}
	stdout, _, err := r.RunWithStdin(context.Background(), t.TempDir(), "hello stdin", "cat")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := string(stdout); got != "hello stdin" {
		t.Errorf("stdout = %q, want %q", got, "hello stdin")
	}
}

func TestExecRunner_EnvAndTokenVisibleToChild(t *testing.T) {
	r := &execx.ExecRunner{Env: []string{"OOMPA_TEST_VAR=abc"}}
	r.SetGHToken("tok123")
	stdout, _, err := r.Run(context.Background(), t.TempDir(), "sh", "-c", "printf '%s:%s' \"$OOMPA_TEST_VAR\" \"$GH_TOKEN\"")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := string(stdout); got != "abc:tok123" {
		t.Errorf("child env = %q, want %q", got, "abc:tok123")
	}
}

func TestExecRunner_RunStreamWithStdin(t *testing.T) {
	r := &execx.ExecRunner{}
	var lines []string
	stdout, _, err := r.RunStreamWithStdin(context.Background(), t.TempDir(), "",
		func(line []byte) { lines = append(lines, string(line)) },
		"sh", "-c", "echo one; echo two")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(lines) != 2 || lines[0] != "one" || lines[1] != "two" {
		t.Errorf("streamed lines = %v, want [one two]", lines)
	}
	if got := string(stdout); got != "one\ntwo\n" {
		t.Errorf("stdout = %q, want %q", got, "one\ntwo\n")
	}
}

func TestExecRunner_StreamHandlesLongLines(t *testing.T) {
	// A single line longer than the scanner's 64 KB initial buffer must
	// still be delivered whole (the scanner grows up to 10 MB).
	r := &execx.ExecRunner{}
	var lines []string
	stdout, stderr, err := r.RunStreamWithStdin(context.Background(), t.TempDir(), "",
		func(line []byte) { lines = append(lines, string(line)) },
		"sh", "-c", `head -c 100000 /dev/zero | tr '\0' 'x'; echo`)
	if err != nil {
		t.Fatalf("unexpected error: %v (stderr %q)", err, stderr)
	}
	if len(lines) != 1 || len(lines[0]) != 100000 {
		t.Fatalf("expected one 100000-char line, got %d lines (first %d chars)", len(lines), len(lines[0]))
	}
	if len(stdout) != 100001 { // line + newline
		t.Errorf("stdout length = %d, want 100001", len(stdout))
	}
}

func TestExecRunner_StreamReturnsErrorOnFailure(t *testing.T) {
	r := &execx.ExecRunner{}
	var lines []string
	_, stderr, err := r.RunStreamWithStdin(context.Background(), t.TempDir(), "",
		func(line []byte) { lines = append(lines, string(line)) },
		"sh", "-c", "echo partial; echo broken >&2; exit 1")
	if err == nil {
		t.Fatal("expected error for nonzero exit")
	}
	if len(lines) != 1 || lines[0] != "partial" {
		t.Errorf("streamed lines before failure = %v, want [partial]", lines)
	}
	if !strings.Contains(string(stderr), "broken") {
		t.Errorf("stderr = %q, want it to contain %q", stderr, "broken")
	}
}
