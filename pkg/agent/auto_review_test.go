package agent

import (
	"testing"

	"go-claude/pkg/llm"
)

func TestHeuristicReview_SafeCommands(t *testing.T) {
	safeCmds := []string{
		"ls",
		"ls -la",
		"pwd",
		"cat README.md",
		"echo hello",
		"git status",
		"git log --oneline -5",
		"go build ./...",
		"go test ./...",
		"grep -r foo .",
		"find . -name '*.go'",
		"head -20 main.go",
		"tail -f app.log",
		"wc -l *.go",
		"which go",
		"env",
	}

	for _, cmd := range safeCmds {
		t.Run(cmd, func(t *testing.T) {
			result := heuristicReview(cmd)
			if !result.Safe {
				t.Errorf("Expected %q to be safe, but got: %s", cmd, result.Reason)
			}
		})
	}
}

func TestHeuristicReview_DangerousCommands(t *testing.T) {
	dangerousCmds := []string{
		"rm -rf /",
		"mv file1 file2",
		"cp src dst",
		"chmod 777 .",
		"sudo apt install vim",
		"curl http://example.com",
		"wget http://example.com",
		"echo hello > output.txt",
		"echo data >> file.txt",
		"kill -9 1234",
	}

	for _, cmd := range dangerousCmds {
		t.Run(cmd, func(t *testing.T) {
			result := heuristicReview(cmd)
			if result.Safe {
				t.Errorf("Expected %q to be dangerous, but got safe: %s", cmd, result.Reason)
			}
		})
	}
}

func TestReviewCommand_SimulateMode(t *testing.T) {
	// In simulate mode (no model), ReviewCommand should use heuristic
	GlobalAutoReviewConfig = nil

	safeResult := ReviewCommand("ls -la")
	if !safeResult.Safe {
		t.Errorf("ls -la should be safe in simulate mode, got: %s", safeResult.Reason)
	}

	dangerResult := ReviewCommand("rm -rf /")
	if dangerResult.Safe {
		t.Errorf("rm -rf / should be dangerous in simulate mode, got: %s", dangerResult.Reason)
	}
}

func TestSetAutoReviewConfig(t *testing.T) {
	simAdapter := llm.NewSimulatedAdapter("test-model", "", nil)
	SetAutoReviewConfig(simAdapter)

	if GlobalAutoReviewConfig == nil {
		t.Fatal("Expected GlobalAutoReviewConfig to be set")
	}
	if GlobalAutoReviewConfig.Model == nil {
		t.Fatal("Expected Model to be set")
	}
	if GlobalAutoReviewConfig.Model.Name() != "test-model" {
		t.Errorf("Expected model name test-model, got %s", GlobalAutoReviewConfig.Model.Name())
	}
}
