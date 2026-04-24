package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/qinqon/oompa/pkg/agent"
)

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func parseIntEnv(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func parseConfig() (cfg agent.Config, exitOnNewVersion string) {
	cfg = agent.Config{}

	var repoFlag string
	flag.StringVar(&repoFlag, "repo", envOrDefault("OOMPA_REPO", ""), "GitHub repo as owner/repo (e.g. ovn-kubernetes/ovn-kubernetes)")
	flag.StringVar(&cfg.Label, "label", envOrDefault("OOMPA_LABEL", "good-for-ai"), "Issue label to watch")
	flag.StringVar(&cfg.CloneDir, "clone-dir", envOrDefault("OOMPA_CLONE_DIR", "/tmp/oompa-work"), "Base directory for clones (owner/repo appended automatically)")
	flag.DurationVar(&cfg.PollInterval, "poll-interval", parseDuration(envOrDefault("OOMPA_POLL_INTERVAL", "2m")), "Poll frequency")
	flag.StringVar(&cfg.VertexRegion, "vertex-region", os.Getenv("CLOUD_ML_REGION"), "GCP Vertex AI region")
	flag.StringVar(&cfg.VertexProject, "vertex-project", os.Getenv("ANTHROPIC_VERTEX_PROJECT_ID"), "GCP project ID for Vertex")
	flag.StringVar(&cfg.LogLevel, "log-level", envOrDefault("OOMPA_LOG_LEVEL", "info"), "Log level (debug, info, warn, error)")
	flag.BoolVar(&cfg.DryRun, "dry-run", false, "Log what would be done without executing")
	flag.BoolVar(&cfg.OneShot, "one-shot", false, "Run one cycle and exit")
	flag.StringVar(&cfg.SignedOffBy, "signed-off-by", os.Getenv("OOMPA_SIGNED_OFF_BY"), "Signed-off-by value for commits (e.g. \"Name <email>\")")

	var reviewers string
	flag.StringVar(&reviewers, "reviewers", os.Getenv("OOMPA_REVIEWERS"), "Comma-separated whitelist of users/bots whose reviews to address (empty = all)")

	var watchPRs string
	flag.StringVar(&watchPRs, "watch-prs", os.Getenv("OOMPA_WATCH_PRS"), "Comma-separated PR numbers to monitor directly (bypasses issue discovery)")

	var reactions string
	flag.StringVar(&reactions, "reactions", os.Getenv("OOMPA_REACTIONS"), "Comma-separated list of reactions to run: reviews, ci, conflicts, rebase (empty = all)")

	var triageJobs string
	flag.StringVar(&triageJobs, "triage-jobs", os.Getenv("OOMPA_TRIAGE_JOBS"), "Comma-separated CI job URLs to monitor for periodic job triage")

	var logFile string
	flag.StringVar(&logFile, "log-file", os.Getenv("OOMPA_LOG_FILE"), "Log file path (default: stderr)")

	flag.BoolVar(&cfg.CreateFlakyIssues, "create-flaky-issues", envOrDefault("OOMPA_CREATE_FLAKY_ISSUES", "") == "true", "Create issues for unrelated CI failures (opt-in)")
	flag.StringVar(&cfg.FlakyLabel, "flaky-label", envOrDefault("OOMPA_FLAKY_LABEL", "flaky-test"), "Label to apply to flaky CI issues")
	flag.BoolVar(&cfg.OnlyAssigned, "only-assigned", envOrDefault("OOMPA_ONLY_ASSIGNED", "") == "true", "Only process issues assigned to the agent user")
	flag.IntVar(&cfg.MaxWorkers, "max-workers", parseIntEnv("OOMPA_MAX_WORKERS", 1), "Maximum parallel Claude invocations (default 1 = sequential)")

	var forkFlag string
	flag.StringVar(&forkFlag, "fork", envOrDefault("OOMPA_FORK", ""), "Fork repo as owner/repo for pushing (e.g. qinqon/ovn-kubernetes)")

	flag.StringVar(&exitOnNewVersion, "exit-on-new-version", os.Getenv("OOMPA_EXIT_ON_NEW_VERSION"), "Exit when a new version is available (format: owner/repo, e.g. qinqon/oompa)")

	// Identity flags (optional, auto-detected from auth when not set)
	flag.StringVar(&cfg.GitHubUser, "github-user", os.Getenv("GITHUB_USER"), "GitHub username (e.g. myapp[bot])")
	flag.StringVar(&cfg.GitAuthorName, "git-author-name", os.Getenv("GIT_AUTHOR_NAME"), "Git commit author name")
	flag.StringVar(&cfg.GitAuthorEmail, "git-author-email", os.Getenv("GIT_AUTHOR_EMAIL"), "Git commit author email")

	// GitHub App auth flags
	var ghAppID int64
	var ghAppPrivateKeyPath string
	var ghAppInstallationID int64
	if v := os.Getenv("GITHUB_APP_ID"); v != "" {
		ghAppID, _ = strconv.ParseInt(v, 10, 64)
	}
	flag.Int64Var(&ghAppID, "github-app-id", ghAppID, "GitHub App ID")
	flag.StringVar(&ghAppPrivateKeyPath, "github-app-private-key", os.Getenv("GITHUB_APP_PRIVATE_KEY_PATH"), "Path to GitHub App private key PEM file (or set GITHUB_APP_PRIVATE_KEY env var with PEM content)")
	if v := os.Getenv("GITHUB_APP_INSTALLATION_ID"); v != "" {
		ghAppInstallationID, _ = strconv.ParseInt(v, 10, 64)
	}
	flag.Int64Var(&ghAppInstallationID, "github-app-installation-id", ghAppInstallationID, "GitHub App installation ID")

	flag.Parse()

	// Parse --repo owner/repo
	if repoFlag == "" {
		fmt.Fprintln(os.Stderr, "--repo is required (format: owner/repo)")
		os.Exit(1)
	}
	if parts := strings.SplitN(repoFlag, "/", 2); len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		fmt.Fprintf(os.Stderr, "invalid --repo %q: must be owner/repo\n", repoFlag)
		os.Exit(1)
	} else {
		cfg.Owner = parts[0]
		cfg.Repo = parts[1]
	}

	// Parse --fork owner/repo
	if forkFlag != "" {
		if parts := strings.SplitN(forkFlag, "/", 2); len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			fmt.Fprintf(os.Stderr, "invalid --fork %q: must be owner/repo\n", forkFlag)
			os.Exit(1)
		} else {
			cfg.ForkOwner = parts[0]
			cfg.ForkRepo = parts[1]
		}
	}

	// Structure clone dir as <base>/<owner>/<repo> so multiple projects can share --clone-dir
	cfg.CloneDir = filepath.Join(cfg.CloneDir, cfg.Owner, cfg.Repo)

	cfg.GitHubAppID = ghAppID
	cfg.GitHubAppInstallationID = ghAppInstallationID

	// Load private key: prefer inline env var, fall back to file path
	if key := os.Getenv("GITHUB_APP_PRIVATE_KEY"); key != "" {
		cfg.GitHubAppPrivateKey = []byte(key)
	} else if ghAppPrivateKeyPath != "" {
		keyBytes, err := os.ReadFile(ghAppPrivateKeyPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to read private key file %s: %v\n", ghAppPrivateKeyPath, err)
			os.Exit(1)
		}
		cfg.GitHubAppPrivateKey = keyBytes
	}

	cfg.LogFile = logFile

	if reviewers != "" {
		for _, r := range strings.Split(reviewers, ",") {
			r = strings.TrimSpace(r)
			if r != "" {
				cfg.Reviewers = append(cfg.Reviewers, r)
			}
		}
	}

	if watchPRs != "" {
		for _, s := range strings.Split(watchPRs, ",") {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			n, err := strconv.Atoi(s)
			if err != nil || n <= 0 {
				fmt.Fprintf(os.Stderr, "invalid PR number in --watch-prs: %q\n", s)
				os.Exit(1)
			}
			cfg.WatchPRs = append(cfg.WatchPRs, n)
		}
	}

	if reactions != "" {
		validReactions := map[string]bool{"reviews": true, "ci": true, "conflicts": true, "rebase": true}
		for _, r := range strings.Split(reactions, ",") {
			r = strings.TrimSpace(r)
			if r == "" {
				continue
			}
			if !validReactions[r] {
				fmt.Fprintf(os.Stderr, "invalid reaction type %q: valid values are reviews, ci, conflicts, rebase\n", r)
				os.Exit(1)
			}
			cfg.Reactions = append(cfg.Reactions, r)
		}
	}

	if triageJobs != "" {
		for _, url := range strings.Split(triageJobs, ",") {
			url = strings.TrimSpace(url)
			if url != "" {
				cfg.TriageJobs = append(cfg.TriageJobs, url)
			}
		}
	}

	return cfg, exitOnNewVersion //nolint:nakedret // named results used for gocritic
}

func parseDuration(s string) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil {
		return 2 * time.Minute
	}
	return d
}

func setupLogger(level, logFile string) (logger *slog.Logger, cleanup func()) {
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
	cleanup = func() {}

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

func getCommitSHA() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" {
			return s.Value
		}
	}
	return ""
}

func shouldExitForNewVersion(ctx context.Context, ghClient *agent.GoGitHubClient, owner, repo, currentSHA string, logger *slog.Logger) bool {
	latestSHA, err := ghClient.GetLatestReleaseSHA(ctx, owner, repo)
	if err != nil {
		logger.Warn("failed to check for new version", "error", err)
		return false
	}

	if latestSHA != currentSHA {
		logger.Info("new version detected, exiting for update", "current", currentSHA, "latest", latestSHA)
		return true
	}

	return false
}

func main() {
	cfg, exitOnNewVersion := parseConfig()

	if cfg.VertexRegion == "" || cfg.VertexProject == "" {
		fmt.Fprintln(os.Stderr, "CLOUD_ML_REGION and ANTHROPIC_VERTEX_PROJECT_ID are required")
		os.Exit(1)
	}

	logger, closeLog := setupLogger(cfg.LogLevel, cfg.LogFile)
	defer closeLog()

	// If GCP credentials JSON is provided inline, write to a temp file
	// so GOOGLE_APPLICATION_CREDENTIALS can reference it.
	if gcpJSON := os.Getenv("GCP_CREDENTIALS_JSON"); gcpJSON != "" {
		f, err := os.CreateTemp("", "gcp-credentials-*.json")
		if err != nil {
			logger.Error("failed to create temp file for GCP credentials", "error", err)
			os.Exit(1) //nolint:gocritic // exitAfterDefer: intentional early exit in CLI startup
		}
		defer os.Remove(f.Name())
		if _, err := f.Write([]byte(gcpJSON)); err != nil {
			f.Close()
			logger.Error("failed to write GCP credentials", "error", err)
			os.Exit(1)
		}
		f.Close()
		os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", f.Name())
	}

	useAppAuth := cfg.GitHubAppID != 0 && len(cfg.GitHubAppPrivateKey) > 0 && cfg.GitHubAppInstallationID != 0

	var ghClient *agent.GoGitHubClient
	var tokenFunc func(context.Context) (string, error)

	if useAppAuth {
		appAuth, err := agent.NewGitHubAppAuth(cfg.GitHubAppID, cfg.GitHubAppInstallationID, cfg.GitHubAppPrivateKey)
		if err != nil {
			logger.Error("failed to set up GitHub App auth", "error", err)
			os.Exit(1)
		}

		ghClient = appAuth.Client
		tokenFunc = appAuth.TokenFunc
		cfg.GitHubUser = appAuth.Login
		if cfg.ForkOwner != "" {
			cfg.GitHubHeadOwner = cfg.ForkOwner
		} else {
			cfg.GitHubHeadOwner = cfg.Owner
		}
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
		os.Setenv("GH_TOKEN", token)

		logger.Info("authenticated as GitHub App", "login", appAuth.Login, "signed-off-by", cfg.SignedOffBy)
	} else {
		token := os.Getenv("GITHUB_TOKEN")
		if token == "" {
			logger.Error("GITHUB_TOKEN is required (or configure GitHub App auth with --github-app-id, --github-app-private-key/GITHUB_APP_PRIVATE_KEY, --github-app-installation-id)")
			os.Exit(1)
		}
		cfg.GitHubToken = token

		ghClient = agent.NewGoGitHubClient(token)
		if cfg.GitHubUser != "" && cfg.GitAuthorName != "" && cfg.GitAuthorEmail != "" {
			// Identity provided via flags/env vars (e.g. GitHub Actions with app installation token)
			if cfg.ForkOwner != "" {
				cfg.GitHubHeadOwner = cfg.ForkOwner
			} else {
				cfg.GitHubHeadOwner = cfg.Owner
			}
			if cfg.SignedOffBy == "" {
				cfg.SignedOffBy = fmt.Sprintf("%s <%s>", cfg.GitAuthorName, cfg.GitAuthorEmail)
			}
			logger.Info("using provided identity", "login", cfg.GitHubUser, "signed-off-by", cfg.SignedOffBy)
		} else if login, name, email, err := ghClient.GetAuthenticatedUser(context.Background()); err == nil {
			cfg.GitHubUser = login
			cfg.GitHubHeadOwner = login // PAT pushes to user's fork
			cfg.GitAuthorName = name
			cfg.GitAuthorEmail = email
			if cfg.SignedOffBy == "" {
				cfg.SignedOffBy = fmt.Sprintf("%s <%s>", name, email)
			}
			logger.Info("authenticated as GitHub user", "login", login, "signed-off-by", cfg.SignedOffBy)
		} else {
			logger.Error("could not fetch GitHub user (set --github-user, --git-author-name, --git-author-email to skip auto-detection)", "error", err)
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
	forkURL := repoURL // default: same-repo workflow
	if cfg.ForkOwner != "" {
		forkRepoName := cfg.ForkRepo
		if forkRepoName == "" {
			forkRepoName = cfg.Repo
		}
		forkURL = fmt.Sprintf("https://github.com/%s/%s.git", cfg.ForkOwner, forkRepoName)
	} else if !useAppAuth {
		forkURL = fmt.Sprintf("https://github.com/%s/%s.git", cfg.GitHubUser, cfg.Repo)
	}
	runner := &agent.ExecRunner{}
	runner.Env = agent.BuildClaudeEnv(cfg)

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

	// Parse exit-on-new-version flag
	var exitOnNewVersionOwner, exitOnNewVersionRepo string
	commitSHA := getCommitSHA()
	if exitOnNewVersion != "" {
		if parts := strings.SplitN(exitOnNewVersion, "/", 2); len(parts) == 2 && parts[0] != "" && parts[1] != "" {
			exitOnNewVersionOwner = parts[0]
			exitOnNewVersionRepo = parts[1]
			if commitSHA == "" {
				logger.Warn("--exit-on-new-version set but no VCS revision found (dev build), version check disabled")
			} else {
				logger.Info("version check enabled", "repo", exitOnNewVersion, "current-sha", commitSHA)
			}
		} else {
			logger.Error("invalid --exit-on-new-version format (must be owner/repo)", "value", exitOnNewVersion)
			os.Exit(1)
		}
	}

	logger.Info("starting oompa",
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
			if exitOnNewVersionOwner != "" && commitSHA != "" && shouldExitForNewVersion(ctx, ghClient, exitOnNewVersionOwner, exitOnNewVersionRepo, commitSHA, logger) {
				return
			}
		}
	}
}

func runLoop(ctx context.Context, a *agent.Agent, logger *slog.Logger) {
	logger.Debug("starting poll cycle")
	if err := a.RefreshToken(ctx); err != nil {
		logger.Error("failed to refresh GitHub token", "error", err)
	}
	a.CleanupDone(ctx)

	if a.HasWatchedPRs() {
		a.BootstrapWatchedPRs(ctx)
	} else {
		a.ProcessNewIssues(ctx)
	}

	if a.ShouldRunReaction("reviews") {
		a.ProcessReviewComments(ctx)
	}
	if a.ShouldRunReaction("conflicts") {
		a.ProcessConflicts(ctx)
	}
	if a.ShouldRunReaction("rebase") {
		a.ProcessRebase(ctx)
	}
	if a.ShouldRunReaction("ci") {
		a.ProcessCIFailures(ctx)
	}

	// ProcessTriageJobs is independent of other reactions and runs when triage jobs are configured
	a.ProcessTriageJobs(ctx)

	logger.Debug("poll cycle complete")
}
