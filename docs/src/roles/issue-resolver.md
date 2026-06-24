# Issue Resolver

The issue resolver is oompa's primary role: it picks up GitHub issues, implements fixes using a coding agent, and opens pull requests.

## How It Works

1. Oompa scans the repository for issues with the configured label (default: `good-for-ai`)
2. For each new issue, it creates a git worktree on a branch named `ai/issue-<number>`
3. The coding agent is invoked with a prompt containing the issue title, body, and project conventions
4. After the agent finishes, oompa pushes the branch and creates a pull request
5. The PR links back to the original issue

## Configuration

**CLI:**

```bash
./oompa \
  --repo myorg/myrepo \
  --label good-for-ai \
  --agent opencode \
  --agent-model google-vertex-anthropic/claude-opus-4-6@default
```

**YAML config file:**

```yaml
projects:
  - repo: myorg/myrepo
    issues:
      - label: good-for-ai
        only-assigned: true
        reviewers: [alice, bob]
```

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--label` | `good-for-ai` | Issue label to watch |
| `--only-assigned` | `false` | Only process issues assigned to the agent user |
| `--reviewers` | all | Allowlist of reviewers to respond to on created PRs |

## Lifecycle

Once a PR is created, the issue resolver also handles:

- **Review comments** -- reviewer feedback is addressed by the coding agent
- **CI failures** -- failing checks are investigated and fixed
- **Merge conflicts** -- automatic rebase with conflict resolution

These behaviors are controlled by `--reactions`. See [Features](../features/ci-investigation.md) for details.

## Failure Handling

If the coding agent fails to implement a fix:

1. The issue is labeled `ai-failed`
2. A comment is posted explaining the error
3. The issue is skipped on subsequent poll cycles

To retry: remove the `ai-failed` label and re-add `good-for-ai`.

## Skipping Already-Tracked Issues

Issues already in oompa's state are skipped. If a PR was created but its number wasn't recorded (e.g., due to a crash), oompa re-checks for the PR on the next cycle.
