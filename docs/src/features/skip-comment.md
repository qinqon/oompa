# Skip Comment

The `--skip-comment` flag allows you to suppress specific categories of PR comments that oompa would normally post.

## Motivation

In some workflows, certain comment types add noise without actionable value. For example, when babysitting PRs on a large project, you might not want a comment every time an unrelated CI failure is detected.

## Usage

**CLI:**

```bash
./oompa --repo myorg/myrepo --skip-comment ci-unrelated,ci-infrastructure
```

**YAML config file:**

```yaml
projects:
  - repo: myorg/myrepo
    prs:
      - watch: [123]
        skip-comment: [ci-unrelated, ci-infrastructure]
```

## Categories

| Category | Description |
|----------|-------------|
| `ci-unrelated` | CI failures unrelated to PR changes |
| `ci-infrastructure` | CI infrastructure failures (network, OOM, timeouts) |
| `ci-related` | CI failures related to PR changes |
| `conflict` | Merge conflict notifications |
| `rebase` | Rebase notifications |
| `flaky` | Flaky test notifications |
| `issue-in-progress` | Notifications about issue processing in progress |

## Behavior

When a comment category is suppressed:

- The underlying action still happens (e.g., CI is still investigated, conflicts are still resolved)
- Only the PR comment is skipped
- Structured logs still record the event

This means you get the benefits of automation without the PR comment noise.
