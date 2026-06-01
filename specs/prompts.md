# Prompt Templates

## `buildImplementationPrompt(issue Issue, signedOffBy string) string`

Tells Claude to:
- Read Claude.md for conventions
- Implement the fix, run `make lint` and `make test`
- Commit (no trailing period, 72 char body)
- If signedOffBy is non-empty, add `Signed-off-by:` to every commit message
- Do NOT push, create PRs, or amend — the agent handles that automatically

## `buildReviewTriagePrompt(work IssueWork, comments []ReviewComment, reviews []PRReview, owner, repo string) string`

Tells Claude to:
- Evaluate each review comment critically and produce a structured triage summary
- For each comment, output a TRIAGE line with: comment ID, user, classification, and ACCEPT/DECLINE decision
- Classifications: BUG FIX, VALID IMPROVEMENT, INCORRECT, STYLE PREFERENCE
- This is a READ-ONLY step: do NOT modify any files, commit, or push
- Output format: `TRIAGE:` header followed by one line per comment

## `buildReviewResponsePrompt(work IssueWork, comments []ReviewComment, reviews []PRReview, owner, repo string) string`

Tells Claude to:
- Use `/ce-resolve-pr-feedback` to evaluate and address all review feedback
- The skill evaluates each comment independently and:
  - Fixes valid issues (leaves changes uncommitted)
  - Declines invalid suggestions with specific rationale
  - Posts per-comment replies quoting the original feedback
  - Resolves addressed review threads via GraphQL
- The skill MUST reply to every thread — there is no fallback mechanism
- Run lint/test
- Do NOT commit, push, or amend — the outer agent handles git operations
- Skip step 7 (commit/push) from the skill workflow

## `buildCIFixPrompt(work IssueWork, failures []CheckRun, diff string, commits []Commit, signedOffBy string) string`

Tells Claude to:
- First investigate whether CI failures are directly caused by PR changes
- If UNRELATED: output starts with "UNRELATED" and an explanation
- If RELATED: critically evaluate the fix before applying it:
  - Verify the failure is actually caused by the PR changes (not a flaky test or infrastructure issue)
  - If the fix involves changing test expectations, confirm the new behavior is correct rather than just making the test pass
  - If the failure is in code the PR did not touch, treat it as UNRELATED (see step 2) even if it looks related
  - Prefer minimal, targeted fixes over broad refactoring
- Output starts with "RELATED" and Claude fixes the code
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
- `TestBuildReviewResponsePrompt` -- verifies each comment's file/line/body is included; verifies per-comment reply instructions, decline-with-rationale, thread resolution, and skip-step-7 constraints
