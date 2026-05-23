package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// BackgroundTask represents a runtime task running in a background lane.
type BackgroundTask struct {
	ID            string    `json:"id"`
	Status        string    `json:"status"` // running, completed, error, timeout
	Result        string    `json:"result,omitempty"`
	Command       string    `json:"command"`
	StartedAt     time.Time `json:"started_at"`
	FinishedAt    time.Time `json:"finished_at,omitempty"`
	ResultPreview string    `json:"result_preview"`
	OutputFile    string    `json:"output_file"`
}

// BackgroundNotification represents a completion event sent to the queue.
type BackgroundNotification struct {
	TaskID     string `json:"task_id"`
	Status     string `json:"status"`
	Command    string `json:"command"`
	Preview    string `json:"preview"`
	OutputFile string `json:"output_file"`
}

// BackgroundManager coordinates slow-running background lanes.
type BackgroundManager struct {
	mu         sync.RWMutex
	tasks      map[string]*BackgroundTask
	notifQueue []BackgroundNotification
	dir        string
}

// GlobalBackgroundManager is the singleton manager for background execution.
var GlobalBackgroundManager *BackgroundManager

func init() {
	GlobalBackgroundManager = NewBackgroundManager()
}

// NewBackgroundManager creates and initializes a BackgroundManager.
func NewBackgroundManager() *BackgroundManager {
	cwd, _ := os.Getwd()
	dir := filepath.Join(cwd, ".runtime-tasks")
	_ = os.MkdirAll(dir, 0755)

	bm := &BackgroundManager{
		tasks: make(map[string]*BackgroundTask),
		dir:   dir,
	}
	bm.loadPersistedTasks()
	return bm
}

func (bm *BackgroundManager) loadPersistedTasks() {
	files, err := os.ReadDir(bm.dir)
	if err != nil {
		return
	}
	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
			continue
		}
		path := filepath.Join(bm.dir, file.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var t BackgroundTask
		if err := json.Unmarshal(data, &t); err == nil {
			bm.tasks[t.ID] = &t
		}
	}
}

// Run starts a shell command in a background thread and returns the task ID immediately.
func (bm *BackgroundManager) Run(command string) (string, error) {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	taskID := uuid.New().String()[:8]
	outputFile := filepath.Join(".runtime-tasks", fmt.Sprintf("%s.log", taskID))
	task := &BackgroundTask{
		ID:         taskID,
		Status:     "running",
		Command:    command,
		StartedAt:  time.Now(),
		OutputFile: outputFile,
	}
	bm.tasks[taskID] = task
	bm.persistTask(task)

	// Fire background lane execute goroutine
	go bm.execute(taskID, command)

	return fmt.Sprintf("Background task %s started: %s (output_file=%s)", taskID, truncateString(command, 80), outputFile), nil
}

// execute executes the shell command in a background goroutine and captures the result.
func (bm *BackgroundManager) execute(taskID string, command string) {
	cmd := exec.Command("sh", "-c", command)
	var outBuf bytes.Buffer

	logPath := filepath.Join(bm.dir, fmt.Sprintf("%s.log", taskID))
	logFile, fileErr := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	var writer io.Writer
	if fileErr == nil {
		defer func() { _ = logFile.Close() }()
		writer = io.MultiWriter(&outBuf, logFile)
	} else {
		writer = &outBuf
	}
	cmd.Stdout = writer
	cmd.Stderr = writer

	done := make(chan error, 1)
	go func() {
		done <- cmd.Run()
	}()

	var err error
	var status string
	select {
	case err = <-done:
		if err != nil {
			status = "error"
		} else {
			status = "completed"
		}
	case <-time.After(300 * time.Second): // 5-minute timeout
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		err = fmt.Errorf("timeout (300s)")
		status = "timeout"
	}

	output := strings.TrimSpace(outBuf.String())
	if output == "" {
		if err != nil {
			output = fmt.Sprintf("Error: %v", err)
		} else {
			output = "(no output)"
		}
	}

	// Limit captured output length
	if len(output) > 50000 {
		output = output[:50000]
	}

	preview := bm.preview(output, 500)

	// Save detailed log output
	_ = os.WriteFile(logPath, []byte(output), 0644)

	bm.mu.Lock()
	task, ok := bm.tasks[taskID]
	if ok {
		task.Status = status
		task.Result = output
		task.FinishedAt = time.Now()
		task.ResultPreview = preview
		bm.persistTask(task)

		// Push completion notification
		bm.notifQueue = append(bm.notifQueue, BackgroundNotification{
			TaskID:     taskID,
			Status:     status,
			Command:    truncateString(command, 80),
			Preview:    preview,
			OutputFile: task.OutputFile,
		})
	}
	bm.mu.Unlock()
}

// Check inspects the status of background tasks.
func (bm *BackgroundManager) Check(taskID string) (string, error) {
	bm.mu.RLock()
	defer bm.mu.RUnlock()

	if taskID != "" {
		t, ok := bm.tasks[taskID]
		if !ok {
			return "", fmt.Errorf("unknown task: %s", taskID)
		}
		visible := map[string]any{
			"id":             t.ID,
			"status":         t.Status,
			"command":        t.Command,
			"result_preview": t.ResultPreview,
			"output_file":    t.OutputFile,
		}
		data, _ := json.MarshalIndent(visible, "", "  ")
		return string(data), nil
	}

	var lines []string
	for tid, t := range bm.tasks {
		preview := t.ResultPreview
		if t.Status == "running" {
			preview = "(running)"
		}
		lines = append(lines, fmt.Sprintf("%s: [%s] %s -> %s", tid, t.Status, truncateString(t.Command, 60), preview))
	}
	if len(lines) == 0 {
		return "No background tasks.", nil
	}
	return strings.Join(lines, "\n"), nil
}

// DrainNotifications retrieves and flushes all pending background completion notifications.
func (bm *BackgroundManager) DrainNotifications() []BackgroundNotification {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	notifs := bm.notifQueue
	bm.notifQueue = nil
	return notifs
}

// DetectStalled queries for task IDs running longer than the specified threshold.
func (bm *BackgroundManager) DetectStalled(threshold time.Duration) []string {
	bm.mu.RLock()
	defer bm.mu.RUnlock()

	var stalled []string
	now := time.Now()
	for tid, t := range bm.tasks {
		if t.Status != "running" {
			continue
		}
		if now.Sub(t.StartedAt) > threshold {
			stalled = append(stalled, tid)
		}
	}
	return stalled
}

func (bm *BackgroundManager) preview(output string, limit int) string {
	compact := strings.Join(strings.Fields(output), " ")
	if len(compact) > limit {
		return compact[:limit]
	}
	return compact
}

func (bm *BackgroundManager) persistTask(t *BackgroundTask) {
	path := filepath.Join(bm.dir, fmt.Sprintf("%s.json", t.ID))
	data, _ := json.MarshalIndent(t, "", "  ")
	_ = os.WriteFile(path, data, 0644)
}

func truncateString(s string, limit int) string {
	if len(s) > limit {
		return s[:limit] + "..."
	}
	return s
}
