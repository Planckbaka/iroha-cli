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
		Padding(0, 1).
		MarginTop(1).
		MarginBottom(1)

	return headerStyle.Render("📋  go-claude 任务规划进度面板\n\n"+todoRender) + "\n"
}

// RenderTaskDashboard renders a premium dashboard box wrapping the task graph status
func RenderTaskDashboard() string {
	tasks, err := agent.GlobalTaskManager.ListTasks()
	if err != nil || len(tasks) == 0 {
		return ""
	}

	var completed, inProgress, ready, blocked []string

	badgeCompleted := lipgloss.NewStyle().Foreground(ColorSuccess).Bold(true).Render("✓")
	badgeInProgress := lipgloss.NewStyle().Foreground(ColorWarning).Bold(true).Render("❯")
	badgeReady := lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true).Render("•")
	badgeBlocked := lipgloss.NewStyle().Foreground(ColorTextMuted).Bold(true).Render("⬡")

	for _, t := range tasks {
		ownerBadge := lipgloss.NewStyle().Foreground(ColorTextMuted).Italic(true).Render(fmt.Sprintf("@%s", t.Owner))
		
		var line string
		if t.Status == "completed" {
			line = fmt.Sprintf("  %s %s %s", badgeCompleted, StylePrompt.Render(t.ID), ownerBadge)
			completed = append(completed, line)
		} else if t.Status == "in_progress" {
			line = fmt.Sprintf("  %s %s %s", badgeInProgress, StylePrompt.Render(t.ID), ownerBadge)
			inProgress = append(inProgress, line)
		} else if len(t.BlockedBy) == 0 {
			line = fmt.Sprintf("  %s %s %s", badgeReady, StylePrompt.Render(t.ID), ownerBadge)
			ready = append(ready, line)
		} else {
			depStyle := lipgloss.NewStyle().Foreground(ColorTextMuted).Italic(true).Render(fmt.Sprintf("(need: %s)", strings.Join(t.BlockedBy, ", ")))
			line = fmt.Sprintf("  %s %s %s %s", badgeBlocked, StylePrompt.Render(t.ID), ownerBadge, depStyle)
			blocked = append(blocked, line)
		}
	}

	var sb strings.Builder
	sb.WriteString("📊 " + StyleKeyActive.Render("持久化任务图 (Durable Work Graph)") + "\n")
	
	var items []string
	if len(inProgress) > 0 {
		items = append(items, strings.Join(inProgress, "  "))
	}
	if len(ready) > 0 {
		items = append(items, strings.Join(ready, "  "))
	}
	if len(blocked) > 0 {
		items = append(items, strings.Join(blocked, "  "))
	}
	if len(completed) > 0 {
		items = append(items, strings.Join(completed, "  "))
	}
	
	sb.WriteString(strings.Join(items, "\n") + "\n")

	var total = len(tasks)
	var done = len(completed)
	progressPct := 0
	if total > 0 {
		progressPct = (done * 100) / total
	}
	sb.WriteString(fmt.Sprintf("\n  进度: \x1b[32m%d%%\x1b[0m (%d/%d 完成)", progressPct, done, total))

	cardStyle := lipgloss.NewStyle().
		Padding(0, 1).
		MarginTop(1).
		MarginBottom(1)

	return cardStyle.Render(sb.String()) + "\n"
}

// RenderTaskDetails renders the full detailed task graph panel for /task command
func RenderTaskDetails() string {
	tasks, err := agent.GlobalTaskManager.ListTasks()
	if err != nil || len(tasks) == 0 {
		return ""
	}

	var completed, inProgress, ready, blocked []string

	badgeCompleted := lipgloss.NewStyle().Background(ColorSuccess).Foreground(lipgloss.Color("#FFFFFF")).Padding(0, 1).Bold(true).Render("COMPLETED")
	badgeInProgress := lipgloss.NewStyle().Background(ColorWarning).Foreground(lipgloss.Color("#FFFFFF")).Padding(0, 1).Bold(true).Render("IN PROGRESS")
	badgeReady := lipgloss.NewStyle().Background(ColorPrimary).Foreground(lipgloss.Color("#FFFFFF")).Padding(0, 1).Bold(true).Render("READY")
	badgeBlocked := lipgloss.NewStyle().Background(ColorTextMuted).Foreground(lipgloss.Color("#FFFFFF")).Padding(0, 1).Bold(true).Render("BLOCKED")

	for _, t := range tasks {
		ownerBadge := lipgloss.NewStyle().Foreground(ColorTextMuted).Italic(true).Render(fmt.Sprintf("@%s", t.Owner))
		
		var line string
		if t.Status == "completed" {
			line = fmt.Sprintf("  %-10s %s %s", StylePrompt.Render(t.ID), t.Subject, ownerBadge)
			completed = append(completed, line)
		} else if t.Status == "in_progress" {
			line = fmt.Sprintf("  %-10s %s %s", StylePrompt.Render(t.ID), t.Subject, ownerBadge)
			inProgress = append(inProgress, line)
		} else if len(t.BlockedBy) == 0 {
			line = fmt.Sprintf("  %-10s %s %s", StylePrompt.Render(t.ID), t.Subject, ownerBadge)
			ready = append(ready, line)
		} else {
			depStyle := lipgloss.NewStyle().Foreground(ColorDanger).Italic(true).Render(fmt.Sprintf("need: %s", strings.Join(t.BlockedBy, ", ")))
			line = fmt.Sprintf("  %-10s %s %s  %s", StylePrompt.Render(t.ID), t.Subject, ownerBadge, depStyle)
			blocked = append(blocked, line)
		}
	}

	var sb strings.Builder
	sb.WriteString("📊  " + StyleKeyActive.Render("Durable Work Graph Details (任务图详细列表)") + "\n\n")

	if len(inProgress) > 0 {
		sb.WriteString(fmt.Sprintf("  %s\n", badgeInProgress))
		sb.WriteString(strings.Join(inProgress, "\n") + "\n\n")
	}
	if len(ready) > 0 {
		sb.WriteString(fmt.Sprintf("  %s\n", badgeReady))
		sb.WriteString(strings.Join(ready, "\n") + "\n\n")
	}
	if len(blocked) > 0 {
		sb.WriteString(fmt.Sprintf("  %s\n", badgeBlocked))
		sb.WriteString(strings.Join(blocked, "\n") + "\n\n")
	}
	if len(completed) > 0 {
		sb.WriteString(fmt.Sprintf("  %s\n", badgeCompleted))
		sb.WriteString(strings.Join(completed, "\n") + "\n\n")
	}

	var total = len(tasks)
	var done = len(completed)
	progressPct := 0
	if total > 0 {
		progressPct = (done * 100) / total
	}
	sb.WriteString(fmt.Sprintf("  进度: \x1b[32m%d%%\x1b[0m (%d/%d 完成)", progressPct, done, total))

	cardStyle := lipgloss.NewStyle().
		Padding(1, 2).
		MarginTop(1).
		MarginBottom(1)

	return cardStyle.Render(sb.String()) + "\n"
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
		Padding(1, 2).
		MarginTop(1).
		MarginBottom(1)

	return cardStyle.Render(sb.String())
}

// RenderTeamDashboard renders a premium dashboard card wrapping the teammate roster and mailboxes
func RenderTeamDashboard() string {
	teammates, err := agent.GlobalTeamManager.ListTeammates()
	if err != nil {
		return StyleToolError.Render(fmt.Sprintf("❌ 获取队友列表失败: %v", err))
	}

	var sb strings.Builder
	sb.WriteString("👥  " + StyleKeyActive.Render("智能体协作团队 (Agent Teams)") + "\n\n")

	if len(teammates) == 0 {
		sb.WriteString("  " + StyleKeyHelp.Render("当前没有已注册的团队成员。") + "\n")
		sb.WriteString("  " + StyleKeyHelp.Render("您可以使用 `spawn_teammate` 工具注册新的队友。") + "\n")
	} else {
		for _, t := range teammates {
			statusSymbol := lipgloss.NewStyle().Foreground(ColorTextMuted).Render("⬡ offline")
			if t.Status == "working" {
				statusSymbol = lipgloss.NewStyle().Foreground(ColorWarning).Bold(true).Render("❯ working")
			} else if t.Status == "idle" {
				statusSymbol = lipgloss.NewStyle().Foreground(ColorSuccess).Bold(true).Render("✓ idle")
			}

			sb.WriteString(fmt.Sprintf("  • %s (%s) - %s (活跃于 %s)\n",
				StylePrompt.Render(t.Name),
				lipgloss.NewStyle().Foreground(ColorSecondary).Render(t.Role),
				statusSymbol,
				t.LastActive.Format("15:04:05"),
			))
		}
	}

	cardStyle := lipgloss.NewStyle().
		Padding(1, 2).
		MarginTop(1).
		MarginBottom(1)

	return cardStyle.Render(sb.String()) + "\n"
}

// RenderWorktreeDashboard renders a premium dashboard card wrapping git worktrees isolation index
func RenderWorktreeDashboard() string {
	worktrees, err := agent.GlobalWorktreeManager.List()
	if err != nil {
		return StyleToolError.Render(fmt.Sprintf("❌ 获取 Worktree 列表失败: %v", err))
	}

	var sb strings.Builder
	sb.WriteString("🌴  " + StyleKeyActive.Render("并发沙箱分支隔离 (Git Worktree Isolation)") + "\n\n")

	if len(worktrees) == 0 {
		sb.WriteString("  " + StyleKeyHelp.Render("当前没有已注册的隔离分支沙箱。") + "\n")
		sb.WriteString("  " + StyleKeyHelp.Render("当队友被派发独立 Task 时，系统会自动为其创建并发沙箱。") + "\n")
	} else {
		for _, w := range worktrees {
			statusSymbol := lipgloss.NewStyle().Foreground(ColorTextMuted).Render("removed")
			if w.Status == "active" {
				statusSymbol = lipgloss.NewStyle().Foreground(ColorSuccess).Bold(true).Render("active")
			} else if w.Status == "kept" {
				statusSymbol = lipgloss.NewStyle().Foreground(ColorWarning).Bold(true).Render("kept")
			}

			taskInfo := ""
			if w.TaskID != "" {
				taskInfo = lipgloss.NewStyle().Foreground(ColorSecondary).Render(fmt.Sprintf(" [Task: %s]", w.TaskID))
			}

			sb.WriteString(fmt.Sprintf("  • %s (分支: %s)%s - %s\n",
				StylePrompt.Render(w.Name),
				lipgloss.NewStyle().Foreground(ColorSecondary).Render(w.Branch),
				taskInfo,
				statusSymbol,
			))
			sb.WriteString(fmt.Sprintf("    路径: %s\n", StyleKeyHelp.Render(w.Path)))
		}
	}

	cardStyle := lipgloss.NewStyle().
		Padding(1, 2).
		MarginTop(1).
		MarginBottom(1)

	return cardStyle.Render(sb.String()) + "\n"
}

// RenderMCPDashboard renders a premium dashboard card wrapping connected plugin servers
func RenderMCPDashboard() string {
	servers := agent.GlobalMCPRouter.ListServers()

	var sb strings.Builder
	sb.WriteString("🔌  " + StyleKeyActive.Render("模型能力总线插件 (MCP Server Plugin Roster)") + "\n\n")

	if len(servers) == 0 {
		sb.WriteString("  " + StyleKeyHelp.Render("当前没有活跃的 MCP Plugin 服务器。") + "\n")
		sb.WriteString("  " + StyleKeyHelp.Render("您可以在 `.go-claude/plugins.json` 配置外部 JSON-RPC 工具。") + "\n")
	} else {
		for name, status := range servers {
			statusSymbol := lipgloss.NewStyle().Foreground(ColorDanger).Bold(true).Render("disconnected")
			if status == "connected" {
				statusSymbol = lipgloss.NewStyle().Foreground(ColorSuccess).Bold(true).Render("connected")
			}

			sb.WriteString(fmt.Sprintf("  • %s - status: %s\n",
				StylePrompt.Render(name),
				statusSymbol,
			))
		}

		// Also discover and display tools if connected
		tools, err := agent.GlobalMCPRouter.DiscoverTools()
		if err == nil && len(tools) > 0 {
			sb.WriteString("\n  " + lipgloss.NewStyle().Foreground(ColorWarning).Bold(true).Render("动态注册工具 (MCP Injected Tools):") + "\n")
			for _, t := range tools {
				sb.WriteString(fmt.Sprintf("    - %s: %s\n",
					lipgloss.NewStyle().Foreground(ColorSuccess).Render(t.Name()),
					StyleKeyHelp.Render(t.Description()),
				))
			}
		}
	}

	cardStyle := lipgloss.NewStyle().
		Padding(1, 2).
		MarginTop(1).
		MarginBottom(1)

	return cardStyle.Render(sb.String()) + "\n"
}
