<!-- Parent: ../AGENTS.md -->
<!-- Generated: 2026-05-20 | Updated: 2026-05-20 -->

# config

## Purpose
Configuration management: loading from `~/.go-claude.json`, saving, and an interactive terminal wizard for first-time setup.

## Key Files
| File | Description |
|------|-------------|
| `config.go` | `Config` struct (provider, model, api_key, base_url), `LoadConfig`, `SaveConfig`, `RunConfigWizard` |

## For AI Agents

### Working In This Directory
- Config file path: `~/.go-claude.json`
- Default provider is `simulate` (offline mode, no API key needed)
- Wizard masks API key display (shows first 4 + "...." + last 4 chars)
- File permissions set to `0600` for security

### Common Patterns
- Struct tags: `json:"api_key"` for JSON serialization
- Wizard uses `bufio.NewReader(os.Stdin)` for interactive input
- Supports providers: `simulate`, `glm` (Zhipu), `openai` (and compatibles)

## Dependencies

### External
- Standard library only (`encoding/json`, `os`, `bufio`, `fmt`)

<!-- MANUAL: -->
