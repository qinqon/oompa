package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/qinqon/oompa/pkg/agent"
)

func TestStatusCommand_Connects(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "oompa.sock")
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	server := agent.NewSocketEventServer(sockPath, 100, logger)

	ctx := t.Context()

	if err := server.Start(ctx); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer server.Stop()

	// Emit an event so there's something to display
	server.Emit(agent.Event{
		Type:   agent.EventWorkerStateChange,
		Worker: "test/repo",
		State:  "working",
		Action: "Testing status command",
	})

	// Connect and get snapshot
	client, err := agent.NewEventClient(sockPath)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer client.Close()

	snap, err := client.RequestSnapshot(1 * time.Hour)
	if err != nil {
		t.Fatalf("failed to get snapshot: %v", err)
	}

	if len(snap.Workers) != 1 {
		t.Errorf("expected 1 worker, got %d", len(snap.Workers))
	}
	if snap.Workers[0].Worker != "test/repo" {
		t.Errorf("expected test/repo, got %s", snap.Workers[0].Worker)
	}

	// Verify printStatus doesn't panic
	printStatus(snap, 1*time.Hour)
}

func TestStatusCommand_NoSocket(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "nonexistent.sock")

	_, err := agent.NewEventClient(sockPath)
	if err == nil {
		t.Error("expected error when connecting to non-existent socket")
	}
}
