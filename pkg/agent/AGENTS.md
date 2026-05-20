<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-05-20 | Updated: 2026-05-20 -->

# agent

## Purpose
Core agent orchestration: runner lifecycle, SWE tool definitions, human-in-the-loop permission system, hook pipeline, cross-session memory, and task planning.

## Key Files
| File | Description |
|------|-------------|
| `runner.go` | `CustomRunner` — wraps ADK runner, manages async execution, tool confirmation via `ConfirmationBridge`, hook pipeline (PreToolUse → execute → PostToolUse) |
| `tools.go` | SWE tool definitions (`file_read`, `file_write`, `search_grep`, `shell_run`, `todo`, `memory_save`, `memory_list`) and their handlers |
| `permission.go` | `PermissionManager` — rule-based allow/deny/ask with bash security validation, three modes (default/plan/auto) |
| `hooks.go` | `HookManager` — external hook scripts loaded from `~/.go-claude/hooks.json` and `./.go-claude/hooks.json`, exit-code protocol (0=continue, 1=block, 2=inject) |
| `memory.go` | `MemoryManager` — file-based persistent memory with YAML frontmatter, four types (user/feedback/project/reference), two-layer storage (global + project) |
| `todo_manager.go` | `TodoManager` — session-level task planning with status tracking (pending/in_progress/completed), max 12 items, nag reminder after 3 rounds without update |

## For AI Agents

### Working In This Directory
- Global singletons: `GlobalPermissionManager`, `GlobalHookManager`, `GlobalMemoryManager`, `GlobalTodoManager`
- `ConfirmationBridge` is the async channel between runner (goroutine) and TUI (main thread)
- `blockingConfirmationTool` wraps every tool to intercept and confirm before execution
- System instruction is assembled in `NewCustomRunner` with memory injection

### Testing Requirements
- `go test ./pkg/agent/...`
- Tests exist for: hooks, memory, permission, todo_manager

### Common Patterns
- Mutex-protected global singletons (`sync.RWMutex`)
- Tool handlers follow `func(ctx tool.Context, args T) (R, error)` signature
- Hook config uses two-layer merge (global + project)
- Memory files use YAML frontmatter with auto-generated `MEMORY.md` index

## Dependencies

### Internal
- `pkg/llm` — Model adapter (`llm.NagReminderTrigger`, `llm.NoteRoundWithoutUpdate` callbacks)

### External
- `google.golang.org/adk/agent` — Agent framework
- `google.golang.org/adk/agent/llmagent` — LLM agent builder
- `google.golang.org/adk/tool` / `functiontool` — Tool system
- `google.golang.org/adk/runner` — Agent runner
- `google.golang.org/adk/session` — Session management
- `google.golang.org/genai` — Generative AI types

<!-- MANUAL: -->
