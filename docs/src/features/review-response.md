# Review Response

Oompa monitors tracked PRs for new review comments and uses the coding agent to address them.

## How It Works

1. On each poll cycle, oompa fetches new review comments on tracked PRs
2. Comments are filtered through the reviewers allowlist
3. An `:eyes:` reaction is added to each comment before processing
4. The coding agent evaluates each comment independently using the `/ce-resolve-pr-feedback` skill
5. For valid issues: code changes are made (left uncommitted for oompa to handle)
6. For invalid suggestions: a reply is posted with specific rationale
7. Addressed review threads are resolved via GraphQL

## Reviewer Allowlist

By default, oompa responds to all reviewers. Use `--reviewers` to restrict to specific users:

```bash
./oompa --repo myorg/myrepo --reviewers alice,bob,coderabbitai[bot]
```

Bot comments (from oompa itself) are always filtered out to prevent self-reply loops.

## Comment Processing

Each comment is evaluated independently:

| Classification | Action |
|---------------|--------|
| Bug fix | Fix the code, post confirmation reply |
| Valid improvement | Implement the change, post confirmation reply |
| Incorrect | Decline with specific rationale |
| Style preference | Evaluate and decide per project conventions |

## Cursor Advancement

The comment cursor advances to the max ID of all fetched comments (including filtered ones). This prevents re-fetching bot-posted or already-replied comments.

Special cases:
- When changes were detected but push failed, the cursor does **not** advance (comments are retried next cycle)
- When no actionable comments remain after filtering, the cursor still advances past filtered items

## Configuration

Enable review response with the `reviews` reaction:

```bash
./oompa --repo myorg/myrepo --watch-prs 123 --reactions reviews
```

In YAML:

```yaml
projects:
  - repo: myorg/myrepo
    reviewers: [alice, bob]
    prs:
      - watch: [123]
        reactions: [reviews]
```
