package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"google.golang.org/adk/model"
	"google.golang.org/genai"
)

func TestOpenAIAdapter_TextStreaming(t *testing.T) {
	sseEvents := []string{
		`data: {"choices":[{"delta":{"content":"Hello"}}]}`,
		`data: {"choices":[{"delta":{"content":" world"}}]}`,
		`data: {"choices":[{"delta":{"content":""},"finish_reason":"stop"}],"usage":{"total_tokens":15}}`,
		`data: [DONE]`,
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("Expected Authorization header with Bearer token")
		}
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			t.Errorf("Expected path /chat/completions, got %s", r.URL.Path)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range sseEvents {
			fmt.Fprintln(w, event)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}))
	defer server.Close()

	adapter := NewOpenAICompatibleAdapter("test-model", "test-key", server.URL, "System instructions go here", nil)
	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Hello"}}},
		},
	}

	var textParts []string
	for resp, err := range adapter.GenerateContent(context.Background(), req, true) {
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if resp != nil && resp.Content != nil {
			for _, p := range resp.Content.Parts {
				if p.Text != "" {
					textParts = append(textParts, p.Text)
				}
			}
		}
	}

	full := strings.Join(textParts, "")
	if full != "Hello world" {
		t.Errorf("Expected 'Hello world', got '%s'", full)
	}

	if adapter.CumulativeTokens() != 15 {
		t.Errorf("Expected 15 cumulative tokens, got %d", adapter.CumulativeTokens())
	}
}

func TestOpenAIAdapter_MultiToolCall(t *testing.T) {
	sseEvents := []string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_shell","type":"function","function":{"name":"shell_run","arguments":"{\n  \"command\":"}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":" \"ls\""}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\n}"}}]}}]}`,
		`data: {"choices":[{"delta":{"content":""},"finish_reason":"tool_calls"}]}`,
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

	adapter := NewOpenAICompatibleAdapter("test-model", "test-key", server.URL, "", nil)

	// Declare a tool schema
	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Run command"}}},
		},
		Config: &genai.GenerateContentConfig{
			Tools: []*genai.Tool{
				{
					FunctionDeclarations: []*genai.FunctionDeclaration{
						{
							Name:        "shell_run",
							Description: "Run shell command",
						},
					},
				},
			},
		},
	}

	var toolCalls []*genai.FunctionCall
	for resp, err := range adapter.GenerateContent(context.Background(), req, true) {
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if resp != nil && resp.Content != nil {
			for _, p := range resp.Content.Parts {
				if p.FunctionCall != nil {
					toolCalls = append(toolCalls, p.FunctionCall)
				}
			}
		}
	}

	if len(toolCalls) != 1 {
		t.Fatalf("Expected 1 tool call, got %d", len(toolCalls))
	}
	if toolCalls[0].Name != "shell_run" {
		t.Errorf("Expected tool name 'shell_run', got '%s'", toolCalls[0].Name)
	}
	if toolCalls[0].Args["command"] != "ls" {
		t.Errorf("Expected command argument 'ls', got '%v'", toolCalls[0].Args["command"])
	}
}

func TestOpenAIAdapter_TransientFailureRetry(t *testing.T) {
	var attempts int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&attempts, 1)
		if count == 1 {
			// First attempt: return transient 429
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"Rate limit exceeded"}}`))
			return
		}

		// Subsequent attempt: return 200 OK
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"Success after retry"}}]}`, "\n", `data: [DONE]`)
	}))
	defer server.Close()

	adapter := NewOpenAICompatibleAdapter("test-model", "test-key", server.URL, "", nil)
	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Hello"}}},
		},
	}

	var textParts []string
	for resp, err := range adapter.GenerateContent(context.Background(), req, true) {
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if resp != nil && resp.Content != nil {
			for _, p := range resp.Content.Parts {
				if p.Text != "" {
					textParts = append(textParts, p.Text)
				}
			}
		}
	}

	finalText := strings.Join(textParts, "")
	if !strings.Contains(finalText, "Success after retry") {
		t.Errorf("Expected response containing 'Success after retry', got '%s'", finalText)
	}

	finalAttempts := atomic.LoadInt32(&attempts)
	if finalAttempts != 2 {
		t.Errorf("Expected 2 attempts, got %d", finalAttempts)
	}
}

func TestOpenAIAdapter_MissingAPIKey(t *testing.T) {
	adapter := NewOpenAICompatibleAdapter("test-model", "", "http://localhost:8080", "", nil)
	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Hello"}}},
		},
	}

	for _, err := range adapter.GenerateContent(context.Background(), req, true) {
		if err == nil {
			t.Fatal("Expected error due to missing API key")
		}
		if !strings.Contains(err.Error(), "requires an API key") {
			t.Errorf("Unexpected error message: %v", err)
		}
		return
	}
}

func TestOpenAIAdapter_HTTPFatalError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Non-transient 400 Bad Request error
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"Bad Request"}}`))
	}))
	defer server.Close()

	adapter := NewOpenAICompatibleAdapter("test-model", "test-key", server.URL, "", nil)
	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Hello"}}},
		},
	}

	var gotError bool
	for _, err := range adapter.GenerateContent(context.Background(), req, true) {
		if err != nil {
			if !strings.Contains(err.Error(), "400") {
				t.Errorf("Expected error containing '400', got: %v", err)
			}
			gotError = true
			break
		}
	}

	if !gotError {
		t.Error("Expected an error from HTTP 400")
	}
}

func TestOpenAIAdapter_ConvertMessageJSON(t *testing.T) {
	// Tests standard conversion helper inside openai.go indirectly by verifying payload structures sent to mock server
	var receivedBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		decoder := json.NewDecoder(r.Body)
		var req chatRequest
		if err := decoder.Decode(&req); err == nil {
			bodyBytes, _ := json.Marshal(req)
			receivedBody = string(bodyBytes)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"choices":[{"delta":{"content":"ok"}}]}`, "\n", `data: [DONE]`)
	}))
	defer server.Close()

	adapter := NewOpenAICompatibleAdapter("test-model", "test-key", server.URL, "CustomSystem", nil)
	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "UserText"}}},
			{Role: "model", Parts: []*genai.Part{
				{Text: "AssistantText"},
				{FunctionCall: &genai.FunctionCall{Name: "test_tool", Args: map[string]any{"arg1": 123}}},
			}},
			{Role: "user", Parts: []*genai.Part{
				{FunctionResponse: &genai.FunctionResponse{Name: "test_tool", Response: map[string]any{"result": "success"}}},
			}},
		},
	}

	for range adapter.GenerateContent(context.Background(), req, true) {
	}

	if !strings.Contains(receivedBody, `"role":"system"`) || !strings.Contains(receivedBody, `"content":"CustomSystem"`) {
		t.Errorf("System prompt conversion failed. Received: %s", receivedBody)
	}
	if !strings.Contains(receivedBody, `"role":"user"`) || !strings.Contains(receivedBody, `"content":"UserText"`) {
		t.Errorf("User prompt conversion failed. Received: %s", receivedBody)
	}
	if !strings.Contains(receivedBody, `"role":"assistant"`) || !strings.Contains(receivedBody, `"content":"AssistantText"`) {
		t.Errorf("Assistant prompt conversion failed. Received: %s", receivedBody)
	}
	if !strings.Contains(receivedBody, `"tool_calls"`) || !strings.Contains(receivedBody, `"test_tool"`) {
		t.Errorf("Tool call conversion failed. Received: %s", receivedBody)
	}
}
