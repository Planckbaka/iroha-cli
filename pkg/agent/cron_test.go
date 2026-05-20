package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCronMatches(t *testing.T) {
	// 2026-05-20T19:30:00 (Wednesday, Weekday=3)
	// Month=5, Day=20
	dt := time.Date(2026, 5, 20, 19, 30, 0, 0, time.UTC)

	tests := []struct {
		expr  string
		want  bool
		descr string
	}{
		{"30 19 20 5 3", true, "exact match"},
		{"* * * * *", true, "all wildcards"},
		{"*/5 * * * *", true, "step match"},
		{"*/7 * * * *", false, "step non-match"},
		{"20-40 19 20 5 *", true, "range match"},
		{"10,30,50 19 * * *", true, "list match"},
		{"30 19 20 5 4", false, "weekday mismatch"},
		{"30 19 20 6 *", false, "month mismatch"},
		{"* * * * * *", false, "too many fields"},
	}

	for _, tc := range tests {
		got := cronMatches(tc.expr, dt)
		if got != tc.want {
			t.Errorf("%s: cronMatches(%q, %v) = %v; want %v", tc.descr, tc.expr, dt, got, tc.want)
		}
	}
}

func TestCronLock(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cron-lock-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	lockPath := filepath.Join(tempDir, "cron.lock")
	lock1 := NewCronLock(lockPath)
	lock2 := NewCronLock(lockPath)

	// 1. First lock acquisition should succeed
	if !lock1.Acquire() {
		t.Error("lock1 acquire failed")
	}

	// 2. Second lock acquisition should fail because lock1 is alive
	if lock2.Acquire() {
		t.Error("lock2 acquired active lock incorrectly")
	}

	// 3. Releasing lock1
	lock1.Release()

	// 4. Second lock acquisition should now succeed
	if !lock2.Acquire() {
		t.Error("lock2 acquire failed after release")
	}

	lock2.Release()
}

func TestCronSchedulerLifecycle(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cron-sched-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	sched := &CronScheduler{
		dir:      tempDir,
		lock:     NewCronLock(filepath.Join(tempDir, "cron.lock")),
		stopChan: make(chan struct{}),
	}

	// Create scheduled jobs
	id1, err := sched.Create("*/2 * * * *", "Run tests", true, true)
	if err != nil {
		t.Fatalf("failed to create job 1: %v", err)
	}
	if id1 == "" {
		t.Error("expected non-empty task ID")
	}

	_, err = sched.Create("0 9 * * 1", "Weekly sync", false, false)
	if err != nil {
		t.Fatalf("failed to create job 2: %v", err)
	}

	// Verify they are registered
	sched.mu.RLock()
	count := len(sched.tasks)
	sched.mu.RUnlock()
	if count != 2 {
		t.Errorf("expected 2 tasks, got %d", count)
	}

	// Delete job
	taskID := sched.tasks[0].ID
	_, err = sched.Delete(taskID)
	if err != nil {
		t.Errorf("failed to delete task %s: %v", taskID, err)
	}

	sched.mu.RLock()
	count = len(sched.tasks)
	sched.mu.RUnlock()
	if count != 1 {
		t.Errorf("expected 1 task after deletion, got %d", count)
	}
}

func TestCronSchedulerMissedTasks(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cron-missed-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	sched := &CronScheduler{
		dir:      tempDir,
		lock:     NewCronLock(filepath.Join(tempDir, "cron.lock")),
		stopChan: make(chan struct{}),
	}

	// Use real time to prevent timezone mismatch with time.Now() inside DetectMissedTasks
	now := time.Now()
	// Last fired was 10 minutes ago
	lastFired := now.Add(-10 * time.Minute)

	task := &ScheduledTask{
		ID:          "missed1",
		Cron:        "*/5 * * * *", // should fire every 5 minutes (e.g. 19:25 and 19:30)
		Prompt:      "Missed job",
		Recurring:   true,
		Durable:     true,
		CreatedAt:   now.Add(-1 * time.Hour),
		LastFiredAt: lastFired.Unix(),
	}
	sched.tasks = append(sched.tasks, task)

	// Since we are checking now, the scheduler should walk from 19:21 to 19:30.
	// It should detect that 19:25 is a match, and flag the task as missed.
	missed := sched.DetectMissedTasks()

	if len(missed) != 1 {
		t.Errorf("expected 1 missed notification, got %d", len(missed))
	} else {
		if missed[0].ScheduleID != "missed1" {
			t.Errorf("expected schedule ID 'missed1', got %s", missed[0].ScheduleID)
		}
	}
}
