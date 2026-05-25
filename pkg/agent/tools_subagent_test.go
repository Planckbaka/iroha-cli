package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"iroha/pkg/llm"
)

func TestSubagentRunHandler_Success(t *testing.T) {
	// 1. Setup mock SSE response server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		// Yield simple choices delta
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"mock subagent analysis completed successfully\"}}]}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	// 2. Configure GlobalAgentPool for testing
	GlobalAgentPool.mu.Lock()
	origProvider := GlobalAgentPool.Provider
	origModelName := GlobalAgentPool.ModelName
	origAPIKey := GlobalAgentPool.APIKey
	origBaseURL := GlobalAgentPool.BaseURL
	origAPIFormat := GlobalAgentPool.APIFormat
	
	GlobalAgentPool.Provider = llm.ProviderOpenAI
	GlobalAgentPool.ModelName = "mock-gpt"
	GlobalAgentPool.APIKey = "mock-key"
	GlobalAgentPool.BaseURL = server.URL
	GlobalAgentPool.APIFormat = llm.APIFormatOpenAI
	GlobalAgentPool.mu.Unlock()

	defer func() {
		GlobalAgentPool.mu.Lock()
		GlobalAgentPool.Provider = origProvider
		GlobalAgentPool.ModelName = origModelName
		GlobalAgentPool.APIKey = origAPIKey
		GlobalAgentPool.BaseURL = origBaseURL
		GlobalAgentPool.APIFormat = origAPIFormat
		GlobalAgentPool.mu.Unlock()
	}()

	// Drain any leftover ToolBridge status updates
	for len(ToolBridge.StatusChan) > 0 {
		<-ToolBridge.StatusChan
	}

	// 3. Build context & args
	tempCwd := t.TempDir()
	stdCtx := context.WithValue(context.Background(), WorkdirKey, tempCwd)
	ctx := &mockToolContext{Context: stdCtx}

	args := SubagentRunArgs{
		Prompt:    "Test run subagent please",
		ModelName: "", // Use active default
	}

	// 4. Run handler
	res, err := SubagentRunHandler(ctx, args)
	if err != nil {
		t.Fatalf("SubagentRunHandler failed: %v", err)
	}

	// 5. Verify results
	if !strings.Contains(res.Summary, "mock subagent analysis completed successfully") {
		t.Errorf("expected summary to contain mock subagent output, got %q", res.Summary)
	}

	// Drain and verify status messages sent to TUI
	s1 := <-ToolBridge.StatusChan
	s2 := <-ToolBridge.StatusChan

	if !s1.Running {
		t.Errorf("expected first status message to show running=true")
	}
	if s2.Running {
		t.Errorf("expected second status message to show running=false")
	}
	if !s2.Success {
		t.Errorf("expected second status message to show success=true")
	}
}
