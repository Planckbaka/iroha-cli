<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-05-23 | Updated: 2026-05-23 -->

# config

## Purpose
Configuration management: loading from `~/.iroha.json`, saving, provider defaults for 7 LLM providers, cost estimation, interactive terminal wizard for first-time setup, and auto-migration from legacy `~/.go-claude.json`.

## Key Files
| File | Description |
|------|-------------|
| `config.go` | `Config` struct, `LoadConfig`, `SaveConfig`, `RunConfigWizard`, `EstimateCost`, provider defaults map |

## For AI Agents

### Working In This Directory
- Config file path: `~/.iroha.json` (auto-migrates from legacy `~/.go-claude.json`)
- 7 providers: `glm` (Zhipu), `openai`, `claude`, `deepseek`, `kimi`, `siliconflow`, `gemini`
- Each provider has defaults: model name, base URL, env key name
- API format support: `openai` and `anthropic` protocols per provider
- Wizard masks API key display (shows first 4 + "...." + last 4 chars)
- File permissions set to `0600` for security
- `EstimateCost` uses model-specific pricing with 85/15 input/output ratio heuristic
- Cost estimation supports models: glm-4-flash (free), glm-4-plus, gpt-4o, claude-3.5-sonnet, deepseek-chat, etc.

### Testing Requirements
- `go test ./pkg/config/...`
- Tests exist for config loading and wizard logic

### Common Patterns
- Struct tags: `json:"api_key"` for JSON serialization
- Wizard uses `bufio.NewReader(os.Stdin)` for interactive input
- Priority chain: CLI flags > environment variables > config file > wizard > provider defaults
- Supported env vars: `ZHIPU_API_KEY`, `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `GEMINI_API_KEY`, `DEEPSEEK_API_KEY`, `MOONSHOT_API_KEY` (Kimi), `SILICONFLOW_API_KEY`

## Dependencies

### External
- Standard library only (`encoding/json`, `os`, `bufio`, `fmt`, `math`)

<!-- MANUAL: -->
