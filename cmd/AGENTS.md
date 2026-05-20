<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-05-20 | Updated: 2026-05-20 -->

# cmd

## Purpose
Application entry points. Each subdirectory is a standalone binary with its own `main()` function.

## Subdirectories
| Directory | Purpose |
|-----------|---------|
| `agent-cli/` | Primary CLI binary (see `agent-cli/AGENTS.md`) |

## For AI Agents

### Working In This Directory
- Each subdirectory produces one binary via `go build ./cmd/<name>`
- Keep `main.go` files thin — delegate to `pkg/` packages

### Common Patterns
- Flag parsing for provider, model, API key, base URL
- Config file resolution with CLI flag override hierarchy

<!-- MANUAL: -->
