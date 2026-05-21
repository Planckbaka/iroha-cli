package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbletea"
)

func TestRenderConfirmCard(t *testing.T) {
	p := "Allow running command?"

	s0 := RenderConfirmCard(p, 0)
	if !strings.Contains(s0, "Y 同意") {
		t.Error("RenderConfirmCard should render option Y")
	}

	s1 := RenderConfirmCard(p, 1)
	if !strings.Contains(s1, "N 拒绝") {
		t.Error("RenderConfirmCard should render option N")
	}

	s2 := RenderConfirmCard(p, 2)
	if !strings.Contains(s2, "A 始终允许") {
		t.Error("RenderConfirmCard should render option A")
	}
}

func TestModelConfirmNavigation(t *testing.T) {
	m := NewModel(nil, "test-session", false)
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
