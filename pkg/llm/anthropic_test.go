package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/adk/model"
	"google.golang.org/genai"
)

func TestAnthropicAdapter_TextStreaming(t *testing.T) {
	sseEvents := []string{
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-sonnet-4-6\",\"usage\":{\"input_tokens\":10,\"output_tokens\":0}}}\n\n",
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hello\"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\" world\"}}\n\n",
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n",
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":5}}\n\n",
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "test-key" {
			t.Error("Expected x-api-key header")
		}
		if r.Header.Get("anthropic-version") != "2023-06-01" {
			t.Error("Expected anthropic-version header")
		}

		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range sseEvents {
			fmt.Fprint(w, event)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}))
	defer server.Close()

	adapter := NewAnthropicAdapter("claude-sonnet-4-6", "test-key", server.URL, "", nil)
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

	if adapter.CumulativeTokens() == 0 {
		t.Error("Expected non-zero cumulative tokens")
	}
}

func TestAnthropicAdapter_ToolUse(t *testing.T) {
	sseEvents := []string{
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-sonnet-4-6\",\"usage\":{\"input_tokens\":10,\"output_tokens\":0}}}\n\n",
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_1\",\"name\":\"shell_run\"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"command\\\"\"}}\n\n",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\":\\\"ls\\\"}\"}}\n\n",
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n",
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":20}}\n\n",
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range sseEvents {
			fmt.Fprint(w, event)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}))
	defer server.Close()

	adapter := NewAnthropicAdapter("claude-sonnet-4-6", "test-key", server.URL, "", nil)
	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "Run ls"}}},
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
		t.Errorf("Expected command arg 'ls', got '%v'", toolCalls[0].Args["command"])
	}
}

func TestAnthropicAdapter_ErrorResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: error\ndata: {\"type\":\"error\",\"error\":{\"message\":\"rate limit exceeded\"}}\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer server.Close()

	adapter := NewAnthropicAdapter("claude-sonnet-4-6", "test-key", server.URL, "", nil)
	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "test"}}},
		},
	}

	var gotError bool
	for _, err := range adapter.GenerateContent(context.Background(), req, true) {
		if err != nil {
			if !strings.Contains(err.Error(), "rate limit exceeded") {
				t.Errorf("Expected rate limit error, got: %v", err)
			}
			gotError = true
			break
		}
	}

	if !gotError {
		t.Error("Expected an error from SSE error event")
	}
}

func TestAnthropicAdapter_NoAPIKey(t *testing.T) {
	adapter := NewAnthropicAdapter("claude-sonnet-4-6", "", "", "", nil)
	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "test"}}},
		},
	}

	for _, err := range adapter.GenerateContent(context.Background(), req, true) {
		if err == nil {
			t.Fatal("Expected error for empty API key")
		}
		if !strings.Contains(err.Error(), "requires an API key") {
			t.Errorf("Unexpected error message: %v", err)
		}
		return
	}
}

func TestAnthropicAdapter_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":{"message":"bad request"}}`)
	}))
	defer server.Close()

	adapter := NewAnthropicAdapter("claude-sonnet-4-6", "test-key", server.URL, "", nil)
	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{Role: "user", Parts: []*genai.Part{{Text: "test"}}},
		},
	}

	var gotError bool
	for _, err := range adapter.GenerateContent(context.Background(), req, true) {
		if err != nil {
			if !strings.Contains(err.Error(), "400") {
				t.Errorf("Expected 400 error, got: %v", err)
			}
			gotError = true
			break
		}
	}

	if !gotError {
		t.Error("Expected an error from HTTP 400")
	}
}

func TestAnthropicAdapter_ConvertMessages(t *testing.T) {
	contents := []*genai.Content{
		{Role: "user", Parts: []*genai.Part{{Text: "Hello"}}},
		{Role: "model", Parts: []*genai.Part{
			{Text: "Let me check"},
			{FunctionCall: &genai.FunctionCall{Name: "file_read", Args: map[string]any{"path": "main.go"}}},
		}},
		{Role: "user", Parts: []*genai.Part{
			{FunctionResponse: &genai.FunctionResponse{Name: "file_read", Response: map[string]any{"output": "file contents"}}},
		}},
	}

	messages, err := convertToAnthropicMessages(contents)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if len(messages) != 3 {
		t.Fatalf("Expected 3 messages, got %d", len(messages))
	}

	// First message: user text
	if messages[0].Role != "user" {
		t.Errorf("Expected role 'user', got '%s'", messages[0].Role)
	}
	if len(messages[0].Content) != 1 || messages[0].Content[0].Type != "text" {
		t.Error("Expected text block in first message")
	}

	// Second message: assistant with text + tool_use
	if messages[1].Role != "assistant" {
		t.Errorf("Expected role 'assistant', got '%s'", messages[1].Role)
	}
	if len(messages[1].Content) != 2 {
		t.Fatalf("Expected 2 content blocks, got %d", len(messages[1].Content))
	}
	if messages[1].Content[1].Type != "tool_use" {
		t.Error("Expected tool_use block")
	}
	if messages[1].Content[1].Name != "file_read" {
		t.Error("Expected tool name 'file_read'")
	}

	// Third message: user role with tool_result
	if messages[2].Role != "user" {
		t.Errorf("Expected role 'user' for tool_result, got '%s'", messages[2].Role)
	}
	if len(messages[2].Content) != 1 || messages[2].Content[0].Type != "tool_result" {
		t.Error("Expected tool_result block")
	}

	// Verify tool_input args are valid JSON
	var input map[string]any
	if err := json.Unmarshal(messages[1].Content[1].Input, &input); err != nil {
		t.Fatalf("Tool input should be valid JSON: %v", err)
	}
	if input["path"] != "main.go" {
		t.Errorf("Expected path='main.go', got '%v'", input["path"])
	}
}
