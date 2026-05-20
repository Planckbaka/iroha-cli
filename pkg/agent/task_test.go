package agent

import (
	"os"
	"testing"
)

func TestResolveTasksDir(t *testing.T) {
	dir := ResolveTasksDir()
	if dir == "" {
		t.Fatal("ResolveTasksDir should not return empty string")
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Fatalf("ResolveTasksDir should return a valid directory, got error: %v", err)
	}
}

func TestReconcileEdges(t *testing.T) {
	tasks := map[string]*TaskRecord{
		"t1": {
			ID:      "t1",
			Subject: "Task 1",
			Status:  "pending",
			Blocks:  []string{"t2"},
		},
		"t2": {
			ID:      "t2",
			Subject: "Task 2",
			Status:  "pending",
		},
	}

	ReconcileEdges(tasks)

	if len(tasks["t2"].BlockedBy) != 1 || tasks["t2"].BlockedBy[0] != "t1" {
		t.Errorf("Expected t2 to be blocked by t1, got BlockedBy: %v", tasks["t2"].BlockedBy)
	}

	// Test reverse edge injection
	tasks["t3"] = &TaskRecord{
		ID:        "t3",
		Subject:   "Task 3",
		Status:    "pending",
		BlockedBy: []string{"t2"},
	}

	ReconcileEdges(tasks)

	if len(tasks["t2"].Blocks) != 1 || tasks["t2"].Blocks[0] != "t3" {
		t.Errorf("Expected t2 to block t3, got Blocks: %v", tasks["t2"].Blocks)
	}
	if len(tasks["t2"].BlockedBy) != 1 || tasks["t2"].BlockedBy[0] != "t1" {
		t.Errorf("Expected t2 to be blocked by t1, got BlockedBy: %v", tasks["t2"].BlockedBy)
	}
}

func TestCheckCycles(t *testing.T) {
	// 1. Valid DAG
	tasks := map[string]*TaskRecord{
		"t1": {
			ID:        "t1",
			Subject:   "Task 1",
			Status:    "pending",
			BlockedBy: []string{"t2"}, // t1 depends on t2 (t2 blocks t1)
		},
		"t2": {
			ID:      "t2",
			Subject: "Task 2",
			Status:  "pending",
		},
	}
	ReconcileEdges(tasks)
	if cycle := CheckCycles(tasks); cycle != nil {
		t.Errorf("Expected no cycle in valid DAG, got: %v", cycle)
	}

	// 2. Direct Cycle (t1 <-> t2)
	tasks["t2"].BlockedBy = []string{"t1"} // t2 depends on t1
	ReconcileEdges(tasks)
	cycle := CheckCycles(tasks)
	if cycle == nil {
		t.Error("Expected cycle to be detected")
	} else {
		// Path should contain cycle
		foundT1, foundT2 := false, false
		for _, node := range cycle {
			if node == "t1" {
				foundT1 = true
			}
			if node == "t2" {
				foundT2 = true
			}
		}
		if !foundT1 || !foundT2 {
			t.Errorf("Expected cycle path to contain t1 and t2, got: %v", cycle)
		}
	}
}

func TestCompletionCascade(t *testing.T) {
	// Setup temporary directory for tasks
	tempDir, err := os.MkdirTemp("", "go-claude-tasks-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	tm := &TaskManager{
		tasksDir: tempDir,
	}

	t1 := &TaskRecord{
		ID:      "t1",
		Subject: "Task 1",
		Status:  "pending",
		Blocks:  []string{"t2"},
	}
	t2 := &TaskRecord{
		ID:      "t2",
		Subject: "Task 2",
		Status:  "pending",
	}

	if err := tm.SaveTask(t1); err != nil {
		t.Fatal(err)
	}
	if err := tm.SaveTask(t2); err != nil {
		t.Fatal(err)
	}

	// Verify t2 is blocked by t1
	t2Fetched, err := tm.GetTask("t2")
	if err != nil {
		t.Fatal(err)
	}
	if len(t2Fetched.BlockedBy) != 1 || t2Fetched.BlockedBy[0] != "t1" {
		t.Errorf("Expected t2 to be blocked by t1, got: %v", t2Fetched.BlockedBy)
	}

	// Mark t1 completed
	t1.Status = "completed"
	if err := tm.SaveTask(t1); err != nil {
		t.Fatal(err)
	}

	// Verify t2 is no longer blocked by t1 (completed tasks do not block)
	t2Fetched, err = tm.GetTask("t2")
	if err != nil {
		t.Fatal(err)
	}
	if len(t2Fetched.BlockedBy) != 0 {
		t.Errorf("Expected t2 to be unblocked, got BlockedBy: %v", t2Fetched.BlockedBy)
	}
}
