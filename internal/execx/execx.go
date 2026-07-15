// Package execx provides external command execution with optional
// line-streaming, used to run git and coding-agent CLIs.
package execx

import (
	"bufio"
	"bytes"
	"context"
	"errors"
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
	const ghTokenKey = "GH_TOKEN="
	for i, env := range r.Env {
		if strings.HasPrefix(env, ghTokenKey) {
			r.Env[i] = ghTokenKey + token
			return
		}
	}
	r.Env = append(r.Env, ghTokenKey+token)
}

// command builds an exec.Cmd with the runner's env overlay applied.
// Runner-level variables are appended after the process environment;
// os/exec documents last-wins semantics for duplicate keys, so overlay
// values (e.g. refreshed GH_TOKEN) shadow inherited ones.
func (r *ExecRunner) command(ctx context.Context, workDir, stdin, name string, args ...string) *exec.Cmd {
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
	return cmd
}

func (r *ExecRunner) Run(ctx context.Context, workDir, name string, args ...string) (stdout, stderr []byte, err error) {
	return r.RunWithStdin(ctx, workDir, "", name, args...)
}

func (r *ExecRunner) RunWithStdin(ctx context.Context, workDir, stdin, name string, args ...string) (stdout, stderr []byte, err error) {
	cmd := r.command(ctx, workDir, stdin, name, args...)
	stdout, err = cmd.Output()
	if exitErr, ok := err.(*exec.ExitError); ok {
		stderr = exitErr.Stderr
	}
	return stdout, stderr, err
}

func (r *ExecRunner) RunStreamWithStdin(ctx context.Context, workDir, stdin string, onLine func(line []byte), name string, args ...string) (stdout, stderr []byte, err error) {
	cmd := r.command(ctx, workDir, stdin, name, args...)

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

	// Close the pipe before Wait: if the scanner stopped early (e.g. a line
	// exceeded the buffer cap), unread output could block the child forever,
	// and Wait must not be called until reads complete or the pipe is closed.
	// After a normal EOF this is a harmless no-op.
	pipe.Close() //nolint:errcheck,gosec // best-effort unblock before Wait

	// Always call Wait to release child process resources and avoid zombies.
	waitErr := cmd.Wait()

	// Join both errors when present: the scanner error describes the read
	// failure, the wait error preserves the process exit status.
	if scanErr != nil {
		return stdoutBuf.Bytes(), stderrBuf.Bytes(), errors.Join(scanErr, waitErr)
	}
	return stdoutBuf.Bytes(), stderrBuf.Bytes(), waitErr
}
