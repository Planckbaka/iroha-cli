<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-05-23 | Updated: 2026-05-25 -->

# tui

## Purpose
Terminal UI built with Bubble Tea: prompt input, streaming output rendering, human-in-the-loop confirmation cards, slash commands with fuzzy filtering, session picker, permission selection, history navigation, markdown rendering, and diagnostic dashboard.

## Key Files
| File | Description |
|------|-------------|
| `model.go` | `Model` — Bubble Tea model with 6 states (prompt/thinking/streaming/confirming/permission_select/session_select), 17 slash commands, async agent event routing, turn finalization |
| `update_keys.go` | Keyboard handler (`Update` for `tea.KeyMsg`) — input, slash commands, Ctrl+C/S/D, PgUp/PgDn scrolling, resize |
| `update_msgs.go` | Message handler (`handleCustomMsg`) — processes agent events (`StreamTextMsg`, `ConfirmationRequiredMsg`, `ToolStatusMsg`, `AgentErrorMsg`, `AgentDoneMsg`), spinner updates |
| `view.go` | `RenderMarkdown`, `RenderConfirmCard`, `RenderWelcomeCard`, `RenderErrorCard`, `RenderTodoDashboard`, `RenderTaskDashboard`, `RenderTaskDetails`, `RenderTeamDashboard`, `RenderWorktreeDashboard`, `RenderMCPDashboard` — view rendering functions |
| `styles.go` | Lipgloss color palette (cyber-holographic: electric cyan + neon pink) and style definitions |
| `input.go` | `HistoryManager` (Up/Down arrow navigation), `SetupTextInput` — prompt input initialization |
| `doctor.go` | `RunDoctor` — environment diagnostic dashboard (checks config, API keys, tools, git) |

## For AI Agents

### Working In This Directory
- State machine: `statePrompt` → `stateThinking` → `stateStreaming` → back to `statePrompt`
- `stateConfirming` interrupts streaming for tool approval (y/n/a)
- `statePermissionSelect` for full-screen permission mode selection at startup
- `stateSessionSelect` for session resume/fork picker
- Slash commands (17): `/permission`, `/hooks`, `/memory`, `/prompt`, `/sections`, `/task`, `/team`, `/worktree`, `/mcp`, `/bg`, `/sessions`, `/help`, `/doctor`, `/exit`, `/quit`, `/mode`, `/rules`
- `ProgramRef` pattern solves the circular reference between `tea.Program` and `Model`
- `ConfirmationRequiredMsg` received from `agent.Bridge.PromptChan` (async)
- Shell output streaming with 100ms throttling
- Dynamic textarea auto-scaling (2-6 lines)

### Testing Requirements
- `go test ./pkg/tui/...`
- Tests exist for render helpers (149 lines)
- **Gap**: No tests for the Update message cycle or state transitions

### Common Patterns
- Custom message types: `StreamTextMsg`, `ConfirmationRequiredMsg`, `ToolStatusMsg`, `AgentErrorMsg`, `AgentDoneMsg`, `DoctorResultMsg`, `StartupPromptMsg`
- `listenToConfirmationBridge()` returns a `tea.Cmd` that blocks on a channel
- Chinese-language UI strings (prompts, placeholders, help text)
- Two channel bridges: `ConfirmationBridge` (y/n/always) and `ToolStatusBridge` (real-time status)

## Dependencies

### Internal
- `pkg/agent` — `CustomRunner`, `Bridge`, `GlobalPermissionManager`, `GlobalHookManager`, `GlobalMemoryManager`, `GlobalTodoManager`, `GlobalTaskManager`, `GlobalTeamManager`, `GlobalWorktreeManager`, `GlobalMCPRouter`

### External
- `github.com/charmbracelet/bubbletea` — Elm-architecture TUI framework
- `github.com/charmbracelet/bubbles` — Spinner, textinput, viewport components
- `github.com/charmbracelet/lipgloss` — Terminal styling
- `github.com/charmbracelet/glamour` — ANSI markdown rendering
- `google.golang.org/adk/session` — Event type

<!-- MANUAL: -->
