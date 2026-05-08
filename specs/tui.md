# TUI and Status Command

## Overview

Two observability commands that connect to the running oompa daemon via Unix socket:
- `oompa status` -- print-and-exit colored table
- `oompa tui` -- live animated terminal dashboard

## Subcommand Dispatch

The main binary uses positional arguments for subcommands:

```text
oompa              # default: run daemon (existing behavior)
oompa status       # print status snapshot and exit
oompa tui          # launch interactive TUI
```

When `os.Args[1]` is `status` or `tui`, dispatch to the subcommand handler.
Otherwise, fall through to existing daemon startup.

## `oompa status`

### Flags

- `--since <duration>` (default: `4h`) -- lookback window for recent events
- `--socket <path>` -- override socket path (default: auto-detect)

### Behavior

1. Connect to Unix socket
2. Send `{"type": "snapshot", "since": "<duration>"}`
3. Read `StatusSnapshot` response
4. Print formatted colored table and exit

### Output Format

```text
OOMPA STATUS (N workers, uptime Xh Ym)

WORKER                     STATE          CURRENT ACTION            LAST EVENT
─────────────────────────────────────────────────────────────────────────────────
worker/name [PR#,PR#]      ● Working      Description               Xm ago

RECENT ACTIVITY (last Xh, N events)

  HH:MM  worker/name    Event description
  ...
```

### State Icons

- `●` Working / Reviewing / Rebasing
- `○` Idle
- `◐` Scheduled
- `✖` Error / Stuck
- `(-.-) Zzz` Sleeping (TUI only)

## `oompa tui`

### Flags

- `--socket <path>` -- override socket path (default: auto-detect)

### Behavior

1. Connect to Unix socket
2. Send `{"type": "snapshot", "since": "1h"}` to get initial state
3. Send `{"type": "stream"}` on same connection to receive live updates
4. Render bubbletea TUI with worker cards and scrollable activity log

### Layout

Factory floor theme where each project is a conveyor belt line with oompas standing on it:

- Header: "OOMPA FACTORY" with connection status and time
- Conveyor belts: workers grouped by project (owner/repo), each project rendered as a belt (`═══ project-name ═══▶`) with oompa sprites standing on `●●●` dot surfaces
- Multi-role projects: oompas stand side-by-side on the same belt (e.g., a project with prs, issues, triage roles has 3 oompas on one belt)
- Column tiling: belts tile left-to-right, wrapping to next row when terminal width runs out
- Belt sizing: single-oompa belts are ~28 chars wide, multi-oompa belts expand proportionally
- Belt animation: active oompas have scrolling dots, idle/sleeping oompas have static dots
- Footer: compact 2-column activity log using short worker names (e.g., `oompa:issues`)

### Oompa-Loompa Sprites

ASCII art characters reflecting worker state:

```text
Working:        Idle:           Sleeping:       Error:
   ___            ___             ___            ___
  (o.o)          (o.o)           (-.- ) Zzz     (x.x)
 --|--|--/       --|--|--         |__|          --|--|--
   |  |            |  |         _/  \_           |  |
  _/  \_          _/  \_                        _/  \_
```

Sprites animate at ~4 FPS (250ms tick). Animations:
- Working: tool alternates position
- Sleeping: Z count cycles (Z, Zz, Zzz)
- Error: stars cycle (*, **, ***)
- Idle: slight sway (space shifts)

### Key Bindings

- `q` / `Ctrl+C` -- quit
- `↑` / `↓` -- scroll activity log
- `Tab` -- cycle focus between cards and log

### Worker Name Format

Worker names use the format `"owner/repo:role"` where role is `prs`, `issues`, or `triage`.
Workers without a role (backward compat) use `"owner/repo"`.

Workers are grouped by project (`owner/repo`) for belt rendering:
```
worker "nmstate/kubernetes-nmstate:prs"    -> project "nmstate/kubernetes-nmstate", role "prs"
worker "nmstate/kubernetes-nmstate:issues" -> project "nmstate/kubernetes-nmstate", role "issues"
```

### Bubbletea Model

```go
type TUIModel struct {
    workers   []WorkerState
    events    []Event
    width     int
    height    int
    frame     int           // animation frame counter
    logOffset int           // scroll position in activity log
    connected bool
    err       error
}
```

### Messages

```go
type eventMsg Event
type snapshotMsg StatusSnapshot
type tickMsg struct{}
type errMsg error
```

## Event Client

```go
type EventClient struct {
    conn       net.Conn
    socketPath string
}
```

### Methods

- `NewEventClient(socketPath string) (*EventClient, error)` -- connects to socket
- `(c *EventClient) RequestSnapshot(since time.Duration) (StatusSnapshot, error)`
- `(c *EventClient) RequestStream() (StatusSnapshot, <-chan Event, error)` -- returns initial snapshot and channel of events
- `(c *EventClient) Close() error`

## Tests

### `status_test.go`

- `TestStatusCommand_Connects` -- connects to socket and prints output
- `TestStatusCommand_NoSocket` -- prints error when daemon not running

### `tui_test.go`

- `TestTUIModel_Update` -- model handles event messages correctly
- `TestTUIModel_WorkerCards` -- worker cards render correctly
- `TestTUIModel_ScrollLog` -- log scrolling works
- `TestSpriteAnimation` -- sprite frames cycle correctly
- `TestParseWorkerProject` -- parses "owner/repo:role" into project and role
- `TestGroupWorkersByProject` -- groups workers by project correctly
- `TestConveyorBeltRendering` -- single-oompa and multi-oompa belts render correctly
- `TestBeltWidthAdapts` -- belt width scales with number of oompas
- `TestColumnTilingAdaptsToWidth` -- belts wrap to next row based on terminal width
- `TestShortWorkerName` -- compact names for activity log
