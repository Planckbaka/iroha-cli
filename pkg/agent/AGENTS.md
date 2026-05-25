<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-05-23 | Updated: 2026-05-25 -->

# agent

## Purpose
Core agent orchestration: runner lifecycle, SWE tool definitions (30+ tools), human-in-the-loop permission system, hook pipeline, cross-session memory, prompt builder, task DAG, cron scheduler, background execution, team coordination, protocol handshake, autonomous polling, git worktree isolation, MCP plugin routing, LSP client, multi-agent pool, session persistence, diff generation, CI monitoring, and audit logging.

## Key Files
| File | Description |
|------|-------------|
| `runner.go` | `CustomRunner` — wraps ADK runner, manages async execution, `ConfirmationBridge` channels, `ToolStatusBridge`, `blockingConfirmationTool` wrapper, hook pipeline (PreToolUse → execute → PostToolUse), `ToolCircuitBreaker` (3 consecutive failures → auto-block) |
| `tools.go` | Tool registration and dispatch — registers all 30+ SWE tools with the ADK agent builder, delegates handlers to `tools_*.go` files |
| `tools_file.go` | File tools: `file_read`, `file_write`, `file_edit`, `list_directory`, `search_grep` — path sandboxing, 10MB read limit, edit validation |
| `tools_shell.go` | Shell tool: `shell_run` — command execution with 30s timeout, 500-line stream cap, sandbox validation |
| `tools_mcp.go` | MCP tools: `mcp_server_list` — lists connected MCP plugin servers |
| `tools_memory.go` | Memory tools: `memory_save`, `memory_list` — persistent memory CRUD via `MemoryManager` |
| `tools_schedule.go` | Schedule tools: `schedule_create`, `schedule_list`, `schedule_delete` — cron job management via `CronScheduler` |
| `tools_task.go` | Task tools: `task_create`, `task_update`, `task_list`, `task_get` — DAG task management via `TaskManager` |
| `tools_team.go` | Team tools: `spawn_teammate`, `send_message`, `team_status` — team coordination via `TeamManager` |
| `tools_todo.go` | Todo tool: `todo` — session-level progress planning via `TodoManager` |
| `tools_worktree.go` | Worktree tools: `worktree_create`, `worktree_remove`, `worktree_status` — git worktree isolation via `WorktreeManager` |
| `pool.go` | `AgentPool` — multi-agent runner pool with per-agent LLM config, `GlobalAgentPool` singleton, dynamic runner creation with tool injection |
| `lsp.go` | `LSPClient` — Language Server Protocol client over stdio JSON-RPC 2.0, supports `textDocument/completion`, `textDocument/definition`, `textDocument/references`, `textDocument/hover`, `textDocument/diagnostics` |
| `git_helper.go` | Git utilities: `GitHasChanges`, `GitGetStagedDiff`, `GitGetCurrentBranch` — porcelain helpers for CI/worktree integrations |
| `session_store.go` | `PersistentSessionService` — wraps ADK `session.InMemoryService` with JSON persistence in `~/.iroha/sessions/`, CRUD + fork, session metadata, stale session GC |
| `permission.go` | `PermissionManager` — rule-based allow/deny/ask with bash security validation, three modes (default/plan/auto), path and content pattern matching |
| `hooks.go` | `HookManager` — external hook scripts loaded from `~/.iroha/hooks.json` and `./.iroha/hooks.json`, exit-code protocol (0=continue, 1=block, 2=inject), matcher support |
| `memory.go` | `MemoryManager` — file-based persistent memory with YAML frontmatter, four types (user/feedback/project/reference), two-layer storage (global `~/.iroha/memory/` + project `.iroha/memory/`), `MEMORY.md` index |
| `prompt.go` | `SystemPromptBuilder` — dynamic prompt assembly with cache-friendly stable/dynamic boundary (`=== DYNAMIC_BOUNDARY ===`), CLAUDE.md layering, skill injection, live task/team/worktree context |
| `todo_manager.go` | `TodoManager` — session-level task planning with status tracking (pending/in_progress/completed), max 12 items, nag reminder after 3 rounds without update |
| `task.go` | `TaskManager` — durable work graph (DAG) persisted as JSON files in `.tasks/`, bidirectional edge reconciliation, DFS cycle detection, auto-created placeholder nodes |
| `background.go` | `BackgroundManager` — slow-running shell commands in background goroutines, 5-min timeout, result preview, notification queue for next-turn delivery |
| `cron.go` | `CronScheduler` — 5-field cron expression evaluator, PID-based lock for multi-session safety, durable/session storage, jitter on :00/:30 marks, 7-day auto-expiry, missed-task detection |
| `team.go` | `TeamManager` — persistent specialist teammates with JSONL mailbox inbox, background polling loops, broadcast, `ProcessMessage` callback for LLM integration |
| `protocol.go` | `ProtocolManager` — structured request-response handshake (shutdown/plan_approval) persisted as JSON, single-use pending→approved/rejected lifecycle |
| `autonomous.go` | `AutonomousManager` — task auto-polling and state transitions (WORK/IDLE), keyword-based task claiming for specialist agents |
| `mcp.go` | `MCPClient` + `MCPToolRouter` — stdio-based JSON-RPC 2.0 lifecycle over child processes, dynamic tool discovery and ADK wrapping, plugins loaded from `.iroha/plugins.json` |
| `worktree.go` | `WorktreeManager` — git worktree creation/removal/keep, JSON index + JSONL event log, cascading task status updates on closeout |
| `auto_review.go` | Hybrid safety review for `shell_run`: heuristic rules first, then LLM semantic analysis, then local dangerous-pattern double-check |
| `compaction.go` | Conversation micro-compaction and archival — large tool outputs archived to transcripts, LLM-based conversational summarization (falls back to text extraction when no LLM provided) |
| `diff.go` | LCS-based unified diff generator for file edit previews |
| `ci_watcher.go` | GitHub Actions CI status monitoring via `gh` CLI |
| `logger.go` | Dual JSONL + plaintext audit logger with secret redaction |

## For AI Agents

### Working In This Directory
- Global singletons: `GlobalPermissionManager`, `GlobalHookManager`, `GlobalMemoryManager`, `GlobalTodoManager`, `GlobalTaskManager`, `GlobalBackgroundManager`, `GlobalCronScheduler`, `GlobalTeamManager`, `GlobalProtocolManager`, `GlobalAutonomyManager`, `GlobalWorktreeManager`, `GlobalMCPRouter`, `GlobalToolCircuitBreaker`, `GlobalAgentPool`
- `ConfirmationBridge` is the async channel between runner (goroutine) and TUI (main thread): `PromptChan`/`ResponseChan`
- `ToolStatusBridge` provides real-time tool status to TUI via `StatusChan` with background drain worker
- `blockingConfirmationTool` wraps every tool to intercept and confirm before execution
- `SystemPromptBuilder` assembles the system instruction with a caching boundary
- `ToolCircuitBreaker` halts after 3 consecutive identical-arg failures on the same tool

### Testing Requirements
- `go test ./pkg/agent/...`
- Tests exist for: hooks, memory, permission, todo_manager, autonomous, background, cron, mcp, protocol, task, team, worktree, prompt, auto_review, compaction, diff, ci_watcher, logger, session_store, runner, git_helper, lsp, pool, error_recovery
- **Gap**: `tools.go` and `tools_*.go` have no dedicated test files

### Common Patterns
- Mutex-protected global singletons (`sync.RWMutex`)
- Tool handlers follow `func(ctx tool.Context, args T) (R, error)` signature via `functiontool.New()`
- Hook config uses two-layer merge (global `~/.iroha/` + project `.iroha/`)
- Memory files use YAML frontmatter with auto-generated `MEMORY.md` index
- DAG edge reconciliation is bidirectional with auto-unblocking cascade
- MCP tools are dynamically discovered and wrapped as `DynamicMCPTool` implementing `tool.Tool`
- Config path: `~/.iroha/` (auto-migrates from legacy `~/.go-claude/`)

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
