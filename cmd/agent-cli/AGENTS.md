<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-05-20 | Updated: 2026-05-20 -->

# agent-cli

## Purpose
Primary CLI entry point. Resolves configuration (flags > env vars > config file > wizard), initializes the agent runner, and launches the Bubble Tea TUI program.

## Key Files
| File | Description |
|------|-------------|
| `main.go` | Binary entry point — flag parsing, config resolution, runner init, TUI launch |

## For AI Agents

### Working In This Directory
- This is the only file that ties all `pkg/` packages together
- Config priority: CLI flags > environment variables > `~/.go-claude.json` > interactive wizard
- Supported env vars: `ZHIPU_API_KEY`, `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `GEMINI_API_KEY`
- The `--config` flag forces the interactive setup wizard

### Testing Requirements
- No unit tests here; tested via integration/manual testing

## Dependencies

### Internal
- `go-claude/pkg/agent` — Runner creation
- `go-claude/pkg/config` — Config loading and wizard
- `go-claude/pkg/llm` — Provider type constants
- `go-claude/pkg/tui` — TUI model and program

<!-- MANUAL: -->
