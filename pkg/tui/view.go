package tui

import (
	"strings"

	"go-claude/pkg/agent"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

// RenderMarkdown renders raw markdown into beautifully styled ANSI terminal text using Glamour
func RenderMarkdown(raw string) string {
	// Standard Glamour rendering with "dark" theme
	r, err := glamour.Render(raw, "dark")
	if err != nil {
		return raw
	}
	return r
}

// RenderConfirmCard renders the Human-in-the-Loop inline confirmation prompt
func RenderConfirmCard(prompt string) string {
	var sb strings.Builder

	sb.WriteString(prompt)
	sb.WriteString("\n   " + StyleKeyActive.Render("是否授权执行此操作？ (y - 同意 / n - 拒绝 / a - 始终允许) "))

	return StyleConfirmCard.Render(sb.String())
}

// RenderWelcomeCard renders a premium aesthetic welcome screen card
func RenderWelcomeCard(runner *agent.CustomRunner) string {
	var sb strings.Builder

	modelName := "Unknown"
	if runner != nil {
		modelName = runner.ModelName()
	}

	modeStr := string(agent.GlobalPermissionManager.GetMode())

	sb.WriteString("  " + StyleKeyActive.Render("go-claude AI Agent CLI (v1.3.0)") + "\n")
	sb.WriteString("  " + StyleKeyHelp.Render("Model: ") + StylePrompt.Render(modelName) + " | " + StyleKeyHelp.Render("Mode: ") + StylePrompt.Render(modeStr) + " | " + StyleKeyHelp.Render("Session: session-default") + "\n\n")
	sb.WriteString("  " + StyleKeyHelp.Render("Use Up/Down to cycle history. Type /exit or Ctrl+C to quit.") + "\n")
	sb.WriteString("  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")

	return StyleWelcome.Render(sb.String())
}

// RenderTodoDashboard renders a premium dashboard box wrapping the task checklist
func RenderTodoDashboard() string {
	todoRender := agent.GlobalTodoManager.Render()
	if todoRender == "" {
		return ""
	}

	headerStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorPrimary).
		Padding(0, 1).
		MarginTop(1).
		MarginBottom(1)

	return headerStyle.Render("📋  go-claude 任务规划进度面板\n\n" + todoRender) + "\n"
}
