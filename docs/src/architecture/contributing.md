# Contributing

Oompa uses spec-driven development. Every component has a corresponding specification that serves as the source of truth.

## Specs

| Spec | Drives |
|------|--------|
| `specs/architecture.md` | Overall structure, directory layout, main loop flow |
| `specs/state.md` | `pkg/agent/state.go` + tests |
| `specs/github-client.md` | `pkg/agent/github.go` + tests |
| `specs/claude-runner.md` | `pkg/agent/claude.go` + tests |
| `specs/worktree.md` | `pkg/agent/worktree.go` + tests |
| `specs/prompts.md` | `pkg/agent/prompt.go` + tests |
| `specs/loop.md` | `pkg/agent/loop.go` + tests |
| `specs/config.md` | `cmd/oompa/main.go` + `pkg/agent/config.go` |
| `specs/error-handling.md` | Error handling and safety constraints |
| `specs/testing.md` | Mock types, test strategy, verification |
| `specs/event.md` | Event system (emitter, server, client) |
| `specs/tui.md` | TUI dashboard |

## Rules

1. **Spec first**: Before writing or modifying any file, read its corresponding spec
2. **Interfaces match spec**: All interface definitions must match the spec exactly
3. **Tests match spec**: Every test listed in a spec must be implemented
4. **No unspecified features**: Do not add functionality not described in a spec
5. **Spec changes require discussion**: If a spec seems wrong, flag it -- do not silently deviate

## Build and Test

```bash
# Build
go build -o oompa ./cmd/oompa

# Run all tests
go test ./...

# Type-check a single package
go vet ./pkg/agent/...

# Run tests for a single file
go test ./pkg/agent/ -run TestProcessNewIssues -v

# Lint
golangci-lint run ./pkg/agent/...

# Static analysis
staticcheck ./pkg/agent/...

# Build documentation locally (requires mdBook)
# Install: https://rust-lang.github.io/mdBook/guide/installation.html
mdbook build docs/
mdbook serve docs/   # live preview at http://localhost:3000
```

## Adding a New Reaction

Follow the pattern in `pkg/agent/ci.go` (see `ProcessCIFailures`):

1. Add the method to `Agent` in a new file (e.g., `pkg/agent/foo.go`)
2. Gate it with `a.ShouldRunReaction("foo")` at the top
3. Add the call to the main polling loop in `cmd/oompa/main.go`
4. Add the reaction name to the `--reactions` flag documentation
5. Write tests in `pkg/agent/loop_test.go` using the mock interfaces

## Adding a New GitHub API Method

See `pkg/agent/github.go` for reference implementations:

1. Add the method signature to the `GitHubClient` interface
2. Implement it on `*GoGitHubClient`
3. Add the mock implementation to `MockGitHubClient` in `pkg/agent/loop_test.go`
4. Write a test using `httptest.NewServer` in `pkg/agent/github_test.go`

## Adding a New CLI Flag

1. Add the field to `Config` in `pkg/agent/config.go`
2. Add the flag binding in `cmd/oompa/main.go` (flag + env var fallback)
3. Document it in the CLI flags reference

## Implementation Order

When implementing from scratch, follow dependency order:

1. `go.mod` + `pkg/agent/types.go`
2. `pkg/agent/state.go` + tests
3. `pkg/agent/prompt.go` + tests
4. `pkg/agent/claude.go` + tests
5. `pkg/agent/github.go` + tests
6. `pkg/agent/worktree.go` + tests
7. `pkg/agent/loop.go` + tests
8. `cmd/oompa/main.go`
