package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorktreeManager_Lifecycle(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "go-claude-worktree-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Set up directories
	wtDir := filepath.Join(tempDir, ".worktrees")
	_ = os.MkdirAll(wtDir, 0755)

	wm := &WorktreeManager{
		worktreesDir: wtDir,
		indexPath:    filepath.Join(wtDir, "index.json"),
		eventsPath:   filepath.Join(wtDir, "events.jsonl"),
		entries:      make(map[string]*WorktreeEntry),
	}

	// Mock Git command execution
	var gitCalls [][]string
	wm.GitCommand = func(args ...string) ([]byte, error) {
		gitCalls = append(gitCalls, args)
		return []byte("mock git success"), nil
	}

	// Override task manager to use tempDir for task records
	originalTasksDir := GlobalTaskManager.tasksDir
	GlobalTaskManager.tasksDir = filepath.Join(tempDir, ".tasks")
	_ = os.MkdirAll(GlobalTaskManager.tasksDir, 0755)
	defer func() { GlobalTaskManager.tasksDir = originalTasksDir }()

	// Pre-create a task record
	task := &TaskRecord{
		ID:      "t1",
		Subject: "Build login page",
		Status:  "pending",
		Owner:   "agent",
	}
	if err := GlobalTaskManager.SaveTask(task); err != nil {
		t.Fatalf("failed to save task t1: %v", err)
	}

	// 1. Create worktree
	entry, err := wm.Create("login-wt", "t1")
	if err != nil {
		t.Fatalf("failed to create worktree: %v", err)
	}

	if entry.Name != "login-wt" || entry.TaskID != "t1" || entry.Status != "active" {
		t.Errorf("unexpected worktree entry: %+v", entry)
	}

	// Verify task status got promoted to in_progress
	t1Refreshed, _ := GlobalTaskManager.GetTask("t1")
	if t1Refreshed.Status != "in_progress" {
		t.Errorf("expected task t1 status to be promoted to 'in_progress', got: %s", t1Refreshed.Status)
	}

	// Verify Git was called with correct parameters
	if len(gitCalls) != 1 {
		t.Fatalf("expected 1 git call, got: %d", len(gitCalls))
	}
	call := gitCalls[0]
	if len(call) < 6 || call[0] != "worktree" || call[1] != "add" || call[2] != "-b" || call[3] != "wt/login-wt" {
		t.Errorf("unexpected git call params: %v", call)
	}

	// Verify events log has been written
	data, err := os.ReadFile(wm.eventsPath)
	if err != nil {
		t.Fatalf("failed to read events path: %v", err)
	}
	events := strings.TrimSpace(string(data))
	if !strings.Contains(events, "worktree.create.before") || !strings.Contains(events, "worktree.create.after") {
		t.Errorf("expected events log to record creation, got:\n%s", events)
	}

	// 2. Enter worktree
	if err := wm.Enter("login-wt"); err != nil {
		t.Fatalf("failed to enter worktree: %v", err)
	}

	// Verify list works
	list, err := wm.List()
	if err != nil || len(list) != 1 || list[0].Name != "login-wt" {
		t.Errorf("unexpected worktree list: %+v", list)
	}

	// Reset git calls
	gitCalls = nil

	// 3. Closeout worktree - keep
	if err := wm.Closeout("login-wt", "keep", false); err != nil {
		t.Fatalf("failed to closeout worktree with 'keep': %v", err)
	}
	entryRefreshed := wm.entries["login-wt"]
	if entryRefreshed.Status != "kept" {
		t.Errorf("expected status 'kept', got: %s", entryRefreshed.Status)
	}

	// 4. Closeout worktree - remove and complete task
	if err := wm.Closeout("login-wt", "remove", true); err != nil {
		t.Fatalf("failed to closeout worktree with 'remove': %v", err)
	}

	entryRefreshed2 := wm.entries["login-wt"]
	if entryRefreshed2.Status != "removed" {
		t.Errorf("expected status 'removed', got: %s", entryRefreshed2.Status)
	}

	// Verify task t1 got completed
	t1Completed, _ := GlobalTaskManager.GetTask("t1")
	if t1Completed.Status != "completed" {
		t.Errorf("expected task t1 status to be 'completed', got: %s", t1Completed.Status)
	}

	// Verify git remove was called
	var foundRemove bool
	for _, call := range gitCalls {
		if len(call) >= 3 && call[0] == "worktree" && call[1] == "remove" {
			foundRemove = true
			break
		}
	}
	if !foundRemove {
		t.Errorf("expected git worktree remove to be called, got calls: %v", gitCalls)
	}

	// Check final event log
	finalData, _ := os.ReadFile(wm.eventsPath)
	finalEvents := string(finalData)
	if !strings.Contains(finalEvents, "worktree.remove.after") || !strings.Contains(finalEvents, "task.completed") {
		t.Errorf("expected events log to contain removal and task completion, got:\n%s", finalEvents)
	}
}
