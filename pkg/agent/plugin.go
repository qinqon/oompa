package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	// pluginCheckInterval is how often the plugin version is checked.
	// Once per day is sufficient — npm view makes an HTTP request to the registry.
	pluginCheckInterval = 24 * time.Hour

	// pluginPackageName is the npm package name for the compound-engineering plugin.
	pluginPackageName = "@opencode-ai/plugin"

	// npmViewTimeout is the timeout for the npm view command.
	npmViewTimeout = 30 * time.Second
)

// pluginPackageJSON is the minimal structure of a package.json file
// containing the version field we need.
type pluginPackageJSON struct {
	Version string `json:"version"`
}

// CheckPluginVersion compares the installed compound-engineering plugin version
// against the latest available on npm. Returns a SlackFinding if outdated, nil otherwise.
// Best-effort: returns nil on any error (npm not installed, package.json missing, etc.).
func CheckPluginVersion(ctx context.Context, logger *slog.Logger) *SlackFinding {
	if logger == nil {
		logger = slog.Default()
	}
	installed, err := readInstalledPluginVersion()
	if err != nil {
		logger.Debug("plugin version check: could not read installed version", "error", err)
		return nil
	}

	latest, err := fetchLatestPluginVersion(ctx)
	if err != nil {
		logger.Debug("plugin version check: could not fetch latest version", "error", err)
		return nil
	}

	if installed == latest {
		return nil
	}

	logger.Warn("compound-engineering plugin outdated",
		"installed", installed,
		"latest", latest,
	)

	return &SlackFinding{
		Owner:    "opencode-ai",
		Repo:     "plugin",
		PRTitle:  "Plugin Version Check",
		PRURL:    "https://www.npmjs.com/package/@opencode-ai/plugin",
		Category: "plugin",
		Message: fmt.Sprintf(
			"⚠️ compound-engineering plugin outdated: installed %s, latest %s. "+
				"Run: `cd ~/.config/opencode && npm install %s@latest`",
			installed, latest, pluginPackageName,
		),
		DedupKey: fmt.Sprintf("plugin-outdated:%s:%s", installed, latest),
	}
}

// RequirePluginInstalled checks that the compound-engineering plugin is installed
// in ~/.config/opencode/node_modules/. Returns the installed version on success,
// or an error if the plugin is missing or unreadable.
// Call this at startup to fail fast if the plugin is not present.
func RequirePluginInstalled() (string, error) {
	return readInstalledPluginVersion()
}

// readInstalledPluginVersion reads the installed plugin version from the
// node_modules package.json under ~/.config/opencode/.
func readInstalledPluginVersion() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("could not determine home directory: %w", err)
	}

	pkgPath := filepath.Join(home, ".config", "opencode", "node_modules", pluginPackageName, "package.json")
	return readPluginVersionFromFile(pkgPath)
}

// readPluginVersionFromFile reads and parses the version field from a package.json file.
func readPluginVersionFromFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", path, err)
	}

	var pkg pluginPackageJSON
	if err := json.Unmarshal(data, &pkg); err != nil {
		return "", fmt.Errorf("parsing %s: %w", path, err)
	}

	if pkg.Version == "" {
		return "", fmt.Errorf("no version field in %s", path)
	}

	return pkg.Version, nil
}

// fetchLatestPluginVersion runs `npm view @opencode-ai/plugin version` to get
// the latest available version from the npm registry.
func fetchLatestPluginVersion(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, npmViewTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, "npm", "view", pluginPackageName, "version").Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("npm view failed (stderr: %s): %w", string(exitErr.Stderr), err)
		}
		return "", fmt.Errorf("npm view failed: %w", err)
	}

	version := strings.TrimSpace(string(out))
	if version == "" {
		return "", fmt.Errorf("npm view returned empty version")
	}

	return version, nil
}

// StartPluginVersionChecker launches a goroutine that checks the plugin version
// once per day and posts a Slack finding if outdated. The goroutine runs until
// the context is cancelled.
//
// Returns a channel that is closed when the initial check completes, allowing
// callers (e.g. OneShot mode) to wait for the first check before exiting.
// Returns nil if the checker is disabled (no Slack reporter).
//
// Runs an immediate check on startup, then uses a 24h ticker for subsequent checks.
func StartPluginVersionChecker(ctx context.Context, slack *SlackReporter, logger *slog.Logger) <-chan struct{} {
	if logger == nil {
		logger = slog.Default()
	}
	if slack == nil {
		logger.Debug("plugin version checker disabled (no Slack reporter)")
		return nil
	}
	if !slack.IsEnabled() {
		logger.Debug("plugin version checker disabled (Slack webhook not configured)")
		return nil
	}

	initDone := make(chan struct{})

	go func() {
		// Run the first check immediately on startup
		if finding := CheckPluginVersion(ctx, logger); finding != nil {
			slack.Collect([]SlackFinding{*finding})
		}
		close(initDone)

		ticker := time.NewTicker(pluginCheckInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if finding := CheckPluginVersion(ctx, logger); finding != nil {
					slack.Collect([]SlackFinding{*finding})
				}
			}
		}
	}()

	logger.Info("plugin version checker started", "interval", pluginCheckInterval)
	return initDone
}
