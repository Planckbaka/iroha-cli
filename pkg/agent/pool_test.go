package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/adk/runner"
)

func TestPool_GetWorkdirAndResolvePath(t *testing.T) {
	tempCwd, _ := os.Getwd()

	t.Run("Default process CWD", func(t *testing.T) {
		// Passing TODO context should return process cwd
		workdir := getWorkdir(context.TODO())
		if workdir != tempCwd {
			t.Errorf("Expected workdir to be %q, got %q", tempCwd, workdir)
		}

		resolved := resolvePath(context.TODO(), "some/file.txt")
		expected := filepath.Join(tempCwd, "some/file.txt")
		if resolved != expected {
			t.Errorf("Expected resolved path %q, got %q", expected, resolved)
		}
	})

	t.Run("Context overridden workdir", func(t *testing.T) {
		mockDir := "/tmp/mock-sandbox"
		stdCtx := context.WithValue(context.Background(), WorkdirKey, mockDir)

		workdir := getWorkdir(stdCtx)
		if workdir != mockDir {
			t.Errorf("Expected workdir to be %q, got %q", mockDir, workdir)
		}

		resolved := resolvePath(stdCtx, "src/main.go")
		expected := filepath.Join(mockDir, "src/main.go")
		if resolved != expected {
			t.Errorf("Expected resolved path %q, got %q", expected, resolved)
		}

		// Absolute path should not be prefixed
		resolvedAbs := resolvePath(stdCtx, "/absolute/path/file.go")
		if resolvedAbs != "/absolute/path/file.go" {
			t.Errorf("Expected unchanged absolute path, got %q", resolvedAbs)
		}
	})
}

func TestPool_ValidateSandboxPath(t *testing.T) {
	mockDir := "/tmp/mock-sandbox"
	stdCtx := context.WithValue(context.Background(), WorkdirKey, mockDir)

	t.Run("Safe path inside sandbox", func(t *testing.T) {
		safePath := filepath.Join(mockDir, "src/main.go")
		err := validateSandboxPath(stdCtx, safePath)
		if err != nil {
			t.Errorf("Expected path %q to be safe, got error: %v", safePath, err)
		}
	})

	t.Run("Unsafe path escaping sandbox", func(t *testing.T) {
		unsafePath := filepath.Join(mockDir, "../escaped.go")
		err := validateSandboxPath(stdCtx, unsafePath)
		if err == nil {
			t.Errorf("Expected path %q to trigger sandbox error, but it passed", unsafePath)
		}
		if !strings.Contains(err.Error(), "security sandbox blocked") {
			t.Errorf("Expected sandbox error message, got: %v", err)
		}
	})
}

func TestPool_AgentPoolInit(t *testing.T) {
	ap := &AgentPool{
		runners: make(map[string]*runner.Runner),
	}

	ap.mu.Lock()
	ap.Provider = "openai"
	ap.ModelName = "gpt-4o"
	ap.APIKey = "sk-test"
	ap.BaseURL = "https://api.openai.com/v1"
	ap.mu.Unlock()

	ap.mu.RLock()
	defer ap.mu.RUnlock()

	if ap.Provider != "openai" || ap.ModelName != "gpt-4o" || ap.APIKey != "sk-test" || ap.BaseURL != "https://api.openai.com/v1" {
		t.Errorf("AgentPool fields not populated correctly: %+v", ap)
	}
}
