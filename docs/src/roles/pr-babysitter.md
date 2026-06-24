# PR Babysitter

The PR babysitter monitors specific pull requests for reviews, CI failures, merge conflicts, and rebase needs. Use this role when you have existing PRs that need ongoing maintenance.

## How It Works

1. Oompa bootstraps state for the specified PR numbers
2. On each poll cycle, it checks each PR for:
   - New review comments that need a response
   - CI failures that need investigation or fixing
   - Merge conflicts that need resolution
   - Rebase needs (outdated base branch)
3. For each detected issue, the appropriate reaction is triggered

## Configuration

**CLI:**

```bash
./oompa \
  --repo myorg/myrepo \
  --watch-prs 123,456 \
  --reactions ci,conflicts,rebase \
  --poll-interval 2m
```

**With a fork** (push branches to your fork instead of upstream):

```bash
./oompa \
  --repo upstream/repo \
  --fork myuser/repo \
  --watch-prs 123,456 \
  --reactions ci,conflicts,rebase
```

**YAML config file:**

```yaml
projects:
  - repo: myorg/myrepo
    prs:
      - watch: [123, 456]
        reactions: [ci, conflicts, rebase]
        skip-comment: [ci-unrelated, ci-infrastructure]
```

## Reactions

Control which maintenance tasks run with `--reactions`:

| Reaction | Description |
|----------|-------------|
| `reviews` | Respond to reviewer comments |
| `ci` | Investigate and fix CI failures |
| `conflicts` | Resolve merge conflicts |
| `rebase` | Rebase onto the latest base branch |

Default: all reactions are enabled. An empty list disables all reactions (useful for report-only mode).

## Bootstrap Behavior

When `--watch-prs` is set:

- `BootstrapWatchedPRs` runs instead of `ProcessNewIssues`
- Open PRs are added to state
- Merged or closed PRs are skipped
- Already-tracked PRs are not duplicated

## Cleanup

PRs that are merged or closed are automatically cleaned up:

- The associated worktree is removed
- The PR is removed from state
- This happens in the `CleanupDone` phase at the start of each poll cycle
