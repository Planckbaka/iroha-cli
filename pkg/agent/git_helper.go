package agent

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// GitHasChanges checks if there are any staged or unstaged changes in the workspace repository.
func GitHasChanges() (bool, error) {
	cmd := exec.Command("git", "status", "--porcelain")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		// If not a git repository or git command missing, return false silently
		return false, err
	}
	return strings.TrimSpace(out.String()) != "", nil
}

// GitGetStagedDiff returns the unified diff of changes in the repository.
// It prioritizes staged changes, falling back to unstaged changes to capture recent edits.
func GitGetStagedDiff() (string, error) {
	// 1. Try Cached / Staged diff first
	cmd := exec.Command("git", "diff", "--cached")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", err
	}
	diff := out.String()
	if strings.TrimSpace(diff) != "" {
		return diff, nil
	}

	// 2. Fallback to general unstaged changes
	cmd2 := exec.Command("git", "diff")
	var out2 bytes.Buffer
	cmd2.Stdout = &out2
	if err := cmd2.Run(); err != nil {
		return "", err
	}
	return out2.String(), nil
}

// GitCommit stages all current modifications and commits them with the given message.
func GitCommit(msg string) error {
	// Aider-style automated staging: ensure all untracked/modified files are staged
	addCmd := exec.Command("git", "add", "-A")
	if err := addCmd.Run(); err != nil {
		return fmt.Errorf("failed to git add -A: %v", err)
	}

	commitCmd := exec.Command("git", "commit", "-m", msg)
	if err := commitCmd.Run(); err != nil {
		return fmt.Errorf("failed to git commit: %v", err)
	}
	return nil
}
