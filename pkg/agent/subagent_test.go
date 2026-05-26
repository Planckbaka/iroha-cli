package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSubagent_Validation(t *testing.T) {
	manager := GlobalSubagentManager

	// Test 1: Empty Name
	spec1 := SubagentSpec{
		Name:   "",
		Type:   SubagentTypeExplore,
		Prompt: "Find all python files",
	}
	_, err := manager.RunSubagent(context.Background(), spec1)
	if err == nil {
		t.Error("expected error for empty name, got nil")
	}

	// Test 2: Empty Prompt
	spec2 := SubagentSpec{
		Name:   "explorer",
		Type:   SubagentTypeExplore,
		Prompt: "",
	}
	_, err = manager.RunSubagent(context.Background(), spec2)
	if err == nil {
		t.Error("expected error for empty prompt, got nil")
	}
}

func TestSubagent_ToolsByType(t *testing.T) {
	// Test 1: Explore type tools selection
	exploreTools, err := GetToolsForType("explore")
	if err != nil {
		t.Fatalf("failed to get tools for explore: %v", err)
	}

	hasRead := false
	hasWrite := false
	hasShell := false

	for _, tool := range exploreTools {
		if tool.Name() == "file_read" {
			hasRead = true
		}
		if tool.Name() == "file_write" {
			hasWrite = true
		}
		if tool.Name() == "shell_run" {
			hasShell = true
		}
	}

	if !hasRead {
		t.Error("explore type should contain file_read tool")
	}
	if hasWrite {
		t.Error("explore type should NOT contain file_write tool")
	}
	if hasShell {
		t.Error("explore type should NOT contain shell_run tool")
	}

	// Test 2: Executor type tools selection (returns all tools)
	execTools, err := GetToolsForType("executor")
	if err != nil {
		t.Fatalf("failed to get tools for executor: %v", err)
	}

	hasWrite = false
	hasShell = false

	for _, tool := range execTools {
		if tool.Name() == "file_write" {
			hasWrite = true
		}
		if tool.Name() == "shell_run" {
			hasShell = true
		}
	}

	if !hasWrite {
		t.Error("executor type should contain file_write tool")
	}
	if !hasShell {
		t.Error("executor type should contain shell_run tool")
	}
}

func TestSubagent_ResolveLogsDir(t *testing.T) {
	tempDir := t.TempDir()
	originalWD, _ := os.Getwd()
	_ = os.Chdir(tempDir)
	defer func() { _ = os.Chdir(originalWD) }()

	logsDir, _ := filepath.EvalSymlinks(ResolveSubagentLogsDir())
	expectedDir, _ := filepath.EvalSymlinks(filepath.Join(tempDir, ".iroha", "subagents", "logs"))

	if logsDir != expectedDir {
		t.Errorf("expected logs dir %s, got %s", expectedDir, logsDir)
	}

	if _, err := os.Stat(logsDir); os.IsNotExist(err) {
		t.Errorf("logs directory was not created: %s", logsDir)
	}
}
