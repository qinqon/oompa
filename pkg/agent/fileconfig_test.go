package agent

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// loadConfig writes yaml to a temp config file and loads it.
func loadConfig(t *testing.T, yaml string) (*FileConfig, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	return LoadFileConfig(path)
}

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
	fc, err := loadConfig(t, yaml)
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

// TestLoadFileConfig_Validation covers LoadFileConfig accept/reject
// behaviour: YAML parse errors, unknown keys, schema violations (projects,
// repo format, roles), and field-level validation of reactions, agent,
// agent-model, skip-comment, schedule, lookback, and rebase-interval.
func TestLoadFileConfig_Validation(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr bool
	}{
		{name: "no_projects", yaml: "agent: opencode\nprojects: []\n", wantErr: true},
		{name: "invalid_repo", yaml: "projects:\n  - repo: invalid\n    prs:\n      - watch: [1]\n", wantErr: true},
		{name: "no_roles", yaml: "projects:\n  - repo: owner/repo\n", wantErr: true},
		{name: "prs_without_watch", yaml: "projects:\n  - repo: owner/repo\n    prs:\n      - reactions: [ci]\n", wantErr: true},
		{name: "triage_without_jobs_or_workflow", yaml: "projects:\n  - repo: owner/repo\n    triage:\n      - schedule: \"09:00 Europe/Madrid\"\n", wantErr: true},
		{name: "triage_workflow_without_lanes", yaml: "projects:\n  - repo: owner/repo\n    triage:\n      - workflow: test.yml\n", wantErr: true},
		{name: "triage_lanes_without_workflow", yaml: "projects:\n  - repo: owner/repo\n    triage:\n      - lanes: [\"e2e*\"]\n", wantErr: true},
		{name: "triage_jobs_and_workflow", yaml: "projects:\n  - repo: owner/repo\n    triage:\n      - jobs: [\"https://ci.example.com/job1\"]\n        workflow: test.yml\n        lanes: [\"e2e*\"]\n", wantErr: true},
		{name: "invalid_reaction", yaml: "projects:\n  - repo: owner/repo\n    prs:\n      - watch: [1]\n        reactions: [invalid]\n", wantErr: true},
		{name: "invalid_agent", yaml: "agent: badagent\nprojects:\n  - repo: owner/repo\n    issues:\n      - label: test\n", wantErr: true},
		{name: "invalid_yaml", yaml: "{{invalid yaml", wantErr: true},
		{
			name: "unknown_keys_rejected",
			yaml: `
agent: opencode
unknown-field: some-value
projects:
  - repo: owner/repo
    issues:
      - label: test
`,
			wantErr: true,
		},
		{
			name: "invalid_skip_comment",
			yaml: `
projects:
  - repo: owner/repo
    skip-comment:
      - invalid-category
    prs:
      - watch: [1]
`,
			wantErr: true,
		},
		{
			name: "agent_model_without_opencode",
			yaml: `
agent: claudecode
agent-model: some-model
projects:
  - repo: owner/repo
    issues:
      - label: test
`,
			wantErr: true,
		},
		{
			// agent-model without an explicit agent is valid: the agent is
			// inherited from the global config and the resolved combination
			// is validated when the code agent is selected.
			name: "agent_model_without_agent_set",
			yaml: `
agent-model: some-model
projects:
  - repo: owner/repo
    issues:
      - label: test
`,
			wantErr: false,
		},
		{
			name: "project_agent_model_without_opencode",
			yaml: `
agent: claudecode
projects:
  - repo: owner/repo
    agent-model: some-model
    prs:
      - watch: [1]
`,
			wantErr: true,
		},
		{
			// Project-level agent-model without an explicit agent is valid:
			// the agent is inherited from the global config and the resolved
			// combination is validated when the code agent is selected.
			name: "project_agent_model_without_agent_set",
			yaml: `
projects:
  - repo: owner/repo
    agent-model: some-model
    prs:
      - watch: [1]
`,
			wantErr: false,
		},
		{
			name: "invalid_schedule_timezone",
			yaml: `
projects:
  - repo: owner/repo
    triage:
      - jobs: [https://ci.example.com/job]
        schedule: "09:00 Invalid/Timezone"
`,
			wantErr: true,
		},
		{
			name: "valid_schedule",
			yaml: `
projects:
  - repo: owner/repo
    triage:
      - jobs: [https://ci.example.com/job]
        schedule: "09:00 UTC"
`,
			wantErr: false,
		},
		{
			name: "invalid_lookback",
			yaml: `
projects:
  - repo: owner/repo
    triage:
      - jobs: [https://ci.example.com/job]
        lookback: "not-a-duration"
`,
			wantErr: true,
		},
		{
			name: "negative_lookback",
			yaml: `
projects:
  - repo: owner/repo
    triage:
      - jobs: [https://ci.example.com/job]
        lookback: "-1h"
`,
			wantErr: true,
		},
		{
			name: "rebase_interval_invalid_project",
			yaml: `
projects:
  - repo: owner/repo
    rebase-interval: not-a-duration
    prs:
      - watch: [1]
`,
			wantErr: true,
		},
		{
			name: "rebase_interval_negative_project",
			yaml: `
projects:
  - repo: owner/repo
    rebase-interval: "-1h"
    prs:
      - watch: [1]
`,
			wantErr: true,
		},
		{
			name: "rebase_interval_zero_project",
			yaml: `
projects:
  - repo: owner/repo
    rebase-interval: "0s"
    prs:
      - watch: [1]
`,
			wantErr: true,
		},
		{
			name: "rebase_interval_negative_pr",
			yaml: `
projects:
  - repo: owner/repo
    prs:
      - watch: [1]
        rebase-interval: "-2h"
`,
			wantErr: true,
		},
		{
			name: "rebase_interval_invalid_pr",
			yaml: `
projects:
  - repo: owner/repo
    prs:
      - watch: [1]
        rebase-interval: bad
`,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loadConfig(t, tt.yaml)
			if tt.wantErr && err == nil {
				t.Fatal("expected error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
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
	// Check clone dirs are per role entry — concurrent role goroutines must
	// never share a .git directory.
	if entries[0].Config.CloneDir != "/tmp/work/org1/repo1/prs" {
		t.Errorf("unexpected clone dir %q", entries[0].Config.CloneDir)
	}
	if entries[1].Config.CloneDir != "/tmp/work/org2/repo2/issues" {
		t.Errorf("unexpected clone dir %q", entries[1].Config.CloneDir)
	}
	if entries[2].Config.CloneDir != "/tmp/work/org2/repo2/triage" {
		t.Errorf("unexpected clone dir %q", entries[2].Config.CloneDir)
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
	fc, err := loadConfig(t, yaml)
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

func TestLoadFileConfig_FileNotFound(t *testing.T) {
	_, err := LoadFileConfig("/nonexistent/path.yaml")
	if err == nil {
		t.Fatal("expected error for missing file")
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
	fc, err := loadConfig(t, yaml)
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
	cfg, err := loadConfig(t, yaml)
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

func TestReactionsNilVsEmpty(t *testing.T) {
	// Issue #184: YAML `reactions: []` → non-nil empty; omitted → nil.
	loadPRReactions := func(t *testing.T, y string) []string {
		t.Helper()
		p := filepath.Join(t.TempDir(), "c.yaml")
		os.WriteFile(p, []byte(y), 0o644) //nolint:errcheck // test helper: errors caught by LoadFileConfig
		fc, err := LoadFileConfig(p)
		if err != nil {
			t.Fatal(err)
		}
		return fc.Projects[0].PRs[0].Reactions
	}
	if r := loadPRReactions(t, "projects:\n  - repo: o/r\n    prs:\n      - watch: [1]\n        reactions: []\n"); r == nil || len(r) != 0 {
		t.Errorf("reactions: [] should produce non-nil empty slice, got %#v", r)
	}
	if r := loadPRReactions(t, "projects:\n  - repo: o/r\n    prs:\n      - watch: [1]\n"); r != nil {
		t.Errorf("omitted reactions should be nil, got %v", r)
	}
	// stringsOr preserves distinction through BuildRoleEntries
	fc := &FileConfig{Projects: []ProjectConfig{{Repo: "o/r", PRs: []PRsRoleConfig{
		{Watch: []int{1}, Reactions: []string{}}, {Watch: []int{2}},
	}}}}
	e := BuildRoleEntries(fc, "/tmp/w", Config{Agent: "claudecode"})
	if len(e) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(e))
	}
	if e[0].Config.Reactions == nil || len(e[0].Config.Reactions) != 0 {
		t.Errorf("entry 0: expected non-nil empty slice, got %#v", e[0].Config.Reactions)
	}
	if e[1].Config.Reactions != nil {
		t.Errorf("entry 1: expected nil, got %v", e[1].Config.Reactions)
	}
}

func TestLoadFileConfig_RebaseIntervalValid(t *testing.T) {
	yaml := `
projects:
  - repo: owner/repo
    rebase-interval: 24h
    prs:
      - watch: [1]
        rebase-interval: 8h
`
	fc, err := loadConfig(t, yaml)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fc.Projects[0].RebaseInterval != "24h" {
		t.Errorf("expected project rebase-interval '24h', got %q", fc.Projects[0].RebaseInterval)
	}
	if fc.Projects[0].PRs[0].RebaseInterval != "8h" {
		t.Errorf("expected PR rebase-interval '8h', got %q", fc.Projects[0].PRs[0].RebaseInterval)
	}
}

func TestBuildRoleEntries_RebaseIntervalTwoTierInheritance(t *testing.T) {
	fc := &FileConfig{
		Projects: []ProjectConfig{
			{
				Repo:           "owner/repo",
				RebaseInterval: "24h",
				PRs: []PRsRoleConfig{
					{
						Watch: []int{100},
						// Inherits project-level rebase-interval (24h)
					},
					{
						Watch:          []int{200},
						RebaseInterval: "8h", // Override project level
					},
				},
			},
		},
	}

	entries := BuildRoleEntries(fc, "/tmp/work", Config{Agent: "claudecode"})
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	// First entry: inherits project-level 24h
	if entries[0].Config.RebaseInterval != 24*time.Hour {
		t.Errorf("expected RebaseInterval 24h, got %v", entries[0].Config.RebaseInterval)
	}
	// Second entry: overrides with 8h
	if entries[1].Config.RebaseInterval != 8*time.Hour {
		t.Errorf("expected RebaseInterval 8h, got %v", entries[1].Config.RebaseInterval)
	}
}

func TestBuildRoleEntries_RebaseIntervalDefault(t *testing.T) {
	fc := &FileConfig{
		Projects: []ProjectConfig{
			{
				Repo: "owner/repo",
				// No rebase-interval set
				PRs: []PRsRoleConfig{
					{Watch: []int{100}},
				},
			},
		},
	}

	entries := BuildRoleEntries(fc, "/tmp/work", Config{Agent: "claudecode"})
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	// Default should be 4h
	if entries[0].Config.RebaseInterval != 4*time.Hour {
		t.Errorf("expected RebaseInterval 4h (default), got %v", entries[0].Config.RebaseInterval)
	}
}

func TestBuildRoleEntries_AgentModelTwoTierInheritance(t *testing.T) {
	fc := &FileConfig{
		Agent:      "opencode",
		AgentModel: "global-model",
		Projects: []ProjectConfig{
			{
				Repo:       "org/with-override",
				AgentModel: "project-model", // Override global
				PRs:        []PRsRoleConfig{{Watch: []int{1}}},
				Issues:     []IssuesRoleConfig{{Label: "ai"}},
				Triage:     []TriageRoleConfig{{Jobs: []string{"https://ci.example.com/job"}}},
			},
			{
				Repo: "org/inherits-global",
				// No agent-model — inherits global
				PRs: []PRsRoleConfig{{Watch: []int{2}}},
			},
		},
	}

	entries := BuildRoleEntries(fc, "/tmp/work", Config{Agent: "claudecode", AgentModel: "cli-model"})
	if len(entries) != 4 {
		t.Fatalf("expected 4 entries, got %d", len(entries))
	}
	// First project: all roles get project-level model
	if entries[0].Config.AgentModel != "project-model" {
		t.Errorf("PRs: expected 'project-model', got %q", entries[0].Config.AgentModel)
	}
	if entries[1].Config.AgentModel != "project-model" {
		t.Errorf("Issues: expected 'project-model', got %q", entries[1].Config.AgentModel)
	}
	if entries[2].Config.AgentModel != "project-model" {
		t.Errorf("Triage: expected 'project-model', got %q", entries[2].Config.AgentModel)
	}
	// Second project: inherits global (file-level overrides CLI-level)
	if entries[3].Config.AgentModel != "global-model" {
		t.Errorf("Inherited: expected 'global-model', got %q", entries[3].Config.AgentModel)
	}
}

func TestLoadFileConfig_ProjectAgentModelValid(t *testing.T) {
	yaml := `
agent: opencode
agent-model: google-vertex-anthropic/claude-opus-4-6@default
projects:
  - repo: owner/repo
    agent-model: google-vertex-anthropic/claude-sonnet-4-20250514
    prs:
      - watch: [1]
`
	fc, err := loadConfig(t, yaml)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fc.Projects[0].AgentModel != "google-vertex-anthropic/claude-sonnet-4-20250514" {
		t.Errorf("expected project agent-model, got %q", fc.Projects[0].AgentModel)
	}
}

func TestParseDurationOr(t *testing.T) {
	tests := []struct {
		input    string
		fallback time.Duration
		want     time.Duration
	}{
		{"24h", 4 * time.Hour, 24 * time.Hour},
		{"8h", 4 * time.Hour, 8 * time.Hour},
		{"", 4 * time.Hour, 4 * time.Hour},
		{"invalid", 4 * time.Hour, 4 * time.Hour},
	}
	for _, tt := range tests {
		got := parseDurationOr(tt.input, tt.fallback)
		if got != tt.want {
			t.Errorf("parseDurationOr(%q, %v) = %v, want %v", tt.input, tt.fallback, got, tt.want)
		}
	}
}

func TestLoadFileConfig_TriageWorkflowLanes(t *testing.T) {
	yamlStr := `
projects:
  - repo: ovn-kubernetes/ovn-kubernetes
    flaky-label: kind/ci-flake
    triage:
      - workflow: test.yml
        lanes:
          - "e2e (kv-live-migration, noHA, local,*"
          - "e2e (kv-live-migration, noHA, shared,*"
        schedule: "09:00 Europe/Madrid"
        lookback: 24h
    prs:
      - watch: [6466]
        reactions: []
`
	fc, err := loadConfig(t, yamlStr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(fc.Projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(fc.Projects))
	}
	p := fc.Projects[0]
	if len(p.Triage) != 1 {
		t.Fatalf("expected 1 triage entry, got %d", len(p.Triage))
	}
	tr := p.Triage[0]
	if tr.Workflow != "test.yml" {
		t.Errorf("expected workflow 'test.yml', got %q", tr.Workflow)
	}
	if len(tr.Lanes) != 2 {
		t.Fatalf("expected 2 lanes, got %d", len(tr.Lanes))
	}
	if tr.Lanes[0] != "e2e (kv-live-migration, noHA, local,*" {
		t.Errorf("unexpected lane[0] %q", tr.Lanes[0])
	}
	if tr.Schedule != "09:00 Europe/Madrid" {
		t.Errorf("unexpected schedule %q", tr.Schedule)
	}
	if tr.Lookback != "24h" {
		t.Errorf("unexpected lookback %q", tr.Lookback)
	}
}

func TestBuildRoleEntries_TriageWorkflowLanes(t *testing.T) {
	fc := &FileConfig{
		Projects: []ProjectConfig{
			{
				Repo:       "ovn-kubernetes/ovn-kubernetes",
				FlakyLabel: "kind/ci-flake",
				Triage: []TriageRoleConfig{
					{
						Workflow: "test.yml",
						Lanes:    []string{"e2e (kv-live-migration,*"},
						Lookback: "24h",
						Schedule: "09:00 Europe/Madrid",
					},
				},
				PRs: []PRsRoleConfig{
					{Watch: []int{6466}, Reactions: []string{}},
				},
			},
		},
	}

	entries := BuildRoleEntries(fc, "/tmp/work", Config{Agent: "claudecode"})

	// Should produce 2 entries: 1 triage + 1 prs
	var triageEntries []RoleEntry
	for _, e := range entries {
		if e.Role == "triage" {
			triageEntries = append(triageEntries, e)
		}
	}
	if len(triageEntries) != 1 {
		t.Fatalf("expected 1 triage entry, got %d", len(triageEntries))
	}

	te := triageEntries[0]
	if te.Config.TriageWorkflow != "test.yml" {
		t.Errorf("expected TriageWorkflow 'test.yml', got %q", te.Config.TriageWorkflow)
	}
	if len(te.Config.TriageLanePatterns) != 1 || te.Config.TriageLanePatterns[0] != "e2e (kv-live-migration,*" {
		t.Errorf("unexpected TriageLanePatterns %v", te.Config.TriageLanePatterns)
	}
	if te.Config.TriageLookback != 24*time.Hour {
		t.Errorf("expected TriageLookback 24h, got %v", te.Config.TriageLookback)
	}
	if te.Config.FlakyLabel != "kind/ci-flake" {
		t.Errorf("expected FlakyLabel 'kind/ci-flake', got %q", te.Config.FlakyLabel)
	}
	if te.Schedule != "09:00 Europe/Madrid" {
		t.Errorf("expected Schedule '09:00 Europe/Madrid', got %q", te.Schedule)
	}
	if te.Config.Owner != "ovn-kubernetes" || te.Config.Repo != "ovn-kubernetes" {
		t.Errorf("expected owner/repo ovn-kubernetes/ovn-kubernetes, got %s/%s", te.Config.Owner, te.Config.Repo)
	}
	// TriageJobs should be empty (workflow+lanes mode, not jobs mode)
	if len(te.Config.TriageJobs) != 0 {
		t.Errorf("expected empty TriageJobs, got %v", te.Config.TriageJobs)
	}
}

func TestBuildRoleEntries_CloneDirsAreUniquePerEntry(t *testing.T) {
	// Concurrent role goroutines each build their own worktree manager whose
	// mutex only guards its own instance, so two entries sharing one clone
	// dir would run unserialized git operations on the same .git.
	fc := &FileConfig{
		Projects: []ProjectConfig{
			{
				Repo: "org/repo",
				PRs: []PRsRoleConfig{
					{Watch: []int{1}},
					{Watch: []int{2}},
				},
				Issues: []IssuesRoleConfig{{Label: "ai"}},
				Triage: []TriageRoleConfig{{Jobs: []string{"https://ci.example.com/job"}}},
			},
		},
	}

	entries := BuildRoleEntries(fc, "/tmp/work", Config{Agent: "claudecode"})
	if len(entries) != 4 {
		t.Fatalf("expected 4 entries, got %d", len(entries))
	}

	seen := map[string]string{}
	for _, e := range entries {
		dir := e.Config.CloneDir
		if dir == "" {
			t.Fatalf("entry %s has empty clone dir", e.Role)
		}
		if prev, dup := seen[dir]; dup {
			t.Errorf("clone dir %q shared by %s and %s entries", dir, prev, e.Role)
		}
		seen[dir] = e.Role
	}

	// Same-role entries are disambiguated by position.
	if entries[0].Config.CloneDir != "/tmp/work/org/repo/prs" {
		t.Errorf("unexpected first prs clone dir %q", entries[0].Config.CloneDir)
	}
	if entries[1].Config.CloneDir != "/tmp/work/org/repo/prs-2" {
		t.Errorf("unexpected second prs clone dir %q", entries[1].Config.CloneDir)
	}
}
