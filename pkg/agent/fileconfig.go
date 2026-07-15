package agent

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// FileConfig represents the top-level YAML configuration file.
type FileConfig struct {
	Agent            string          `yaml:"agent"`
	AgentModel       string          `yaml:"agent-model"`
	PollInterval     string          `yaml:"poll-interval"`
	LogLevel         string          `yaml:"log-level"`
	ExitOnNewVersion string          `yaml:"exit-on-new-version"`
	DryRun           bool            `yaml:"dry-run"`
	OneShot          bool            `yaml:"one-shot"`
	Projects         []ProjectConfig `yaml:"projects"`
}

// ProjectConfig represents a single project in the YAML config.
type ProjectConfig struct {
	Repo       string `yaml:"repo"`        // "owner/repo"
	Fork       string `yaml:"fork"`        // "owner/repo" for fork
	AgentModel string `yaml:"agent-model"` // model override for this project (empty = inherit global)

	// Project-level defaults (inherited by roles unless overridden)
	CreateFlakyIssues *bool    `yaml:"create-flaky-issues"`
	FlakyLabel        string   `yaml:"flaky-label"`
	SkipComment       []string `yaml:"skip-comment"`
	SkipChecks        []string `yaml:"skip-checks"`
	SkipFix           *bool    `yaml:"skip-fix"`
	Reactions         []string `yaml:"reactions"`
	Label             string   `yaml:"label"`
	OnlyAssigned      *bool    `yaml:"only-assigned"`
	Reviewers         []string `yaml:"reviewers"`       // whitelist of users/bots whose reviews to address
	RebaseInterval    string   `yaml:"rebase-interval"` // e.g. "24h", "12h" — minimum time between rebases

	// Role arrays
	PRs    []PRsRoleConfig    `yaml:"prs"`
	Issues []IssuesRoleConfig `yaml:"issues"`
	Triage []TriageRoleConfig `yaml:"triage"`
}

// PRsRoleConfig represents a single PRs role entry.
type PRsRoleConfig struct {
	Watch             []int    `yaml:"watch"`
	Reactions         []string `yaml:"reactions"`
	SkipComment       []string `yaml:"skip-comment"`
	SkipChecks        []string `yaml:"skip-checks"`
	SkipFix           *bool    `yaml:"skip-fix"`
	CreateFlakyIssues *bool    `yaml:"create-flaky-issues"`
	FlakyLabel        string   `yaml:"flaky-label"`
	Reviewers         []string `yaml:"reviewers"`       // overrides project-level reviewers
	RebaseInterval    string   `yaml:"rebase-interval"` // overrides project-level rebase-interval
}

// IssuesRoleConfig represents a single Issues role entry.
type IssuesRoleConfig struct {
	Label             string   `yaml:"label"`
	OnlyAssigned      *bool    `yaml:"only-assigned"`
	SkipFix           *bool    `yaml:"skip-fix"`
	SkipComment       []string `yaml:"skip-comment"`
	SkipChecks        []string `yaml:"skip-checks"`
	Fork              string   `yaml:"fork"`
	CreateFlakyIssues *bool    `yaml:"create-flaky-issues"`
	FlakyLabel        string   `yaml:"flaky-label"`
	Reviewers         []string `yaml:"reviewers"` // overrides project-level reviewers
}

// TriageRoleConfig represents a single Triage role entry.
type TriageRoleConfig struct {
	Jobs              []string `yaml:"jobs"`     // existing: full URLs for Prow/GCS/cross-repo
	Workflow          string   `yaml:"workflow"` // GHA workflow file (relative to project repo)
	Lanes             []string `yaml:"lanes"`    // glob patterns for matrix job names
	Schedule          string   `yaml:"schedule"`
	Lookback          string   `yaml:"lookback"`
	CreateFlakyIssues *bool    `yaml:"create-flaky-issues"`
	FlakyLabel        string   `yaml:"flaky-label"`
	SkipComment       []string `yaml:"skip-comment"`
	SkipChecks        []string `yaml:"skip-checks"`
	SkipFix           *bool    `yaml:"skip-fix"`
	Reviewers         []string `yaml:"reviewers"` // overrides project-level reviewers
}

// LoadFileConfig reads and parses a YAML config file, returning the validated FileConfig.
func LoadFileConfig(path string) (*FileConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	var cfg FileConfig
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	if err := validateFileConfig(&cfg); err != nil {
		return nil, fmt.Errorf("validating config: %w", err)
	}

	return &cfg, nil
}

// validateFileConfig validates the parsed config for required fields and correct values.
func validateFileConfig(cfg *FileConfig) error {
	if len(cfg.Projects) == 0 {
		return fmt.Errorf("no projects defined")
	}

	if cfg.Agent != "" && cfg.Agent != "claudecode" && cfg.Agent != "opencode" {
		return fmt.Errorf("invalid agent %q: must be claudecode or opencode", cfg.Agent)
	}

	if cfg.AgentModel != "" {
		// agent-model is only valid with opencode. When agent is omitted in the
		// file config, it inherits the global config; the resolved combination
		// is validated when the code agent is selected.
		if cfg.Agent != "" && cfg.Agent != "opencode" {
			return fmt.Errorf("agent-model can only be used with agent: opencode")
		}
	}

	if cfg.PollInterval != "" {
		if _, err := time.ParseDuration(cfg.PollInterval); err != nil {
			return fmt.Errorf("invalid poll-interval %q: %w", cfg.PollInterval, err)
		}
	}

	// Validate project-level rebase-interval
	for i, p := range cfg.Projects {
		if p.RebaseInterval != "" {
			d, err := time.ParseDuration(p.RebaseInterval)
			if err != nil {
				return fmt.Errorf("project %d (%s): invalid rebase-interval %q: %w", i, p.Repo, p.RebaseInterval, err)
			}
			if d <= 0 {
				return fmt.Errorf("project %d (%s): rebase-interval must be positive, got %q", i, p.Repo, p.RebaseInterval)
			}
		}
	}

	validReactions := map[string]bool{"reviews": true, "ci": true, "conflicts": true, "rebase": true}
	validComments := map[string]bool{
		"ci-unrelated": true, "ci-infrastructure": true, "ci-related": true,
		"conflict": true, "rebase": true, "flaky": true, "issue-in-progress": true,
	}

	for i, p := range cfg.Projects {
		if p.Repo == "" {
			return fmt.Errorf("project %d: repo is required", i)
		}
		parts := strings.SplitN(p.Repo, "/", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return fmt.Errorf("project %d: repo must be owner/repo, got %q", i, p.Repo)
		}

		if p.Fork != "" {
			forkParts := strings.SplitN(p.Fork, "/", 2)
			if len(forkParts) != 2 || forkParts[0] == "" || forkParts[1] == "" {
				return fmt.Errorf("project %d: fork must be owner/repo, got %q", i, p.Fork)
			}
		}

		// Validate project-level agent-model: incompatible with claudecode.
		// When agent is omitted it inherits the global config; the resolved
		// combination is validated when the code agent is selected.
		if p.AgentModel != "" {
			if cfg.Agent != "" && cfg.Agent != "opencode" {
				return fmt.Errorf("project %d (%s): agent-model can only be used with agent: opencode", i, p.Repo)
			}
		}

		// Validate project-level reactions
		for _, r := range p.Reactions {
			if !validReactions[r] {
				return fmt.Errorf("project %d: invalid reaction %q", i, r)
			}
		}

		// Validate project-level skip-comment
		for _, c := range p.SkipComment {
			if !validComments[c] {
				return fmt.Errorf("project %d: invalid skip-comment %q", i, c)
			}
		}

		if len(p.PRs) == 0 && len(p.Issues) == 0 && len(p.Triage) == 0 {
			return fmt.Errorf("project %d (%s): at least one role (prs, issues, triage) is required", i, p.Repo)
		}

		for j, pr := range p.PRs {
			if len(pr.Watch) == 0 {
				return fmt.Errorf("project %d (%s): prs[%d]: watch is required", i, p.Repo, j)
			}
			for _, r := range pr.Reactions {
				if !validReactions[r] {
					return fmt.Errorf("project %d (%s): prs[%d]: invalid reaction %q", i, p.Repo, j, r)
				}
			}
			for _, c := range pr.SkipComment {
				if !validComments[c] {
					return fmt.Errorf("project %d (%s): prs[%d]: invalid skip-comment %q", i, p.Repo, j, c)
				}
			}
			if pr.RebaseInterval != "" {
				d, err := time.ParseDuration(pr.RebaseInterval)
				if err != nil {
					return fmt.Errorf("project %d (%s): prs[%d]: invalid rebase-interval %q: %w", i, p.Repo, j, pr.RebaseInterval, err)
				}
				if d <= 0 {
					return fmt.Errorf("project %d (%s): prs[%d]: rebase-interval must be positive, got %q", i, p.Repo, j, pr.RebaseInterval)
				}
			}
		}

		for j, issue := range p.Issues {
			for _, c := range issue.SkipComment {
				if !validComments[c] {
					return fmt.Errorf("project %d (%s): issues[%d]: invalid skip-comment %q", i, p.Repo, j, c)
				}
			}
			if issue.Fork != "" {
				forkParts := strings.SplitN(issue.Fork, "/", 2)
				if len(forkParts) != 2 || forkParts[0] == "" || forkParts[1] == "" {
					return fmt.Errorf("project %d (%s): issues[%d]: fork must be owner/repo, got %q", i, p.Repo, j, issue.Fork)
				}
			}
		}

		for j, triage := range p.Triage {
			hasJobs := len(triage.Jobs) > 0
			hasWorkflow := triage.Workflow != ""
			hasLanes := len(triage.Lanes) > 0

			// Validate: must have either jobs or (workflow + lanes), not both, not neither
			if hasJobs && (hasWorkflow || hasLanes) {
				return fmt.Errorf("project %d (%s): triage[%d]: cannot specify both jobs and workflow/lanes", i, p.Repo, j)
			}
			if !hasJobs && !hasWorkflow && !hasLanes {
				return fmt.Errorf("project %d (%s): triage[%d]: either jobs or workflow+lanes is required", i, p.Repo, j)
			}
			if hasWorkflow && !hasLanes {
				return fmt.Errorf("project %d (%s): triage[%d]: workflow requires lanes", i, p.Repo, j)
			}
			if hasLanes && !hasWorkflow {
				return fmt.Errorf("project %d (%s): triage[%d]: lanes requires workflow", i, p.Repo, j)
			}
			if triage.Schedule != "" {
				if _, err := ParseSchedule(triage.Schedule, time.Now()); err != nil {
					return fmt.Errorf("project %d (%s): triage[%d]: %w", i, p.Repo, j, err)
				}
			}
			if triage.Lookback != "" {
				d, err := time.ParseDuration(triage.Lookback)
				if err != nil {
					return fmt.Errorf("project %d (%s): triage[%d]: invalid lookback %q: %w", i, p.Repo, j, triage.Lookback, err)
				}
				if d < 0 {
					return fmt.Errorf("project %d (%s): triage[%d]: lookback must be >= 0, got %q", i, p.Repo, j, triage.Lookback)
				}
			}
			for _, c := range triage.SkipComment {
				if !validComments[c] {
					return fmt.Errorf("project %d (%s): triage[%d]: invalid skip-comment %q", i, p.Repo, j, c)
				}
			}
		}
	}

	return nil
}

// boolOr returns the value of ptr if non-nil, otherwise the fallback.
func boolOr(ptr *bool, fallback bool) bool {
	if ptr != nil {
		return *ptr
	}
	return fallback
}

// stringOr returns s if non-empty, otherwise the fallback.
func stringOr(s, fallback string) string {
	if s != "" {
		return s
	}
	return fallback
}

// stringsOr returns s if non-nil, otherwise the fallback.
func stringsOr(s, fallback []string) []string {
	if s != nil {
		return s
	}
	return fallback
}

// parseDurationOr parses s as a time.Duration. If s is empty or invalid,
// it returns the fallback value.
func parseDurationOr(s string, fallback time.Duration) time.Duration {
	if s == "" {
		return fallback
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return fallback
	}
	return d
}

// RoleEntry describes a single role goroutine to run. Built from FileConfig by
// expanding projects and roles with two-tier inheritance applied.
type RoleEntry struct {
	Config Config // Agent config with all values resolved
	Role   string // "prs", "issues", "triage"

	// Triage scheduling (only for role == "triage")
	Schedule string
}

// BuildRoleEntries expands a FileConfig into a flat list of RoleEntry values,
// one per goroutine. Two-tier inheritance is applied: project-level defaults
// are used unless the role-level value overrides them.
func BuildRoleEntries(fc *FileConfig, baseCloneDir string, globalCfg Config) []RoleEntry {
	var entries []RoleEntry

	// Global defaults from FileConfig
	agent := stringOr(fc.Agent, globalCfg.Agent)
	agentModel := stringOr(fc.AgentModel, globalCfg.AgentModel)
	pollInterval := globalCfg.PollInterval
	if fc.PollInterval != "" {
		if d, err := time.ParseDuration(fc.PollInterval); err == nil {
			pollInterval = d
		}
	}
	logLevel := stringOr(fc.LogLevel, globalCfg.LogLevel)

	for _, p := range fc.Projects {
		repoParts := strings.SplitN(p.Repo, "/", 2)
		owner, repo := repoParts[0], repoParts[1]

		// Parse project-level fork
		var projForkOwner, projForkRepo string
		if p.Fork != "" {
			forkParts := strings.SplitN(p.Fork, "/", 2)
			projForkOwner = forkParts[0]
			projForkRepo = forkParts[1]
		}

		// Project-level defaults
		projCreateFlaky := boolOr(p.CreateFlakyIssues, false)
		projFlakyLabel := stringOr(p.FlakyLabel, "flaky-test")
		projSkipComment := p.SkipComment
		projSkipChecks := p.SkipChecks
		projSkipFix := boolOr(p.SkipFix, false)
		projReactions := p.Reactions
		projLabel := stringOr(p.Label, "good-for-ai")
		projOnlyAssigned := boolOr(p.OnlyAssigned, false)
		projReviewers := stringsOr(p.Reviewers, globalCfg.Reviewers)
		projRebaseInterval := parseDurationOr(p.RebaseInterval, 4*time.Hour)
		projAgentModel := stringOr(p.AgentModel, agentModel)

		// Base config for this project (shared fields)
		baseCfg := Config{
			Owner:        owner,
			Repo:         repo,
			CloneDir:     fmt.Sprintf("%s/%s/%s", baseCloneDir, owner, repo),
			PollInterval: pollInterval,
			LogLevel:     logLevel,
			DryRun:       fc.DryRun || globalCfg.DryRun,
			OneShot:      fc.OneShot || globalCfg.OneShot,
			Agent:        agent,
			AgentModel:   projAgentModel,
			ForkOwner:    projForkOwner,
			ForkRepo:     projForkRepo,
			// These are inherited from the global config (set by main from auth)
			GitHubUser:       globalCfg.GitHubUser,
			GitHubHeadOwner:  globalCfg.GitHubHeadOwner,
			GitAuthorName:    globalCfg.GitAuthorName,
			GitAuthorEmail:   globalCfg.GitAuthorEmail,
			SignedOffBy:      globalCfg.SignedOffBy,
			AssistedBy:       globalCfg.AssistedBy,
			Reviewers:        projReviewers,
			Version:          globalCfg.Version,
			MaxReviewNoOps:   globalCfg.MaxReviewNoOps,
			MaxPRSessionCost: globalCfg.MaxPRSessionCost,
			SlackWebhookURL:  globalCfg.SlackWebhookURL,
			// GitHub App auth (shared)
			GitHubAppID:             globalCfg.GitHubAppID,
			GitHubAppPrivateKey:     globalCfg.GitHubAppPrivateKey,
			GitHubAppInstallationID: globalCfg.GitHubAppInstallationID,
		}

		// Resolve GitHubHeadOwner per project
		if projForkOwner != "" {
			baseCfg.GitHubHeadOwner = projForkOwner
		}

		// PRs role entries
		for _, pr := range p.PRs {
			cfg := baseCfg
			cfg.Role = "prs"
			cfg.WatchPRs = pr.Watch
			cfg.Reactions = stringsOr(pr.Reactions, projReactions)
			cfg.SkipComments = stringsOr(pr.SkipComment, projSkipComment)
			cfg.SkipChecks = stringsOr(pr.SkipChecks, projSkipChecks)
			cfg.SkipFix = boolOr(pr.SkipFix, projSkipFix)
			cfg.CreateFlakyIssues = boolOr(pr.CreateFlakyIssues, projCreateFlaky)
			cfg.FlakyLabel = stringOr(pr.FlakyLabel, projFlakyLabel)
			cfg.Reviewers = stringsOr(pr.Reviewers, projReviewers)
			cfg.RebaseInterval = parseDurationOr(pr.RebaseInterval, projRebaseInterval)
			entries = append(entries, RoleEntry{Config: cfg, Role: "prs"})
		}

		// Issues role entries
		for _, issue := range p.Issues {
			cfg := baseCfg
			cfg.Role = "issues"
			cfg.Label = stringOr(issue.Label, projLabel)
			cfg.OnlyAssigned = boolOr(issue.OnlyAssigned, projOnlyAssigned)
			cfg.SkipFix = boolOr(issue.SkipFix, projSkipFix)
			cfg.SkipComments = stringsOr(issue.SkipComment, projSkipComment)
			cfg.SkipChecks = stringsOr(issue.SkipChecks, projSkipChecks)
			cfg.CreateFlakyIssues = boolOr(issue.CreateFlakyIssues, projCreateFlaky)
			cfg.FlakyLabel = stringOr(issue.FlakyLabel, projFlakyLabel)
			cfg.Reviewers = stringsOr(issue.Reviewers, projReviewers)
			// Issues can override fork
			if issue.Fork != "" {
				forkParts := strings.SplitN(issue.Fork, "/", 2)
				cfg.ForkOwner = forkParts[0]
				cfg.ForkRepo = forkParts[1]
				cfg.GitHubHeadOwner = forkParts[0]
			}
			entries = append(entries, RoleEntry{Config: cfg, Role: "issues"})
		}

		// Triage role entries
		for _, triage := range p.Triage {
			cfg := baseCfg
			cfg.Role = "triage"
			cfg.TriageJobs = triage.Jobs
			cfg.TriageWorkflow = triage.Workflow
			cfg.TriageLanePatterns = triage.Lanes
			cfg.CreateFlakyIssues = boolOr(triage.CreateFlakyIssues, projCreateFlaky)
			cfg.FlakyLabel = stringOr(triage.FlakyLabel, projFlakyLabel)
			cfg.SkipComments = stringsOr(triage.SkipComment, projSkipComment)
			cfg.SkipChecks = stringsOr(triage.SkipChecks, projSkipChecks)
			cfg.SkipFix = boolOr(triage.SkipFix, projSkipFix)
			cfg.TriageLookback = globalCfg.TriageLookback
			if triage.Lookback != "" {
				if d, err := time.ParseDuration(triage.Lookback); err == nil {
					cfg.TriageLookback = d
				}
			}
			cfg.Reviewers = stringsOr(triage.Reviewers, projReviewers)
			entries = append(entries, RoleEntry{
				Config:   cfg,
				Role:     "triage",
				Schedule: triage.Schedule,
			})
		}
	}

	return entries
}

// ParseSchedule parses a schedule string like "09:00 Europe/Madrid" or
// "09:00 Monday Europe/Madrid" and returns the next occurrence.
// Supported formats:
//   - "HH:MM TZ"           — daily at HH:MM in timezone TZ
//   - "HH:MM Weekday TZ"   — weekly on Weekday at HH:MM in timezone TZ
func ParseSchedule(schedule string, now time.Time) (time.Time, error) {
	parts := strings.Fields(schedule)
	if len(parts) < 2 {
		return time.Time{}, fmt.Errorf("invalid schedule %q: expected 'HH:MM timezone' or 'HH:MM weekday timezone'", schedule)
	}

	timePart := parts[0]
	var weekday *time.Weekday
	var tzName string

	switch len(parts) {
	case 2:
		// "HH:MM TZ" — daily
		tzName = parts[1]
	case 3:
		// "HH:MM Weekday TZ" — weekly
		wd, err := parseWeekday(parts[1])
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid schedule %q: %w", schedule, err)
		}
		weekday = &wd
		tzName = parts[2]
	default:
		return time.Time{}, fmt.Errorf("invalid schedule %q: too many parts", schedule)
	}

	loc, err := time.LoadLocation(tzName)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid schedule %q: unknown timezone %q: %w", schedule, tzName, err)
	}

	hourMin := strings.SplitN(timePart, ":", 2)
	if len(hourMin) != 2 {
		return time.Time{}, fmt.Errorf("invalid schedule %q: time must be HH:MM", schedule)
	}

	var hour, minute int
	if _, err := fmt.Sscanf(hourMin[0], "%d", &hour); err != nil || hour < 0 || hour > 23 {
		return time.Time{}, fmt.Errorf("invalid schedule %q: invalid hour", schedule)
	}
	if _, err := fmt.Sscanf(hourMin[1], "%d", &minute); err != nil || minute < 0 || minute > 59 {
		return time.Time{}, fmt.Errorf("invalid schedule %q: invalid minute", schedule)
	}

	nowLocal := now.In(loc)
	next := time.Date(nowLocal.Year(), nowLocal.Month(), nowLocal.Day(), hour, minute, 0, 0, loc)

	if weekday != nil {
		// Weekly: advance to the next matching weekday
		daysUntil := (int(*weekday) - int(next.Weekday()) + 7) % 7
		if daysUntil == 0 && !next.After(nowLocal) {
			daysUntil = 7
		}
		next = next.AddDate(0, 0, daysUntil)
	} else if !next.After(nowLocal) {
		// Daily: if the time has passed today, advance to tomorrow
		next = next.AddDate(0, 0, 1)
	}

	return next, nil
}

// parseWeekday parses a weekday name (case-insensitive).
func parseWeekday(s string) (time.Weekday, error) {
	switch strings.ToLower(s) {
	case "sunday":
		return time.Sunday, nil
	case "monday":
		return time.Monday, nil
	case "tuesday":
		return time.Tuesday, nil
	case "wednesday":
		return time.Wednesday, nil
	case "thursday":
		return time.Thursday, nil
	case "friday":
		return time.Friday, nil
	case "saturday":
		return time.Saturday, nil
	default:
		return 0, fmt.Errorf("unknown weekday %q", s)
	}
}

// NewRoleLogger creates a structured logger scoped to a role entry.
// Always includes project (owner/repo) and role. Per-role context fields
// (watch_prs, triage_jobs, label) are added when present.
func NewRoleLogger(base *slog.Logger, entry RoleEntry) *slog.Logger {
	project := fmt.Sprintf("%s/%s", entry.Config.Owner, entry.Config.Repo)
	logger := base.With("project", project, "role", entry.Role)

	switch entry.Role {
	case "prs":
		if len(entry.Config.WatchPRs) > 0 {
			logger = logger.With("watch_prs", entry.Config.WatchPRs)
		}
	case "issues":
		if entry.Config.Label != "" {
			logger = logger.With("label", entry.Config.Label)
		}
	case "triage":
		if len(entry.Config.TriageJobs) > 0 {
			logger = logger.With("triage_jobs", entry.Config.TriageJobs)
		}
		if entry.Schedule != "" {
			logger = logger.With("schedule", entry.Schedule)
		}
	}

	return logger
}
