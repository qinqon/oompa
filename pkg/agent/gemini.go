package agent

import (
	"context"
	"fmt"
	"strings"
)

// GeminiReviewer defines the interface for Gemini-based code reviews.
type GeminiReviewer interface {
	ReviewPR(ctx context.Context, diff, title, description string) (GeminiReview, error)
}

// GeminiReview represents the result of a Gemini code review.
type GeminiReview struct {
	Summary  string              // Overall review summary
	Comments []GeminiReviewIssue // Specific issues found in the code
}

// GeminiReviewIssue represents a single issue found during review.
type GeminiReviewIssue struct {
	File     string // File path where the issue was found
	Line     int    // Line number (0 if not applicable)
	Severity string // "info", "warning", or "error"
	Message  string // Description of the issue
}

// VertexGeminiReviewer implements GeminiReviewer using Google Vertex AI.
type VertexGeminiReviewer struct {
	runner  CommandRunner
	cfg     Config
	model   string
	project string
	region  string
}

// NewVertexGeminiReviewer creates a new Gemini reviewer using Vertex AI.
func NewVertexGeminiReviewer(runner CommandRunner, cfg Config) *VertexGeminiReviewer {
	model := cfg.GeminiModel
	if model == "" {
		model = "gemini-2.0-flash-exp" // Default to latest stable model
	}
	return &VertexGeminiReviewer{
		runner:  runner,
		cfg:     cfg,
		model:   model,
		project: cfg.VertexProject,
		region:  cfg.VertexRegion,
	}
}

// ReviewPR performs a code review on the given PR diff using Gemini.
func (g *VertexGeminiReviewer) ReviewPR(ctx context.Context, diff, title, description string) (GeminiReview, error) {
	prompt := buildReviewPrompt(diff, title, description, g.cfg.GeminiReviewSeverity)

	// Use gcloud CLI to call Gemini via Vertex AI
	// This avoids adding additional SDK dependencies
	args := []string{
		"ai", "models", "generate-text",
		"--model", g.model,
		"--project", g.project,
		"--region", g.region,
		"--prompt", prompt,
	}

	stdout, stderr, err := g.runner.Run(ctx, "", "gcloud", args...)
	if err != nil {
		return GeminiReview{}, fmt.Errorf("gcloud gemini invocation failed: %w (stderr: %s)", err, string(stderr))
	}

	return parseGeminiResponse(string(stdout))
}

// buildReviewPrompt constructs the prompt for Gemini to review the PR.
func buildReviewPrompt(diff, title, description, severity string) string {
	minSeverity := severity
	if minSeverity == "" {
		minSeverity = "warning"
	}

	return fmt.Sprintf(`You are an expert code reviewer. Review the following pull request and identify potential issues.

PR Title: %s
PR Description:
%s

Diff:
%s

Please review the code and identify:
- Bugs or logic errors
- Missing error handling
- Test coverage gaps
- Style/convention issues
- Security concerns
- Performance issues

For each issue found, provide:
1. File path (if applicable)
2. Line number (if applicable, otherwise 0)
3. Severity: "info", "warning", or "error"
4. Message: A clear description of the issue and suggested fix

Respond in the following format:
SUMMARY: [Overall assessment of the PR]

ISSUES:
FILE: [file path or "general"]
LINE: [line number or 0]
SEVERITY: [info/warning/error]
MESSAGE: [description]
---
[Repeat for each issue]

Only include issues with severity level "%s" or higher. If no issues are found, respond with just "SUMMARY: No issues found. Code looks good!"`, title, description, diff, minSeverity)
}

// parseGeminiResponse parses the Gemini API response into a structured review.
func parseGeminiResponse(response string) (GeminiReview, error) {
	lines := strings.Split(response, "\n")
	review := GeminiReview{
		Comments: []GeminiReviewIssue{},
	}

	var currentIssue *GeminiReviewIssue
	inIssueSection := false

	for _, line := range lines {
		line = strings.TrimSpace(line)

		if strings.HasPrefix(line, "SUMMARY:") {
			review.Summary = strings.TrimSpace(strings.TrimPrefix(line, "SUMMARY:"))
			continue
		}

		if strings.HasPrefix(line, "ISSUES:") {
			inIssueSection = true
			continue
		}

		if !inIssueSection {
			continue
		}

		if line == "---" {
			if currentIssue != nil {
				review.Comments = append(review.Comments, *currentIssue)
			}
			currentIssue = &GeminiReviewIssue{}
			continue
		}

		if strings.HasPrefix(line, "FILE:") {
			if currentIssue == nil {
				currentIssue = &GeminiReviewIssue{}
			}
			currentIssue.File = strings.TrimSpace(strings.TrimPrefix(line, "FILE:"))
		} else if strings.HasPrefix(line, "LINE:") {
			if currentIssue != nil {
				lineStr := strings.TrimSpace(strings.TrimPrefix(line, "LINE:"))
				fmt.Sscanf(lineStr, "%d", &currentIssue.Line)
			}
		} else if strings.HasPrefix(line, "SEVERITY:") {
			if currentIssue != nil {
				currentIssue.Severity = strings.TrimSpace(strings.TrimPrefix(line, "SEVERITY:"))
			}
		} else if strings.HasPrefix(line, "MESSAGE:") {
			if currentIssue != nil {
				currentIssue.Message = strings.TrimSpace(strings.TrimPrefix(line, "MESSAGE:"))
			}
		}
	}

	// Don't forget the last issue
	if currentIssue != nil && currentIssue.Message != "" {
		review.Comments = append(review.Comments, *currentIssue)
	}

	return review, nil
}
