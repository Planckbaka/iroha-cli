package agent

import (
	"errors"
	"fmt"
	"os"
	"testing"
)

func TestWrapToolError(t *testing.T) {
	// 1. Test os.ErrNotExist
	errNotExists := fmt.Errorf("open file.txt: %w", os.ErrNotExist)
	wrapped := WrapToolError("file_read", "file.txt", errNotExists)
	if wrapped == nil {
		t.Fatalf("expected wrapped error to be non-nil")
	}
	if !errors.Is(wrapped, os.ErrNotExist) {
		t.Errorf("expected wrapped error to retain os.ErrNotExist")
	}
	if !contains(wrapped.Error(), "Self-repair suggestion") || !contains(wrapped.Error(), "typo") {
		t.Errorf("expected wrapped error to contain self-correction suggestions for not exist, got: %s", wrapped.Error())
	}

	// 2. Test os.ErrPermission
	errPermission := fmt.Errorf("write file.txt: %w", os.ErrPermission)
	wrappedPerm := WrapToolError("file_write", "file.txt", errPermission)
	if wrappedPerm == nil {
		t.Fatalf("expected wrapped perm error to be non-nil")
	}
	if !errors.Is(wrappedPerm, os.ErrPermission) {
		t.Errorf("expected wrapped error to retain os.ErrPermission")
	}
	if !contains(wrappedPerm.Error(), "read/write permission") {
		t.Errorf("expected wrapped error to contain permission suggestions, got: %s", wrappedPerm.Error())
	}

	// 3. Test shell_run exit code failed wrapping
	errCmd := fmt.Errorf("command exited with 127")
	wrappedCmd := WrapToolError("shell_run", "go test", errCmd)
	if !contains(wrappedCmd.Error(), "local development environment") {
		t.Errorf("expected wrapped shell_run error to contain dependency environment advice, got: %s", wrappedCmd.Error())
	}
}

func TestToolCircuitBreaker(t *testing.T) {
	cb := &ToolCircuitBreaker{}

	// 1. Initial status
	cb.Reset()
	if count := cb.Track("file_read", "dummy.txt", false); count != 0 {
		t.Errorf("expected success to return 0, got %d", count)
	}

	// 2. Continuous failure tracking
	if count := cb.Track("file_read", "dummy.txt", true); count != 1 {
		t.Errorf("expected 1st failure count to be 1, got %d", count)
	}
	if count := cb.Track("file_read", "dummy.txt", true); count != 2 {
		t.Errorf("expected 2nd failure count to be 2, got %d", count)
	}
	if count := cb.Track("file_read", "dummy.txt", true); count != 3 {
		t.Errorf("expected 3rd failure count to be 3, got %d", count)
	}

	// 3. Success resets identical tracking
	if count := cb.Track("file_read", "dummy.txt", false); count != 0 {
		t.Errorf("expected success to reset count, got %d", count)
	}

	// 4. Success check again
	if count := cb.Track("file_read", "dummy.txt", true); count != 1 {
		t.Errorf("expected failure count to restart at 1, got %d", count)
	}

	// 5. Different args reset tracking
	if count := cb.Track("file_read", "other.txt", true); count != 1 {
		t.Errorf("expected different args failure to start at 1, got %d", count)
	}

	// 6. Reset explicitly
	cb.Reset()
	if count := cb.Track("file_read", "other.txt", true); count != 1 {
		t.Errorf("expected failure after reset to start at 1, got %d", count)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && s != "" && substr != "" && (s == substr || len(s) > len(substr) && stringContains(s, substr))
}

func stringContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
