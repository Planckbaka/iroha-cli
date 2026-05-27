package agent

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"iroha/pkg/llm"

	"google.golang.org/adk/session"
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

	if !strings.Contains(err.Error(), "security policy") {
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

// Below are added tests for comprehensive runner and session simulation

func TestCustomRunner_LifecycleAndSession(t *testing.T) {
	tempHome, err := os.MkdirTemp("", "iroha-home-lifecycle-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempHome)

	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tempHome)
	defer os.Setenv("HOME", oldHome)

	cr, err := NewCustomRunner("openai", "gpt-4o", "sk-mock-key", "http://mock-api.com", llm.APIFormatOpenAI)
	if err != nil {
		t.Fatalf("failed to create custom runner: %v", err)
	}
	defer GlobalCronScheduler.Stop()

	if cr.ModelName() != "gpt-4o" {
		t.Errorf("expected model name 'gpt-4o', got %q", cr.ModelName())
	}

	if cr.GetTokenUsage() != 0 {
		t.Errorf("expected token usage 0, got %d", cr.GetTokenUsage())
	}

	if GlobalSessionService == nil {
		t.Fatal("expected GlobalSessionService to be initialized")
	}

	ctx := context.Background()
	_, err = GlobalSessionService.Create(ctx, &session.CreateRequest{
		AppName:   "iroha",
		UserID:    "test-user",
		SessionID: "sess-test-runner",
	})
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	sessFile := filepath.Join(tempHome, ".iroha", "sessions", "sess-test-runner.json")
	if _, err := os.Stat(sessFile); os.IsNotExist(err) {
		t.Errorf("expected session file %s to be created", sessFile)
	}
}

func TestCustomRunner_Execute(t *testing.T) {
	tempHome, err := os.MkdirTemp("", "iroha-home-exec-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempHome)

	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tempHome)
	defer os.Setenv("HOME", oldHome)

	sseEvents := []string{
		`data: {"choices":[{"delta":{"content":"Mocked execution response"}}]}`,
		`data: [DONE]`,
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range sseEvents {
			fmt.Fprintln(w, event)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}))
	defer server.Close()

	cr, err := NewCustomRunner("openai", "gpt-4o", "sk-mock-key", server.URL, llm.APIFormatOpenAI)
	if err != nil {
		t.Fatalf("failed to create custom runner: %v", err)
	}
	defer GlobalCronScheduler.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	var events []*session.Event
	var execErr error
	doneChan := make(chan struct{})

	cr.Execute(ctx, "test-user", "sess-runner-exec", "How are you?", func(ev *session.Event) {
		events = append(events, ev)
	}, func(err error) {
		execErr = err
	}, func() {
		close(doneChan)
	})

	select {
	case <-doneChan:
	case <-time.After(4 * time.Second):
		t.Fatal("Timeout waiting for runner execution done")
	}

	if execErr != nil {
		t.Fatalf("unexpected runner error: %v", execErr)
	}

	if len(events) == 0 {
		t.Error("expected runner events, got none")
	}

	metaList, err := GlobalSessionService.ListSavedSessions()
	if err != nil {
		t.Fatalf("failed to list saved sessions: %v", err)
	}

	var found bool
	for _, m := range metaList {
		if m.ID == "sess-runner-exec" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected session 'sess-runner-exec' to be persisted inside mock home directory")
	}
}

func TestSelfHealingPostEditHook(t *testing.T) {
	// 1. Create a broken Go file in the pkg/agent directory to force a compile failure
	brokenFile := "broken.go"
	brokenContent := `package agent
	
	// This is broken syntax that won't compile
	func BrokenGoFunction() {
		invalid_token_here!!!
	}`
	
	if err := os.WriteFile(brokenFile, []byte(brokenContent), 0644); err != nil {
		t.Fatalf("failed to write broken file: %v", err)
	}
	defer os.Remove(brokenFile) // Clean up immediately upon test exit

	// 2. Mock a file modification tool call to trigger the compiler check
	rawTool := &mockTool{name: "file_edit"}
	toolWrapper := &blockingConfirmationTool{Tool: rawTool}

	// Make sure we are in default mode (to bypass Plan mode denials)
	originalMode := GlobalPermissionManager.GetMode()
	GlobalPermissionManager.SetMode(ModeDefault)
	defer GlobalPermissionManager.SetMode(originalMode)

	// Since toolWrapper.Run uses the global confirmation bridge, we approve it automatically
	Bridge.Reset()
	go func() {
		select {
		case <-Bridge.PromptChan:
			// Approve the tool execution
			Bridge.ResponseChan <- "y"
		case <-time.After(200 * time.Millisecond):
		}
	}()

	// Run the wrapper tool
	res, err := toolWrapper.Run(nil, map[string]any{"path": "pkg/agent/broken.go"})
	if err != nil {
		t.Fatalf("unexpected wrapper execution error: %v", err)
	}

	// 3. Verify that the compiler alert was successfully generated and injected into the additional_context!
	additionalCtx, ok := res["additional_context"].(string)
	if !ok || additionalCtx == "" {
		t.Fatalf("expected additional_context to contain compile alert, got empty/nil: %v", res)
	}

	if !strings.Contains(additionalCtx, "Post-Edit Compiler Alert") {
		t.Errorf("expected warning to contain 'Post-Edit Compiler Alert', got %q", additionalCtx)
	}

	if !strings.Contains(additionalCtx, "broken.go") {
		t.Errorf("expected warning to contain the compiler error output for broken.go, got %q", additionalCtx)
	}
}
