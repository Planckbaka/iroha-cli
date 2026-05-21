<!-- Generated: 2026-05-20 | Updated: 2026-05-20 -->

# iroha-code

## Purpose
An interactive AI Agent CLI built in Go, powered by Zhipu GLM-4 / OpenAI-compatible LLMs with a Bubble Tea TUI, human-in-the-loop tool-use permissions, cross-session memory, hook system, and task planning. It is designed as a Claude Code-inspired agent for the terminal.

## Key Files
| File | Description |
|------|-------------|
| `go.mod` | Go module definition (go 1.26.1, Charm stack, Google ADK/GenAI) |
| `go.sum` | Dependency checksums |
| `.gitignore` | Excludes binary (`/iroha`), `.omc/`, `.iroha/`, `scratch/` |

## Subdirectories
| Directory | Purpose |
|-----------|---------|
| `cmd/` | Application entry points (see `cmd/AGENTS.md`) |
| `pkg/` | Core library packages (see `pkg/AGENTS.md`) |

## For AI Agents

### Working In This Directory
- Run `go build -o iroha ./cmd/agent-cli` to compile the binary
- Run `go test ./...` to execute all tests
- The binary output is `./iroha` at repo root
- Config is stored at `~/.iroha.json` (outside repo)
- Project-local state lives in `./.iroha/` (gitignored)

### Testing Requirements
- Unit tests live alongside source files (`*_test.go`)
- Run `go test ./pkg/...` for all package tests

### Common Patterns
- Standard Go project layout: `cmd/` for binaries, `pkg/` for libraries
- Google ADK (`google.golang.org/adk`) for agent framework
- Charm stack (Bubble Tea, Lipgloss, Glamour, Bubbles) for TUI
- Chinese-language user-facing strings throughout

## Dependencies

### External
- `github.com/charmbracelet/bubbletea` v1.3.10 — TUI framework
- `github.com/charmbracelet/lipgloss` v1.1.1 — Terminal styling
- `github.com/charmbracelet/glamour` v1.0.0 — Markdown rendering
- `github.com/charmbracelet/bubbles` v1.0.0 — TUI components
- `google.golang.org/adk` v1.2.1 — Agent development kit
- `google.golang.org/genai` v1.57.0 — Generative AI types

<!-- MANUAL: Custom project notes can be added below -->
