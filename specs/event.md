# Event Model and Socket Protocol

## Overview

The event system provides real-time observability into the oompa daemon via a Unix domain socket.
Goroutines emit structured events through an `EventEmitter` interface. A `SocketEventServer`
buffers events in a ring buffer and streams them to connected clients.

## Event Types

```go
type EventType string

const (
    EventWorkerStateChange EventType = "worker_state_change"
    EventActionStarted     EventType = "action_started"
    EventActionCompleted   EventType = "action_completed"
    EventAgentInvocation   EventType = "agent_invocation"
    EventAgentCompleted    EventType = "agent_completed"
    EventPollCycleStart    EventType = "poll_cycle_start"
    EventPollCycleEnd      EventType = "poll_cycle_end"
    EventError             EventType = "error"
)
```

## Event Struct

```go
type Event struct {
    Type      EventType `json:"type"`
    Timestamp time.Time `json:"timestamp"`
    Worker    string    `json:"worker"`              // e.g. "ovn-k/prs"
    State     string    `json:"state,omitempty"`      // idle, working, reviewing, rebasing, error, sleeping
    Action    string    `json:"action,omitempty"`     // human-readable description
    Detail    string    `json:"detail,omitempty"`     // extra context
    PRNumbers []int     `json:"prNumbers,omitempty"`  // associated PR numbers
    CostUSD   float64   `json:"costUSD,omitempty"`    // agent invocation cost
    Duration  float64   `json:"durationSec,omitempty"` // duration in seconds
    Error     string    `json:"error,omitempty"`      // error message
}
```

## EventEmitter Interface

```go
type EventEmitter interface {
    Emit(event Event)
}
```

### NoopEmitter

Zero-overhead implementation used when no socket server is running.

```go
type NoopEmitter struct{}
func (n *NoopEmitter) Emit(Event) {}
```

## WorkerState

```go
type WorkerState struct {
    Worker    string    `json:"worker"`
    State     string    `json:"state"`
    Action    string    `json:"action"`
    Detail    string    `json:"detail"`
    PRNumbers []int     `json:"prNumbers,omitempty"`
    LastEvent time.Time `json:"lastEvent"`
}
```

## SocketEventServer

```go
type SocketEventServer struct {
    socketPath string
    buffer     *RingBuffer
    mu         sync.RWMutex
    workers    map[string]*WorkerState
    clients    map[net.Conn]chan Event // per-client buffered channel for non-blocking broadcast
    listener   net.Listener
    startTime  time.Time
    logger     *slog.Logger
    done       chan struct{}
}
```

### Methods

- `NewSocketEventServer(socketPath string, bufferSize int, logger *slog.Logger) *SocketEventServer`
- `(s *SocketEventServer) Start(ctx context.Context) error` -- starts listening, returns error if socket creation fails
- `(s *SocketEventServer) Stop()` -- closes listener and all client connections
- `(s *SocketEventServer) Emit(event Event)` -- implements EventEmitter, adds to ring buffer, updates worker state, broadcasts to streaming clients
- `(s *SocketEventServer) Snapshot(since time.Duration) StatusSnapshot` -- returns current state + events within `since` window

### Ring Buffer

```go
type RingBuffer struct {
    events []Event
    size   int
    head   int
    count  int
    mu     sync.Mutex
}
```

- `NewRingBuffer(size int) *RingBuffer`
- `(r *RingBuffer) Add(event Event)`
- `(r *RingBuffer) Events(since time.Duration) []Event` -- returns events within time window
- `(r *RingBuffer) All() []Event` -- returns all buffered events

Default buffer size: 1000 events.

## Protocol

JSON-lines over Unix socket at `$XDG_RUNTIME_DIR/oompa/oompa.sock` (fallback: `/tmp/oompa/oompa.sock`).

### Client Request

```json
{"type": "stream"}
{"type": "snapshot", "since": "4h"}
```

### Server Response

For `snapshot` requests:
```go
type StatusSnapshot struct {
    Uptime   float64        `json:"uptimeSec"`
    Workers  []WorkerState  `json:"workers"`
    Events   []Event        `json:"events"`
}
```

For `stream` requests: the server first sends a `StatusSnapshot` JSON object (1h lookback window)
as the first newline-terminated message, then continuous newline-delimited `Event` JSON objects.
Clients should consume or skip the initial snapshot line before processing events.

When `since` is omitted from a `snapshot` request, the default lookback window is 4h.

### Socket Path Resolution

```go
func DefaultSocketPath() string
```

Uses `$XDG_RUNTIME_DIR/oompa/oompa.sock`, falls back to a per-user path under `os.TempDir()` (e.g. `/tmp/oompa-1000/oompa.sock`).

## Tests (`event_test.go`)

- `TestNoopEmitter` -- Emit does not panic
- `TestRingBuffer_Add` -- events are stored and retrievable
- `TestRingBuffer_Overflow` -- old events are evicted when buffer is full
- `TestRingBuffer_EventsSince` -- time-windowed retrieval works correctly
- `TestSocketEventServer_Snapshot` -- server returns correct snapshot
- `TestSocketEventServer_Stream` -- server streams events to connected clients
- `TestSocketEventServer_MultipleClients` -- multiple clients receive events
- `TestSocketEventServer_WorkerState` -- worker state is tracked correctly
- `TestDefaultSocketPath` -- returns correct path based on environment
