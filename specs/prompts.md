# Prompt Templates

## `buildImplementationPrompt(issue Issue, signedOffBy string) string`

Tells Claude to:
- Read Claude.md for conventions
- Implement the fix, run `make lint` and `make test`
- Commit (no trailing period, 72 char body)
- If signedOffBy is non-empty, add `Signed-off-by:` to every commit message
- Do NOT push, create PRs, or amend — the agent handles that automatically

## `buildReviewResponsePrompt(work IssueWork, comments []ReviewComment, reviews []PRReview, owner, repo, signedOffBy string) string`

Tells Claude to:
- For each review comment: implement if valid, push back with explanation if not
- Always reply to every comment, even when implementing the suggestion
- Reply using `gh pr review` or `gh api`
- Run lint/test, commit, push
- No force-push
- If signedOffBy is non-empty, add `Signed-off-by:` to every commit message

## `buildCIFixPrompt(work IssueWork, failures []CheckRun, diff string, commits []Commit, signedOffBy string) string`

Tells Claude to:
- Investigate whether CI failures are DIRECTLY related to PR changes
- If UNRELATED: do not fix, output starts with "UNRELATED"
- If RELATED: output starts with "RELATED", fix the code
- Run `make lint` and `make test` to verify
- If multiple commits: create fixup commit targeting the commit that introduced the issue
- If single commit: stage changes but do NOT commit (agent handles squashing)
- If signedOffBy is non-empty, add `Signed-off-by:` to every commit message
- Do NOT push or rebase — the agent handles that automatically

## `buildConflictResolutionPrompt(work IssueWork, originDefaultBranch, signedOffBy string) string`

Tells Claude to:
- Fetch latest changes from origin
- Rebase on top of the default branch (e.g., `origin/main`)
- Resolve any merge conflicts that arise
- Keep the PR's functionality intact while incorporating upstream changes
- Run `make lint` and `make test` to verify the resolved code still works
- If signedOffBy is non-empty, add `Signed-off-by:` to every commit message
- Do NOT push — the agent handles that automatically

## Tests (`prompt_test.go`)

- `TestBuildImplementationPrompt` -- verifies issue number, title, body are interpolated; verifies push/PR instructions are absent
- `TestBuildReviewResponsePrompt` -- verifies each comment's file/line/body is included
