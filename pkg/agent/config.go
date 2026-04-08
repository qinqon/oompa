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
	DryRun        bool
	SignedOffBy   string
	GitHubUser    string   // authenticated GitHub username (for reaction checks)
	Reviewers     []string // whitelist of users/bots whose reviews to address
}
