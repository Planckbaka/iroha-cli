package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// WorktreeEntry represents an isolated branch directory mapping for a specific task.
type WorktreeEntry struct {
	Name        string    `json:"name"`
	Path        string    `json:"path"`
	Branch      string    `json:"branch"`
	TaskID      string    `json:"task_id"`
	Status      string    `json:"status"` // "active", "kept", "removed"
	LastEntered time.Time `json:"last_entered"`
}

// WorktreeConfig holds the serialized worktree index state.
type WorktreeConfig struct {
	Worktrees []WorktreeEntry `json:"worktrees"`
}

// WorktreeManager coordinates isolated workspace files and directories.
type WorktreeManager struct {
	mu           sync.RWMutex
	worktreesDir string
	indexPath    string
	eventsPath   string
	entries      map[string]*WorktreeEntry
	GitCommand   func(args ...string) ([]byte, error)
}

// NewWorktreeManager creates a new WorktreeManager.
func NewWorktreeManager() *WorktreeManager {
	wd, err := os.Getwd()
	if err != nil {
		wd = "."
	}
	root := findProjectRoot(wd)
	wtDir := filepath.Join(root, ".worktrees")
	_ = os.MkdirAll(wtDir, 0755)

	wm := &WorktreeManager{
		worktreesDir: wtDir,
		indexPath:    filepath.Join(wtDir, "index.json"),
		eventsPath:   filepath.Join(wtDir, "events.jsonl"),
		entries:      make(map[string]*WorktreeEntry),
	}

	// Default git runner
	wm.GitCommand = func(args ...string) ([]byte, error) {
		cmd := exec.Command("git", args...)
		return cmd.CombinedOutput()
	}

	return wm
}

// GlobalWorktreeManager is the singleton worktree manager.
var GlobalWorktreeManager = NewWorktreeManager()

// LoadIndex parses the worktree registry from disk.
func (wm *WorktreeManager) LoadIndex() error {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	data, err := os.ReadFile(wm.indexPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read worktree index: %w", err)
	}

	var cfg WorktreeConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("failed to parse worktree index: %w", err)
	}

	wm.entries = make(map[string]*WorktreeEntry)
	for i := range cfg.Worktrees {
		w := cfg.Worktrees[i]
		wm.entries[w.Name] = &w
	}
	return nil
}

// SaveIndex persists the worktree registry to disk.
func (wm *WorktreeManager) SaveIndex() error {
	var cfg WorktreeConfig
	cfg.Worktrees = make([]WorktreeEntry, 0, len(wm.entries))
	for _, w := range wm.entries {
		cfg.Worktrees = append(cfg.Worktrees, *w)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal worktree index: %w", err)
	}

	if err := os.WriteFile(wm.indexPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write worktree index: %w", err)
	}
	return nil
}

// LogEvent writes a structured log line to the events.jsonl lifecycle stream.
func (wm *WorktreeManager) LogEvent(event, name, taskID, extra string) {
	f, err := os.OpenFile(wm.eventsPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()

	logLine := map[string]any{
		"event":     event,
		"name":      name,
		"task_id":   taskID,
		"extra":     extra,
		"timestamp": time.Now().Format(time.RFC3339),
	}

	if data, err := json.Marshal(logLine); err == nil {
		_, _ = f.Write(append(data, '\n'))
	}
}

// Create allocates a fresh git worktree and binds it to a Task ID.
func (wm *WorktreeManager) Create(name, taskID string) (*WorktreeEntry, error) {
	if name == "" {
		return nil, fmt.Errorf("worktree name is required")
	}

	_ = wm.LoadIndex()

	wm.mu.Lock()
	if existing, ok := wm.entries[name]; ok && existing.Status == "active" {
		wm.mu.Unlock()
		return nil, fmt.Errorf("worktree '%s' is already active", name)
	}
	wm.mu.Unlock()

	wtPath := filepath.Join(wm.worktreesDir, name)
	wm.LogEvent("worktree.create.before", name, taskID, fmt.Sprintf("path: %s", wtPath))

	// Exec git worktree add
	branch := fmt.Sprintf("wt/%s", name)
	_, err := wm.GitCommand("worktree", "add", "-b", branch, wtPath, "HEAD")
	if err != nil {
		// Log failure
		wm.LogEvent("worktree.create.failed", name, taskID, err.Error())
		return nil, fmt.Errorf("failed to create git worktree: %w", err)
	}

	entry := &WorktreeEntry{
		Name:        name,
		Path:        wtPath,
		Branch:      branch,
		TaskID:      taskID,
		Status:      "active",
		LastEntered: time.Now(),
	}

	wm.mu.Lock()
	wm.entries[name] = entry
	_ = wm.SaveIndex()
	wm.mu.Unlock()

	// Update task status on the graph to in_progress
	if taskID != "" {
		if t, err := GlobalTaskManager.GetTask(taskID); err == nil {
			t.Status = "in_progress"
			_ = GlobalTaskManager.SaveTask(t)
		}
	}

	wm.LogEvent("worktree.create.after", name, taskID, "")
	return entry, nil
}

// Closeout closes out an isolated worktree by keeping its path or cleanly removing it.
func (wm *WorktreeManager) Closeout(name string, action string, completeTask bool) error {
	_ = wm.LoadIndex()

	wm.mu.Lock()
	entry, ok := wm.entries[name]
	if !ok {
		wm.mu.Unlock()
		return fmt.Errorf("worktree '%s' not found", name)
	}
	wm.mu.Unlock()

	if action == "keep" {
		wm.mu.Lock()
		entry.Status = "kept"
		_ = wm.SaveIndex()
		wm.mu.Unlock()

		wm.LogEvent("worktree.keep", name, entry.TaskID, "")
		return nil
	} else if action == "remove" {
		wm.LogEvent("worktree.remove.before", name, entry.TaskID, "")

		// Exec git worktree remove
		_, err := wm.GitCommand("worktree", "remove", "--force", entry.Path)
		if err != nil {
			wm.LogEvent("worktree.remove.failed", name, entry.TaskID, err.Error())
			return fmt.Errorf("failed to remove git worktree: %w", err)
		}

		// Also try to delete the branch we created
		_, _ = wm.GitCommand("branch", "-D", entry.Branch)

		wm.mu.Lock()
		entry.Status = "removed"
		_ = wm.SaveIndex()
		wm.mu.Unlock()

		wm.LogEvent("worktree.remove.after", name, entry.TaskID, "")

		// Cascade task completion if requested
		if completeTask && entry.TaskID != "" {
			if t, err := GlobalTaskManager.GetTask(entry.TaskID); err == nil {
				t.Status = "completed"
				_ = GlobalTaskManager.SaveTask(t)
				wm.LogEvent("task.completed", name, entry.TaskID, "")
			}
		}
		return nil
	}

	return fmt.Errorf("invalid closeout action: %s (must be keep or remove)", action)
}

// Enter updates the last entered timestamp for a worktree.
func (wm *WorktreeManager) Enter(name string) error {
	_ = wm.LoadIndex()

	wm.mu.Lock()
	defer wm.mu.Unlock()

	entry, ok := wm.entries[name]
	if !ok {
		return fmt.Errorf("worktree '%s' not found", name)
	}

	entry.LastEntered = time.Now()
	_ = wm.SaveIndex()
	wm.LogEvent("worktree.enter", name, entry.TaskID, "")
	return nil
}

// List returns all registered worktrees.
func (wm *WorktreeManager) List() ([]WorktreeEntry, error) {
	_ = wm.LoadIndex()

	wm.mu.RLock()
	defer wm.mu.RUnlock()

	list := make([]WorktreeEntry, 0, len(wm.entries))
	for _, w := range wm.entries {
		list = append(list, *w)
	}
	return list, nil
}
