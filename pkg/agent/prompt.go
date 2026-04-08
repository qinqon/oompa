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

func buildReviewResponsePrompt(work IssueWork, comments []ReviewComment, signedOffBy, owner, repo string) string {
	prompt := fmt.Sprintf(`You are addressing review comments on PR #%d for issue #%d: %s
Repository: %s/%s

Review comments to address:
`, work.PRNumber, work.IssueNumber, work.IssueTitle, owner, repo)

	for _, c := range comments {
		prompt += fmt.Sprintf("\n--- Comment by %s (comment ID: %d)", c.User, c.ID)
		if c.Path != "" {
			prompt += fmt.Sprintf(" on file %s", c.Path)
			if c.Line > 0 {
				prompt += fmt.Sprintf(" line %d", c.Line)
			}
		}
		prompt += fmt.Sprintf(" ---\n%s\n", c.Body)
	}

	prompt += fmt.Sprintf(`
Instructions:
1. For each review comment above:
   - If the suggestion is valid, implement it and reply to the comment explaining what you changed
   - If the suggestion does not make sense or would break things, reply to the comment explaining why you disagree and do not implement it
   - Always reply to every comment, even if you agree and are implementing the change
2. Reply to each comment using this command (replace COMMENT_ID and BODY):
   gh api repos/%s/%s/pulls/comments/COMMENT_ID/replies -f body="BODY"
3. Run "make lint" and "make test" to verify your changes
4. Squash your changes into the existing commit(s) using "git add -A && git commit --amend --no-edit" then force push with "git push --force-with-lease"`, owner, repo)

	if signedOffBy != "" {
		prompt += fmt.Sprintf("\n5. Ensure the commit has \"Signed-off-by: %s\"", signedOffBy)
	}

	return prompt
}

func buildCIFixPrompt(work IssueWork, failures []CheckRun, signedOffBy string) string {
	prompt := fmt.Sprintf(`CI is failing on PR #%d for issue #%d: %s

Failed checks:
`, work.PRNumber, work.IssueNumber, work.IssueTitle)

	for _, f := range failures {
		prompt += fmt.Sprintf("\n--- Check: %s (conclusion: %s) ---\n", f.Name, f.Conclusion)
		if f.Output != "" {
			prompt += f.Output + "\n"
		}
	}

	prompt += `
Instructions:
1. Investigate the CI failures above
2. Fix the code so that CI passes
3. Run "make lint" and "make test" locally to verify
4. Squash your changes into the existing commit(s) using "git add -A && git commit --amend --no-edit" then force push with "git push --force-with-lease"`

	if signedOffBy != "" {
		prompt += fmt.Sprintf("\n5. Ensure the commit has \"Signed-off-by: %s\"", signedOffBy)
	}

	return prompt
}
