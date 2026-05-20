<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-05-20 | Updated: 2026-05-20 -->

# pkg

## Purpose
Core library packages implementing the agent system, LLM integration, configuration, and terminal UI.

## Subdirectories
| Directory | Purpose |
|-----------|---------|
| `agent/` | Agent runner, tools, permissions, hooks, memory, todo (see `agent/AGENTS.md`) |
| `config/` | Configuration loading and interactive setup wizard (see `config/AGENTS.md`) |
| `llm/` | LLM provider adapters (GLM-4, OpenAI, simulate) (see `llm/AGENTS.md`) |
| `tui/` | Bubble Tea terminal UI (see `tui/AGENTS.md`) |

## For AI Agents

### Working In This Directory
- All packages use the `go-claude/pkg/<name>` import path
- Packages communicate via exported interfaces and global singletons (`Global*`)
- Test files are colocated with source (`*_test.go`)

### Testing Requirements
- `go test ./pkg/...` runs all tests
- Each package is independently testable

### Common Patterns
- Global singletons: `GlobalPermissionManager`, `GlobalHookManager`, `GlobalMemoryManager`, `GlobalTodoManager`, `GlobalTaskManager`, `GlobalBackgroundManager`, `GlobalCronScheduler`, `GlobalTeamManager`, `GlobalProtocolManager`, `GlobalAutonomyManager`, `GlobalWorktreeManager`, `GlobalMCPRouter`
- Channel-based bridge for async TUI ↔ Agent communication
- Chinese-language system prompts and error messages

## Dependencies

### Internal
- All `pkg/` packages may import each other through the `agent` package as the orchestrator
- `agent` → `llm` (model adapter)
- `tui` → `agent` (runner, bridge, permission manager)
- `cmd/agent-cli` → all `pkg/` packages

<!-- MANUAL: -->
