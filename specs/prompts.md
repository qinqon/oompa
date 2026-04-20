# Prompt Templates

## `buildImplementationPrompt(issue Issue, signedOffBy string) string`

Tells Claude to:
- Read Claude.md for conventions
- Implement the fix, run `make lint` and `make test`
- Commit (no trailing period, 72 char body)
- If signedOffBy is non-empty, add `Signed-off-by:` to every commit message
- Do NOT push, create PRs, or amend — the agent handles that automatically

## `buildReviewResponsePrompt(work IssueWork, comments []ReviewComment, signedOffBy string) string`

Tells Claude to:
- For each review comment: implement if valid, push back with explanation if not
- Always reply to every comment, even when implementing the suggestion
- Reply using `gh pr review` or `gh api`
- Run lint/test, commit, push
- No force-push
- If signedOffBy is non-empty, add `Signed-off-by:` to every commit message

## `buildCIFixPrompt(work IssueWork, failures []CheckRun, diff string, commits []Commit, signedOffBy string) string`

Tells Claude to:
- First investigate whether CI failures are directly caused by PR changes
- If UNRELATED: output starts with "UNRELATED" and an explanation
- If RELATED: output starts with "RELATED" and Claude fixes the code
- For multi-commit PRs: create fixup commits targeting the commit that introduced the issue
- For single-commit PRs: stage changes but do NOT commit (agent amends)
- Run lint/test to verify the fix
- If signedOffBy is non-empty, add `Signed-off-by:` to every commit message
- Do NOT push or rebase — the agent handles that automatically

## `buildConflictResolutionPrompt(work IssueWork, originDefaultBranch string, signedOffBy string) string`

Tells Claude to:
- Fetch latest changes and rebase on top of the main branch
- Resolve conflicts WITHIN the rebase flow (not by creating new commits)
- After resolving conflicts in files, run `git add <resolved-files>` and `git rebase --continue`
- Repeat for each conflicting commit until rebase completes
- Do NOT run `git rebase --abort` or create standalone commits
- The original commit structure must be preserved
- Run lint/test to verify the resolved code still works
- If signedOffBy is non-empty, add `Signed-off-by:` to every commit message
- Do NOT push — the agent handles that automatically

## Tests (`prompt_test.go`)

- `TestBuildImplementationPrompt` -- verifies issue number, title, body are interpolated; verifies push/PR instructions are absent
- `TestBuildReviewResponsePrompt` -- verifies each comment's file/line/body is included
