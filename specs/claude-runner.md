# Claude / Command Runner

Defines a `CommandRunner` interface for executing external commands (enables mocking in tests).

## Interface

```go
type CommandRunner interface {
    Run(ctx context.Context, workDir string, name string, args ...string) (stdout []byte, stderr []byte, err error)
}
```

## Concrete Implementation

`ExecRunner` uses `os/exec`.

## `runClaude` Function

```go
func runClaude(ctx context.Context, runner CommandRunner, workDir, prompt string, cfg Config) (ClaudeResult, error)
```

- Invokes: `claude -p --output-format json --dangerously-skip-permissions "prompt"`
- Sets Vertex env vars on the command: `CLAUDE_CODE_USE_VERTEX=1`, `CLOUD_ML_REGION`, `ANTHROPIC_VERTEX_PROJECT_ID`
- Parses JSON stdout into `ClaudeResult`

## Tests (`claude_test.go`)

Mock `CommandRunner`:

- `TestRunClaude_Success` -- mock runner returns valid JSON, verify parsed result
- `TestRunClaude_Failure` -- mock runner returns error, verify error wrapping
- `TestRunClaude_VertexEnvVars` -- verify the correct env vars are passed to the command
- `TestRunClaude_InvalidJSON` -- mock returns non-JSON stdout, verify error
