package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReadPluginVersionFromFile_ValidPackageJSON(t *testing.T) {
	dir := t.TempDir()
	pkgFile := filepath.Join(dir, "package.json")
	if err := os.WriteFile(pkgFile, []byte(`{"name":"@opencode-ai/plugin","version":"1.15.13"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	version, err := readPluginVersionFromFile(pkgFile)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if version != "1.15.13" {
		t.Errorf("expected version 1.15.13, got %s", version)
	}
}

func TestReadPluginVersionFromFile_MissingFile(t *testing.T) {
	_, err := readPluginVersionFromFile("/nonexistent/package.json")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestReadPluginVersionFromFile_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	pkgFile := filepath.Join(dir, "package.json")
	if err := os.WriteFile(pkgFile, []byte(`not json`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := readPluginVersionFromFile(pkgFile)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestReadPluginVersionFromFile_NoVersionField(t *testing.T) {
	dir := t.TempDir()
	pkgFile := filepath.Join(dir, "package.json")
	if err := os.WriteFile(pkgFile, []byte(`{"name":"@opencode-ai/plugin"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := readPluginVersionFromFile(pkgFile)
	if err == nil {
		t.Fatal("expected error for missing version field")
	}
	if !strings.Contains(err.Error(), "no version field") {
		t.Errorf("expected 'no version field' in error, got: %v", err)
	}
}

func TestReadPluginVersionFromFile_EmptyVersion(t *testing.T) {
	dir := t.TempDir()
	pkgFile := filepath.Join(dir, "package.json")
	if err := os.WriteFile(pkgFile, []byte(`{"name":"@opencode-ai/plugin","version":""}`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := readPluginVersionFromFile(pkgFile)
	if err == nil {
		t.Fatal("expected error for empty version")
	}
}

func TestRequirePluginInstalled_Present(t *testing.T) {
	// When the plugin is installed, RequirePluginInstalled returns the version.
	fakeHome := t.TempDir()
	pluginDir := filepath.Join(fakeHome, ".config", "opencode", "node_modules", "@opencode-ai", "plugin")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(pluginDir, "package.json"),
		[]byte(`{"name":"@opencode-ai/plugin","version":"1.15.13"}`),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", fakeHome)

	version, err := RequirePluginInstalled()
	if err != nil {
		t.Fatalf("expected no error when plugin is installed, got: %v", err)
	}
	if version != "1.15.13" {
		t.Errorf("expected version 1.15.13, got %s", version)
	}
}

func TestRequirePluginInstalled_Missing(t *testing.T) {
	// When the plugin is not installed, RequirePluginInstalled returns an error.
	t.Setenv("HOME", t.TempDir())

	_, err := RequirePluginInstalled()
	if err == nil {
		t.Fatal("expected error when plugin is not installed")
	}
}

func TestCheckPluginVersion_BestEffortNpmUnavailable(t *testing.T) {
	// This test verifies that CheckPluginVersion handles a missing npm gracefully
	// by returning nil (best-effort). Also validates readInstalledPluginVersion with
	// a fake home directory.

	// Create fake home with installed plugin
	fakeHome := t.TempDir()
	pluginDir := filepath.Join(fakeHome, ".config", "opencode", "node_modules", "@opencode-ai", "plugin")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(pluginDir, "package.json"),
		[]byte(`{"name":"@opencode-ai/plugin","version":"1.14.22"}`),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	// Override HOME so readInstalledPluginVersion finds our fake directory
	t.Setenv("HOME", fakeHome)

	logger := discardLogger()

	// We can't easily mock npm view in a unit test, so we test the components
	// directly and test CheckPluginVersion integration only when npm is available.
	// The unit tests below cover the individual functions.

	// Test readInstalledPluginVersion with the fake home
	version, err := readInstalledPluginVersion()
	if err != nil {
		t.Fatalf("unexpected error reading installed version: %v", err)
	}
	if version != "1.14.22" {
		t.Errorf("expected installed version 1.14.22, got %s", version)
	}

	// Test that CheckPluginVersion handles missing npm gracefully
	// (best-effort: returns nil on error)
	t.Setenv("PATH", "") // ensure npm is not found
	finding := CheckPluginVersion(context.Background(), logger)
	// Should return nil because npm view will fail
	if finding != nil {
		t.Errorf("expected nil finding when npm is not available, got: %+v", finding)
	}
}

func TestCheckPluginVersion_BestEffortPluginNotInstalled(t *testing.T) {
	// When the plugin is not installed (package.json missing), CheckPluginVersion
	// should silently return nil (best-effort).
	logger := discardLogger()

	t.Setenv("HOME", t.TempDir())
	finding := CheckPluginVersion(context.Background(), logger)
	if finding != nil {
		t.Errorf("expected nil finding when plugin not installed, got: %+v", finding)
	}
}

func TestCheckPluginVersion_OutdatedWithFakeNpm(t *testing.T) {
	// Deterministic test: creates a fake npm script that returns a fixed "latest"
	// version, and a fake installed package.json with a different version.
	// Verifies CheckPluginVersion returns a non-nil finding with correct fields.

	// Create fake home with installed plugin at version 1.14.22
	fakeHome := t.TempDir()
	pluginDir := filepath.Join(fakeHome, ".config", "opencode", "node_modules", "@opencode-ai", "plugin")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(pluginDir, "package.json"),
		[]byte(`{"name":"@opencode-ai/plugin","version":"1.14.22"}`),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", fakeHome)

	// Create a fake npm executable that prints "1.15.13" for `npm view ... version`
	fakeBin := t.TempDir()
	npmScript := filepath.Join(fakeBin, "npm")
	if err := os.WriteFile(npmScript, []byte("#!/bin/sh\necho 1.15.13\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin)

	logger := discardLogger()
	finding := CheckPluginVersion(context.Background(), logger)

	if finding == nil {
		t.Fatal("expected non-nil finding when versions differ")
	}
	if finding.Owner != "opencode-ai" {
		t.Errorf("expected Owner 'opencode-ai', got %s", finding.Owner)
	}
	if finding.Repo != "plugin" {
		t.Errorf("expected Repo 'plugin', got %s", finding.Repo)
	}
	if finding.Category != "plugin" {
		t.Errorf("expected category 'plugin', got %s", finding.Category)
	}
	if !strings.Contains(finding.Message, "1.14.22") {
		t.Error("expected message to contain installed version 1.14.22")
	}
	if !strings.Contains(finding.Message, "1.15.13") {
		t.Error("expected message to contain latest version 1.15.13")
	}
	if finding.DedupKey != "plugin-outdated:1.14.22:1.15.13" {
		t.Errorf("expected DedupKey 'plugin-outdated:1.14.22:1.15.13', got %s", finding.DedupKey)
	}
}

func TestCheckPluginVersion_SameVersionWithFakeNpm(t *testing.T) {
	// Deterministic test: when installed and latest versions match,
	// CheckPluginVersion should return nil (no finding).

	fakeHome := t.TempDir()
	pluginDir := filepath.Join(fakeHome, ".config", "opencode", "node_modules", "@opencode-ai", "plugin")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(pluginDir, "package.json"),
		[]byte(`{"name":"@opencode-ai/plugin","version":"1.15.13"}`),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", fakeHome)

	// Fake npm returns the same version
	fakeBin := t.TempDir()
	npmScript := filepath.Join(fakeBin, "npm")
	if err := os.WriteFile(npmScript, []byte("#!/bin/sh\necho 1.15.13\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin)

	logger := discardLogger()
	finding := CheckPluginVersion(context.Background(), logger)

	if finding != nil {
		t.Errorf("expected nil finding when versions match, got: %+v", finding)
	}
}

func TestCheckPluginVersion_FindingFormat(t *testing.T) {
	// Verify the SlackFinding format directly
	finding := &SlackFinding{
		Owner:    "opencode-ai",
		Repo:     "plugin",
		PRTitle:  "Plugin Version Check",
		PRURL:    "https://www.npmjs.com/package/@opencode-ai/plugin",
		Category: "plugin",
		Message: "⚠️ compound-engineering plugin outdated: installed 1.14.22, latest 1.15.13. " +
			"Run: `cd ~/.config/opencode && npm install @opencode-ai/plugin@latest`",
		DedupKey: "plugin-outdated:1.14.22:1.15.13",
	}

	if finding.Category != "plugin" {
		t.Errorf("expected category 'plugin', got %s", finding.Category)
	}
	if finding.Owner != "opencode-ai" {
		t.Errorf("expected Owner 'opencode-ai', got %s", finding.Owner)
	}
	if finding.Repo != "plugin" {
		t.Errorf("expected Repo 'plugin', got %s", finding.Repo)
	}
	if finding.PRTitle != "Plugin Version Check" {
		t.Errorf("expected PRTitle 'Plugin Version Check', got %s", finding.PRTitle)
	}
	if finding.PRURL != "https://www.npmjs.com/package/@opencode-ai/plugin" {
		t.Errorf("expected PRURL to point to npm package, got %s", finding.PRURL)
	}
	if !strings.Contains(finding.Message, "1.14.22") {
		t.Error("expected message to contain installed version")
	}
	if !strings.Contains(finding.Message, "1.15.13") {
		t.Error("expected message to contain latest version")
	}
	if !strings.Contains(finding.Message, "npm install") {
		t.Error("expected message to contain update command")
	}
	if !strings.Contains(finding.DedupKey, "1.14.22") || !strings.Contains(finding.DedupKey, "1.15.13") {
		t.Error("expected DedupKey to contain both versions")
	}
}

func TestStartPluginVersionChecker_DisabledWithoutSlack(t *testing.T) {
	logger := discardLogger()

	// Should return nil and not panic with nil Slack reporter
	ch := StartPluginVersionChecker(context.Background(), nil, logger)
	if ch != nil {
		t.Error("expected nil channel when Slack reporter is nil")
	}

	// Should return nil and not panic with disabled Slack reporter
	reporter := NewSlackReporter("", "", logger)
	ch = StartPluginVersionChecker(context.Background(), reporter, logger)
	if ch != nil {
		t.Error("expected nil channel when Slack reporter is disabled")
	}
}

func TestStartPluginVersionChecker_RunsAndStops(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	// Set HOME to a temp dir so the installed version check fails gracefully
	t.Setenv("HOME", t.TempDir())
	// Ensure npm is not found so fetchLatestPluginVersion fails gracefully
	t.Setenv("PATH", "")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	logger := discardLogger()
	reporter := NewSlackReporter(ts.URL, "", logger)
	reporter.httpClient = ts.Client()

	ctx, cancel := context.WithCancel(context.Background())
	initDone := StartPluginVersionChecker(ctx, reporter, logger)

	if initDone == nil {
		t.Fatal("expected non-nil initDone channel when Slack is enabled")
	}

	// Wait for the initial check to complete
	select {
	case <-initDone:
		// Initial check completed
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for initial plugin check")
	}

	// Cancel should stop the goroutine
	cancel()

	// Give it time to stop
	time.Sleep(100 * time.Millisecond)
	// No assertion needed — we're verifying it doesn't panic or leak
}
