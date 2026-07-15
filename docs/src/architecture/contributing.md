# Contributing

Project conventions for AI agents and contributors live in `AGENTS.md` at the repository root.

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
