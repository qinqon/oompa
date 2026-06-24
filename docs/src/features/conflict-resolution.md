# Conflict Resolution

Oompa detects merge conflicts on tracked PRs and resolves them automatically.

## How It Works

1. On each poll cycle, oompa checks if tracked PRs have merge conflicts
2. First, an automatic `git rebase` is attempted
3. If the rebase has conflicts, the coding agent is invoked to resolve them
4. The agent resolves conflicts within the rebase flow (not by creating new commits)
5. After resolution, oompa force-pushes with `--force-with-lease`

## Resolution Strategy

The coding agent follows a strict resolution protocol:

1. Fetch latest changes and rebase onto the default branch
2. For each conflicting commit:
   - Resolve conflicts in the affected files
   - Run `git add <resolved-files>`
   - Run `git rebase --continue`
3. The original commit structure is preserved
4. Lint and tests are run to verify the resolved code

The agent never runs `git rebase --abort` or creates standalone merge commits.

## Configuration

Enable conflict resolution with the `conflicts` reaction:

```bash
./oompa --repo myorg/myrepo --watch-prs 123 --reactions conflicts
```

To suppress conflict-related PR comments:

```bash
./oompa --repo myorg/myrepo --watch-prs 123 --reactions conflicts \
  --skip-comment conflict
```

## Rebase Interval

To avoid excessive rebasing, use the `rebase-interval` setting (default: 4 hours):

```yaml
projects:
  - repo: myorg/myrepo
    rebase-interval: 24h
    prs:
      - watch: [123]
        reactions: [conflicts, rebase]
```

## Safety

- Force-pushes use `--force-with-lease` only for history-rewriting operations
- The original commit structure is preserved -- no squashing during conflict resolution
- If resolution fails, the issue is logged and skipped until the next cycle
