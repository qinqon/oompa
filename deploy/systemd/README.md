# Systemd Deployment

This directory contains a native systemd unit for running oompa with automatic binary updates and restart handling.

## Features

- **Auto-update**: Downloads the latest oompa binary from GitHub releases on each restart
- **Process supervision**: Systemd handles restarts, logging, and dependency management
- **Multi-instance**: Template unit supports running multiple oompa instances
- **Journald logging**: All output goes to systemd's journal (`journalctl -u oompa@<instance>`)
- **Boot-time startup**: Enable with `systemctl enable oompa@<instance>`

## Quick Start

### 1. Install the unit file

**System-wide** (requires root):
```bash
sudo cp oompa@.service /etc/systemd/system/
sudo systemctl daemon-reload
```

**User-specific** (no root required):
```bash
mkdir -p ~/.config/systemd/user/
cp oompa@.service ~/.config/systemd/user/
systemctl --user daemon-reload
```

### 2. Configure environment

**System-wide**:
```bash
sudo mkdir -p /etc/oompa
sudo cp example.env /etc/oompa/issue-resolver.env
sudo nano /etc/oompa/issue-resolver.env  # Add your GITHUB_TOKEN and OOMPA_FLAGS
```

**User-specific**:
```bash
mkdir -p ~/.config/oompa
cp example.env ~/.config/oompa/issue-resolver.env
nano ~/.config/oompa/issue-resolver.env  # Add your GITHUB_TOKEN and OOMPA_FLAGS
```

### 3. Create binary directory

**System-wide**:
```bash
sudo mkdir -p /var/lib/oompa/bin
```

**User-specific**:
```bash
mkdir -p ~/var/lib/oompa/bin
# Edit the service file to use a user-writable path:
# Environment=OOMPA_BINARY_DIR=%h/var/lib/oompa/bin
```

### 4. Start the service

**System-wide**:
```bash
sudo systemctl start oompa@issue-resolver
sudo systemctl status oompa@issue-resolver
```

**User-specific**:
```bash
systemctl --user start oompa@issue-resolver
systemctl --user status oompa@issue-resolver
```

### 5. Enable on boot (optional)

**System-wide**:
```bash
sudo systemctl enable oompa@issue-resolver
```

**User-specific**:
```bash
systemctl --user enable oompa@issue-resolver
systemctl --user enable --now oompa@issue-resolver  # Enable and start
```

## Multiple Instances

Run multiple oompa instances with different configurations:

```bash
# Create separate environment files
sudo cp example.env /etc/oompa/issue-resolver.env
sudo cp example.env /etc/oompa/pr-babysitter.env
sudo cp example.env /etc/oompa/ci-fixer.env

# Edit each with instance-specific configuration
sudo nano /etc/oompa/issue-resolver.env
sudo nano /etc/oompa/pr-babysitter.env
sudo nano /etc/oompa/ci-fixer.env

# Start each instance
sudo systemctl start oompa@issue-resolver
sudo systemctl start oompa@pr-babysitter
sudo systemctl start oompa@ci-fixer
```

## Managing Services

### View logs
```bash
# Follow logs for a specific instance
sudo journalctl -u oompa@issue-resolver -f

# View logs with user service
journalctl --user -u oompa@issue-resolver -f

# View logs since last restart
sudo journalctl -u oompa@issue-resolver --since today
```

### Restart service
```bash
sudo systemctl restart oompa@issue-resolver
```

### Stop service
```bash
sudo systemctl stop oompa@issue-resolver
```

### Check status
```bash
sudo systemctl status oompa@issue-resolver
```

### Disable auto-start
```bash
sudo systemctl disable oompa@issue-resolver
```

## How It Works

1. **ExecStartPre**: Downloads the latest oompa binary from GitHub releases using `gh release download`
2. **ExecStart**: Runs oompa with `--exit-on-new-version`, which monitors for new releases
3. **Restart=always**: When oompa exits (new version detected or crash), systemd restarts it
4. **RestartSec=5**: Waits 5 seconds between restarts to avoid tight loops

The binary exits cleanly when a new version is available, triggering systemd to restart the service. On restart, ExecStartPre downloads the new binary, creating a seamless auto-update loop.

## Configuration Variables

Environment variables can be set in the `.env` file:

- `GITHUB_TOKEN`: GitHub API token (required)
- `OOMPA_FLAGS`: Additional command-line flags (e.g., `--repo owner/repo --poll-interval 300`)
- `OOMPA_BINARY_DIR`: Where to store the binary (default: `/var/lib/oompa/bin`)
- `OOMPA_RELEASE_REPO`: GitHub repo for binary releases (default: `qinqon/oompa`)
- `OOMPA_BINARY_NAME`: Binary filename to download (default: `oompa-linux-amd64`)

## Architecture Support

For ARM64 systems, update the environment file:
```bash
OOMPA_BINARY_NAME=oompa-linux-arm64
```

For macOS (with launchd instead of systemd), see the wrapper script at `workflows/run-oompa.sh`.

## Security Notes

- The unit includes basic hardening: `NoNewPrivileges=true` and `PrivateTmp=true`
- User-specific services run with user privileges (no root access)
- System-wide services can specify a dedicated user with `User=oompa` in the unit file
- Store `GITHUB_TOKEN` securely in environment files with restricted permissions:
  ```bash
  sudo chmod 600 /etc/oompa/*.env
  ```

## Troubleshooting

### Service fails to start
```bash
# Check service status
sudo systemctl status oompa@issue-resolver

# View full logs
sudo journalctl -u oompa@issue-resolver -n 100

# Verify environment file exists and is readable
sudo cat /etc/oompa/issue-resolver.env

# Test manually
export $(cat /etc/oompa/issue-resolver.env | xargs)
/var/lib/oompa/bin/oompa --exit-on-new-version=qinqon/oompa $OOMPA_FLAGS
```

### Binary download fails
- Ensure `gh` (GitHub CLI) is installed and authenticated
- Check network connectivity: `curl -I https://github.com`
- Verify release exists: `gh release view --repo qinqon/oompa`

### Permission denied
- System-wide: Ensure `/var/lib/oompa/bin` is writable by the service user
- User-specific: Use a user-writable path in `OOMPA_BINARY_DIR`

## Migration from Wrapper Script

If you're currently using `workflows/run-oompa.sh`, here's how to migrate:

1. Stop the wrapper script (Ctrl+C or kill the process)
2. Follow the Quick Start steps above
3. Set `OOMPA_FLAGS` in the env file to match the arguments you passed to the wrapper script
4. Start the systemd service

The systemd unit provides the same functionality with better process management and logging.
