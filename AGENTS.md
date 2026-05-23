<!-- Generated: 2026-05-23 | Updated: 2026-05-23 -->

# iroha-code

## Purpose
An interactive AI Agent CLI built in Go, powered by 7 LLM providers (GLM, OpenAI, Claude, DeepSeek, Kimi, SiliconFlow, Gemini) with a Bubble Tea TUI, human-in-the-loop tool-use permissions, hook system, cross-session memory, task DAG planning, team coordination, MCP plugin routing, and autonomous execution. Designed as a Claude Code-inspired agent for the terminal.

## Key Files
| File | Description |
|------|-------------|
| `go.mod` | Go module definition (go 1.26.1, Charm stack, Google ADK/GenAI, Firebase Genkit) |
| `go.sum` | Dependency checksums |
| `.gitignore` | Excludes binary (`/iroha`), `.omc/`, `.iroha/`, `scratch/` |
| `system_prompt.md` | Default system prompt template for the agent |
| `.golangci.yml` | Linter config (errcheck, govet, revive, staticcheck) |
| `.goreleaser.yml` | GoReleaser build and release configuration |
| `install.sh` | Installation script |

## Subdirectories
| Directory | Purpose |
|-----------|---------|
| `cmd/` | Application entry points (see `cmd/AGENTS.md`) |
| `pkg/` | Core library packages (see `pkg/AGENTS.md`) |
| `docs/` | Project documentation and analysis |
| `.github/` | CI workflows, issue/PR templates |
| `scratch/` | Debug and experimental scripts |

## For AI Agents

### Working In This Directory
- Run `go build -o iroha ./cmd/agent-cli` to compile the binary
- Run `go test ./...` to execute all tests
- The binary output is `./iroha` at repo root
- Config is stored at `~/.iroha.json` (outside repo)
- Project-local state lives in `./.iroha/` (gitignored)
- Auto-migrates from legacy `~/.go-claude.json` path

### Testing Requirements
- Unit tests live alongside source files (`*_test.go`)
- Run `go test ./pkg/...` for all package tests
- Test coverage: ~25% (3,633 test lines / ~16,000 source lines)
- Key gaps: `runner.go` has no dedicated tests, `glm_test.go` is empty

### Common Patterns
- Standard Go project layout: `cmd/` for binaries, `pkg/` for libraries
- Google ADK (`google.golang.org/adk`) for agent framework
- Firebase Genkit (`github.com/firebase/genkit/go`) for Gemini/Claude SDK bridging
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
- `github.com/firebase/genkit/go` — Firebase Genkit Go SDK

<!-- MANUAL: Custom project notes can be added below -->
