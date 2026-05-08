package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/qinqon/oompa/pkg/agent"
)

func runStatusCommand(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	since := fs.Duration("since", 4*time.Hour, "Lookback window for recent events")
	socketPath := fs.String("socket", "", "Override socket path (default: auto-detect)")
	fs.Parse(args) //nolint:errcheck // ExitOnError flag set handles parse errors

	sock := *socketPath
	if sock == "" {
		sock = agent.DefaultSocketPath()
	}

	client, err := agent.NewEventClient(sock)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: could not connect to oompa daemon at %s\n", sock)
		fmt.Fprintln(os.Stderr, "Is oompa running? Check with: systemctl --user status oompa")
		os.Exit(1)
	}

	snap, err := client.RequestSnapshot(*since)
	client.Close()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to get status: %v\n", err)
		os.Exit(1)
	}

	printStatus(snap, *since)
}

func printStatus(snap agent.StatusSnapshot, since time.Duration) {
	uptime := time.Duration(snap.Uptime * float64(time.Second))
	hours := int(uptime.Hours())
	minutes := int(uptime.Minutes()) % 60

	fmt.Printf("\nOOMPA STATUS (%d workers, uptime %dh %02dm)\n\n", len(snap.Workers), hours, minutes)

	// Sort workers by name
	sort.Slice(snap.Workers, func(i, j int) bool {
		return snap.Workers[i].Worker < snap.Workers[j].Worker
	})

	// Table header
	fmt.Printf("%-40s %-14s %-30s %s\n", "WORKER", "STATE", "CURRENT ACTION", "LAST EVENT")
	fmt.Println(strings.Repeat("\u2500", 105))

	for _, w := range snap.Workers {
		icon := stateIcon(w.State)
		ago := formatAgo(w.LastEvent)
		prInfo := ""
		if len(w.PRNumbers) > 0 {
			nums := make([]string, len(w.PRNumbers))
			for i, n := range w.PRNumbers {
				nums[i] = fmt.Sprintf("#%d", n)
			}
			prInfo = " [" + strings.Join(nums, ",") + "]"
		}
		workerCol := w.Worker + prInfo
		action := truncateRunes(w.Action, 30)
		fmt.Printf("%-40s %s %-12s %-30s %s\n", workerCol, icon, w.State, action, ago)
	}

	// Recent activity
	fmt.Printf("\nRECENT ACTIVITY (last %s, %d events)\n\n", formatDuration(since), len(snap.Events))

	// Sort events by time descending
	sort.Slice(snap.Events, func(i, j int) bool {
		return snap.Events[i].Timestamp.After(snap.Events[j].Timestamp)
	})

	maxEvents := min(len(snap.Events), 20)

	for _, e := range snap.Events[:maxEvents] {
		ts := e.Timestamp.Local().Format("15:04")
		action := e.Action
		if e.Detail != "" {
			action += " — " + e.Detail
		}
		if len(action) > 70 {
			action = action[:67] + "..."
		}
		fmt.Printf("  %s  %-20s %s\n", ts, e.Worker, action)
	}

	remaining := len(snap.Events) - maxEvents
	if remaining > 0 {
		fmt.Printf("  ...\n  (%d more events)\n", remaining)
	}
	fmt.Println()
}

func stateIcon(state string) string {
	switch state {
	case "working", "reviewing", "rebasing":
		return "\u25cf" // ●
	case "idle":
		return "\u25cb" // ○
	case "scheduled", "sleeping":
		return "\u25d0" // ◐
	case "error", "stuck":
		return "\u2716" // ✖
	default:
		return "\u25cb" // ○
	}
}

func formatAgo(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func formatDuration(d time.Duration) string {
	if d >= time.Hour {
		h := int(d.Hours())
		m := int(d.Minutes()) % 60
		if m > 0 {
			return fmt.Sprintf("%dh%dm", h, m)
		}
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}
