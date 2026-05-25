package agent

import (
	"strings"
	"testing"
)

func TestTodoManager_Update(t *testing.T) {
	// Create a clean TodoManager instance
	tm := &TodoManager{
		items:             make([]TodoItem, 0),
		roundsSinceUpdate: 5, // pre-set rounds to verify reset
	}

	// 1. Successful update
	validItems := []TodoItem{
		{Content: "Analyze system architecture", Status: "completed"},
		{Content: "Modify config file", Status: "in_progress", ActiveForm: "Writing config.go"},
		{Content: "Run unit tests", Status: "pending"},
	}

	err := tm.Update(validItems)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if len(tm.GetItems()) != 3 {
		t.Errorf("Expected 3 items, got %d", len(tm.GetItems()))
	}

	if tm.RoundsSinceUpdate() != 0 {
		t.Errorf("Expected roundsSinceUpdate to reset to 0, got %d", tm.RoundsSinceUpdate())
	}

	// 2. Error: More than 12 items
	tooManyItems := make([]TodoItem, 13)
	for i := range tooManyItems {
		tooManyItems[i] = TodoItem{Content: "Task", Status: "pending"}
	}
	err = tm.Update(tooManyItems)
	if err == nil || !strings.Contains(err.Error(), "session plan is too long") {
		t.Errorf("Expected 'session plan is too long' error, got: %v", err)
	}

	// 3. Error: Empty content
	invalidContent := []TodoItem{
		{Content: "   ", Status: "pending"},
	}
	err = tm.Update(invalidContent)
	if err == nil || !strings.Contains(err.Error(), "content is required") {
		t.Errorf("Expected 'content is required' error, got: %v", err)
	}

	// 4. Error: Invalid status
	invalidStatus := []TodoItem{
		{Content: "Task 1", Status: "unknown_status"},
	}
	err = tm.Update(invalidStatus)
	if err == nil || !strings.Contains(err.Error(), "invalid status") {
		t.Errorf("Expected 'invalid status' error, got: %v", err)
	}

	// 5. Error: Double 'in_progress'
	doubleInProgress := []TodoItem{
		{Content: "Task 1", Status: "in_progress"},
		{Content: "Task 2", Status: "in_progress"},
	}
	err = tm.Update(doubleInProgress)
	if err == nil || !strings.Contains(err.Error(), "only one plan item can be in_progress") {
		t.Errorf("Expected 'only one plan item can be in_progress' error, got: %v", err)
	}
}

func TestTodoManager_RoundsAndNag(t *testing.T) {
	tm := &TodoManager{
		items:             make([]TodoItem, 0),
		roundsSinceUpdate: 0,
	}

	tm.NoteRoundWithoutUpdate()
	tm.NoteRoundWithoutUpdate()
	if tm.RoundsSinceUpdate() != 2 {
		t.Errorf("Expected roundsSinceUpdate = 2, got %d", tm.RoundsSinceUpdate())
	}

	tm.ResetRounds()
	if tm.RoundsSinceUpdate() != 0 {
		t.Errorf("Expected roundsSinceUpdate = 0 after ResetRounds, got %d", tm.RoundsSinceUpdate())
	}
}

func TestTodoManager_Render(t *testing.T) {
	tm := &TodoManager{
		items:             make([]TodoItem, 0),
		roundsSinceUpdate: 0,
	}

	// Empty render
	if tm.Render() != "" {
		t.Errorf("Expected empty render for empty items, got: %q", tm.Render())
	}

	items := []TodoItem{
		{Content: "Task A", Status: "completed"},
		{Content: "Task B", Status: "in_progress", ActiveForm: "doing B"},
		{Content: "Task C", Status: "pending"},
	}
	_ = tm.Update(items)

	rendered := tm.Render()
	t.Logf("Rendered Output:\n%s", rendered)

	if !strings.Contains(rendered, "Task A") || !strings.Contains(rendered, "Task B") || !strings.Contains(rendered, "Task C") {
		t.Errorf("Rendered output missing task description")
	}

	// Verify status icons / colors
	if !strings.Contains(rendered, "[") || !strings.Contains(rendered, "]") {
		t.Errorf("Rendered output missing checklist brackets")
	}

	if !strings.Contains(rendered, "completed)") {
		t.Errorf("Rendered output missing stats summary suffix")
	}
}
