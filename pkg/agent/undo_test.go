package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUndoHistoryManager_PushAndUndo(t *testing.T) {
	tempDir := t.TempDir()
	file1 := filepath.Join(tempDir, "test1.txt")
	file2 := filepath.Join(tempDir, "test2.txt")

	// 1. Setup initial state: file1 exists, file2 does not exist
	err := os.WriteFile(file1, []byte("original content 1"), 0644)
	if err != nil {
		t.Fatalf("Failed to write initial file: %v", err)
	}

	// 2. Perform mock modifications:
	// - file1 gets overwritten
	// - file2 gets created
	// Capture snapshots for undo (representing the original state before modification)
	snapshots := map[string]string{
		file1: "original content 1",
		file2: "", // Empty string means the file did not exist
	}

	// Make changes
	err = os.WriteFile(file1, []byte("modified content 1"), 0644)
	if err != nil {
		t.Fatalf("Failed to write modified file: %v", err)
	}
	err = os.WriteFile(file2, []byte("new file content 2"), 0644)
	if err != nil {
		t.Fatalf("Failed to write new file: %v", err)
	}

	// 3. Register undo group
	mgr := &UndoHistoryManager{}
	mgr.Push(UndoGroup{
		Snapshots: snapshots,
	})

	// Verify manager history length
	mgr.mu.Lock()
	histLen := len(mgr.history)
	mgr.mu.Unlock()
	if histLen != 1 {
		t.Fatalf("Expected history length 1, got %d", histLen)
	}

	// 4. Perform Undo
	count, err := mgr.PopAndUndo()
	if err != nil {
		t.Fatalf("PopAndUndo failed: %v", err)
	}
	if count != 2 {
		t.Errorf("Expected 2 reverted files, got %d", count)
	}

	// 5. Verify files are back to original state
	content1, err := os.ReadFile(file1)
	if err != nil {
		t.Fatalf("Failed to read file1: %v", err)
	}
	if string(content1) != "original content 1" {
		t.Errorf("Expected file1 to be 'original content 1', got %q", string(content1))
	}

	if _, err := os.Stat(file2); !os.IsNotExist(err) {
		t.Errorf("Expected file2 to be deleted, but it still exists")
	}

	// 6. Verify second undo returns error
	_, err = mgr.PopAndUndo()
	if err == nil {
		t.Error("Expected error on empty undo history, got nil")
	}
}
