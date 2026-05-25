package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// CrashRecord records a single crash event.
type CrashRecord struct {
	Timestamp time.Time `json:"timestamp"`
	Reason    string    `json:"reason"`
}

// CheckpointData holds serialized state for crash recovery.
type CheckpointData struct {
	AgentName   string          `json:"agent_name"`
	Checkpoint  json.RawMessage `json:"checkpoint"`
	SavedAt     time.Time       `json:"saved_at"`
	LastMsgID   string          `json:"last_msg_id"`
	Processed   int             `json:"processed"`
}

// Watchdog manages a child process teammate with crash tolerance.
type Watchdog struct {
	teammateName      string
	cmd               *exec.Cmd
	crashBudget       int
	crashWindow       time.Duration
	crashes           []CrashRecord
	deadLetterQueue   []IPCMessage
	dlmu              sync.Mutex
	stateFile         string
	heartbeatInterval time.Duration
	binaryPath        string
	args              []string
	mu                sync.Mutex
	processRunning    bool
	cancelMonitor     context.CancelFunc
}

// NewWatchdog creates a new Watchdog for a teammate process.
func NewWatchdog(teammateName string, crashBudget int, crashWindow time.Duration) *Watchdog {
	return &Watchdog{
		teammateName:      teammateName,
		crashBudget:       crashBudget,
		crashWindow:       crashWindow,
		crashes:           make([]CrashRecord, 0),
		deadLetterQueue:   make([]IPCMessage, 0),
		heartbeatInterval: 10 * time.Second,
		stateFile:         ResolveTeammateStateFile(teammateName),
	}
}

// ResolveTeammateStateFile returns the checkpoint path for a teammate.
func ResolveTeammateStateFile(name string) string {
	wd, err := os.Getwd()
	if err != nil {
		wd = "."
	}
	root := findProjectRoot(wd)
	dir := filepath.Join(root, ".iroha", "state", "teammates")
	_ = os.MkdirAll(dir, 0755)
	return filepath.Join(dir, name+".json")
}

// Start spawns the teammate as a child process.
func (w *Watchdog) Start(ctx context.Context, binaryPath string, args []string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.binaryPath = binaryPath
	w.args = args

	return w.spawnLocked(ctx)
}

// spawnLocked starts the child process. Caller must hold w.mu.
func (w *Watchdog) spawnLocked(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, w.binaryPath, w.args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("watchdog: failed to spawn %s: %w", w.teammateName, err)
	}

	w.cmd = cmd
	w.processRunning = true

	LogInfo(CatSubagent, "watchdog_started", fmt.Sprintf("Watchdog spawned teammate process '%s' (pid: %d)", w.teammateName, cmd.Process.Pid), map[string]any{
		"teammate": w.teammateName,
		"pid":      cmd.Process.Pid,
	})

	return nil
}

// Monitor watches the process and restarts it within crash budget.
// This blocks until the context is cancelled or crash budget is exceeded.
func (w *Watchdog) Monitor(ctx context.Context) error {
	monitorCtx, cancel := context.WithCancel(ctx)
	w.cancelMonitor = cancel
	defer cancel()

	for {
		select {
		case <-monitorCtx.Done():
			return nil
		default:
		}

		w.mu.Lock()
		cmd := w.cmd
		w.mu.Unlock()

		if cmd == nil || cmd.Process == nil {
			return fmt.Errorf("watchdog: no process to monitor for %s", w.teammateName)
		}

		// Wait for process to exit
		waitErr := cmd.Wait()

		w.mu.Lock()
		w.processRunning = false
		w.mu.Unlock()

		if monitorCtx.Err() != nil {
			return nil
		}

		reason := "unknown"
		if waitErr != nil {
			reason = waitErr.Error()
		}

		LogWarn(CatSubagent, "watchdog_process_exited", fmt.Sprintf("Teammate '%s' process exited", w.teammateName), map[string]any{
			"teammate": w.teammateName,
			"reason":   reason,
		})

		if !w.RecordCrash(reason) {
			return fmt.Errorf("watchdog: crash budget exceeded for teammate %s", w.teammateName)
		}

		// Restart
		w.mu.Lock()
		if err := w.spawnLocked(monitorCtx); err != nil {
			w.mu.Unlock()
			return fmt.Errorf("watchdog: failed to restart %s: %w", w.teammateName, err)
		}
		w.mu.Unlock()

		LogInfo(CatSubagent, "watchdog_restarted", fmt.Sprintf("Watchdog restarted teammate '%s'", w.teammateName), map[string]any{
			"teammate": w.teammateName,
			"crashes":  len(w.crashes),
		})
	}
}

// Stop terminates the monitored process.
func (w *Watchdog) Stop() {
	if w.cancelMonitor != nil {
		w.cancelMonitor()
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if w.cmd != nil && w.cmd.Process != nil {
		_ = w.cmd.Process.Signal(os.Interrupt)

		// Give it a moment, then kill
		done := make(chan struct{})
		go func() {
			_ = w.cmd.Wait()
			close(done)
		}()

		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = w.cmd.Process.Kill()
		}

		w.processRunning = false
	}
}

// Checkpoint saves agent state to disk before risky operations.
func (w *Watchdog) Checkpoint(state any) error {
	stateBytes, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("watchdog checkpoint marshal: %w", err)
	}

	data := CheckpointData{
		AgentName:  w.teammateName,
		Checkpoint: stateBytes,
		SavedAt:    time.Now(),
	}

	fileBytes, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("watchdog checkpoint encode: %w", err)
	}

	if err := os.WriteFile(w.stateFile, fileBytes, 0644); err != nil {
		return fmt.Errorf("watchdog checkpoint write: %w", err)
	}

	return nil
}

// Recover restores from the last checkpoint after a crash.
func (w *Watchdog) Recover() (*CheckpointData, error) {
	data, err := os.ReadFile(w.stateFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("watchdog recover read: %w", err)
	}

	var cp CheckpointData
	if err := json.Unmarshal(data, &cp); err != nil {
		return nil, fmt.Errorf("watchdog recover parse: %w", err)
	}

	return &cp, nil
}

// RecordCrash records a crash and returns false if the budget is exceeded.
func (w *Watchdog) RecordCrash(reason string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	now := time.Now()
	w.crashes = append(w.crashes, CrashRecord{
		Timestamp: now,
		Reason:    reason,
	})

	// Prune old crash records outside the window
	cutoff := now.Add(-w.crashWindow)
	recent := make([]CrashRecord, 0, len(w.crashes))
	for _, c := range w.crashes {
		if c.Timestamp.After(cutoff) {
			recent = append(recent, c)
		}
	}
	w.crashes = recent

	LogError(CatSubagent, "watchdog_crash_recorded", fmt.Sprintf("Crash recorded for teammate '%s'", w.teammateName), fmt.Errorf("%s", reason), map[string]any{
		"teammate":     w.teammateName,
		"recent_count": len(recent),
		"budget":       w.crashBudget,
	})

	return len(recent) <= w.crashBudget
}

// EnqueueDeadLetter stores a message that couldn't be delivered during a crash.
func (w *Watchdog) EnqueueDeadLetter(msg IPCMessage) {
	w.dlmu.Lock()
	defer w.dlmu.Unlock()
	w.deadLetterQueue = append(w.deadLetterQueue, msg)

	LogWarn(CatSubagent, "watchdog_dead_letter", fmt.Sprintf("Dead letter enqueued for teammate '%s'", w.teammateName), map[string]any{
		"teammate":  w.teammateName,
		"msg_id":    msg.ID,
		"queue_len": len(w.deadLetterQueue),
	})

	// Persist dead letters to disk as backup
	w.persistDeadLettersLocked()
}

// DrainDeadLetters returns and clears the dead letter queue.
func (w *Watchdog) DrainDeadLetters() []IPCMessage {
	w.dlmu.Lock()
	defer w.dlmu.Unlock()

	letters := make([]IPCMessage, len(w.deadLetterQueue))
	copy(letters, w.deadLetterQueue)
	w.deadLetterQueue = w.deadLetterQueue[:0]

	// Clear persisted dead letters
	_ = os.Remove(w.deadLetterPath())

	return letters
}

// IsRunning reports whether the child process is currently alive.
func (w *Watchdog) IsRunning() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.processRunning
}

// deadLetterPath returns the file path for persisted dead letters.
func (w *Watchdog) deadLetterPath() string {
	return w.stateFile + ".deadletters"
}

// persistDeadLettersLocked writes the dead letter queue to disk. Caller must hold dlmu.
func (w *Watchdog) persistDeadLettersLocked() {
	if len(w.deadLetterQueue) == 0 {
		return
	}

	data, err := json.Marshal(w.deadLetterQueue)
	if err != nil {
		return
	}
	_ = os.WriteFile(w.deadLetterPath(), data, 0644)
}

// loadDeadLetters restores dead letters from disk.
func (w *Watchdog) loadDeadLetters() {
	w.dlmu.Lock()
	defer w.dlmu.Unlock()

	data, err := os.ReadFile(w.deadLetterPath())
	if err != nil {
		return
	}

	var letters []IPCMessage
	if err := json.Unmarshal(data, &letters); err != nil {
		return
	}

	w.deadLetterQueue = append(w.deadLetterQueue, letters...)

	LogInfo(CatSubagent, "watchdog_dead_letters_restored", fmt.Sprintf("Restored %d dead letters for teammate '%s'", len(letters), w.teammateName), map[string]any{
		"teammate": w.teammateName,
		"count":    len(letters),
	})
}

