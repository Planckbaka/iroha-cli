package tui

import (
	"context"
	"fmt"
	"strings"

	"go-claude/pkg/agent"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"google.golang.org/adk/session"
)

type TuiState int

const (
	statePrompt TuiState = iota
	stateThinking
	stateStreaming
	stateConfirming
)

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

// Model is the main TUI model
type Model struct {
	State              TuiState
	TextInput          textinput.Model
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

	// Callback closures to avoid nil pointer program issues
	OnEvent func(*session.Event)
	OnError func(error)
	OnDone  func()
}

func NewModel(runner *agent.CustomRunner) Model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = StyleThinking

	ctx, cancel := context.WithCancel(context.Background())
	pref := &ProgramRef{}

	m := Model{
		State:          statePrompt,
		TextInput:      SetupTextInput(),
		Spinner:        s,
		HistoryManager: NewHistoryManager(),
		History:        make([]string, 0),
		Runner:         runner,
		Ctx:            ctx,
		Cancel:         cancel,
		ProgramRef:     pref,
	}

	m.OnEvent = func(ev *session.Event) {
		if pref.P != nil && ev.LLMResponse.Content != nil {
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
		textinput.Blink,
		m.Spinner.Tick,
		m.listenToConfirmationBridge(), // Listen for sensitive tool auth calls
	)
}

// listenToConfirmationBridge waits on the Bridge's PromptChan and sends a message to the TUI
func (m Model) listenToConfirmationBridge() tea.Cmd {
	return func() tea.Msg {
		prompt := <-agent.Bridge.PromptChan
		return ConfirmationRequiredMsg{Prompt: prompt}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
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

		case tea.KeyEnter:
			if m.State == statePrompt {
				inputVal := strings.TrimSpace(m.TextInput.Value())
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
						var replyLog string

						if len(parts) < 2 {
							replyLog = StyleToolError.Render("❌ 请指定安全模式: /mode <plan | auto | default>")
						} else {
							modeArg := agent.PermissionMode(strings.ToLower(parts[1]))
							err := agent.GlobalPermissionManager.SetMode(modeArg)
							if err != nil {
								replyLog = StyleToolError.Render(fmt.Sprintf("❌ 无效的安全模式: %s。可选模式: default, plan, auto", parts[1]))
							} else {
								var desc string
								switch modeArg {
								case agent.ModePlan:
									desc = "【规划模式】(只读模式，拦截所有写操作)"
								case agent.ModeAuto:
									desc = "【自动模式】(读操作自动同意，写操作仍需授权)"
								default:
									desc = "【默认模式】(每次非匹配规则的敏感操作均需授权)"
								}
								replyLog = StyleToolSuccess.Render(fmt.Sprintf("🛡️ 安全模式已切换为: %s %s", modeArg, desc))
							}
						}
						m.History = append(m.History, userLog, replyLog)
						m.TextInput.SetValue("")
						return m, nil
					}

					if cmdName == "/rules" {
						m.HistoryManager.Add(inputVal)
						userLog := StyleUserMsg.Render("> " + inputVal)

						var sb strings.Builder
						sb.WriteString("📜 " + StyleKeyActive.Render("当前活跃的权限安全规则列表:") + "\n")

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
						m.TextInput.SetValue("")
						return m, nil
					}

					if cmdName == "/hooks" {
						m.HistoryManager.Add(inputVal)
						userLog := StyleUserMsg.Render("> " + inputVal)

						// Sub-command: /hooks reload
						if len(parts) >= 2 && strings.ToLower(parts[1]) == "reload" {
							agent.GlobalHookManager.Reload()
							replyLog := StyleToolSuccess.Render("🔄 Hook 配置已重新加载")
							sources := agent.GlobalHookManager.GetSources()
							if len(sources) > 0 {
								replyLog += "\n" + StyleKeyHelp.Render("已加载配置文件: "+strings.Join(sources, ", "))
							}
							m.History = append(m.History, userLog, replyLog)
							m.TextInput.SetValue("")
							return m, nil
						}

						// Default: /hooks — show all registered hooks
						var sb strings.Builder
						hookEventStyle := lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true)
						matcherStyle := lipgloss.NewStyle().Foreground(ColorWarning).Bold(false)

						hooks := agent.GlobalHookManager.GetHooks()
						sources := agent.GlobalHookManager.GetSources()

						if agent.GlobalHookManager.IsEmpty() {
							sb.WriteString("🪝 " + StyleKeyActive.Render("Hook 系统") + "\n")
							sb.WriteString("  " + StyleKeyHelp.Render("当前无已注册的 Hook。") + "\n")
							sb.WriteString("  " + StyleKeyHelp.Render("创建 .go-claude/hooks.json 或 ~/.go-claude/hooks.json 来配置 Hook。") + "\n")
						} else {
							sb.WriteString("🪝 " + StyleKeyActive.Render("已注册的 Hook 列表:") + "\n")
							if len(sources) > 0 {
								sb.WriteString("  " + StyleKeyHelp.Render("配置来源: "+strings.Join(sources, ", ")) + "\n\n")
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
						m.TextInput.SetValue("")
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
							sb.WriteString("🧠 " + StyleKeyActive.Render("Memory System") + "\n")
							sb.WriteString("  " + StyleKeyHelp.Render("当前无已存储的记忆条目。") + "\n")
							sb.WriteString("  " + StyleKeyHelp.Render("对话中告诉 Agent「记住这个」，它会调用 memory_save 工具保存。") + "\n")
						} else {
							sb.WriteString("🧠 " + StyleKeyActive.Render("持久化记忆") +
								StyleKeyHelp.Render(fmt.Sprintf(" (%d 条)", count)) + "\n")
							if len(dirs) > 0 {
								sb.WriteString("  " + StyleKeyHelp.Render("存储位置: "+strings.Join(dirs, ", ")) + "\n\n")
							}
							all := agent.GlobalMemoryManager.List()
							typeOrder := []agent.MemoryType{
								agent.MemTypeUser, agent.MemTypeFeedback,
								agent.MemTypeProject, agent.MemTypeReference,
							}
							typeIcons := map[agent.MemoryType]string{
								agent.MemTypeUser:      "👤 user",
								agent.MemTypeFeedback:  "🔁 feedback",
								agent.MemTypeProject:   "📁 project",
								agent.MemTypeReference: "🔗 reference",
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
						m.TextInput.SetValue("")
						return m, nil
					}

					if cmdName == "/prompt" {
						m.HistoryManager.Add(inputVal)
						userLog := StyleUserMsg.Render("> " + inputVal)

						builder := agent.NewSystemPromptBuilder()
						fullPrompt := builder.Build()

						var sb strings.Builder
						sb.WriteString("📋 " + StyleKeyActive.Render("当前活跃的系统提示词(System Prompt):") + "\n")
						sb.WriteString("--------------------------------------------------------------------------------\n")
						sb.WriteString(fullPrompt + "\n")
						sb.WriteString("--------------------------------------------------------------------------------\n")
						sb.WriteString("  " + StyleKeyHelp.Render(fmt.Sprintf("提示词字数统计: %d 字符", len(fullPrompt))))

						m.History = append(m.History, userLog, sb.String())
						m.TextInput.SetValue("")
						return m, nil
					}

					if cmdName == "/sections" {
						m.HistoryManager.Add(inputVal)
						userLog := StyleUserMsg.Render("> " + inputVal)

						builder := agent.NewSystemPromptBuilder()
						fullPrompt := builder.Build()

						var sb strings.Builder
						sb.WriteString("🗂️ " + StyleKeyActive.Render("系统提示词区块与结构大纲:") + "\n\n")

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
								sb.WriteString("  " + lipgloss.NewStyle().Foreground(ColorDanger).Bold(true).Render("⚡ === DYNAMIC CACHING BOUNDARY ===") + "\n")
							}
						}

						sb.WriteString("\n  " + StyleKeyHelp.Render("提示: 输入 /prompt 可查看每个区块的完整内容"))

						m.History = append(m.History, userLog, sb.String())
						m.TextInput.SetValue("")
						return m, nil
					}
				}

				// Record in history
				m.HistoryManager.Add(inputVal)

				// Prepare for the turn
				m.CurrentPrompt = inputVal
				m.StreamedText = ""
				m.State = stateThinking
				m.TextInput.SetValue("")

				// Start background Agent Execution
				ctx, cancel := context.WithCancel(context.Background())
				m.Ctx = ctx
				m.Cancel = cancel

				// Trigger execution with our registered closures
				m.Runner.Execute(m.Ctx, "user-dev", "session-default", m.CurrentPrompt,
					m.OnEvent, m.OnError, m.OnDone,
				)

				return m, m.Spinner.Tick
			}

		case tea.KeyUp:
			if m.State == statePrompt {
				m.TextInput.SetValue(m.HistoryManager.Up())
				return m, nil
			}

		case tea.KeyDown:
			if m.State == statePrompt {
				m.TextInput.SetValue(m.HistoryManager.Down())
				return m, nil
			}
		}

		// Handle key presses specifically for confirmation state
		if m.State == stateConfirming {
			keyStr := strings.ToLower(msg.String())
			if keyStr == "y" {
				m.State = stateThinking
				agent.Bridge.ResponseChan <- "y"
				return m, m.listenToConfirmationBridge() // Listen again for the next tool confirmation
			} else if keyStr == "n" || msg.Type == tea.KeyEscape {
				m.State = stateThinking
				agent.Bridge.ResponseChan <- "n"
				return m, m.listenToConfirmationBridge()
			} else if keyStr == "a" {
				m.State = stateThinking
				agent.Bridge.ResponseChan <- "always"
				return m, m.listenToConfirmationBridge()
			}
			return m, nil
		}

	// Dynamic Background Runner Stream messages
	case StreamTextMsg:
		m.State = stateStreaming
		m.StreamedText += msg.Text
		return m, nil

	case ConfirmationRequiredMsg:
		m.State = stateConfirming
		m.ConfirmationPrompt = msg.Prompt
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
		return m, cmd
	}

	// Update text input only in prompt state
	if m.State == statePrompt {
		m.TextInput, cmd = m.TextInput.Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m *Model) finalizeTurn() {
	m.State = statePrompt
	// Append current turn to static scrollback history in clean shell style
	userLog := StyleUserMsg.Render("> " + m.CurrentPrompt)
	
	var agentLog string
	if m.LastError != nil {
		agentLog = RenderErrorCard(m.LastError)
		m.LastError = nil // Reset
	} else {
		agentLog = RenderMarkdown(m.StreamedText)
	}
	
	m.History = append(m.History, userLog, agentLog)
	m.TextInput.Focus()
}

func (m Model) View() string {
	var sb strings.Builder

	// Render fixed premium header card / Todo Dashboard if there are active todo items
	todoRender := RenderTodoDashboard()
	if todoRender != "" {
		sb.WriteString(todoRender)
		sb.WriteString("\n")
	}

	// 1. Render static scrollback history
	if len(m.History) > 0 {
		sb.WriteString(strings.Join(m.History, "\n"))
		sb.WriteString("\n")
	} else if m.State == statePrompt {
		// Render premium welcome screen on first sight
		sb.WriteString(RenderWelcomeCard(m.Runner))
		sb.WriteString("\n")
	}

	// 2. Render current active state
	switch m.State {
	case statePrompt:
		sb.WriteString(StylePrompt.Render("go-claude > ") + m.TextInput.View())

	case stateThinking:
		sb.WriteString(m.Spinner.View() + StyleThinking.Render(" Thinking..."))

	case stateStreaming:
		sb.WriteString(RenderMarkdown(m.StreamedText))

	case stateConfirming:
		sb.WriteString(RenderMarkdown(m.StreamedText) + "\n")
		sb.WriteString(RenderConfirmCard(m.ConfirmationPrompt))
	}

	return sb.String()
}
