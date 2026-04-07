package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// State holds all active issue work.
type State struct {
	ActiveIssues map[int]*IssueWork `json:"activeIssues"`
	path         string
}

// LoadState reads state from the given file path. Returns empty state if the
// file doesn't exist or contains corrupt JSON.
func LoadState(path string) *State {
	s := &State{
		ActiveIssues: make(map[int]*IssueWork),
		path:         path,
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return s
	}

	if err := json.Unmarshal(data, s); err != nil {
		s.ActiveIssues = make(map[int]*IssueWork)
		return s
	}

	if s.ActiveIssues == nil {
		s.ActiveIssues = make(map[int]*IssueWork)
	}

	return s
}

// Save writes the current state to the configured file path, creating parent
// directories as needed.
func (s *State) Save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(s.path, data, 0o644)
}
