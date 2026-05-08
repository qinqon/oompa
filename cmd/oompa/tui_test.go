package main

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/qinqon/oompa/pkg/agent"
)

func TestTUIModel_Update(t *testing.T) {
	snap := agent.StatusSnapshot{
		Uptime: 3600,
		Workers: []agent.WorkerState{
			{Worker: "test/repo", State: "idle", Action: "Waiting"},
		},
	}
	ch := make(chan agent.Event, 10)
	model := newTUIModel(snap, ch, nil)

	// Send an event message
	event := agent.Event{
		Type:      agent.EventWorkerStateChange,
		Worker:    "test/repo",
		State:     "working",
		Action:    "Processing issue",
		Timestamp: time.Now(),
	}
	updated, _ := model.Update(eventMsg(event))
	m := updated.(TUIModel)

	if len(m.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(m.events))
	}
	if m.workers[0].State != "working" {
		t.Errorf("expected worker state 'working', got %s", m.workers[0].State)
	}
	if m.workers[0].Action != "Processing issue" {
		t.Errorf("expected action 'Processing issue', got %s", m.workers[0].Action)
	}

	// Test disconnected message
	updated, _ = model.Update(disconnectedMsg{})
	m = updated.(TUIModel)
	if m.connected {
		t.Error("expected connected=false after disconnectedMsg")
	}
}

func TestTUIModel_WorkerCards(t *testing.T) {
	snap := agent.StatusSnapshot{
		Uptime: 7200,
		Workers: []agent.WorkerState{
			{Worker: "project/prs", State: "working", Action: "Investigating CI", PRNumbers: []int{42, 99}},
			{Worker: "project/issues", State: "idle", Action: "Waiting"},
		},
	}
	ch := make(chan agent.Event, 10)
	model := newTUIModel(snap, ch, nil)
	model.width = 120
	model.height = 40

	// Render a worker card
	card := model.renderWorkerCard(snap.Workers[0])
	if card == "" {
		t.Error("expected non-empty card rendering")
	}

	// Full view should render without panic
	view := model.View()
	if view == "" {
		t.Error("expected non-empty view")
	}
}

func TestTUIModel_ScrollLog(t *testing.T) {
	now := time.Now()
	events := make([]agent.Event, 20)
	for i := range events {
		events[i] = agent.Event{
			Type:      agent.EventActionStarted,
			Worker:    "test/repo",
			Action:    "Action",
			Timestamp: now.Add(time.Duration(i) * time.Minute),
		}
	}
	snap := agent.StatusSnapshot{
		Uptime: 3600,
		Events: events,
	}
	ch := make(chan agent.Event, 10)
	model := newTUIModel(snap, ch, nil)
	model.width = 120
	model.height = 40

	// Initial offset should be 0 (newest first)
	if model.logOffset != 0 {
		t.Errorf("expected initial logOffset 0, got %d", model.logOffset)
	}

	// Scroll down
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	m := updated.(TUIModel)
	if m.logOffset != 1 {
		t.Errorf("expected logOffset 1 after scroll down, got %d", m.logOffset)
	}

	// Scroll up
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	m = updated.(TUIModel)
	if m.logOffset != 0 {
		t.Errorf("expected logOffset 0 after scroll up, got %d", m.logOffset)
	}

	// Don't scroll below 0
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	m = updated.(TUIModel)
	if m.logOffset != 0 {
		t.Errorf("expected logOffset to stay at 0, got %d", m.logOffset)
	}
}

func TestSpriteAnimation(t *testing.T) {
	// Verify all sprite states have at least one frame
	states := []string{"working", "idle", "sleeping", "error", "reviewing", "rebasing"}
	for _, state := range states {
		frames, ok := spriteFrames[state]
		if !ok {
			t.Errorf("missing sprite frames for state %q", state)
			continue
		}
		if len(frames) == 0 {
			t.Errorf("expected at least 1 frame for state %q", state)
		}
		for i, frame := range frames {
			if len(frame) == 0 {
				t.Errorf("frame %d for state %q has no lines", i, state)
			}
		}
	}

	// Verify frame cycling works
	snap := agent.StatusSnapshot{
		Workers: []agent.WorkerState{
			{Worker: "test", State: "working"},
		},
	}
	ch := make(chan agent.Event, 10)
	model := newTUIModel(snap, ch, nil)
	model.width = 120
	model.height = 40

	// Render at different frames to ensure no panic
	for frame := range 10 {
		model.frame = frame
		card := model.renderWorkerCard(snap.Workers[0])
		if card == "" {
			t.Errorf("empty card at frame %d", frame)
		}
	}
}
