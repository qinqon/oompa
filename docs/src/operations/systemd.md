# Systemd

Oompa can run as a systemd user service that downloads the latest release binary on each (re)start.

## Credentials File

Store provider credentials in `~/.config/oompa/env`:

```bash
# For Vertex AI (used by both OpenCode and Claude Code)
CLOUD_ML_REGION=us-east5
ANTHROPIC_VERTEX_PROJECT_ID=my-gcp-project
GOOGLE_APPLICATION_CREDENTIALS=/path/to/credentials.json
GOOGLE_CLOUD_PROJECT=my-gcp-project

# GITHUB_TOKEN is optional -- oompa falls back to `gh auth token`
```

## Issue Resolver Service

`~/.config/systemd/user/oompa-issue-resolver.service`:

```ini
[Unit]
Description=Oompa Issue Resolver - myorg/myrepo
After=network-online.target
Wants=network-online.target

[Service]
EnvironmentFile=%h/.config/oompa/env
Environment=PATH=/usr/local/bin:/usr/bin:/bin
RuntimeDirectory=oompa-resolver
ExecStartPre=/bin/bash -c 'gh release download --repo qinqon/oompa --pattern oompa-linux-amd64 --dir %t/oompa-resolver --clobber && chmod +x %t/oompa-resolver/oompa-linux-amd64'
ExecStart=%t/oompa-resolver/oompa-linux-amd64 --exit-on-new-version=qinqon/oompa --repo myorg/myrepo --poll-interval 2m --log-level info
Restart=always
RestartSec=10

[Install]
WantedBy=default.target
```

## PR Babysitter Service

`~/.config/systemd/user/oompa-pr-babysitter.service`:

```ini
[Unit]
Description=Oompa PR Babysitter - myorg/myrepo
After=network-online.target
Wants=network-online.target

[Service]
EnvironmentFile=%h/.config/oompa/env
Environment=PATH=/usr/local/bin:/usr/bin:/bin
RuntimeDirectory=oompa-babysitter
ExecStartPre=/bin/bash -c 'gh release download --repo qinqon/oompa --pattern oompa-linux-amd64 --dir %t/oompa-babysitter --clobber && chmod +x %t/oompa-babysitter/oompa-linux-amd64'
ExecStart=%t/oompa-babysitter/oompa-linux-amd64 --exit-on-new-version=qinqon/oompa --repo myorg/myrepo --watch-prs 123,456 --reactions ci,conflicts,rebase --fork myuser/myrepo --poll-interval 2m --log-level info
Restart=always
RestartSec=10

[Install]
WantedBy=default.target
```

## Periodic CI Triage (Timer)

For one-shot workflows, use a `Type=oneshot` service paired with a systemd timer.

`~/.config/systemd/user/oompa-periodic-triage.service`:

```ini
[Unit]
Description=Oompa Periodic CI Triage - myorg/myrepo
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
EnvironmentFile=%h/.config/oompa/env
Environment=PATH=/usr/local/bin:/usr/bin:/bin
RuntimeDirectory=oompa-periodic-triage
ExecStartPre=/bin/bash -c 'gh release download --repo qinqon/oompa --pattern oompa-linux-amd64 --dir %t/oompa-periodic-triage --clobber && chmod +x %t/oompa-periodic-triage/oompa-linux-amd64'
ExecStart=%t/oompa-periodic-triage/oompa-linux-amd64 --repo myorg/myrepo --triage-jobs https://prow.example.com/view/gs/bucket/logs/periodic-e2e-job/ --create-flaky-issues --one-shot --log-level info
```

`~/.config/systemd/user/oompa-periodic-triage.timer`:

```ini
[Unit]
Description=Run Oompa Periodic CI Triage daily at 9 AM

[Timer]
OnCalendar=*-*-* 09:00:00 Europe/Madrid
Persistent=true

[Install]
WantedBy=timers.target
```

Enable the timer (not the service):

```bash
systemctl --user enable --now oompa-periodic-triage.timer
```

## How It Works

- `ExecStartPre` downloads the latest release binary before each start
- `--exit-on-new-version` makes the agent exit when it detects a newer release during polling
- `Restart=always` restarts the service on exit, triggering `ExecStartPre` to download the new binary
- `RuntimeDirectory=` gives each unit its own directory under `/run/user/<uid>/`
- `Persistent=true` on timers ensures missed runs are caught up on next boot
- The timer's `OnCalendar` supports IANA timezones (e.g. `Europe/Madrid`, `US/Eastern`)

## Managing Services

```bash
# Enable and start
systemctl --user enable --now oompa-issue-resolver

# Check status
systemctl --user status oompa-issue-resolver

# View logs
journalctl --user -u oompa-issue-resolver -f

# Restart
systemctl --user restart oompa-issue-resolver
```
