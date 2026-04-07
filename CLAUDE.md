# AI Agent: Automated GitHub Issue Resolution

## Spec-Driven Development

This project uses spec-driven development. **Read the relevant spec before implementing any file.**

### Specs

| Spec | Drives |
|------|--------|
| [specs/architecture.md](specs/architecture.md) | Overall structure, directory layout, main loop flow |
| [specs/state.md](specs/state.md) | `state.go` + `state_test.go` |
| [specs/github-client.md](specs/github-client.md) | `github.go` + `github_test.go` |
| [specs/claude-runner.md](specs/claude-runner.md) | `claude.go` + `claude_test.go` |
| [specs/worktree.md](specs/worktree.md) | `worktree.go` + `worktree_test.go` |
| [specs/prompts.md](specs/prompts.md) | `prompt.go` + `prompt_test.go` |
| [specs/loop.md](specs/loop.md) | `loop.go` + `loop_test.go` |
| [specs/config.md](specs/config.md) | `main.go` (Config struct, env/flag parsing) |
| [specs/error-handling.md](specs/error-handling.md) | Error handling and safety constraints |
| [specs/testing.md](specs/testing.md) | Mock types, test strategy, verification |

### Rules

1. **Spec first**: Before writing or modifying any file, read its corresponding spec. The spec is the source of truth.
2. **Interfaces match spec**: All interface definitions must match the signatures in the spec exactly.
3. **Tests match spec**: Every test listed in a spec must be implemented. Do not skip tests.
4. **No unspecified features**: Do not add functionality not described in a spec.
5. **Spec changes require discussion**: If a spec seems wrong or incomplete, flag it -- do not silently deviate.

## Build & Test

```bash
go build -o ai-agent .
go test ./...
```

## Implementation Order

Implement in dependency order:
1. `go.mod` + types (Issue, ReviewComment, PR, ClaudeResult)
2. `state.go` + `state_test.go`
3. `prompt.go` + `prompt_test.go`
4. `claude.go` + `claude_test.go`
5. `github.go` + `github_test.go`
6. `worktree.go` + `worktree_test.go`
7. `loop.go` + `loop_test.go`
8. `main.go`
