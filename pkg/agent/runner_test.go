package agent

import (
	"errors"
	"strings"
	"testing"
	"time"

	"google.golang.org/adk/tool"
	"google.golang.org/genai"
)

// mockTool implements the adkRunnableTool interface and tool.Tool for testing.
type mockTool struct {
	name    string
	runFunc func(ctx tool.Context, args any) (map[string]any, error)
}

func (m *mockTool) Name() string {
	return m.name
}

func (m *mockTool) Description() string {
	return "Mock tool for testing purposes"
}

func (m *mockTool) IsLongRunning() bool {
	return false
}

func (m *mockTool) Run(ctx tool.Context, args any) (map[string]any, error) {
	if m.runFunc != nil {
		return m.runFunc(ctx, args)
	}
	return map[string]any{"status": "ok"}, nil
}

func (m *mockTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        m.name,
		Description: m.Description(),
	}
}

func TestConfirmationBridge_Workflow(t *testing.T) {
	t.Run("Standard Y Approval Flow", func(t *testing.T) {
		Bridge.Reset()

		promptSent := "Test Prompt"
		go func() {
			select {
			case p := <-Bridge.PromptChan:
				if p != promptSent {
					t.Errorf("Expected prompt %q, got %q", promptSent, p)
				}
				Bridge.ResponseChan <- "y"
			case <-time.After(200 * time.Millisecond):
				t.Error("Timed out waiting for prompt")
			}
		}()

		select {
		case Bridge.PromptChan <- promptSent:
		case <-time.After(100 * time.Millisecond):
			t.Fatal("Failed to send prompt")
		}

		select {
		case res := <-Bridge.ResponseChan:
			if res != "y" {
				t.Errorf("Expected 'y', got %q", res)
			}
		case <-time.After(100 * time.Millisecond):
			t.Fatal("Failed to receive response")
		}
	})

	t.Run("Cancellation Workflow", func(t *testing.T) {
		Bridge.Reset()

		cancelCh := Bridge.CancelChanRead()
		select {
		case <-cancelCh:
			t.Error("Cancel channel should be open initially")
		default:
		}

		Bridge.Cancel()

		select {
		case <-cancelCh:
			// Success, channel closed
		case <-time.After(100 * time.Millisecond):
			t.Error("Timed out waiting for cancel channel to close")
		}

		// Resetting should open cancel channel again
		Bridge.Reset()
		cancelCh2 := Bridge.CancelChanRead()
		select {
		case <-cancelCh2:
			t.Error("Cancel channel should be open after reset")
		default:
		}
	})
}

func TestToolStatusBridge_Drain(t *testing.T) {
	// StatusChan has buffer capacity of 100, let's flush any leftovers first
	for len(ToolBridge.StatusChan) > 0 {
		<-ToolBridge.StatusChan
	}

	status1 := ToolStatus{Name: "test_tool", Running: true}
	status2 := ToolStatus{Name: "test_tool", Running: false, Success: true}

	ToolBridge.Send(status1)
	ToolBridge.Send(status2)

	select {
	case s := <-ToolBridge.StatusChan:
		if s.Name != "test_tool" || !s.Running {
			t.Errorf("Unexpected status: %+v", s)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Timed out waiting for status 1")
	}

	select {
	case s := <-ToolBridge.StatusChan:
		if s.Name != "test_tool" || s.Running || !s.Success {
			t.Errorf("Unexpected status: %+v", s)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Timed out waiting for status 2")
	}
}

func TestToolCircuitBreaker_FailureAccumulation(t *testing.T) {
	cb := &ToolCircuitBreaker{}

	// First failure: count = 1
	count := cb.Track("shell_run", "ls -la", true)
	if count != 1 {
		t.Errorf("Expected failure count 1, got %d", count)
	}

	// Second failure with same arguments: count = 2
	count = cb.Track("shell_run", "ls -la", true)
	if count != 2 {
		t.Errorf("Expected failure count 2, got %d", count)
	}

	// Third failure with different arguments: resets to 1
	count = cb.Track("shell_run", "ls", true)
	if count != 1 {
		t.Errorf("Expected count reset to 1 due to different args, got %d", count)
	}

	// Success with same args: resets to 0
	count = cb.Track("shell_run", "ls", false)
	if count != 0 {
		t.Errorf("Expected count to reset to 0 on success, got %d", count)
	}

	// Failure count rises again
	count = cb.Track("shell_run", "ls", true)
	if count != 1 {
		t.Errorf("Expected count 1, got %d", count)
	}

	cb.Reset()
	count = cb.Track("shell_run", "ls", true)
	if count != 1 {
		t.Errorf("Expected count 1 after reset, got %d", count)
	}
}

func TestBlockingConfirmationTool_PermissionDeny(t *testing.T) {
	// Temporarily override permission mode to Plan
	originalMode := GlobalPermissionManager.GetMode()
	GlobalPermissionManager.SetMode(ModePlan)
	defer GlobalPermissionManager.SetMode(originalMode)

	// In Plan Mode, any "shell_run" write action is immediately denied
	rawTool := &mockTool{name: "shell_run"}
	toolWrapper := &blockingConfirmationTool{Tool: rawTool}

	res, err := toolWrapper.Run(nil, ShellRunArgs{Command: "echo hello"})
	if err == nil {
		t.Fatal("Expected error because permission should be denied in Plan mode")
	}

	if res != nil {
		t.Errorf("Expected nil result, got %+v", res)
	}

	if !strings.Contains(err.Error(), "安全策略拒绝") {
		t.Errorf("Expected safety policy rejection error, got: %v", err)
	}
}

func TestBlockingConfirmationTool_AskFlow(t *testing.T) {
	// Standard Mode: shell_run prompt asks the user
	originalMode := GlobalPermissionManager.GetMode()
	GlobalPermissionManager.SetMode(ModeDefault)
	defer GlobalPermissionManager.SetMode(originalMode)

	rawTool := &mockTool{name: "shell_run"}
	toolWrapper := &blockingConfirmationTool{Tool: rawTool}

	// Reset confirmation bridge
	Bridge.Reset()

	// 1. Simulating approval "y"
	go func() {
		select {
		case <-Bridge.PromptChan:
			// Automatically approve
			Bridge.ResponseChan <- "y"
		case <-time.After(200 * time.Millisecond):
			// Timeout fallback
		}
	}()

	res, err := toolWrapper.Run(nil, ShellRunArgs{Command: "echo hello"})
	if err != nil {
		t.Fatalf("Unexpected error under approved confirmation flow: %v", err)
	}
	if res == nil || res["status"] != "ok" {
		t.Errorf("Expected success result status ok, got %+v", res)
	}

	// 2. Simulating denial "n"
	Bridge.Reset()
	go func() {
		select {
		case <-Bridge.PromptChan:
			Bridge.ResponseChan <- "n"
		case <-time.After(200 * time.Millisecond):
		}
	}()

	res, err = toolWrapper.Run(nil, ShellRunArgs{Command: "echo hello"})
	if err == nil {
		t.Fatal("Expected error under denied confirmation flow")
	}
	if !errors.Is(err, tool.ErrConfirmationRejected) {
		t.Errorf("Expected ErrConfirmationRejected, got: %v", err)
	}
	if res != nil {
		t.Errorf("Expected nil result under denial, got %+v", res)
	}
}
