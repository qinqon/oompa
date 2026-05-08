package agent

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadFileConfig_Valid(t *testing.T) {
	yaml := `
agent: opencode
agent-model: google-vertex-anthropic/claude-opus-4-6@default
poll-interval: 2m
log-level: debug
exit-on-new-version: qinqon/oompa
projects:
  - repo: ovn-kubernetes/ovn-kubernetes
    create-flaky-issues: true
    flaky-label: kind/ci-flake
    prs:
      - watch: [6252, 6229]
        reactions: [ci, conflicts, rebase]
  - repo: qinqon/oompa
    issues:
      - label: good-for-ai
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}

	fc, err := LoadFileConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if fc.Agent != "opencode" {
		t.Errorf("expected agent 'opencode', got %q", fc.Agent)
	}
	if fc.AgentModel != "google-vertex-anthropic/claude-opus-4-6@default" {
		t.Errorf("unexpected agent-model %q", fc.AgentModel)
	}
	if fc.PollInterval != "2m" {
		t.Errorf("expected poll-interval '2m', got %q", fc.PollInterval)
	}
	if fc.LogLevel != "debug" {
		t.Errorf("expected log-level 'debug', got %q", fc.LogLevel)
	}
	if fc.ExitOnNewVersion != "qinqon/oompa" {
		t.Errorf("unexpected exit-on-new-version %q", fc.ExitOnNewVersion)
	}
	if len(fc.Projects) != 2 {
		t.Fatalf("expected 2 projects, got %d", len(fc.Projects))
	}
	// First project
	p := fc.Projects[0]
	if p.Repo != "ovn-kubernetes/ovn-kubernetes" {
		t.Errorf("unexpected repo %q", p.Repo)
	}
	if p.CreateFlakyIssues == nil || !*p.CreateFlakyIssues {
		t.Error("expected create-flaky-issues to be true")
	}
	if p.FlakyLabel != "kind/ci-flake" {
		t.Errorf("unexpected flaky-label %q", p.FlakyLabel)
	}
	if len(p.PRs) != 1 {
		t.Fatalf("expected 1 prs entry, got %d", len(p.PRs))
	}
	if len(p.PRs[0].Watch) != 2 || p.PRs[0].Watch[0] != 6252 {
		t.Errorf("unexpected watch list %v", p.PRs[0].Watch)
	}
	// Second project
	p2 := fc.Projects[1]
	if p2.Repo != "qinqon/oompa" {
		t.Errorf("unexpected repo %q", p2.Repo)
	}
	if len(p2.Issues) != 1 {
		t.Fatalf("expected 1 issues entry, got %d", len(p2.Issues))
	}
	if p2.Issues[0].Label != "good-for-ai" {
		t.Errorf("unexpected label %q", p2.Issues[0].Label)
	}
}

func TestLoadFileConfig_NoProjects(t *testing.T) {
	yaml := `
agent: opencode
projects: []
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte(yaml), 0o644) //nolint:errcheck // test helper: WriteFile errors are caught by subsequent LoadFileConfig

	_, err := LoadFileConfig(path)
	if err == nil {
		t.Fatal("expected error for empty projects")
	}
}

func TestLoadFileConfig_InvalidRepo(t *testing.T) {
	yaml := `
projects:
  - repo: invalid
    prs:
      - watch: [1]
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte(yaml), 0o644) //nolint:errcheck // test helper: WriteFile errors are caught by subsequent LoadFileConfig

	_, err := LoadFileConfig(path)
	if err == nil {
		t.Fatal("expected error for invalid repo format")
	}
}

func TestLoadFileConfig_NoRoles(t *testing.T) {
	yaml := `
projects:
  - repo: owner/repo
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte(yaml), 0o644) //nolint:errcheck // test helper: WriteFile errors are caught by subsequent LoadFileConfig

	_, err := LoadFileConfig(path)
	if err == nil {
		t.Fatal("expected error when no roles defined")
	}
}

func TestLoadFileConfig_PRsWithoutWatch(t *testing.T) {
	yaml := `
projects:
  - repo: owner/repo
    prs:
      - reactions: [ci]
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte(yaml), 0o644) //nolint:errcheck // test helper: WriteFile errors are caught by subsequent LoadFileConfig

	_, err := LoadFileConfig(path)
	if err == nil {
		t.Fatal("expected error for prs without watch list")
	}
}

func TestLoadFileConfig_TriageWithoutJobs(t *testing.T) {
	yaml := `
projects:
  - repo: owner/repo
    triage:
      - schedule: "09:00 Europe/Madrid"
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte(yaml), 0o644) //nolint:errcheck // test helper: WriteFile errors are caught by subsequent LoadFileConfig

	_, err := LoadFileConfig(path)
	if err == nil {
		t.Fatal("expected error for triage without jobs")
	}
}

func TestLoadFileConfig_InvalidReaction(t *testing.T) {
	yaml := `
projects:
  - repo: owner/repo
    prs:
      - watch: [1]
        reactions: [invalid]
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte(yaml), 0o644) //nolint:errcheck // test helper: WriteFile errors are caught by subsequent LoadFileConfig

	_, err := LoadFileConfig(path)
	if err == nil {
		t.Fatal("expected error for invalid reaction")
	}
}

func TestLoadFileConfig_InvalidAgent(t *testing.T) {
	yaml := `
agent: badagent
projects:
  - repo: owner/repo
    issues:
      - label: test
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte(yaml), 0o644) //nolint:errcheck // test helper: WriteFile errors are caught by subsequent LoadFileConfig

	_, err := LoadFileConfig(path)
	if err == nil {
		t.Fatal("expected error for invalid agent")
	}
}

func TestBuildRoleEntries_TwoTierInheritance(t *testing.T) {
	fc := &FileConfig{
		Agent:        "opencode",
		PollInterval: "5m",
		Projects: []ProjectConfig{
			{
				Repo:              "owner/repo",
				CreateFlakyIssues: new(true),
				FlakyLabel:        "kind/flaky",
				SkipComment:       []string{"ci-unrelated"},
				PRs: []PRsRoleConfig{
					{
						Watch: []int{100, 200},
						// Inherits project-level create-flaky-issues, flaky-label, skip-comment
					},
					{
						Watch:             []int{300},
						CreateFlakyIssues: new(false),                    // Override
						FlakyLabel:        "override",                    // Override
						SkipComment:       []string{"ci-infrastructure"}, // Override
					},
				},
				Issues: []IssuesRoleConfig{
					{Label: "ai-label"},
				},
			},
		},
	}

	globalCfg := Config{
		Agent:    "claudecode", // Should be overridden by file config
		LogLevel: "info",
	}

	entries := BuildRoleEntries(fc, "/tmp/work", globalCfg)
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	// First PRs entry inherits project defaults
	e1 := entries[0]
	if e1.Role != "prs" {
		t.Errorf("expected role 'prs', got %q", e1.Role)
	}
	if e1.Config.Role != "prs" {
		t.Errorf("expected Config.Role 'prs', got %q", e1.Config.Role)
	}
	if e1.Config.Owner != "owner" || e1.Config.Repo != "repo" {
		t.Errorf("unexpected owner/repo %s/%s", e1.Config.Owner, e1.Config.Repo)
	}
	if len(e1.Config.WatchPRs) != 2 || e1.Config.WatchPRs[0] != 100 {
		t.Errorf("unexpected watch PRs %v", e1.Config.WatchPRs)
	}
	if !e1.Config.CreateFlakyIssues {
		t.Error("expected inherited create-flaky-issues=true")
	}
	if e1.Config.FlakyLabel != "kind/flaky" {
		t.Errorf("expected inherited flaky-label 'kind/flaky', got %q", e1.Config.FlakyLabel)
	}
	if len(e1.Config.SkipComments) != 1 || e1.Config.SkipComments[0] != "ci-unrelated" {
		t.Errorf("expected inherited skip-comment, got %v", e1.Config.SkipComments)
	}
	if e1.Config.Agent != "opencode" {
		t.Errorf("expected agent 'opencode' from file config, got %q", e1.Config.Agent)
	}
	// Second PRs entry overrides project defaults
	e2 := entries[1]
	if e2.Config.CreateFlakyIssues {
		t.Error("expected overridden create-flaky-issues=false")
	}
	if e2.Config.FlakyLabel != "override" {
		t.Errorf("expected overridden flaky-label 'override', got %q", e2.Config.FlakyLabel)
	}
	if len(e2.Config.SkipComments) != 1 || e2.Config.SkipComments[0] != "ci-infrastructure" {
		t.Errorf("expected overridden skip-comment, got %v", e2.Config.SkipComments)
	}
	// Issues entry
	e3 := entries[2]
	if e3.Role != "issues" {
		t.Errorf("expected role 'issues', got %q", e3.Role)
	}
	if e3.Config.Role != "issues" {
		t.Errorf("expected Config.Role 'issues', got %q", e3.Config.Role)
	}
	if e3.Config.Label != "ai-label" {
		t.Errorf("expected label 'ai-label', got %q", e3.Config.Label)
	}
}

func TestBuildRoleEntries_MultipleProjectsAndRoles(t *testing.T) {
	fc := &FileConfig{
		Projects: []ProjectConfig{
			{
				Repo: "org1/repo1",
				PRs:  []PRsRoleConfig{{Watch: []int{1}}},
			},
			{
				Repo:   "org2/repo2",
				Issues: []IssuesRoleConfig{{Label: "ai"}},
				Triage: []TriageRoleConfig{{Jobs: []string{"https://ci.example.com/job"}, Schedule: "09:00 UTC"}},
			},
		},
	}

	entries := BuildRoleEntries(fc, "/tmp/work", Config{Agent: "claudecode"})
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	// Check each entry's project is correct
	if entries[0].Config.Owner != "org1" || entries[0].Config.Repo != "repo1" {
		t.Error("entry 0 has wrong owner/repo")
	}
	if entries[1].Config.Owner != "org2" || entries[1].Config.Repo != "repo2" {
		t.Error("entry 1 has wrong owner/repo")
	}
	if entries[2].Config.Owner != "org2" || entries[2].Config.Repo != "repo2" {
		t.Error("entry 2 has wrong owner/repo")
	}
	// Check clone dirs are per-project
	if entries[0].Config.CloneDir != "/tmp/work/org1/repo1" {
		t.Errorf("unexpected clone dir %q", entries[0].Config.CloneDir)
	}
	if entries[1].Config.CloneDir != "/tmp/work/org2/repo2" {
		t.Errorf("unexpected clone dir %q", entries[1].Config.CloneDir)
	}
	// Check triage entry has schedule and Config.Role
	if entries[2].Schedule != "09:00 UTC" {
		t.Errorf("expected schedule '09:00 UTC', got %q", entries[2].Schedule)
	}
	if entries[2].Config.Role != "triage" {
		t.Errorf("expected Config.Role 'triage', got %q", entries[2].Config.Role)
	}
}

func TestBuildRoleEntries_ForkInheritance(t *testing.T) {
	fc := &FileConfig{
		Projects: []ProjectConfig{
			{
				Repo: "upstream/repo",
				Fork: "myfork/repo",
				PRs:  []PRsRoleConfig{{Watch: []int{1}}},
				Issues: []IssuesRoleConfig{
					{Label: "ai"}, // Inherits project fork
					{Label: "special", Fork: "otherfork/myrepo"}, // Overrides fork
				},
			},
		},
	}

	entries := BuildRoleEntries(fc, "/tmp/work", Config{Agent: "claudecode"})
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	// PRs entry inherits project fork
	if entries[0].Config.ForkOwner != "myfork" || entries[0].Config.ForkRepo != "repo" {
		t.Errorf("PRs: expected fork myfork/repo, got %s/%s", entries[0].Config.ForkOwner, entries[0].Config.ForkRepo)
	}
	if entries[0].Config.GitHubHeadOwner != "myfork" {
		t.Errorf("PRs: expected head owner 'myfork', got %q", entries[0].Config.GitHubHeadOwner)
	}
	// First issues entry inherits project fork
	if entries[1].Config.ForkOwner != "myfork" {
		t.Errorf("Issues[0]: expected fork owner 'myfork', got %q", entries[1].Config.ForkOwner)
	}
	// Second issues entry overrides fork
	if entries[2].Config.ForkOwner != "otherfork" || entries[2].Config.ForkRepo != "myrepo" {
		t.Errorf("Issues[1]: expected fork otherfork/myrepo, got %s/%s", entries[2].Config.ForkOwner, entries[2].Config.ForkRepo)
	}
	if entries[2].Config.GitHubHeadOwner != "otherfork" {
		t.Errorf("Issues[1]: expected head owner 'otherfork', got %q", entries[2].Config.GitHubHeadOwner)
	}
}

func TestParseSchedule_Daily(t *testing.T) {
	// Set a known time: Wednesday 2024-01-10 14:00 UTC
	now := time.Date(2024, 1, 10, 14, 0, 0, 0, time.UTC)

	// Schedule for 15:00 UTC — should be today since it's in the future
	next, err := ParseSchedule("15:00 UTC", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := time.Date(2024, 1, 10, 15, 0, 0, 0, time.UTC)
	if !next.Equal(expected) {
		t.Errorf("expected %v, got %v", expected, next)
	}
	// Schedule for 13:00 UTC — should be tomorrow since it's in the past
	next, err = ParseSchedule("13:00 UTC", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected = time.Date(2024, 1, 11, 13, 0, 0, 0, time.UTC)
	if !next.Equal(expected) {
		t.Errorf("expected %v, got %v", expected, next)
	}
}

func TestParseSchedule_Weekly(t *testing.T) {
	// Wednesday 2024-01-10 14:00 UTC
	now := time.Date(2024, 1, 10, 14, 0, 0, 0, time.UTC)

	// Schedule for Monday 09:00 UTC — should be next Monday (2024-01-15)
	next, err := ParseSchedule("09:00 Monday UTC", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := time.Date(2024, 1, 15, 9, 0, 0, 0, time.UTC)
	if !next.Equal(expected) {
		t.Errorf("expected %v, got %v", expected, next)
	}
	// Schedule for Wednesday 09:00 UTC — time has passed today, should be next Wednesday
	next, err = ParseSchedule("09:00 Wednesday UTC", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected = time.Date(2024, 1, 17, 9, 0, 0, 0, time.UTC)
	if !next.Equal(expected) {
		t.Errorf("expected %v, got %v", expected, next)
	}
	// Schedule for Wednesday 15:00 UTC — hasn't happened yet today, should be today
	next, err = ParseSchedule("15:00 Wednesday UTC", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected = time.Date(2024, 1, 10, 15, 0, 0, 0, time.UTC)
	if !next.Equal(expected) {
		t.Errorf("expected %v, got %v", expected, next)
	}
}

func TestParseSchedule_InvalidFormats(t *testing.T) {
	tests := []string{
		"",
		"09:00",
		"invalid UTC",
		"09:00 Invalid/Timezone",
		"09:00 Flurpday UTC",
		"25:00 UTC",
		"09:60 UTC",
	}

	now := time.Now()
	for _, s := range tests {
		_, err := ParseSchedule(s, now)
		if err == nil {
			t.Errorf("expected error for schedule %q", s)
		}
	}
}

func TestNewRoleLogger_PRs(t *testing.T) {
	base := slog.Default()
	entry := RoleEntry{
		Config: Config{Owner: "org", Repo: "repo", WatchPRs: []int{1, 2, 3}},
		Role:   "prs",
	}
	logger := NewRoleLogger(base, entry)
	if logger == nil {
		t.Fatal("expected non-nil logger")
	}
}

func TestNewRoleLogger_Issues(t *testing.T) {
	base := slog.Default()
	entry := RoleEntry{
		Config: Config{Owner: "org", Repo: "repo", Label: "good-for-ai"},
		Role:   "issues",
	}
	logger := NewRoleLogger(base, entry)
	if logger == nil {
		t.Fatal("expected non-nil logger")
	}
}

func TestNewRoleLogger_Triage(t *testing.T) {
	base := slog.Default()
	entry := RoleEntry{
		Config:   Config{Owner: "org", Repo: "repo", TriageJobs: []string{"https://ci.example.com/job"}},
		Role:     "triage",
		Schedule: "09:00 UTC",
	}

	logger := NewRoleLogger(base, entry)
	if logger == nil {
		t.Fatal("expected non-nil logger")
	}
}

func TestBuildRoleEntries_ReviewersInheritance(t *testing.T) {
	fc := &FileConfig{
		Projects: []ProjectConfig{
			{
				Repo:      "owner/repo",
				Reviewers: []string{"proj-reviewer1", "proj-reviewer2"},
				PRs: []PRsRoleConfig{
					{
						Watch: []int{1},
						// Inherits project-level reviewers
					},
					{
						Watch:     []int{2},
						Reviewers: []string{"role-reviewer"}, // Override
					},
				},
				Issues: []IssuesRoleConfig{
					{
						Label: "ai",
						// Inherits project-level reviewers
					},
					{
						Label:     "special",
						Reviewers: []string{"issue-reviewer"}, // Override
					},
				},
				Triage: []TriageRoleConfig{
					{
						Jobs: []string{"https://ci.example.com/job1"},
						// Inherits project-level reviewers
					},
					{
						Jobs:      []string{"https://ci.example.com/job2"},
						Reviewers: []string{"triage-reviewer"}, // Override
					},
				},
			},
			{
				Repo: "org/noproj",
				PRs:  []PRsRoleConfig{{Watch: []int{10}}},
				// No project-level reviewers — inherits global
			},
		},
	}

	globalCfg := Config{
		Agent:     "claudecode",
		Reviewers: []string{"global-reviewer"},
	}

	entries := BuildRoleEntries(fc, "/tmp/work", globalCfg)
	if len(entries) != 7 {
		t.Fatalf("expected 7 entries, got %d", len(entries))
	}
	// PRs[0]: inherits project-level reviewers
	if len(entries[0].Config.Reviewers) != 2 || entries[0].Config.Reviewers[0] != "proj-reviewer1" {
		t.Errorf("PRs[0]: expected project reviewers, got %v", entries[0].Config.Reviewers)
	}
	// PRs[1]: overrides with role-level reviewers
	if len(entries[1].Config.Reviewers) != 1 || entries[1].Config.Reviewers[0] != "role-reviewer" {
		t.Errorf("PRs[1]: expected role reviewers, got %v", entries[1].Config.Reviewers)
	}
	// Issues[0]: inherits project-level reviewers
	if len(entries[2].Config.Reviewers) != 2 || entries[2].Config.Reviewers[0] != "proj-reviewer1" {
		t.Errorf("Issues[0]: expected project reviewers, got %v", entries[2].Config.Reviewers)
	}
	// Issues[1]: overrides with role-level reviewers
	if len(entries[3].Config.Reviewers) != 1 || entries[3].Config.Reviewers[0] != "issue-reviewer" {
		t.Errorf("Issues[1]: expected role reviewers, got %v", entries[3].Config.Reviewers)
	}
	// Triage[0]: inherits project-level reviewers
	if len(entries[4].Config.Reviewers) != 2 || entries[4].Config.Reviewers[0] != "proj-reviewer1" {
		t.Errorf("Triage[0]: expected project reviewers, got %v", entries[4].Config.Reviewers)
	}
	// Triage[1]: overrides with role-level reviewers
	if len(entries[5].Config.Reviewers) != 1 || entries[5].Config.Reviewers[0] != "triage-reviewer" {
		t.Errorf("Triage[1]: expected role reviewers, got %v", entries[5].Config.Reviewers)
	}
	// Second project PRs[0]: no project reviewers, inherits global
	if len(entries[6].Config.Reviewers) != 1 || entries[6].Config.Reviewers[0] != "global-reviewer" {
		t.Errorf("Project2 PRs[0]: expected global reviewers, got %v", entries[6].Config.Reviewers)
	}
}

func TestLoadFileConfig_WithReviewers(t *testing.T) {
	yaml := `
projects:
  - repo: owner/repo
    reviewers: [alice, bob]
    prs:
      - watch: [1]
        reviewers: [charlie]
    issues:
      - label: ai
        reviewers: [dave]
    triage:
      - jobs: [https://ci.example.com/job]
        reviewers: [eve]
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}

	fc, err := LoadFileConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	p := fc.Projects[0]
	if len(p.Reviewers) != 2 || p.Reviewers[0] != "alice" || p.Reviewers[1] != "bob" {
		t.Errorf("expected project reviewers [alice bob], got %v", p.Reviewers)
	}
	if len(p.PRs[0].Reviewers) != 1 || p.PRs[0].Reviewers[0] != "charlie" {
		t.Errorf("expected prs reviewers [charlie], got %v", p.PRs[0].Reviewers)
	}
	if len(p.Issues[0].Reviewers) != 1 || p.Issues[0].Reviewers[0] != "dave" {
		t.Errorf("expected issues reviewers [dave], got %v", p.Issues[0].Reviewers)
	}
	if len(p.Triage[0].Reviewers) != 1 || p.Triage[0].Reviewers[0] != "eve" {
		t.Errorf("expected triage reviewers [eve], got %v", p.Triage[0].Reviewers)
	}
}

func TestIssueKey(t *testing.T) {
	key := IssueKey("owner", "repo", 42)
	if key != "owner/repo#42" {
		t.Errorf("expected 'owner/repo#42', got %q", key)
	}
}

func TestBuildRoleEntries_GlobalOverrides(t *testing.T) {
	fc := &FileConfig{
		DryRun:  true,
		OneShot: true,
		Projects: []ProjectConfig{
			{
				Repo: "owner/repo",
				PRs:  []PRsRoleConfig{{Watch: []int{1}}},
			},
		},
	}

	entries := BuildRoleEntries(fc, "/tmp/work", Config{Agent: "claudecode"})
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if !entries[0].Config.DryRun {
		t.Error("expected dry-run to propagate from file config")
	}
	if !entries[0].Config.OneShot {
		t.Error("expected one-shot to propagate from file config")
	}
}

func TestLoadFileConfig_InvalidSkipComment(t *testing.T) {
	yaml := `
projects:
  - repo: owner/repo
    skip-comment:
      - invalid-category
    prs:
      - watch: [1]
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte(yaml), 0o644) //nolint:errcheck // test helper: WriteFile errors are caught by subsequent LoadFileConfig

	_, err := LoadFileConfig(path)
	if err == nil {
		t.Fatal("expected error for invalid skip-comment")
	}
}

func TestLoadFileConfig_FileNotFound(t *testing.T) {
	_, err := LoadFileConfig("/nonexistent/path.yaml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadFileConfig_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte("{{invalid yaml"), 0o644) //nolint:errcheck // test helper: WriteFile errors are caught by subsequent LoadFileConfig

	_, err := LoadFileConfig(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestLoadFileConfig_AgentModelWithoutOpenCode(t *testing.T) {
	yaml := `
agent: claudecode
agent-model: some-model
projects:
  - repo: owner/repo
    issues:
      - label: test
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte(yaml), 0o644) //nolint:errcheck // test helper: WriteFile errors are caught by subsequent LoadFileConfig

	_, err := LoadFileConfig(path)
	if err == nil {
		t.Fatal("expected error for agent-model with claudecode")
	}
}

func TestLoadFileConfig_AgentModelWithoutAgentSet(t *testing.T) {
	yaml := `
agent-model: some-model
projects:
  - repo: owner/repo
    issues:
      - label: test
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte(yaml), 0o644) //nolint:errcheck // test helper: WriteFile errors are caught by subsequent LoadFileConfig

	_, err := LoadFileConfig(path)
	if err == nil {
		t.Fatal("expected error for agent-model without explicit agent")
	}
}

func TestLoadFileConfig_InvalidSchedule(t *testing.T) {
	yaml := `
projects:
  - repo: owner/repo
    triage:
      - jobs: [https://ci.example.com/job]
        schedule: "09:00 Invalid/Timezone"
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte(yaml), 0o644) //nolint:errcheck // test helper: WriteFile errors are caught by subsequent LoadFileConfig

	_, err := LoadFileConfig(path)
	if err == nil {
		t.Fatal("expected error for invalid schedule timezone")
	}
}

func TestLoadFileConfig_ValidSchedule(t *testing.T) {
	yaml := `
projects:
  - repo: owner/repo
    triage:
      - jobs: [https://ci.example.com/job]
        schedule: "09:00 UTC"
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte(yaml), 0o644) //nolint:errcheck // test helper: WriteFile errors are caught by subsequent LoadFileConfig

	_, err := LoadFileConfig(path)
	if err != nil {
		t.Fatalf("unexpected error for valid schedule: %v", err)
	}
}

func TestLoadFileConfig_InvalidLookback(t *testing.T) {
	yaml := `
projects:
  - repo: owner/repo
    triage:
      - jobs: [https://ci.example.com/job]
        lookback: "not-a-duration"
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte(yaml), 0o644) //nolint:errcheck // test helper: WriteFile errors are caught by subsequent LoadFileConfig

	_, err := LoadFileConfig(path)
	if err == nil {
		t.Fatal("expected error for invalid lookback duration")
	}
}

func TestLoadFileConfig_NegativeLookback(t *testing.T) {
	yaml := `
projects:
  - repo: owner/repo
    triage:
      - jobs: [https://ci.example.com/job]
        lookback: "-1h"
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte(yaml), 0o644) //nolint:errcheck // test helper: WriteFile errors are caught by subsequent LoadFileConfig

	_, err := LoadFileConfig(path)
	if err == nil {
		t.Fatal("expected error for negative lookback duration")
	}
}

func TestLoadFileConfig_ValidLookback(t *testing.T) {
	yaml := `
projects:
  - repo: owner/repo
    triage:
      - jobs: [https://ci.example.com/job]
        lookback: 24h
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte(yaml), 0o644) //nolint:errcheck // test helper: WriteFile errors are caught by subsequent LoadFileConfig

	fc, err := LoadFileConfig(path)
	if err != nil {
		t.Fatalf("unexpected error for valid lookback: %v", err)
	}
	if fc.Projects[0].Triage[0].Lookback != "24h" {
		t.Errorf("expected lookback '24h', got %q", fc.Projects[0].Triage[0].Lookback)
	}
}

func TestBuildRoleEntries_TriageLookback(t *testing.T) {
	const (
		defaultLookback  = 12 * time.Hour
		overrideLookback = 24 * time.Hour
	)

	fc := &FileConfig{
		Projects: []ProjectConfig{
			{
				Repo: "owner/repo",
				Triage: []TriageRoleConfig{
					{
						Jobs:     []string{"https://ci.example.com/job1"},
						Lookback: "24h", // Override global default
					},
					{
						Jobs: []string{"https://ci.example.com/job2"},
						// No lookback — should inherit global default
					},
				},
			},
		},
	}

	entries := BuildRoleEntries(fc, "/tmp/work", Config{Agent: "claudecode", TriageLookback: defaultLookback})
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	// First triage entry overrides with role-level lookback
	if entries[0].Config.TriageLookback != overrideLookback {
		t.Errorf("expected TriageLookback %v, got %v", overrideLookback, entries[0].Config.TriageLookback)
	}
	// Second triage entry inherits global default lookback
	if entries[1].Config.TriageLookback != defaultLookback {
		t.Errorf("expected TriageLookback %v, got %v", defaultLookback, entries[1].Config.TriageLookback)
	}
}

func TestBuildRoleEntries_SkipChecksTwoTierInheritance(t *testing.T) {
	fc := &FileConfig{
		Projects: []ProjectConfig{
			{
				Repo:       "owner/repo",
				SkipChecks: []string{"can-be-merged"},
				PRs: []PRsRoleConfig{
					{
						Watch: []int{100},
						// Inherits project-level skip-checks
					},
					{
						Watch:      []int{200},
						SkipChecks: []string{"can-be-merged", "verified"}, // Override
					},
				},
			},
		},
	}

	entries := BuildRoleEntries(fc, "/tmp/work", Config{Agent: "claudecode"})
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	// First entry inherits project-level skip-checks
	e1 := entries[0]
	if len(e1.Config.SkipChecks) != 1 || e1.Config.SkipChecks[0] != "can-be-merged" {
		t.Errorf("expected inherited skip-checks [can-be-merged], got %v", e1.Config.SkipChecks)
	}
	// Second entry overrides with role-level skip-checks
	e2 := entries[1]
	if len(e2.Config.SkipChecks) != 2 || e2.Config.SkipChecks[0] != "can-be-merged" || e2.Config.SkipChecks[1] != "verified" {
		t.Errorf("expected overridden skip-checks [can-be-merged verified], got %v", e2.Config.SkipChecks)
	}
}

func TestBuildRoleEntries_SkipChecksIssuesAndTriageInheritance(t *testing.T) {
	fc := &FileConfig{
		Projects: []ProjectConfig{
			{
				Repo:       "owner/repo",
				SkipChecks: []string{"can-be-merged"},
				Issues: []IssuesRoleConfig{
					{
						Label: "ai",
						// Inherits project-level skip-checks
					},
					{
						Label:      "special",
						SkipChecks: []string{"can-be-merged", "verified"}, // Override
					},
				},
				Triage: []TriageRoleConfig{
					{
						Jobs: []string{"https://ci.example.com/job1"},
						// Inherits project-level skip-checks
					},
					{
						Jobs:       []string{"https://ci.example.com/job2"},
						SkipChecks: []string{"e2e-check"}, // Override
					},
				},
			},
		},
	}

	entries := BuildRoleEntries(fc, "/tmp/work", Config{Agent: "claudecode"})
	if len(entries) != 4 {
		t.Fatalf("expected 4 entries, got %d", len(entries))
	}
	// Issues[0]: inherits project-level skip-checks
	if len(entries[0].Config.SkipChecks) != 1 || entries[0].Config.SkipChecks[0] != "can-be-merged" {
		t.Errorf("Issues[0]: expected inherited skip-checks [can-be-merged], got %v", entries[0].Config.SkipChecks)
	}
	// Issues[1]: overrides with role-level skip-checks
	if len(entries[1].Config.SkipChecks) != 2 || entries[1].Config.SkipChecks[0] != "can-be-merged" || entries[1].Config.SkipChecks[1] != "verified" {
		t.Errorf("Issues[1]: expected overridden skip-checks [can-be-merged verified], got %v", entries[1].Config.SkipChecks)
	}
	// Triage[0]: inherits project-level skip-checks
	if len(entries[2].Config.SkipChecks) != 1 || entries[2].Config.SkipChecks[0] != "can-be-merged" {
		t.Errorf("Triage[0]: expected inherited skip-checks [can-be-merged], got %v", entries[2].Config.SkipChecks)
	}
	// Triage[1]: overrides with role-level skip-checks
	if len(entries[3].Config.SkipChecks) != 1 || entries[3].Config.SkipChecks[0] != "e2e-check" {
		t.Errorf("Triage[1]: expected overridden skip-checks [e2e-check], got %v", entries[3].Config.SkipChecks)
	}
}

func TestLoadFileConfig_SkipChecksAccepted(t *testing.T) {
	yaml := `
projects:
  - repo: owner/repo
    skip-checks:
      - can-be-merged
    prs:
      - watch: [1]
        skip-checks:
          - verified
    issues:
      - label: ai
        skip-checks:
          - e2e-check
    triage:
      - jobs: [https://ci.example.com/job]
        skip-checks:
          - lint-check
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte(yaml), 0o644) //nolint:errcheck // test helper: WriteFile errors are caught by subsequent LoadFileConfig

	cfg, err := LoadFileConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(cfg.Projects[0].SkipChecks) != 1 || cfg.Projects[0].SkipChecks[0] != "can-be-merged" {
		t.Errorf("expected project skip-checks [can-be-merged], got %v", cfg.Projects[0].SkipChecks)
	}
	if len(cfg.Projects[0].PRs[0].SkipChecks) != 1 || cfg.Projects[0].PRs[0].SkipChecks[0] != "verified" {
		t.Errorf("expected PR skip-checks [verified], got %v", cfg.Projects[0].PRs[0].SkipChecks)
	}
	if len(cfg.Projects[0].Issues[0].SkipChecks) != 1 || cfg.Projects[0].Issues[0].SkipChecks[0] != "e2e-check" {
		t.Errorf("expected Issues skip-checks [e2e-check], got %v", cfg.Projects[0].Issues[0].SkipChecks)
	}
	if len(cfg.Projects[0].Triage[0].SkipChecks) != 1 || cfg.Projects[0].Triage[0].SkipChecks[0] != "lint-check" {
		t.Errorf("expected Triage skip-checks [lint-check], got %v", cfg.Projects[0].Triage[0].SkipChecks)
	}
}

func TestLoadFileConfig_UnknownKeysRejected(t *testing.T) {
	yaml := `
agent: opencode
unknown-field: some-value
projects:
  - repo: owner/repo
    issues:
      - label: test
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte(yaml), 0o644) //nolint:errcheck // test helper: WriteFile errors are caught by subsequent LoadFileConfig

	_, err := LoadFileConfig(path)
	if err == nil {
		t.Fatal("expected error for unknown YAML key")
	}
}
