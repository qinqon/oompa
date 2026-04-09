package agent

import "time"

// Config holds all agent configuration.
type Config struct {
	Owner         string
	Repo          string
	Label         string
	CloneDir      string
	PollInterval  time.Duration
	VertexRegion  string
	VertexProject string
	LogLevel      string
	LogFile       string
	DryRun        bool
	OneShot       bool
	SignedOffBy   string
	GitHubUser    string   // authenticated GitHub username (for reaction checks)
	GitHubToken   string   // GitHub token (passed to Claude for gh CLI access)
	Reviewers     []string // whitelist of users/bots whose reviews to address
}
