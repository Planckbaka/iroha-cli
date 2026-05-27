package agent

import (
	"os"
	"path/filepath"
	"sync"
)

// ── Atomic Edit Support ─────────────────────────────────────────────────────

// pendingEditSnapshots tracks original file contents before edits for rollback support.
var pendingEditSnapshots struct {
	mu        sync.Mutex
	snapshots map[string]string // absolute path -> original content
}

func init() {
	pendingEditSnapshots.snapshots = make(map[string]string)
}

// rollbackPendingEdits restores all files to their pre-edit state and clears snapshots.
func rollbackPendingEdits() {
	pendingEditSnapshots.mu.Lock()
	defer pendingEditSnapshots.mu.Unlock()

	for path, content := range pendingEditSnapshots.snapshots {
		var err error
		if content == "" {
			// File was newly created by the edit; remove it
			err = os.Remove(path)
		} else {
			err = os.WriteFile(path, []byte(content), 0644)
		}
		if err != nil {
			LogError(CatSystem, "rollback_edit_failed", "Failed to rollback pending edit", err, map[string]any{"path": path})
		}
	}
	pendingEditSnapshots.snapshots = make(map[string]string)
}

// commitPendingEdits clears all snapshots after a successful turn.
func commitPendingEdits() {
	pendingEditSnapshots.mu.Lock()
	defer pendingEditSnapshots.mu.Unlock()
	pendingEditSnapshots.snapshots = make(map[string]string)
}

// findGoModuleRoot walks up from the current directory to find the directory containing go.mod
func findGoModuleRoot() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	dir := cwd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return cwd
}
