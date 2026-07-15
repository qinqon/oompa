package events

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNoopEmitter(t *testing.T) {
	e := &NoopEmitter{}
	// Should not panic
	e.Emit(Event{Type: EventWorkerStateChange, Worker: "test"})
}

func TestRingBuffer_Add(t *testing.T) {
	rb := NewRingBuffer(5)
	rb.Add(Event{Type: EventActionStarted, Worker: "w1", Action: "a1"})
	rb.Add(Event{Type: EventActionCompleted, Worker: "w2", Action: "a2"})

	all := rb.All()
	if len(all) != 2 {
		t.Fatalf("expected 2 events, got %d", len(all))
	}
	if all[0].Worker != "w1" {
		t.Errorf("expected w1, got %s", all[0].Worker)
	}
	if all[1].Worker != "w2" {
		t.Errorf("expected w2, got %s", all[1].Worker)
	}
}

func TestRingBuffer_Overflow(t *testing.T) {
	rb := NewRingBuffer(3)
	for i := range 5 {
		rb.Add(Event{Type: EventActionStarted, Worker: "w", Action: string(rune('A' + i))})
	}

	all := rb.All()
	if len(all) != 3 {
		t.Fatalf("expected 3 events, got %d", len(all))
	}
	// Should have events C, D, E (first two evicted)
	if all[0].Action != "C" {
		t.Errorf("expected C, got %s", all[0].Action)
	}
	if all[1].Action != "D" {
		t.Errorf("expected D, got %s", all[1].Action)
	}
	if all[2].Action != "E" {
		t.Errorf("expected E, got %s", all[2].Action)
	}
}

func TestRingBuffer_EventsSince(t *testing.T) {
	rb := NewRingBuffer(10)
	now := time.Now()

	rb.Add(Event{Type: EventActionStarted, Timestamp: now.Add(-3 * time.Hour), Worker: "old"})
	rb.Add(Event{Type: EventActionStarted, Timestamp: now.Add(-30 * time.Minute), Worker: "recent"})
	rb.Add(Event{Type: EventActionStarted, Timestamp: now.Add(-5 * time.Minute), Worker: "very_recent"})

	events := rb.Events(1 * time.Hour)
	if len(events) != 2 {
		t.Fatalf("expected 2 events within 1h, got %d", len(events))
	}
	if events[0].Worker != "recent" {
		t.Errorf("expected recent, got %s", events[0].Worker)
	}
	if events[1].Worker != "very_recent" {
		t.Errorf("expected very_recent, got %s", events[1].Worker)
	}
}

func TestDefaultEventCategories(t *testing.T) {
	defaults := DefaultEventCategories()

	// Should include actionable categories
	actionable := []EventCategory{CategoryRebase, CategoryCI, CategoryReview, CategoryConflict, CategoryAgent, CategoryIssue, CategoryTriage, CategoryComment, CategoryError}
	for _, cat := range actionable {
		if !defaults[cat] {
			t.Errorf("expected default categories to include %q", cat)
		}
	}

	// Should exclude noise categories
	noise := []EventCategory{CategoryPollCycle, CategoryCheck, CategoryCleanup, CategorySkip}
	for _, cat := range noise {
		if defaults[cat] {
			t.Errorf("expected default categories to exclude %q", cat)
		}
	}
}

func TestAllEventCategories(t *testing.T) {
	all := AllEventCategories()

	expected := []EventCategory{
		CategoryPollCycle, CategoryCheck, CategoryCleanup, CategoryRebase,
		CategoryCI, CategoryReview, CategoryConflict, CategoryAgent,
		CategoryIssue, CategoryTriage, CategoryComment, CategoryError, CategorySkip,
	}
	if len(all) != len(expected) {
		t.Errorf("expected %d categories, got %d", len(expected), len(all))
	}
	for _, cat := range expected {
		if !all[cat] {
			t.Errorf("expected all categories to include %q", cat)
		}
	}
}

func TestParseEventCategories(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    map[EventCategory]bool
		wantErr bool
	}{
		{
			name:  "single category",
			input: "ci",
			want:  map[EventCategory]bool{CategoryCI: true},
		},
		{
			name:  "multiple categories",
			input: "ci,error,agent",
			want:  map[EventCategory]bool{CategoryCI: true, CategoryError: true, CategoryAgent: true},
		},
		{
			name:  "with spaces",
			input: " ci , error ",
			want:  map[EventCategory]bool{CategoryCI: true, CategoryError: true},
		},
		{
			name:    "invalid category",
			input:   "ci,invalid",
			wantErr: true,
		},
		{
			name:    "empty string",
			input:   "",
			wantErr: true,
		},
		{
			name:    "only commas",
			input:   ",,",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseEventCategories(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseEventCategories() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				return
			}
			if len(got) != len(tt.want) {
				t.Errorf("ParseEventCategories() = %v, want %v", got, tt.want)
				return
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("ParseEventCategories() missing category %q", k)
				}
			}
		})
	}
}

func TestEventCategoryJSONRoundTrip(t *testing.T) {
	event := Event{
		Type:     EventActionStarted,
		Category: CategoryCI,
		Worker:   "test/repo:prs",
		Action:   "Checking CI",
	}
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	// Verify category is in the JSON
	if !strings.Contains(string(data), `"category":"ci"`) {
		t.Errorf("expected category in JSON, got: %s", string(data))
	}

	var decoded Event
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if decoded.Category != CategoryCI {
		t.Errorf("expected category ci, got %q", decoded.Category)
	}
}

func TestEventCategoryOmitEmpty(t *testing.T) {
	event := Event{
		Type:   EventActionStarted,
		Worker: "test/repo",
		Action: "Test action",
	}
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}
	if strings.Contains(string(data), `"category"`) {
		t.Errorf("expected category field to be omitted when empty, got: %s", string(data))
	}
}

func TestDefaultSocketPath(t *testing.T) {
	// Test with XDG_RUNTIME_DIR set
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	path := DefaultSocketPath()
	expected := "/run/user/1000/oompa/oompa.sock"
	if path != expected {
		t.Errorf("expected %s, got %s", expected, path)
	}

	// Test fallback (per-user path)
	t.Setenv("XDG_RUNTIME_DIR", "")
	path = DefaultSocketPath()
	expected = filepath.Join(os.TempDir(), fmt.Sprintf("oompa-%d", os.Getuid()), "oompa.sock")
	if path != expected {
		t.Errorf("expected %s, got %s", expected, path)
	}
}

func testSocketPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "oompa.sock")
}

// waitForClients polls until server has at least n streaming clients or timeout.
func waitForClients(t *testing.T, server *SocketEventServer, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if server.ClientCount() >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d clients (got %d)", n, server.ClientCount())
}

func TestSocketEventServer_Snapshot(t *testing.T) {
	sockPath := testSocketPath(t)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	server := NewSocketEventServer(sockPath, 100, logger)

	ctx := t.Context()

	if err := server.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer server.Stop()

	// Emit some events
	server.Emit(Event{Type: EventWorkerStateChange, Worker: "test/prs", State: "working", Action: "Investigating CI"})
	server.Emit(Event{Type: EventActionCompleted, Worker: "test/prs", Action: "CI investigation done"})

	// Connect and request snapshot
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close()

	req := `{"type":"snapshot","since":"1h"}` + "\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("failed to send request: %v", err)
	}

	conn.SetReadDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck // test helper, deadline failure will surface as read timeout
	r := bufio.NewReader(conn)
	line, err := r.ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	var snap StatusSnapshot
	if err := json.Unmarshal([]byte(strings.TrimRight(line, "\n")), &snap); err != nil {
		t.Fatalf("failed to parse snapshot: %v (%s)", err, line)
	}

	if len(snap.Workers) != 1 {
		t.Errorf("expected 1 worker, got %d", len(snap.Workers))
	}
	if snap.Workers[0].Worker != "test/prs" {
		t.Errorf("expected test/prs, got %s", snap.Workers[0].Worker)
	}
	if len(snap.Events) != 2 {
		t.Errorf("expected 2 events, got %d", len(snap.Events))
	}
	if snap.Uptime <= 0 {
		t.Errorf("expected positive uptime, got %f", snap.Uptime)
	}
}

func TestSocketEventServer_Stream(t *testing.T) {
	sockPath := testSocketPath(t)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	server := NewSocketEventServer(sockPath, 100, logger)

	ctx := t.Context()

	if err := server.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer server.Stop()

	// Connect and request stream
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close()

	req := `{"type":"stream"}` + "\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("failed to send request: %v", err)
	}

	conn.SetDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck // test helper, deadline failure will surface as read timeout
	r := bufio.NewReader(conn)

	// Read initial snapshot
	line, err := r.ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read initial snapshot: %v", err)
	}
	var snap StatusSnapshot
	if err := json.Unmarshal([]byte(strings.TrimRight(line, "\n")), &snap); err != nil {
		t.Fatalf("failed to parse initial snapshot: %v (%s)", err, line)
	}

	// Wait for client registration before emitting
	waitForClients(t, server, 1)

	// Emit an event
	server.Emit(Event{Type: EventWorkerStateChange, Worker: "test/issues", State: "working", Action: "Processing"})

	// Read streamed event
	line, err = r.ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read streamed event: %v", err)
	}
	var event Event
	if err := json.Unmarshal([]byte(strings.TrimRight(line, "\n")), &event); err != nil {
		t.Fatalf("failed to parse event: %v (%s)", err, line)
	}

	if event.Worker != "test/issues" {
		t.Errorf("expected test/issues, got %s", event.Worker)
	}
	if event.State != "working" {
		t.Errorf("expected working, got %s", event.State)
	}
}

func TestSocketEventServer_MultipleClients(t *testing.T) {
	sockPath := testSocketPath(t)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	server := NewSocketEventServer(sockPath, 100, logger)

	ctx := t.Context()

	if err := server.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer server.Stop()

	// Connect two streaming clients
	type streamConn struct {
		conn   net.Conn
		reader *bufio.Reader
	}
	var clients [2]streamConn
	for i := range clients {
		conn, err := net.Dial("unix", sockPath)
		if err != nil {
			t.Fatalf("client %d: failed to connect: %v", i, err)
		}

		req := `{"type":"stream"}` + "\n"
		if _, err := conn.Write([]byte(req)); err != nil {
			conn.Close()
			t.Fatalf("client %d: failed to send request: %v", i, err)
		}

		conn.SetDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck // test helper, deadline failure will surface as read timeout
		r := bufio.NewReader(conn)

		// Read and discard initial snapshot
		if _, err := r.ReadString('\n'); err != nil {
			conn.Close()
			t.Fatalf("client %d: failed to read initial snapshot: %v", i, err)
		}

		clients[i] = streamConn{conn: conn, reader: r}
	}
	defer func() {
		for _, c := range clients {
			c.conn.Close()
		}
	}()

	// Wait for both clients to register
	waitForClients(t, server, 2)

	// Emit event
	server.Emit(Event{Type: EventActionStarted, Worker: "shared/work", Action: "building"})

	// Both clients should receive the event
	for i, c := range clients {
		line, err := c.reader.ReadString('\n')
		if err != nil {
			t.Fatalf("client %d: failed to read event: %v", i, err)
		}
		var event Event
		if err := json.Unmarshal([]byte(strings.TrimRight(line, "\n")), &event); err != nil {
			t.Fatalf("client %d: failed to parse event: %v (%s)", i, err, line)
		}
		if event.Worker != "shared/work" {
			t.Errorf("client %d: expected shared/work, got %s", i, event.Worker)
		}
	}
}

func TestSocketEventServer_WorkerState(t *testing.T) {
	sockPath := testSocketPath(t)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	server := NewSocketEventServer(sockPath, 100, logger)

	ctx := t.Context()

	if err := server.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer server.Stop()

	// Emit events that update worker state
	server.Emit(Event{Type: EventWorkerStateChange, Worker: "w1", State: "idle", Action: "Waiting"})
	server.Emit(Event{Type: EventWorkerStateChange, Worker: "w1", State: "working", Action: "Processing issue", PRNumbers: []int{42}})

	snap := server.Snapshot(1 * time.Hour)
	if len(snap.Workers) != 1 {
		t.Fatalf("expected 1 worker, got %d", len(snap.Workers))
	}
	w := snap.Workers[0]
	if w.State != "working" {
		t.Errorf("expected working, got %s", w.State)
	}
	if w.Action != "Processing issue" {
		t.Errorf("expected 'Processing issue', got %s", w.Action)
	}
	if len(w.PRNumbers) != 1 || w.PRNumbers[0] != 42 {
		t.Errorf("expected PRNumbers [42], got %v", w.PRNumbers)
	}
}
