package agent

import (
	"os"
	"testing"
	"time"
)

func TestAutonomous_StateMachine(t *testing.T) {
	am := &AutonomousManager{
		state: StateIdle,
	}

	if am.GetState() != StateIdle {
		t.Errorf("expected initial state to be IDLE, got %s", am.GetState())
	}

	am.SetState(StateWork)
	if am.GetState() != StateWork {
		t.Errorf("expected transitioned state to be WORK, got %s", am.GetState())
	}
}

func TestAutonomous_AutoClaim(t *testing.T) {
	// Create a temp directory for tasks
	tempDir, err := os.MkdirTemp("", "go-claude-autonomy-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Override task manager to use tempDir
	originalTasksDir := GlobalTaskManager.tasksDir
	GlobalTaskManager.tasksDir = tempDir
	defer func() { GlobalTaskManager.tasksDir = originalTasksDir }()

	// 1. Create t2 (blocker task)
	t2 := &TaskRecord{
		ID:      "t2",
		Subject: "Write database migrations",
		Status:  "pending",
		Owner:   "agent",
	}
	if err := GlobalTaskManager.SaveTask(t2); err != nil {
		t.Fatalf("failed to save t2: %v", err)
	}

	// 2. Create t1 (blocked task)
	t1 := &TaskRecord{
		ID:        "t1",
		Subject:   "Refactor database queries",
		Status:    "pending",
		BlockedBy: []string{"t2"},
		Owner:     "agent",
	}
	if err := GlobalTaskManager.SaveTask(t1); err != nil {
		t.Fatalf("failed to save t1: %v", err)
	}

	// Double-check graph edges
	t1Refreshed, _ := GlobalTaskManager.GetTask("t1")
	if len(t1Refreshed.BlockedBy) != 1 || t1Refreshed.BlockedBy[0] != "t2" {
		t.Fatalf("t1 BlockedBy is incorrect: %+v", t1Refreshed.BlockedBy)
	}

	am := &AutonomousManager{
		state: StateIdle,
	}

	// Claim with keyword "queries" — should NOT claim t1 because it is blocked by t2 (which is pending)
	claimed, err := am.AutoClaimTasks("specialist-alice", []string{"queries"})
	if err != nil {
		t.Fatalf("AutoClaimTasks failed: %v", err)
	}
	if len(claimed) != 0 {
		t.Errorf("expected 0 tasks claimed (t1 is blocked), got: %v", claimed)
	}

	// Claim with keyword "migrations" — should claim t2 since it has no blockers
	claimed2, err := am.AutoClaimTasks("specialist-alice", []string{"migrations"})
	if err != nil {
		t.Fatalf("AutoClaimTasks failed: %v", err)
	}
	if len(claimed2) != 1 || claimed2[0] != "t2" {
		t.Errorf("expected only t2 to be claimed, got: %v", claimed2)
	}

	// Check that t2 status is now in_progress and owner is specialist-alice
	t2Refreshed, err := GlobalTaskManager.GetTask("t2")
	if err != nil {
		t.Fatalf("failed to get t2: %v", err)
	}
	if t2Refreshed.Status != "in_progress" || t2Refreshed.Owner != "specialist-alice" {
		t.Errorf("claimed task attributes are incorrect: %+v", t2Refreshed)
	}

	// Mark t2 as completed to unblock t1
	t2Refreshed.Status = "completed"
	if err := GlobalTaskManager.SaveTask(t2Refreshed); err != nil {
		t.Fatalf("failed to complete t2: %v", err)
	}

	// Claim with keyword "queries" now — t1 is unblocked, so it should be claimed!
	claimed3, err := am.AutoClaimTasks("specialist-alice", []string{"queries"})
	if err != nil {
		t.Fatalf("AutoClaimTasks failed: %v", err)
	}
	if len(claimed3) != 1 || claimed3[0] != "t1" {
		t.Errorf("expected t1 to be claimed, got: %v", claimed3)
	}

	t1Refreshed, err = GlobalTaskManager.GetTask("t1")
	if err != nil {
		t.Fatalf("failed to get t1: %v", err)
	}
	if t1Refreshed.Status != "in_progress" || t1Refreshed.Owner != "specialist-alice" {
		t.Errorf("claimed task attributes are incorrect: %+v", t1Refreshed)
	}
}

func TestAutonomous_BackgroundLoop(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "go-claude-autonomy-loop-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	originalTasksDir := GlobalTaskManager.tasksDir
	GlobalTaskManager.tasksDir = tempDir
	defer func() { GlobalTaskManager.tasksDir = originalTasksDir }()

	t1 := &TaskRecord{
		ID:      "t1",
		Subject: "Fix bug in auth system",
		Status:  "pending",
		Owner:   "agent",
	}
	_ = GlobalTaskManager.SaveTask(t1)

	am := &AutonomousManager{
		state: StateIdle,
	}

	// Start background polling every 50ms
	am.StartAutoPolling("specialist-auth", []string{"auth"}, 50*time.Millisecond)
	defer am.StopAutoPolling()

	// Wait up to 1 second for background routine to claim task
	deadline := time.Now().Add(1 * time.Second)
	claimed := false
	for time.Now().Before(deadline) {
		t1Refreshed, err := GlobalTaskManager.GetTask("t1")
		if err == nil && t1Refreshed.Status == "in_progress" && t1Refreshed.Owner == "specialist-auth" {
			claimed = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if !claimed {
		t.Error("expected task to be claimed in the background polling loop")
	}
}
