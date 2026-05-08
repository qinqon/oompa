package agent

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"time"
)

// EventClient connects to the oompa daemon's Unix socket for reading events.
type EventClient struct {
	conn       net.Conn
	socketPath string
}

// NewEventClient connects to the daemon's Unix socket.
func NewEventClient(socketPath string) (*EventClient, error) {
	conn, err := net.DialTimeout("unix", socketPath, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("connecting to oompa daemon at %s: %w", socketPath, err)
	}
	return &EventClient{
		conn:       conn,
		socketPath: socketPath,
	}, nil
}

// RequestSnapshot sends a snapshot request and returns the response.
func (c *EventClient) RequestSnapshot(since time.Duration) (StatusSnapshot, error) {
	req := clientRequest{
		Type:  "snapshot",
		Since: since.String(),
	}
	data, err := json.Marshal(req)
	if err != nil {
		return StatusSnapshot{}, fmt.Errorf("marshaling request: %w", err)
	}
	data = append(data, '\n')
	if _, err := c.conn.Write(data); err != nil {
		return StatusSnapshot{}, fmt.Errorf("sending request: %w", err)
	}

	// Set read deadline for snapshot response
	c.conn.SetReadDeadline(time.Now().Add(10 * time.Second)) //nolint:errcheck // best effort

	// Use json.Decoder to avoid scanner line-length limits on large snapshots
	decoder := json.NewDecoder(c.conn)
	var snap StatusSnapshot
	if err := decoder.Decode(&snap); err != nil {
		return StatusSnapshot{}, fmt.Errorf("parsing response: %w", err)
	}
	return snap, nil
}

// RequestStream sends a stream request and returns the initial snapshot along
// with a channel of subsequent events. The snapshot and event stream are read
// from a single connection, avoiding the race condition where events can be
// missed between a separate snapshot request and the start of streaming.
// The channel is closed when the connection is closed or an error occurs.
func (c *EventClient) RequestStream() (StatusSnapshot, <-chan Event, error) {
	req := clientRequest{Type: "stream"}
	data, err := json.Marshal(req)
	if err != nil {
		return StatusSnapshot{}, nil, fmt.Errorf("marshaling request: %w", err)
	}
	data = append(data, '\n')
	if _, err := c.conn.Write(data); err != nil {
		return StatusSnapshot{}, nil, fmt.Errorf("sending request: %w", err)
	}

	// Clear any read deadline for streaming
	c.conn.SetReadDeadline(time.Time{}) //nolint:errcheck // best effort

	eventCh := make(chan Event, 100)
	snapCh := make(chan StatusSnapshot, 1)
	errCh := make(chan error, 1)

	go func() {
		defer close(eventCh)
		decoder := json.NewDecoder(c.conn)

		// The first JSON value is the initial StatusSnapshot.
		var snap StatusSnapshot
		if err := decoder.Decode(&snap); err != nil {
			errCh <- fmt.Errorf("decoding initial snapshot: %w", err)
			return
		}
		snapCh <- snap

		// Subsequent values are Event objects.
		for {
			var event Event
			if err := decoder.Decode(&event); err != nil {
				slog.Debug("stream decode error (connection likely closed)", "error", err)
				return
			}
			eventCh <- event
		}
	}()

	select {
	case snap := <-snapCh:
		return snap, eventCh, nil
	case err := <-errCh:
		return StatusSnapshot{}, nil, err
	case <-time.After(10 * time.Second):
		return StatusSnapshot{}, nil, fmt.Errorf("timed out waiting for initial snapshot")
	}
}

// Close closes the connection.
func (c *EventClient) Close() error {
	return c.conn.Close()
}
