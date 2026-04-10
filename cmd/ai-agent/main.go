package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"github.com/qinqon/github-issue-resolver/pkg/agent"
)

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func parseConfig() agent.Config {
	cfg := agent.Config{}

	flag.StringVar(&cfg.Owner, "owner", envOrDefault("AI_AGENT_OWNER", "openperouter"), "GitHub repo owner")
	flag.StringVar(&cfg.Repo, "repo", envOrDefault("AI_AGENT_REPO", "openperouter"), "GitHub repo name")
	flag.StringVar(&cfg.Label, "label", envOrDefault("AI_AGENT_LABEL", "good-for-ai"), "Issue label to watch")
	flag.StringVar(&cfg.CloneDir, "clone-dir", envOrDefault("AI_AGENT_CLONE_DIR", os.ExpandEnv("$HOME/ai-agent-work")), "Clone/worktree directory")
	flag.DurationVar(&cfg.PollInterval, "poll-interval", parseDuration(envOrDefault("AI_AGENT_POLL_INTERVAL", "2m")), "Poll frequency")
	flag.StringVar(&cfg.VertexRegion, "vertex-region", os.Getenv("CLOUD_ML_REGION"), "GCP Vertex AI region")
	flag.StringVar(&cfg.VertexProject, "vertex-project", os.Getenv("ANTHROPIC_VERTEX_PROJECT_ID"), "GCP project ID for Vertex")
	flag.StringVar(&cfg.LogLevel, "log-level", envOrDefault("AI_AGENT_LOG_LEVEL", "info"), "Log level (debug, info, warn, error)")
	flag.BoolVar(&cfg.DryRun, "dry-run", false, "Log what would be done without executing")
	flag.BoolVar(&cfg.OneShot, "one-shot", false, "Run one cycle and exit")
	flag.StringVar(&cfg.SignedOffBy, "signed-off-by", os.Getenv("AI_AGENT_SIGNED_OFF_BY"), "Signed-off-by value for commits (e.g. \"Name <email>\")")

	var reviewers string
	flag.StringVar(&reviewers, "reviewers", os.Getenv("AI_AGENT_REVIEWERS"), "Comma-separated whitelist of users/bots whose reviews to address (empty = all)")

	var logFile string
	flag.StringVar(&logFile, "log-file", os.Getenv("AI_AGENT_LOG_FILE"), "Log file path (default: stderr)")

	// GitHub App auth flags
	var ghAppID int64
	var ghAppPrivateKey string
	var ghAppInstallationID int64
	if v := os.Getenv("GITHUB_APP_ID"); v != "" {
		ghAppID, _ = strconv.ParseInt(v, 10, 64)
	}
	flag.Int64Var(&ghAppID, "github-app-id", ghAppID, "GitHub App ID")
	flag.StringVar(&ghAppPrivateKey, "github-app-private-key", os.Getenv("GITHUB_APP_PRIVATE_KEY_PATH"), "Path to GitHub App private key PEM file")
	if v := os.Getenv("GITHUB_APP_INSTALLATION_ID"); v != "" {
		ghAppInstallationID, _ = strconv.ParseInt(v, 10, 64)
	}
	flag.Int64Var(&ghAppInstallationID, "github-app-installation-id", ghAppInstallationID, "GitHub App installation ID")

	flag.Parse()

	cfg.GitHubAppID = ghAppID
	cfg.GitHubAppPrivateKeyPath = ghAppPrivateKey
	cfg.GitHubAppInstallationID = ghAppInstallationID

	cfg.LogFile = logFile

	if reviewers != "" {
		for _, r := range strings.Split(reviewers, ",") {
			r = strings.TrimSpace(r)
			if r != "" {
				cfg.Reviewers = append(cfg.Reviewers, r)
			}
		}
	}

	return cfg
}

func parseDuration(s string) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil {
		return 2 * time.Minute
	}
	return d
}

func setupLogger(level, logFile string) (*slog.Logger, func()) {
	var logLevel slog.Level
	switch level {
	case "debug":
		logLevel = slog.LevelDebug
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	default:
		logLevel = slog.LevelInfo
	}

	w := os.Stderr
	cleanup := func() {}

	if logFile != "" {
		f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to open log file %s: %v\n", logFile, err)
			os.Exit(1)
		}
		w = f
		cleanup = func() { f.Close() }
	}

	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: logLevel})), cleanup
}

func main() {
	cfg := parseConfig()
	logger, closeLog := setupLogger(cfg.LogLevel, cfg.LogFile)
	defer closeLog()

	if cfg.VertexRegion == "" || cfg.VertexProject == "" {
		logger.Error("CLOUD_ML_REGION and ANTHROPIC_VERTEX_PROJECT_ID are required")
		os.Exit(1)
	}

	useAppAuth := cfg.GitHubAppID != 0 && cfg.GitHubAppPrivateKeyPath != "" && cfg.GitHubAppInstallationID != 0

	var ghClient *agent.GoGitHubClient
	var tokenFunc func(context.Context) (string, error)

	if useAppAuth {
		appAuth, err := agent.NewGitHubAppAuth(cfg.GitHubAppID, cfg.GitHubAppInstallationID, cfg.GitHubAppPrivateKeyPath)
		if err != nil {
			logger.Error("failed to set up GitHub App auth", "error", err)
			os.Exit(1)
		}

		ghClient = appAuth.Client
		tokenFunc = appAuth.TokenFunc
		cfg.GitHubUser = appAuth.Login
		cfg.GitHubHeadOwner = cfg.Owner // App pushes directly to upstream, no fork
		cfg.GitAuthorName = appAuth.Name
		cfg.GitAuthorEmail = appAuth.Email
		if cfg.SignedOffBy == "" {
			cfg.SignedOffBy = fmt.Sprintf("%s <%s>", appAuth.Name, appAuth.Email)
		}

		// Get an initial token for GH_TOKEN and git credential helpers
		token, err := appAuth.TokenFunc(context.Background())
		if err != nil {
			logger.Error("failed to get initial installation token", "error", err)
			os.Exit(1)
		}
		cfg.GitHubToken = token

		logger.Info("authenticated as GitHub App", "login", appAuth.Login, "signed-off-by", cfg.SignedOffBy)
	} else {
		token := os.Getenv("GITHUB_TOKEN")
		if token == "" {
			logger.Error("GITHUB_TOKEN is required (or configure GitHub App auth with --github-app-id, --github-app-private-key, --github-app-installation-id)")
			os.Exit(1)
		}
		cfg.GitHubToken = token

		ghClient = agent.NewGoGitHubClient(token)
		if login, name, email, err := ghClient.GetAuthenticatedUser(context.Background()); err == nil {
			cfg.GitHubUser = login
			cfg.GitHubHeadOwner = login // PAT pushes to user's fork
			cfg.GitAuthorName = name
			cfg.GitAuthorEmail = email
			if cfg.SignedOffBy == "" {
				cfg.SignedOffBy = fmt.Sprintf("%s <%s>", name, email)
			}
			logger.Info("authenticated as GitHub user", "login", login, "signed-off-by", cfg.SignedOffBy)
		} else {
			logger.Error("could not fetch GitHub user", "error", err)
			os.Exit(1)
		}
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	// Build initial state from GitHub
	logger.Info("rebuilding state from GitHub...")
	state := agent.BuildStateFromGitHub(ctx, ghClient, cfg, cfg.CloneDir, logger)
	logger.Info("state rebuilt", "active-issues", len(state.ActiveIssues))

	repoURL := fmt.Sprintf("https://github.com/%s/%s.git", cfg.Owner, cfg.Repo)
	forkURL := repoURL // default: same-repo workflow (GitHub App)
	if !useAppAuth {
		forkURL = fmt.Sprintf("https://github.com/%s/%s.git", cfg.GitHubUser, cfg.Repo)
	}
	runner := &agent.ExecRunner{}

	wtm := agent.NewGitWorktreeManager(runner, cfg.CloneDir, repoURL, forkURL)
	if cfg.GitAuthorName != "" || cfg.GitAuthorEmail != "" {
		wtm.SetGitIdentity(cfg.GitAuthorName, cfg.GitAuthorEmail)
	}

	a := agent.NewAgent(
		ghClient,
		runner,
		wtm,
		state,
		cfg,
		logger,
	)

	if tokenFunc != nil {
		a.SetTokenFunc(tokenFunc)
	}

	logger.Info("starting ai-agent",
		"owner", cfg.Owner,
		"repo", cfg.Repo,
		"label", cfg.Label,
		"poll-interval", cfg.PollInterval,
		"dry-run", cfg.DryRun,
		"auth-mode", map[bool]string{true: "github-app", false: "pat"}[useAppAuth],
	)

	runLoop(ctx, a, logger)

	if cfg.OneShot {
		return
	}

	ticker := time.NewTicker(cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("shutting down")
			return
		case <-ticker.C:
			runLoop(ctx, a, logger)
		}
	}
}

func runLoop(ctx context.Context, a *agent.Agent, logger *slog.Logger) {
	logger.Debug("starting poll cycle")
	if err := a.RefreshToken(ctx); err != nil {
		logger.Error("failed to refresh GitHub token", "error", err)
	}
	a.CleanupDone(ctx)
	a.ProcessNewIssues(ctx)
	a.ProcessReviewComments(ctx)
	a.ProcessConflicts(ctx)
	a.ProcessCIFailures(ctx)
	logger.Debug("poll cycle complete")
}
