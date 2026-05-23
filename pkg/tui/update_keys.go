package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"iroha/pkg/agent"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/google/uuid"
)

// handleKeyMsg processes key press events depending on TUI state
func (m Model) handleKeyMsg(msg tea.KeyMsg) (Model, tea.Cmd) {
	var cmd tea.Cmd

	// Log structural or action keypresses to avoid overloading the log
	isStructural := false
	switch msg.Type {
	case tea.KeyCtrlC, tea.KeyEnter, tea.KeyEscape, tea.KeyTab, tea.KeyUp, tea.KeyDown, tea.KeyLeft, tea.KeyRight:
		isStructural = true
	}
	if m.State == stateConfirming && (msg.String() == "y" || msg.String() == "n" || msg.String() == "a") {
		isStructural = true
	}
	if isStructural {
		agent.LogInfo(agent.CatTUI, "key_press", fmt.Sprintf("User pressed structural key: %s (State: %s)", msg.String(), m.State.String()), map[string]any{
			"key":        msg.String(),
			"state":      m.State.String(),
			"session_id": m.SessionID,
		})
	}

	if msg.Type == tea.KeyCtrlC {
		if m.State == statePermissionSelect || m.State == stateSessionSelect {
			return m, tea.Quit
		}
		if m.State != statePrompt {
			// Cancel current agent execution
			m.Cancel()
			elapsed := time.Duration(0)
			if !m.RoundStartTime.IsZero() {
				elapsed = time.Since(m.RoundStartTime)
			}
			m.StreamedText += "\n" + RenderCancelCard(elapsed)
			cmd = m.finalizeTurn()
			return m, cmd
		}
		return m, tea.Quit
	}

	// Handle permission select state FIRST
	if m.State == statePermissionSelect {
		permModes := []agent.PermissionMode{agent.ModePlan, agent.ModeDefault, agent.ModeAuto}
		switch msg.Type {
		case tea.KeyUp:
			if m.PermSelectIndex > 0 {
				m.PermSelectIndex--
			}
			return m, nil
		case tea.KeyDown:
			if m.PermSelectIndex < len(permModes)-1 {
				m.PermSelectIndex++
			}
			return m, nil
		case tea.KeyEnter:
			_ = agent.GlobalPermissionManager.SetMode(permModes[m.PermSelectIndex])
			if m.StartInSessionPicker {
				m.PrevState = statePrompt
				m = m.transitionTo(stateSessionSelect)
				m.loadSessionsList()
			} else {
				m = m.transitionTo(statePrompt)
			}
			m.Viewport.SetContent(m.renderViewportContent())
			return m, nil
		case tea.KeyCtrlC:
			return m, tea.Quit
		}
		return m, nil
	}

	// Handle session selection state
	if m.State == stateSessionSelect {
		switch msg.Type {
		case tea.KeyUp:
			if m.SessionListIndex > 0 {
				m.SessionListIndex--
			}
			return m, nil
		case tea.KeyDown:
			if m.SessionListIndex < len(m.SessionsList) {
				m.SessionListIndex++
			}
			return m, nil
		case tea.KeyEscape:
			m = m.transitionTo(m.PrevState)
			m.Viewport.SetContent(m.renderViewportContent())
			return m, nil
		case tea.KeyEnter:
			if m.SessionListIndex == 0 {
				// Start New Session
				newID := uuid.New().String()
				m.SessionID = newID
				m.History = nil
				m.TotalTokens = 0
			} else {
				// Switch to selected session
				sel := m.SessionsList[m.SessionListIndex-1]
				m.SessionID = sel.ID
				m.LoadHistoryFromSession(sel.ID)
			}
			m = m.transitionTo(statePrompt)
			m.Viewport.SetContent(m.renderViewportContent())
			m.Viewport.GotoBottom()
			return m, nil
		case tea.KeyCtrlC:
			return m, tea.Quit
		}
		return m, nil
	}

	// Handle confirmation state FIRST — before any TextArea processing
	if m.State == stateConfirming {
		keyStr := strings.ToLower(msg.String())
		switch msg.Type {
		case tea.KeyLeft:
			m.ConfirmSelectIndex = (m.ConfirmSelectIndex - 1 + 3) % 3
			m.Viewport.SetContent(m.renderViewportContent())
			m.Viewport.GotoBottom()
			return m, nil
		case tea.KeyRight:
			m.ConfirmSelectIndex = (m.ConfirmSelectIndex + 1) % 3
			m.Viewport.SetContent(m.renderViewportContent())
			m.Viewport.GotoBottom()
			return m, nil
		case tea.KeyTab:
			m.ConfirmSelectIndex = (m.ConfirmSelectIndex + 1) % 3
			m.Viewport.SetContent(m.renderViewportContent())
			m.Viewport.GotoBottom()
			return m, nil
		case tea.KeyShiftTab:
			m.ConfirmSelectIndex = (m.ConfirmSelectIndex - 1 + 3) % 3
			m.Viewport.SetContent(m.renderViewportContent())
			m.Viewport.GotoBottom()
			return m, nil
		case tea.KeyEnter:
			m = m.transitionTo(stateThinking)
			var resp string
			switch m.ConfirmSelectIndex {
			case 0:
				resp = "y"
			case 1:
				resp = "n"
			case 2:
				resp = "always"
			}
			agent.Bridge.ResponseChan <- resp
			m.ConfirmationListenerActive = true
			return m, m.listenToConfirmationBridge()
		}

		switch keyStr {
		case "d":
			if m.ConfirmDiffText != "" {
				m.ConfirmDiffActive = !m.ConfirmDiffActive
				m.Viewport.SetContent(m.renderViewportContent())
				if m.ConfirmDiffActive {
					m.Viewport.GotoTop()
				} else {
					m.Viewport.GotoBottom()
				}
				return m, nil
			}
		case "y":
			m = m.transitionTo(stateThinking)
			agent.Bridge.ResponseChan <- "y"
			m.ConfirmationListenerActive = true
			return m, m.listenToConfirmationBridge()
		case "n", "esc":
			m = m.transitionTo(stateThinking)
			agent.Bridge.ResponseChan <- "n"
			m.ConfirmationListenerActive = true
			return m, m.listenToConfirmationBridge()
		case "a":
			m = m.transitionTo(stateThinking)
			agent.Bridge.ResponseChan <- "always"
			m.ConfirmationListenerActive = true
			return m, m.listenToConfirmationBridge()
		case "shift+tab":
			m.ConfirmSelectIndex = (m.ConfirmSelectIndex - 1 + 3) % 3
			m.Viewport.SetContent(m.renderViewportContent())
			m.Viewport.GotoBottom()
			return m, nil
		default:
			return m, nil
		}
	}

	switch msg.Type {

	case tea.KeyPgUp:
		m.Viewport.HalfPageUp()
		return m, nil

	case tea.KeyPgDown:
		m.Viewport.HalfPageDown()
		return m, nil

	case tea.KeyUp:
		if m.State == statePrompt && m.SlashMenuActive {
			if m.SlashMenuIndex > 0 {
				m.SlashMenuIndex--
			}
			return m, nil
		}
		if m.State == statePrompt {
			m.TextArea.SetValue(m.HistoryManager.Up())
			return m, nil
		}

	case tea.KeyDown:
		if m.State == statePrompt && m.SlashMenuActive {
			if m.SlashMenuIndex < len(m.SlashMenuItems)-1 {
				m.SlashMenuIndex++
			}
			return m, nil
		}
		if m.State == statePrompt {
			m.TextArea.SetValue(m.HistoryManager.Down())
			return m, nil
		}

	case tea.KeyTab:
		if m.State == statePrompt {
			if m.SlashMenuActive && len(m.SlashMenuItems) > 0 {
				selected := m.SlashMenuItems[m.SlashMenuIndex]
				m.TextArea.SetValue(selected.Command + " ")
				m.SlashMenuActive = false
				m.SlashMenuItems = nil
				m.resetPathCompletion()
				return m, nil
			}

			// Handle path auto-completion cycling
			if m.PathCompletionActive && len(m.PathCompletionItems) > 0 {
				m.PathCompletionIndex = (m.PathCompletionIndex + 1) % len(m.PathCompletionItems)
				matched := m.PathCompletionItems[m.PathCompletionIndex]
				m.TextArea.SetValue(m.PathCompletionRest + matched)
				m.TextArea.SetCursor(len(m.PathCompletionRest) + len(matched))
				return m, nil
			}

			// Perform initial path scanning
			val := m.TextArea.Value()
			var prefix, rest string
			lastSpace := strings.LastIndex(val, " ")
			if lastSpace == -1 {
				prefix = val
				rest = ""
			} else {
				prefix = val[lastSpace+1:]
				rest = val[:lastSpace+1]
			}

			matches := m.matchLocalPaths(prefix)
			if len(matches) > 0 {
				m.PathCompletionActive = true
				m.PathCompletionItems = matches
				m.PathCompletionIndex = 0
				m.PathCompletionOriginal = prefix
				m.PathCompletionRest = rest

				m.TextArea.SetValue(rest + matches[0])
				m.TextArea.SetCursor(len(rest) + len(matches[0]))
				return m, nil
			}
		}

	case tea.KeyEscape:
		if m.State == statePrompt && m.SlashMenuActive {
			m.SlashMenuActive = false
			m.SlashMenuItems = nil
			return m, nil
		}

	case tea.KeyEnter:
		if m.State == statePrompt {
			// If slash menu is active and user presses Enter, execute selected command
			if m.SlashMenuActive && len(m.SlashMenuItems) > 0 {
				selected := m.SlashMenuItems[m.SlashMenuIndex]
				m.TextArea.SetValue(selected.Command)
				m.SlashMenuActive = false
				m.SlashMenuItems = nil
				// Fall through to execute the command
			}

			inputVal := strings.TrimSpace(m.TextArea.Value())
			if inputVal == "" {
				return m, nil
			}

			// Intercept Slash commands
			if strings.HasPrefix(inputVal, "/") {
				newM, slashCmd, handled := m.handleSlashCommand(inputVal)
				if handled {
					return newM, slashCmd
				}
			}

			// Prepare for the turn
			m.CurrentPrompt = inputVal
			m.StreamedText = ""
			m = m.transitionTo(stateThinking)
			m.TextArea.SetValue("")
			m.TextArea.SetHeight(2)

			// Phase 2 round tracking
			m.RoundCount++
			m.RoundStartTime = time.Now()
			m.ActiveTool = agent.ToolStatus{}

			// Start background Agent Execution
			ctx, cancel := context.WithCancel(context.Background())
			m.Ctx = ctx
			m.Cancel = cancel

			// Trigger execution with our registered closures
			m.Runner.Execute(m.Ctx, "user-dev", m.SessionID, m.CurrentPrompt,
				m.OnEvent, m.OnError, m.OnDone,
			)

			return m, m.Spinner.Tick
		}
	}

	return m, nil
}

// handleSlashCommand processes commands starting with '/' and returns (updatedModel, command, handled)
func (m Model) handleSlashCommand(inputVal string) (Model, tea.Cmd, bool) {
	parts := strings.Fields(inputVal)
	cmdName := parts[0]

	if cmdName == "/exit" || cmdName == "/quit" {
		return m, tea.Quit, true
	}

	if cmdName == "/mode" {
		m.HistoryManager.Add(inputVal)
		userLog := StyleUserMsg.Render("> " + inputVal)
		m.TextArea.SetValue("")
		m.TextArea.SetHeight(2)

		warningMsg := lipgloss.NewStyle().Foreground(ColorWarning).Render("[已弃用] 建议使用统一的 /permission 命令。")
		var replyLog string

		if len(parts) < 2 {
			replyLog = warningMsg + "\n" + StyleToolError.Render("[error] 请指定权限等级: /permission <plan | auto | default>")
		} else {
			modeArg := agent.PermissionMode(strings.ToLower(parts[1]))
			err := agent.GlobalPermissionManager.SetMode(modeArg)
			if err != nil {
				replyLog = warningMsg + "\n" + StyleToolError.Render(fmt.Sprintf("[error] 无效的权限等级: %s。可选模式: default, plan, auto", parts[1]))
			} else {
				var desc string
				switch modeArg {
				case agent.ModePlan:
					desc = "(只读模式，拦截所有写操作)"
				case agent.ModeAuto:
					desc = "(读操作自动同意，写操作仍需授权)"
				default:
					desc = "(每次非匹配规则的敏感操作均需授权)"
				}
				replyLog = warningMsg + "\n" + StyleToolSuccess.Render(fmt.Sprintf("权限等级已成功切换为: %s %s", modeArg, desc))
			}
		}
		m.History = append(m.History, userLog, replyLog)
		return m, nil, true
	}

	if cmdName == "/rules" {
		m.HistoryManager.Add(inputVal)
		userLog := StyleUserMsg.Render("> " + inputVal)

		var sb strings.Builder
		sb.WriteString(StyleKeyActive.Render("Permission Rules") + "\n")

		rules := agent.GlobalPermissionManager.GetRules()
		for i, r := range rules {
			behaviorStr := ""
			if r.Behavior == "allow" {
				behaviorStr = lipgloss.NewStyle().Foreground(ColorSuccess).Bold(true).Render("ALLOW")
			} else {
				behaviorStr = lipgloss.NewStyle().Foreground(ColorDanger).Bold(true).Render("DENY")
			}

			patternInfo := ""
			if r.Path != "" {
				patternInfo += fmt.Sprintf(" path: %s", r.Path)
			}
			if r.Content != "" {
				patternInfo += fmt.Sprintf(" content: %s", r.Content)
			}
			sb.WriteString(fmt.Sprintf("  %d. [%s] tool: %s%s\n", i+1, behaviorStr, r.Tool, patternInfo))
		}

		m.History = append(m.History, userLog, sb.String())
		m.TextArea.SetValue("")
		m.TextArea.SetHeight(2)
		return m, nil, true
	}

	if cmdName == "/hooks" {
		m.HistoryManager.Add(inputVal)
		userLog := StyleUserMsg.Render("> " + inputVal)

		// Sub-command: /hooks reload
		if len(parts) >= 2 && strings.ToLower(parts[1]) == "reload" {
			agent.GlobalHookManager.Reload()
			replyLog := StyleToolSuccess.Render("hooks reloaded")
			sources := agent.GlobalHookManager.GetSources()
			if len(sources) > 0 {
				replyLog += "\n" + StyleKeyHelp.Render("已加载配置文件: "+strings.Join(sources, ", "))
			}
			m.History = append(m.History, userLog, replyLog)
			m.TextArea.SetValue("")
			m.TextArea.SetHeight(2)
			return m, nil, true
		}

		// Default: /hooks — show all registered hooks
		var sb strings.Builder
		hookEventStyle := lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true)
		matcherStyle := lipgloss.NewStyle().Foreground(ColorWarning).Bold(false)

		hooks := agent.GlobalHookManager.GetHooks()
		sources := agent.GlobalHookManager.GetSources()

		if agent.GlobalHookManager.IsEmpty() {
			sb.WriteString(StyleKeyActive.Render("Hooks") + "\n")
			sb.WriteString("  " + StyleKeyHelp.Render("no hooks registered") + "\n")
			sb.WriteString("  " + StyleKeyHelp.Render("create .iroha/hooks.json or ~/.iroha/hooks.json") + "\n")
		} else {
			sb.WriteString(StyleKeyActive.Render("Hooks") + "\n")
			if len(sources) > 0 {
				sb.WriteString("  " + StyleKeyHelp.Render("sources: "+strings.Join(sources, ", ")) + "\n\n")
			}
			for _, event := range []string{"SessionStart", "PreToolUse", "PostToolUse"} {
				defs := hooks[event]
				if len(defs) == 0 {
					continue
				}
				sb.WriteString("  " + hookEventStyle.Render(event) + "\n")
				for i, d := range defs {
					matcher := d.Matcher
					if matcher == "" {
						matcher = "*"
					}
					sb.WriteString(fmt.Sprintf("    %d. matcher: %s  cmd: %s\n",
						i+1,
						matcherStyle.Render(matcher),
						lipgloss.NewStyle().Foreground(ColorSuccess).Render(d.Command),
					))
				}
			}
		}

		sb.WriteString("\n  " + StyleKeyHelp.Render("提示: 输入 /hooks reload 可热重载配置文件"))

		m.History = append(m.History, userLog, sb.String())
		m.TextArea.SetValue("")
		m.TextArea.SetHeight(2)
		return m, nil, true
	}

	if cmdName == "/memory" {
		m.HistoryManager.Add(inputVal)
		userLog := StyleUserMsg.Render("> " + inputVal)

		var sb strings.Builder
		memTypeStyle := lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true)
		nameStyle := lipgloss.NewStyle().Foreground(ColorWarning).Bold(true)

		count := agent.GlobalMemoryManager.Count()
		dirs := agent.GlobalMemoryManager.GetDirs()

		if count == 0 {
			sb.WriteString(StyleKeyActive.Render("Memory") + "\n")
			sb.WriteString("  " + StyleKeyHelp.Render("no memories stored") + "\n")
			sb.WriteString("  " + StyleKeyHelp.Render("tell the agent to remember something") + "\n")
		} else {
			sb.WriteString(StyleKeyActive.Render("Memory") +
				StyleKeyHelp.Render(fmt.Sprintf(" (%d entries)", count)) + "\n")
			if len(dirs) > 0 {
				sb.WriteString("  " + StyleKeyHelp.Render("stored at: "+strings.Join(dirs, ", ")) + "\n\n")
			}
			all := agent.GlobalMemoryManager.List()
			typeOrder := []agent.MemoryType{
				agent.MemTypeUser, agent.MemTypeFeedback,
				agent.MemTypeProject, agent.MemTypeReference,
			}
			typeIcons := map[agent.MemoryType]string{
				agent.MemTypeUser:      "user",
				agent.MemTypeFeedback:  "feedback",
				agent.MemTypeProject:   "project",
				agent.MemTypeReference: "reference",
			}
			for _, t := range typeOrder {
				entries := all[t]
				if len(entries) == 0 {
					continue
				}
				sb.WriteString("  " + memTypeStyle.Render(typeIcons[t]) + "\n")
				for _, e := range entries {
					sb.WriteString(fmt.Sprintf("    • %s — %s\n",
						nameStyle.Render(e.Name), e.Description))
				}
			}
		}
		sb.WriteString("\n  " + StyleKeyHelp.Render("提示: 对话中说「记住…」让 Agent 调用 memory_save | 说「你记得什么」让它调用 memory_list"))

		m.History = append(m.History, userLog, sb.String())
		m.TextArea.SetValue("")
		m.TextArea.SetHeight(2)
		return m, nil, true
	}

	if cmdName == "/prompt" {
		m.HistoryManager.Add(inputVal)
		userLog := StyleUserMsg.Render("> " + inputVal)

		builder := agent.NewSystemPromptBuilder()
		fullPrompt := builder.Build()

		var sb strings.Builder
		sb.WriteString(StyleKeyActive.Render("System Prompt") + "\n")
		sb.WriteString(strings.Repeat("─", 72) + "\n")
		sb.WriteString(fullPrompt + "\n")
		sb.WriteString(strings.Repeat("─", 72) + "\n")
		sb.WriteString("  " + StyleKeyHelp.Render(fmt.Sprintf("%d chars", len(fullPrompt))))

		m.History = append(m.History, userLog, sb.String())
		m.TextArea.SetValue("")
		m.TextArea.SetHeight(2)
		return m, nil, true
	}

	if cmdName == "/sections" {
		m.HistoryManager.Add(inputVal)
		userLog := StyleUserMsg.Render("> " + inputVal)

		builder := agent.NewSystemPromptBuilder()
		fullPrompt := builder.Build()

		var sb strings.Builder
		sb.WriteString(StyleKeyActive.Render("System Prompt Sections") + "\n\n")

		lines := strings.Split(fullPrompt, "\n")
		sectionIdx := 1
		for _, line := range lines {
			lineTrimmed := strings.TrimSpace(line)
			if strings.HasPrefix(lineTrimmed, "# ") {
				sb.WriteString(fmt.Sprintf("  %d. %s\n", sectionIdx, lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true).Render(strings.TrimPrefix(lineTrimmed, "# "))))
				sectionIdx++
			} else if strings.HasPrefix(lineTrimmed, "## ") {
				sb.WriteString(fmt.Sprintf("     • %s\n", lipgloss.NewStyle().Foreground(ColorWarning).Render(strings.TrimPrefix(lineTrimmed, "## "))))
			} else if strings.HasPrefix(lineTrimmed, "### ") {
				sb.WriteString(fmt.Sprintf("       - %s\n", lipgloss.NewStyle().Foreground(ColorSuccess).Render(strings.TrimPrefix(lineTrimmed, "### "))))
			} else if strings.HasPrefix(lineTrimmed, "#### ") {
				sb.WriteString(fmt.Sprintf("         ▪ %s\n", lipgloss.NewStyle().Foreground(ColorSecondary).Render(strings.TrimPrefix(lineTrimmed, "#### "))))
			} else if lineTrimmed == "=== DYNAMIC_BOUNDARY ===" {
				sb.WriteString("  " + lipgloss.NewStyle().Foreground(ColorDanger).Bold(true).Render("--- DYNAMIC CACHING BOUNDARY ---") + "\n")
			}
		}

		sb.WriteString("\n  " + StyleKeyHelp.Render("提示: 输入 /prompt 可查看每个区块的完整内容"))

		m.History = append(m.History, userLog, sb.String())
		m.TextArea.SetValue("")
		m.TextArea.SetHeight(2)
		return m, nil, true
	}

	if cmdName == "/task" {
		m.HistoryManager.Add(inputVal)
		userLog := StyleUserMsg.Render("> " + inputVal)
		m.History = append(m.History, userLog, RenderTaskDetails())
		m.TextArea.SetValue("")
		m.TextArea.SetHeight(2)
		return m, nil, true
	}

	if cmdName == "/team" {
		m.HistoryManager.Add(inputVal)
		userLog := StyleUserMsg.Render("> " + inputVal)
		m.History = append(m.History, userLog, RenderTeamDashboard())
		m.TextArea.SetValue("")
		m.TextArea.SetHeight(2)
		return m, nil, true
	}

	if cmdName == "/worktree" {
		m.HistoryManager.Add(inputVal)
		userLog := StyleUserMsg.Render("> " + inputVal)
		m.History = append(m.History, userLog, RenderWorktreeDashboard())
		m.TextArea.SetValue("")
		m.TextArea.SetHeight(2)
		return m, nil, true
	}

	if cmdName == "/mcp" {
		m.HistoryManager.Add(inputVal)
		userLog := StyleUserMsg.Render("> " + inputVal)
		m.History = append(m.History, userLog, RenderMCPDashboard())
		m.TextArea.SetValue("")
		m.TextArea.SetHeight(2)
		return m, nil, true
	}

	if cmdName == "/bg" {
		m.HistoryManager.Add(inputVal)
		userLog := StyleUserMsg.Render("> " + inputVal)
		m.History = append(m.History, userLog, RenderBackgroundDashboard())
		m.TextArea.SetValue("")
		m.TextArea.SetHeight(2)
		return m, nil, true
	}

	if cmdName == "/help" || cmdName == "/commands" {
		m.HistoryManager.Add(inputVal)
		userLog := StyleUserMsg.Render("> " + inputVal)
		m.History = append(m.History, userLog, RenderHelpDashboard())
		m.TextArea.SetValue("")
		m.TextArea.SetHeight(2)
		m.Viewport.SetContent(m.renderViewportContent())
		m.Viewport.GotoBottom()
		return m, nil, true
	}

	if cmdName == "/doctor" {
		m.HistoryManager.Add(inputVal)
		userLog := StyleUserMsg.Render("> " + inputVal)
		m.History = append(m.History, userLog)
		m.TextArea.SetValue("")
		m.TextArea.SetHeight(2)

		m = m.transitionTo(stateThinking)
		m.ActiveTool = agent.ToolStatus{
			Name:    "🩺 环境诊断",
			Running: true,
		}
		m.RoundStartTime = time.Now()

		return m, runDoctorCmd(), true
	}

	if cmdName == "/sessions" {
		m.HistoryManager.Add(inputVal)
		m.TextArea.SetValue("")
		m.TextArea.SetHeight(2)
		m.PrevState = m.State
		m = m.transitionTo(stateSessionSelect)
		m.loadSessionsList()
		m.Viewport.SetContent(m.renderViewportContent())
		return m, nil, true
	}

	if cmdName == "/permission" {
		m.HistoryManager.Add(inputVal)
		userLog := StyleUserMsg.Render("> " + inputVal)
		m.TextArea.SetValue("")
		m.TextArea.SetHeight(2)

		if len(parts) < 2 {
			m.History = append(m.History, userLog, RenderPermissionSelect(agent.GlobalPermissionManager.GetMode()))
			// Switch to inline permission selection state
			m = m.transitionTo(statePermissionSelect)
			m.PermSelectIndex = 1 // default
			return m, nil, true
		}

		// Direct switch mode
		modeArg := agent.PermissionMode(strings.ToLower(parts[1]))
		err := agent.GlobalPermissionManager.SetMode(modeArg)
		var replyLog string
		if err != nil {
			replyLog = StyleToolError.Render(fmt.Sprintf("[error] 无效的权限等级: %s。可选模式: default, plan, auto", parts[1]))
		} else {
			var desc string
			switch modeArg {
			case agent.ModePlan:
				desc = "(只读模式，拦截所有写操作)"
			case agent.ModeAuto:
				desc = "(读操作自动同意，写操作仍需授权)"
			default:
				desc = "(每次非匹配规则的敏感操作均需授权)"
			}
			replyLog = StyleToolSuccess.Render(fmt.Sprintf("权限等级已成功切换为: %s %s", modeArg, desc))
		}
		m.History = append(m.History, userLog, replyLog)
		return m, nil, true
	}

	return m, nil, false
}
