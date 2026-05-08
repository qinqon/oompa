package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// EventType represents the type of event emitted by the agent.
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

// Event represents a structured event emitted by the agent.
type Event struct {
	Type      EventType `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	Worker    string    `json:"worker"`                // e.g. "ovn-k/prs"
	State     string    `json:"state,omitempty"`       // idle, working, reviewing, rebasing, error, sleeping
	Action    string    `json:"action,omitempty"`      // human-readable description
	Detail    string    `json:"detail,omitempty"`      // extra context
	PRNumbers []int     `json:"prNumbers,omitempty"`   // associated PR numbers
	CostUSD   float64   `json:"costUSD,omitempty"`     // agent invocation cost
	Duration  float64   `json:"durationSec,omitempty"` // duration in seconds
	Error     string    `json:"error,omitempty"`       // error message
}

// EventEmitter is the interface for emitting events.
type EventEmitter interface {
	Emit(event Event)
}

// NoopEmitter is a zero-overhead implementation used when no socket server is running.
type NoopEmitter struct{}

// Emit does nothing.
func (n *NoopEmitter) Emit(Event) {}

// WorkerState tracks the current state of a single worker.
type WorkerState struct {
	Worker    string    `json:"worker"`
	State     string    `json:"state"`
	Action    string    `json:"action"`
	Detail    string    `json:"detail"`
	PRNumbers []int     `json:"prNumbers,omitempty"`
	LastEvent time.Time `json:"lastEvent"`
}

// StatusSnapshot is the response for a snapshot request.
type StatusSnapshot struct {
	Uptime  float64       `json:"uptimeSec"`
	Workers []WorkerState `json:"workers"`
	Events  []Event       `json:"events"`
}

// RingBuffer is a fixed-size circular buffer for events.
type RingBuffer struct {
	events []Event
	size   int
	head   int
	count  int
	mu     sync.Mutex
}

// NewRingBuffer creates a new ring buffer with the given capacity.
func NewRingBuffer(size int) *RingBuffer {
	return &RingBuffer{
		events: make([]Event, size),
		size:   size,
	}
}

// Add inserts an event into the buffer, evicting the oldest if full.
func (r *RingBuffer) Add(event Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events[r.head] = event
	r.head = (r.head + 1) % r.size
	if r.count < r.size {
		r.count++
	}
}

// All returns all buffered events in chronological order.
func (r *RingBuffer) All() []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.count == 0 {
		return nil
	}
	result := make([]Event, 0, r.count)
	start := (r.head - r.count + r.size) % r.size
	for i := range r.count {
		idx := (start + i) % r.size
		result = append(result, r.events[idx])
	}
	return result
}

// Events returns events within the given time window.
func (r *RingBuffer) Events(since time.Duration) []Event {
	cutoff := time.Now().Add(-since)
	all := r.All()
	var result []Event
	for _, e := range all {
		if !e.Timestamp.Before(cutoff) {
			result = append(result, e)
		}
	}
	return result
}

// DefaultSocketPath returns the Unix socket path for oompa IPC.
// Uses $XDG_RUNTIME_DIR/oompa/oompa.sock, falls back to a per-user
// path under os.TempDir() to avoid collisions on multi-user hosts.
func DefaultSocketPath() string {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return filepath.Join(dir, "oompa", "oompa.sock")
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("oompa-%d", os.Getuid()), "oompa.sock")
}
