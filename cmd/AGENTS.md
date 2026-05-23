<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-05-23 | Updated: 2026-05-23 -->

# cmd

## Purpose
Application entry points. Each subdirectory is a standalone binary with its own `main()` function.

## Subdirectories
| Directory | Purpose |
|-----------|---------|
| `agent-cli/` | Primary CLI binary — config resolution, runner init, TUI launch (see `agent-cli/AGENTS.md`) |

## For AI Agents

### Working In This Directory
- Each subdirectory produces one binary via `go build -o iroha ./cmd/agent-cli`
- Keep `main.go` files thin — delegate to `pkg/` packages
- The main.go in agent-cli is ~203 lines, all orchestration logic is in `pkg/`

### Testing Requirements
- No unit tests for entry points; tested via integration/manual testing
- Build verification: `go build -o /dev/null ./cmd/agent-cli`

### Common Patterns
- Flag parsing for provider, model, API key, base URL, API format, session, permission mode
- Config file resolution with CLI flag > env var > config file > wizard priority chain
- Auto-migration from legacy `~/.go-claude.json` to `~/.iroha.json`

<!-- MANUAL: -->
