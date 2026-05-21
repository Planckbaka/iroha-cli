package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBackgroundManager_Basic(t *testing.T) {
	bm := NewBackgroundManager()
	defer os.RemoveAll(bm.dir)

	// 1. Run a simple echo command
	msg, err := bm.Run(`echo "hello background lane"`)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if !strings.Contains(msg, "started") {
		t.Errorf("expected msg to mention started, got %s", msg)
	}

	// 2. Wait up to 2 seconds for execution
	var notifs []BackgroundNotification
	for i := 0; i < 20; i++ {
		time.Sleep(100 * time.Millisecond)
		notifs = bm.DrainNotifications()
		if len(notifs) > 0 {
			break
		}
	}

	if len(notifs) == 0 {
		t.Fatalf("expected to receive completion notification")
	}

	notif := notifs[0]
	if notif.Status != "completed" {
		t.Errorf("expected status completed, got %s", notif.Status)
	}

	if !strings.Contains(notif.Preview, "hello background lane") {
		t.Errorf("expected preview to contain command output, got %s", notif.Preview)
	}

	// 3. Verify files exist in .runtime-tasks
	jsonPath := filepath.Join(bm.dir, notif.TaskID+".json")
	logPath := filepath.Join(bm.dir, notif.TaskID+".log")

	if _, err := os.Stat(jsonPath); err != nil {
		t.Errorf("expected json record to exist: %v", err)
	}
	if _, err := os.Stat(logPath); err != nil {
		t.Errorf("expected log file to exist: %v", err)
	}

	// 4. Test Check specific and list all
	details, err := bm.Check(notif.TaskID)
	if err != nil {
		t.Fatalf("Check failed: %v", err)
	}
	if !strings.Contains(details, notif.TaskID) {
		t.Errorf("expected details to contain task ID, got %s", details)
	}

	list, err := bm.Check("")
	if err != nil {
		t.Fatalf("Check all failed: %v", err)
	}
	if !strings.Contains(list, notif.TaskID) {
		t.Errorf("expected list to contain task ID, got %s", list)
	}
}

func TestBackgroundManager_TimeoutAndError(t *testing.T) {
	bm := NewBackgroundManager()
	defer os.RemoveAll(bm.dir)

	// 1. Run a command that fails
	_, err := bm.Run("false")
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	var notifs []BackgroundNotification
	for i := 0; i < 20; i++ {
		time.Sleep(100 * time.Millisecond)
		notifs = bm.DrainNotifications()
		if len(notifs) > 0 {
			break
		}
	}

	if len(notifs) == 0 {
		t.Fatalf("expected to receive completion notification")
	}

	if notifs[0].Status != "error" {
		t.Errorf("expected status error, got %s", notifs[0].Status)
	}
}

func TestBackgroundManager_PersistedLoading(t *testing.T) {
	// Create temporary directory for isolated testing
	tmpDir, err := os.MkdirTemp("", "runtime-tasks-test-*")
	if err != nil {
		t.Fatalf("failed to create tmp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	bm1 := &BackgroundManager{
		tasks: make(map[string]*BackgroundTask),
		dir:   tmpDir,
	}

	// 1. Run a simple command
	msg, err := bm1.Run(`echo "hello reload lane"`)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	_ = msg

	// Wait for completion
	var notifs []BackgroundNotification
	for i := 0; i < 20; i++ {
		time.Sleep(100 * time.Millisecond)
		notifs = bm1.DrainNotifications()
		if len(notifs) > 0 {
			break
		}
	}

	if len(notifs) == 0 {
		t.Fatalf("expected to receive completion notification")
	}

	taskID := notifs[0].TaskID

	// 2. Re-instantiate a new background manager using the same directory
	bm2 := &BackgroundManager{
		tasks: make(map[string]*BackgroundTask),
		dir:   tmpDir,
	}
	bm2.loadPersistedTasks()

	// 3. Verify it loaded the task
	details, err := bm2.Check(taskID)
	if err != nil {
		t.Fatalf("Check failed on reloaded manager: %v", err)
	}

	if !strings.Contains(details, taskID) {
		t.Errorf("expected reloaded details to contain task ID %s, got %s", taskID, details)
	}
	if !strings.Contains(details, "completed") {
		t.Errorf("expected reloaded task to be completed, got %s", details)
	}
}
