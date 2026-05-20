package llm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/adk/model"
	"google.golang.org/genai"
)

func TestGLMAdapter_CompactRequestContents_MicroCompaction(t *testing.T) {
	// Use t.Setenv to direct user home directory to a clean temp directory
	tempHome := t.TempDir()
	t.Setenv("HOME", tempHome)

	adapter := NewGLMAdapter("glm-4", "", "")

	// 1. Create a large tool response ( > 1000 chars )
	largeStr := strings.Repeat("A", 1050)
	req := &model.LLMRequest{
		Contents: []*genai.Content{
			{
				Role: "user",
				Parts: []*genai.Part{
					{Text: "Run something"},
				},
			},
			{
				Role: "model",
				Parts: []*genai.Part{
					{
						FunctionResponse: &genai.FunctionResponse{
							Name: "shell_run",
							Response: map[string]any{
								"output": largeStr,
							},
						},
					},
				},
			},
		},
	}

	// 2. Execute Compaction
	compacted := adapter.CompactRequestContents(req)

	// Assert return length
	if len(compacted) != 2 {
		t.Fatalf("Expected compacted length of 2, got %d", len(compacted))
	}

	// Verify micro-compaction replacement has occurred in compacted output
	respPart := compacted[1].Parts[0]
	if respPart.FunctionResponse == nil {
		t.Fatal("Expected FunctionResponse part to exist")
	}
	respMap := respPart.FunctionResponse.Response
	outputVal, _ := respMap["output"].(string)
	if !strings.Contains(outputVal, "Full output archived to") {
		t.Errorf("Expected output placeholder, got: %s", outputVal)
	}

	// Verify the original request WAS NOT modified (deep-copied successfully)
	origPart := req.Contents[1].Parts[0]
	origMap := origPart.FunctionResponse.Response
	origOutput := origMap["output"].(string)
	if origOutput != largeStr {
		t.Errorf("Original request was modified! Expected length %d, got %d", len(largeStr), len(origOutput))
	}

	// Verify transcript was archived in tempHome/.go-claude/transcripts/session-default.jsonl
	archivePath := filepath.Join(tempHome, ".go-claude", "transcripts", "session-default.jsonl")
	if _, err := os.Stat(archivePath); os.IsNotExist(err) {
		t.Fatalf("Expected transcript archive file to exist at %s", archivePath)
	}

	// Read and verify the JSONL log entry
	data, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatalf("Failed to read transcript: %v", err)
	}

	var entry map[string]any
	err = json.Unmarshal(data, &entry)
	if err != nil {
		t.Fatalf("Failed to parse JSONL entry: %v", err)
	}

	if entry["tool_name"] != "shell_run" {
		t.Errorf("Expected tool_name 'shell_run', got '%v'", entry["tool_name"])
	}
	if !strings.Contains(entry["content"].(string), largeStr) {
		t.Errorf("Expected content to contain the large string")
	}
}

func TestGLMAdapter_CompactRequestContents_ConversationalSummarization(t *testing.T) {
	adapter := NewGLMAdapter("glm-4", "", "")

	// 1. Create a request with 14 turns (> 12 turns limit)
	contents := make([]*genai.Content, 14)
	for i := 0; i < 14; i++ {
		role := "user"
		if i%2 == 1 {
			role = "model"
		}
		text := "message"
		if i == 0 {
			text = "INITIAL_PROMPT"
		} else if i >= 10 {
			text = "LATEST_MESSAGES"
		}

		contents[i] = &genai.Content{
			Role: role,
			Parts: []*genai.Part{
				{Text: text},
			},
		}
	}

	req := &model.LLMRequest{
		Contents: contents,
	}

	// 2. Execute compaction
	compacted := adapter.CompactRequestContents(req)

	// Since total contents (14) > 12, it keeps the first round, summarizes middle rounds (1 to 9), and keeps the last 4 rounds (10, 11, 12, 13).
	// Total elements should be 1 + 1 (system summary) + 4 = 6 elements.
	if len(compacted) != 6 {
		t.Fatalf("Expected compacted history length of 6, got %d", len(compacted))
	}

	// First content should be INITIAL_PROMPT
	if compacted[0].Parts[0].Text != "INITIAL_PROMPT" {
		t.Errorf("Expected first round to be preserved, got: %s", compacted[0].Parts[0].Text)
	}

	// Second content should be the compacted system message
	if compacted[1].Role != "system" {
		t.Errorf("Expected role of second element to be 'system', got '%s'", compacted[1].Role)
	}
	summaryText := compacted[1].Parts[0].Text
	if !strings.Contains(summaryText, "Previous conversational history compacted") {
		t.Errorf("Expected system summary text to contain previous compacted message, got: %s", summaryText)
	}

	// The last 4 rounds should be preserved as LATEST_MESSAGES
	for i := 2; i < 6; i++ {
		text := compacted[i].Parts[0].Text
		if text != "LATEST_MESSAGES" {
			t.Errorf("Expected last rounds to be preserved, got: %s at index %d", text, i)
		}
	}
}
