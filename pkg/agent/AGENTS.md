<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-05-20 | Updated: 2026-05-20 -->

# agent

## Purpose
Core agent orchestration: runner lifecycle, SWE tool definitions (30+ tools), human-in-the-loop permission system, hook pipeline, cross-session memory, prompt builder, task DAG, cron scheduler, background execution, team coordination, protocol handshake, autonomous polling, git worktree isolation, and MCP plugin routing.

## Key Files
| File | Description |
|------|-------------|
| `runner.go` | `CustomRunner` — wraps ADK runner, manages async execution, tool confirmation via `ConfirmationBridge`, hook pipeline (PreToolUse → execute → PostToolUse), circuit breaker |
| `tools.go` | 30+ SWE tool definitions and handlers: `file_read`, `file_write`, `search_grep`, `shell_run`, `todo`, `memory_save/list`, `task_create/update/list/get`, `background_run/check`, `schedule_create/list/delete`, team/protocol/autonomous/worktree/MCP tools |
| `permission.go` | `PermissionManager` — rule-based allow/deny/ask with bash security validation, three modes (default/plan/auto) |
| `hooks.go` | `HookManager` — external hook scripts loaded from `~/.go-claude/hooks.json` and `./.go-claude/hooks.json`, exit-code protocol (0=continue, 1=block, 2=inject) |
| `memory.go` | `MemoryManager` — file-based persistent memory with YAML frontmatter, four types (user/feedback/project/reference), two-layer storage (global + project) |
| `todo_manager.go` | `TodoManager` — session-level task planning with status tracking (pending/in_progress/completed), max 12 items, nag reminder after 3 rounds without update |
| `prompt.go` | `SystemPromptBuilder` — dynamic prompt assembly with cache-friendly stable/dynamic boundary, CLAUDE.md layering, skill injection, live task/team/worktree context |
| `task.go` | `TaskManager` — durable work graph (DAG) persisted as JSON files, bidirectional edge reconciliation, DFS cycle detection, auto-created placeholder nodes |
| `background.go` | `BackgroundManager` — slow-running shell commands in background goroutines, 5-min timeout, result preview, notification queue for next-turn delivery |
| `cron.go` | `CronScheduler` — 5-field cron expression evaluator, PID-based lock for multi-session safety, durable/session storage, jitter on :00/:30 marks, 7-day auto-expiry, missed-task detection |
| `team.go` | `TeamManager` — persistent specialist teammates with JSONL mailbox inbox, background polling loops, broadcast, `ProcessMessage` callback for LLM integration |
| `protocol.go` | `ProtocolManager` — structured request-response handshake (shutdown/plan_approval) persisted as JSON files, single-use pending→approved/rejected lifecycle |
| `autonomous.go` | `AutonomousManager` — task auto-polling and state transitions (WORK/IDLE), keyword-based task claiming for specialist agents |
| `mcp.go` | `MCPClient` + `MCPToolRouter` — stdio-based JSON-RPC 2.0 lifecycle over child processes, dynamic tool discovery and ADK wrapping, plugins loaded from `.go-claude/plugins.json` |
| `worktree.go` | `WorktreeManager` — git worktree creation/removal/keep, JSON index + JSONL event log, cascading task status updates on closeout |

## For AI Agents

### Working In This Directory
- Global singletons: `GlobalPermissionManager`, `GlobalHookManager`, `GlobalMemoryManager`, `GlobalTodoManager`, `GlobalTaskManager`, `GlobalBackgroundManager`, `GlobalCronScheduler`, `GlobalTeamManager`, `GlobalProtocolManager`, `GlobalAutonomyManager`, `GlobalWorktreeManager`, `GlobalMCPRouter`, `GlobalToolCircuitBreaker`
- `ConfirmationBridge` is the async channel between runner (goroutine) and TUI (main thread)
- `blockingConfirmationTool` wraps every tool to intercept and confirm before execution
- `SystemPromptBuilder` assembles the system instruction with a caching boundary (`=== DYNAMIC_BOUNDARY ===`)
- `ToolCircuitBreaker` halts after 3 consecutive identical-arg failures on the same tool

### Testing Requirements
- `go test ./pkg/agent/...`
- Tests exist for: hooks, memory, permission, todo_manager, autonomous, background, cron, mcp, protocol, task, team, worktree, prompt

### Common Patterns
- Mutex-protected global singletons (`sync.RWMutex`)
- Tool handlers follow `func(ctx tool.Context, args T) (R, error)` signature
- Hook config uses two-layer merge (global + project)
- Memory files use YAML frontmatter with auto-generated `MEMORY.md` index
- DAG edge reconciliation is bidirectional with auto-unblocking cascade
- MCP tools are dynamically discovered and wrapped as `DynamicMCPTool` implementing `tool.Tool`

## Dependencies

### Internal
- `pkg/llm` — Model adapter (`llm.NagReminderTrigger`, `llm.NoteRoundWithoutUpdate`, `llm.SystemPromptTrigger` callbacks)

### External
- `google.golang.org/adk/agent` — Agent framework
- `google.golang.org/adk/agent/llmagent` — LLM agent builder
- `google.golang.org/adk/tool` / `functiontool` — Tool system
- `google.golang.org/adk/runner` — Agent runner
- `google.golang.org/adk/session` — Session management
- `google.golang.org/genai` — Generative AI types
- `github.com/google/uuid` — Unique ID generation (background tasks, cron jobs)

<!-- MANUAL: -->
