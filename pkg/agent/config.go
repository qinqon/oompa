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
	GitHubUser      string   // authenticated GitHub username (for reaction checks)
	GitHubToken     string   // GitHub token (passed to Claude for gh CLI access)
	GitHubHeadOwner string   // owner for PR head filtering (fork owner for PAT, repo owner for App)
	ForkOwner       string   // owner of the fork repo for pushing (empty = push to upstream)
	ForkRepo        string   // name of the fork repo (empty = same as Repo)
	GitAuthorName   string   // git commit author name
	GitAuthorEmail  string   // git commit author email
	Reviewers       []string // whitelist of users/bots whose reviews to address

	// GitHub App authentication (alternative to GITHUB_TOKEN)
	GitHubAppID             int64
	GitHubAppPrivateKey     []byte
	GitHubAppInstallationID int64
}
