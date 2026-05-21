package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"google.golang.org/genai"
)

// CompactContents checks for large tool outputs and compacts req.Contents.
// It also compresses older conversation rounds if total rounds > 12.
// The sessionID parameter controls which transcript archive file is used.
func CompactContents(contents []*genai.Content, sessionID string) []*genai.Content {
	if len(contents) == 0 {
		return nil
	}

	// 1. Deep copy contents so we don't modify the session history held in memory.
	copied := make([]*genai.Content, len(contents))
	for i, c := range contents {
		copied[i] = &genai.Content{
			Role: c.Role,
		}
		if c.Parts != nil {
			copied[i].Parts = make([]*genai.Part, len(c.Parts))
			for j, p := range c.Parts {
				var fcCopy *genai.FunctionCall
				if p.FunctionCall != nil {
					fcCopy = &genai.FunctionCall{
						Name: p.FunctionCall.Name,
					}
					if p.FunctionCall.Args != nil {
						argsCopy := make(map[string]any)
						for k, v := range p.FunctionCall.Args {
							argsCopy[k] = v
						}
						fcCopy.Args = argsCopy
					}
				}

				var frCopy *genai.FunctionResponse
				if p.FunctionResponse != nil {
					frCopy = &genai.FunctionResponse{
						Name: p.FunctionResponse.Name,
					}
					if p.FunctionResponse.Response != nil {
						respCopy := make(map[string]any)
						for k, v := range p.FunctionResponse.Response {
							respCopy[k] = v
						}
						frCopy.Response = respCopy
					}
				}

				copied[i].Parts[j] = &genai.Part{
					Text:             p.Text,
					InlineData:       p.InlineData,
					FunctionCall:     fcCopy,
					FunctionResponse: frCopy,
				}
			}
		}
	}

	// 2. Perform Micro-Compaction of large tool outputs (FunctionResponse)
	if sessionID == "" {
		sessionID = "session-default"
	}
	homeDir, _ := os.UserHomeDir()
	archiveDir := filepath.Join(homeDir, ".go-claude", "transcripts")
	archivePath := filepath.Join(archiveDir, sessionID+".jsonl")

	for _, c := range copied {
		for _, p := range c.Parts {
			if p != nil && p.FunctionResponse != nil && p.FunctionResponse.Response != nil {
				respBytes, _ := json.Marshal(p.FunctionResponse.Response)
				respStr := string(respBytes)

				if len(respStr) > 1000 {
					// Archive the full original output to JSONL
					_ = os.MkdirAll(archiveDir, 0755)
					f, err := os.OpenFile(archivePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
					if err == nil {
						logEntry := map[string]any{
							"timestamp": time.Now().Format(time.RFC3339),
							"role":      "tool",
							"tool_name": p.FunctionResponse.Name,
							"content":   respStr,
						}
						entryBytes, _ := json.Marshal(logEntry)
						_, _ = f.Write(append(entryBytes, '\n'))
						_ = f.Close()
					}

					// Replace with micro-compaction placeholder
					p.FunctionResponse.Response = map[string]any{
						"output": fmt.Sprintf("[Tool \"%s\" output: %d bytes of output. (Full output archived to %s)]",
							p.FunctionResponse.Name, len(respStr), archivePath),
					}
				}
			}
		}
	}

	// 3. Perform Conversational Summarization if total messages > 12
	if len(copied) > 12 {
		compacted := make([]*genai.Content, 0)

		// Keep the first round (which contains the user's initial prompt)
		compacted = append(compacted, copied[0])

		// Summarize the middle rounds (from index 1 to len(copied) - 5)
		var middlePrompts []string
		var middleTools []string

		for i := 1; i < len(copied)-4; i++ {
			c := copied[i]
			role := c.Role
			if role == "" || role == "model" {
				role = "assistant"
			}

			for _, p := range c.Parts {
				if p.Text != "" {
					if role == "user" {
						middlePrompts = append(middlePrompts, fmt.Sprintf("User: %s", p.Text))
					} else {
						middlePrompts = append(middlePrompts, fmt.Sprintf("Agent: %s", p.Text))
					}
				}
				if p.FunctionCall != nil {
					middleTools = append(middleTools, fmt.Sprintf("Called tool %s", p.FunctionCall.Name))
				}
				if p.FunctionResponse != nil {
					middleTools = append(middleTools, fmt.Sprintf("Tool %s responded", p.FunctionResponse.Name))
				}
			}
		}

		// Build a highly compact system instruction summarizing completed steps
		summaryContent := fmt.Sprintf("[System: Previous conversational history compacted. Summary of completed steps:\nPrompts: %s\nTools Executed: %s]",
			strings.Join(middlePrompts, " | "), strings.Join(middleTools, ", "))

		systemSummary := &genai.Content{
			Role: "system",
			Parts: []*genai.Part{
				{Text: summaryContent},
			},
		}
		compacted = append(compacted, systemSummary)

		// Keep the last 4 rounds
		for i := len(copied) - 4; i < len(copied); i++ {
			compacted = append(compacted, copied[i])
		}

		copied = compacted
	}

	return copied
}
