package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/qinqon/oompa/pkg/agent"
)

func runTUICommand(args []string) {
	fs := flag.NewFlagSet("tui", flag.ExitOnError)
	socketPath := fs.String("socket", "", "Override socket path (default: auto-detect)")
	fs.Parse(args) //nolint:errcheck // ExitOnError flag set handles parse errors

	sock := *socketPath
	if sock == "" {
		sock = agent.DefaultSocketPath()
	}

	// Use a single connection: RequestStream returns the initial snapshot
	// and event channel over one connection, avoiding the race condition
	// where events could be missed between a separate snapshot and stream.
	client, err := agent.NewEventClient(sock)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: could not connect to oompa daemon at %s\n", sock)
		fmt.Fprintln(os.Stderr, "Is oompa running? Check with: systemctl --user status oompa")
		os.Exit(1)
	}

	snap, eventCh, err := client.RequestStream()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to start event stream: %v\n", err)
		client.Close()
		os.Exit(1)
	}

	model := newTUIModel(snap, eventCh, client)

	p := tea.NewProgram(model, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running TUI: %v\n", err)
		os.Exit(1)
	}
}

// TUI styles
var (
	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("229")).
			Background(lipgloss.Color("57")).
			Padding(0, 1)

	cardStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("240")).
			Padding(0, 1).
			Width(28)

	activeCardStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("39")).
			Padding(0, 1).
			Width(28)

	errorCardStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("196")).
			Padding(0, 1).
			Width(28)

	logStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color("240")).
			Padding(0, 1)

	titleStyle = lipgloss.NewStyle().Bold(true)

	dimStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))

	statusWorking = lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true)
	statusIdle    = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	statusError   = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)
)

// spriteFrames defines animation frames for oompa-loompa sprites per state.
var spriteFrames = map[string][][]string{
	"working": {
		{"   ___   ", "  (o.o)  ", " --|--|\\  ", "   |  |  ", "  _/  \\_ "},
		{"   ___   ", "  (o.o)  ", "  /|--|-- ", "   |  |  ", "  _/  \\_ "},
	},
	"idle": {
		{"   ___   ", "  (o.o)  ", " --|--|-- ", "   |  |  ", "  _/  \\_ "},
		{"   ___   ", "  (o.o)  ", " --|--|-- ", "   |  |  ", " _/  \\_  "},
	},
	"sleeping": {
		{"   ___   ", "  (-.- )Z", "   |__|  ", "  _/  \\_ ", " sitting "},
		{"   ___   ", "  (-.- )Zz", "   |__|  ", "  _/  \\_ ", " sitting "},
		{"   ___    ", "  (-.- )Zzz", "   |__|  ", "  _/  \\_ ", " sitting "},
	},
	"error": {
		{"   ___   ", "  (x.x)  ", " --|--|-- ", "   |  |  ", "  _/  \\_ "},
		{"   ___   ", "  (x.x) *", " --|--|-- ", "   |  |  ", "  _/  \\_ "},
		{"   ___   ", "  (x.x)**", " --|--|-- ", "   |  |  ", "  _/  \\_ "},
	},
	"reviewing": {
		{"   ___   ", "  (o.o)  ", " --|--|Q ", "   |  |  ", "  _/  \\_ "},
		{"   ___   ", "  (o.o)  ", " --|--|q ", "   |  |  ", "  _/  \\_ "},
	},
	"rebasing": {
		{"   ___   ", "  (o.o)  ", " --|--|>[", "   |  |  ", "  _/  \\_ "},
		{"   ___   ", "  (o.o)  ", " --|--|>]", "   |  |  ", "  _/  \\_ "},
	},
}

// TUIModel is the bubbletea model for the live TUI dashboard.
type TUIModel struct {
	workers      []agent.WorkerState
	events       []agent.Event
	width        int
	height       int
	frame        int // animation frame counter
	logOffset    int // scroll position in activity log
	connected    bool
	eventCh      <-chan agent.Event
	streamClient *agent.EventClient
	uptime       float64
}

type eventMsg agent.Event
type tickMsg struct{}
type disconnectedMsg struct{}

func newTUIModel(snap agent.StatusSnapshot, eventCh <-chan agent.Event, streamClient *agent.EventClient) TUIModel {
	return TUIModel{
		workers:      snap.Workers,
		events:       snap.Events,
		connected:    true,
		eventCh:      eventCh,
		streamClient: streamClient,
		uptime:       snap.Uptime,
	}
}

func (m TUIModel) Init() tea.Cmd {
	return tea.Batch(tickAnimation(), listenForEvents(m.eventCh))
}

func tickAnimation() tea.Cmd {
	return tea.Tick(250*time.Millisecond, func(_ time.Time) tea.Msg {
		return tickMsg{}
	})
}

func listenForEvents(ch <-chan agent.Event) tea.Cmd {
	return func() tea.Msg {
		event, ok := <-ch
		if !ok {
			return disconnectedMsg{}
		}
		return eventMsg(event)
	}
}

func (m TUIModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			if m.streamClient != nil {
				m.streamClient.Close()
			}
			return m, tea.Quit
		case "up", "k":
			if m.logOffset > 0 {
				m.logOffset--
			}
		case "down", "j":
			maxOffset := max(len(m.events)-10, 0)
			if m.logOffset < maxOffset {
				m.logOffset++
			}
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case tickMsg:
		m.frame++
		m.uptime += 0.25
		return m, tickAnimation()

	case disconnectedMsg:
		m.connected = false
		return m, nil

	case eventMsg:
		event := agent.Event(msg)
		m.events = append(m.events, event)

		// Update worker state
		found := false
		for i, w := range m.workers {
			if w.Worker != event.Worker {
				continue
			}
			if event.State != "" {
				m.workers[i].State = event.State
			}
			if event.Action != "" {
				m.workers[i].Action = event.Action
			}
			if event.Detail != "" {
				m.workers[i].Detail = event.Detail
			}
			if len(event.PRNumbers) > 0 {
				m.workers[i].PRNumbers = event.PRNumbers
			}
			m.workers[i].LastEvent = event.Timestamp
			found = true
			break
		}
		if !found && event.Worker != "" {
			m.workers = append(m.workers, agent.WorkerState{
				Worker:    event.Worker,
				State:     event.State,
				Action:    event.Action,
				Detail:    event.Detail,
				PRNumbers: event.PRNumbers,
				LastEvent: event.Timestamp,
			})
		}

		// Auto-scroll to top (newest event first in the sorted view)
		m.logOffset = 0

		return m, listenForEvents(m.eventCh)
	}

	return m, nil
}

func (m TUIModel) View() string {
	if m.width == 0 {
		return "Loading..."
	}

	var b strings.Builder

	// Header
	uptime := time.Duration(m.uptime * float64(time.Second))
	hours := int(uptime.Hours())
	minutes := int(uptime.Minutes()) % 60
	connStatus := "Connected"
	if !m.connected {
		connStatus = "Disconnected"
	}
	header := headerStyle.Width(m.width).Render(
		fmt.Sprintf("  OOMPA FACTORY                                      %s  %02d:%02d UTC  uptime %dh%02dm",
			connStatus,
			time.Now().UTC().Hour(),
			time.Now().UTC().Minute(),
			hours, minutes,
		),
	)
	b.WriteString(header + "\n\n")

	// Worker cards in rows of 3
	sort.Slice(m.workers, func(i, j int) bool {
		return m.workers[i].Worker < m.workers[j].Worker
	})

	cardsPerRow := 3
	if m.width < 100 {
		cardsPerRow = 2
	}
	if m.width < 65 {
		cardsPerRow = 1
	}

	for i := 0; i < len(m.workers); i += cardsPerRow {
		var cards []string
		for j := range cardsPerRow {
			if i+j >= len(m.workers) {
				break
			}
			cards = append(cards, m.renderWorkerCard(m.workers[i+j]))
		}
		b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, cards...) + "\n")
	}

	// Activity log
	availableHeight := max(m.height-lipgloss.Height(b.String())-4, 3)

	b.WriteString(m.renderActivityLog(availableHeight))
	b.WriteString(dimStyle.Render("  q: quit  j/k: scroll log"))

	return b.String()
}

func (m TUIModel) renderWorkerCard(w agent.WorkerState) string {
	state := w.State
	if state == "" {
		state = "idle"
	}

	// Get sprite for this state
	spriteKey := state
	frames, ok := spriteFrames[spriteKey]
	if !ok {
		frames = spriteFrames["idle"]
	}

	frameIdx := (m.frame / 2) % len(frames) // change every 500ms
	sprite := frames[frameIdx]

	// Build card content
	var lines []string

	// Worker name with PR info
	name := w.Worker
	if len(w.PRNumbers) > 0 {
		nums := make([]string, len(w.PRNumbers))
		for i, n := range w.PRNumbers {
			nums[i] = fmt.Sprintf("%d", n)
		}
		name += " [" + strings.Join(nums, ",") + "]"
	}
	lines = append(lines, titleStyle.Render(truncateRunes(name, 26)))

	// Sprite
	lines = append(lines, sprite...)

	// State
	stateStr := stateIcon(state) + " " + state
	switch state {
	case "working", "reviewing", "rebasing":
		lines = append(lines, statusWorking.Render(stateStr))
	case "error", "stuck":
		lines = append(lines, statusError.Render(stateStr))
	default:
		lines = append(lines, statusIdle.Render(stateStr))
	}

	// Action
	action := w.Action
	if action != "" {
		lines = append(lines, truncateRunes(action, 26))
	}
	// Detail
	detail := w.Detail
	if detail != "" {
		lines = append(lines, dimStyle.Render(truncateRunes(detail, 26)))
	}

	content := strings.Join(lines, "\n")

	// Choose card style
	style := cardStyle
	switch state {
	case "working", "reviewing", "rebasing":
		style = activeCardStyle
	case "error", "stuck":
		style = errorCardStyle
	}

	return style.Render(content)
}

func (m TUIModel) renderActivityLog(height int) string {
	title := fmt.Sprintf(" Activity Log (%d events)", len(m.events))

	// Sort events newest first for display
	sortedEvents := make([]agent.Event, len(m.events))
	copy(sortedEvents, m.events)
	sort.Slice(sortedEvents, func(i, j int) bool {
		return sortedEvents[i].Timestamp.After(sortedEvents[j].Timestamp)
	})

	// Apply scroll offset
	startIdx := m.logOffset
	if startIdx >= len(sortedEvents) {
		startIdx = 0
	}
	endIdx := min(
		// -2 for title and border
		startIdx+height-2, len(sortedEvents))

	var lines []string
	lines = append(lines, titleStyle.Render(title))
	for _, e := range sortedEvents[startIdx:endIdx] {
		ts := e.Timestamp.Local().Format("15:04:05")
		action := e.Action
		if e.Detail != "" {
			action += " - " + e.Detail
		}
		maxActionLen := max(m.width-35, 20)
		action = truncateRunes(action, maxActionLen)
		line := fmt.Sprintf(" %s  %-18s %s", dimStyle.Render(ts), e.Worker, action)
		lines = append(lines, line)
	}

	content := strings.Join(lines, "\n")
	return logStyle.Width(m.width-2).Render(content) + "\n"
}

// truncateRunes truncates a string to maxLen runes, appending "..." if truncated.
// Operates on runes to avoid splitting multi-byte UTF-8 sequences.
func truncateRunes(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return "..."
	}
	return string(runes[:maxLen-3]) + "..."
}
