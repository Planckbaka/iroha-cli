package tui

import (
	"context"
	"strings"
	"time"

	"iroha/pkg/agent"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

// handleCustomMsg processes custom agent and spinner events and returns (updatedModel, cmd, handled)
func (m Model) handleCustomMsg(msg tea.Msg) (Model, tea.Cmd, bool) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case StartupPromptMsg:
		if msg.Prompt == "" {
			return m, nil, true
		}
		// Record in history
		m.HistoryManager.Add(msg.Prompt)

		m.CurrentPrompt = msg.Prompt
		m.StreamedText = ""
		m = m.transitionTo(stateThinking)
		m.TextArea.SetValue("")
		m.TextArea.SetHeight(2)

		m.RoundCount++
		m.RoundStartTime = time.Now()
		m.ActiveTool = agent.ToolStatus{}

		ctx, cancel := context.WithCancel(context.Background())
		m.Ctx = ctx
		m.Cancel = cancel

		m.Runner.Execute(m.Ctx, "user-dev", m.SessionID, m.CurrentPrompt,
			m.OnEvent, m.OnError, m.OnDone,
		)
		return m, m.Spinner.Tick, true

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
		return m, nil, true

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
			return m, m.listenToToolBridge(), true
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
				logLine = "\n" + RenderToolSuccessCard(status.Name, status.Args, status.Duration) + "\n"
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
		return m, m.listenToToolBridge(), true

	case ConfirmationRequiredMsg:
		m = m.transitionTo(stateConfirming)
		m.ConfirmSelectIndex = 0
		m.ConfirmationListenerActive = false

		// Extract Unified Diff if present in prompt to avoid massive bloat in simple confirmation cards
		const diffMarker = "\n\n\x1b[1;34m[文件变更差异 (Diff)]:\x1b[0m\n"
		if idx := strings.Index(msg.Prompt, diffMarker); idx != -1 {
			m.ConfirmationPrompt = msg.Prompt[:idx]
			m.ConfirmDiffText = msg.Prompt[idx+len(diffMarker):]
			m.ConfirmDiffActive = false
		} else {
			altMarker := "\n\n\x1b[1;34m[文件变更差异 (Diff)]:\x1b[0m"
			if idx := strings.Index(msg.Prompt, altMarker); idx != -1 {
				m.ConfirmationPrompt = msg.Prompt[:idx]
				m.ConfirmDiffText = msg.Prompt[idx+len(altMarker):]
				m.ConfirmDiffActive = false
			} else {
				m.ConfirmationPrompt = msg.Prompt
				m.ConfirmDiffText = ""
				m.ConfirmDiffActive = false
			}
		}

		m.Viewport.SetContent(m.renderViewportContent())
		m.Viewport.GotoBottom()
		return m, nil, true

	case DoctorResultMsg:
		m = m.transitionTo(statePrompt)
		m.ActiveTool = agent.ToolStatus{}
		m.History = append(m.History, msg.Report)
		m.Viewport.SetContent(m.renderViewportContent())
		m.Viewport.GotoBottom()
		return m, nil, true

	case AgentErrorMsg:
		m.LastError = msg.Err
		cmd = m.finalizeTurn()
		return m, cmd, true

	case AgentDoneMsg:
		cmd = m.finalizeTurn()
		return m, cmd, true

	case spinner.TickMsg:
		m.Spinner, cmd = m.Spinner.Update(msg)
		if m.State == stateThinking {
			m.Viewport.SetContent(m.renderViewportContent())
		}
		return m, cmd, true
	}

	return m, nil, false
}
