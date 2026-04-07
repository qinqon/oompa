package agent

import "time"

// Config holds all agent configuration.
type Config struct {
	Owner         string
	Repo          string
	Label         string
	CloneDir      string
	StatePath     string
	PollInterval  time.Duration
	VertexRegion  string
	VertexProject string
	LogLevel      string
	DryRun        bool
	SignedOffBy   string
	Reviewers     []string // whitelist of users/bots whose reviews to address
}
