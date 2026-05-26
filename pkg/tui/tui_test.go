package tui

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"iroha/pkg/agent"

	"github.com/charmbracelet/bubbletea"
)

func TestRenderConfirmCard(t *testing.T) {
	p := "Allow running command?"

	s0 := RenderConfirmCard(p, 0)
	if !strings.Contains(s0, "Y Allow") {
		t.Error("RenderConfirmCard should render option Y")
	}

	s1 := RenderConfirmCard(p, 1)
	if !strings.Contains(s1, "N Deny") {
		t.Error("RenderConfirmCard should render option N")
	}

	s2 := RenderConfirmCard(p, 2)
	if !strings.Contains(s2, "A Always Allow") {
		t.Error("RenderConfirmCard should render option A")
	}
}

func TestModelConfirmNavigation(t *testing.T) {
	m := NewModel(nil, "test-session", false, "", "")
	m.State = stateConfirming
	m.ConfirmSelectIndex = 0

	// Move right
	res, _ := m.Update(tea.KeyMsg{Type: tea.KeyRight})
	newM := res.(Model)
	if newM.ConfirmSelectIndex != 1 {
		t.Errorf("expected ConfirmSelectIndex = 1 after KeyRight, got %d", newM.ConfirmSelectIndex)
	}

	// Move tab
	res, _ = newM.Update(tea.KeyMsg{Type: tea.KeyTab})
	newM = res.(Model)
	if newM.ConfirmSelectIndex != 2 {
		t.Errorf("expected ConfirmSelectIndex = 2 after KeyTab, got %d", newM.ConfirmSelectIndex)
	}

	// Move shift-tab (left)
	res, _ = newM.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	newM = res.(Model)
	if newM.ConfirmSelectIndex != 1 {
		t.Errorf("expected ConfirmSelectIndex = 1 after KeyShiftTab, got %d", newM.ConfirmSelectIndex)
	}
}

func TestConfirmationListenerState(t *testing.T) {
	m := NewModel(nil, "test-session", false, "", "")
	if !m.ConfirmationListenerActive {
		t.Error("expected ConfirmationListenerActive = true initially")
	}

	// 1. Send ConfirmationRequiredMsg -> should set to false
	res, cmd := m.Update(ConfirmationRequiredMsg{Prompt: "test prompt"})
	m = res.(Model)
	if m.ConfirmationListenerActive {
		t.Error("expected ConfirmationListenerActive = false after ConfirmationRequiredMsg")
	}
	if cmd != nil {
		t.Error("expected nil cmd from ConfirmationRequiredMsg")
	}
	if m.State != stateConfirming {
		t.Errorf("expected state = stateConfirming, got %s", m.State)
	}

	// 2. Press Y -> should set to true and return a listenToConfirmationBridge cmd
	res, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	m = res.(Model)
	if !m.ConfirmationListenerActive {
		t.Error("expected ConfirmationListenerActive = true after Y confirm")
	}
	if cmd == nil {
		t.Error("expected listenToConfirmationBridge cmd, got nil")
	}

	// 3. Go back to inactive state
	res, _ = m.Update(ConfirmationRequiredMsg{Prompt: "test prompt 2"})
	m = res.(Model)
	if m.ConfirmationListenerActive {
		t.Error("expected ConfirmationListenerActive = false after second ConfirmationRequiredMsg")
	}

	// 4. Cancel turn using Ctrl+C -> should set to true and return a non-nil cmd restarting the listener
	res, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = res.(Model)
	if !m.ConfirmationListenerActive {
		t.Error("expected ConfirmationListenerActive = true after Ctrl+C cancel")
	}
	if cmd == nil {
		t.Error("expected non-nil cmd restarting listener after Ctrl+C cancel, got nil")
	}
}

func TestRenderToolErrorCard(t *testing.T) {
	// 1. With a non-nil error
	errVal := errors.New("something went wrong")
	res := RenderToolErrorCard("test_tool", "arg1", 100*time.Millisecond, errVal)
	if !strings.Contains(res, "something went wrong") {
		t.Errorf("expected error message in output, got:\n%s", res)
	}

	// 2. With a nil error
	resNil := RenderToolErrorCard("test_tool", "arg1", 100*time.Millisecond, nil)
	if !strings.Contains(resNil, "operation failed") {
		t.Errorf("expected fallback message in output when error is nil, got:\n%s", resNil)
	}
}

func TestNewModelBypassPermission(t *testing.T) {
	// If initialMode is non-empty, State should be statePrompt instead of statePermissionSelect
	mAuto := NewModel(nil, "test-session", false, "auto", "hello")
	if mAuto.State != statePrompt {
		t.Errorf("expected State to be statePrompt when initialMode is set, got %s", mAuto.State.String())
	}
	if mAuto.StartupPrompt != "hello" {
		t.Errorf("expected StartupPrompt to be 'hello', got '%s'", mAuto.StartupPrompt)
	}

	mNone := NewModel(nil, "test-session", false, "", "")
	if mNone.State != statePermissionSelect {
		t.Errorf("expected State to be statePermissionSelect when initialMode is empty, got %s", mNone.State.String())
	}
}

func TestRenderHelpAndCancel(t *testing.T) {
	// Test RenderHelpDashboard
	h := RenderHelpDashboard()
	if !strings.Contains(h, "Iroha Code") || !strings.Contains(h, "Keyboard Shortcuts") {
		t.Errorf("expected help dashboard to render help text, got:\n%s", h)
	}

	// Test RenderCancelCard
	c := RenderCancelCard(1500 * time.Millisecond)
	if !strings.Contains(c, "Session aborted by user") || !strings.Contains(c, "1.5s") {
		t.Errorf("expected cancellation card to render elapsed duration, got:\n%s", c)
	}
}

func TestMatchLocalPathsAndSafety(t *testing.T) {
	// Temporarily switch CWD to project root to allow consistent relative path scans
	oldCwd, err := os.Getwd()
	if err == nil {
		if strings.HasSuffix(oldCwd, "pkg/tui") {
			_ = os.Chdir("../../")
			defer func() { _ = os.Chdir(oldCwd) }()
		}
	}

	m := NewModel(nil, "test-session", false, "", "")

	// 1. Valid local matching
	matches := m.matchLocalPaths("go.m")
	if len(matches) == 0 {
		t.Error("expected to match go.mod or go.sum under workspace root, got 0 matches")
	}
	matchedMod := false
	for _, match := range matches {
		if match == "go.mod" {
			matchedMod = true
		}
	}
	if !matchedMod {
		t.Error("expected to match 'go.mod'")
	}

	// 2. Traversal escape safety check
	escapedMatches := m.matchLocalPaths("../../../")
	if len(escapedMatches) != 0 {
		t.Errorf("safety boundary failure: expected 0 matches for traversal escape '../../..', got %d", len(escapedMatches))
	}

	// 3. Absolute path safety check
	absMatches := m.matchLocalPaths("/etc/passwd")
	if len(absMatches) != 0 {
		t.Errorf("safety boundary failure: expected 0 matches for absolute path '/etc/passwd', got %d", len(absMatches))
	}
}

func TestRenderPathCompletionBar(t *testing.T) {
	items := []string{"pkg/agent/", "pkg/tui/"}

	// Active selected index 0
	bar0 := RenderPathCompletionBar(items, 0, 80)
	if !strings.Contains(bar0, "▸ pkg/agent/") {
		t.Error("expected active match pkg/agent/ to have active indicator ▸")
	}

	// Truncation check
	longItems := []string{"path1/", "path2/", "path3/", "path4/", "path5/", "path6/"}
	barTruncated := RenderPathCompletionBar(longItems, 0, 25)
	if !strings.Contains(barTruncated, "...") {
		t.Error("expected very narrow viewport to trigger truncation '...' indicator")
	}
}

func TestModelPathCompletionFlow(t *testing.T) {
	oldCwd, err := os.Getwd()
	if err == nil {
		if strings.HasSuffix(oldCwd, "pkg/tui") {
			_ = os.Chdir("../../")
			defer func() { _ = os.Chdir(oldCwd) }()
		}
	}

	m := NewModel(nil, "test-session", false, "auto", "hello")
	m.State = statePrompt
	m.TextArea.SetValue("read go.")
	m.TextArea.SetCursor(8)

	// 1. Initial Tab Press -> Should trigger scan and auto-complete first match
	res, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	newM := res.(Model)

	if !newM.PathCompletionActive {
		t.Error("expected PathCompletionActive to be true after first Tab press")
	}
	if len(newM.PathCompletionItems) == 0 {
		t.Fatal("expected match list to be populated")
	}
	if !strings.HasPrefix(newM.TextArea.Value(), "read go.") {
		t.Errorf("expected text area value to be completed to matching files, got: %s", newM.TextArea.Value())
	}

	// 2. Second Tab Press -> Should cycle to next match
	t.Logf("[DEBUG] matches count: %d, items: %v", len(newM.PathCompletionItems), newM.PathCompletionItems)
	t.Logf("[DEBUG] before second tab: index = %d, active = %v", newM.PathCompletionIndex, newM.PathCompletionActive)
	prevVal := newM.TextArea.Value()
	res, _ = newM.Update(tea.KeyMsg{Type: tea.KeyTab})
	newM = res.(Model)
	t.Logf("[DEBUG] after second tab: index = %d, active = %v, value = '%s'", newM.PathCompletionIndex, newM.PathCompletionActive, newM.TextArea.Value())

	if newM.PathCompletionIndex != 1 {
		t.Errorf("expected completion index to cycle to 1, got %d", newM.PathCompletionIndex)
	}
	if newM.TextArea.Value() == prevVal && len(newM.PathCompletionItems) > 1 {
		t.Error("expected text area to cycle to next value, but it remained identical")
	}

	// 3. Typing other character -> Should reset completion cycle
	res, _ = newM.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	newM = res.(Model)

	if newM.PathCompletionActive {
		t.Error("expected completion active state to reset on normal char input")
	}
}

func TestConfirmationPromptAndDiffSplitting(t *testing.T) {
	m := NewModel(nil, "test-session", false, "", "")

	// 1. Prompt without diff marker
	plainPrompt := "Allow writing file test.txt?"
	res, _ := m.Update(ConfirmationRequiredMsg{Prompt: plainPrompt})
	newM := res.(Model)

	if newM.ConfirmationPrompt != plainPrompt {
		t.Errorf("expected ConfirmationPrompt to be '%s', got '%s'", plainPrompt, newM.ConfirmationPrompt)
	}
	if newM.ConfirmDiffText != "" {
		t.Errorf("expected empty ConfirmDiffText, got '%s'", newM.ConfirmDiffText)
	}
	if newM.ConfirmDiffActive {
		t.Error("expected ConfirmDiffActive to be false initially")
	}

	// 2. Prompt with diff marker
	diffContent := "+ added line\n- deleted line"
	fullPromptWithDiff := "Allow writing file test.txt?\n\n\x1b[1;34m[File Changes (Diff)]:\x1b[0m\n" + diffContent

	res, _ = m.Update(ConfirmationRequiredMsg{Prompt: fullPromptWithDiff})
	newM = res.(Model)

	if newM.ConfirmationPrompt != "Allow writing file test.txt?" {
		t.Errorf("expected extracted ConfirmationPrompt to be 'Allow writing file test.txt?', got '%s'", newM.ConfirmationPrompt)
	}
	if newM.ConfirmDiffText != diffContent {
		t.Errorf("expected extracted ConfirmDiffText to be '%s', got '%s'", diffContent, newM.ConfirmDiffText)
	}
	if newM.ConfirmDiffActive {
		t.Error("expected ConfirmDiffActive to be false initially")
	}
}

func TestModelDiffToggleKeyAction(t *testing.T) {
	m := NewModel(nil, "test-session", false, "", "")
	m.State = stateConfirming
	m.ConfirmationPrompt = "Allow writing file test.txt?"
	m.ConfirmDiffText = "+ added line\n- deleted line"
	m.ConfirmDiffActive = false

	// Pressing 'D' to toggle active state
	res, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	newM := res.(Model)

	if !newM.ConfirmDiffActive {
		t.Error("expected ConfirmDiffActive to be true after pressing 'd'")
	}
	if !strings.Contains(newM.Viewport.View(), "+ added line") {
		t.Error("expected viewport to render the diff content when ConfirmDiffActive is true")
	}

	// Pressing 'D' again to toggle off
	res, _ = newM.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	newM = res.(Model)

	if newM.ConfirmDiffActive {
		t.Error("expected ConfirmDiffActive to toggle back to false")
	}
}

func TestGetEditableValue(t *testing.T) {
	m := Model{}

	// 1. Nil ActiveTool Args
	if val := m.getEditableValue(); val != "" {
		t.Errorf("expected empty string when active tool args is nil, got '%s'", val)
	}

	// 2. shell_run command extraction
	m.ActiveTool = agent.ToolStatus{
		Name: "shell_run",
		Args: map[string]any{"command": "echo hello"},
	}
	if val := m.getEditableValue(); val != "echo hello" {
		t.Errorf("expected extracted command to be 'echo hello', got '%s'", val)
	}

	// 3. file_write content extraction
	m.ActiveTool = agent.ToolStatus{
		Name: "file_write",
		Args: map[string]any{"content": "print('hello')"},
	}
	if val := m.getEditableValue(); val != "print('hello')" {
		t.Errorf("expected extracted content to be 'print(\\'hello\\')', got '%s'", val)
	}
}

func TestConfirmationFiveOptions(t *testing.T) {
	m := NewModel(nil, "test-session", false, "auto", "hello")
	m.State = stateConfirming
	m.ConfirmSelectIndex = 0

	// 1. Cycle right (Y -> N -> Always -> Edit -> Explain)
	res, _ := m.Update(tea.KeyMsg{Type: tea.KeyRight})
	newM := res.(Model)
	if newM.ConfirmSelectIndex != 1 {
		t.Errorf("expected cycling right once to select index 1, got %d", newM.ConfirmSelectIndex)
	}

	// 2. Cycle right 4 times (wrapping around back to Y)
	for i := 0; i < 4; i++ {
		res, _ = newM.Update(tea.KeyMsg{Type: tea.KeyRight})
		newM = res.(Model)
	}
	if newM.ConfirmSelectIndex != 0 {
		t.Errorf("expected wrapping around to 0, got %d", newM.ConfirmSelectIndex)
	}

	// 3. RenderConfirmCardWithDiff rendering check for E Edit and ? Explain buttons
	card := RenderConfirmCardWithDiff("Authorize writing file?", 3, false, false)
	if !strings.Contains(card, "E Edit") || !strings.Contains(card, "? Explain") {
		t.Error("expected RenderConfirmCardWithDiff to contain E Edit and ? Explain buttons")
	}
}

func TestStatsSlashCommand(t *testing.T) {
	m := NewModel(nil, "test-session", false, "auto", "hello")
	m.State = statePrompt
	m.TextArea.SetValue("/stats")

	// Trigger stats slash command
	res, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	newM := res.(Model)

	if len(newM.History) == 0 {
		t.Fatal("expected slash command execution to add logs to History")
	}

	lastLog := newM.History[len(newM.History)-1]
	if !strings.Contains(lastLog, "Session Statistics & Telemetry") || !strings.Contains(lastLog, "Interaction Rounds") {
		t.Errorf("expected History to contain telemetry details, got:\n%s", lastLog)
	}
}

