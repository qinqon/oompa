package agent

import "fmt"

func buildImplementationPrompt(issue Issue) string {
	return fmt.Sprintf(`You are resolving GitHub issue #%d: %s

Issue description:
%s

Instructions:
1. Read CLAUDE.md for project conventions
2. Implement the fix for this issue
3. Run "make lint" and "make test" to verify your changes
4. Commit your changes with a descriptive message (no trailing period, wrap body at 72 chars)
5. Create a PR using "gh pr create" with:
   - A /kind label (e.g. /kind bug, /kind feature)
   - "Fixes #%d" in the PR body
   - A release-note block describing the change

Do not merge the PR. Only create it.`,
		issue.Number, issue.Title, issue.Body, issue.Number)
}

func buildReviewResponsePrompt(work IssueWork, comments []ReviewComment) string {
	prompt := fmt.Sprintf(`You are addressing review comments on PR #%d for issue #%d: %s

Review comments to address:
`, work.PRNumber, work.IssueNumber, work.IssueTitle)

	for _, c := range comments {
		prompt += fmt.Sprintf("\n--- Comment by %s", c.User)
		if c.Path != "" {
			prompt += fmt.Sprintf(" on file %s", c.Path)
			if c.Line > 0 {
				prompt += fmt.Sprintf(" line %d", c.Line)
			}
		}
		prompt += fmt.Sprintf(" ---\n%s\n", c.Body)
	}

	prompt += `
Instructions:
1. Address each review comment above
2. Run "make lint" and "make test" to verify your changes
3. Commit and push your changes
4. Do not force-push`

	return prompt
}
