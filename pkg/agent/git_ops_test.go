package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTruncateSubject(t *testing.T) {
	tests := []struct {
		name     string
		subject  string
		maxLen   int
		expected string
	}{
		{"short subject unchanged", "fix: short title", 72, "fix: short title"},
		{"exactly 72 chars unchanged", strings.Repeat("a", 72), 72, strings.Repeat("a", 72)},
		{"long title truncated at word boundary", "CI Failure: pull-e2e-cluster-network-addons-operator-monitoring-k8s / The pull-e2e-cluster-network-addons-operator-monitoring-k8s", 72, "CI Failure: pull-e2e-cluster-network-addons-operator-monitoring-k8s..."},
		{"long single word hard truncated", strings.Repeat("x", 100), 72, strings.Repeat("x", 69) + "..."},
		{"empty string unchanged", "", 72, ""},
		{"breaks at last space before cutoff", "word1 word2 word3 word4 word5 word6 word7 word8 word9 word10 word11 word12 word13", 72, "word1 word2 word3 word4 word5 word6 word7 word8 word9 word10 word11..."},
		{"multi-byte runes not split", "Ошибка CI: " + strings.Repeat("слово ", 20), 30, "Ошибка CI: слово слово..."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateSubject(tt.subject, tt.maxLen)
			if got != tt.expected {
				t.Errorf("truncateSubject(%q, %d) =\n  %q (len=%d)\nwant:\n  %q (len=%d)", tt.subject, tt.maxLen, got, len(got), tt.expected, len(tt.expected))
			}
			if len([]rune(got)) > tt.maxLen {
				t.Errorf("truncateSubject result exceeds maxLen: got %d runes, max %d", len([]rune(got)), tt.maxLen)
			}
		})
	}
}

func TestReadCommitMsgFile_Present(t *testing.T) {
	// When .oompa-commit-msg exists and is non-empty, readCommitMsgFile should
	// return its trimmed contents and true, then delete the file.
	dir := t.TempDir()
	msgPath := filepath.Join(dir, commitMsgFile)
	want := "feat: new commit subject\n\nBody paragraph"
	if err := os.WriteFile(msgPath, []byte(want+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	msg, ok := readCommitMsgFile(dir)
	if !ok {
		t.Fatal("expected readCommitMsgFile to return true when file exists")
	}
	if msg != want {
		t.Errorf("expected %q, got %q", want, msg)
	}

	// File should have been deleted
	if _, err := os.Stat(msgPath); !os.IsNotExist(err) {
		t.Error("expected .oompa-commit-msg to be deleted after reading")
	}
}

func TestReadCommitMsgFile_Absent(t *testing.T) {
	// When .oompa-commit-msg does not exist, readCommitMsgFile should return ("", false).
	dir := t.TempDir()
	msg, ok := readCommitMsgFile(dir)
	if ok {
		t.Error("expected readCommitMsgFile to return false when file is absent")
	}
	if msg != "" {
		t.Errorf("expected empty string, got %q", msg)
	}
}

func TestReadCommitMsgFile_Empty(t *testing.T) {
	// When .oompa-commit-msg exists but is empty/whitespace-only, return ("", false).
	dir := t.TempDir()
	msgPath := filepath.Join(dir, commitMsgFile)
	if err := os.WriteFile(msgPath, []byte("  \n  \n"), 0o644); err != nil {
		t.Fatal(err)
	}

	msg, ok := readCommitMsgFile(dir)
	if ok {
		t.Error("expected readCommitMsgFile to return false for empty file")
	}
	if msg != "" {
		t.Errorf("expected empty string, got %q", msg)
	}

	// File should have been deleted even when empty
	if _, err := os.Stat(msgPath); !os.IsNotExist(err) {
		t.Error("expected .oompa-commit-msg to be deleted after reading empty file")
	}
}

func TestEnsureTrailers_AppendsWhenMissing(t *testing.T) {
	agent := newTestAgent(&mockGitHubClient{}, &mockCommandRunner{}, &mockWorktreeManager{})
	agent.cfg.SignedOffBy = "Test User <test@example.com>"
	agent.cfg.AssistedBy = "Claude <noreply@anthropic.com>"

	msg := agent.ensureTrailers("fix: subject\n\nbody text")

	if !strings.Contains(msg, "Signed-off-by: Test User <test@example.com>") {
		t.Error("expected Signed-off-by trailer to be appended")
	}
	if !strings.Contains(msg, "Assisted-by: Claude <noreply@anthropic.com>") {
		t.Error("expected Assisted-by trailer to be appended")
	}
	if !strings.Contains(msg, "fix: subject") {
		t.Error("original subject should be preserved")
	}
	if !strings.Contains(msg, "body text") {
		t.Error("original body should be preserved")
	}
}

func TestEnsureTrailers_SkipsWhenPresent(t *testing.T) {
	agent := newTestAgent(&mockGitHubClient{}, &mockCommandRunner{}, &mockWorktreeManager{})
	agent.cfg.SignedOffBy = "Test User <test@example.com>"
	agent.cfg.AssistedBy = "Claude <noreply@anthropic.com>"

	msg := "fix: subject\n\nbody text\n\nSigned-off-by: Test User <test@example.com>\nAssisted-by: Claude <noreply@anthropic.com>"
	result := agent.ensureTrailers(msg)

	if result != msg {
		t.Errorf("expected message to be unchanged when trailers already present, got %q", result)
	}
}

func TestEnsureTrailers_NoConfigNoChange(t *testing.T) {
	agent := newTestAgent(&mockGitHubClient{}, &mockCommandRunner{}, &mockWorktreeManager{})
	// No SignedOffBy or AssistedBy configured

	msg := "fix: subject\n\nbody text"
	result := agent.ensureTrailers(msg)

	if result != msg {
		t.Errorf("expected message to be unchanged when no trailers configured, got %q", result)
	}
}

func TestIsCommentOnlyDiff(t *testing.T) {
	cases := []struct {
		name string
		diff string
		want bool
	}{
		{"empty diff", "", false},
		{
			"hash comments only",
			"--- a/f.sh\n+++ b/f.sh\n@@ -1 +1,2 @@\n context\n+# a comment\n+#another\n",
			true,
		},
		{
			"slash comments only",
			"+++ b/f.go\n+// explanation\n+/* block */\n+ * middle\n+ */\n",
			true,
		},
		{
			"whitespace only",
			"+++ b/f.go\n+\n-   \n+\t\n",
			true,
		},
		{
			"mixed comment and code",
			"+++ b/f.go\n+// comment\n+x := 1\n",
			false,
		},
		{
			"removed code line",
			"+++ b/f.go\n-x := 1\n+# comment\n",
			false,
		},
		{
			"code only",
			"+++ b/f.go\n+x := 1\n",
			false,
		},
		{
			"added line starting with plus signs is not a header",
			"+++ b/f.c\n++++i;\n",
			false,
		},
		{
			"removed line starting with minus signs is not a header",
			"+++ b/f.c\n----i;\n",
			false,
		},
		{
			"shebang is not a comment",
			"+++ b/f.sh\n+#!/bin/bash\n",
			false,
		},
		{
			"nolint directive is not a comment",
			"+++ b/f.go\n+//nolint:errcheck\n",
			false,
		},
		{
			"go directive is not a comment",
			"+++ b/f.go\n+//go:generate stringer\n",
			false,
		},
		{
			"build tag is not a comment",
			"+++ b/f.go\n+// +build linux\n",
			false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isCommentOnlyDiffText(tc.diff); got != tc.want {
				t.Errorf("isCommentOnlyDiffText(%q) = %v, want %v", tc.diff, got, tc.want)
			}
		})
	}
}
