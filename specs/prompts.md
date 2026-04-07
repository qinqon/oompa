# Prompt Templates

## `buildImplementationPrompt(issue Issue, signedOffBy string) string`

Tells Claude to:
- Read Claude.md for conventions
- Implement the fix, run `make lint` and `make test`
- Commit (no trailing period, 72 char body)
- Create PR via `gh pr create` with `/kind`, `Fixes #N`, release-note block
- If signedOffBy is non-empty, add `Signed-off-by:` to every commit message

## `buildReviewResponsePrompt(work IssueWork, comments []ReviewComment, signedOffBy string) string`

Tells Claude to:
- For each review comment: implement if valid, push back with explanation if not
- Always reply to every comment, even when implementing the suggestion
- Reply using `gh pr review` or `gh api`
- Run lint/test, commit, push
- No force-push
- If signedOffBy is non-empty, add `Signed-off-by:` to every commit message

## Tests (`prompt_test.go`)

- `TestBuildImplementationPrompt` -- verifies issue number, title, body are interpolated; `/kind` and `release-note` instructions present
- `TestBuildReviewResponsePrompt` -- verifies each comment's file/line/body is included
