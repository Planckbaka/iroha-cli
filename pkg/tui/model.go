package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"iroha/pkg/agent"
	"iroha/pkg/config"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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

type DoctorResultMsg struct {
	Report string
}

type ExternalEditorFinishedMsg struct {
	Content string
	Err     error
}

func runDoctorCmd() tea.Cmd {
	return func() tea.Msg {
		report := RunDiagnostics()
		return DoctorResultMsg{Report: report}
	}
}

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
	{"/permission", "Select or switch permission level (plan | auto | default)"},
	{"/rules", "View current permission rules list"},
	{"/hooks", "View or hot-reload Hook configuration (reload)"},
	{"/memory", "View cross-session memory content"},
	{"/prompt", "View full System Prompt"},
	{"/sections", "View System Prompt structure outline"},
	{"/task", "View task planning board"},
	{"/team", "View multi-agent team status"},
	{"/worktree", "View Git Worktree isolation status"},
	{"/mcp", "View MCP plugin status"},
	{"/bg", "View background task status"},
	{"/skill", "Invoke a registered skill by name (e.g. /skill tdd-workflow)"},
	{"/trace", "View tool call trace log for the current session"},
	{"/stats", "View session statistics, performance latency, and token cost details"},
	{"/sessions", "View and switch session history"},
	{"/resume", "Resume the most recent session and continue the conversation"},
	{"/help", "View system help, keyboard shortcuts, and command palette"},
	{"/commands", "View all supported slash commands"},
	{"/doctor", "Run system diagnostics to check API, network, Git, and toolchain status"},
	{"/exit", "Exit the program"},
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

	// Clipboard copy
	LastRawResponse string

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
	TotalTokens      int
	TotalSessionCost float64

	// Incremental streaming render cache
	RenderedText    string
	LastRenderedLen int
	PendingText     string

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

	// Human-in-the-Loop Confirmation listener state tracking
	ConfirmationListenerActive bool

	// Startup prompt passed from command line
	StartupPrompt string

	// Tab auto-completion fields for files and directories
	PathCompletionActive   bool
	PathCompletionItems    []string
	PathCompletionIndex    int
	PathCompletionOriginal string
	PathCompletionRest     string

	// Premium interactive Diff fields
	ConfirmDiffText   string
	ConfirmDiffActive bool

	// Premium interactive Edit fields during confirmation
	ConfirmEditActive bool
	ConfirmEditText   string
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

func NewModel(runner *agent.CustomRunner, sessionID string, startInSessionPicker bool, initialMode agent.PermissionMode, startupPrompt string) Model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = StyleThinking

	ctx, cancel := context.WithCancel(context.Background())
	pref := &ProgramRef{}

	ta := SetupTextArea()
	vp := viewport.New(100, 20)
	vp.SetContent("Welcome to Iroha.")

	m := Model{
		State:                      statePermissionSelect,
		TextArea:                   ta,
		Viewport:                   vp,
		Spinner:                    s,
		HistoryManager:             NewHistoryManager(),
		History:                    make([]string, 0),
		Runner:                     runner,
		Ctx:                        ctx,
		Cancel:                     cancel,
		ProgramRef:                 pref,
		SessionStartTime:           time.Now(),
		PermSelectIndex:            1, // Default to "default" mode (index 1)
		SessionID:                  sessionID,
		StartInSessionPicker:       startInSessionPicker,
		ConfirmationListenerActive: true,
		StartupPrompt:              startupPrompt,
	}

	if initialMode != "" {
		_ = agent.GlobalPermissionManager.SetMode(initialMode)
		if startInSessionPicker {
			m.State = stateSessionSelect
		} else {
			m.State = statePrompt
		}
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

type StartupPromptMsg struct {
	Prompt string
}

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{
		textarea.Blink,
		m.Spinner.Tick,
		m.listenToConfirmationBridge(), // Listen for sensitive tool auth calls
		m.listenToToolBridge(),         // Listen for real-time tool execution status
	}
	if m.StartupPrompt != "" {
		cmds = append(cmds, func() tea.Msg {
			return StartupPromptMsg{Prompt: m.StartupPrompt}
		})
	}
	return tea.Batch(cmds...)
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
		if newM, keyCmd, handled := m.handleKeyMsg(msg); handled {
			return newM, keyCmd
		}

	case ExternalEditorFinishedMsg:
		if msg.Err != nil {
			m.History = append(m.History, StyleToolError.Render(fmt.Sprintf("[error] External editor failed: %v", msg.Err)))
			m.Viewport.SetContent(m.renderViewportContent())
			m.Viewport.GotoBottom()
		} else {
			m.TextArea.SetValue(msg.Content)
			m.TextArea.SetCursor(len(msg.Content))
		}
		return m, nil

	default:
		// Attempt to process custom agent events
		if newM, customCmd, handled := m.handleCustomMsg(msg); handled {
			return newM, customCmd
		}
	}

	// Handle viewport update
	var vpCmd tea.Cmd
	m.Viewport, vpCmd = m.Viewport.Update(msg)
	cmd = tea.Batch(cmd, vpCmd)

	// Update text area only in prompt state, then update slash menu filter
	if m.State == statePrompt || (m.State == stateConfirming && m.ConfirmEditActive) {
		prevVal := m.TextArea.Value()
		var taCmd tea.Cmd
		m.TextArea, taCmd = m.TextArea.Update(msg)
		cmd = tea.Batch(cmd, taCmd)
		newVal := m.TextArea.Value()

		if m.State == statePrompt {
			// Update slash menu if the input changed
			if newVal != prevVal {
				m.updateSlashMenu(newVal)

				// Reset path completion cycle if text changed via a non-Tab key
				isKeyTab := false
				if keyMsg, ok := msg.(tea.KeyMsg); ok && keyMsg.Type == tea.KeyTab {
					isKeyTab = true
				}
				if !isKeyTab {
					m.resetPathCompletion()
				}
			}
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

func (m *Model) finalizeTurn() tea.Cmd {
	*m = m.transitionTo(statePrompt)
	if !m.RoundStartTime.IsZero() {
		m.LastRoundDuration = time.Since(m.RoundStartTime)
		m.RoundStartTime = time.Time{}
	}
	m.ActiveTool = agent.ToolStatus{}
	m.CurrentStatusText = ""
	m.RenderedText = ""
	m.PendingText = ""
	m.LastRenderedLen = 0
	m.ConfirmEditActive = false
	m.ConfirmEditText = ""

	// Update token count and cost estimation
	if m.Runner != nil {
		usage := m.Runner.GetTokenUsage()
		if usage > 0 {
			m.TotalTokens = usage
		} else if m.TotalTokens == 0 {
			// Fallback: local estimation (character count / 4)
			m.TotalTokens = len(m.StreamedText) / 4
		}
		m.TotalSessionCost = config.EstimateCost(m.Runner.ModelName(), m.TotalTokens)
	}

	userLog := StyleUserMsg.Render("> " + m.CurrentPrompt)

	var agentLog string
	if m.LastError != nil {
		agentLog = StyleAgentMsg.Render(RenderErrorCard(m.LastError))
		m.LastError = nil // Reset
	} else {
		m.LastRawResponse = m.StreamedText
		agentLog = StyleAgentMsg.Render(RenderMarkdown(m.StreamedText))
	}

	m.History = append(m.History, userLog, agentLog)
	m.TextArea.Focus()
	m.Viewport.SetContent(m.renderViewportContent())
	m.Viewport.GotoBottom()

	var cmd tea.Cmd
	if !m.ConfirmationListenerActive {
		m.ConfirmationListenerActive = true
		cmd = m.listenToConfirmationBridge()
	}
	return cmd
}

func extractCommand(args any) string {
	if argMap, ok := args.(map[string]any); ok {
		if cmd, ok := argMap["command"].(string); ok {
			return cmd
		}
	}
	return ""
}

// renderIncremental renders only the pending text through Glamour and appends it
// to the cached RenderedText, avoiding a full re-render of the entire stream.
func (m *Model) renderIncremental() {
	rendered := RenderMarkdown(m.PendingText)
	m.RenderedText += rendered
	m.LastRenderedLen = len(m.StreamedText)
	m.PendingText = ""
	m.Viewport.SetContent(m.renderViewportContent())
	m.Viewport.GotoBottom()
}

func (m *Model) renderViewportContent() string {
	// If interactive Edit is active, render editing instructions in the viewport
	if m.State == stateConfirming && m.ConfirmEditActive {
		var sb strings.Builder
		sb.WriteString(lipgloss.NewStyle().Foreground(ColorWarning).Bold(true).Render("Editing Tool Arguments") + "\n\n")
		sb.WriteString(lipgloss.NewStyle().Foreground(ColorTextMuted).Render("Modify the arguments in the input area at the bottom:") + "\n\n")
		sb.WriteString("  " + lipgloss.NewStyle().Foreground(ColorPrimary).Render("Tool: ") + lipgloss.NewStyle().Foreground(ColorSuccess).Bold(true).Render(m.ActiveTool.Name) + "\n\n")
		sb.WriteString("  Press " + lipgloss.NewStyle().Foreground(ColorSuccess).Bold(true).Render("Enter") + " to run with modified arguments.\n")
		sb.WriteString("  Press " + lipgloss.NewStyle().Foreground(ColorDanger).Bold(true).Render("Esc") + " to cancel editing.\n\n")
		return sb.String()
	}

	// If interactive Diff is active during confirmation, only render the Diff view in the Viewport
	if m.State == stateConfirming && m.ConfirmDiffActive && m.ConfirmDiffText != "" {
		return m.ConfirmDiffText + "\n\n" + RenderConfirmCardWithDiff(m.ConfirmationPrompt, m.ConfirmSelectIndex, true, true)
	}

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
			// Shell streaming output: spinner + streaming area
			cmd := extractCommand(m.ActiveTool.Args)
			sb.WriteString("\n" + StyleAgentMsg.Render(m.Spinner.View()+StyleThinking.Render(" Running terminal command...")))
			sb.WriteString(RenderShellStreamArea(m.ShellOutputStreamLines, cmd, m.Width))
		} else if m.ActiveTool.Running {
			activity := FormatToolActivity(m.ActiveTool.Name, m.ActiveTool.Args)
			sb.WriteString("\n" + StyleAgentMsg.Render(m.Spinner.View()+StyleThinking.Render(" "+activity)))
		} else {
			sb.WriteString("\n" + StyleAgentMsg.Render(m.Spinner.View()+StyleThinking.Render(" thinking...")))
		}
	case stateStreaming:
		fullRendered := m.RenderedText
		if m.PendingText != "" {
			fullRendered += RenderMarkdown(m.PendingText)
		}
		if fullRendered != "" {
			sb.WriteString("\n" + StyleAgentMsg.Render(fullRendered))
		}
		if m.ShellStreamActive && len(m.ShellOutputStreamLines) > 0 {
			cmd := extractCommand(m.ActiveTool.Args)
			sb.WriteString(RenderShellStreamArea(m.ShellOutputStreamLines, cmd, m.Width))
		} else if m.ActiveTool.Running {
			activity := FormatToolActivity(m.ActiveTool.Name, m.ActiveTool.Args)
			sb.WriteString("\n" + StyleAgentMsg.Render(m.Spinner.View()+StyleThinking.Render(" "+activity)))
		}
	case stateConfirming:
		card := RenderConfirmCardWithDiff(m.ConfirmationPrompt, m.ConfirmSelectIndex, m.ConfirmDiffText != "", false)
		sb.WriteString("\n" + StyleAgentMsg.Render(RenderMarkdown(m.StreamedText)+"\n"+card))
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

	// Render path auto-completion suggestion line if active
	if m.PathCompletionActive && len(m.PathCompletionItems) > 0 {
		sb.WriteString(RenderPathCompletionBar(m.PathCompletionItems, m.PathCompletionIndex, m.Width))
		sb.WriteString("\n")
	}

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

	// Restore token usage and cost estimation for resurrected session
	totalTextLen := 0
	for _, t := range turns {
		totalTextLen += len(t.prompt) + len(t.response)
	}
	if totalTextLen > 0 {
		m.TotalTokens = totalTextLen / 4
		if m.Runner != nil {
			m.TotalSessionCost = config.EstimateCost(m.Runner.ModelName(), m.TotalTokens)
		}
	}
}

// matchLocalPaths scans the workspace directory for items matching the prefix.
func (m Model) matchLocalPaths(prefix string) []string {
	if prefix == "" {
		return nil
	}

	// Determine directory and file prefix
	var dir, filePrefix string
	if strings.Contains(prefix, "/") {
		lastSlash := strings.LastIndex(prefix, "/")
		dir = prefix[:lastSlash]
		filePrefix = prefix[lastSlash+1:]
		if dir == "" {
			dir = "/"
		}
	} else {
		dir = "."
		filePrefix = prefix
	}

	// Prevent directory traversal escapes for safety
	cleanDir := filepath.Clean(dir)
	if cleanDir == ".." || strings.HasPrefix(cleanDir, "../") || strings.HasPrefix(cleanDir, "/") {
		// Secure sandbox limit - lock to workspace
		return nil
	}

	entries, err := os.ReadDir(cleanDir)
	if err != nil {
		return nil
	}

	var matches []string
	for _, entry := range entries {
		name := entry.Name()
		// Skip hidden git files and local state dirs unless searching for dotfiles
		if strings.HasPrefix(name, ".") && !strings.HasPrefix(filePrefix, ".") {
			continue
		}

		if strings.HasPrefix(strings.ToLower(name), strings.ToLower(filePrefix)) {
			// Construct match path
			var matchPath string
			if cleanDir == "." {
				matchPath = name
			} else {
				matchPath = filepath.Join(cleanDir, name)
			}

			if entry.IsDir() {
				matchPath += "/"
			}
			matches = append(matches, matchPath)
		}
	}

	return matches
}

// resetPathCompletion clears path auto-completion states.
func (m *Model) resetPathCompletion() {
	m.PathCompletionActive = false
	m.PathCompletionItems = nil
	m.PathCompletionIndex = 0
	m.PathCompletionOriginal = ""
	m.PathCompletionRest = ""
}

// getEditableValue extracts the editable command or content string from active tool arguments.
func (m Model) getEditableValue() string {
	if m.ActiveTool.Args == nil {
		return ""
	}
	if argMap, ok := m.ActiveTool.Args.(map[string]any); ok {
		if cmd, ok := argMap["command"].(string); ok {
			return cmd
		}
		if content, ok := argMap["content"].(string); ok {
			return content
		}
		if path, ok := argMap["path"].(string); ok {
			return path
		}
	}
	return ""
}
