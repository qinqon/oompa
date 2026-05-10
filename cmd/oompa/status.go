package main

import (
	"flag"
	"fmt"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/qinqon/oompa/pkg/agent"
)

// statusFilters holds the parsed filter flags for the status command.
type statusFilters struct {
	categories map[agent.EventCategory]bool // nil = use default set
	allEvents  bool                         // show all categories
	project    string                       // partial match against worker project
	role       string                       // exact match against worker role
	prNumbers  []int                        // match events with these PR numbers
}

func runStatusCommand(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	since := fs.Duration("since", 4*time.Hour, "Lookback window for recent events")
	socketPath := fs.String("socket", "", "Override socket path (default: auto-detect)")
	allEvents := fs.Bool("all-events", false, "Show all events including poll cycles and routine checks")
	eventsFlag := fs.String("events", "", "Comma-separated event categories to show (e.g. ci,error,agent)")
	projectFlag := fs.String("project", "", "Filter by project (partial match against owner/repo)")
	roleFlag := fs.String("role", "", "Filter by worker role (e.g. prs, issues, triage)")
	prFlag := fs.String("pr", "", "Filter by PR number(s), comma-separated")
	fs.Parse(args) //nolint:errcheck // ExitOnError flag set handles parse errors

	// Parse filters
	var filters statusFilters
	filters.allEvents = *allEvents

	if *eventsFlag != "" {
		if filters.allEvents {
			fmt.Fprintf(os.Stderr, "Error: --all-events and --events cannot be used together\n")
			os.Exit(1)
		}
		cats, err := agent.ParseEventCategories(*eventsFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		filters.categories = cats
	}

	// Precompute effective category set so matchesEventFilter doesn't
	// allocate DefaultEventCategories() on every call.
	if !filters.allEvents && filters.categories == nil {
		filters.categories = agent.DefaultEventCategories()
	}

	filters.project = *projectFlag
	filters.role = *roleFlag

	if *prFlag != "" {
		for s := range strings.SplitSeq(*prFlag, ",") {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			n, err := strconv.Atoi(s)
			if err != nil || n <= 0 {
				fmt.Fprintf(os.Stderr, "Error: invalid PR number %q\n", s)
				os.Exit(1)
			}
			filters.prNumbers = append(filters.prNumbers, n)
		}
	}

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

	printStatus(snap, *since, filters)
}

// workerProject extracts the project part from a worker name (before ":").
func workerProject(worker string) string {
	if idx := strings.LastIndex(worker, ":"); idx >= 0 {
		return worker[:idx]
	}
	return worker
}

// workerRole extracts the role part from a worker name (after ":").
func workerRole(worker string) string {
	if idx := strings.LastIndex(worker, ":"); idx >= 0 {
		return worker[idx+1:]
	}
	return ""
}

// matchesWorkerFilter returns true if the worker matches the project/role/pr filters.
func matchesWorkerFilter(w agent.WorkerState, filters statusFilters) bool {
	if filters.project != "" {
		project := workerProject(w.Worker)
		if !strings.Contains(strings.ToLower(project), strings.ToLower(filters.project)) {
			return false
		}
	}
	if filters.role != "" {
		role := workerRole(w.Worker)
		if !strings.EqualFold(role, filters.role) {
			return false
		}
	}
	if len(filters.prNumbers) > 0 {
		matched := false
		for _, pr := range filters.prNumbers {
			if slices.Contains(w.PRNumbers, pr) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

// matchesEventFilter returns true if the event passes all active filters.
func matchesEventFilter(e agent.Event, filters statusFilters) bool {
	// Category filter (categories is precomputed at parse time; nil only when allEvents is true)
	if !filters.allEvents {
		if !filters.categories[e.Category] {
			return false
		}
	}

	// Project filter
	if filters.project != "" {
		project := workerProject(e.Worker)
		if !strings.Contains(strings.ToLower(project), strings.ToLower(filters.project)) {
			return false
		}
	}

	// Role filter
	if filters.role != "" {
		role := workerRole(e.Worker)
		if !strings.EqualFold(role, filters.role) {
			return false
		}
	}

	// PR filter
	if len(filters.prNumbers) > 0 {
		matched := false
		for _, pr := range filters.prNumbers {
			if slices.Contains(e.PRNumbers, pr) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	return true
}

func printStatus(snap agent.StatusSnapshot, since time.Duration, filters statusFilters) {
	// Filter workers
	var workers []agent.WorkerState
	for _, w := range snap.Workers {
		if matchesWorkerFilter(w, filters) {
			workers = append(workers, w)
		}
	}

	uptime := time.Duration(snap.Uptime * float64(time.Second))
	hours := int(uptime.Hours())
	minutes := int(uptime.Minutes()) % 60

	// Build header with filter info
	header := fmt.Sprintf("\nOOMPA STATUS (%d workers, uptime %dh %02dm", len(workers), hours, minutes)
	filterParts := buildFilterDescription(filters)
	if filterParts != "" {
		header += ", filter: " + filterParts
	}
	header += ")\n"
	fmt.Print(header)
	fmt.Println()

	// Sort workers by name
	sort.Slice(workers, func(i, j int) bool {
		return workers[i].Worker < workers[j].Worker
	})

	// Table header
	fmt.Printf("%-40s %-14s %-30s %s\n", "WORKER", "STATE", "CURRENT ACTION", "LAST EVENT")
	fmt.Println(strings.Repeat("\u2500", 105))

	for _, w := range workers {
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

	// Filter events
	var events []agent.Event
	for _, e := range snap.Events {
		if matchesEventFilter(e, filters) {
			events = append(events, e)
		}
	}

	// Recent activity
	fmt.Printf("\nRECENT ACTIVITY (last %s, %d events)\n\n", formatDuration(since), len(events))

	// Sort events by time descending
	sort.Slice(events, func(i, j int) bool {
		return events[i].Timestamp.After(events[j].Timestamp)
	})

	maxEvents := min(len(events), 20)

	for _, e := range events[:maxEvents] {
		ts := e.Timestamp.Local().Format("15:04")
		catTag := ""
		if e.Category != "" {
			catTag = fmt.Sprintf("[%-8s]", e.Category)
		} else {
			catTag = fmt.Sprintf("%-10s", "")
		}
		action := e.Action
		if e.Detail != "" {
			action += " \u2014 " + e.Detail
		}
		action = truncateRunes(action, 60)
		fmt.Printf("  %s  %s %-18s %s\n", ts, catTag, e.Worker, action)
	}

	remaining := len(events) - maxEvents
	if remaining > 0 {
		fmt.Printf("  ...\n  (%d more events)\n", remaining)
	}
	fmt.Println()
}

// buildFilterDescription creates a human-readable description of active filters.
func buildFilterDescription(filters statusFilters) string {
	var parts []string
	if filters.project != "" {
		parts = append(parts, "project="+filters.project)
	}
	if filters.role != "" {
		parts = append(parts, "role="+filters.role)
	}
	if len(filters.prNumbers) > 0 {
		nums := make([]string, len(filters.prNumbers))
		for i, n := range filters.prNumbers {
			nums[i] = fmt.Sprintf("#%d", n)
		}
		parts = append(parts, "pr="+strings.Join(nums, ","))
	}
	if filters.categories != nil {
		var cats []string
		for c := range filters.categories {
			cats = append(cats, string(c))
		}
		sort.Strings(cats)
		parts = append(parts, "events="+strings.Join(cats, ","))
	}
	if filters.allEvents {
		parts = append(parts, "all-events")
	}
	return strings.Join(parts, ", ")
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
