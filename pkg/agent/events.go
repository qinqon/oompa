package agent

import (
	"log/slog"

	"github.com/qinqon/oompa/internal/events"
)

// The event model and socket server/client live in internal/events; these
// aliases keep the package-local names used by the agent and cmd/oompa.
type (
	// EventType represents the type of event emitted by the agent.
	EventType = events.EventType
	// EventCategory classifies events for filtering in the status/TUI views.
	EventCategory = events.EventCategory
	// Event is a single observability event emitted by the agent.
	Event = events.Event
	// EventEmitter receives events emitted by the agent.
	EventEmitter = events.EventEmitter
	// NoopEmitter discards all events.
	NoopEmitter = events.NoopEmitter
	// WorkerState describes one worker in a status snapshot.
	WorkerState = events.WorkerState
	// StatusSnapshot is the aggregate state served to status clients.
	StatusSnapshot = events.StatusSnapshot
	// RingBuffer retains the most recent events for late-joining clients.
	RingBuffer = events.RingBuffer
	// SocketEventServer serves events over a Unix socket.
	SocketEventServer = events.SocketEventServer
	// EventClient consumes events from a SocketEventServer.
	EventClient = events.EventClient
)

const (
	EventWorkerStateChange = events.EventWorkerStateChange
	EventActionStarted     = events.EventActionStarted
	EventActionCompleted   = events.EventActionCompleted
	EventAgentInvocation   = events.EventAgentInvocation
	EventAgentCompleted    = events.EventAgentCompleted
	EventPollCycleStart    = events.EventPollCycleStart
	EventPollCycleEnd      = events.EventPollCycleEnd
	EventError             = events.EventError

	CategoryPollCycle = events.CategoryPollCycle
	CategoryCheck     = events.CategoryCheck
	CategoryCleanup   = events.CategoryCleanup
	CategoryRebase    = events.CategoryRebase
	CategoryCI        = events.CategoryCI
	CategoryReview    = events.CategoryReview
	CategoryConflict  = events.CategoryConflict
	CategoryAgent     = events.CategoryAgent
	CategoryIssue     = events.CategoryIssue
	CategoryTriage    = events.CategoryTriage
	CategoryComment   = events.CategoryComment
	CategoryError     = events.CategoryError
	CategorySkip      = events.CategorySkip
)

// Function re-exports; see the internal/events documentation.

// DefaultEventCategories returns the categories shown by default.
func DefaultEventCategories() map[EventCategory]bool { return events.DefaultEventCategories() }

// AllEventCategories returns every known category, enabled.
func AllEventCategories() map[EventCategory]bool { return events.AllEventCategories() }

// ParseEventCategories parses a comma-separated category list.
func ParseEventCategories(s string) (map[EventCategory]bool, error) {
	return events.ParseEventCategories(s)
}

// NewRingBuffer creates a ring buffer retaining the last size events.
func NewRingBuffer(size int) *RingBuffer { return events.NewRingBuffer(size) }

// DefaultSocketPath returns the per-user default event socket path.
func DefaultSocketPath() string { return events.DefaultSocketPath() }

// NewSocketEventServer creates an event server listening on socketPath.
func NewSocketEventServer(socketPath string, bufferSize int, logger *slog.Logger) *SocketEventServer {
	return events.NewSocketEventServer(socketPath, bufferSize, logger)
}

// NewEventClient connects to the event server at socketPath.
func NewEventClient(socketPath string) (*EventClient, error) {
	return events.NewEventClient(socketPath)
}
