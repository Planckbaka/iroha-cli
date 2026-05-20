package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
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

// CronLock prevents multiple concurrent sessions from double-firing cron jobs.
type CronLock struct {
	lockPath string
}

func NewCronLock(lockPath string) *CronLock {
	return &CronLock{lockPath: lockPath}
}

// Acquire tries to acquire the cron lock. Returns true on success.
func (cl *CronLock) Acquire() bool {
	if _, err := os.Stat(cl.lockPath); err == nil {
		// Lock file exists, read PID
		data, err := os.ReadFile(cl.lockPath)
		if err == nil {
			storedPid, err := strconv.Atoi(strings.TrimSpace(string(data)))
			if err == nil {
				if isPIDAlive(storedPid) {
					// Owner is alive -- lock held
					return false
				}
			}
		}
		// Stale lock -- remove it
		_ = os.Remove(cl.lockPath)
	}

	_ = os.MkdirAll(filepath.Dir(cl.lockPath), 0755)
	err := os.WriteFile(cl.lockPath, []byte(strconv.Itoa(os.Getpid())), 0644)
	return err == nil
}

// Release deletes the lock file if it belongs to this process.
func (cl *CronLock) Release() {
	if _, err := os.Stat(cl.lockPath); err == nil {
		data, err := os.ReadFile(cl.lockPath)
		if err == nil {
			storedPid, err := strconv.Atoi(strings.TrimSpace(string(data)))
			if err == nil && storedPid == os.Getpid() {
				_ = os.Remove(cl.lockPath)
			}
		}
	}
}

func isPIDAlive(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = process.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	if err == syscall.ESRCH {
		return false
	}
	if err == os.ErrProcessDone {
		return false
	}
	// Other errors (like permission denied) imply the process is alive but we can't signal it
	return true
}

// cronMatches checks if the 5-field cron expression matches a given time.
func cronMatches(expr string, dt time.Time) bool {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return false
	}

	values := []int{dt.Minute(), dt.Hour(), dt.Day(), int(dt.Month()), int(dt.Weekday())}
	ranges := [][]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 6}}

	for i := 0; i < 5; i++ {
		if !fieldMatches(fields[i], values[i], ranges[i][0], ranges[i][1]) {
			return false
		}
	}
	return true
}

func fieldMatches(field string, value int, lo, hi int) bool {
	if field == "*" {
		return true
	}

	parts := strings.Split(field, ",")
	for _, part := range parts {
		step := 1
		if strings.Contains(part, "/") {
			sp := strings.Split(part, "/")
			part = sp[0]
			step, _ = strconv.Atoi(sp[1])
			if step <= 0 {
				step = 1
			}
		}

		if part == "*" {
			if (value-lo)%step == 0 {
				return true
			}
		} else if strings.Contains(part, "-") {
			sp := strings.Split(part, "-")
			if len(sp) == 2 {
				start, _ := strconv.Atoi(sp[0])
				end, _ := strconv.Atoi(sp[1])
				if value >= start && value <= end && (value-start)%step == 0 {
					return true
				}
			}
		} else {
			val, err := strconv.Atoi(part)
			if err == nil {
				// Special case: Sunday in cron dow field can be 0 or 7.
				if lo == 0 && hi == 6 && value == 0 && val == 7 {
					return true
				}
				if val == value {
					return true
				}
			}
		}
	}

	return false
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
	dir := filepath.Join(cwd, ".go-claude")
	_ = os.MkdirAll(dir, 0755)

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
		if !t.Durable || t.LastFiredAt == 0 {
			continue
		}

		lastDt := time.Unix(t.LastFiredAt, 0)
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

func hashString(s string) int32 {
	var h int32
	for i := 0; i < len(s); i++ {
		h = 31*h + int32(s[i])
	}
	return h
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
