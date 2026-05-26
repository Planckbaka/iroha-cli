package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"iroha/pkg/agent"

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
func RenderConfirmCard(prompt string, selectedIndex int) string {
	return RenderConfirmCardWithDiff(prompt, selectedIndex, false, false)
}

// RenderConfirmCardWithDiff renders confirmation prompts and appends optional interactive Diff triggers.
func RenderConfirmCardWithDiff(prompt string, selectedIndex int, hasDiff bool, diffActive bool) string {
	var sb strings.Builder

	// Header
	sb.WriteString(lipgloss.NewStyle().
		Foreground(ColorWarning).Bold(true).
		Render("Authorization Required") + "\n\n")

	// Prompt content
	sb.WriteString(prompt)
	sb.WriteString("\n\n")

	// Key hints
	yStyle := lipgloss.NewStyle().Foreground(ColorSuccess).Bold(true).Padding(0, 1).Border(lipgloss.RoundedBorder()).BorderForeground(ColorSuccess)
	nStyle := lipgloss.NewStyle().Foreground(ColorDanger).Bold(true).Padding(0, 1).Border(lipgloss.RoundedBorder()).BorderForeground(ColorDanger)
	aStyle := lipgloss.NewStyle().Foreground(ColorWarning).Bold(true).Padding(0, 1).Border(lipgloss.RoundedBorder()).BorderForeground(ColorWarning)
	eStyle := lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true).Padding(0, 1).Border(lipgloss.RoundedBorder()).BorderForeground(ColorPrimary)
	qStyle := lipgloss.NewStyle().Foreground(ColorSecondary).Bold(true).Padding(0, 1).Border(lipgloss.RoundedBorder()).BorderForeground(ColorSecondary)

	if selectedIndex == 0 {
		yStyle = yStyle.Background(ColorSuccess).Foreground(lipgloss.Color("#18181B"))
	} else if selectedIndex == 1 {
		nStyle = nStyle.Background(ColorDanger).Foreground(lipgloss.Color("#18181B"))
	} else if selectedIndex == 2 {
		aStyle = aStyle.Background(ColorWarning).Foreground(lipgloss.Color("#18181B"))
	} else if selectedIndex == 3 {
		eStyle = eStyle.Background(ColorPrimary).Foreground(lipgloss.Color("#18181B"))
	} else if selectedIndex == 4 {
		qStyle = qStyle.Background(ColorSecondary).Foreground(lipgloss.Color("#18181B"))
	}

	sb.WriteString("  ")
	sb.WriteString(yStyle.Render("Y Allow"))
	sb.WriteString("  ")
	sb.WriteString(nStyle.Render("N Deny"))
	sb.WriteString("  ")
	sb.WriteString(aStyle.Render("A Always Allow"))
	sb.WriteString("  ")
	sb.WriteString(eStyle.Render("E Edit"))
	sb.WriteString("  ")
	sb.WriteString(qStyle.Render("? Explain"))

	sb.WriteString("\n\n")

	hints := "← → / Tab Select   Enter Confirm   Shortcuts: Y / N / A / E / ?"
	if hasDiff {
		if diffActive {
			hints += "   [D] Hide Diff"
		} else {
			hints += "   [D] Show Diff"
		}
	}
	sb.WriteString("  " + lipgloss.NewStyle().Foreground(ColorTextMuted).Italic(true).Render(hints))

	borderColor := ColorWarning
	if strings.Contains(prompt, "[file_write]") || strings.Contains(prompt, "[file_read]") {
		borderColor = ColorSecondary
	} else if strings.Contains(prompt, "[mcp]") {
		borderColor = ColorPrimary
	}

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.ThickBorder()).
		BorderForeground(borderColor).
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

	// Cyber-Holographic IROHA ASCII Logo
	cyan := lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true).Render
	pink := lipgloss.NewStyle().Foreground(ColorSecondary).Bold(true).Render

	sb.WriteString(cyan("   ___   ____     ___    _   _    _    ") + "\n")
	sb.WriteString(cyan("  |_ _| |  _ \\   / _ \\  | | | |  / \\   ") + "\n")
	sb.WriteString(pink("   | |  | |_) | | | | | | |_| | / _ \\  ") + "\n")
	sb.WriteString(pink("   | |  |  _ <  | |_| | |  _  |/ ___ \\ ") + "\n")
	sb.WriteString(pink("  |___| |_| \\_\\  \\___/  |_| |_/_/   \\_\\") + "\n\n")

	// Energetic part-time student girl welcoming msg
	welcomeMsg := pink("[Iroha] ") + lipgloss.NewStyle().Foreground(lipgloss.Color("#E2E8F0")).Render("Phew, just finished my shift! Let's write some code together, shall we?")
	sb.WriteString("  " + welcomeMsg + "\n\n")

	sb.WriteString("  " + StyleKeyHelp.Render("brand  ") + StylePrompt.Render("iroha code") + "  " + StyleKeyHelp.Render("v1.3.0") + "\n")
	sb.WriteString("  " + StyleKeyHelp.Render("model  ") + StylePrompt.Render(modelName) + "\n")
	sb.WriteString("  " + StyleKeyHelp.Render("mode   ") + StylePrompt.Render(modeStr) + "\n\n")
	sb.WriteString("  " + StyleKeyHelp.Render("Type / to see all commands   Up/Down - History   /exit - Quit") + "\n")

	return StyleWelcome.Render(sb.String())
}

// RenderSlashMenu renders the slash command popup above the textarea
func RenderSlashMenu(items []SlashMenuItem, selectedIndex int, width int) string {
	maxItems := 8
	if len(items) < maxItems {
		maxItems = len(items)
	}

	// Calculate scroll offset so selected item is always visible
	startIdx := 0
	if selectedIndex >= maxItems {
		startIdx = selectedIndex - maxItems + 1
	}
	if startIdx+maxItems > len(items) {
		startIdx = len(items) - maxItems
	}
	if startIdx < 0 {
		startIdx = 0
	}

	var sb strings.Builder
	for i := startIdx; i < startIdx+maxItems; i++ {
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
		sb.WriteString("  " + StyleKeyHelp.Render(fmt.Sprintf("... %d more commands", len(items)-8)) + "\n")
	}

	footer := StyleKeyHelp.Render("  Up/Down select   Tab complete   Enter execute   Esc close")
	sb.WriteString(footer)

	menuStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorPrimary).
		Padding(0, 0)

	return menuStyle.Render(sb.String())
}

var permModeNames = []struct {
	Mode  agent.PermissionMode
	Label string
	Desc  string
	Icon  string
}{
	{agent.ModePlan, "Plan Mode", "Read-only mode - blocks all write operations and Shell commands", ""},
	{agent.ModeDefault, "Default Mode", "Every sensitive operation requires manual user approval (recommended)", ""},
	{agent.ModeAcceptEdits, "AcceptEdits Mode", "File edits auto-approved, shell commands require authorization", ""},
	{agent.ModeAuto, "Auto Mode", "Read and low-risk operations auto-approved, write operations still require approval", ""},
	{agent.ModeBypass, "Bypass Mode", "YOLO mode - skips all confirmation prompts (dangerous)", ""},
}

// RenderPermissionSelectScreen renders the full-screen startup permission selection
func RenderPermissionSelectScreen(m Model) string {
	var sb strings.Builder

	sb.WriteString("\n\n")
	sb.WriteString(lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true).
		Render("  Select Agent Permission Mode") + "\n\n")
	sb.WriteString(lipgloss.NewStyle().Foreground(ColorTextMuted).
		Render("  This setting controls the security level for Agent tool execution") + "\n\n")

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
		Render("  Up/Down select   Enter confirm   Ctrl+C exit") + "\n")

	return sb.String()
}

// RenderSessionSelectScreen renders the interactive sessions picker screen.
func RenderSessionSelectScreen(m Model) string {
	var sb strings.Builder

	sb.WriteString("\n\n")
	sb.WriteString(lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true).
		Render("  Iroha Code - Session History Manager") + "\n\n")
	sb.WriteString(lipgloss.NewStyle().Foreground(ColorTextMuted).
		Render("  Select a session to resume, or start a new session:") + "\n\n")

	// Render virtual "[Start New Session]" entry
	var line string
	if m.SessionListIndex == 0 {
		pointer := lipgloss.NewStyle().Foreground(ColorWarning).Bold(true).Render("▶ ")
		label := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ffffff")).Render("[ Start New Session ]")
		desc := lipgloss.NewStyle().Foreground(lipgloss.Color("#A1A1AA")).Render("Start a fresh session with no history.")
		line = "  " + pointer + label + "\n     " + desc
	} else {
		label := lipgloss.NewStyle().Bold(true).Foreground(ColorPrimary).Render("[ Start New Session ]")
		desc := lipgloss.NewStyle().Foreground(ColorTextMuted).Render("Start a fresh session with no history.")
		line = "     " + label + "  " + desc
	}
	sb.WriteString(line + "\n\n")

	// Render historical sessions
	for i, sess := range m.SessionsList {
		var line string
		isActive := sess.ID == m.SessionID
		activeTag := ""
		if isActive {
			activeTag = lipgloss.NewStyle().Foreground(ColorSuccess).Bold(true).Render(" (active)")
		}

		timeStr := sess.LastUpdateTime.Format("2006-01-02 15:04:05")

		tokensStr := "-"
		costStr := "-"
		if sess.TotalTokens > 0 {
			if sess.TotalTokens >= 1000 {
				tokensStr = fmt.Sprintf("%.1fk", float64(sess.TotalTokens)/1000)
			} else {
				tokensStr = fmt.Sprintf("%d", sess.TotalTokens)
			}
			if sess.TotalCost > 0 {
				if sess.TotalCost < 0.01 {
					costStr = fmt.Sprintf("$%.4f", sess.TotalCost)
				} else {
					costStr = fmt.Sprintf("$%.2f", sess.TotalCost)
				}
			}
		}

		statsStr := ""
		if tokensStr != "-" {
			if costStr != "-" {
				statsStr = fmt.Sprintf(" (Tokens: %s, Cost: %s)", tokensStr, costStr)
			} else {
				statsStr = fmt.Sprintf(" (Tokens: %s)", tokensStr)
			}
		}

		if i+1 == m.SessionListIndex {
			pointer := lipgloss.NewStyle().Foreground(ColorWarning).Bold(true).Render("▶ ")
			label := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ffffff")).Render(sess.FirstPrompt)
			desc := lipgloss.NewStyle().Foreground(lipgloss.Color("#A1A1AA")).Render(
				fmt.Sprintf("ID: %s  Updated: %s  Path: %s%s%s", sess.ID, timeStr, sess.CWD, activeTag, statsStr))
			line = "  " + pointer + label + "\n     " + desc
		} else {
			labelStyle := lipgloss.NewStyle().Bold(true).Foreground(ColorPrimary)
			if isActive {
				labelStyle = labelStyle.Foreground(ColorSuccess)
			}
			label := labelStyle.Render(sess.FirstPrompt)
			desc := lipgloss.NewStyle().Foreground(ColorTextMuted).Render(
				fmt.Sprintf("Updated: %s  Path: %s%s%s", timeStr, sess.CWD, activeTag, statsStr))
			line = "     " + label + "\n     " + desc
		}
		sb.WriteString(line + "\n\n")
	}

	sb.WriteString(lipgloss.NewStyle().Foreground(ColorTextMuted).
		Render("  Up/Down select   Enter confirm   Esc back   Ctrl+C exit") + "\n")

	return sb.String()
}

// RenderPermissionSelect renders an inline permission selection card (used after /permission command)
func RenderPermissionSelect(currentMode agent.PermissionMode) string {
	var sb strings.Builder
	sb.WriteString(StyleKeyActive.Render("Permission Mode Select") + "\n\n")

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

	sb.WriteString("\n" + StyleKeyHelp.Render("  Up/Down select   Enter confirm"))

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorPrimary).
		Padding(1, 2).
		MarginTop(1).
		Render(sb.String())
}

// RenderTodoDashboard renders the task checklist - hidden by default when empty
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
	if strings.Contains(errMsg, "API") || strings.Contains(errMsg, "Authorization") || strings.Contains(errMsg, "ApiKey") || strings.Contains(errMsg, "接口") || strings.Contains(errMsg, "http") || strings.Contains(errMsg, "调用") || strings.Contains(errMsg, "call") {
		tips = []string{
			"Please check your local network connection and whether the API endpoint (Base URL) is reachable",
			"Confirm you have configured the correct API Key in ~/.iroha.json or environment variables",
			"If you want offline read-only operations, switch to Plan mode by typing /mode plan",
		}
	} else if strings.Contains(errMsg, "权限") || strings.Contains(errMsg, "Permission") || strings.Contains(errMsg, "denied") {
		tips = []string{
			"Please check your system read/write permissions for the target directory or file",
			"Try to keep code changes and test commands within the current workspace directory",
		}
	} else {
		tips = []string{
			"Check if your command-line tools or local Go environment are configured correctly",
			"You can re-enter the command or try different parameters",
		}
	}

	sb.WriteString("  " + lipgloss.NewStyle().Foreground(ColorDanger).Bold(true).Render("[error]") + " " + errMsg + "\n\n")
	sb.WriteString("  " + StyleKeyHelp.Render("Troubleshooting:") + "\n")
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

// FormatToolActivity converts a tool name and arguments into a clear action description
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
			return fmt.Sprintf("Read file %s", path)
		}
		return "Read file"
	case "file_write":
		path := getStr("path", "TargetFile", "AbsolutePath")
		if path != "" {
			return fmt.Sprintf("Write file %s", path)
		}
		return "Write file"
	case "grep":
		pattern := getStr("pattern", "query", "Query")
		if pattern != "" {
			return fmt.Sprintf("Search pattern/regex %q", pattern)
		}
		return "Search file contents"
	case "shell_run":
		cmd := getStr("command", "CommandLine")
		if cmd != "" {
			return fmt.Sprintf("Run terminal command: %s", cmd)
		}
		return "Run terminal command"
	case "todo":
		text := getStr("text", "Text")
		if text != "" {
			return fmt.Sprintf("Update todo %q", text)
		}
		return "Update todo"
	case "memory_save":
		nameVal := getStr("name", "Name")
		if nameVal != "" {
			return fmt.Sprintf("Save cross-session memory %q", nameVal)
		}
		return "Save cross-session memory"
	case "memory_list":
		return "List cross-session memories"
	case "task_create":
		id := getStr("id", "ID", "TaskId")
		if id != "" {
			return fmt.Sprintf("Create task %s", id)
		}
		return "Create task"
	case "task_update":
		id := getStr("id", "ID", "TaskId")
		if id != "" {
			return fmt.Sprintf("Update task %s", id)
		}
		return "Update task"
	case "task_list":
		return "List tasks"
	case "task_get":
		id := getStr("id", "ID", "TaskId")
		if id != "" {
			return fmt.Sprintf("Get task %s details", id)
		}
		return "Get task details"
	case "background_run":
		cmd := getStr("command", "CommandLine")
		if cmd != "" {
			return fmt.Sprintf("Run background command: %s", cmd)
		}
		return "Run background command"
	case "check_background":
		return "Check background tasks"
	case "schedule_create":
		return "Create scheduled task"
	case "schedule_list":
		return "List scheduled tasks"
	case "schedule_delete":
		return "Delete scheduled task"
	case "spawn_teammate":
		nameVal := getStr("name", "Name")
		if nameVal != "" {
			return fmt.Sprintf("Spawn agent teammate %s", nameVal)
		}
		return "Spawn agent teammate"
	case "list_teammates":
		return "Check agent team status"
	case "send_message":
		recipient := getStr("recipient", "Recipient")
		if recipient != "" {
			return fmt.Sprintf("Send message to agent %s", recipient)
		}
		return "Send message to agent team"
	case "read_inbox":
		return "Read agent inbox"
	case "broadcast":
		return "Broadcast to agent team"
	case "worktree_create":
		nameVal := getStr("name", "Name")
		if nameVal != "" {
			return fmt.Sprintf("Create git worktree %s", nameVal)
		}
		return "Create git worktree"
	case "worktree_list":
		return "List git worktrees"
	case "worktree_status":
		return "Check git worktree status"
	case "worktree_enter":
		return "Enter git worktree"
	case "worktree_closeout":
		return "Close/clean up git worktree"
	case "mcp_server_list":
		return "List configured MCP servers"
	default:
		argsStr := FormatToolArgs(args)
		if argsStr != "" {
			return fmt.Sprintf("Call tool %s%s", name, argsStr)
		}
		return fmt.Sprintf("Call tool %s", name)
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
	sb.WriteString(lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true).Render("$ " + cmdDisplay))
	sb.WriteString("\n")

	if truncated > 0 {
		sb.WriteString(lipgloss.NewStyle().Foreground(ColorTextMuted).Italic(true).
			Render(fmt.Sprintf("  ... (truncated %d earlier lines)", truncated)))
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
	if err != nil {
		sb.WriteString(fmt.Sprintf("       %s", err.Error()))
	} else {
		sb.WriteString("       operation failed")
	}

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
		return StyleToolError.Render(fmt.Sprintf("[error] Failed to list teammates: %v", err))
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
		return StyleToolError.Render(fmt.Sprintf("[error] Failed to list worktrees: %v", err))
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
		sb.WriteString("  " + StyleKeyHelp.Render("edit .iroha/plugins.json to add servers") + "\n")
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

// RenderStatusBar renders an enhanced status bar with agent activity and token count
func RenderStatusBar(m Model) string {
	modeStr := strings.ToLower(string(agent.GlobalPermissionManager.GetMode()))

	// Left: agent action + duration
	var left string
	if m.CurrentStatusText != "" && (m.State == stateThinking || m.State == stateStreaming) {
		// Prefer displaying LLM status tag text
		left = fmt.Sprintf("  [thinking] %s", m.CurrentStatusText)
	} else if m.ActiveTool.Running {
		dur := time.Since(m.RoundStartTime).Round(time.Millisecond)
		activity := FormatToolActivity(m.ActiveTool.Name, m.ActiveTool.Args)
		if len(activity) > 40 {
			activity = activity[:37] + "..."
		}
		left = fmt.Sprintf("  [tool] %s (%v)", activity, dur)
	} else if m.State == stateThinking || m.State == stateStreaming {
		dur := time.Since(m.RoundStartTime).Round(time.Second)
		left = fmt.Sprintf("  [thinking] thinking... (%v)", dur)
	} else {
		left = fmt.Sprintf("  mode:%s", modeStr)
	}

	// Right: [mode] + token count + cost
	var tokenStr string
	if m.TotalTokens > 0 {
		var tokPart string
		if m.TotalTokens >= 1000 {
			tokPart = fmt.Sprintf("%.1fk", float64(m.TotalTokens)/1000)
		} else {
			tokPart = fmt.Sprintf("%d", m.TotalTokens)
		}
		if m.TotalSessionCost > 0 {
			var costPart string
			if m.TotalSessionCost < 0.01 {
				costPart = fmt.Sprintf("$%.4f", m.TotalSessionCost)
			} else {
				costPart = fmt.Sprintf("$%.2f", m.TotalSessionCost)
			}
			tokenStr = fmt.Sprintf("%s (%s)", tokPart, costPart)
		} else {
			tokenStr = tokPart
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

// RenderPathCompletionBar renders the bottom path auto-completion suggestion line.
func RenderPathCompletionBar(items []string, selectedIndex int, width int) string {
	if len(items) == 0 {
		return ""
	}

	styleActive := lipgloss.NewStyle().Foreground(ColorWarning).Bold(true)
	styleNormal := lipgloss.NewStyle().Foreground(ColorTextMuted)
	stylePrefix := lipgloss.NewStyle().Foreground(ColorTextMuted).Italic(true)

	var builder strings.Builder
	builder.WriteString(stylePrefix.Render("  Candidates: "))

	var itemStrings []string
	for i, item := range items {
		if i == selectedIndex {
			itemStrings = append(itemStrings, styleActive.Render("▸ "+item))
		} else {
			itemStrings = append(itemStrings, styleNormal.Render(item))
		}
	}

	// Dynamic truncation to prevent terminal line folding
	candidatesStr := strings.Join(itemStrings, "   ")
	totalLen := lipgloss.Width(stylePrefix.Render("  Candidates: ")) + lipgloss.Width(candidatesStr)

	if totalLen > width && width > 20 {
		limit := width
		currentLen := lipgloss.Width(stylePrefix.Render("  Candidates: "))
		var truncated []string

		for i, itemStr := range itemStrings {
			w := lipgloss.Width(itemStr)
			if currentLen+w > limit {
				if i > 0 {
					truncated = append(truncated, styleNormal.Render("..."))
				}
				break
			}
			truncated = append(truncated, itemStr)
			currentLen += w + 3 // accounts for spacing "   "
		}
		if len(truncated) > 0 {
			candidatesStr = strings.Join(truncated, "   ")
		}
	}

	builder.WriteString(candidatesStr)
	return builder.String()
}

// RenderCancelCard renders a premium cancellation card when an operation is aborted
func RenderCancelCard(duration time.Duration) string {
	var sb strings.Builder
	sb.WriteString("⚠️  " + lipgloss.NewStyle().Foreground(ColorDanger).Bold(true).Render("Session aborted by user (Generation Aborted)") + "\n\n")
	sb.WriteString(lipgloss.NewStyle().Foreground(ColorTextMuted).Render(fmt.Sprintf("    • Run duration   :  %s\n", duration.Round(time.Millisecond))))
	sb.WriteString(lipgloss.NewStyle().Foreground(ColorTextMuted).Render("    • Interrupted at :  " + time.Now().Format("15:04:05") + "\n"))

	cardStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorDanger).
		Padding(0, 1).
		MarginTop(1).
		MarginBottom(1)

	return cardStyle.Render(sb.String()) + "\n"
}

// RenderHelpDashboard renders a gorgeous cheat sheet overlay for keyboard shortcuts and commands
func RenderHelpDashboard() string {
	var sb strings.Builder

	sb.WriteString("\n" + lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true).Render("💡 Iroha Code — Developer Guide & Command Reference") + "\n")
	sb.WriteString(lipgloss.NewStyle().Foreground(ColorTextMuted).Render("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━") + "\n\n")

	// Keyboard Shortcuts section
	sb.WriteString(lipgloss.NewStyle().Foreground(ColorSecondary).Bold(true).Render(" ⌨️  Keyboard Shortcuts") + "\n")

	shortcuts := []struct {
		Keys string
		Desc string
	}{
		{"Ctrl + C", "Abort current thinking and tool calls, or exit idle state"},
		{"Ctrl + Y", "Copy last AI response to system clipboard"},
		{"Ctrl + D / /exit", "Safely save and exit current session"},
		{"PageUp / PageDown", "Scroll up/down half a page in the viewport"},
		{"Esc", "Exit session history picker or close slash command autocomplete"},
		{"↑ / ↓ (empty input)", "Browse or cycle through prompt history"},
		{" / + command (e.g. /doc)", "Trigger autocomplete, press Tab or Enter to select"},
	}

	for _, s := range shortcuts {
		sb.WriteString(fmt.Sprintf("    %-18s : %s\n",
			lipgloss.NewStyle().Foreground(ColorWarning).Bold(true).Render(s.Keys),
			lipgloss.NewStyle().Foreground(ColorTextMuted).Render(s.Desc)))
	}
	sb.WriteString("\n")

	// Slash Commands section
	sb.WriteString(lipgloss.NewStyle().Foreground(ColorSecondary).Bold(true).Render(" 🚀 Slash Commands") + "\n")
	for _, cmd := range AllSlashCommands {
		sb.WriteString(fmt.Sprintf("    %-18s : %s\n",
			lipgloss.NewStyle().Foreground(ColorSuccess).Bold(true).Render(cmd.Command),
			lipgloss.NewStyle().Foreground(ColorTextMuted).Render(cmd.Description)))
	}

	sb.WriteString("\n" + lipgloss.NewStyle().Foreground(ColorSuccess).Bold(true).Render(" 🎉 Type a prompt to guide the Agent! Type /sessions to switch history, /doctor to diagnose environment.") + "\n")
	sb.WriteString(lipgloss.NewStyle().Foreground(ColorTextMuted).Render("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━") + "\n")

	cardStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorPrimary).
		Padding(0, 1).
		MarginTop(1).
		MarginBottom(1)

	return cardStyle.Render(sb.String()) + "\n"
}
