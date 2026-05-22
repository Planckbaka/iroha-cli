package tui

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"iroha/pkg/agent"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/google/uuid"
	"google.golang.org/adk/session"
)

var statusTagRe = regexp.MustCompile(`(?m)^\[status:(.+?)\]`)

type TuiState int

const (
	statePrompt TuiState = iota
	stateThinking
	stateStreaming
	stateConfirming
	statePermissionSelect
	stateSessionSelect
)

func (s TuiState) String() string {
	switch s {
	case statePrompt:
		return "Prompt"
	case stateThinking:
		return "Thinking"
	case stateStreaming:
		return "Streaming"
	case stateConfirming:
		return "Confirming"
	case statePermissionSelect:
		return "PermissionSelect"
	case stateSessionSelect:
		return "SessionSelect"
	default:
		return "Unknown"
	}
}

func (m Model) transitionTo(newState TuiState) Model {
	oldState := m.State
	m.State = newState
	agent.LogInfo(agent.CatTUI, "state_transition", fmt.Sprintf("TUI transitioned from %s to %s", oldState.String(), newState.String()), map[string]any{
		"session_id": m.SessionID,
		"old_state":  oldState.String(),
		"new_state":  newState.String(),
	})
	return m
}

// Custom Message Types for Concurrency
type StreamTextMsg struct {
	Text string
}

type ConfirmationRequiredMsg struct {
	Prompt string
}

type AgentErrorMsg struct {
	Err error
}

type AgentDoneMsg struct{}

type ProgramRef struct {
	P *tea.Program
}

// SlashMenuItem represents a single slash command entry in the popup menu
type SlashMenuItem struct {
	Command     string
	Description string
}

// AllSlashCommands is the master list of all supported slash commands
var AllSlashCommands = []SlashMenuItem{
	{"/permission", "选择或切换权限等级 (plan | auto | default)"},
	{"/rules", "查看当前权限规则列表"},
	{"/hooks", "查看或热重载 Hook 配置 (reload)"},
	{"/memory", "查看跨会话记忆内容"},
	{"/prompt", "查看完整 System Prompt"},
	{"/sections", "查看 System Prompt 结构大纲"},
	{"/task", "查看任务规划看板"},
	{"/team", "查看多 Agent 团队状态"},
	{"/worktree", "查看 Git Worktree 隔离状态"},
	{"/mcp", "查看 MCP 插件状态"},
	{"/bg", "查看后台任务状态"},
	{"/sessions", "查看和切换会话历史"},
	{"/exit", "退出程序"},
}

// Model is the main TUI model
type Model struct {
	State              TuiState
	TextArea           textarea.Model
	Viewport           viewport.Model
	Width              int
	Height             int
	Ready              bool
	Spinner            spinner.Model
	HistoryManager     *HistoryManager
	History            []string
	CurrentPrompt      string
	StreamedText       string
	ConfirmationPrompt string
	Runner             *agent.CustomRunner
	Ctx                context.Context
	Cancel             context.CancelFunc
	ProgramRef         *ProgramRef
	LastError          error

	// Phase 2 display metrics
	ActiveTool        agent.ToolStatus
	RoundCount        int
	SessionStartTime  time.Time
	RoundStartTime    time.Time
	LastRoundDuration time.Duration

	// Shell streaming output
	ShellOutputStreamLines []string
	ShellStreamActive      bool
	lastStreamUpdate       time.Time

	// Token usage tracking
	TotalTokens int

	// Status tag parsing
	CurrentStatusText string

	// Slash command popup
	SlashMenuActive bool
	SlashMenuItems  []SlashMenuItem
	SlashMenuIndex  int

	// Startup permission selection
	PermSelectIndex int

	// Confirm card selection index (0: Y, 1: N, 2: A)
	ConfirmSelectIndex int

	// Session management
	SessionID            string
	StartInSessionPicker bool
	SessionsList         []agent.SessionMetadata
	SessionListIndex     int
	PrevState            TuiState

	// Callback closures to avoid nil pointer program issues
	OnEvent func(*session.Event)
	OnError func(error)
	OnDone  func()
}

func SetupTextArea() textarea.Model {
	ta := textarea.New()
	ta.Placeholder = "Send a message... (Enter to send, Shift+Down for new line)"
	ta.Focus()
	ta.Prompt = "┃ "
	ta.CharLimit = 0
	ta.SetWidth(100)
	ta.SetHeight(2)
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.ShowLineNumbers = false
	ta.KeyMap.InsertNewline.SetKeys("shift+down")
	return ta
}

func NewModel(runner *agent.CustomRunner, sessionID string, startInSessionPicker bool) Model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = StyleThinking

	ctx, cancel := context.WithCancel(context.Background())
	pref := &ProgramRef{}

	ta := SetupTextArea()
	vp := viewport.New(100, 20)
	vp.SetContent("Welcome to Iroha.")

	m := Model{
		State:                statePermissionSelect,
		TextArea:             ta,
		Viewport:             vp,
		Spinner:              s,
		HistoryManager:       NewHistoryManager(),
		History:              make([]string, 0),
		Runner:               runner,
		Ctx:                  ctx,
		Cancel:               cancel,
		ProgramRef:           pref,
		SessionStartTime:     time.Now(),
		PermSelectIndex:      1, // Default to "default" mode (index 1)
		SessionID:            sessionID,
		StartInSessionPicker: startInSessionPicker,
	}

	if sessionID != "" && !startInSessionPicker {
		m.LoadHistoryFromSession(sessionID)
	}

	m.OnEvent = func(ev *session.Event) {
		if pref.P != nil && ev != nil && ev.LLMResponse.Content != nil {
			for _, part := range ev.LLMResponse.Content.Parts {
				if part.Text != "" {
					pref.P.Send(StreamTextMsg{Text: part.Text})
				}
			}
		}
	}
	m.OnError = func(err error) {
		if pref.P != nil {
			pref.P.Send(AgentErrorMsg{Err: err})
		}
	}
	m.OnDone = func() {
		if pref.P != nil {
			pref.P.Send(AgentDoneMsg{})
		}
	}

	return m
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		textarea.Blink,
		m.Spinner.Tick,
		m.listenToConfirmationBridge(), // Listen for sensitive tool auth calls
		m.listenToToolBridge(),         // Listen for real-time tool execution status
	)
}

// listenToConfirmationBridge waits on the Bridge's PromptChan and sends a message to the TUI
func (m Model) listenToConfirmationBridge() tea.Cmd {
	return func() tea.Msg {
		prompt := <-agent.Bridge.PromptChan
		return ConfirmationRequiredMsg{Prompt: prompt}
	}
}

// listenToToolBridge waits on the ToolBridge's StatusChan and sends a message to the TUI
func (m Model) listenToToolBridge() tea.Cmd {
	return func() tea.Msg {
		status := <-agent.ToolBridge.StatusChan
		return ToolStatusMsg{Status: status}
	}
}

type ToolStatusMsg struct {
	Status agent.ToolStatus
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.Width = msg.Width
		m.Height = msg.Height

		m.TextArea.SetWidth(msg.Width)
		m.Viewport.Width = msg.Width
		m.Viewport.Height = msg.Height - m.TextArea.Height() - 3 // Subtract 3 to account for status bar and separator

		if !m.Ready {
			m.Ready = true
			m.Viewport.SetContent(m.renderViewportContent())
		}

		return m, nil

	case tea.KeyMsg:
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
				return m, m.listenToConfirmationBridge()
			}

			switch keyStr {
			case "y":
				m = m.transitionTo(stateThinking)
				agent.Bridge.ResponseChan <- "y"
				return m, m.listenToConfirmationBridge()
			case "n", "esc":
				m = m.transitionTo(stateThinking)
				agent.Bridge.ResponseChan <- "n"
				return m, m.listenToConfirmationBridge()
			case "a":
				m = m.transitionTo(stateThinking)
				agent.Bridge.ResponseChan <- "always"
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
		case tea.KeyCtrlC:
			if m.State != statePrompt {
				// Cancel current agent execution
				m.Cancel()
				m.StreamedText += "\n\x1b[31m[操作已被用户取消]\x1b[0m"
				m.finalizeTurn()
				return m, nil
			}
			return m, tea.Quit

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
			if m.State == statePrompt && m.SlashMenuActive && len(m.SlashMenuItems) > 0 {
				selected := m.SlashMenuItems[m.SlashMenuIndex]
				m.TextArea.SetValue(selected.Command + " ")
				m.SlashMenuActive = false
				m.SlashMenuItems = nil
				return m, nil
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
					parts := strings.Fields(inputVal)
					cmdName := parts[0]

					if cmdName == "/exit" || cmdName == "/quit" {
						return m, tea.Quit
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
						return m, nil
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
						return m, nil
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
							return m, nil
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
						return m, nil
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
						return m, nil
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
						return m, nil
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
						return m, nil
					}

					if cmdName == "/task" {
						m.HistoryManager.Add(inputVal)
						userLog := StyleUserMsg.Render("> " + inputVal)
						m.History = append(m.History, userLog, RenderTaskDetails())
						m.TextArea.SetValue("")
						m.TextArea.SetHeight(2)
						return m, nil
					}

					if cmdName == "/team" {
						m.HistoryManager.Add(inputVal)
						userLog := StyleUserMsg.Render("> " + inputVal)
						m.History = append(m.History, userLog, RenderTeamDashboard())
						m.TextArea.SetValue("")
						m.TextArea.SetHeight(2)
						return m, nil
					}

					if cmdName == "/worktree" {
						m.HistoryManager.Add(inputVal)
						userLog := StyleUserMsg.Render("> " + inputVal)
						m.History = append(m.History, userLog, RenderWorktreeDashboard())
						m.TextArea.SetValue("")
						m.TextArea.SetHeight(2)
						return m, nil
					}

					if cmdName == "/mcp" {
						m.HistoryManager.Add(inputVal)
						userLog := StyleUserMsg.Render("> " + inputVal)
						m.History = append(m.History, userLog, RenderMCPDashboard())
						m.TextArea.SetValue("")
						m.TextArea.SetHeight(2)
						return m, nil
					}

					if cmdName == "/bg" {
						m.HistoryManager.Add(inputVal)
						userLog := StyleUserMsg.Render("> " + inputVal)
						m.History = append(m.History, userLog, RenderBackgroundDashboard())
						m.TextArea.SetValue("")
						m.TextArea.SetHeight(2)
						return m, nil
					}

					if cmdName == "/sessions" {
						m.HistoryManager.Add(inputVal)
						m.TextArea.SetValue("")
						m.TextArea.SetHeight(2)
						m.PrevState = m.State
						m = m.transitionTo(stateSessionSelect)
						m.loadSessionsList()
						m.Viewport.SetContent(m.renderViewportContent())
						return m, nil
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
							return m, nil
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
						return m, nil
					}
				}

				// Record in history
				m.HistoryManager.Add(inputVal)

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

		// (stateConfirming is handled above before TextArea steals keys)

	// Dynamic Background Runner Stream messages
	case StreamTextMsg:
		m = m.transitionTo(stateStreaming)
		m.StreamedText += msg.Text

		// 解析 [status:xxx] 标签（取最后一个匹配）
		matches := statusTagRe.FindAllStringSubmatch(m.StreamedText, -1)
		if len(matches) > 0 {
			m.CurrentStatusText = matches[len(matches)-1][1]
		}

		m.Viewport.SetContent(m.renderViewportContent())
		m.Viewport.GotoBottom()
		return m, nil

	case ToolStatusMsg:
		status := msg.Status

		// 处理流式输出行（仅 shell_run）
		if status.Running && len(status.StreamLines) > 0 {
			m.ShellOutputStreamLines = append(m.ShellOutputStreamLines, status.StreamLines...)
			m.ShellStreamActive = true
			// 节流：100ms 或累计 ≥5 行时才刷新 Viewport
			now := time.Now()
			if now.Sub(m.lastStreamUpdate) >= 100*time.Millisecond || len(m.ShellOutputStreamLines)%5 == 0 {
				m.lastStreamUpdate = now
				m.Viewport.SetContent(m.renderViewportContent())
				m.Viewport.GotoBottom()
			}
			return m, m.listenToToolBridge()
		}

		if status.Running {
			m.ActiveTool = status
			if m.RoundStartTime.IsZero() {
				m.RoundStartTime = time.Now()
			}
		} else {
			m.ActiveTool = agent.ToolStatus{}
			// 清空流式输出区域
			m.ShellOutputStreamLines = nil
			m.ShellStreamActive = false
			var logLine string
			if status.Success {
				logLine = "\n\n" + RenderToolSuccessCard(status.Name, status.Args, status.Duration) + "\n"
			} else {
				logLine = "\n\n" + RenderToolErrorCard(status.Name, status.Args, status.Duration, status.Error) + "\n"
			}
			m.StreamedText += logLine
			if !m.RoundStartTime.IsZero() {
				m.LastRoundDuration = time.Since(m.RoundStartTime)
			}
		}
		m.Viewport.SetContent(m.renderViewportContent())
		m.Viewport.GotoBottom()
		return m, m.listenToToolBridge()

	case ConfirmationRequiredMsg:
		m = m.transitionTo(stateConfirming)
		m.ConfirmationPrompt = msg.Prompt
		m.ConfirmSelectIndex = 0
		m.Viewport.SetContent(m.renderViewportContent())
		m.Viewport.GotoBottom()
		// IMPORTANT: Do NOT re-register listenToConfirmationBridge here.
		// It will be re-registered only AFTER the user responds (y/n/a).
		// This prevents a race where we listen again before the response is sent.
		return m, nil

	case AgentErrorMsg:
		m.LastError = msg.Err
		m.finalizeTurn()
		return m, nil

	case AgentDoneMsg:
		m.finalizeTurn()
		return m, nil

	case spinner.TickMsg:
		m.Spinner, cmd = m.Spinner.Update(msg)
		if m.State == stateThinking {
			m.Viewport.SetContent(m.renderViewportContent())
		}
		return m, cmd
	}

	// Handle viewport update
	var vpCmd tea.Cmd
	m.Viewport, vpCmd = m.Viewport.Update(msg)
	cmd = tea.Batch(cmd, vpCmd)

	// Update text area only in prompt state, then update slash menu filter
	if m.State == statePrompt {
		prevVal := m.TextArea.Value()
		var taCmd tea.Cmd
		m.TextArea, taCmd = m.TextArea.Update(msg)
		cmd = tea.Batch(cmd, taCmd)
		newVal := m.TextArea.Value()
		// Update slash menu if the input changed
		if newVal != prevVal {
			m.updateSlashMenu(newVal)
		}

		// Dynamic auto-scaling height of Textarea between 2 and 6 lines
		numLines := len(strings.Split(newVal, "\n"))
		h := numLines
		if h < 2 {
			h = 2
		} else if h > 6 {
			h = 6
		}
		if m.TextArea.Height() != h {
			m.TextArea.SetHeight(h)
			m.Viewport.Height = m.Height - h - 3
			// Refresh viewport content styling
			m.Viewport.SetContent(m.renderViewportContent())
			m.Viewport.GotoBottom()
		}

		return m, cmd
	}

	return m, nil
}

func (m *Model) finalizeTurn() {
	*m = m.transitionTo(statePrompt)
	if !m.RoundStartTime.IsZero() {
		m.LastRoundDuration = time.Since(m.RoundStartTime)
		m.RoundStartTime = time.Time{}
	}
	m.ActiveTool = agent.ToolStatus{}
	m.CurrentStatusText = ""

	// 更新 token 计数
	if m.Runner != nil {
		usage := m.Runner.GetTokenUsage()
		if usage > 0 {
			m.TotalTokens = usage
		} else if m.TotalTokens == 0 {
			// Fallback: 本地估算（字符数 / 4）
			m.TotalTokens = len(m.StreamedText) / 4
		}
	}

	userLog := StyleUserMsg.Render("> " + m.CurrentPrompt)

	var agentLog string
	if m.LastError != nil {
		agentLog = StyleAgentMsg.Render(RenderErrorCard(m.LastError))
		m.LastError = nil // Reset
	} else {
		agentLog = StyleAgentMsg.Render(RenderMarkdown(m.StreamedText))
	}

	m.History = append(m.History, userLog, agentLog)
	m.TextArea.Focus()
	m.Viewport.SetContent(m.renderViewportContent())
	m.Viewport.GotoBottom()
}

func extractCommand(args any) string {
	if argMap, ok := args.(map[string]any); ok {
		if cmd, ok := argMap["command"].(string); ok {
			return cmd
		}
	}
	return ""
}

func (m *Model) renderViewportContent() string {
	var sb strings.Builder

	todoRender := RenderTodoDashboard()
	if todoRender != "" {
		sb.WriteString(todoRender)
		sb.WriteString("\n")
	}

	taskRender := RenderTaskDashboard()
	if taskRender != "" {
		sb.WriteString(taskRender)
		sb.WriteString("\n")
	}

	if len(m.History) > 0 {
		sb.WriteString(strings.Join(m.History, "\n"))
		sb.WriteString("\n")
	} else if m.State == statePrompt {
		sb.WriteString(RenderWelcomeCard(m.Runner))
		sb.WriteString("\n")
	}

	switch m.State {
	case stateThinking:
		if m.ShellStreamActive && len(m.ShellOutputStreamLines) > 0 {
			// Shell 流式输出：spinner + 流式区域
			cmd := extractCommand(m.ActiveTool.Args)
			sb.WriteString("\n" + StyleAgentMsg.Render(m.Spinner.View()+StyleThinking.Render(" 运行终端命令...")))
			sb.WriteString(RenderShellStreamArea(m.ShellOutputStreamLines, cmd, m.Width))
		} else if m.ActiveTool.Running {
			activity := FormatToolActivity(m.ActiveTool.Name, m.ActiveTool.Args)
			sb.WriteString("\n" + StyleAgentMsg.Render(m.Spinner.View()+StyleThinking.Render(" "+activity)))
		} else {
			sb.WriteString("\n" + StyleAgentMsg.Render(m.Spinner.View()+StyleThinking.Render(" thinking...")))
		}
	case stateStreaming:
		sb.WriteString("\n" + StyleAgentMsg.Render(RenderMarkdown(m.StreamedText)))
		if m.ShellStreamActive && len(m.ShellOutputStreamLines) > 0 {
			cmd := extractCommand(m.ActiveTool.Args)
			sb.WriteString(RenderShellStreamArea(m.ShellOutputStreamLines, cmd, m.Width))
		} else if m.ActiveTool.Running {
			activity := FormatToolActivity(m.ActiveTool.Name, m.ActiveTool.Args)
			sb.WriteString("\n" + StyleAgentMsg.Render(m.Spinner.View()+StyleThinking.Render(" "+activity)))
		}
	case stateConfirming:
		sb.WriteString("\n" + StyleAgentMsg.Render(RenderMarkdown(m.StreamedText)+"\n"+RenderConfirmCard(m.ConfirmationPrompt, m.ConfirmSelectIndex)))
	}

	return sb.String()
}

func (m Model) View() string {
	if !m.Ready {
		return "\n  Initializing..."
	}

	// Full-screen permission selection on startup or /permission command
	if m.State == statePermissionSelect {
		return RenderPermissionSelectScreen(m)
	}

	// Full-screen session selection
	if m.State == stateSessionSelect {
		return RenderSessionSelectScreen(m)
	}

	var sb strings.Builder

	// Viewport taking up top space
	sb.WriteString(m.Viewport.View())
	sb.WriteString("\n")

	// Separator line
	sb.WriteString(lipgloss.NewStyle().Foreground(ColorSecondary).Render(strings.Repeat("─", m.Width)))
	sb.WriteString("\n")

	// Slash command popup — rendered ABOVE the textarea
	if m.SlashMenuActive && len(m.SlashMenuItems) > 0 {
		sb.WriteString(RenderSlashMenu(m.SlashMenuItems, m.SlashMenuIndex, m.Width))
		sb.WriteString("\n")
	}

	// TextArea taking up bottom space
	sb.WriteString(m.TextArea.View())
	sb.WriteString("\n")

	// Status Bar at the bottom
	sb.WriteString(RenderStatusBar(m))

	return sb.String()
}

// updateSlashMenu re-filters the slash menu based on current input
func (m *Model) updateSlashMenu(input string) {
	if !strings.HasPrefix(input, "/") {
		m.SlashMenuActive = false
		m.SlashMenuItems = nil
		return
	}

	filter := strings.ToLower(strings.TrimSpace(input))
	var matches []SlashMenuItem
	for _, item := range AllSlashCommands {
		if strings.HasPrefix(strings.ToLower(item.Command), filter) {
			matches = append(matches, item)
		}
	}

	if len(matches) == 0 {
		m.SlashMenuActive = false
		m.SlashMenuItems = nil
		return
	}

	m.SlashMenuActive = true
	m.SlashMenuItems = matches
	// Clamp selection index
	if m.SlashMenuIndex >= len(matches) {
		m.SlashMenuIndex = len(matches) - 1
	}
	if m.SlashMenuIndex < 0 {
		m.SlashMenuIndex = 0
	}
}

func (m *Model) loadSessionsList() {
	if agent.GlobalSessionService != nil {
		list, err := agent.GlobalSessionService.ListSavedSessions()
		if err == nil {
			m.SessionsList = list
			// Reset session list picker index if out of bounds
			if m.SessionListIndex > len(list) {
				m.SessionListIndex = len(list)
			}
			if m.SessionListIndex < 0 {
				m.SessionListIndex = 0
			}
		}
	}
}

func (m *Model) LoadHistoryFromSession(sessionID string) {
	m.History = nil
	if agent.GlobalSessionService == nil {
		return
	}
	resp, err := agent.GlobalSessionService.Get(context.Background(), &session.GetRequest{
		SessionID: sessionID,
	})
	if err != nil || resp.Session == nil {
		return
	}

	var events []*session.Event
	if resp.Session.Events() != nil {
		for ev := range resp.Session.Events().All() {
			events = append(events, ev)
		}
	}

	type turn struct {
		prompt   string
		response string
	}
	var turns []turn
	var currentTurn *turn

	for _, ev := range events {
		if ev == nil {
			continue
		}
		if ev.Content != nil {
			var promptParts []string
			for _, part := range ev.Content.Parts {
				if part.Text != "" {
					promptParts = append(promptParts, part.Text)
				}
			}
			if len(promptParts) > 0 {
				pText := strings.Join(promptParts, "\n")
				if currentTurn != nil {
					turns = append(turns, *currentTurn)
				}
				currentTurn = &turn{
					prompt: pText,
				}
			}
		}

		if ev.LLMResponse.Content != nil {
			var respParts []string
			for _, part := range ev.LLMResponse.Content.Parts {
				if part.Text != "" {
					respParts = append(respParts, part.Text)
				}
			}
			if len(respParts) > 0 {
				rText := strings.Join(respParts, "")
				if currentTurn == nil {
					currentTurn = &turn{}
				}
				currentTurn.response += rText
			}
		}
	}

	if currentTurn != nil {
		turns = append(turns, *currentTurn)
	}

	for _, t := range turns {
		userLog := StyleUserMsg.Render("> " + t.prompt)
		agentLog := StyleAgentMsg.Render(RenderMarkdown(t.response))
		m.History = append(m.History, userLog, agentLog)
	}
}
