package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go-claude/pkg/agent"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

// RenderMarkdown renders raw markdown into beautifully styled ANSI terminal text using Glamour
func RenderMarkdown(raw string) string {
	r, err := glamour.Render(raw, "dark")
	if err != nil {
		return raw
	}

	// Post-process to highlight diff lines in terminal
	lines := strings.Split(r, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "+ ") || trimmed == "+" {
			lines[i] = "\x1b[32m" + line + "\x1b[0m"
		} else if strings.HasPrefix(trimmed, "- ") || trimmed == "-" {
			lines[i] = "\x1b[31m" + line + "\x1b[0m"
		}
	}
	return strings.Join(lines, "\n")
}

// RenderConfirmCard renders the Human-in-the-Loop inline confirmation prompt
func RenderConfirmCard(prompt string) string {
	var sb strings.Builder

	// Header
	sb.WriteString(lipgloss.NewStyle().
		Foreground(ColorWarning).Bold(true).
		Render("需要您的授权") + "\n\n")

	// Prompt content
	sb.WriteString(prompt)
	sb.WriteString("\n\n")

	// Key hints
	yStyle := lipgloss.NewStyle().Foreground(ColorSuccess).Bold(true).Padding(0, 1).Border(lipgloss.RoundedBorder()).BorderForeground(ColorSuccess)
	nStyle := lipgloss.NewStyle().Foreground(ColorDanger).Bold(true).Padding(0, 1).Border(lipgloss.RoundedBorder()).BorderForeground(ColorDanger)
	aStyle := lipgloss.NewStyle().Foreground(ColorWarning).Bold(true).Padding(0, 1).Border(lipgloss.RoundedBorder()).BorderForeground(ColorWarning)

	sb.WriteString("  ")
	sb.WriteString(yStyle.Render("Y 同意"))
	sb.WriteString("  ")
	sb.WriteString(nStyle.Render("N 拒绝"))
	sb.WriteString("  ")
	sb.WriteString(aStyle.Render("A 始终允许"))

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.ThickBorder()).
		BorderForeground(ColorWarning).
		Padding(1, 2).
		MarginTop(1).
		MarginBottom(1)

	return boxStyle.Render(sb.String())
}

// RenderWelcomeCard renders a minimal welcome screen
func RenderWelcomeCard(runner *agent.CustomRunner) string {
	var sb strings.Builder

	modelName := "Unknown"
	if runner != nil {
		modelName = runner.ModelName()
	}

	modeStr := string(agent.GlobalPermissionManager.GetMode())

	sb.WriteString("  " + StyleKeyActive.Render("go-claude") + "  " + StyleKeyHelp.Render("v1.3.0") + "\n")
	sb.WriteString("  " + StyleKeyHelp.Render("model  ") + StylePrompt.Render(modelName) + "\n")
	sb.WriteString("  " + StyleKeyHelp.Render("mode   ") + StylePrompt.Render(modeStr) + "\n\n")
	sb.WriteString("  " + StyleKeyHelp.Render("输入 / 查看所有命令   Up/Down — 历史记录   /exit — 退出") + "\n")

	return StyleWelcome.Render(sb.String())
}

// RenderSlashMenu renders the slash command popup above the textarea
func RenderSlashMenu(items []SlashMenuItem, selectedIndex int, width int) string {
	maxItems := 8
	if len(items) < maxItems {
		maxItems = len(items)
	}

	var sb strings.Builder
	for i := 0; i < maxItems; i++ {
		item := items[i]
		cmdStyle := lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true).Width(18)
		descStyle := lipgloss.NewStyle().Foreground(ColorTextMuted)

		line := "  " + cmdStyle.Render(item.Command) + "  " + descStyle.Render(item.Description)

		if i == selectedIndex {
			line = lipgloss.NewStyle().
				Background(lipgloss.Color("#3F3F46")).
				Foreground(lipgloss.Color("#ffffff")).
				Bold(true).
				Width(width - 2).
				Render("  " + lipgloss.NewStyle().Bold(true).Width(18).Render(item.Command) + "  " + item.Description)
		}
		sb.WriteString(line + "\n")
	}

	if len(items) > 8 {
		sb.WriteString("  " + StyleKeyHelp.Render(fmt.Sprintf("... 还有 %d 个命令", len(items)-8)) + "\n")
	}

	footer := StyleKeyHelp.Render("  ↑↓ 选择   Tab 补全   Enter 执行   Esc 关闭")
	sb.WriteString(footer)

	menuStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorPrimary).
		Padding(0, 0)

	return menuStyle.Render(sb.String())
}

// permModeNames is the ordered list for the permission selection screen
var permModeNames = []struct {
	Mode  agent.PermissionMode
	Label string
	Desc  string
	Icon  string
}{
	{agent.ModePlan, "Plan Mode", "只读模式 — 拦截所有写操作与 Shell 命令", ""},
	{agent.ModeDefault, "Default Mode", "每次敏感操作需用户手动授权（推荐）", ""},
	{agent.ModeAuto, "Auto Mode", "读操作自动放行，写操作仍需授权", ""},
}

// RenderPermissionSelectScreen renders the full-screen startup permission selection
func RenderPermissionSelectScreen(m Model) string {
	var sb strings.Builder

	sb.WriteString("\n\n")
	sb.WriteString(lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true).
		Render("  选择 Agent 权限模式") + "\n\n")
	sb.WriteString(lipgloss.NewStyle().Foreground(ColorTextMuted).
		Render("  此设置控制 Agent 执行工具时的安全级别") + "\n\n")

	for i, entry := range permModeNames {
		var line string
		labelStyle := lipgloss.NewStyle().Bold(true).Foreground(ColorPrimary).Width(16)
		descStyle := lipgloss.NewStyle().Foreground(ColorTextMuted)

		if i == m.PermSelectIndex {
			pointer := lipgloss.NewStyle().Foreground(ColorWarning).Bold(true).Render("▶ ")
			selectedLabel := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ffffff")).Render(entry.Label)
			selectedDesc := lipgloss.NewStyle().Foreground(lipgloss.Color("#A1A1AA")).Render(entry.Desc)
			line = "  " + pointer + selectedLabel + "\n     " + selectedDesc
		} else {
			line = "     " + labelStyle.Render(entry.Label) + "  " + descStyle.Render(entry.Desc)
		}
		sb.WriteString(line + "\n\n")
	}

	sb.WriteString(lipgloss.NewStyle().Foreground(ColorTextMuted).
		Render("  ↑↓ 选择   Enter 确认   Ctrl+C 退出") + "\n")

	return sb.String()
}

// RenderPermissionSelect renders an inline permission selection card (used after /permission command)
func RenderPermissionSelect(currentMode agent.PermissionMode) string {
	var sb strings.Builder
	sb.WriteString(StyleKeyActive.Render("权限模式选择") + "\n\n")

	for i, entry := range permModeNames {
		marker := "  "
		if entry.Mode == currentMode {
			marker = lipgloss.NewStyle().Foreground(ColorWarning).Bold(true).Render("▶ ")
		} else {
			marker = "  "
		}
		sb.WriteString(fmt.Sprintf("%s%s. %s  —  %s\n",
			marker,
			fmt.Sprintf("%d", i+1),
			lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true).Render(entry.Label),
			lipgloss.NewStyle().Foreground(ColorTextMuted).Render(entry.Desc),
		))
	}

	sb.WriteString("\n" + StyleKeyHelp.Render("  ↑↓ 选择   Enter 确认"))

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorPrimary).
		Padding(1, 2).
		MarginTop(1).
		Render(sb.String())
}

// RenderTodoDashboard renders the task checklist — hidden by default when empty
func RenderTodoDashboard() string {
	todoRender := agent.GlobalTodoManager.Render()
	if todoRender == "" {
		return ""
	}

	headerStyle := lipgloss.NewStyle().
		Padding(0, 1).
		MarginTop(1).
		MarginBottom(1)

	return headerStyle.Render("Tasks\n\n"+todoRender) + "\n"
}

// RenderTaskDashboard renders a compact task graph summary
func RenderTaskDashboard() string {
	tasks, err := agent.GlobalTaskManager.ListTasks()
	if err != nil || len(tasks) == 0 {
		return ""
	}

	var completed, inProgress, ready, blocked []string

	badgeCompleted := lipgloss.NewStyle().Foreground(ColorSuccess).Bold(true).Render("✓")
	badgeInProgress := lipgloss.NewStyle().Foreground(ColorWarning).Bold(true).Render("›")
	badgeReady := lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true).Render("·")
	badgeBlocked := lipgloss.NewStyle().Foreground(ColorTextMuted).Bold(true).Render("-")

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
	sb.WriteString(StyleKeyActive.Render("Tasks") + "\n")

	var items []string
	if len(inProgress) > 0 {
		items = append(items, strings.Join(inProgress, "\n"))
	}
	if len(ready) > 0 {
		items = append(items, strings.Join(ready, "\n"))
	}
	if len(blocked) > 0 {
		items = append(items, strings.Join(blocked, "\n"))
	}
	if len(completed) > 0 {
		items = append(items, strings.Join(completed, "\n"))
	}

	sb.WriteString(strings.Join(items, "\n") + "\n")

	var total = len(tasks)
	var done = len(completed)
	progressPct := 0
	if total > 0 {
		progressPct = (done * 100) / total
	}
	sb.WriteString(fmt.Sprintf("\n  %d%% complete  (%d/%d)", progressPct, done, total))

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
		return StyleKeyHelp.Render("  no tasks found")
	}

	var completed, inProgress, ready, blocked []string

	badgeCompleted := lipgloss.NewStyle().Background(ColorSuccess).Foreground(lipgloss.Color("#FFFFFF")).Padding(0, 1).Bold(true).Render("done")
	badgeInProgress := lipgloss.NewStyle().Background(ColorWarning).Foreground(lipgloss.Color("#FFFFFF")).Padding(0, 1).Bold(true).Render("active")
	badgeReady := lipgloss.NewStyle().Background(ColorPrimary).Foreground(lipgloss.Color("#FFFFFF")).Padding(0, 1).Bold(true).Render("ready")
	badgeBlocked := lipgloss.NewStyle().Background(ColorTextMuted).Foreground(lipgloss.Color("#FFFFFF")).Padding(0, 1).Bold(true).Render("blocked")

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
	sb.WriteString(StyleKeyActive.Render("Durable Work Graph") + "\n\n")

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
	sb.WriteString(fmt.Sprintf("  %d%% complete  (%d/%d)", progressPct, done, total))

	cardStyle := lipgloss.NewStyle().
		Padding(1, 2).
		MarginTop(1).
		MarginBottom(1)

	return cardStyle.Render(sb.String()) + "\n"
}

// RenderErrorCard renders a clean error card wrapping unrecoverable execution errors
func RenderErrorCard(err error) string {
	if err == nil {
		return ""
	}

	var sb strings.Builder
	errMsg := err.Error()

	var tips []string
	if strings.Contains(errMsg, "API") || strings.Contains(errMsg, "Authorization") || strings.Contains(errMsg, "ApiKey") || strings.Contains(errMsg, "接口") || strings.Contains(errMsg, "http") || strings.Contains(errMsg, "调用") {
		tips = []string{
			"请检查您的本地网络连接以及 API 终点（Base URL）是否可达",
			"确认您已在 ~/.go-claude.json 或环境变量中配置了正确的 API Key",
			"如果您想进行离线只读操作，可以通过输入 /mode plan 切换为只读规划模式",
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

	sb.WriteString("  " + lipgloss.NewStyle().Foreground(ColorDanger).Bold(true).Render("[error]") + " " + errMsg + "\n\n")
	sb.WriteString("  " + StyleKeyHelp.Render("建议排查:") + "\n")
	for i, tip := range tips {
		sb.WriteString(fmt.Sprintf("    %d. %s\n", i+1, StyleKeyHelp.Render(tip)))
	}

	cardStyle := lipgloss.NewStyle().
		Padding(1, 2).
		MarginTop(1).
		MarginBottom(1)

	return cardStyle.Render(sb.String())
}

// FormatToolArgs extracts and formats key arguments from a tool invocation.
func FormatToolArgs(args any) string {
	if args == nil {
		return ""
	}
	if m, ok := args.(map[string]any); ok {
		if len(m) == 0 {
			return ""
		}
		var parts []string
		for _, key := range []string{"path", "command", "pattern", "query", "text"} {
			if val, exists := m[key]; exists {
				parts = append(parts, fmt.Sprintf("%s: %q", key, val))
			}
		}
		for key, val := range m {
			if key == "path" || key == "command" || key == "pattern" || key == "query" || key == "text" {
				continue
			}
			parts = append(parts, fmt.Sprintf("%s: %v", key, val))
		}
		if len(parts) > 0 {
			return "(" + strings.Join(parts, ", ") + ")"
		}
		return ""
	}

	data, err := json.Marshal(args)
	if err == nil && len(data) > 2 {
		return string(data)
	}
	return fmt.Sprintf("%v", args)
}

// FormatToolActivity converts a tool name and arguments into a clear, elegant Chinese action description
func FormatToolActivity(name string, args any) string {
	var argMap map[string]any
	if m, ok := args.(map[string]any); ok {
		argMap = m
	}

	getStr := func(keys ...string) string {
		if argMap == nil {
			return ""
		}
		for _, k := range keys {
			if val, exists := argMap[k]; exists {
				if str, ok := val.(string); ok {
					return str
				}
				return fmt.Sprintf("%v", val)
			}
		}
		return ""
	}

	switch name {
	case "file_read":
		path := getStr("path", "AbsolutePath", "TargetFile")
		if path != "" {
			return fmt.Sprintf("读取文件 %s", path)
		}
		return "读取文件"
	case "file_write":
		path := getStr("path", "TargetFile", "AbsolutePath")
		if path != "" {
			return fmt.Sprintf("写入文件 %s", path)
		}
		return "写入文件"
	case "grep":
		pattern := getStr("pattern", "query", "Query")
		if pattern != "" {
			return fmt.Sprintf("搜索关键字/正则 %q", pattern)
		}
		return "搜索文件内容"
	case "shell_run":
		cmd := getStr("command", "CommandLine")
		if cmd != "" {
			return fmt.Sprintf("运行终端命令: %s", cmd)
		}
		return "运行终端命令"
	case "todo":
		text := getStr("text", "Text")
		if text != "" {
			return fmt.Sprintf("更新待办事项 %q", text)
		}
		return "更新待办事项"
	case "memory_save":
		nameVal := getStr("name", "Name")
		if nameVal != "" {
			return fmt.Sprintf("保存跨会话记忆 %q", nameVal)
		}
		return "保存跨会话记忆"
	case "memory_list":
		return "获取跨会话记忆列表"
	case "task_create":
		id := getStr("id", "ID", "TaskId")
		if id != "" {
			return fmt.Sprintf("创建工作链任务 %s", id)
		}
		return "创建工作链任务"
	case "task_update":
		id := getStr("id", "ID", "TaskId")
		if id != "" {
			return fmt.Sprintf("更新工作链任务 %s", id)
		}
		return "更新工作链任务"
	case "task_list":
		return "查看工作链任务列表"
	case "task_get":
		id := getStr("id", "ID", "TaskId")
		if id != "" {
			return fmt.Sprintf("获取工作链任务 %s 详情", id)
		}
		return "获取工作链任务详情"
	case "background_run":
		cmd := getStr("command", "CommandLine")
		if cmd != "" {
			return fmt.Sprintf("后台启动终端命令: %s", cmd)
		}
		return "后台启动终端命令"
	case "check_background":
		return "检查后台运行任务"
	case "schedule_create":
		return "创建计划任务/定时器"
	case "schedule_list":
		return "查看活动计划任务列表"
	case "schedule_delete":
		return "删除计划任务/定时器"
	case "spawn_teammate":
		nameVal := getStr("name", "Name")
		if nameVal != "" {
			return fmt.Sprintf("生成子 Agent 协同体 %s", nameVal)
		}
		return "生成子 Agent 协同体"
	case "list_teammates":
		return "检查协同 Agent 团队状态"
	case "send_message":
		recipient := getStr("recipient", "Recipient")
		if recipient != "" {
			return fmt.Sprintf("向 Agent %s 发送消息", recipient)
		}
		return "向协同 Agent 发送消息"
	case "read_inbox":
		return "读取协同 Agent 收件箱"
	case "broadcast":
		return "广播消息给协同 Agent 团队"
	case "worktree_create":
		nameVal := getStr("name", "Name")
		if nameVal != "" {
			return fmt.Sprintf("创建 Git 工作区隔离树 %s", nameVal)
		}
		return "创建 Git 工作区隔离树"
	case "worktree_list":
		return "查看 Git 工作区隔离树列表"
	case "worktree_status":
		return "检查 Git 工作区隔离树状态"
	case "worktree_enter":
		return "切入 Git 工作区隔离区"
	case "worktree_closeout":
		return "关闭/清理 Git 工作区隔离区"
	case "mcp_server_list":
		return "列出已配置的 MCP 服务插件"
	default:
		argsStr := FormatToolArgs(args)
		if argsStr != "" {
			return fmt.Sprintf("调用工具 %s%s", name, argsStr)
		}
		return fmt.Sprintf("调用工具 %s", name)
	}
}

// maxVisibleStreamLines is the maximum number of lines to display in the shell stream area
const maxVisibleStreamLines = 15

// RenderShellStreamArea renders a bordered area showing real-time shell output
func RenderShellStreamArea(lines []string, cmd string, width int) string {
	if len(lines) == 0 {
		return ""
	}

	visibleLines := lines
	truncated := 0
	if len(lines) > maxVisibleStreamLines {
		truncated = len(lines) - maxVisibleStreamLines
		visibleLines = lines[len(lines)-maxVisibleStreamLines:]
	}

	var sb strings.Builder

	// Header with command name
	cmdDisplay := cmd
	if len(cmdDisplay) > width-14 {
		cmdDisplay = cmdDisplay[:width-17] + "..."
	}
	sb.WriteString(lipgloss.NewStyle().Foreground(ColorSecondary).Render(" shell: "))
	sb.WriteString(lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true).Render("$ "+cmdDisplay))
	sb.WriteString("\n")

	if truncated > 0 {
		sb.WriteString(lipgloss.NewStyle().Foreground(ColorTextMuted).Italic(true).
			Render(fmt.Sprintf("  ... (已截断前 %d 行)", truncated)))
		sb.WriteString("\n")
	}

	for _, line := range visibleLines {
		sb.WriteString("  " + line + "\n")
	}

	areaStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorSecondary).
		Padding(0, 1).
		MarginTop(1).
		Width(width - 4)

	return areaStyle.Render(sb.String())
}

// RenderToolErrorCard renders a minimal failure card for tool execution
func RenderToolErrorCard(name string, args any, duration time.Duration, err error) string {
	var sb strings.Builder
	activity := FormatToolActivity(name, args)
	sb.WriteString(fmt.Sprintf("\x1b[1;31m[fail]\x1b[0m %s  %v\n", activity, duration.Round(time.Millisecond)))
	sb.WriteString(fmt.Sprintf("       %s", err.Error()))

	cardStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorDanger).
		Padding(0, 1).
		MarginTop(1).
		MarginBottom(1)

	return cardStyle.Render(sb.String())
}

// RenderToolSuccessCard renders a minimal success log for tool execution
func RenderToolSuccessCard(name string, args any, duration time.Duration) string {
	activity := FormatToolActivity(name, args)
	return fmt.Sprintf("\x1b[32m✓\x1b[0m %s  \x1b[2m%v\x1b[0m", activity, duration.Round(time.Millisecond))
}

// RenderTeamDashboard renders a clean team roster card
func RenderTeamDashboard() string {
	teammates, err := agent.GlobalTeamManager.ListTeammates()
	if err != nil {
		return StyleToolError.Render(fmt.Sprintf("[error] 获取队友列表失败: %v", err))
	}

	var sb strings.Builder
	sb.WriteString(StyleKeyActive.Render("Agent Teams") + "\n\n")

	if len(teammates) == 0 {
		sb.WriteString("  " + StyleKeyHelp.Render("no teammates registered") + "\n")
		sb.WriteString("  " + StyleKeyHelp.Render("use spawn_teammate tool to add one") + "\n")
	} else {
		for _, t := range teammates {
			statusSymbol := lipgloss.NewStyle().Foreground(ColorTextMuted).Render("offline")
			if t.Status == "working" {
				statusSymbol = lipgloss.NewStyle().Foreground(ColorWarning).Bold(true).Render("working")
			} else if t.Status == "idle" {
				statusSymbol = lipgloss.NewStyle().Foreground(ColorSuccess).Bold(true).Render("idle")
			}

			sb.WriteString(fmt.Sprintf("  %s  %s  %s  %s\n",
				StylePrompt.Render(t.Name),
				lipgloss.NewStyle().Foreground(ColorSecondary).Render(t.Role),
				statusSymbol,
				StyleKeyHelp.Render(t.LastActive.Format("15:04:05")),
			))
		}
	}

	cardStyle := lipgloss.NewStyle().
		Padding(1, 2).
		MarginTop(1).
		MarginBottom(1)

	return cardStyle.Render(sb.String()) + "\n"
}

// RenderWorktreeDashboard renders a clean worktree isolation card
func RenderWorktreeDashboard() string {
	worktrees, err := agent.GlobalWorktreeManager.List()
	if err != nil {
		return StyleToolError.Render(fmt.Sprintf("[error] 获取 Worktree 列表失败: %v", err))
	}

	var sb strings.Builder
	sb.WriteString(StyleKeyActive.Render("Git Worktrees") + "\n\n")

	if len(worktrees) == 0 {
		sb.WriteString("  " + StyleKeyHelp.Render("no worktrees registered") + "\n")
		sb.WriteString("  " + StyleKeyHelp.Render("worktrees are created automatically when a task is dispatched") + "\n")
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
				taskInfo = lipgloss.NewStyle().Foreground(ColorSecondary).Render(fmt.Sprintf(" [%s]", w.TaskID))
			}

			sb.WriteString(fmt.Sprintf("  %s  %s%s  %s\n",
				StylePrompt.Render(w.Name),
				lipgloss.NewStyle().Foreground(ColorSecondary).Render(w.Branch),
				taskInfo,
				statusSymbol,
			))
			sb.WriteString(fmt.Sprintf("    %s\n", StyleKeyHelp.Render(w.Path)))
		}
	}

	cardStyle := lipgloss.NewStyle().
		Padding(1, 2).
		MarginTop(1).
		MarginBottom(1)

	return cardStyle.Render(sb.String()) + "\n"
}

// RenderMCPDashboard renders a clean MCP plugin server card
func RenderMCPDashboard() string {
	servers := agent.GlobalMCPRouter.ListServers()

	var sb strings.Builder
	sb.WriteString(StyleKeyActive.Render("MCP Plugins") + "\n\n")

	if len(servers) == 0 {
		sb.WriteString("  " + StyleKeyHelp.Render("no MCP servers configured") + "\n")
		sb.WriteString("  " + StyleKeyHelp.Render("edit .go-claude/plugins.json to add servers") + "\n")
	} else {
		for name, status := range servers {
			statusSymbol := lipgloss.NewStyle().Foreground(ColorDanger).Bold(true).Render("disconnected")
			if status == "connected" {
				statusSymbol = lipgloss.NewStyle().Foreground(ColorSuccess).Bold(true).Render("connected")
			}

			sb.WriteString(fmt.Sprintf("  %s  %s\n",
				StylePrompt.Render(name),
				statusSymbol,
			))
		}

		tools, err := agent.GlobalMCPRouter.DiscoverTools()
		if err == nil && len(tools) > 0 {
			sb.WriteString("\n  " + StyleKeyHelp.Render("available tools:") + "\n")
			for _, t := range tools {
				sb.WriteString(fmt.Sprintf("    %s  %s\n",
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

// RenderBackgroundDashboard renders the background tasks and CI watchers
func RenderBackgroundDashboard() string {
	var sb strings.Builder

	sb.WriteString(StyleKeyActive.Render("Background Tasks") + "\n")
	sb.WriteString(strings.Repeat("─", 60) + "\n")

	watchers := agent.ListActiveCIWatchers()
	if len(watchers) > 0 {
		sb.WriteString(lipgloss.NewStyle().Foreground(ColorWarning).Bold(true).Render("CI Watchers:") + "\n")
		for owner, startTime := range watchers {
			dur := time.Since(startTime).Round(time.Second)
			sb.WriteString(fmt.Sprintf("  %s  uptime: %s\n", StylePrompt.Render(owner), dur))
		}
		sb.WriteString("\n")
	}

	bgStatus, err := agent.GlobalBackgroundManager.Check("")
	if err == nil && bgStatus != "No background tasks." {
		sb.WriteString(lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true).Render("System Tasks:") + "\n")
		lines := strings.Split(bgStatus, "\n")
		for _, line := range lines {
			sb.WriteString("  " + line + "\n")
		}
	} else {
		sb.WriteString(lipgloss.NewStyle().Foreground(ColorTextMuted).Italic(true).Render("  no background tasks running") + "\n")
	}

	cardStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorPrimary).
		Padding(0, 1).
		MarginTop(1).
		MarginBottom(1)

	return cardStyle.Render(sb.String()) + "\n"
}

func getActiveTaskInfo() string {
	tasks, err := agent.GlobalTaskManager.ListTasks()
	if err != nil || len(tasks) == 0 {
		return ""
	}
	for _, t := range tasks {
		if t.Status == "in_progress" {
			return fmt.Sprintf("task: %s", t.ID)
		}
	}
	return ""
}

// RenderStatusBar renders an enhanced status bar with agent activity and token count
func RenderStatusBar(m Model) string {
	modeStr := strings.ToLower(string(agent.GlobalPermissionManager.GetMode()))

	// Left: agent action + duration
	var left string
	if m.CurrentStatusText != "" && (m.State == stateThinking || m.State == stateStreaming) {
		// 优先显示 LLM status 标签文字
		left = fmt.Sprintf("  ⏳ %s", m.CurrentStatusText)
	} else if m.ActiveTool.Running {
		dur := time.Since(m.RoundStartTime).Round(time.Millisecond)
		activity := FormatToolActivity(m.ActiveTool.Name, m.ActiveTool.Args)
		if len(activity) > 40 {
			activity = activity[:37] + "..."
		}
		left = fmt.Sprintf("  ⚙️ %s (%v)", activity, dur)
	} else if m.State == stateThinking || m.State == stateStreaming {
		dur := time.Since(m.RoundStartTime).Round(time.Second)
		left = fmt.Sprintf("  ⏳ 思考中... (%v)", dur)
	} else {
		left = fmt.Sprintf("  mode:%s", modeStr)
	}

	// Right: [mode] + token count
	var tokenStr string
	if m.TotalTokens > 0 {
		if m.TotalTokens >= 1000 {
			tokenStr = fmt.Sprintf("%.1fk", float64(m.TotalTokens)/1000)
		} else {
			tokenStr = fmt.Sprintf("%d", m.TotalTokens)
		}
	} else {
		tokenStr = "-"
	}
	right := fmt.Sprintf("[%s] %s  ", modeStr, tokenStr)

	leftWidth := lipgloss.Width(left)
	rightWidth := lipgloss.Width(right)

	spaces := m.Width - leftWidth - rightWidth
	if spaces < 0 {
		spaces = 0
	}

	barText := left + strings.Repeat(" ", spaces) + right
	return StyleStatusBar.Render(barText)
}
