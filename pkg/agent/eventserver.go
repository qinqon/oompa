package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// clientRequest is the JSON-lines request from a client.
type clientRequest struct {
	Type  string `json:"type"`            // "stream" or "snapshot"
	Since string `json:"since,omitempty"` // duration string for snapshot (e.g. "4h")
}

// SocketEventServer listens on a Unix socket and broadcasts events to connected clients.
type SocketEventServer struct {
	socketPath string
	buffer     *RingBuffer
	mu         sync.RWMutex
	workers    map[string]*WorkerState
	clients    map[net.Conn]chan Event // streaming clients with their event channels
	listener   net.Listener
	startTime  time.Time
	logger     *slog.Logger
	done       chan struct{}
}

// NewSocketEventServer creates a new event server.
func NewSocketEventServer(socketPath string, bufferSize int, logger *slog.Logger) *SocketEventServer {
	return &SocketEventServer{
		socketPath: socketPath,
		buffer:     NewRingBuffer(bufferSize),
		workers:    make(map[string]*WorkerState),
		clients:    make(map[net.Conn]chan Event),
		startTime:  time.Now(),
		logger:     logger,
		done:       make(chan struct{}),
	}
}

// Start begins listening on the Unix socket. It blocks accepting connections
// until the context is cancelled or Stop is called.
func (s *SocketEventServer) Start(ctx context.Context) error {
	// Ensure parent directory exists
	dir := filepath.Dir(s.socketPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	// Remove stale socket file
	os.Remove(s.socketPath) //nolint:errcheck // best effort

	ln, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return err
	}
	s.listener = ln

	go func() {
		<-ctx.Done()
		s.Stop()
	}()

	go s.acceptLoop()

	return nil
}

// Stop closes the listener and all client connections.
func (s *SocketEventServer) Stop() {
	select {
	case <-s.done:
		return // already stopped
	default:
	}
	close(s.done)

	if s.listener != nil {
		s.listener.Close()
	}

	s.mu.Lock()
	for conn, ch := range s.clients {
		close(ch)
		conn.Close()
		delete(s.clients, conn)
	}
	s.mu.Unlock()

	// Clean up socket file
	os.Remove(s.socketPath) //nolint:errcheck // best effort
}

// Emit implements EventEmitter. It adds the event to the ring buffer,
// updates worker state, and broadcasts to streaming clients.
func (s *SocketEventServer) Emit(event Event) {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	s.buffer.Add(event)

	// Update worker state (only for worker-scoped events)
	if event.Worker != "" {
		s.mu.Lock()
		ws, ok := s.workers[event.Worker]
		if !ok {
			ws = &WorkerState{Worker: event.Worker}
			s.workers[event.Worker] = ws
		}
		if event.State != "" {
			ws.State = event.State
		}
		if event.Action != "" {
			ws.Action = event.Action
		}
		if event.Detail != "" {
			ws.Detail = event.Detail
		}
		if len(event.PRNumbers) > 0 {
			ws.PRNumbers = event.PRNumbers
		}
		ws.LastEvent = event.Timestamp
		s.mu.Unlock()
	}

	// Broadcast every event to streaming clients (worker-scoped or not).
	// Hold the read lock so Stop cannot close channels mid-broadcast.
	s.mu.RLock()
	for _, ch := range s.clients {
		select {
		case ch <- event:
		default:
			// Client is slow, drop event
		}
	}
	s.mu.RUnlock()
}

// Snapshot returns the current state and events within the time window.
func (s *SocketEventServer) Snapshot(since time.Duration) StatusSnapshot {
	s.mu.RLock()
	workers := make([]WorkerState, 0, len(s.workers))
	for _, ws := range s.workers {
		workers = append(workers, *ws)
	}
	s.mu.RUnlock()

	return StatusSnapshot{
		Uptime:  time.Since(s.startTime).Seconds(),
		Workers: workers,
		Events:  s.buffer.Events(since),
	}
}

// ClientCount returns the number of connected streaming clients.
func (s *SocketEventServer) ClientCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.clients)
}

func (s *SocketEventServer) acceptLoop() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.done:
				return
			default:
				s.logger.Debug("accept error", "error", err)
				continue
			}
		}
		go s.handleClient(conn)
	}
}

func (s *SocketEventServer) handleClient(conn net.Conn) {
	defer conn.Close()

	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		return
	}

	var req clientRequest
	if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
		s.logger.Debug("invalid client request", "error", err)
		return
	}

	switch req.Type {
	case "snapshot":
		since := 4 * time.Hour
		if req.Since != "" {
			if d, err := time.ParseDuration(req.Since); err == nil {
				since = d
			}
		}
		snap := s.Snapshot(since)
		data, err := json.Marshal(snap)
		if err != nil {
			s.logger.Debug("failed to marshal snapshot", "error", err)
			return
		}
		data = append(data, '\n')
		conn.Write(data) //nolint:errcheck // best effort

	case "stream":
		// Send initial snapshot first
		snap := s.Snapshot(1 * time.Hour)
		data, err := json.Marshal(snap)
		if err != nil {
			s.logger.Debug("failed to marshal initial snapshot", "error", err)
			return
		}
		data = append(data, '\n')
		if _, err := conn.Write(data); err != nil {
			s.logger.Debug("failed to send initial snapshot to client", "error", err)
			return
		}

		// Register as streaming client
		ch := make(chan Event, 100)
		s.mu.Lock()
		s.clients[conn] = ch
		s.mu.Unlock()

		defer func() {
			s.mu.Lock()
			delete(s.clients, conn)
			s.mu.Unlock()
		}()

		encoder := json.NewEncoder(conn)
		for {
			select {
			case event, ok := <-ch:
				if !ok {
					return
				}
				if err := encoder.Encode(event); err != nil {
					return
				}
			case <-s.done:
				return
			}
		}
	}
}
