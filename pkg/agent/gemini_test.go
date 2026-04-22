package agent

import (
	"context"
	"testing"
)

// MockGeminiReviewer implements GeminiReviewer for testing.
type MockGeminiReviewer struct {
	ReviewFunc func(ctx context.Context, diff, title, description string) (GeminiReview, error)
}

func (m *MockGeminiReviewer) ReviewPR(ctx context.Context, diff, title, description string) (GeminiReview, error) {
	if m.ReviewFunc != nil {
		return m.ReviewFunc(ctx, diff, title, description)
	}
	return GeminiReview{
		Summary: "No issues found. Code looks good!",
	}, nil
}

func TestBuildReviewPrompt(t *testing.T) {
	diff := "diff --git a/file.go b/file.go\n+func foo() {}\n"
	title := "Add foo function"
	description := "This adds a new function"
	severity := "warning"

	prompt := buildReviewPrompt(diff, title, description, severity)

	if prompt == "" {
		t.Fatal("prompt should not be empty")
	}

	// Check that key elements are in the prompt
	if !contains(prompt, title) {
		t.Errorf("prompt should contain PR title")
	}
	if !contains(prompt, description) {
		t.Errorf("prompt should contain PR description")
	}
	if !contains(prompt, diff) {
		t.Errorf("prompt should contain diff")
	}
	if !contains(prompt, severity) {
		t.Errorf("prompt should contain severity level")
	}
}

func TestParseGeminiResponse(t *testing.T) {
	tests := []struct {
		name     string
		response string
		want     GeminiReview
	}{
		{
			name:     "no issues",
			response: "SUMMARY: No issues found. Code looks good!",
			want: GeminiReview{
				Summary:  "No issues found. Code looks good!",
				Comments: []GeminiReviewIssue{},
			},
		},
		{
			name: "single issue",
			response: `SUMMARY: Found one issue

ISSUES:
FILE: main.go
LINE: 42
SEVERITY: warning
MESSAGE: Variable x is never used
---`,
			want: GeminiReview{
				Summary: "Found one issue",
				Comments: []GeminiReviewIssue{
					{
						File:     "main.go",
						Line:     42,
						Severity: "warning",
						Message:  "Variable x is never used",
					},
				},
			},
		},
		{
			name: "multiple issues",
			response: `SUMMARY: Found several issues

ISSUES:
FILE: file1.go
LINE: 10
SEVERITY: error
MESSAGE: Potential nil pointer dereference
---
FILE: file2.go
LINE: 25
SEVERITY: info
MESSAGE: Consider adding a comment
---`,
			want: GeminiReview{
				Summary: "Found several issues",
				Comments: []GeminiReviewIssue{
					{
						File:     "file1.go",
						Line:     10,
						Severity: "error",
						Message:  "Potential nil pointer dereference",
					},
					{
						File:     "file2.go",
						Line:     25,
						Severity: "info",
						Message:  "Consider adding a comment",
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseGeminiResponse(tt.response)
			if err != nil {
				t.Fatalf("parseGeminiResponse() error = %v", err)
			}

			if got.Summary != tt.want.Summary {
				t.Errorf("Summary = %q, want %q", got.Summary, tt.want.Summary)
			}

			if len(got.Comments) != len(tt.want.Comments) {
				t.Fatalf("Comments length = %d, want %d", len(got.Comments), len(tt.want.Comments))
			}

			for i, wantComment := range tt.want.Comments {
				gotComment := got.Comments[i]
				if gotComment.File != wantComment.File {
					t.Errorf("Comment[%d].File = %q, want %q", i, gotComment.File, wantComment.File)
				}
				if gotComment.Line != wantComment.Line {
					t.Errorf("Comment[%d].Line = %d, want %d", i, gotComment.Line, wantComment.Line)
				}
				if gotComment.Severity != wantComment.Severity {
					t.Errorf("Comment[%d].Severity = %q, want %q", i, gotComment.Severity, wantComment.Severity)
				}
				if gotComment.Message != wantComment.Message {
					t.Errorf("Comment[%d].Message = %q, want %q", i, gotComment.Message, wantComment.Message)
				}
			}
		})
	}
}

func TestNewVertexGeminiReviewer(t *testing.T) {
	runner := &mockCommandRunner{}
	cfg := Config{
		GeminiModel:   "gemini-2.0-flash-exp",
		VertexProject: "test-project",
		VertexRegion:  "us-central1",
	}

	reviewer := NewVertexGeminiReviewer(runner, cfg)

	if reviewer == nil {
		t.Fatal("NewVertexGeminiReviewer should not return nil")
	}
	if reviewer.model != cfg.GeminiModel {
		t.Errorf("model = %q, want %q", reviewer.model, cfg.GeminiModel)
	}
	if reviewer.project != cfg.VertexProject {
		t.Errorf("project = %q, want %q", reviewer.project, cfg.VertexProject)
	}
	if reviewer.region != cfg.VertexRegion {
		t.Errorf("region = %q, want %q", reviewer.region, cfg.VertexRegion)
	}
}

func TestNewVertexGeminiReviewer_DefaultModel(t *testing.T) {
	runner := &mockCommandRunner{}
	cfg := Config{
		GeminiModel:   "", // Empty should use default
		VertexProject: "test-project",
		VertexRegion:  "us-central1",
	}

	reviewer := NewVertexGeminiReviewer(runner, cfg)

	if reviewer.model == "" {
		t.Error("model should have a default value when not specified")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
