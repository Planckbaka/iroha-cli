package agent

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// setupTestGitRepo creates a sandboxed temporary Git repository for safe test execution.
func setupTestGitRepo(t *testing.T) (string, func()) {
	tempDir, err := os.MkdirTemp("", "iroha-git-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get CWD: %v", err)
	}

	// Change working directory to sandboxed temp git repo
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("failed to Chdir: %v", err)
	}

	// Initialize git
	initCmd := exec.Command("git", "init")
	if err := initCmd.Run(); err != nil {
		t.Fatalf("failed to git init: %v", err)
	}

	// Configure mock git user for safe commit testing (prevents failure if git config is empty on host)
	_ = exec.Command("git", "config", "user.name", "Iroha Tester").Run()
	_ = exec.Command("git", "config", "user.email", "tester@iroha.ai").Run()

	cleanup := func() {
		_ = os.Chdir(oldCwd)
		_ = os.RemoveAll(tempDir)
	}

	return tempDir, cleanup
}

func TestGitHasChanges(t *testing.T) {
	_, cleanup := setupTestGitRepo(t)
	defer cleanup()

	// 1. Initial empty state -> should report no changes
	hasChanges, err := GitHasChanges()
	if err != nil {
		t.Fatalf("unexpected GitHasChanges error: %v", err)
	}
	if hasChanges {
		t.Error("expected hasChanges to be false on empty repo")
	}

	// 2. Write file -> should report changes present
	testFile := "test.txt"
	if err := os.WriteFile(testFile, []byte("hello world"), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	hasChanges, err = GitHasChanges()
	if err != nil {
		t.Fatalf("unexpected GitHasChanges error: %v", err)
	}
	if !hasChanges {
		t.Error("expected hasChanges to be true after adding modifications")
	}
}

func TestGitGetStagedDiff(t *testing.T) {
	_, cleanup := setupTestGitRepo(t)
	defer cleanup()

	// 1. Empty state -> empty diff
	diff, err := GitGetStagedDiff()
	if err != nil {
		t.Fatalf("unexpected GitGetStagedDiff error: %v", err)
	}
	if strings.TrimSpace(diff) != "" {
		t.Errorf("expected empty diff, got: %s", diff)
	}

	// 2. Modified but unstaged -> diff should capture unstaged edits
	testFile := "test.txt"
	if err := os.WriteFile(testFile, []byte("line 1\n"), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}
	// Initial commit to establish base
	_ = exec.Command("git", "add", "test.txt").Run()
	_ = exec.Command("git", "commit", "-m", "initial").Run()

	// Modify file
	if err := os.WriteFile(testFile, []byte("line 1\nline 2 added\n"), 0644); err != nil {
		t.Fatalf("failed to modify test file: %v", err)
	}

	diff, err = GitGetStagedDiff()
	if err != nil {
		t.Fatalf("unexpected GitGetStagedDiff error: %v", err)
	}
	if !strings.Contains(diff, "+line 2 added") {
		t.Errorf("expected diff to contain added line, got: %s", diff)
	}

	// 3. Stage changes -> diff should capture staged edits
	_ = exec.Command("git", "add", "test.txt").Run()
	diffStaged, err := GitGetStagedDiff()
	if err != nil {
		t.Fatalf("unexpected GitGetStagedDiff error: %v", err)
	}
	if !strings.Contains(diffStaged, "+line 2 added") {
		t.Errorf("expected staged diff to contain added line, got: %s", diffStaged)
	}
}

func TestGitCommit(t *testing.T) {
	_, cleanup := setupTestGitRepo(t)
	defer cleanup()

	testFile := "test.txt"
	if err := os.WriteFile(testFile, []byte("aider style auto-commit content"), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	// Commit automatically staging the untracked file
	commitMsg := "feat: implement aider auto commit"
	err := GitCommit(commitMsg)
	if err != nil {
		t.Fatalf("GitCommit failed: %v", err)
	}

	// Check git log for verify
	logCmd := exec.Command("git", "log", "-n", "1", "--pretty=format:%s")
	var out bytes.Buffer
	logCmd.Stdout = &out
	if err := logCmd.Run(); err != nil {
		t.Fatalf("failed to run git log: %v", err)
	}

	gotMsg := out.String()
	if gotMsg != commitMsg {
		t.Errorf("expected commit message '%s', got '%s'", commitMsg, gotMsg)
	}
}
