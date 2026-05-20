package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// TaskRecord represents a task node in the Directed Acyclic Graph (DAG).
type TaskRecord struct {
	ID          string   `json:"id"`
	Subject     string   `json:"subject"`
	Description string   `json:"description,omitempty"`
	Status      string   `json:"status"` // "pending", "in_progress", "completed", "deleted"
	BlockedBy   []string `json:"blockedBy"` // prerequisite task IDs
	Blocks      []string `json:"blocks"`    // dependent task IDs
	Owner       string   `json:"owner"`     // "agent" or "user"
}

// TaskManager manages durable work graph persisted on disk.
type TaskManager struct {
	mu       sync.RWMutex
	tasksDir string
}

// NewTaskManager creates a TaskManager with resolved directory.
func NewTaskManager() *TaskManager {
	return &TaskManager{
		tasksDir: ResolveTasksDir(),
	}
}

// GlobalTaskManager is the singleton task manager.
var GlobalTaskManager = NewTaskManager()

// ResolveTasksDir finds the appropriate directory for tasks.
func ResolveTasksDir() string {
	wd, err := os.Getwd()
	if err != nil {
		wd = "."
	}
	root := findProjectRoot(wd)

	// Check local project root .tasks directory
	localTasksDir := filepath.Join(root, ".tasks")
	if err := os.MkdirAll(localTasksDir, 0755); err == nil {
		testFile := filepath.Join(localTasksDir, ".write_test")
		if err := os.WriteFile(testFile, []byte("test"), 0644); err == nil {
			_ = os.Remove(testFile)
			return localTasksDir
		}
	}

	// Fallback to user-global tasks directory
	home, err := os.UserHomeDir()
	if err == nil {
		globalTasksDir := filepath.Join(home, ".go-claude", "tasks")
		_ = os.MkdirAll(globalTasksDir, 0755)
		return globalTasksDir
	}

	return "./.tasks"
}

// SaveTask validates and saves a task record, performing edge reconciliation and cycle check.
func (tm *TaskManager) SaveTask(task *TaskRecord) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	tasks, err := tm.loadAllTasksRaw()
	if err != nil {
		return err
	}

	// Normalize task
	task.ID = strings.TrimSpace(task.ID)
	if task.ID == "" {
		return fmt.Errorf("task ID is required")
	}
	task.Subject = strings.TrimSpace(task.Subject)
	if task.Subject == "" {
		return fmt.Errorf("task subject is required")
	}
	task.Status = strings.ToLower(strings.TrimSpace(task.Status))
	if task.Status == "" {
		task.Status = "pending"
	}
	if task.Status != "pending" && task.Status != "in_progress" && task.Status != "completed" && task.Status != "deleted" {
		return fmt.Errorf("invalid task status: %s", task.Status)
	}
	if task.Owner == "" {
		task.Owner = "agent"
	}

	// Save original status in case we need to roll back
	var oldStatus string
	if existing, ok := tasks[task.ID]; ok {
		oldStatus = existing.Status
	}

	// Put/update in map
	tasks[task.ID] = task

	// Run bidirectional edge reconciliation and auto-unblocking
	ReconcileEdges(tasks)

	// Run cycle validation
	if cycle := CheckCycles(tasks); cycle != nil {
		// Roll back the change in memory to prevent corruption of subsequent calls
		if oldStatus == "" {
			delete(tasks, task.ID)
		} else {
			tasks[task.ID].Status = oldStatus
		}
		ReconcileEdges(tasks)
		return fmt.Errorf("dependency cycle detected: %s", strings.Join(cycle, " -> "))
	}

	// Ensure the tasks directory exists
	if err := os.MkdirAll(tm.tasksDir, 0755); err != nil {
		return fmt.Errorf("failed to create tasks directory: %w", err)
	}

	// Write all tasks to disk to persist bidirectional updates and unblocking cascade
	for _, t := range tasks {
		path := filepath.Join(tm.tasksDir, t.ID+".json")
		data, err := json.MarshalIndent(t, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal task %s: %w", t.ID, err)
		}
		if err := os.WriteFile(path, data, 0644); err != nil {
			return fmt.Errorf("failed to write task %s to disk: %w", t.ID, err)
		}
	}

	return nil
}

// GetTask retrieves a task record by ID.
func (tm *TaskManager) GetTask(id string) (*TaskRecord, error) {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	id = strings.TrimSpace(id)
	path := filepath.Join(tm.tasksDir, id+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("task not found: %s", id)
	}
	var task TaskRecord
	if err := json.Unmarshal(data, &task); err != nil {
		return nil, fmt.Errorf("failed to parse task %s: %w", id, err)
	}
	return &task, nil
}

// ListTasks lists all active tasks (excluding deleted).
func (tm *TaskManager) ListTasks() ([]*TaskRecord, error) {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	tasksMap, err := tm.loadAllTasksRaw()
	if err != nil {
		return nil, err
	}

	var list []*TaskRecord
	for _, t := range tasksMap {
		if t.Status != "deleted" {
			list = append(list, t)
		}
	}

	// Sort alphabetically by ID
	sort.Slice(list, func(i, j int) bool {
		return list[i].ID < list[j].ID
	})
	return list, nil
}

// loadAllTasksRaw reads all JSON tasks from disk.
func (tm *TaskManager) loadAllTasksRaw() (map[string]*TaskRecord, error) {
	tasks := make(map[string]*TaskRecord)
	entries, err := os.ReadDir(tm.tasksDir)
	if err != nil {
		if os.IsNotExist(err) {
			return tasks, nil
		}
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			path := filepath.Join(tm.tasksDir, entry.Name())
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			var task TaskRecord
			if err := json.Unmarshal(data, &task); err == nil {
				tasks[task.ID] = &task
			}
		}
	}
	return tasks, nil
}

// ReconcileEdges synchronizes bidirectional graph relations and resolves task cascades.
func ReconcileEdges(tasks map[string]*TaskRecord) {
	// 0. Automatically create placeholder records for any referenced but missing tasks
	referenced := make(map[string]bool)
	for _, t := range tasks {
		for _, blocked := range t.Blocks {
			blocked = strings.TrimSpace(blocked)
			if blocked != "" && tasks[blocked] == nil {
				referenced[blocked] = true
			}
		}
		for _, blocker := range t.BlockedBy {
			blocker = strings.TrimSpace(blocker)
			if blocker != "" && tasks[blocker] == nil {
				referenced[blocker] = true
			}
		}
	}
	for id := range referenced {
		tasks[id] = &TaskRecord{
			ID:      id,
			Subject: "Auto-created dependency: " + id,
			Status:  "pending",
			Owner:   "agent",
		}
	}

	// 1. Gather all active directed edges (From -> To), where From blocks To.
	// We ignore edges from tasks that are completed or deleted.
	edges := make(map[string]map[string]bool)
	getOrInit := func(from string) map[string]bool {
		if edges[from] == nil {
			edges[from] = make(map[string]bool)
		}
		return edges[from]
	}

	for _, t := range tasks {
		if t.Status == "completed" || t.Status == "deleted" {
			continue
		}
		for _, blocked := range t.Blocks {
			blocked = strings.TrimSpace(blocked)
			if blocked != "" && tasks[blocked] != nil {
				getOrInit(t.ID)[blocked] = true
			}
		}
		for _, blocker := range t.BlockedBy {
			blocker = strings.TrimSpace(blocker)
			if blocker != "" && tasks[blocker] != nil {
				blockerTask := tasks[blocker]
				if blockerTask.Status != "completed" && blockerTask.Status != "deleted" {
					getOrInit(blocker)[t.ID] = true
				}
			}
		}
	}

	// 2. Rebuild BlockedBy and Blocks lists for each task based on active edges
	for _, t := range tasks {
		t.Blocks = []string{}
		t.BlockedBy = []string{}
	}

	for from, targets := range edges {
		fromTask := tasks[from]
		if fromTask == nil {
			continue
		}
		for to := range targets {
			toTask := tasks[to]
			if toTask == nil {
				continue
			}
			// Add edge: fromTask blocks toTask (toTask is blocked by fromTask)
			fromTask.Blocks = append(fromTask.Blocks, to)
			toTask.BlockedBy = append(toTask.BlockedBy, from)
		}
	}

	// 3. Keep lists clean and sorted
	for _, t := range tasks {
		t.Blocks = removeDuplicatesAndSort(t.Blocks)
		t.BlockedBy = removeDuplicatesAndSort(t.BlockedBy)
	}
}

// CheckCycles runs DFS with 3-color algorithm to detect cycles in dependency graph.
// It returns the cycle path as a slice if found, e.g. ["t1", "t2", "t1"].
func CheckCycles(tasks map[string]*TaskRecord) []string {
	// visited map tracking states: 1 = visiting, 2 = visited
	visited := make(map[string]int)
	var path []string

	var dfs func(id string) bool
	dfs = func(id string) bool {
		visited[id] = 1
		path = append(path, id)

		task := tasks[id]
		if task != nil {
			for _, depID := range task.BlockedBy {
				if visited[depID] == 1 {
					// Cycle found! Extract the cycle path
					cycleStartIdx := -1
					for i, pID := range path {
						if pID == depID {
							cycleStartIdx = i
							break
						}
					}
					if cycleStartIdx != -1 {
						cyclePath := append([]string{}, path[cycleStartIdx:]...)
						cyclePath = append(cyclePath, depID)
						path = cyclePath
						return true
					}
					path = append(path, depID)
					return true
				} else if visited[depID] == 0 {
					if dfs(depID) {
						return true
					}
				}
			}
		}

		path = path[:len(path)-1]
		visited[id] = 2
		return false
	}

	for id := range tasks {
		if visited[id] == 0 {
			if dfs(id) {
				return path
			}
		}
	}
	return nil
}

func removeDuplicatesAndSort(slice []string) []string {
	keys := make(map[string]bool)
	var list []string
	for _, entry := range slice {
		entry = strings.TrimSpace(entry)
		if entry != "" && !keys[entry] {
			keys[entry] = true
			list = append(list, entry)
		}
	}
	sort.Strings(list)
	return list
}
