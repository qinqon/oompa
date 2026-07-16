# Oompa: Autonomous Code Maintenance Agent

## Build & Test

```bash
go build -o oompa ./cmd/oompa
go test ./...
make fmt    # gofmt/goimports via golangci-lint fmt — run before committing
```

## Package Layout

- `cmd/oompa` — CLI entry point, flag binding, polling loop, TUI.
- `pkg/agent` — the agent core: reactions (`Process*`), state, config, prompts, coding-agent backends.
- `internal/execx` — external command execution (`CommandRunner`, `ExecRunner`, streaming).
- `internal/gh` — GitHub REST client (`Client` interface, `RESTClient`, etag cache, dry-run wrapper, App auth) and the GitHub domain types.
- `internal/worktree` — clone and per-branch git worktree management (`Manager`, `GitManager`).
- `internal/events` — observability event model and the Unix-socket server/client behind status/TUI.
- `internal/slack` — Slack webhook reporter (`Reporter`, `Finding`) with cross-restart dedup state.
- `pkg/agent` re-exports the internal names it consumes via type aliases and wrapper funcs, so `cmd/oompa` and most agent code keep a single import surface.
- `test/e2e` — binary-level tests against a fake GitHub HTTP server.

## Single-File Verification

Verify a single file or package without running the full test suite:

```bash
# Type-check a single package
go vet ./pkg/agent/...

# Run tests for a single file (by test name pattern)
go test ./pkg/agent/ -run TestProcessNewIssues -v

# Lint a single file
golangci-lint run pkg/agent/loop.go

# Lint an entire package
golangci-lint run ./pkg/agent/...

# Static analysis on a single package
staticcheck ./pkg/agent/...
```

## Common Patterns

### Adding a new reaction type (e.g. `ProcessFoo`)

Follow the pattern in `pkg/agent/ci.go` — see `ProcessCIFailures` for a complete example:

1. Add the method to `Agent` in a new file (e.g. `pkg/agent/foo.go`):
   ```go
   func (a *Agent) ProcessFoo(ctx context.Context) { ... }
   ```
2. Gate it with `a.ShouldRunReaction("foo")` at the top.
3. Add the call to the main polling loop in `cmd/oompa/main.go`, after the existing `Process*` calls.
4. Add the reaction name to the `--reactions` flag documentation.
5. Write tests in a matching `pkg/agent/foo_test.go` (see Testing Conventions below).

### Adding a new GitHub API method

See `internal/gh/client.go` for reference implementations of paginated API calls:

1. Add the method signature to the `Client` interface in `internal/gh/client.go`.
2. Implement it on `*RESTClient` in the same file.
3. Add the mock implementation to `mockGitHubClient` in `pkg/agent/mocks_test.go`.
4. Add a no-op or pass-through implementation to `DryRunClient` in `internal/gh/dryrun.go` (no-op for mutating methods, pass-through for reads).
5. Write a test using `httptest.NewServer` in `internal/gh/client_test.go`.

### Adding a new tracked state field

Use `IssueWork` in `pkg/agent/types.go` as a template for new tracked fields:

1. Add any new fields to `IssueWork` in `pkg/agent/types.go`.
2. State is never persisted to disk — it is rebuilt from GitHub on startup by `BuildStateFromGitHub` in `pkg/agent/state.go`. If the new field must survive restarts, add recovery logic there (see `recoverCommentCursors` for an example) and cover it in `pkg/agent/state_test.go`.
3. Update the relevant `Process*` method to populate the new field.

### Adding a new CLI flag

Based on `pkg/agent/config.go` for the Config struct pattern:

1. Add the field to `Config` in `pkg/agent/config.go`.
2. Add the flag binding in `cmd/oompa/main.go` (flag + env var fallback).
3. Document it in the README flags table.

## Testing Conventions

Shared helpers live in `pkg/agent/mocks_test.go` — use them instead of hand-rolling setup:

- `newTestAgent(gh, runner, wt, opts...)` builds an Agent through the production `NewAgent` constructor with canonical owner/repo config and a discard logger. Customize with `withCfg(func(*Config))` and `withCodeAgent(ca)`. Never construct `&Agent{...}` literals in tests except for pure-function tests that only need `cfg`.
- `trackWork(agent, ...func(*IssueWork))` registers the canonical in-flight fixture (issue 42 → PR 100 on branch `ai/issue-42`, status pr-open); pass mutators for scenario deltas only.
- `countCalls(runner.calls, "claude")` counts invocations of a binary; `discardLogger()` for quiet loggers.
- `mockGitHubClient` is data-driven (set fields, no function stubs); `mockCommandRunner` records calls and can script claude output via `claudeResults`.
- Near-identical one-scenario tests belong in a table (`tests := []struct{...}` + `t.Run`) with a doc comment naming the behavioral surface; apply the strictest common assertion set to every row. Keep genuinely different flows as standalone functions — do not force-fit.

## Design Invariants

- The agent chose polling instead of webhooks because it avoids inbound connectivity requirements and runs behind firewalls.
- Sequential processing is a precondition for correctness: one issue at a time prevents race conditions on shared git state.
- The coding agent never merges — this invariant ensures a human reviews every change before it reaches main.
- The trade-off of stateless-on-disk design is a small startup cost from API calls, but eliminates state file corruption and migration concerns.
