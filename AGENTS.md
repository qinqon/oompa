# Oompa: Autonomous Code Maintenance Agent

## Build & Test

```bash
go build -o oompa ./cmd/oompa
go test ./...
```

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
5. Write tests in `pkg/agent/loop_test.go` using the mock interfaces.

### Adding a new GitHub API method

See `pkg/agent/github.go` for reference implementations of paginated API calls:

1. Add the method signature to the `GitHubClient` interface in `pkg/agent/github.go`.
2. Implement it on `*GoGitHubClient` in the same file.
3. Add the mock implementation to `MockGitHubClient` in `pkg/agent/loop_test.go`.
4. Write a test using `httptest.NewServer` in `pkg/agent/github_test.go`.

### Adding a new event type to state tracking

Use `IssueWork` in `pkg/agent/types.go` as a template for new tracked fields:

1. Add any new fields to `IssueWork` in `pkg/agent/types.go`.
2. Ensure JSON serialization round-trips correctly — add a test in `pkg/agent/state_test.go`.
3. Update the relevant `Process*` method to populate the new field.

### Adding a new CLI flag

Based on `pkg/agent/config.go` for the Config struct pattern:

1. Add the field to `Config` in `pkg/agent/config.go`.
2. Add the flag binding in `cmd/oompa/main.go` (flag + env var fallback).
3. Document it in the README flags table.

## Design Invariants

- The agent chose polling instead of webhooks because it avoids inbound connectivity requirements and runs behind firewalls.
- Sequential processing is a precondition for correctness: one issue at a time prevents race conditions on shared git state.
- The coding agent never merges — this invariant ensures a human reviews every change before it reaches main.
- The trade-off of stateless-on-disk design is a small startup cost from API calls, but eliminates state file corruption and migration concerns.
