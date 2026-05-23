<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-05-23 | Updated: 2026-05-23 -->

# agent-cli

## Purpose
Primary CLI entry point. Resolves configuration (flags > env vars > config file > wizard), initializes the agent runner with LLM adapter and 30+ tools, and launches the Bubble Tea TUI program with alt screen and mouse support.

## Key Files
| File | Description |
|------|-------------|
| `main.go` | Binary entry point — flag parsing, config resolution, runner init, TUI launch (~203 lines) |

## For AI Agents

### Working In This Directory
- This is the only file that ties all `pkg/` packages together
- Config priority: CLI flags > environment variables > `~/.iroha.json` > interactive wizard > provider defaults
- Supported env vars: `ZHIPU_API_KEY`, `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `GEMINI_API_KEY`, `DEEPSEEK_API_KEY`, `MOONSHOT_API_KEY`, `SILICONFLOW_API_KEY`
- Key flags: `--provider`, `--model`, `--apikey`, `--baseurl`, `--api-format`, `--config`, `--resume`, `--last`, `--session`, `--fork`, `--yes`, `--plan`, `--default`
- The `--config` flag forces the interactive setup wizard
- `--yes` sets auto-permission mode, `--plan` sets plan-only mode
- Auto-migrates config from legacy `~/.go-claude.json`
- Initializes Genkit only for Gemini/Claude providers (not needed for OpenAI-compatible)
- Configures global singletons and starts CronScheduler before TUI launch

### Testing Requirements
- No unit tests here; tested via integration/manual testing

## Dependencies

### Internal
- `go-claude/pkg/agent` — Runner creation
- `go-claude/pkg/config` — Config loading and wizard
- `go-claude/pkg/llm` — Provider type constants, adapter creation
- `go-claude/pkg/tui` — TUI model and program

<!-- MANUAL: -->
