package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// AppendToInbox appends a message to the teammate's inbox JSONL file.
func (tm *TeamManager) AppendToInbox(name string, msg TeamMessage) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	inboxPath := filepath.Join(tm.teamDir, "inbox", name+".jsonl")
	f, err := os.OpenFile(inboxPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		LogError(CatSubagent, "subagent_inbox_open_failed", fmt.Sprintf("Failed to open inbox file for teammate '%s'", name), err, map[string]any{"name": name, "path": inboxPath})
		return fmt.Errorf("failed to open inbox for %s: %w", name, err)
	}
	defer func() { _ = f.Close() }()

	data, err := json.Marshal(msg)
	if err != nil {
		LogError(CatSubagent, "subagent_marshal_failed", fmt.Sprintf("Failed to marshal message for teammate '%s'", name), err, map[string]any{"name": name})
		return fmt.Errorf("failed to marshal inbox message for %s: %w", name, err)
	}

	if _, err := f.Write(append(data, '\n')); err != nil {
		LogError(CatSubagent, "subagent_inbox_write_failed", fmt.Sprintf("Failed to write to inbox file for teammate '%s'", name), err, map[string]any{"name": name, "path": inboxPath})
		return fmt.Errorf("failed to write inbox message for %s: %w", name, err)
	}

	LogInfo(CatSubagent, "subagent_message_sent", fmt.Sprintf("Message sent to teammate '%s' from '%s'", name, msg.Sender), map[string]any{
		"sender":    msg.Sender,
		"recipient": name,
		"content":   msg.Content,
	})

	return nil
}

// ReadAndClearInbox reads all messages from an inbox and truncates the file.
func (tm *TeamManager) ReadAndClearInbox(name string) ([]TeamMessage, error) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	inboxPath := filepath.Join(tm.teamDir, "inbox", name+".jsonl")
	data, err := os.ReadFile(inboxPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read inbox for %s: %w", name, err)
	}

	// Truncate/Clear the file
	if err := os.WriteFile(inboxPath, nil, 0644); err != nil {
		return nil, fmt.Errorf("failed to clear inbox for %s: %w", name, err)
	}

	var messages []TeamMessage
	lines := splitJSONLines(data)
	for _, line := range lines {
		var msg TeamMessage
		if err := json.Unmarshal([]byte(line), &msg); err == nil {
			messages = append(messages, msg)
		}
	}
	return messages, nil
}

// PeekInbox reads all messages from an inbox without clearing it.
func (tm *TeamManager) PeekInbox(name string) ([]TeamMessage, error) {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	inboxPath := filepath.Join(tm.teamDir, "inbox", name+".jsonl")
	data, err := os.ReadFile(inboxPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to peek inbox for %s: %w", name, err)
	}

	var messages []TeamMessage
	lines := splitJSONLines(data)
	for _, line := range lines {
		var msg TeamMessage
		if err := json.Unmarshal([]byte(line), &msg); err == nil {
			messages = append(messages, msg)
		}
	}
	return messages, nil
}

// Broadcast sends a message to all registered teammates.
func (tm *TeamManager) Broadcast(sender, content string) error {
	LogInfo(CatSubagent, "subagent_broadcast", fmt.Sprintf("Broadcast message sent by '%s'", sender), map[string]any{
		"sender":  sender,
		"content": content,
	})

	teammates, err := tm.ListTeammates()
	if err != nil {
		return err
	}

	for _, t := range teammates {
		if t.Name == sender {
			continue
		}
		msg := TeamMessage{
			Sender:    sender,
			Content:   content,
			Timestamp: float64(time.Now().Unix()),
		}
		_ = tm.AppendToInbox(t.Name, msg)
	}
	return nil
}

// Helper: split JSONL data into lines
func splitJSONLines(data []byte) []string {
	var lines []string
	start := 0
	for i, b := range data {
		if b == '\n' {
			line := string(data[start:i])
			if len(line) > 0 {
				lines = append(lines, line)
			}
			start = i + 1
		}
	}
	if start < len(data) {
		line := string(data[start:])
		if len(line) > 0 {
			lines = append(lines, line)
		}
	}
	return lines
}
