package tui

import (
	"fmt"
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

	return headerStyle.Render("📋  go-claude 任务规划进度面板\n\n"+todoRender) + "\n"
}

// RenderErrorCard renders a premium aesthetic error card wrapping unrecoverable execution errors
func RenderErrorCard(err error) string {
	if err == nil {
		return ""
	}

	var sb strings.Builder
	errMsg := err.Error()

	// Capture dynamic tips based on error content
	var tips []string
	if strings.Contains(errMsg, "API") || strings.Contains(errMsg, "Authorization") || strings.Contains(errMsg, "ApiKey") || strings.Contains(errMsg, "接口") || strings.Contains(errMsg, "http") || strings.Contains(errMsg, "调用") {
		tips = []string{
			"请检查您的本地网络连接以及 API 终点（Base URL）是否可达",
			"确认您已在 ~/.go-claude.json 或环境变量中配置了正确的 API Key",
			"如果您想进行离线只读操作，可以通过输入 /mode plan 切换为只读“规划模式”",
		}
	} else if strings.Contains(errMsg, "权限") || strings.Contains(errMsg, "Permission") || strings.Contains(errMsg, "denied") {
		tips = []string{
			"请检查您对目标目录或文件的系统读写权限",
			"尽量把代码修改与测试命令限制在当前工作区目录内运行",
		}
	} else {
		tips = []string{
			"检查您的底层命令行工具或本地 Go 环境是否正确配置",
			"您可以重新输入指令或者尝试更换别的命令参数重新执行",
		}
	}

	sb.WriteString("  " + lipgloss.NewStyle().Foreground(ColorDanger).Bold(true).Render("❌ 执行异常 (Execution Error)") + "\n")
	sb.WriteString("  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	sb.WriteString("  " + StylePrompt.Render("故障成因:") + " " + lipgloss.NewStyle().Foreground(lipgloss.Color("#E2E8F0")).Render(errMsg) + "\n\n")
	sb.WriteString("  " + StyleKeyActive.Render("💡 建议排查方案:") + "\n")
	for i, tip := range tips {
		sb.WriteString(fmt.Sprintf("    %d. %s\n", i+1, StyleKeyHelp.Render(tip)))
	}
	sb.WriteString("  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")

	// Wrap in a gorgeous red-border card style
	cardStyle := lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).
		BorderForeground(ColorDanger).
		Padding(1, 2).
		MarginTop(1).
		MarginBottom(1)

	return cardStyle.Render(sb.String())
}
