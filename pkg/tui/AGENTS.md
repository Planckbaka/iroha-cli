<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-05-20 | Updated: 2026-05-20 -->

# tui

## Purpose
Terminal UI built with Bubble Tea: prompt input, streaming output rendering, human-in-the-loop confirmation cards, slash commands, and history navigation.

## Key Files
| File | Description |
|------|-------------|
| `model.go` | `Model` — Bubble Tea model with states (prompt/thinking/streaming/confirming), slash command handling, async agent event routing |
| `view.go` | `RenderMarkdown`, `RenderConfirmCard`, `RenderWelcomeCard`, `RenderErrorCard`, `RenderTodoDashboard`, `RenderTaskDashboard`, `RenderTaskDetails`, `RenderTeamDashboard`, `RenderWorktreeDashboard`, `RenderMCPDashboard` — view rendering functions |
| `styles.go` | Lipgloss color palette (Emerald/Amber/Coral theme) and style definitions |
| `input.go` | `HistoryManager` (Up/Down arrow navigation), `SetupTextInput` — prompt input initialization |

## For AI Agents

### Working In This Directory
- State machine: `statePrompt` → `stateThinking` → `stateStreaming` → back to `statePrompt`
- `stateConfirming` interrupts streaming for tool approval (y/n/a)
- Slash commands: `/exit`, `/quit`, `/mode <plan|auto|default>`, `/rules`, `/hooks [reload]`, `/memory`
- `ProgramRef` pattern solves the circular reference between `tea.Program` and `Model`
- `ConfirmationRequiredMsg` received from `agent.Bridge.PromptChan` (async)

### Testing Requirements
- Manual testing via running the CLI
- No unit tests currently

### Common Patterns
- Custom message types: `StreamTextMsg`, `ConfirmationRequiredMsg`, `AgentErrorMsg`, `AgentDoneMsg`
- `listenToConfirmationBridge()` returns a `tea.Cmd` that blocks on a channel
- Chinese-language UI strings (prompts, placeholders, help text)

## Dependencies

### Internal
- `pkg/agent` — `CustomRunner`, `Bridge`, `GlobalPermissionManager`, `GlobalHookManager`, `GlobalMemoryManager`, `GlobalTodoManager`, `GlobalTaskManager`, `GlobalTeamManager`, `GlobalWorktreeManager`, `GlobalMCPRouter`

### External
- `github.com/charmbracelet/bubbletea` — Elm-architecture TUI framework
- `github.com/charmbracelet/bubbles` — Spinner, textinput components
- `github.com/charmbracelet/lipgloss` — Terminal styling
- `github.com/charmbracelet/glamour` — ANSI markdown rendering
- `google.golang.org/adk/session` — Event type

<!-- MANUAL: -->
