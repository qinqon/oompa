# Quickstart

This guide walks you through your first oompa run in single-repo mode.

## 1. Authenticate

```bash
gh auth login
gh auth setup-git
gcloud auth application-default login
```

## 2. Build

```bash
go build -o oompa ./cmd/oompa
```

## 3. Label an Issue

On your GitHub repository, add the `good-for-ai` label to an issue you want oompa to pick up. The issue should contain a clear description of the bug or feature to implement.

## 4. Run

**With OpenCode (recommended):**

```bash
./oompa \
  --agent opencode \
  --agent-model google-vertex-anthropic/claude-opus-4-6@default \
  --repo myorg/myrepo
```

**With Claude Code:**

```bash
export CLAUDE_CODE_USE_VERTEX=1
export CLOUD_ML_REGION="us-east5"
export ANTHROPIC_VERTEX_PROJECT_ID="my-gcp-project"

./oompa --repo myorg/myrepo
```

## 5. What Happens

Oompa will:

1. Clone the repository to `/tmp/oompa-work` (configurable with `--clone-dir`)
2. Scan for issues with the `good-for-ai` label
3. Create a git worktree on a branch named `ai/issue-<number>`
4. Invoke the coding agent to implement the fix
5. Push the branch and create a pull request
6. Continue polling every 2 minutes for new issues, review comments, CI failures, and merge conflicts

## 6. Dry Run

To see what oompa would do without making changes:

```bash
./oompa --repo myorg/myrepo --dry-run --one-shot
```

`--dry-run` logs actions without executing them. `--one-shot` runs a single poll cycle and exits.

## Next Steps

- [CLI Flags](../configuration/cli-flags.md) -- full flag reference
- [Config File](../configuration/config-file.md) -- multi-project configuration
- [Roles](../roles/issue-resolver.md) -- different operating modes
