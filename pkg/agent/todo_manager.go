package agent

import (
	"fmt"
	"strings"
	"sync"
)

type TodoItem struct {
	Content    string `json:"content"`
	Status     string `json:"status"` // "pending", "in_progress", "completed"
	ActiveForm string `json:"activeForm,omitempty"`
}

type TodoManager struct {
	mu                 sync.RWMutex
	items              []TodoItem
	roundsSinceUpdate  int
}

var GlobalTodoManager = &TodoManager{
	items:             make([]TodoItem, 0),
	roundsSinceUpdate: 0,
}

func (tm *TodoManager) Update(items []TodoItem) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if len(items) > 12 {
		return fmt.Errorf("session plan is too long (max 12 items)")
	}

	inProgressCount := 0
	normalized := make([]TodoItem, 0, len(items))

	for i, item := range items {
		content := strings.TrimSpace(item.Content)
		if content == "" {
			return fmt.Errorf("item %d: content is required", i)
		}
		status := strings.ToLower(strings.TrimSpace(item.Status))
		if status == "" {
			status = "pending"
		}
		if status != "pending" && status != "in_progress" && status != "completed" {
			return fmt.Errorf("item %d: invalid status '%s'", i, status)
		}
		if status == "in_progress" {
			inProgressCount++
		}
		normalized = append(normalized, TodoItem{
			Content:    content,
			Status:     status,
			ActiveForm: strings.TrimSpace(item.ActiveForm),
		})
	}

	if inProgressCount > 1 {
		return fmt.Errorf("only one plan item can be in_progress")
	}

	tm.items = normalized
	tm.roundsSinceUpdate = 0
	return nil
}

func (tm *TodoManager) GetItems() []TodoItem {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	res := make([]TodoItem, len(tm.items))
	copy(res, tm.items)
	return res
}

func (tm *TodoManager) NoteRoundWithoutUpdate() {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.roundsSinceUpdate++
}

func (tm *TodoManager) RoundsSinceUpdate() int {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	return tm.roundsSinceUpdate
}

func (tm *TodoManager) ResetRounds() {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.roundsSinceUpdate = 0
}

func (tm *TodoManager) Render() string {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	if len(tm.items) == 0 {
		return ""
	}

	var sb strings.Builder
	for _, item := range tm.items {
		marker := "[ ]"
		switch item.Status {
		case "in_progress":
			marker = "[\x1b[33m>\x1b[0m]" // Yellow > for in_progress
		case "completed":
			marker = "[\x1b[32mx\x1b[0m]" // Green x for completed
		}
		line := fmt.Sprintf("  %s %s", marker, item.Content)
		if item.Status == "in_progress" && item.ActiveForm != "" {
			line += fmt.Sprintf(" (%s)", item.ActiveForm)
		}
		sb.WriteString(line + "\n")
	}

	completedCount := 0
	for _, item := range tm.items {
		if item.Status == "completed" {
			completedCount++
		}
	}
	sb.WriteString(fmt.Sprintf("\n  (\x1b[32m%d\x1b[0m/%d completed)", completedCount, len(tm.items)))
	return sb.String()
}
