package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ScheduledTask represents a stored future prompt intent.
type ScheduledTask struct {
	ID           string    `json:"id"`
	Cron         string    `json:"cron"`
	Prompt       string    `json:"prompt"`
	Recurring    bool      `json:"recurring"`
	Durable      bool      `json:"durable"`
	CreatedAt    time.Time `json:"created_at"`
	LastFiredAt  int64     `json:"last_fired_at,omitempty"`
	JitterOffset int       `json:"jitter_offset,omitempty"`
}

// ScheduledNotification represents a triggered task notification.
type ScheduledNotification struct {
	ScheduleID string `json:"schedule_id"`
	Prompt     string `json:"prompt"`
	MissedAt   string `json:"missed_at,omitempty"`
}


// CronScheduler manages cron jobs and handles time-based triggers.
type CronScheduler struct {
	mu         sync.RWMutex
	tasks      []*ScheduledTask
	notifQueue []ScheduledNotification
	dir        string
	lock       *CronLock
	stopChan   chan struct{}
	wg         sync.WaitGroup
	hasLock    bool
}

// GlobalCronScheduler is the singleton cron scheduler.
var GlobalCronScheduler *CronScheduler

func init() {
	GlobalCronScheduler = NewCronScheduler()
}

func NewCronScheduler() *CronScheduler {
	cwd, _ := os.Getwd()
	dir := filepath.Join(cwd, ".iroha")
	oldDir := filepath.Join(cwd, ".go-claude")
	_ = os.MkdirAll(dir, 0755)

	// Migration logic for cron scheduled_tasks.json
	newTasksPath := filepath.Join(dir, "scheduled_tasks.json")
	oldTasksPath := filepath.Join(oldDir, "scheduled_tasks.json")
	if _, err := os.Stat(newTasksPath); os.IsNotExist(err) {
		if _, oldErr := os.Stat(oldTasksPath); oldErr == nil {
			if data, copyErr := os.ReadFile(oldTasksPath); copyErr == nil {
				_ = os.WriteFile(newTasksPath, data, 0644)
			}
			_ = os.Rename(oldTasksPath, oldTasksPath+".bak")
		}
	}

	lockPath := filepath.Join(dir, "cron.lock")

	return &CronScheduler{
		dir:      dir,
		lock:     NewCronLock(lockPath),
		stopChan: make(chan struct{}),
	}
}

// Start starts the background time check goroutine.
func (cs *CronScheduler) Start() {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	cs.loadDurable()
	cs.hasLock = cs.lock.Acquire()

	cs.stopChan = make(chan struct{})
	cs.wg.Add(1)
	go cs.checkLoop()
}

// Stop stops the background checking goroutine and releases the lock.
func (cs *CronScheduler) Stop() {
	close(cs.stopChan)
	cs.wg.Wait()

	cs.mu.Lock()
	defer cs.mu.Unlock()
	if cs.hasLock {
		cs.lock.Release()
		cs.hasLock = false
	}
}

// Create schedules a new task.
func (cs *CronScheduler) Create(cronExpr, prompt string, recurring, durable bool) (string, error) {
	fields := strings.Fields(cronExpr)
	if len(fields) != 5 {
		return "", fmt.Errorf("invalid cron expression: must have 5 fields")
	}

	cs.mu.Lock()
	defer cs.mu.Unlock()

	taskID := uuid.New().String()[:8]
	jitter := 0
	if recurring {
		jitter = cs.computeJitter(cronExpr)
	}

	task := &ScheduledTask{
		ID:           taskID,
		Cron:         cronExpr,
		Prompt:       prompt,
		Recurring:    recurring,
		Durable:      durable,
		CreatedAt:    time.Now(),
		JitterOffset: jitter,
	}

	cs.tasks = append(cs.tasks, task)

	if durable {
		cs.saveDurable()
	}

	mode := "recurring"
	if !recurring {
		mode = "one-shot"
	}
	store := "session"
	if durable {
		store = "durable"
	}

	return fmt.Sprintf("Created task %s (%s, %s): cron=%s", taskID, mode, store, cronExpr), nil
}

// Delete removes a scheduled task by ID.
func (cs *CronScheduler) Delete(taskID string) (string, error) {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	found := false
	var remaining []*ScheduledTask
	for _, t := range cs.tasks {
		if t.ID == taskID {
			found = true
		} else {
			remaining = append(remaining, t)
		}
	}

	if !found {
		return "", fmt.Errorf("task not found: %s", taskID)
	}

	cs.tasks = remaining
	cs.saveDurable()
	return fmt.Sprintf("Deleted task %s", taskID), nil
}

// ListTasks returns a string representation of all active tasks.
func (cs *CronScheduler) ListTasks() string {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	if len(cs.tasks) == 0 {
		return "No scheduled tasks."
	}

	var lines []string
	now := time.Now()
	for _, t := range cs.tasks {
		mode := "recurring"
		if !t.Recurring {
			mode = "one-shot"
		}
		store := "session"
		if t.Durable {
			store = "durable"
		}
		age := now.Sub(t.CreatedAt).Hours()
		lines = append(lines, fmt.Sprintf("  %s  %s  [%s/%s] (%.1fh old): %s", t.ID, t.Cron, mode, store, age, t.Prompt))
	}
	return strings.Join(lines, "\n")
}

// DrainNotifications flushes and returns all pending triggered job notifications.
func (cs *CronScheduler) DrainNotifications() []ScheduledNotification {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	notifs := cs.notifQueue
	cs.notifQueue = nil
	return notifs
}

// DetectMissedTasks checks durable tasks for executions that should have fired while closed.
func (cs *CronScheduler) DetectMissedTasks() []ScheduledNotification {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	now := time.Now()
	var missed []ScheduledNotification

	for _, t := range cs.tasks {
		if !t.Durable {
			continue
		}

		var lastDt time.Time
		if t.LastFiredAt > 0 {
			lastDt = time.Unix(t.LastFiredAt, 0)
		} else {
			lastDt = t.CreatedAt
		}

		check := lastDt.Add(time.Minute)
		capTime := now
		if now.Sub(lastDt) > 24*time.Hour {
			capTime = lastDt.Add(24 * time.Hour)
		}

		for check.Before(capTime) || check.Equal(capTime) {
			checkTime := check
			if t.JitterOffset > 0 {
				checkTime = check.Add(-time.Duration(t.JitterOffset) * time.Minute)
			}
			if cronMatches(t.Cron, checkTime) {
				missed = append(missed, ScheduledNotification{
					ScheduleID: t.ID,
					Prompt:     t.Prompt,
					MissedAt:   check.Format(time.RFC3339),
				})
				break // one missed trigger is enough to flag it
			}
			check = check.Add(time.Minute)
		}
	}

	return missed
}

func (cs *CronScheduler) computeJitter(cronExpr string) int {
	fields := strings.Fields(cronExpr)
	if len(fields) < 1 {
		return 0
	}
	minuteField := fields[0]
	val, err := strconv.Atoi(minuteField)
	if err == nil {
		if val == 0 || val == 30 {
			// Deterministic jitter based on cronExpr hash
			h := int(hashString(cronExpr))
			if h < 0 {
				h = -h
			}
			return (h % 4) + 1
		}
	}
	return 0
}


func (cs *CronScheduler) checkLoop() {
	defer cs.wg.Done()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	lastCheckedMinute := -1

	for {
		select {
		case <-cs.stopChan:
			return
		case <-ticker.C:
			cs.mu.Lock()
			hasL := cs.hasLock
			if !hasL {
				// Try to acquire lock if we didn't have it (dynamic fallback)
				cs.hasLock = cs.lock.Acquire()
				hasL = cs.hasLock
			}
			cs.mu.Unlock()

			if !hasL {
				continue
			}

			now := time.Now()
			currentMinute := now.Hour()*60 + now.Minute()

			if currentMinute != lastCheckedMinute {
				lastCheckedMinute = currentMinute
				cs.checkTasks(now)
			}
		}
	}
}

func (cs *CronScheduler) checkTasks(now time.Time) {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	var remaining []*ScheduledTask
	var expired []string
	var firedOneshots []string

	for _, t := range cs.tasks {
		// Auto-expiry: recurring tasks older than 7 days
		if t.Recurring && now.Sub(t.CreatedAt) > 7*24*time.Hour {
			expired = append(expired, t.ID)
			continue
		}

		checkTime := now
		if t.JitterOffset > 0 {
			checkTime = now.Add(-time.Duration(t.JitterOffset) * time.Minute)
		}

		if cronMatches(t.Cron, checkTime) {
			cs.notifQueue = append(cs.notifQueue, ScheduledNotification{
				ScheduleID: t.ID,
				Prompt:     t.Prompt,
			})
			t.LastFiredAt = now.Unix()

			if !t.Recurring {
				firedOneshots = append(firedOneshots, t.ID)
			} else {
				remaining = append(remaining, t)
			}
		} else {
			remaining = append(remaining, t)
		}
	}

	cs.tasks = remaining

	// Persist changes if any jobs expired or fired one-shots were removed
	if len(expired) > 0 || len(firedOneshots) > 0 {
		cs.saveDurable()
	}
}

func (cs *CronScheduler) loadDurable() {
	path := filepath.Join(cs.dir, "scheduled_tasks.json")
	if _, err := os.Stat(path); err != nil {
		return
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

	var allTasks []*ScheduledTask
	if err := json.Unmarshal(data, &allTasks); err == nil {
		// Filter only durable tasks
		var durable []*ScheduledTask
		for _, t := range allTasks {
			if t.Durable {
				durable = append(durable, t)
			}
		}
		cs.tasks = durable
	}
}

func (cs *CronScheduler) saveDurable() {
	var durable []*ScheduledTask
	for _, t := range cs.tasks {
		if t.Durable {
			durable = append(durable, t)
		}
	}

	path := filepath.Join(cs.dir, "scheduled_tasks.json")
	data, _ := json.MarshalIndent(durable, "", "  ")
	_ = os.WriteFile(path, data, 0644)
}
