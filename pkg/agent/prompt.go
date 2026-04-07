package agent

import "fmt"

func buildImplementationPrompt(issue Issue, signedOffBy string) string {
	signoff := ""
	if signedOffBy != "" {
		signoff = fmt.Sprintf("\n6. Add \"Signed-off-by: %s\" to every commit message", signedOffBy)
	}

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
   - A release-note block describing the change%s

Do not merge the PR. Only create it.`,
		issue.Number, issue.Title, issue.Body, issue.Number, signoff)
}

func buildReviewResponsePrompt(work IssueWork, comments []ReviewComment, signedOffBy string) string {
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
1. For each review comment above:
   - If the suggestion is valid, implement it and reply to the comment explaining what you changed
   - If the suggestion does not make sense or would break things, reply to the comment explaining why you disagree and do not implement it
   - Always reply to every comment, even if you agree and are implementing the change
2. Reply to comments using "gh pr review" or "gh api" to post responses on the PR
3. Run "make lint" and "make test" to verify your changes
4. Commit and push your changes
5. Do not force-push`

	if signedOffBy != "" {
		prompt += fmt.Sprintf("\n6. Add \"Signed-off-by: %s\" to every commit message", signedOffBy)
	}

	return prompt
}
