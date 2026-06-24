# CI Investigation

Oompa detects failing CI checks on pull requests and uses the coding agent to investigate and fix them.

## How It Works

1. On each poll cycle, oompa checks for failing CI check runs on tracked PRs
2. The coding agent receives the failure details, the PR diff, and commit history
3. The agent classifies the failure as **related** or **unrelated** to the PR changes

### Related Failures

When the failure is caused by the PR:

- The agent analyzes the failure and implements a fix
- For **multi-commit PRs**: a fixup commit is created targeting the commit that introduced the issue
- For **single-commit PRs**: changes are staged but not committed (oompa amends the existing commit)
- Lint and tests are run to verify the fix

### Unrelated Failures

When the failure is not caused by the PR (flaky test, infrastructure issue):

- The agent outputs `UNRELATED` with an explanation
- A comment is posted on the PR (unless suppressed with `--skip-comment`)
- If `--create-flaky-issues` is enabled, an issue is created for the flaky test

## Classification Rules

The agent applies strict classification criteria:

- Failures in code the PR did not touch are treated as **unrelated**
- Changes to test expectations require verification that the new behavior is correct
- Minimal, targeted fixes are preferred over broad refactoring

## Configuration

Enable CI investigation with the `ci` reaction:

```bash
./oompa --repo myorg/myrepo --watch-prs 123 --reactions ci
```

Suppress comment categories:

```bash
./oompa --repo myorg/myrepo --watch-prs 123 --reactions ci \
  --skip-comment ci-unrelated,ci-infrastructure
```

## Comment Suppression

| Category | Description |
|----------|-------------|
| `ci-unrelated` | Suppress comments for failures unrelated to PR changes |
| `ci-infrastructure` | Suppress comments for infrastructure failures |
| `ci-related` | Suppress comments for failures related to PR changes |
