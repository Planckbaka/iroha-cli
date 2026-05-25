<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-05-23 | Updated: 2026-05-25 -->

# pkg

## Purpose
Core library packages implementing the agent system, LLM integration (7 providers), configuration, and terminal UI.

## Subdirectories
| Directory | Purpose |
|-----------|---------|
| `agent/` | Agent runner, 30+ tools, permissions, hooks, memory, task DAG, team, MCP (see `agent/AGENTS.md`) |
| `config/` | Configuration loading, 7-provider defaults, cost estimation, interactive wizard (see `config/AGENTS.md`) |
| `llm/` | LLM provider adapters — OpenAI-compatible, Anthropic, Genkit (see `llm/AGENTS.md`) |
| `tui/` | Bubble Tea terminal UI with 6 states, 17 slash commands, doctor (see `tui/AGENTS.md`) |

## For AI Agents

### Working In This Directory
- All packages use the `go-claude/pkg/<name>` import path
- Packages communicate via exported interfaces and global singletons (`Global*`)
- Test files are colocated with source (`*_test.go`)
- Config path: `~/.iroha/` (auto-migrates from legacy `~/.go-claude/`)

### Testing Requirements
- `go test ./pkg/...` runs all tests
- Each package is independently testable
- Total: ~3,633 test lines across 23 test files

### Common Patterns
- Global singletons: `GlobalPermissionManager`, `GlobalHookManager`, `GlobalMemoryManager`, `GlobalTodoManager`, `GlobalTaskManager`, `GlobalBackgroundManager`, `GlobalCronScheduler`, `GlobalTeamManager`, `GlobalProtocolManager`, `GlobalAutonomyManager`, `GlobalWorktreeManager`, `GlobalMCPRouter`
- Channel-based bridges for async TUI ↔ Agent communication (`ConfirmationBridge`, `ToolStatusBridge`)
- Chinese-language system prompts and error messages
- Mutex-protected concurrent access (`sync.RWMutex`)

## Dependencies

### Internal
- All `pkg/` packages may import each other through the `agent` package as the orchestrator
- `agent` → `llm` (model adapter)
- `tui` → `agent` (runner, bridge, permission manager)
- `cmd/agent-cli` → all `pkg/` packages

<!-- MANUAL: -->
