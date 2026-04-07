# Prompt Templates

## `buildImplementationPrompt(issue Issue) string`

Tells Claude to:
- Read Claude.md for conventions
- Implement the fix, run `make lint` and `make test`
- Commit (no trailing period, 72 char body)
- Create PR via `gh pr create` with `/kind`, `Fixes #N`, release-note block

## `buildReviewResponsePrompt(work IssueWork, comments []ReviewComment) string`

Tells Claude to:
- Address each review comment
- Run lint/test, commit, push
- No force-push

## Tests (`prompt_test.go`)

- `TestBuildImplementationPrompt` -- verifies issue number, title, body are interpolated; `/kind` and `release-note` instructions present
- `TestBuildReviewResponsePrompt` -- verifies each comment's file/line/body is included
